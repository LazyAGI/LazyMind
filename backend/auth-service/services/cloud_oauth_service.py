import secrets
import threading
import uuid
import json
from dataclasses import dataclass
from datetime import datetime, timedelta, timezone
from typing import Any

from sqlalchemy.exc import IntegrityError

from core.cloud_crypto import decrypt_json, encrypt_json
from core.database import SessionLocal
from core.errors import AppException, ErrorCodes, app_exception_from_exception, raise_error
from repositories import CloudAuthConnectionRepository
from services.cloud_oauth_provider import (
    CloudAccountProfile,
    CloudOAuthProvider,
    CloudProviderError,
    CloudTokenPayload,
)
from services.mail_providers import MAIL_IMAP_PROVIDERS, is_mail_imap_provider
from services.providers import (
    FeishuOAuthProvider,
    GitHubOAuthProvider,
    GoogleDriveOAuthProvider,
    IMAPMailProvider,
    NotionOAuthProvider,
    WeChatProvider,
)


_AUTH_MODES = {'tenant', 'oauth_user', 'service_account'}
_OAUTH_APP_AUTH_MODE = 'oauth_app'
_TOKEN_REFRESH_BUFFER_SECONDS = 300
_OAUTH_STATE_TTL_MINUTES = 10
_WECHAT_PROVIDER = 'wechat'


@dataclass
class _TokenCacheItem:
    provider: str
    access_token: str
    token_type: str
    expires_at: datetime | None


def _utcnow() -> datetime:
    return datetime.now(timezone.utc)


def _iso(dt: datetime | None) -> str:
    if dt is None:
        return ''
    if dt.tzinfo is None:
        dt = dt.replace(tzinfo=timezone.utc)
    return dt.astimezone(timezone.utc).isoformat()


def _parse_dt(raw: Any) -> datetime | None:
    if not raw:
        return None
    if isinstance(raw, datetime):
        dt = raw
    elif isinstance(raw, str):
        try:
            dt = datetime.fromisoformat(raw.replace('Z', '+00:00'))
        except ValueError:
            return None
    else:
        return None
    if dt.tzinfo is None:
        dt = dt.replace(tzinfo=timezone.utc)
    return dt.astimezone(timezone.utc)


def _truncate_error(err: Exception) -> str:
    msg = str(err or '').strip()
    if isinstance(err, AppException) and err.extra:
        msg = f'{msg}: {err.extra}' if msg else err.extra
    if len(msg) > 1000:
        return msg[:1000]
    return msg


def _normalize_owner_user_id(owner_user_id: str | None) -> str:
    return (owner_user_id or '').strip()


def _can_use_chat(status: str | None, chat_enabled: bool) -> bool:
    """The single eligibility rule shared by connection responses and runtime filtering."""
    return (status or '').strip().upper() == 'ACTIVE' and chat_enabled


def _reserved_tenant_id(_: str | None = None) -> str:
    return ''


def _feishu_chat_options(options: dict | None, previous: dict | None = None) -> dict:
    result = dict(options or {})
    if 'chat_enabled' not in result and 'chatEnabled' not in result:
        enabled = True if previous is None else previous.get('chat_enabled', previous.get('chatEnabled', False))
        result.update(chat_enabled=enabled, chatEnabled=enabled)
    return result


def _json_dumps(payload: dict[str, Any]) -> str:
    return json.dumps(payload or {}, ensure_ascii=False, separators=(',', ':'))


def _json_loads(raw: str | None) -> dict[str, Any]:
    if not raw:
        return {}
    try:
        loaded = json.loads(raw)
    except (TypeError, ValueError):
        return {}
    return loaded if isinstance(loaded, dict) else {}


class CloudOAuthService:
    def __init__(self):
        feishu = FeishuOAuthProvider()
        google_drive = GoogleDriveOAuthProvider()
        notion = NotionOAuthProvider()
        wechat = WeChatProvider()
        github = GitHubOAuthProvider()
        self._providers: dict[str, CloudOAuthProvider] = {
            feishu.provider_name(): feishu,
            google_drive.provider_name(): google_drive,
            notion.provider_name(): notion,
            wechat.provider_name(): wechat,
            github.provider_name(): github,
        }
        for imap_name in MAIL_IMAP_PROVIDERS:
            self._providers[imap_name] = IMAPMailProvider(imap_name)
        self._cache_lock = threading.Lock()
        self._token_cache: dict[str, _TokenCacheItem] = {}

    def _provider(self, provider: str) -> CloudOAuthProvider:
        key = (provider or '').strip().lower()
        p = self._providers.get(key)
        if p is None:
            raise_error(ErrorCodes.CLOUD_PROVIDER_UNSUPPORTED, extra_msg=provider)
        return p

    @staticmethod
    def _validate_auth_mode(auth_mode: str) -> str:
        mode = (auth_mode or 'oauth_user').strip().lower()
        if mode not in _AUTH_MODES:
            raise_error(ErrorCodes.CLOUD_AUTH_MODE_INVALID, extra_msg=auth_mode)
        return mode

    @staticmethod
    def _is_token_valid(access_token: str, expires_at: datetime | None) -> bool:
        if not access_token:
            return False
        if expires_at is None:
            return True
        return expires_at > (_utcnow() + timedelta(seconds=_TOKEN_REFRESH_BUFFER_SECONDS))

    def _cache_get(self, connection_id: str) -> _TokenCacheItem | None:
        key = (connection_id or '').strip()
        if not key:
            return None
        with self._cache_lock:
            item = self._token_cache.get(key)
        if item is None:
            return None
        if self._is_token_valid(item.access_token, item.expires_at):
            return item
        with self._cache_lock:
            self._token_cache.pop(key, None)
        return None

    def _cache_delete(self, connection_id: str) -> None:
        key = (connection_id or '').strip()
        if not key:
            return
        with self._cache_lock:
            self._token_cache.pop(key, None)

    def _cache_set(self, connection_id: str, provider: str, payload: CloudTokenPayload) -> None:
        key = (connection_id or '').strip()
        if not key:
            return
        with self._cache_lock:
            self._token_cache[key] = _TokenCacheItem(
                provider=(provider or '').strip().lower(),
                access_token=payload.access_token,
                token_type=payload.token_type or 'Bearer',
                expires_at=payload.expires_at,
            )

    def _recover_refreshed_oauth_token(
        self,
        connection_id: str,
        *,
        user_id: str | None,
        tenant_id: str | None,
    ) -> tuple[str, str, CloudTokenPayload] | None:
        cached = self._cache_get(connection_id)
        with SessionLocal() as db:
            row = CloudAuthConnectionRepository.get_by_id(db, connection_id)
            if row is None:
                return None
            self._ensure_connection_owner(row, tenant_id=tenant_id, user_id=user_id)
            if (row.status or '').strip().upper() == 'REVOKED':
                return None
            auth_state_payload = self._decrypt_payload(row.auth_state_ciphertext, field_name='auth_state')
            access_token = (auth_state_payload.get('access_token') or '').strip()
            expires_at = _parse_dt(auth_state_payload.get('access_expires_at'))
            token_payload: CloudTokenPayload | None = None
            if self._is_token_valid(access_token, expires_at):
                token_payload = CloudTokenPayload(
                    access_token=access_token,
                    expires_at=expires_at,
                    refresh_token=(auth_state_payload.get('refresh_token') or '').strip(),
                    token_type=(auth_state_payload.get('token_type') or 'Bearer').strip() or 'Bearer',
                )
            elif cached is not None:
                token_payload = CloudTokenPayload(
                    access_token=cached.access_token,
                    expires_at=cached.expires_at,
                    token_type=cached.token_type,
                )
                auth_state_payload.update({
                    'access_token': token_payload.access_token,
                    'access_expires_at': _iso(token_payload.expires_at),
                    'token_type': token_payload.token_type or 'Bearer',
                })
                row.auth_state_ciphertext = self._encrypt_payload(auth_state_payload, field_name='auth_state')
            if token_payload is None:
                return None
            if (row.status or '').strip().upper() != 'ACTIVE' or row.last_error:
                row.status = 'ACTIVE'
                row.last_error = ''
                row.last_used_at = _utcnow()
                CloudAuthConnectionRepository.save(db, row)
            return row.provider, row.auth_mode, token_payload

    @staticmethod
    def _validate_required_credentials(*, tenant_id: str, client_id: str, client_secret: str) -> tuple[str, str, str]:
        tid = _reserved_tenant_id(tenant_id)
        cid = (client_id or '').strip()
        csec = (client_secret or '').strip()
        if not cid or not csec:
            raise_error(ErrorCodes.CLOUD_CLIENT_CREDENTIALS_REQUIRED)
        return tid, cid, csec

    @staticmethod
    def _new_connection_id() -> str:
        return f'conn_{uuid.uuid4().hex}'

    def _extract_app_key(self, row) -> tuple[str, str, str, str]:
        """Return (owner, provider, auth_mode, app_id) for dedup checks."""
        connection_method = (getattr(row, 'connection_method', '') or '').strip().lower()
        if connection_method == 'managed_oauth':
            return (
                (row.owner_user_id or '').strip(),
                (row.provider or '').strip(),
                (row.auth_mode or '').strip(),
                (row.cloud_connection_id or row.connection_id or '').strip(),
            )
        if connection_method == 'cli_personal_app':
            return (
                (row.owner_user_id or '').strip(),
                (row.provider or '').strip(),
                (row.auth_mode or '').strip(),
                (row.profile_ref or row.connection_id or '').strip(),
            )
        credential = self._management_credential(row) or {}
        return (
            (row.owner_user_id or '').strip(),
            (row.provider or '').strip(),
            (row.auth_mode or '').strip(),
            (credential.get('client_id') or '').strip(),
        )

    @staticmethod
    def _encrypt_payload(payload: dict[str, Any], *, field_name: str) -> str:
        try:
            return encrypt_json(payload)
        except RuntimeError as exc:
            raise_error(ErrorCodes.CLOUD_CRYPTO_UNAVAILABLE, extra_msg=_truncate_error(exc))
        except Exception as exc:
            raise_error(
                ErrorCodes.CLOUD_CREDENTIAL_ENCRYPT_FAILED,
                extra_msg=f'{field_name}: {_truncate_error(exc)}',
            )

    @staticmethod
    def _decrypt_payload(ciphertext: str, *, field_name: str) -> dict[str, Any]:
        try:
            return decrypt_json(ciphertext)
        except RuntimeError as exc:
            raise_error(ErrorCodes.CLOUD_CRYPTO_UNAVAILABLE, extra_msg=_truncate_error(exc))
        except Exception as exc:
            raise_error(
                ErrorCodes.CLOUD_CREDENTIAL_DECRYPT_FAILED,
                extra_msg=f'{field_name}: {_truncate_error(exc)}',
            )

    def _management_credential(self, row) -> dict[str, Any] | None:
        """Keep damaged accounts manageable; token flows still require decryption."""
        try:
            return self._decrypt_payload(row.credential_ciphertext, field_name='credential')
        except AppException as exc:
            if exc.code != ErrorCodes.CLOUD_CREDENTIAL_DECRYPT_FAILED[1]:
                raise
            return None

    def _create_connection_record(
        self,
        *,
        provider: str,
        tenant_id: str,
        owner_user_id: str = '',
        auth_mode: str,
        client_id: str,
        client_secret: str,
        display_name: str = '',
        redirect_uri: str = '',
        scope: str = '',
        provider_options: dict[str, Any] | None = None,
        oauth_state: str = '',
        oauth_state_expires_at: datetime | None = None,
        reauthorize_connection_id: str = '',
        reauthorize_provider_account_id: str = '',
        reauthorize_provider_tenant_key: str = '',
        status: str = 'ACTIVE',
        reuse_existing: bool = False,
    ) -> str:
        connection_id = self._new_connection_id()
        normalized_owner = _normalize_owner_user_id(owner_user_id)
        normalized_provider = (provider or '').strip().lower()
        normalized_auth_mode = (auth_mode or '').strip().lower()
        normalized_client_id = (client_id or '').strip()
        normalized_display_name = (display_name or '').strip()
        credential = {
            'client_id': normalized_client_id,
            'client_secret': client_secret,
            'redirect_uri': (redirect_uri or '').strip(),
            'scope': (scope or '').strip(),
            'provider_options': (
                _feishu_chat_options(provider_options) if normalized_provider == 'feishu' else provider_options or {}
            ),
        }
        auth_state_payload = {
            'oauth_state': (oauth_state or '').strip(),
            'oauth_state_expires_at': _iso(oauth_state_expires_at),
            'access_token': '',
            'access_expires_at': '',
            'refresh_token': '',
            'token_type': 'Bearer',
            'reauthorize_connection_id': (reauthorize_connection_id or '').strip(),
            'reauthorize_provider_account_id': (reauthorize_provider_account_id or '').strip(),
            'reauthorize_provider_tenant_key': (reauthorize_provider_tenant_key or '').strip(),
        }
        credential_ciphertext = self._encrypt_payload(credential, field_name='credential')
        auth_state_ciphertext = self._encrypt_payload(auth_state_payload, field_name='auth_state')

        def update_existing(db, row) -> str:
            row.client_id = normalized_client_id
            if normalized_provider == 'feishu':
                previous = self._decrypt_payload(row.credential_ciphertext, field_name='credential')
                credential['provider_options'] = _feishu_chat_options(
                    provider_options, previous.get('provider_options') or {},
                )
                row.credential_ciphertext = self._encrypt_payload(credential, field_name='credential')
            else:
                row.credential_ciphertext = credential_ciphertext
            row.auth_state_ciphertext = auth_state_ciphertext
            row.scope = (scope or '').strip()
            if normalized_display_name:
                row.display_name = normalized_display_name
            row.status = (status or '').strip().upper() or 'ACTIVE'
            row.last_error = ''
            row.last_used_at = None
            CloudAuthConnectionRepository.save(db, row)
            return row.connection_id

        with SessionLocal() as db:
            if reuse_existing:
                row = CloudAuthConnectionRepository.find_by_client_identity(
                    db,
                    owner_user_id=normalized_owner,
                    provider=normalized_provider,
                    auth_mode=normalized_auth_mode,
                    client_id=normalized_client_id,
                )
                if row is None:
                    legacy_rows = CloudAuthConnectionRepository.list_without_client_identity(
                        db,
                        owner_user_id=normalized_owner,
                        provider=normalized_provider,
                        auth_mode=normalized_auth_mode,
                    )
                    for candidate in legacy_rows:
                        try:
                            saved_credential = self._decrypt_payload(
                                candidate.credential_ciphertext,
                                field_name='credential',
                            )
                        except AppException:
                            continue
                        if (saved_credential.get('client_id') or '').strip() == normalized_client_id:
                            row = candidate
                            break
                if row is not None:
                    connection_id = update_existing(db, row)
                    self._cache_delete(connection_id)
                    return connection_id

            try:
                CloudAuthConnectionRepository.create(
                    db,
                    connection_id=connection_id,
                    tenant_id=_reserved_tenant_id(tenant_id),
                    owner_user_id=normalized_owner,
                    provider=normalized_provider,
                    auth_mode=normalized_auth_mode,
                    client_id=normalized_client_id,
                    credential_ciphertext=credential_ciphertext,
                    auth_state_ciphertext=auth_state_ciphertext,
                    display_name=normalized_display_name,
                    scope=(scope or '').strip(),
                    status=status,
                    last_error='',
                )
            except IntegrityError:
                if not reuse_existing:
                    raise
                db.rollback()
                row = CloudAuthConnectionRepository.find_by_client_identity(
                    db,
                    owner_user_id=normalized_owner,
                    provider=normalized_provider,
                    auth_mode=normalized_auth_mode,
                    client_id=normalized_client_id,
                )
                if row is None:
                    raise
                connection_id = update_existing(db, row)
                self._cache_delete(connection_id)
        return connection_id

    def _connection_payload(self, row) -> dict[str, Any]:
        connection_method = (row.connection_method or '').strip().lower()
        is_reference = connection_method in {'managed_oauth', 'cli_personal_app'}
        credential = {} if is_reference else self._management_credential(row)
        credential_unreadable = credential is None
        credential = credential or {}
        status = 'ERROR' if credential_unreadable and (row.status or '').upper() != 'REVOKED' else row.status
        reference_meta = _json_loads(row.provider_account_meta) if is_reference else {}
        raw_options = reference_meta if is_reference else credential.get('provider_options')
        options = dict(raw_options) if isinstance(raw_options, dict) else {}
        chat_enabled = options.get('chat_enabled', options.get('chatEnabled', False)) is True
        # Normalize response aliases without changing the user's stored preference.
        options.update(chat_enabled=chat_enabled, chatEnabled=chat_enabled)
        return {
            'connection_id': row.connection_id,
            'tenant_id': row.tenant_id or '',
            'owner_user_id': row.owner_user_id or '',
            'provider': row.provider,
            'auth_mode': row.auth_mode,
            'connection_method': row.connection_method or 'legacy_byo',
            'credential_location': row.credential_location or 'local',
            'profile_ref': row.profile_ref or '',
            'cloud_connection_id': row.cloud_connection_id or '',
            'cloud_owner_user_id': row.cloud_owner_user_id or '',
            'app_id': (credential.get('client_id') or '').strip(),
            'provider_account_id': row.provider_account_id or '',
            'display_name': row.display_name or '',
            'provider_tenant_key': row.provider_tenant_key or '',
            'provider_workspace_id': row.provider_workspace_id or '',
            'capability_contract_version': row.capability_contract_version or '',
            'provider_account_meta': _json_loads(row.provider_account_meta),
            'provider_options': options,
            'scope': row.scope or '',
            'last_used_at': row.last_used_at,
            'status': status,
            'can_use_chat': _can_use_chat(status, chat_enabled),
            'last_error': (
                ErrorCodes.CLOUD_CREDENTIAL_DECRYPT_FAILED[2] if credential_unreadable else row.last_error or ''
            ),
            'created_at': row.created_at,
            'updated_at': row.updated_at,
        }

    @staticmethod
    def _connection_status_payload(row) -> dict[str, Any]:
        return {
            'connection_id': row.connection_id,
            'tenant_id': row.tenant_id or '',
            'owner_user_id': row.owner_user_id or '',
            'provider': row.provider or '',
            'auth_mode': row.auth_mode or '',
            'provider_account_id': row.provider_account_id or '',
            'display_name': row.display_name or '',
            'provider_tenant_key': row.provider_tenant_key or '',
            'status': row.status or '',
            'last_error': row.last_error or '',
            'last_used_at': row.last_used_at,
            'updated_at': row.updated_at,
        }

    def _app_credential_payload(self, row) -> dict[str, Any]:
        if row is None:
            return {
                'provider': '',
                'app_id': '',
                'secret_configured': False,
                'status': '',
                'created_at': None,
                'updated_at': None,
            }
        credential = self._decrypt_payload(row.credential_ciphertext, field_name='credential')
        return {
            'provider': row.provider or '',
            'app_id': (credential.get('client_id') or '').strip(),
            'secret_configured': bool((credential.get('client_secret') or '').strip()),
            'status': row.status or '',
            'created_at': row.created_at,
            'updated_at': row.updated_at,
        }

    def _get_active_app_credential_row(self, db, *, provider: str, owner_user_id: str):
        return CloudAuthConnectionRepository.find_latest_for_owner(
            db,
            owner_user_id=owner_user_id,
            provider=provider,
            auth_mode=_OAUTH_APP_AUTH_MODE,
            status='ACTIVE',
        )

    def _get_saved_app_credentials(self, *, provider: str, owner_user_id: str) -> tuple[str, str, dict[str, Any]]:
        owner = _normalize_owner_user_id(owner_user_id)
        if not owner:
            raise_error(ErrorCodes.UNAUTHORIZED)
        with SessionLocal() as db:
            row = self._get_active_app_credential_row(db, provider=provider, owner_user_id=owner)
            if row is None:
                raise_error(ErrorCodes.CLOUD_APP_CREDENTIAL_NOT_CONFIGURED)
            credential = self._decrypt_payload(row.credential_ciphertext, field_name='credential')
        client_id = (credential.get('client_id') or '').strip()
        client_secret = (credential.get('client_secret') or '').strip()
        if not client_id or not client_secret:
            raise_error(ErrorCodes.CLOUD_APP_CREDENTIAL_INCOMPLETE)
        provider_options = (
            credential.get('provider_options')
            if isinstance(credential.get('provider_options'), dict)
            else {}
        )
        return client_id, client_secret, provider_options

    def _get_connection_credentials(
        self,
        *,
        provider: str,
        tenant_id: str,
        owner_user_id: str,
        connection_id: str,
    ) -> tuple[str, str, dict[str, Any]]:
        target_id = (connection_id or '').strip()
        if not target_id:
            raise_error(ErrorCodes.CLOUD_CONNECTION_NOT_FOUND)
        with SessionLocal() as db:
            row = CloudAuthConnectionRepository.get_by_id(db, target_id)
            if row is None:
                raise_error(ErrorCodes.CLOUD_CONNECTION_NOT_FOUND)
            self._ensure_connection_owner(row, tenant_id=tenant_id, user_id=owner_user_id)
            if (row.provider or '').strip().lower() != (provider or '').strip().lower():
                raise_error(ErrorCodes.CLOUD_PROVIDER_UNSUPPORTED)
            if (row.auth_mode or '').strip().lower() != 'oauth_user':
                raise_error(ErrorCodes.CLOUD_REAUTHORIZE_TARGET_OAUTH_USER_ONLY)
            credential = self._decrypt_payload(row.credential_ciphertext, field_name='credential')
        client_id = (credential.get('client_id') or '').strip()
        client_secret = (credential.get('client_secret') or '').strip()
        if not client_id or not client_secret:
            raise_error(ErrorCodes.CLOUD_REAUTHORIZE_CREDENTIAL_INCOMPLETE)
        provider_options = (
            credential.get('provider_options')
            if isinstance(credential.get('provider_options'), dict)
            else {}
        )
        return client_id, client_secret, provider_options

    def _get_reauthorize_target(
        self,
        *,
        provider: str,
        tenant_id: str,
        owner_user_id: str,
        connection_id: str,
    ) -> tuple[str, str, str]:
        target_id = (connection_id or '').strip()
        if not target_id:
            return '', '', ''
        owner = _normalize_owner_user_id(owner_user_id)
        if not owner:
            raise_error(ErrorCodes.UNAUTHORIZED)
        with SessionLocal() as db:
            row = CloudAuthConnectionRepository.get_by_id(db, target_id)
            if row is None:
                raise_error(ErrorCodes.CLOUD_CONNECTION_NOT_FOUND)
            self._ensure_connection_owner(row, tenant_id=tenant_id, user_id=owner)
            if (row.provider or '').strip().lower() != (provider or '').strip().lower():
                raise_error(ErrorCodes.CLOUD_PROVIDER_UNSUPPORTED)
            if (row.auth_mode or '').strip().lower() != 'oauth_user':
                raise_error(ErrorCodes.CLOUD_REAUTHORIZE_TARGET_OAUTH_USER_ONLY)
            if (row.status or '').strip().upper() not in {'ACTIVE', 'EXPIRED', 'ERROR', 'PENDING'}:
                raise_error(ErrorCodes.CLOUD_CONNECTION_NOT_FOUND)
            account_id = (row.provider_account_id or '').strip()
            # PENDING connections have no provider_account_id yet.
            # PENDING, ERROR, and ACTIVE may all legitimately have an empty
            # provider_account_id (auth never completed, code exchange failed
            # before fetching the profile, or fetch_account_profile errored).
            # Skip the check and let the callback set or verify the account.
            if not account_id and (row.status or '').strip().upper() not in ('PENDING', 'ERROR', 'ACTIVE'):
                raise_error(ErrorCodes.CLOUD_REAUTHORIZE_ACCOUNT_ID_REQUIRED)
            return row.connection_id, account_id, (row.provider_tenant_key or '').strip()

    @staticmethod
    def _profile_from_provider(provider_impl: CloudOAuthProvider, token: CloudTokenPayload) -> CloudAccountProfile:
        if not token.access_token or not hasattr(provider_impl, 'fetch_account_profile'):
            return CloudAccountProfile()
        try:
            return provider_impl.fetch_account_profile(access_token=token.access_token)
        except Exception:
            return CloudAccountProfile()

    @staticmethod
    def _apply_profile(row, profile: CloudAccountProfile, *, fallback_display_name: str = '') -> None:
        account_id = (profile.provider_account_id or '').strip()
        if account_id:
            row.provider_account_id = account_id
        display_name = (
            profile.display_name
            or fallback_display_name
            or row.display_name
            or row.provider_account_id
            or ''
        ).strip()
        if display_name:
            row.display_name = display_name
        row.provider_tenant_key = (profile.provider_tenant_key or row.provider_tenant_key or '').strip()
        if profile.meta:
            row.provider_account_meta = _json_dumps(profile.meta)

    def create_connection(
        self,
        *,
        provider: str,
        tenant_id: str,
        owner_user_id: str | None = None,
        auth_mode: str,
        client_id: str,
        client_secret: str,
        display_name: str = '',
        provider_options: dict[str, Any] | None = None,
    ) -> dict[str, Any]:
        provider_impl = self._provider(provider)
        mode = self._validate_auth_mode(auth_mode)
        if mode == 'oauth_user':
            raise_error(ErrorCodes.CLOUD_OAUTH_AUTHORIZE_MODE_REQUIRED)
        if is_mail_imap_provider(provider_impl.provider_name()) and mode != 'service_account':
            raise_error(ErrorCodes.MAIL_PROVIDER_AUTH_MODE_INVALID)
        if provider_impl.provider_name() == _WECHAT_PROVIDER and mode != 'service_account':
            raise_error(ErrorCodes.WECHAT_OFFICIAL_ACCOUNT_SERVICE_ACCOUNT_ONLY)
        tid, cid, csec = self._validate_required_credentials(
            tenant_id=tenant_id,
            client_id=client_id,
            client_secret=client_secret,
        )
        options = dict(provider_options or {})
        if is_mail_imap_provider(provider_impl.provider_name()):
            options.setdefault('chat_enabled', True)
            options.setdefault('chatEnabled', True)
            existing_id = self._existing_connection_id_for_client(
                owner_user_id=_normalize_owner_user_id(owner_user_id),
                provider=provider_impl.provider_name(),
                auth_mode=mode,
                client_id=cid,
            )
            if existing_id:
                # Login first so a bad auth code cannot overwrite a working mailbox.
                self._imap_login_or_raise(provider_impl, cid, csec)
        requires_validation = provider_impl.provider_name() == _WECHAT_PROVIDER
        if requires_validation:
            options.update({'chat_enabled': False, 'chatEnabled': False})
        initial_status = 'PENDING' if requires_validation else 'ACTIVE'
        connection_id = self._create_connection_record(
            provider=provider_impl.provider_name(),
            tenant_id=tid,
            owner_user_id=_normalize_owner_user_id(owner_user_id),
            auth_mode=mode,
            client_id=cid,
            client_secret=csec,
            display_name=display_name,
            provider_options=options,
            status=initial_status,
            reuse_existing=True,
        )
        if is_mail_imap_provider(provider_impl.provider_name()):
            return self._activate_imap_mail_connection(
                provider_impl=provider_impl,
                connection_id=connection_id,
                owner_user_id=_normalize_owner_user_id(owner_user_id),
                email=cid,
                secret=csec,
            )
        return {
            'connection_id': connection_id,
            'tenant_id': tid,
            'owner_user_id': _normalize_owner_user_id(owner_user_id),
            'provider': provider_impl.provider_name(),
            'auth_mode': mode,
            'scope': '',
            'status': initial_status,
        }

    def get_app_credentials(
        self,
        *,
        provider: str,
        owner_user_id: str | None = None,
    ) -> dict[str, Any]:
        provider_impl = self._provider(provider)
        owner = _normalize_owner_user_id(owner_user_id)
        if not owner:
            raise_error(ErrorCodes.UNAUTHORIZED)
        with SessionLocal() as db:
            row = self._get_active_app_credential_row(
                db,
                provider=provider_impl.provider_name(),
                owner_user_id=owner,
            )
            if row is None:
                return {
                    'provider': provider_impl.provider_name(),
                    'app_id': '',
                    'secret_configured': False,
                    'status': '',
                    'created_at': None,
                    'updated_at': None,
                }
            return self._app_credential_payload(row)

    def save_app_credentials(
        self,
        *,
        provider: str,
        owner_user_id: str | None = None,
        client_id: str,
        client_secret: str | None = None,
        provider_options: dict[str, Any] | None = None,
    ) -> dict[str, Any]:
        provider_impl = self._provider(provider)
        owner = _normalize_owner_user_id(owner_user_id)
        if not owner:
            raise_error(ErrorCodes.UNAUTHORIZED)
        client_id = (client_id or '').strip()
        client_secret = (client_secret or '').strip()
        if not client_id:
            raise_error(ErrorCodes.CLOUD_CLIENT_ID_REQUIRED)

        with SessionLocal() as db:
            row = self._get_active_app_credential_row(
                db,
                provider=provider_impl.provider_name(),
                owner_user_id=owner,
            )
            current_credential = (
                self._decrypt_payload(row.credential_ciphertext, field_name='credential')
                if row is not None
                else {}
            )
            previous_client_id = (current_credential.get('client_id') or '').strip()
            previous_client_secret = (current_credential.get('client_secret') or '').strip()
            if not client_secret and previous_client_id and client_id != previous_client_id:
                raise_error(ErrorCodes.CLOUD_CLIENT_SECRET_REQUIRED_ON_CLIENT_ID_CHANGE)
            effective_client_secret = client_secret or previous_client_secret
            if not effective_client_secret:
                raise_error(ErrorCodes.CLOUD_CLIENT_SECRET_REQUIRED)

            effective_provider_options = provider_options
            if effective_provider_options is None:
                saved_options = current_credential.get('provider_options')
                effective_provider_options = saved_options if isinstance(saved_options, dict) else {}
            credential = {
                'client_id': client_id,
                'client_secret': effective_client_secret,
                'redirect_uri': '',
                'scope': '',
                'provider_options': effective_provider_options or {},
            }
            auth_state_payload = {
                'access_token': '',
                'access_expires_at': '',
                'refresh_token': '',
                'token_type': 'Bearer',
            }

            if row is None:
                row = CloudAuthConnectionRepository.create(
                    db,
                    connection_id=self._new_connection_id(),
                    tenant_id='',
                    owner_user_id=owner,
                    provider=provider_impl.provider_name(),
                    auth_mode=_OAUTH_APP_AUTH_MODE,
                    credential_ciphertext=self._encrypt_payload(credential, field_name='credential'),
                    auth_state_ciphertext=self._encrypt_payload(auth_state_payload, field_name='auth_state'),
                    status='ACTIVE',
                    last_error='',
                )
            else:
                row.credential_ciphertext = self._encrypt_payload(credential, field_name='credential')
                row.auth_state_ciphertext = self._encrypt_payload(auth_state_payload, field_name='auth_state')
                row.status = 'ACTIVE'
                row.last_error = ''
                row = CloudAuthConnectionRepository.save(db, row)
            return self._app_credential_payload(row)

    def delete_app_credentials(
        self,
        *,
        provider: str,
        owner_user_id: str | None = None,
    ) -> dict[str, Any]:
        provider_impl = self._provider(provider)
        owner = _normalize_owner_user_id(owner_user_id)
        if not owner:
            raise_error(ErrorCodes.UNAUTHORIZED)
        with SessionLocal() as db:
            row = self._get_active_app_credential_row(
                db,
                provider=provider_impl.provider_name(),
                owner_user_id=owner,
            )
            if row is not None:
                row.status = 'REVOKED'
                row.last_error = 'app credential reset'
                CloudAuthConnectionRepository.save(db, row)
        return {
            'provider': provider_impl.provider_name(),
            'app_id': '',
            'secret_configured': False,
            'status': '',
            'created_at': None,
            'updated_at': None,
        }

    def create_authorize_url(
        self,
        *,
        provider: str,
        tenant_id: str,
        owner_user_id: str | None = None,
        auth_mode: str,
        client_id: str | None = None,
        client_secret: str | None = None,
        redirect_uri: str,
        scope: str | None = None,
        state: str | None = None,
        reauthorize_connection_id: str | None = None,
        provider_options: dict[str, Any] | None = None,
    ) -> dict[str, Any]:
        provider_impl = self._provider(provider)
        mode = self._validate_auth_mode(auth_mode)
        if mode != 'oauth_user':
            raise_error(ErrorCodes.CLOUD_AUTHORIZE_URL_OAUTH_USER_ONLY)
        if provider_impl.provider_name() == _WECHAT_PROVIDER:
            raise_error(ErrorCodes.WECHAT_OFFICIAL_ACCOUNT_SERVICE_ACCOUNT_ONLY)
        tenant_id = _reserved_tenant_id(tenant_id)
        normalized_owner = _normalize_owner_user_id(owner_user_id)
        reauthorize_target_id, reauthorize_account_id, reauthorize_tenant_key = self._get_reauthorize_target(
            provider=provider_impl.provider_name(),
            tenant_id=tenant_id,
            owner_user_id=normalized_owner,
            connection_id=reauthorize_connection_id or '',
        )
        normalized_client_id = (client_id or '').strip()
        normalized_client_secret = (client_secret or '').strip()
        if normalized_client_id or normalized_client_secret:
            tenant_id, normalized_client_id, normalized_client_secret = self._validate_required_credentials(
                tenant_id=tenant_id,
                client_id=normalized_client_id,
                client_secret=normalized_client_secret,
            )
        elif reauthorize_target_id:
            target_client_id, target_client_secret, target_provider_options = self._get_connection_credentials(
                provider=provider_impl.provider_name(),
                tenant_id=tenant_id,
                owner_user_id=normalized_owner,
                connection_id=reauthorize_target_id,
            )
            normalized_client_id = target_client_id
            normalized_client_secret = target_client_secret
            if provider_options is None:
                provider_options = target_provider_options
        else:
            saved_client_id, saved_client_secret, saved_provider_options = self._get_saved_app_credentials(
                provider=provider_impl.provider_name(),
                owner_user_id=normalized_owner,
            )
            normalized_client_id = saved_client_id
            normalized_client_secret = saved_client_secret
            if provider_options is None:
                provider_options = saved_provider_options
        redirect_uri = (redirect_uri or '').strip()
        if not redirect_uri:
            raise_error(ErrorCodes.CLOUD_REDIRECT_URI_REQUIRED_FOR_OAUTH_USER)

        oauth_state = ''
        oauth_state_expires = None
        authorize_url = ''
        scope_value = (scope or '').strip()
        if not scope_value and hasattr(provider_impl, 'default_scope'):
            scope_value = provider_impl.default_scope()
        if provider_impl.provider_name() == 'github':
            provider_options = dict(provider_options or {})
            if 'chat_enabled' not in provider_options and 'chatEnabled' not in provider_options:
                provider_options['chat_enabled'] = True
                provider_options['chatEnabled'] = True
        oauth_state = (state or '').strip() or secrets.token_urlsafe(18)
        oauth_state_expires = _utcnow() + timedelta(minutes=_OAUTH_STATE_TTL_MINUTES)
        authorize_url = provider_impl.build_authorize_url(
            client_id=normalized_client_id,
            redirect_uri=redirect_uri,
            scope=scope_value,
            state=oauth_state,
        )

        # Reuse or revoke existing incomplete connections.
        # When no reauthorize target is specified (new authorization): reuse the
        # first PENDING/ERROR connection so the connection_id stays stable across
        # page refreshes and duplicate tabs. Only refresh the oauth_state.
        # When a reauthorize target exists: revoke other PENDING/ERROR to keep
        # the connection list clean.
        reused_connection_id = ''
        with SessionLocal() as db:
            incomplete_rows: list = []
            for status_filter in ('PENDING', 'ERROR'):
                incomplete_rows.extend(
                    CloudAuthConnectionRepository.list_for_owner(
                        db,
                        owner_user_id=normalized_owner,
                        provider=provider_impl.provider_name(),
                        auth_mode='oauth_user',
                        status=status_filter,
                    )
                )
            if not reauthorize_target_id:
                # Check ACTIVE first — scope by client_id so App X does not
                # block App Y from creating its own connection.
                active = CloudAuthConnectionRepository.list_for_owner(
                    db,
                    owner_user_id=normalized_owner,
                    provider=provider_impl.provider_name(),
                    auth_mode='oauth_user',
                    status='ACTIVE',
                )
                active_same_app = [
                    r for r in active
                    if self._extract_app_key(r)[3] == normalized_client_id
                ]
                if active_same_app:
                    raise_error(ErrorCodes.CLOUD_CLIENT_ID_ALREADY_EXISTS)
                # Scope incomplete rows to the same client_id so App Y's
                # "new account" flow never reuses App X's PENDING/ERROR.
                same_app_rows = [
                    r for r in incomplete_rows
                    if self._extract_app_key(r)[3] == normalized_client_id
                ]
                if same_app_rows:
                    # Reuse the first PENDING/ERROR connection — refresh its
                    # oauth_state and credential so the new authorize_url,
                    # callback, and token exchange all use the latest secrets.
                    reused = same_app_rows[0]
                    reused_connection_id = reused.connection_id
                    auth_state = self._decrypt_payload(
                        reused.auth_state_ciphertext, field_name='auth_state'
                    )
                    auth_state['oauth_state'] = oauth_state
                    auth_state['oauth_state_expires_at'] = _iso(oauth_state_expires)
                    auth_state['reauthorize_connection_id'] = ''
                    auth_state['reauthorize_provider_account_id'] = ''
                    auth_state['reauthorize_provider_tenant_key'] = ''
                    reused.auth_state_ciphertext = self._encrypt_payload(
                        auth_state, field_name='auth_state'
                    )
                    # Sync credential with the latest client_id/client_secret
                    # from the request so the callback does not use stale secrets.
                    credential = self._decrypt_payload(
                        reused.credential_ciphertext, field_name='credential'
                    )
                    credential['client_id'] = normalized_client_id
                    credential['client_secret'] = normalized_client_secret
                    credential['provider_options'] = (
                        _feishu_chat_options(provider_options, credential.get('provider_options') or {})
                        if provider_impl.provider_name() == 'feishu' else provider_options or {}
                    )
                    reused.credential_ciphertext = self._encrypt_payload(
                        credential, field_name='credential'
                    )
                    reused.status = 'PENDING'
                    reused.last_error = ''
                    reused.scope = scope_value
                    CloudAuthConnectionRepository.save(db, reused)
                    # Revoke extras from the same app to prevent duplicates.
                    for extra in same_app_rows[1:]:
                        extra.status = 'REVOKED'
                        extra.last_error = 'superseded by new authorization attempt'
                        CloudAuthConnectionRepository.save(db, extra)
            elif not reauthorize_account_id:
                # Reauthorize target is PENDING (no provider account yet).
                # Reuse it directly instead of creating a new child connection.
                # Only touch rows from the same app (client_id).
                target_app_id = normalized_client_id
                for row in incomplete_rows:
                    if row.connection_id == reauthorize_target_id:
                        auth_state = self._decrypt_payload(
                            row.auth_state_ciphertext, field_name='auth_state'
                        )
                        auth_state['oauth_state'] = oauth_state
                        auth_state['oauth_state_expires_at'] = _iso(oauth_state_expires)
                        auth_state['reauthorize_connection_id'] = ''
                        auth_state['reauthorize_provider_account_id'] = ''
                        auth_state['reauthorize_provider_tenant_key'] = ''
                        row.auth_state_ciphertext = self._encrypt_payload(
                            auth_state, field_name='auth_state'
                        )
                        row.status = 'PENDING'
                        row.last_error = ''
                        row.scope = scope_value
                        CloudAuthConnectionRepository.save(db, row)
                        reused_connection_id = row.connection_id
                    elif self._extract_app_key(row)[3] == target_app_id:
                        row.status = 'REVOKED'
                        row.last_error = 'superseded by new authorization attempt'
                        CloudAuthConnectionRepository.save(db, row)
            else:
                # Reauthorize target is ACTIVE/EXPIRED/ERROR — normal
                # reauthorize flow: revoke other PENDING/ERROR from the
                # same app only, keep target.
                target_app_id = normalized_client_id
                for row in incomplete_rows:
                    if row.connection_id == reauthorize_target_id:
                        continue
                    if self._extract_app_key(row)[3] != target_app_id:
                        continue
                    row.status = 'REVOKED'
                    row.last_error = 'superseded by new authorization attempt'
                    CloudAuthConnectionRepository.save(db, row)

        if reused_connection_id:
            connection_id = reused_connection_id
        else:
            connection_id = self._create_connection_record(
                provider=provider_impl.provider_name(),
                tenant_id=tenant_id,
                owner_user_id=normalized_owner,
                auth_mode=mode,
                client_id=normalized_client_id,
                client_secret=normalized_client_secret,
                redirect_uri=redirect_uri,
                scope=scope_value,
                provider_options=provider_options,
                oauth_state=oauth_state,
                oauth_state_expires_at=oauth_state_expires,
                reauthorize_connection_id=reauthorize_target_id,
                reauthorize_provider_account_id=reauthorize_account_id,
                reauthorize_provider_tenant_key=reauthorize_tenant_key,
                status='PENDING',
            )

        return {
            'connection_id': connection_id,
            'tenant_id': tenant_id,
            'owner_user_id': normalized_owner,
            'provider': provider_impl.provider_name(),
            'auth_mode': mode,
            'scope': scope_value,
            'authorize_url': authorize_url,
            'state': oauth_state,
        }

    def oauth_callback(
        self,
        *,
        provider: str,
        tenant_id: str,
        owner_user_id: str | None = None,
        connection_id: str,
        code: str,
        state: str | None = None,
        redirect_uri: str | None = None,
    ) -> dict[str, Any]:
        provider_impl = self._provider(provider)
        connection_id = (connection_id or '').strip()
        tenant_id = _reserved_tenant_id(tenant_id)
        code = (code or '').strip()
        if not code:
            raise_error(ErrorCodes.CLOUD_OAUTH_CODE_REQUIRED)

        with SessionLocal() as db:
            row = CloudAuthConnectionRepository.get_by_id(db, connection_id)
            if row is None:
                raise_error(ErrorCodes.CLOUD_CONNECTION_NOT_FOUND)
            self._ensure_connection_owner(row, tenant_id=tenant_id, user_id=owner_user_id)
            if (row.provider or '').strip().lower() != provider_impl.provider_name():
                raise_error(ErrorCodes.CLOUD_PROVIDER_UNSUPPORTED)
            if (row.auth_mode or '').strip().lower() != 'oauth_user':
                raise_error(ErrorCodes.CLOUD_CALLBACK_OAUTH_USER_ONLY)

            credential = self._decrypt_payload(row.credential_ciphertext, field_name='credential')
            auth_state_payload = self._decrypt_payload(row.auth_state_ciphertext, field_name='auth_state')
            expected_state = (auth_state_payload.get('oauth_state') or '').strip()
            expected_expire = _parse_dt(auth_state_payload.get('oauth_state_expires_at'))
            incoming_state = (state or '').strip()
            if not expected_state or not incoming_state or incoming_state != expected_state:
                raise_error(ErrorCodes.CLOUD_OAUTH_STATE_INVALID)
            if expected_expire is not None and expected_expire <= _utcnow():
                raise_error(ErrorCodes.CLOUD_OAUTH_STATE_INVALID)

            effective_redirect_uri = (redirect_uri or '').strip() or (credential.get('redirect_uri') or '').strip()
            if not effective_redirect_uri:
                raise_error(ErrorCodes.CLOUD_REDIRECT_URI_REQUIRED)

            try:
                token = provider_impl.exchange_code(
                    client_id=(credential.get('client_id') or '').strip(),
                    client_secret=(credential.get('client_secret') or '').strip(),
                    code=code,
                    redirect_uri=effective_redirect_uri,
                )
            except Exception as exc:
                row.status = 'ERROR'
                row.last_error = _truncate_error(exc)
                CloudAuthConnectionRepository.save(db, row)
                raise_error(ErrorCodes.CLOUD_TOKEN_UNAVAILABLE, extra_msg=_truncate_error(exc))
            if not token.access_token:
                raise_error(ErrorCodes.CLOUD_PROVIDER_ACCESS_TOKEN_EMPTY)
            if provider_impl.provider_name() == 'feishu' and not token.refresh_token:
                row.status = 'ERROR'
                row.last_error = 'provider returned empty refresh_token'
                CloudAuthConnectionRepository.save(db, row)
                raise_error(ErrorCodes.CLOUD_PROVIDER_REFRESH_TOKEN_EMPTY)

            reauthorize_connection_id = (auth_state_payload.get('reauthorize_connection_id') or '').strip()
            reauthorize_account_id = (auth_state_payload.get('reauthorize_provider_account_id') or '').strip()
            reauthorize_tenant_key = (auth_state_payload.get('reauthorize_provider_tenant_key') or '').strip()
            profile = self._profile_from_provider(provider_impl, token)
            # Skip account-matching when the reauthorize target is PENDING
            # (reauthorize_account_id is empty — no prior account to compare).
            if reauthorize_connection_id and reauthorize_account_id:
                profile_account_id = (profile.provider_account_id or '').strip()
                if not reauthorize_account_id or not profile_account_id or profile_account_id != reauthorize_account_id:
                    row.status = 'ERROR'
                    row.last_error = 'reauthorized account does not match target connection'
                    CloudAuthConnectionRepository.save(db, row)
                    raise_error(
                        ErrorCodes.CLOUD_REAUTHORIZED_ACCOUNT_MISMATCH,
                    )
                profile_tenant_key = (profile.provider_tenant_key or '').strip()
                if reauthorize_tenant_key and profile_tenant_key and profile_tenant_key != reauthorize_tenant_key:
                    row.status = 'ERROR'
                    row.last_error = 'reauthorized tenant does not match target connection'
                    CloudAuthConnectionRepository.save(db, row)
                    raise_error(
                        ErrorCodes.CLOUD_REAUTHORIZED_TENANT_MISMATCH,
                    )
            auth_state_payload.update({
                'oauth_state': '',
                'oauth_state_expires_at': '',
                'access_token': token.access_token,
                'access_expires_at': _iso(token.expires_at),
                'refresh_token': token.refresh_token or '',
                'token_type': token.token_type or 'Bearer',
                'reauthorize_connection_id': '',
                'reauthorize_provider_account_id': '',
                'reauthorize_provider_tenant_key': '',
            })
            if redirect_uri:
                credential['redirect_uri'] = effective_redirect_uri
                row.credential_ciphertext = self._encrypt_payload(credential, field_name='credential')

            row.auth_state_ciphertext = self._encrypt_payload(auth_state_payload, field_name='auth_state')
            self._apply_profile(row, profile, fallback_display_name=f'{provider_impl.provider_name()} account')
            row.status = 'ACTIVE'
            row.last_error = ''
            if reauthorize_connection_id and reauthorize_connection_id != row.connection_id:
                existing = CloudAuthConnectionRepository.get_by_id(db, reauthorize_connection_id)
                if existing is None:
                    row.status = 'ERROR'
                    row.last_error = 'reauthorize target connection not found'
                    CloudAuthConnectionRepository.save(db, row)
                    raise_error(ErrorCodes.CLOUD_CONNECTION_NOT_FOUND)
                self._ensure_connection_owner(existing, tenant_id=tenant_id, user_id=owner_user_id)
                if (existing.provider or '').strip().lower() != provider_impl.provider_name():
                    raise_error(ErrorCodes.CLOUD_PROVIDER_UNSUPPORTED)
                if (existing.auth_mode or '').strip().lower() != 'oauth_user':
                    raise_error(ErrorCodes.CLOUD_REAUTHORIZE_TARGET_OAUTH_USER_ONLY)
                if (existing.status or '').strip().upper() not in {'ACTIVE', 'EXPIRED', 'ERROR', 'PENDING'}:
                    row.status = 'ERROR'
                    row.last_error = 'reauthorize target connection is not active'
                    CloudAuthConnectionRepository.save(db, row)
                    raise_error(ErrorCodes.CLOUD_CONNECTION_NOT_FOUND)
                if (existing.provider_account_id or '').strip() != reauthorize_account_id:
                    row.status = 'ERROR'
                    row.last_error = 'reauthorize target account changed'
                    CloudAuthConnectionRepository.save(db, row)
                    raise_error(
                        ErrorCodes.CLOUD_REAUTHORIZE_ACCOUNT_CHANGED,
                    )
            else:
                provider_account_lookup = {
                    'owner_user_id': row.owner_user_id or '',
                    'provider': row.provider or '',
                    'auth_mode': row.auth_mode or '',
                    'provider_account_id': row.provider_account_id or '',
                    'provider_tenant_key': row.provider_tenant_key or '',
                    'exclude_connection_id': row.connection_id,
                }
                existing = CloudAuthConnectionRepository.find_by_provider_account(
                    db,
                    **provider_account_lookup,
                    exclude_statuses=('REVOKED',),
                )
                if existing is None:
                    existing = CloudAuthConnectionRepository.find_by_provider_account(
                        db,
                        **provider_account_lookup,
                        status='REVOKED',
                    )
            # Only merge when the existing connection belongs to the same
            # app (client_id).  Different apps keep independent rows even
            # if the same Feishu user authorized both.
            is_different_app = False
            if existing is not None:
                existing_credential = self._management_credential(existing)
                existing_client_id = ((existing_credential or {}).get('client_id') or '').strip()
                new_client_id = (credential.get('client_id') or '').strip()
                # Do not merge into an account whose app identity cannot be verified.
                is_different_app = existing_credential is None or bool(
                    existing_client_id and new_client_id and existing_client_id != new_client_id
                )
            if (
                existing is not None
                and existing.connection_id != row.connection_id
                and not is_different_app
            ):
                restoring_revoked_connection = (
                    (existing.status or '').strip().upper() == 'REVOKED'
                )
                if provider_impl.provider_name() == 'feishu':
                    # Reauthorization replaces credentials, not the user's current chat preference.
                    previous = self._decrypt_payload(existing.credential_ciphertext, field_name='credential')
                    previous_options = previous.get('provider_options') or {}
                    options = dict(credential.get('provider_options') or {})
                    enabled = previous_options.get('chat_enabled', previous_options.get('chatEnabled', False))
                    options.update(chat_enabled=enabled, chatEnabled=enabled)
                    credential['provider_options'] = options
                    row.credential_ciphertext = self._encrypt_payload(credential, field_name='credential')
                existing.client_id = row.client_id
                existing.credential_ciphertext = row.credential_ciphertext
                existing.auth_state_ciphertext = row.auth_state_ciphertext
                existing.display_name = row.display_name
                existing.provider_tenant_key = row.provider_tenant_key
                existing.provider_account_meta = row.provider_account_meta
                existing.scope = row.scope
                existing.status = 'ACTIVE'
                existing.last_error = ''
                if restoring_revoked_connection:
                    existing.created_at = row.created_at
                row.status = 'REVOKED'
                row.last_error = 'superseded by existing provider account connection'
                CloudAuthConnectionRepository.save(db, row)
                CloudAuthConnectionRepository.save(db, existing)
                row = existing
                connection_id = existing.connection_id
            else:
                CloudAuthConnectionRepository.save(db, row)

        self._cache_set(connection_id, provider_impl.provider_name(), token)
        return {
            'connection_id': connection_id,
            'tenant_id': tenant_id,
            'owner_user_id': row.owner_user_id or '',
            'provider': provider_impl.provider_name(),
            'auth_mode': row.auth_mode or 'oauth_user',
            'provider_account_id': row.provider_account_id or '',
            'display_name': row.display_name or '',
            'provider_tenant_key': row.provider_tenant_key or '',
            'provider_account_meta': _json_loads(row.provider_account_meta),
            'scope': row.scope or '',
            'status': 'ACTIVE',
            'expires_at': token.expires_at,
            'refresh_token_bound': bool((token.refresh_token or '').strip()),
        }

    def list_connections(
        self,
        *,
        owner_user_id: str,
        provider: str | None = None,
        auth_mode: str | None = None,
        status: str | None = None,
    ) -> dict[str, Any]:
        owner = _normalize_owner_user_id(owner_user_id)
        if not owner:
            raise_error(ErrorCodes.UNAUTHORIZED)
        normalized_auth_mode = self._validate_auth_mode(auth_mode) if auth_mode else None
        with SessionLocal() as db:
            rows = CloudAuthConnectionRepository.list_for_owner(
                db,
                owner_user_id=owner,
                provider=provider,
                auth_mode=normalized_auth_mode,
                status=status,
                exclude_auth_modes=(_OAUTH_APP_AUTH_MODE,),
            )
            # Collect (owner, provider, auth_mode, app_id) combos that already
            # have an ACTIVE connection. PENDING rows sharing the same combo
            # are reauthorize children and should be hidden from the frontend.
            active_app_keys: set[tuple[str, str, str, str]] = set()
            for row in rows:
                if (row.status or '').strip().upper() == 'ACTIVE':
                    app_key = self._extract_app_key(row)
                    if app_key[3]:
                        active_app_keys.add(app_key)
            return {
                'items': [
                    self._connection_payload(row)
                    for row in rows
                    if (row.status or '').strip().upper() != 'REVOKED'
                    and not (
                        (row.status or '').strip().upper() == 'PENDING'
                        and self._extract_app_key(row) in active_app_keys
                    )
                ]
            }

    def list_chat_enabled_connections(
        self,
        *,
        owner_user_id: str,
        provider: str | None = None,
    ) -> dict[str, Any]:
        """Return connections eligible for chat using the public response's rule."""
        owner = _normalize_owner_user_id(owner_user_id)
        if not owner:
            raise_error(ErrorCodes.UNAUTHORIZED)
        with SessionLocal() as db:
            rows = CloudAuthConnectionRepository.list_for_owner(
                db,
                owner_user_id=owner if owner else None,
                provider=provider,
                auth_mode=None,
                status=None,
                exclude_auth_modes=(_OAUTH_APP_AUTH_MODE,),
            )
            enabled = []
            for row in rows:
                try:
                    payload = self._connection_payload(row)
                    if payload['can_use_chat']:
                        enabled.append(payload)
                except Exception:
                    pass
            return {'items': enabled}

    def list_target_cache_connections(
        self,
        *,
        provider: str | None = 'feishu',
        limit: int = 100,
    ) -> dict[str, Any]:
        """Return active user OAuth connections that scan-control-plane can prewarm."""
        try:
            normalized_limit = max(1, min(int(limit or 100), 500))
        except Exception:
            normalized_limit = 100
        with SessionLocal() as db:
            rows = CloudAuthConnectionRepository.list_health_check_candidates(
                db,
                provider=provider,
                auth_mode='oauth_user',
                statuses=('ACTIVE',),
                limit=normalized_limit,
            )
            # Collect (owner, provider, auth_mode, app_id) combos that already
            # have an ACTIVE connection. PENDING rows sharing the same combo
            # are reauthorize children and should be hidden from the frontend.
            active_app_keys: set[tuple[str, str, str, str]] = set()
            for row in rows:
                if (row.status or '').strip().upper() == 'ACTIVE':
                    app_key = self._extract_app_key(row)
                    if app_key[3]:
                        active_app_keys.add(app_key)
            return {
                'items': [
                    self._connection_payload(row)
                    for row in rows
                    if (row.status or '').strip().upper() != 'REVOKED'
                    and not (
                        (row.status or '').strip().upper() == 'PENDING'
                        and self._extract_app_key(row) in active_app_keys
                    )
                ]
            }

    def batch_connection_status(
        self,
        connection_ids: list[str],
        *,
        user_id: str | None = None,
        tenant_id: str | None = None,
    ) -> dict[str, Any]:
        normalized_ids = []
        seen = set()
        for connection_id in connection_ids or []:
            normalized = (connection_id or '').strip()
            if normalized and normalized not in seen:
                normalized_ids.append(normalized)
                seen.add(normalized)
        if not normalized_ids:
            return {'items': []}
        with SessionLocal() as db:
            rows = CloudAuthConnectionRepository.list_by_ids(db, normalized_ids)
            items = []
            for row in rows:
                self._ensure_connection_owner(row, tenant_id=tenant_id, user_id=user_id)
                items.append(self._connection_status_payload(row))
            order = {connection_id: index for index, connection_id in enumerate(normalized_ids)}
            items.sort(key=lambda item: order.get(item['connection_id'], len(order)))
            return {'items': items}

    def get_connection(self, connection_id: str, *, user_id: str | None = None) -> dict[str, Any]:
        if user_id is not None and not user_id.strip():
            raise_error(ErrorCodes.FORBIDDEN)
        with SessionLocal() as db:
            row = CloudAuthConnectionRepository.get_by_id(db, connection_id)
            if row is None:
                raise_error(ErrorCodes.CLOUD_CONNECTION_NOT_FOUND)
            self._ensure_connection_owner(row, tenant_id='', user_id=user_id)
            return self._connection_payload(row)

    def get_connection_internal(self, connection_id: str, *, user_id: str) -> dict[str, Any]:
        if not user_id.strip():
            raise_error(ErrorCodes.FORBIDDEN)
        with SessionLocal() as db:
            row = CloudAuthConnectionRepository.get_for_owner(db, connection_id, user_id)
            if row is None:
                raise_error(ErrorCodes.CLOUD_CONNECTION_NOT_FOUND)
            return self._connection_payload(row)

    def upsert_managed_connection(
        self,
        *,
        auth_connection_id: str,
        owner_user_id: str,
        cloud_owner_user_id: str,
        provider: str,
        display_name: str,
        provider_tenant_key: str,
        provider_workspace_id: str,
        status: str,
        capability_contract_version: str,
        capabilities: list[dict[str, Any]],
        provider_account_meta: dict[str, Any] | None = None,
    ) -> dict[str, Any]:
        if (
            not auth_connection_id or not owner_user_id or not cloud_owner_user_id
            or provider not in {'notion', 'feishu'}
        ):
            raise_error(ErrorCodes.INVALID_REQUEST)
        identity_meta = {
            key: value.strip()
            for key, value in (provider_account_meta or {}).items()
            if key in {'open_id', 'union_id', 'user_id'}
            and isinstance(value, str)
            and value.strip()
            and len(value.strip()) <= 512
        }
        meta = _json_dumps({
            'capabilities': capabilities or [],
            'chat_enabled': provider == 'feishu',
            **identity_meta,
        })
        with SessionLocal() as db:
            row = CloudAuthConnectionRepository.upsert_managed(
                db,
                connection_id=auth_connection_id,
                owner_user_id=owner_user_id,
                cloud_owner_user_id=cloud_owner_user_id,
                provider=provider,
                display_name=display_name or auth_connection_id,
                provider_tenant_key=provider_tenant_key,
                provider_workspace_id=provider_workspace_id,
                provider_account_meta=meta,
                status=status,
                capability_contract_version=capability_contract_version,
            )
            return self._connection_payload(row)

    def upsert_feishu_cli_connection(
        self,
        *,
        auth_connection_id: str,
        owner_user_id: str,
        display_name: str,
        provider_account_id: str,
        provider_tenant_key: str,
        provider_workspace_id: str,
        provider_account_meta: dict[str, Any] | None,
        profile_ref: str,
        granted_scopes: list[str],
        credential_location: str,
        status: str,
        capability_contract_version: str,
        capabilities: list[dict[str, Any]],
    ) -> dict[str, Any]:
        normalized_location = (credential_location or '').strip().lower()
        normalized_owner = (owner_user_id or '').strip()
        normalized_connection = (auth_connection_id or '').strip()
        normalized_profile_ref = (profile_ref or '').strip()
        if (
            not normalized_connection
            or not normalized_owner
            or not provider_account_id
            or not provider_tenant_key
            or normalized_profile_ref != f'{normalized_owner}/{normalized_connection}'
            or normalized_location not in {'local', 'cli_sidecar'}
        ):
            raise_error(ErrorCodes.INVALID_REQUEST)
        identity_meta = {
            key: value.strip()
            for key, value in (provider_account_meta or {}).items()
            if key in {'open_id', 'union_id', 'user_id'}
            and isinstance(value, str)
            and value.strip()
            and len(value.strip()) <= 512
        }
        meta = _json_dumps({
            'capabilities': capabilities or [],
            'chat_enabled': True,
            **identity_meta,
        })
        scopes = sorted({
            scope.strip()
            for scope in (granted_scopes or [])
            if isinstance(scope, str) and scope.strip() and len(scope.strip()) <= 128
        })
        with SessionLocal() as db:
            try:
                row = CloudAuthConnectionRepository.upsert_feishu_cli(
                    db,
                    connection_id=normalized_connection,
                    owner_user_id=normalized_owner,
                    display_name=display_name or normalized_connection,
                    provider_account_id=provider_account_id,
                    provider_tenant_key=provider_tenant_key,
                    provider_workspace_id=provider_workspace_id or provider_tenant_key,
                    provider_account_meta=meta,
                    profile_ref=normalized_profile_ref,
                    granted_scopes=' '.join(scopes),
                    credential_location=normalized_location,
                    status=status,
                    capability_contract_version=capability_contract_version or 'feishu-cli/v1',
                )
            except ValueError:
                raise_error(ErrorCodes.CLOUD_REAUTHORIZED_ACCOUNT_MISMATCH)
            return self._connection_payload(row)

    def delete_connection(self, connection_id: str, *, user_id: str | None = None) -> dict[str, Any]:
        connection_id = (connection_id or '').strip()
        if not connection_id:
            raise_error(ErrorCodes.CLOUD_CONNECTION_NOT_FOUND)
        with SessionLocal() as db:
            row = CloudAuthConnectionRepository.get_by_id(db, connection_id)
            if row is None:
                raise_error(ErrorCodes.CLOUD_CONNECTION_NOT_FOUND)
            self._ensure_connection_owner(row, tenant_id='', user_id=user_id)
            if (row.auth_mode or '').strip().lower() == _OAUTH_APP_AUTH_MODE:
                raise_error(ErrorCodes.CLOUD_CONNECTION_NOT_FOUND)

            if (row.connection_method or '').strip().lower() in {'managed_oauth', 'cli_personal_app'}:
                row.status = 'REVOKED'
                row.last_error = 'Provider connection revoked by owner'
                row.provider_account_meta = _json_dumps({})
                CloudAuthConnectionRepository.save(db, row)
                self._cache_delete(connection_id)
                return {'connection_id': connection_id, 'status': 'REVOKED', 'deleted': True}

            empty_credential = {
                'client_id': '',
                'client_secret': '',
                'redirect_uri': '',
                'scope': '',
                'provider_options': {},
            }
            empty_auth_state = {
                'oauth_state': '',
                'oauth_state_expires_at': '',
                'access_token': '',
                'access_expires_at': '',
                'refresh_token': '',
                'token_type': 'Bearer',
                'reauthorize_connection_id': '',
                'reauthorize_provider_account_id': '',
                'reauthorize_provider_tenant_key': '',
            }
            # Extract app_id before clearing the credential so we can scope
            # the PENDING cleanup to the same app only.
            # Without a verified app_id, leave other connections alone.
            deleted_credential = self._management_credential(row) or {}
            deleted_app_id = (deleted_credential.get('client_id') or '').strip()

            row.credential_ciphertext = self._encrypt_payload(empty_credential, field_name='credential')
            row.auth_state_ciphertext = self._encrypt_payload(empty_auth_state, field_name='auth_state')
            row.status = 'REVOKED'
            row.last_error = 'deleted by owner'
            row.last_used_at = None

            # Also revoke any PENDING connections for the same app so they
            # do not appear as orphaned entries after the parent is deleted.
            # Scope by client_id to avoid accidentally revoking PENDING
            # connections belonging to a different Feishu app.
            if deleted_app_id:
                pending_rows = CloudAuthConnectionRepository.list_for_owner(
                    db,
                    owner_user_id=(row.owner_user_id or '').strip(),
                    provider=(row.provider or '').strip(),
                    auth_mode=(row.auth_mode or '').strip(),
                    status='PENDING',
                )
                for pending_row in pending_rows:
                    if pending_row.connection_id == connection_id:
                        continue
                    pending_credential = self._management_credential(pending_row) or {}
                    pending_app_id = (pending_credential.get('client_id') or '').strip()
                    if pending_app_id == deleted_app_id:
                        pending_row.status = 'REVOKED'
                        pending_row.last_error = 'parent connection deleted by owner'

            # Commit the target and related cleanup together. A database error
            # must not leave a deleted target behind a failed API response.
            db.commit()

        self._cache_delete(connection_id)
        return {
            'connection_id': connection_id,
            'status': 'REVOKED',
            'deleted': True,
        }

    def update_connection(
        self,
        connection_id: str,
        *,
        user_id: str | None = None,
        display_name: str | None = None,
        displayName: str | None = None,
        name: str | None = None,
        client_id: str | None = None,
        app_id: str | None = None,
        appId: str | None = None,
        client_secret: str | None = None,
        app_secret: str | None = None,
        appSecret: str | None = None,
        provider_options: dict[str, Any] | None = None,
        provider_account_meta: dict[str, Any] | None = None,
        chat_enabled: bool | None = None,
        chatEnabled: bool | None = None,
    ) -> dict[str, Any]:
        connection_id = (connection_id or '').strip()
        if not connection_id:
            raise_error(ErrorCodes.CLOUD_CONNECTION_NOT_FOUND)

        with SessionLocal() as db:
            row = CloudAuthConnectionRepository.get_by_id(db, connection_id)
            if row is None:
                raise_error(ErrorCodes.CLOUD_CONNECTION_NOT_FOUND)
            self._ensure_connection_owner(row, tenant_id='', user_id=user_id)
            if (row.auth_mode or '').strip().lower() == _OAUTH_APP_AUTH_MODE:
                raise_error(ErrorCodes.CLOUD_CONNECTION_NOT_FOUND)
            if (row.status or '').strip().upper() == 'REVOKED':
                raise_error(ErrorCodes.CLOUD_CONNECTION_NOT_FOUND)

            if (row.connection_method or '').strip().lower() in {'managed_oauth', 'cli_personal_app'}:
                if any(value is not None for value in (client_id, app_id, appId, client_secret, app_secret, appSecret)):
                    raise_error(ErrorCodes.MANAGED_TOKEN_REQUIRES_CORE_BRIDGE)
                meta = _json_loads(row.provider_account_meta)
                requested_chat_enabled = chat_enabled if chat_enabled is not None else chatEnabled
                if requested_chat_enabled is not None:
                    meta['chat_enabled'] = bool(requested_chat_enabled)
                    meta['chatEnabled'] = bool(requested_chat_enabled)
                if provider_account_meta is not None:
                    meta.update(provider_account_meta)
                requested_display_name = next(
                    (value for value in (display_name, displayName, name) if value is not None), None,
                )
                if requested_display_name is not None:
                    row.display_name = (requested_display_name or '').strip()[:255]
                row.provider_account_meta = _json_dumps(meta)
                row = CloudAuthConnectionRepository.save(db, row)
                return self._connection_payload(row)

            credential = self._decrypt_payload(row.credential_ciphertext, field_name='credential')
            original_client_id = (credential.get('client_id') or '').strip()
            original_client_secret = (credential.get('client_secret') or '').strip()
            options = credential.get('provider_options')
            if not isinstance(options, dict):
                options = {}
            if provider_options is not None:
                options.update(provider_options)

            requested_client_id = next(
                (value for value in (client_id, app_id, appId) if value is not None),
                None,
            )
            normalized_client_id = (requested_client_id or '').strip()
            if requested_client_id is not None and normalized_client_id:
                mode = (row.auth_mode or '').strip().lower()
                if mode in {'tenant', 'service_account'}:
                    existing = CloudAuthConnectionRepository.find_by_client_identity(
                        db,
                        owner_user_id=row.owner_user_id or '',
                        provider=row.provider or '',
                        auth_mode=mode,
                        client_id=normalized_client_id,
                    )
                    if existing is not None and existing.connection_id != row.connection_id:
                        raise_error(
                            ErrorCodes.CLOUD_CLIENT_ID_ALREADY_EXISTS,
                        )
                row.client_id = normalized_client_id
                credential['client_id'] = normalized_client_id

            requested_client_secret = next(
                (value for value in (client_secret, app_secret, appSecret) if value is not None),
                None,
            )
            normalized_client_secret = (requested_client_secret or '').strip()
            if requested_client_secret is not None and normalized_client_secret:
                credential['client_secret'] = normalized_client_secret

            credentials_changed = (
                requested_client_id is not None
                and normalized_client_id
                and normalized_client_id != original_client_id
            ) or (
                requested_client_secret is not None
                and normalized_client_secret
                and normalized_client_secret != original_client_secret
            )
            if (
                (row.provider or '').strip().lower() == _WECHAT_PROVIDER
                and credentials_changed
            ):
                row.status = 'PENDING'
                row.last_error = ''
                row.last_used_at = None
                options.update({'chat_enabled': False, 'chatEnabled': False})

            requested_chat_enabled = chat_enabled if chat_enabled is not None else chatEnabled
            if requested_chat_enabled is not None:
                if requested_chat_enabled and (row.status or '').strip().upper() != 'ACTIVE':
                    raise_error(ErrorCodes.CLOUD_CONNECTION_VERIFICATION_REQUIRED)
                options['chat_enabled'] = bool(requested_chat_enabled)
                options['chatEnabled'] = bool(requested_chat_enabled)
            credential['provider_options'] = options

            meta = _json_loads(row.provider_account_meta)
            if provider_account_meta is not None:
                meta.update(provider_account_meta)

            requested_display_name = next(
                (
                    value
                    for value in (display_name, displayName, name)
                    if value is not None
                ),
                None,
            )
            if requested_display_name is not None:
                row.display_name = (requested_display_name or '').strip()[:255]
                meta['display_name'] = row.display_name
                meta['name'] = row.display_name

            effective_client_id = (credential.get('client_id') or '').strip()
            if effective_client_id:
                meta['client_id'] = effective_client_id
                meta['app_id'] = effective_client_id
            if requested_chat_enabled is not None:
                meta['chat_enabled'] = bool(requested_chat_enabled)
                meta['chatEnabled'] = bool(requested_chat_enabled)

            row.credential_ciphertext = self._encrypt_payload(credential, field_name='credential')
            row.provider_account_meta = _json_dumps(meta)
            row = CloudAuthConnectionRepository.save(db, row)
            payload = self._connection_payload(row)

        self._cache_delete(connection_id)
        return payload

    def refresh_connection_token(
        self,
        connection_id: str,
        *,
        user_id: str | None = None,
    ) -> dict[str, Any]:
        connection_id = (connection_id or '').strip()
        if not connection_id:
            raise_error(ErrorCodes.CLOUD_CONNECTION_NOT_FOUND)

        with SessionLocal() as db:
            row = CloudAuthConnectionRepository.get_by_id(db, connection_id)
            if row is None or (row.status or '').strip().upper() == 'REVOKED':
                raise_error(ErrorCodes.CLOUD_CONNECTION_NOT_FOUND)
            self._ensure_connection_owner(row, tenant_id='', user_id=user_id)
            mode = (row.auth_mode or '').strip().lower()
            if mode not in {'tenant', 'service_account'}:
                raise_error(ErrorCodes.CLOUD_AUTH_MODE_INVALID, extra_msg=row.auth_mode)
            provider_impl = self._provider(row.provider)
            credential = self._decrypt_payload(row.credential_ciphertext, field_name='credential')
            try:
                token_payload = self._acquire_tenant_token(provider_impl, credential)
            except Exception as exc:
                self._record_refresh_failure(db, row, exc)
                self._cache_delete(connection_id)
                if isinstance(exc, AppException):
                    raise
                raise_error(ErrorCodes.CLOUD_TOKEN_UNAVAILABLE, extra_msg=_truncate_error(exc))

            row.status = 'ACTIVE'
            row.last_error = ''
            row.last_used_at = _utcnow()
            CloudAuthConnectionRepository.save(db, row)

        self._cache_set(connection_id, row.provider, token_payload)
        return {
            'connection_id': row.connection_id,
            'tenant_id': row.tenant_id or '',
            'owner_user_id': row.owner_user_id or '',
            'provider': row.provider,
            'status': row.status,
        }

    @staticmethod
    def _acquire_tenant_token(
        provider_impl: CloudOAuthProvider,
        credential: dict[str, Any],
    ) -> CloudTokenPayload:
        token = provider_impl.acquire_tenant_access_token(
            client_id=(credential.get('client_id') or '').strip(),
            client_secret=(credential.get('client_secret') or '').strip(),
        )
        if not token.access_token:
            raise_error(ErrorCodes.CLOUD_PROVIDER_ACCESS_TOKEN_EMPTY)
        return token

    def _refresh_oauth_user_token(
        self,
        *,
        provider_impl: CloudOAuthProvider,
        credential: dict[str, Any],
        auth_state_payload: dict[str, Any],
    ) -> CloudTokenPayload:
        access_token = (auth_state_payload.get('access_token') or '').strip()
        expires_at = _parse_dt(auth_state_payload.get('access_expires_at'))
        if self._is_token_valid(access_token, expires_at):
            return CloudTokenPayload(
                access_token=access_token,
                expires_at=expires_at,
                refresh_token=(auth_state_payload.get('refresh_token') or '').strip(),
                token_type=(auth_state_payload.get('token_type') or 'Bearer').strip() or 'Bearer',
            )

        refresh_token = (auth_state_payload.get('refresh_token') or '').strip()
        if not refresh_token:
            raise_error(ErrorCodes.CLOUD_REFRESH_TOKEN_REQUIRED)
        refreshed = provider_impl.refresh_access_token(
            client_id=(credential.get('client_id') or '').strip(),
            client_secret=(credential.get('client_secret') or '').strip(),
            refresh_token=refresh_token,
        )
        if not refreshed.access_token:
            raise_error(ErrorCodes.CLOUD_PROVIDER_ACCESS_TOKEN_EMPTY)
        return refreshed

    @staticmethod
    def _record_refresh_failure(db, row, exc: Exception) -> bool:
        retryable = isinstance(exc, CloudProviderError) and exc.retryable
        requires_reauth = (
            isinstance(exc, CloudProviderError) and exc.requires_reauth
        ) or (
            isinstance(exc, AppException)
            and 'refresh_token is missing' in (exc.extra or '').lower()
        )
        if requires_reauth:
            row.status = 'EXPIRED'
        elif not retryable:
            row.status = 'ERROR'
        row.last_error = _truncate_error(exc)
        CloudAuthConnectionRepository.save(db, row)
        return retryable

    @staticmethod
    def _ensure_connection_owner(row, *, tenant_id: str | None, user_id: str | None) -> None:
        expected_tenant = _reserved_tenant_id(tenant_id)
        expected_user = _normalize_owner_user_id(user_id)
        if expected_tenant and (row.tenant_id or '').strip() != expected_tenant:
            raise_error(ErrorCodes.CLOUD_CONNECTION_NOT_FOUND)
        owner_user_id = (getattr(row, 'owner_user_id', '') or '').strip()
        if expected_user and owner_user_id != expected_user:
            raise_error(ErrorCodes.FORBIDDEN)

    @staticmethod
    def _ensure_connection_active(row) -> None:
        if (getattr(row, 'status', '') or '').strip().upper() != 'ACTIVE':
            raise_error(ErrorCodes.CLOUD_CONNECTION_NOT_FOUND)

    def _existing_connection_id_for_client(
        self,
        *,
        owner_user_id: str,
        provider: str,
        auth_mode: str,
        client_id: str,
    ) -> str:
        normalized_owner = _normalize_owner_user_id(owner_user_id)
        normalized_provider = (provider or '').strip().lower()
        normalized_auth_mode = (auth_mode or '').strip().lower()
        normalized_client_id = (client_id or '').strip()
        with SessionLocal() as db:
            row = CloudAuthConnectionRepository.find_by_client_identity(
                db,
                owner_user_id=normalized_owner,
                provider=normalized_provider,
                auth_mode=normalized_auth_mode,
                client_id=normalized_client_id,
            )
            if row is not None:
                return row.connection_id
            legacy_rows = CloudAuthConnectionRepository.list_without_client_identity(
                db,
                owner_user_id=normalized_owner,
                provider=normalized_provider,
                auth_mode=normalized_auth_mode,
            )
            for candidate in legacy_rows:
                try:
                    saved_credential = self._decrypt_payload(
                        candidate.credential_ciphertext,
                        field_name='credential',
                    )
                except AppException:
                    continue
                if (saved_credential.get('client_id') or '').strip() == normalized_client_id:
                    return candidate.connection_id
        return ''

    def _imap_login_or_raise(self, provider_impl: CloudOAuthProvider, email: str, secret: str):
        try:
            return provider_impl.acquire_tenant_access_token(client_id=email, client_secret=secret)
        except Exception as exc:
            if isinstance(exc, AppException):
                raise
            mapped = app_exception_from_exception(exc)
            if mapped.code == ErrorCodes.INTERNAL_ERROR[1]:
                raise_error(ErrorCodes.MAIL_IMAP_LOGIN_FAILED, extra_msg=_truncate_error(exc))
            raise mapped

    def _discard_failed_imap_connection(self, connection_id: str) -> None:
        with SessionLocal() as db:
            row = CloudAuthConnectionRepository.get_by_id(db, connection_id)
            if row is None:
                return
            if (row.provider_account_id or '').strip():
                return
            CloudAuthConnectionRepository.delete(db, row)
            self._cache_delete(connection_id)

    def _activate_imap_mail_connection(
        self,
        *,
        provider_impl: CloudOAuthProvider,
        connection_id: str,
        owner_user_id: str,
        email: str,
        secret: str,
    ) -> dict[str, Any]:
        try:
            token = self._imap_login_or_raise(provider_impl, email, secret)
        except Exception:
            self._discard_failed_imap_connection(connection_id)
            raise
        profile = CloudAccountProfile()
        if hasattr(provider_impl, 'account_profile_from_email'):
            profile = provider_impl.account_profile_from_email(email)
        with SessionLocal() as db:
            row = CloudAuthConnectionRepository.get_by_id(db, connection_id)
            if row is None:
                raise_error(ErrorCodes.CLOUD_CONNECTION_NOT_FOUND)
            auth_state_payload = self._decrypt_payload(row.auth_state_ciphertext, field_name='auth_state')
            auth_state_payload.update({
                'access_token': token.access_token,
                'access_expires_at': '',
                'refresh_token': '',
                'token_type': token.token_type or 'IMAP',
            })
            row.auth_state_ciphertext = self._encrypt_payload(auth_state_payload, field_name='auth_state')
            row.scope = 'imap smtp'
            self._apply_profile(row, profile, fallback_display_name=email)
            row.status = 'ACTIVE'
            row.last_error = ''
            row.last_used_at = _utcnow()
            CloudAuthConnectionRepository.save(db, row)
            payload = self._connection_payload(row)
        self._cache_set(connection_id, provider_impl.provider_name(), token)
        return {
            'connection_id': payload['connection_id'],
            'tenant_id': payload['tenant_id'],
            'owner_user_id': payload['owner_user_id'],
            'provider': payload['provider'],
            'auth_mode': payload['auth_mode'],
            'scope': payload['scope'],
            'status': payload['status'],
        }

    def verify_connection(
        self,
        connection_id: str,
        *,
        user_id: str | None = None,
        tenant_id: str | None = None,
    ) -> dict[str, Any]:
        connection_id = (connection_id or '').strip()
        if not connection_id:
            raise_error(ErrorCodes.CLOUD_CONNECTION_NOT_FOUND)
        with SessionLocal() as db:
            row = CloudAuthConnectionRepository.get_by_id(db, connection_id)
            if row is None:
                raise_error(ErrorCodes.CLOUD_CONNECTION_NOT_FOUND)
            self._ensure_connection_owner(row, tenant_id=tenant_id, user_id=user_id)
            if (
                (row.connection_method or '').strip().lower() in {'managed_oauth', 'cli_personal_app'}
                or (row.credential_location or '').strip().lower() in {'cloud', 'cli_sidecar'}
            ):
                raise_error(ErrorCodes.MANAGED_TOKEN_REQUIRES_CORE_BRIDGE)
            self._ensure_connection_active(row)
            return {
                'connection_id': row.connection_id,
                'tenant_id': row.tenant_id or '',
                'owner_user_id': row.owner_user_id or '',
                'provider': row.provider,
                'status': row.status,
            }

    def get_access_token(
        self,
        connection_id: str,
        *,
        user_id: str | None = None,
        tenant_id: str | None = None,
    ) -> dict[str, Any]:
        connection_id = (connection_id or '').strip()
        if not connection_id:
            raise_error(ErrorCodes.CLOUD_CONNECTION_NOT_FOUND)

        with SessionLocal() as db:
            row = CloudAuthConnectionRepository.get_by_id(db, connection_id)
            if row is None:
                raise_error(ErrorCodes.CLOUD_CONNECTION_NOT_FOUND)
            self._ensure_connection_owner(row, tenant_id=tenant_id, user_id=user_id)
            if (row.status or '').strip().upper() != 'ACTIVE':
                recovered = self._recover_refreshed_oauth_token(connection_id, user_id=user_id, tenant_id=tenant_id)
                if recovered is not None:
                    provider_name, auth_mode, token_payload = recovered
                    self._cache_set(connection_id, provider_name, token_payload)
                    return {
                        'connection_id': connection_id,
                        'provider': provider_name,
                        'auth_mode': auth_mode,
                        'access_token': token_payload.access_token,
                        'token_type': token_payload.token_type or 'Bearer',
                        'expires_at': token_payload.expires_at,
                        'status': 'ACTIVE',
                    }
                self._ensure_connection_active(row)
            provider = row.provider
            auth_mode = row.auth_mode

        cached = self._cache_get(connection_id)
        if cached is not None:
            return {
                'connection_id': connection_id,
                'provider': provider or cached.provider,
                'auth_mode': auth_mode,
                'access_token': cached.access_token,
                'token_type': cached.token_type,
                'expires_at': cached.expires_at,
                'status': 'ACTIVE',
            }

        with SessionLocal() as db:
            row = CloudAuthConnectionRepository.get_by_id(db, connection_id)
            if row is None:
                raise_error(ErrorCodes.CLOUD_CONNECTION_NOT_FOUND)
            self._ensure_connection_owner(row, tenant_id=tenant_id, user_id=user_id)
            self._ensure_connection_active(row)
            provider_impl = self._provider(row.provider)
            credential = self._decrypt_payload(row.credential_ciphertext, field_name='credential')
            auth_state_payload = self._decrypt_payload(row.auth_state_ciphertext, field_name='auth_state')

            try:
                mode = (row.auth_mode or '').strip().lower()
                token_payload: CloudTokenPayload
                if mode == 'oauth_user':
                    token_payload = self._refresh_oauth_user_token(
                        provider_impl=provider_impl,
                        credential=credential,
                        auth_state_payload=auth_state_payload,
                    )
                    auth_state_payload.update({
                        'access_token': token_payload.access_token,
                        'access_expires_at': _iso(token_payload.expires_at),
                        'refresh_token': token_payload.refresh_token or auth_state_payload.get('refresh_token') or '',
                        'token_type': token_payload.token_type or 'Bearer',
                    })
                    row.auth_state_ciphertext = self._encrypt_payload(auth_state_payload, field_name='auth_state')
                elif mode in {'tenant', 'service_account'}:
                    token_payload = self._acquire_tenant_token(provider_impl, credential)
                else:
                    raise_error(ErrorCodes.CLOUD_AUTH_MODE_INVALID, extra_msg=row.auth_mode)
            except Exception as exc:
                if mode == 'oauth_user':
                    recovered = self._recover_refreshed_oauth_token(connection_id, user_id=user_id, tenant_id=tenant_id)
                    if recovered is not None:
                        provider_name, auth_mode, token_payload = recovered
                        self._cache_set(connection_id, provider_name, token_payload)
                        return {
                            'connection_id': connection_id,
                            'provider': provider_name,
                            'auth_mode': auth_mode or row.auth_mode,
                            'access_token': token_payload.access_token,
                            'token_type': token_payload.token_type or 'Bearer',
                            'expires_at': token_payload.expires_at,
                            'status': 'ACTIVE',
                        }
                self._record_refresh_failure(db, row, exc)
                if isinstance(exc, AppException):
                    raise
                raise_error(ErrorCodes.CLOUD_TOKEN_UNAVAILABLE, extra_msg=_truncate_error(exc))

            row.status = 'ACTIVE'
            row.last_error = ''
            row.last_used_at = _utcnow()
            CloudAuthConnectionRepository.save(db, row)

        self._cache_set(connection_id, row.provider, token_payload)
        return {
            'connection_id': connection_id,
            'provider': row.provider,
            'auth_mode': row.auth_mode,
            'access_token': token_payload.access_token,
            'token_type': token_payload.token_type or 'Bearer',
            'expires_at': token_payload.expires_at,
            'status': row.status,
        }

    def _refresh_connection_for_health_check(self, connection_id: str) -> tuple[str, bool]:
        with SessionLocal() as db:
            row = CloudAuthConnectionRepository.get_by_id(db, connection_id)
            if row is None:
                raise_error(ErrorCodes.CLOUD_CONNECTION_NOT_FOUND)
            status = (row.status or '').strip().upper()
            if status in {'PENDING', 'REVOKED'}:
                return status, False
            if (row.auth_mode or '').strip().lower() != 'oauth_user':
                return status, False
            provider_impl = self._provider(row.provider)
            credential = self._decrypt_payload(row.credential_ciphertext, field_name='credential')
            auth_state_payload = self._decrypt_payload(row.auth_state_ciphertext, field_name='auth_state')
            try:
                token_payload = self._refresh_oauth_user_token(
                    provider_impl=provider_impl,
                    credential=credential,
                    auth_state_payload=auth_state_payload,
                )
            except Exception as exc:
                retryable = self._record_refresh_failure(db, row, exc)
                self._cache_delete(connection_id)
                return row.status, retryable
            auth_state_payload.update({
                'access_token': token_payload.access_token,
                'access_expires_at': _iso(token_payload.expires_at),
                'refresh_token': token_payload.refresh_token or auth_state_payload.get('refresh_token') or '',
                'token_type': token_payload.token_type or 'Bearer',
            })
            row.auth_state_ciphertext = self._encrypt_payload(auth_state_payload, field_name='auth_state')
            row.status = 'ACTIVE'
            row.last_error = ''
            row.last_used_at = _utcnow()
            CloudAuthConnectionRepository.save(db, row)
        self._cache_set(connection_id, row.provider, token_payload)
        return 'ACTIVE', False

    def check_connection_health(self, connection_id: str) -> dict[str, Any]:
        connection_id = (connection_id or '').strip()
        if not connection_id:
            return {'connection_id': '', 'checked': False, 'status': '', 'last_error': 'connection_id is required'}
        status, retryable = self._refresh_connection_for_health_check(connection_id)
        with SessionLocal() as db:
            row = CloudAuthConnectionRepository.get_by_id(db, connection_id)
            if row is None:
                return {
                    'connection_id': connection_id,
                    'checked': False,
                    'status': '',
                    'last_error': 'connection not found',
                }
            return {
                'connection_id': connection_id,
                'checked': status not in {'PENDING', 'REVOKED'},
                'status': row.status or status,
                'last_error': row.last_error or '',
                'retryable': retryable,
            }

    def run_health_check_once(
        self,
        *,
        provider: str | None = 'feishu',
        batch_size: int = 100,
    ) -> dict[str, Any]:
        with SessionLocal() as db:
            rows = CloudAuthConnectionRepository.list_health_check_candidates(
                db,
                provider=provider,
                auth_mode='oauth_user',
                statuses=('ACTIVE', 'ERROR'),
                limit=batch_size,
            )
            connection_ids = [row.connection_id for row in rows]
        checked = 0
        active = 0
        expired = 0
        error = 0
        retryable_errors = 0
        for connection_id in connection_ids:
            result = self.check_connection_health(connection_id)
            if result.get('checked'):
                checked += 1
            if result.get('status') == 'ACTIVE':
                active += 1
            elif result.get('status') == 'EXPIRED':
                expired += 1
            elif result.get('status') == 'ERROR':
                error += 1
            if result.get('retryable'):
                retryable_errors += 1
        return {
            'checked': checked,
            'active': active,
            'expired': expired,
            'error': error,
            'retryable_errors': retryable_errors,
            'candidate_count': len(connection_ids),
        }


cloud_oauth_service = CloudOAuthService()

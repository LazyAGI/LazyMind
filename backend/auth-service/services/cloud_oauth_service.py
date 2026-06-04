import secrets
import threading
import uuid
import json
from dataclasses import dataclass
from datetime import datetime, timedelta, timezone
from typing import Any

from core.cloud_crypto import decrypt_json, encrypt_json
from core.database import SessionLocal
from core.errors import AppException, ErrorCodes, raise_error
from repositories import CloudAuthConnectionRepository
from services.cloud_oauth_provider import CloudAccountProfile, CloudOAuthProvider, CloudTokenPayload
from services.providers import FeishuOAuthProvider


_AUTH_MODES = {'tenant', 'oauth_user', 'service_account'}
_OAUTH_APP_AUTH_MODE = 'oauth_app'
_TOKEN_REFRESH_BUFFER_SECONDS = 300
_OAUTH_STATE_TTL_MINUTES = 10


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
    if len(msg) > 1000:
        return msg[:1000]
    return msg


def _normalize_owner_user_id(owner_user_id: str | None) -> str:
    return (owner_user_id or '').strip()


def _reserved_tenant_id(_: str | None = None) -> str:
    return ''


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
        self._providers: dict[str, CloudOAuthProvider] = {feishu.provider_name(): feishu}
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
            raise_error(ErrorCodes.CLOUD_CREDENTIAL_INVALID, extra_msg='client_id/client_secret are required')
        return tid, cid, csec

    @staticmethod
    def _new_connection_id() -> str:
        return f'conn_{uuid.uuid4().hex}'

    @staticmethod
    def _encrypt_payload(payload: dict[str, Any], *, field_name: str) -> str:
        try:
            return encrypt_json(payload)
        except RuntimeError as exc:
            raise_error(ErrorCodes.CLOUD_CRYPTO_UNAVAILABLE, extra_msg=_truncate_error(exc))
        except Exception as exc:
            raise_error(
                ErrorCodes.CLOUD_CREDENTIAL_INVALID,
                extra_msg=f'{field_name} encrypt failed: {_truncate_error(exc)}',
            )

    @staticmethod
    def _decrypt_payload(ciphertext: str, *, field_name: str) -> dict[str, Any]:
        try:
            return decrypt_json(ciphertext)
        except RuntimeError as exc:
            raise_error(ErrorCodes.CLOUD_CRYPTO_UNAVAILABLE, extra_msg=_truncate_error(exc))
        except Exception as exc:
            raise_error(
                ErrorCodes.CLOUD_CREDENTIAL_INVALID,
                extra_msg=f'{field_name} decrypt failed: {_truncate_error(exc)}',
            )

    def _create_connection_record(
        self,
        *,
        provider: str,
        tenant_id: str,
        owner_user_id: str = '',
        auth_mode: str,
        client_id: str,
        client_secret: str,
        redirect_uri: str = '',
        scope: str = '',
        provider_options: dict[str, Any] | None = None,
        oauth_state: str = '',
        oauth_state_expires_at: datetime | None = None,
        reauthorize_connection_id: str = '',
        reauthorize_provider_account_id: str = '',
        reauthorize_provider_tenant_key: str = '',
        status: str = 'ACTIVE',
    ) -> str:
        connection_id = self._new_connection_id()
        credential = {
            'client_id': client_id,
            'client_secret': client_secret,
            'redirect_uri': (redirect_uri or '').strip(),
            'scope': (scope or '').strip(),
            'provider_options': provider_options or {},
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
        with SessionLocal() as db:
            CloudAuthConnectionRepository.create(
                db,
                connection_id=connection_id,
                tenant_id=_reserved_tenant_id(tenant_id),
                owner_user_id=owner_user_id,
                provider=(provider or '').strip().lower(),
                auth_mode=auth_mode,
                credential_ciphertext=self._encrypt_payload(credential, field_name='credential'),
                auth_state_ciphertext=self._encrypt_payload(auth_state_payload, field_name='auth_state'),
                scope=(scope or '').strip(),
                status=status,
                last_error='',
            )
        return connection_id

    def _connection_payload(self, row) -> dict[str, Any]:
        credential = self._decrypt_payload(row.credential_ciphertext, field_name='credential')
        return {
            'connection_id': row.connection_id,
            'tenant_id': row.tenant_id or '',
            'owner_user_id': row.owner_user_id or '',
            'provider': row.provider,
            'auth_mode': row.auth_mode,
            'app_id': (credential.get('client_id') or '').strip(),
            'provider_account_id': row.provider_account_id or '',
            'display_name': row.display_name or '',
            'provider_tenant_key': row.provider_tenant_key or '',
            'provider_account_meta': _json_loads(row.provider_account_meta),
            'provider_options': (
                credential.get('provider_options')
                if isinstance(credential.get('provider_options'), dict)
                else {}
            ),
            'scope': row.scope or '',
            'last_used_at': row.last_used_at,
            'status': row.status,
            'last_error': row.last_error or '',
            'created_at': row.created_at,
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
                raise_error(ErrorCodes.CLOUD_CREDENTIAL_INVALID, extra_msg='cloud app credential is not configured')
            credential = self._decrypt_payload(row.credential_ciphertext, field_name='credential')
        client_id = (credential.get('client_id') or '').strip()
        client_secret = (credential.get('client_secret') or '').strip()
        if not client_id or not client_secret:
            raise_error(ErrorCodes.CLOUD_CREDENTIAL_INVALID, extra_msg='cloud app credential is incomplete')
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
                raise_error(ErrorCodes.CLOUD_AUTH_MODE_INVALID, extra_msg='reauthorize target must be oauth_user')
            credential = self._decrypt_payload(row.credential_ciphertext, field_name='credential')
        client_id = (credential.get('client_id') or '').strip()
        client_secret = (credential.get('client_secret') or '').strip()
        if not client_id or not client_secret:
            raise_error(
                ErrorCodes.CLOUD_CREDENTIAL_INVALID,
                extra_msg='reauthorize connection credential is incomplete',
            )
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
                raise_error(ErrorCodes.CLOUD_AUTH_MODE_INVALID, extra_msg='reauthorize target must be oauth_user')
            if (row.status or '').strip().upper() not in {'ACTIVE', 'EXPIRED', 'ERROR'}:
                raise_error(ErrorCodes.CLOUD_CONNECTION_NOT_FOUND)
            account_id = (row.provider_account_id or '').strip()
            if not account_id:
                raise_error(
                    ErrorCodes.CLOUD_CREDENTIAL_INVALID,
                    extra_msg='reauthorize target provider_account_id is missing',
                )
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
        provider_options: dict[str, Any] | None = None,
    ) -> dict[str, Any]:
        provider_impl = self._provider(provider)
        mode = self._validate_auth_mode(auth_mode)
        if mode == 'oauth_user':
            raise_error(ErrorCodes.CLOUD_AUTH_MODE_INVALID, extra_msg='oauth_user should use oauth/authorize-url')
        tid, cid, csec = self._validate_required_credentials(
            tenant_id=tenant_id,
            client_id=client_id,
            client_secret=client_secret,
        )
        connection_id = self._create_connection_record(
            provider=provider_impl.provider_name(),
            tenant_id=tid,
            owner_user_id=_normalize_owner_user_id(owner_user_id),
            auth_mode=mode,
            client_id=cid,
            client_secret=csec,
            provider_options=provider_options,
        )
        return {
            'connection_id': connection_id,
            'tenant_id': tid,
            'owner_user_id': _normalize_owner_user_id(owner_user_id),
            'provider': provider_impl.provider_name(),
            'auth_mode': mode,
            'scope': '',
            'status': 'ACTIVE',
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
            raise_error(ErrorCodes.CLOUD_CREDENTIAL_INVALID, extra_msg='client_id is required')

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
                raise_error(
                    ErrorCodes.CLOUD_CREDENTIAL_INVALID,
                    extra_msg='client_secret is required when client_id changes',
                )
            effective_client_secret = client_secret or previous_client_secret
            if not effective_client_secret:
                raise_error(ErrorCodes.CLOUD_CREDENTIAL_INVALID, extra_msg='client_secret is required')

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
            raise_error(ErrorCodes.CLOUD_AUTH_MODE_INVALID, extra_msg='authorize-url only supports oauth_user')
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
            raise_error(ErrorCodes.CLOUD_CREDENTIAL_INVALID, extra_msg='redirect_uri is required for oauth_user')

        oauth_state = ''
        oauth_state_expires = None
        authorize_url = ''
        scope_value = (scope or '').strip()
        if not scope_value and hasattr(provider_impl, 'default_scope'):
            scope_value = provider_impl.default_scope()
        oauth_state = (state or '').strip() or secrets.token_urlsafe(18)
        oauth_state_expires = _utcnow() + timedelta(minutes=_OAUTH_STATE_TTL_MINUTES)
        authorize_url = provider_impl.build_authorize_url(
            client_id=normalized_client_id,
            redirect_uri=redirect_uri,
            scope=scope_value,
            state=oauth_state,
        )
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
                raise_error(ErrorCodes.CLOUD_AUTH_MODE_INVALID, extra_msg='callback only supports oauth_user')

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
                raise_error(ErrorCodes.CLOUD_CREDENTIAL_INVALID, extra_msg='redirect_uri is required')

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
                raise_error(ErrorCodes.CLOUD_TOKEN_UNAVAILABLE, extra_msg='empty access_token')

            reauthorize_connection_id = (auth_state_payload.get('reauthorize_connection_id') or '').strip()
            reauthorize_account_id = (auth_state_payload.get('reauthorize_provider_account_id') or '').strip()
            reauthorize_tenant_key = (auth_state_payload.get('reauthorize_provider_tenant_key') or '').strip()
            profile = self._profile_from_provider(provider_impl, token)
            if reauthorize_connection_id:
                profile_account_id = (profile.provider_account_id or '').strip()
                if not reauthorize_account_id or not profile_account_id or profile_account_id != reauthorize_account_id:
                    row.status = 'ERROR'
                    row.last_error = 'reauthorized account does not match target connection'
                    CloudAuthConnectionRepository.save(db, row)
                    raise_error(
                        ErrorCodes.CLOUD_CREDENTIAL_INVALID,
                        extra_msg='reauthorized account does not match target connection',
                    )
                profile_tenant_key = (profile.provider_tenant_key or '').strip()
                if reauthorize_tenant_key and profile_tenant_key and profile_tenant_key != reauthorize_tenant_key:
                    row.status = 'ERROR'
                    row.last_error = 'reauthorized tenant does not match target connection'
                    CloudAuthConnectionRepository.save(db, row)
                    raise_error(
                        ErrorCodes.CLOUD_CREDENTIAL_INVALID,
                        extra_msg='reauthorized tenant does not match target connection',
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
                    raise_error(ErrorCodes.CLOUD_AUTH_MODE_INVALID, extra_msg='reauthorize target must be oauth_user')
                if (existing.status or '').strip().upper() not in {'ACTIVE', 'EXPIRED', 'ERROR'}:
                    row.status = 'ERROR'
                    row.last_error = 'reauthorize target connection is not active'
                    CloudAuthConnectionRepository.save(db, row)
                    raise_error(ErrorCodes.CLOUD_CONNECTION_NOT_FOUND)
                if (existing.provider_account_id or '').strip() != reauthorize_account_id:
                    row.status = 'ERROR'
                    row.last_error = 'reauthorize target account changed'
                    CloudAuthConnectionRepository.save(db, row)
                    raise_error(
                        ErrorCodes.CLOUD_CREDENTIAL_INVALID,
                        extra_msg='reauthorize target account changed',
                    )
            else:
                existing = CloudAuthConnectionRepository.find_by_provider_account(
                    db,
                    owner_user_id=row.owner_user_id or '',
                    provider=row.provider or '',
                    auth_mode=row.auth_mode or '',
                    provider_account_id=row.provider_account_id or '',
                    exclude_statuses=('REVOKED',),
                )
            if existing is not None and existing.connection_id != row.connection_id:
                existing.credential_ciphertext = row.credential_ciphertext
                existing.auth_state_ciphertext = row.auth_state_ciphertext
                existing.display_name = row.display_name
                existing.provider_tenant_key = row.provider_tenant_key
                existing.provider_account_meta = row.provider_account_meta
                existing.scope = row.scope
                existing.status = 'ACTIVE'
                existing.last_error = ''
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
            return {'items': [self._connection_payload(row) for row in rows]}

    def get_connection(self, connection_id: str, *, user_id: str | None = None) -> dict[str, Any]:
        with SessionLocal() as db:
            row = CloudAuthConnectionRepository.get_by_id(db, connection_id)
            if row is None:
                raise_error(ErrorCodes.CLOUD_CONNECTION_NOT_FOUND)
            self._ensure_connection_owner(row, tenant_id='', user_id=user_id)
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
            row.credential_ciphertext = self._encrypt_payload(empty_credential, field_name='credential')
            row.auth_state_ciphertext = self._encrypt_payload(empty_auth_state, field_name='auth_state')
            row.status = 'REVOKED'
            row.last_error = 'deleted by owner'
            row.last_used_at = None
            CloudAuthConnectionRepository.save(db, row)

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

            credential = self._decrypt_payload(row.credential_ciphertext, field_name='credential')
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
                credential['client_id'] = normalized_client_id

            requested_client_secret = next(
                (value for value in (client_secret, app_secret, appSecret) if value is not None),
                None,
            )
            normalized_client_secret = (requested_client_secret or '').strip()
            if requested_client_secret is not None and normalized_client_secret:
                credential['client_secret'] = normalized_client_secret

            requested_chat_enabled = chat_enabled if chat_enabled is not None else chatEnabled
            if requested_chat_enabled is not None:
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
            raise_error(ErrorCodes.CLOUD_TOKEN_UNAVAILABLE, extra_msg='refresh_token is missing')
        refreshed = provider_impl.refresh_access_token(
            client_id=(credential.get('client_id') or '').strip(),
            client_secret=(credential.get('client_secret') or '').strip(),
            refresh_token=refresh_token,
        )
        if not refreshed.access_token:
            raise_error(ErrorCodes.CLOUD_TOKEN_UNAVAILABLE, extra_msg='provider returned empty access_token')
        return refreshed

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
                    token_payload = provider_impl.acquire_tenant_access_token(
                        client_id=(credential.get('client_id') or '').strip(),
                        client_secret=(credential.get('client_secret') or '').strip(),
                    )
                else:
                    raise_error(ErrorCodes.CLOUD_AUTH_MODE_INVALID, extra_msg=row.auth_mode)
            except AppException:
                raise
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
                row.status = 'ERROR'
                row.last_error = _truncate_error(exc)
                CloudAuthConnectionRepository.save(db, row)
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


cloud_oauth_service = CloudOAuthService()

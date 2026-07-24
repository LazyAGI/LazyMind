import datetime as dt
import hashlib
import logging
import re
import threading
import uuid
from collections.abc import Callable
from typing import Any
from urllib.parse import urlparse

from channel_gateway.common.database import ACTIVE_SESSION_STATUSES, GatewayStore
from channel_gateway.common.security import JsonCipher
from channel_gateway.settings import Settings
from channel_gateway.wechat.client import WeChatClient, WeChatError


_logger = logging.getLogger(__name__)
_TERMINAL_STATUSES = {'connected', 'expired', 'canceled', 'failed'}
_REDIRECT_HOST_RE = re.compile(r'^[A-Za-z0-9.-]+$')


class GatewayError(RuntimeError):
    def __init__(self, http_status: int, code: str, message: str, retryable: bool = False):
        super().__init__(http_status, code, message, retryable)
        self.http_status = http_status
        self.code = code
        self.message = message
        self.retryable = retryable


def _utc_now() -> dt.datetime:
    return dt.datetime.now(dt.timezone.utc)


def _iso(value: dt.datetime | None) -> str | None:
    return value.isoformat() if value else None


class WeChatConnectionService:
    def __init__(
        self,
        *,
        settings: Settings,
        store: GatewayStore,
        cipher: JsonCipher,
        on_account_connected: Callable[[str], None] | None = None,
    ):
        self._settings = settings
        self._store = store
        self._cipher = cipher
        self._on_account_connected = on_account_connected
        self._wechat = WeChatClient(
            settings.wechat_ilink_base_url,
            settings.wechat_poll_timeout_seconds,
        )
        self._lock = threading.Lock()
        self._workers: dict[tuple[str, int], tuple[threading.Thread, threading.Event]] = {}
        self._shutdown = threading.Event()

    def start(self) -> None:
        self._store.initialize()
        now = _utc_now()
        for row in self._store.recoverable_sessions():
            if row['expires_at'] <= now:
                self._store.mark_expired(row['id'], row['qr_version'])
                continue
            if not row.get('provider_state_ciphertext'):
                self._store.mark_failed(
                    row['id'],
                    row['qr_version'],
                    code='LOGIN_INTERRUPTED',
                    message='登录过程被中断，请刷新二维码后重试',
                    retryable=True,
                )
                continue
            self._start_worker(row['id'], row['qr_version'])

    def stop(self) -> None:
        self._shutdown.set()
        with self._lock:
            workers = list(self._workers.values())
        for _, stop_event in workers:
            stop_event.set()
        for thread, _ in workers:
            thread.join(timeout=0.2)

    def create_session(
        self,
        *,
        owner_user_id: str,
        provider: str,
        idempotency_key: str | None,
    ) -> dict[str, Any]:
        provider = provider.strip().lower()
        if provider != 'wechat':
            raise GatewayError(422, 'PROVIDER_NOT_SUPPORTED', f'暂不支持频道类型：{provider}')
        normalized_idempotency_key = (idempotency_key or '').strip()
        if len(normalized_idempotency_key) > 128:
            raise GatewayError(422, 'INVALID_IDEMPOTENCY_KEY', 'Idempotency-Key 长度不能超过 128 个字符')
        expires_at = _utc_now() + dt.timedelta(seconds=self._settings.wechat_qr_session_ttl_seconds)
        session_id = f'cs_{uuid.uuid4().hex}'
        row, created = self._store.reserve_session(
            session_id=session_id,
            owner_user_id=owner_user_id,
            provider=provider,
            idempotency_key=normalized_idempotency_key or None,
            expires_at=expires_at,
        )
        if not created:
            return self._session_view(row)
        try:
            qrcode, qr_payload, base_url = self._wechat.start_login()
        except WeChatError as exc:
            _logger.warning('wechat_start_login_failed session_id=%s error=%s', session_id, exc)
            self._store.mark_failed(
                session_id,
                1,
                code='WECHAT_UNAVAILABLE',
                message='微信登录服务暂时不可用，请稍后重试',
                retryable=True,
            )
            raise GatewayError(
                503,
                'WECHAT_UNAVAILABLE',
                '微信登录服务暂时不可用，请稍后重试',
                retryable=True,
            ) from exc
        state = {
            'qrcode': qrcode,
            'qr_payload': qr_payload,
            'base_url': base_url,
            'verify_code': '',
        }
        row = self._store.set_qr_ready(
            session_id,
            self._cipher.encrypt(owner_user_id, state),
            expires_at,
        )
        if not row:
            raise GatewayError(409, 'INVALID_STATE', '连接会话状态已经变化')
        self._start_worker(session_id, row['qr_version'])
        return self._session_view(row)

    def get_session(self, owner_user_id: str, session_id: str) -> dict[str, Any]:
        row = self._store.get_session(owner_user_id, session_id)
        if not row:
            raise GatewayError(404, 'LOGIN_NOT_FOUND', '连接会话不存在')
        return self._session_view(row)

    def submit_challenge(
        self,
        *,
        owner_user_id: str,
        session_id: str,
        challenge_type: str,
        value: str,
    ) -> dict[str, Any]:
        row = self._store.get_session(owner_user_id, session_id)
        if not row:
            raise GatewayError(404, 'LOGIN_NOT_FOUND', '连接会话不存在')
        if row['status'] != 'verification_required':
            raise GatewayError(409, 'INVALID_STATE', '当前连接会话不需要数字验证')
        code = value.strip()
        if challenge_type != 'numeric_code' or not code.isdigit():
            raise GatewayError(422, 'INVALID_VERIFICATION_CODE', '请输入手机微信中显示的数字')
        state = self._decrypt_session_state(row)
        state['verify_code'] = code
        updated = self._store.update_active_session(
            session_id=session_id,
            qr_version=row['qr_version'],
            status='confirming',
            message='正在验证，请稍候',
            state_ciphertext=self._cipher.encrypt(owner_user_id, state),
        )
        if not updated:
            raise GatewayError(409, 'INVALID_STATE', '连接会话状态已经变化')
        return self._session_view(updated)

    def refresh_session(self, owner_user_id: str, session_id: str) -> dict[str, Any]:
        row = self._store.get_session(owner_user_id, session_id)
        if not row:
            raise GatewayError(404, 'LOGIN_NOT_FOUND', '连接会话不存在')
        refreshable = row['status'] == 'expired' or (
            row['status'] == 'failed' and row.get('error_retryable') is True
        )
        if not refreshable:
            raise GatewayError(409, 'INVALID_STATE', '当前连接会话不能刷新二维码')
        try:
            qrcode, qr_payload, base_url = self._wechat.start_login()
        except WeChatError as exc:
            _logger.warning('wechat_refresh_login_failed session_id=%s error=%s', session_id, exc)
            raise GatewayError(
                503,
                'WECHAT_UNAVAILABLE',
                '微信登录服务暂时不可用，请稍后重试',
                retryable=True,
            ) from exc
        state = {
            'qrcode': qrcode,
            'qr_payload': qr_payload,
            'base_url': base_url,
            'verify_code': '',
        }
        expires_at = _utc_now() + dt.timedelta(seconds=self._settings.wechat_qr_session_ttl_seconds)
        updated = self._store.refresh_session(
            owner_user_id=owner_user_id,
            session_id=session_id,
            state_ciphertext=self._cipher.encrypt(owner_user_id, state),
            expires_at=expires_at,
        )
        if not updated:
            raise GatewayError(409, 'INVALID_STATE', '连接会话状态已经变化')
        self._start_worker(session_id, updated['qr_version'])
        return self._session_view(updated)

    def cancel_session(self, owner_user_id: str, session_id: str) -> None:
        row = self._store.get_session(owner_user_id, session_id)
        if not row:
            raise GatewayError(404, 'LOGIN_NOT_FOUND', '连接会话不存在')
        if row['status'] in _TERMINAL_STATUSES:
            raise GatewayError(409, 'INVALID_STATE', '连接会话已经结束')
        if not self._store.cancel_session(owner_user_id, session_id):
            raise GatewayError(409, 'INVALID_STATE', '连接会话状态已经变化')

    def list_accounts(self, owner_user_id: str, provider: str) -> dict[str, Any]:
        provider = provider.strip().lower()
        if provider != 'wechat':
            raise GatewayError(422, 'PROVIDER_NOT_SUPPORTED', f'暂不支持频道类型：{provider}')
        rows = self._store.list_accounts(owner_user_id, provider)
        return {'items': [self._account_view(row) for row in rows]}

    def _start_worker(self, session_id: str, qr_version: int) -> None:
        key = (session_id, qr_version)
        with self._lock:
            existing = self._workers.get(key)
            if existing and existing[0].is_alive():
                return
            stop_event = threading.Event()
            thread = threading.Thread(
                target=self._poll_worker,
                args=(session_id, qr_version, stop_event),
                name=f'wechat-login-{session_id[-8:]}-{qr_version}',
                daemon=True,
            )
            self._workers[key] = (thread, stop_event)
            thread.start()

    def _poll_worker(self, session_id: str, qr_version: int, stop_event: threading.Event) -> None:
        consecutive_errors = 0
        try:
            while not self._shutdown.is_set() and not stop_event.is_set():
                row = self._store.get_session_internal(session_id)
                if not row or row['qr_version'] != qr_version or row['status'] not in ACTIVE_SESSION_STATUSES:
                    return
                if row['expires_at'] <= _utc_now():
                    self._store.mark_expired(session_id, qr_version)
                    return
                state = self._decrypt_session_state(row)
                try:
                    result = self._wechat.poll_login_status(
                        str(state.get('qrcode') or ''),
                        str(state.get('base_url') or self._settings.wechat_ilink_base_url),
                        str(state.get('verify_code') or ''),
                    )
                    consecutive_errors = 0
                except WeChatError as exc:
                    consecutive_errors += 1
                    _logger.warning(
                        'wechat_poll_failed session_id=%s attempt=%s error=%s',
                        session_id,
                        consecutive_errors,
                        exc,
                    )
                    if consecutive_errors >= self._settings.wechat_max_consecutive_errors:
                        self._store.mark_failed(
                            session_id,
                            qr_version,
                            code='WECHAT_UNAVAILABLE',
                            message='微信登录状态查询失败，请刷新二维码后重试',
                            retryable=True,
                        )
                        return
                    stop_event.wait(2)
                    continue
                provider_status = str(result.get('status') or '')
                if provider_status == 'wait':
                    continue
                if provider_status == 'scaned':
                    state['verify_code'] = ''
                    self._store.update_active_session(
                        session_id=session_id,
                        qr_version=qr_version,
                        status='scanned',
                        message='二维码已扫描，请在手机微信中确认',
                        state_ciphertext=self._cipher.encrypt(str(row['owner_user_id']), state),
                    )
                    continue
                if provider_status == 'need_verifycode':
                    state['verify_code'] = ''
                    self._store.update_active_session(
                        session_id=session_id,
                        qr_version=qr_version,
                        status='verification_required',
                        message='请输入手机微信中显示的数字',
                        state_ciphertext=self._cipher.encrypt(str(row['owner_user_id']), state),
                    )
                    continue
                if provider_status == 'scaned_but_redirect':
                    redirect_host = self._validated_redirect_host(str(result.get('redirect_host') or ''))
                    if not redirect_host:
                        self._store.mark_failed(
                            session_id,
                            qr_version,
                            code='INVALID_WECHAT_REDIRECT',
                            message='微信登录返回了无效的服务地址',
                            retryable=True,
                        )
                        return
                    state['base_url'] = f'https://{redirect_host}'
                    self._store.update_active_session(
                        session_id=session_id,
                        qr_version=qr_version,
                        status='confirming',
                        message='正在完成微信登录',
                        state_ciphertext=self._cipher.encrypt(str(row['owner_user_id']), state),
                    )
                    continue
                if provider_status == 'confirmed':
                    self._handle_confirmed(row, result)
                    return
                if provider_status == 'expired':
                    self._store.mark_expired(session_id, qr_version)
                    return
                if provider_status == 'verify_code_blocked':
                    self._store.mark_failed(
                        session_id,
                        qr_version,
                        code='VERIFICATION_BLOCKED',
                        message='数字验证尝试次数过多，请稍后刷新二维码重试',
                        retryable=True,
                    )
                    return
                if provider_status == 'binded_redirect':
                    self._store.mark_failed(
                        session_id,
                        qr_version,
                        code='ACCOUNT_ALREADY_BOUND',
                        message='该微信 ClawBot 已绑定，请使用原有凭据或重新创建',
                        retryable=False,
                    )
                    return
                _logger.info(
                    'wechat_unknown_login_status session_id=%s status=%s',
                    session_id,
                    provider_status or '<empty>',
                )
                stop_event.wait(1)
        finally:
            with self._lock:
                self._workers.pop((session_id, qr_version), None)

    def _handle_confirmed(self, row: dict[str, Any], result: dict[str, Any]) -> None:
        token = str(result.get('bot_token') or '')
        provider_account_id = str(result.get('ilink_bot_id') or '')
        authorized_user_id = str(result.get('ilink_user_id') or '')
        base_url = str(result.get('baseurl') or self._settings.wechat_ilink_base_url)
        if (
            not token
            or not provider_account_id
            or not authorized_user_id
            or not self._valid_wechat_base_url(base_url)
        ):
            self._store.mark_failed(
                row['id'],
                row['qr_version'],
                code='INVALID_WECHAT_CREDENTIALS',
                message='微信登录成功响应缺少必要凭据',
                retryable=True,
            )
            return
        credentials = {
            'token': token,
            'account_id': provider_account_id,
            'authorized_user_id': authorized_user_id,
            'base_url': base_url.rstrip('/'),
            'saved_at': _utc_now().isoformat(),
        }
        external_id_hash = hashlib.sha256(provider_account_id.encode('utf-8')).hexdigest()
        account = self._store.save_connected_account(
            session_id=row['id'],
            qr_version=row['qr_version'],
            owner_user_id=row['owner_user_id'],
            provider='wechat',
            external_id_hash=external_id_hash,
            label='微信 ClawBot',
            credentials_ciphertext=self._cipher.encrypt(str(row['owner_user_id']), credentials),
        )
        if account:
            _logger.info(
                'wechat_login_connected session_id=%s account_id=%s owner_user_id=%s',
                row['id'],
                account['id'],
                row['owner_user_id'],
            )
            if self._on_account_connected:
                self._on_account_connected(str(account['id']))

    def _decrypt_session_state(self, row: dict[str, Any]) -> dict[str, Any]:
        ciphertext = str(row.get('provider_state_ciphertext') or '')
        if not ciphertext:
            raise GatewayError(409, 'INVALID_STATE', '连接会话缺少登录状态，请刷新二维码')
        try:
            return self._cipher.decrypt(str(row['owner_user_id']), ciphertext)
        except Exception as exc:
            raise GatewayError(500, 'STATE_DECRYPT_FAILED', '连接会话状态无法读取') from exc

    @staticmethod
    def _validated_redirect_host(host: str) -> str | None:
        normalized = host.strip().lower().rstrip('.')
        if not normalized or not _REDIRECT_HOST_RE.fullmatch(normalized):
            return None
        if normalized == 'weixin.qq.com' or normalized.endswith('.weixin.qq.com'):
            return normalized
        return None

    @staticmethod
    def _valid_wechat_base_url(value: str) -> bool:
        parsed = urlparse(value)
        hostname = (parsed.hostname or '').lower().rstrip('.')
        return (
            parsed.scheme == 'https'
            and (hostname == 'weixin.qq.com' or hostname.endswith('.weixin.qq.com'))
        )

    def _session_view(self, row: dict[str, Any]) -> dict[str, Any]:
        status = str(row['status'])
        qr = None
        if status in ACTIVE_SESSION_STATUSES and row.get('provider_state_ciphertext'):
            state = self._decrypt_session_state(row)
            qr_payload = str(state.get('qr_payload') or '')
            if qr_payload:
                qr = {
                    'payload': qr_payload,
                    'version': row['qr_version'],
                    'expires_at': _iso(row['expires_at']),
                }
        challenge = None
        if status == 'verification_required':
            challenge = {
                'type': 'numeric_code',
                'prompt': '请输入手机微信中显示的数字',
                'input_mode': 'numeric',
            }
        allowed_actions: list[str] = []
        if status in ACTIVE_SESSION_STATUSES:
            allowed_actions.append('cancel')
        if status == 'verification_required':
            allowed_actions.insert(0, 'submit_challenge')
        if status == 'expired' or (status == 'failed' and row.get('error_retryable') is True):
            allowed_actions.append('refresh')
        account = None
        if status == 'connected' and row.get('account_id'):
            account_row = self._store.get_account(row['owner_user_id'], row['account_id'])
            if account_row:
                account = self._account_view(account_row)
        error = None
        if row.get('error_code'):
            error = {
                'code': row['error_code'],
                'message': row.get('error_message') or row['message'],
                'retryable': bool(row.get('error_retryable')),
            }
        return {
            'id': row['id'],
            'provider': row['provider'],
            'mode': 'qr_code',
            'status': status,
            'revision': row['revision'],
            'message': row['message'],
            'qr': qr,
            'challenge': challenge,
            'poll_after_ms': 1000,
            'allowed_actions': allowed_actions,
            'account': account,
            'error': error,
        }

    @staticmethod
    def _account_view(row: dict[str, Any]) -> dict[str, Any]:
        return {
            'id': row['id'],
            'provider': row['provider'],
            'label': row['label'],
            'status': row['status'],
            'runtime_status': row.get('runtime_status') or 'stopped',
            'connected_at': _iso(row.get('connected_at')),
            'last_poll_at': _iso(row.get('last_poll_at')),
            'last_message_at': _iso(row.get('last_message_at')),
            'last_error': row.get('last_error'),
            'updated_at': _iso(row['updated_at']),
        }

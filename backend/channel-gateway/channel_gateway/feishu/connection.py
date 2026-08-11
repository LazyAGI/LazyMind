from __future__ import annotations

import datetime as dt
import logging
import threading
import uuid
from dataclasses import dataclass
from typing import Any

from channel_gateway.common.domain.channel import (
    ACTIVE_CONNECTION_SESSION_STATUSES,
    WELCOME_MESSAGE,
    account_view,
)
from channel_gateway.common.errors import (
    GatewayError,
    RuntimeLeaseLostError,
)
from channel_gateway.common.ports.providers import PayloadCipher
from channel_gateway.common.ports.providers import RuntimeLease
from channel_gateway.feishu.domain import FeishuRuntimeError
from channel_gateway.feishu.ports import (
    FeishuAppRegistrar,
    FeishuConnectionRepository,
    FeishuOutboundFactory,
)
from channel_gateway.feishu.workspace import (
    FeishuWorkspaceState,
)
from channel_gateway.feishu.presentation import FeishuReplyRenderer
from channel_gateway.feishu.domain import FeishuAppCredentials
from channel_gateway.feishu.accounts import FeishuAccountService


_logger = logging.getLogger(__name__)
_SESSION_TTL = dt.timedelta(minutes=10)
_PROVISIONING_TTL = dt.timedelta(minutes=10)
_RUNTIME_READY_TIMEOUT_SECONDS = int(
    _PROVISIONING_TTL.total_seconds()
)
_TERMINAL_STATUSES = {'connected', 'expired', 'canceled', 'failed'}


def _utc_now() -> dt.datetime:
    return dt.datetime.now(dt.timezone.utc)


def _iso(value: dt.datetime | None) -> str | None:
    return value.isoformat() if value else None


def _registration_lease_key(session_id: str) -> str:
    return f'feishu-registration:{session_id}'


def _tls_certificate_error(error: BaseException) -> bool:
    seen: set[int] = set()
    current: BaseException | None = error
    while current is not None and id(current) not in seen:
        seen.add(id(current))
        message = str(current).lower()
        if (
            'certificate_verify_failed' in message
            or 'unable to get local issuer certificate' in message
        ):
            return True
        current = current.__cause__ or current.__context__
    return False


@dataclass(slots=True)
class _RegistrationWorker:
    cancel_event: threading.Event
    thread: threading.Thread | None = None
    lease: RuntimeLease | None = None


class _LeaseKeeper:
    def __init__(self, lease: RuntimeLease):
        self._lease = lease
        self._stop = threading.Event()
        self._error: Exception | None = None
        self._thread = threading.Thread(
            target=self._run,
            name='feishu-registration-lease',
            daemon=True,
        )

    def __enter__(self) -> '_LeaseKeeper':
        self._thread.start()
        return self

    def __exit__(self, *args) -> None:
        self._stop.set()
        self._thread.join(timeout=2)

    def ensure_owned(self) -> None:
        if self._error is not None:
            raise RuntimeLeaseLostError(
                'Feishu registration lease was lost'
            ) from self._error

    def _run(self) -> None:
        while not self._stop.wait(30):
            try:
                self._lease.keepalive()
            except Exception as exc:
                self._error = exc
                self._stop.set()


class FeishuConnectionService:
    """Owns Feishu one-click registration and account provisioning."""

    def __init__(
        self,
        *,
        store: FeishuConnectionRepository,
        cipher: PayloadCipher,
        registrar: FeishuAppRegistrar,
        accounts: FeishuAccountService,
        channels: FeishuOutboundFactory,
    ):
        self._store = store
        self._cipher = cipher
        self._registrar = registrar
        self._accounts = accounts
        self._channels = channels
        self._shutdown = threading.Event()
        self._lock = threading.Lock()
        self._workers: dict[
            tuple[str, int],
            _RegistrationWorker,
        ] = {}
        self._reconciler: threading.Thread | None = None

    def start(self) -> None:
        if self._reconciler and self._reconciler.is_alive():
            return
        self._reconcile_sessions()
        self._reconciler = threading.Thread(
            target=self._reconcile_loop,
            name='feishu-registration-reconciler',
            daemon=True,
        )
        self._reconciler.start()

    def stop(self) -> None:
        self._shutdown.set()
        if (
            self._reconciler
            and self._reconciler is not threading.current_thread()
        ):
            self._reconciler.join(timeout=3)
        with self._lock:
            workers = list(self._workers.values())
        for worker in workers:
            worker.cancel_event.set()
            if worker.lease is not None:
                worker.lease.close()
        for worker in workers:
            if worker.thread is not None:
                worker.thread.join(timeout=2)
        self._reconciler = None

    def _reconcile_loop(self) -> None:
        while not self._shutdown.wait(2):
            try:
                self._reconcile_sessions()
            except Exception:
                _logger.exception(
                    'feishu_registration_reconcile_failed'
                )

    def _reconcile_sessions(self) -> None:
        self._accounts.cleanup_orphaned_provisioning()
        now = _utc_now()
        for row in self._store.recoverable_sessions('feishu'):
            if row['status'] not in ACTIVE_CONNECTION_SESSION_STATUSES:
                self._cleanup_interrupted_registration(row)
                continue
            if row['expires_at'] <= now:
                lease = self._store.acquire_runtime_lease(
                    _registration_lease_key(
                        str(row['id']),
                    )
                )
                if lease is None:
                    continue
                try:
                    current = self._store.get_session_internal(
                        str(row['id'])
                    )
                    if (
                        current
                        and current['status']
                        in ACTIVE_CONNECTION_SESSION_STATUSES
                        and current['expires_at'] <= _utc_now()
                    ):
                        expired = self._store.mark_expired(
                            str(row['id']),
                            int(row['qr_version']),
                        )
                        if expired:
                            self._cleanup_interrupted_registration(
                                expired,
                                runtime_lease=lease,
                            )
                finally:
                    lease.close()
                continue
            self._start_worker(row['id'], row['qr_version'])

    def create_session(
        self,
        *,
        owner_user_id: str,
        idempotency_key: str | None,
    ) -> dict[str, Any]:
        key = (idempotency_key or '').strip()
        if len(key) > 128:
            raise GatewayError(
                422,
                'INVALID_IDEMPOTENCY_KEY',
                'Idempotency-Key 长度不能超过 128 个字符',
            )
        session_id = f'cs_{uuid.uuid4().hex}'
        row, created = self._store.reserve_session(
            session_id=session_id,
            owner_user_id=owner_user_id,
            provider='feishu',
            idempotency_key=key or None,
            expires_at=_utc_now() + _SESSION_TTL,
        )
        if created:
            self._start_worker(session_id, row['qr_version'])
        return self._session_view(row)

    def get_session(
        self,
        owner_user_id: str,
        session_id: str,
    ) -> dict[str, Any]:
        row = self._store.get_session(owner_user_id, session_id)
        if not row:
            raise GatewayError(
                404,
                'LOGIN_NOT_FOUND',
                '连接会话不存在',
            )
        return self._session_view(row)

    def submit_challenge(
        self,
        *,
        owner_user_id: str,
        session_id: str,
        challenge_type: str,
        value: str,
    ) -> dict[str, Any]:
        del challenge_type, value
        row = self._store.get_session(owner_user_id, session_id)
        if not row:
            raise GatewayError(
                404,
                'LOGIN_NOT_FOUND',
                '连接会话不存在',
            )
        raise GatewayError(
            409,
            'INVALID_STATE',
            '飞书扫码连接不需要数字验证',
        )

    def refresh_session(
        self,
        owner_user_id: str,
        session_id: str,
    ) -> dict[str, Any]:
        row = self._store.get_session(owner_user_id, session_id)
        if not row:
            raise GatewayError(
                404,
                'LOGIN_NOT_FOUND',
                '连接会话不存在',
            )
        lease = self._store.acquire_runtime_lease(
            _registration_lease_key(
                session_id,
            )
        )
        if lease is None:
            raise GatewayError(
                409,
                'REGISTRATION_BUSY',
                '飞书连接正在清理，请稍后刷新二维码',
                retryable=True,
            )
        try:
            updated = self._store.restart_connection_session(
                owner_user_id=owner_user_id,
                session_id=session_id,
                expires_at=_utc_now() + _SESSION_TTL,
            )
        finally:
            lease.close()
        if not updated:
            raise GatewayError(
                409,
                'INVALID_STATE',
                '当前连接会话不能刷新二维码',
            )
        self._start_worker(session_id, updated['qr_version'])
        return self._session_view(updated)

    def cancel_session(
        self,
        owner_user_id: str,
        session_id: str,
    ) -> None:
        row = self._store.get_session(owner_user_id, session_id)
        if not row:
            raise GatewayError(
                404,
                'LOGIN_NOT_FOUND',
                '连接会话不存在',
            )
        if row['status'] in _TERMINAL_STATUSES:
            raise GatewayError(
                409,
                'INVALID_STATE',
                '连接会话已经结束',
            )
        if not self._store.cancel_session(
            owner_user_id,
            session_id,
        ):
            raise GatewayError(
                409,
                'INVALID_STATE',
                '连接会话状态已经变化',
            )
        with self._lock:
            worker = self._workers.get(
                (session_id, row['qr_version'])
            )
        if worker is not None:
            worker.cancel_event.set()

    def _start_worker(
        self,
        session_id: str,
        qr_version: int,
    ) -> None:
        key = (session_id, qr_version)
        with self._lock:
            current = self._workers.get(key)
            if current and current.thread and current.thread.is_alive():
                return
            worker = _RegistrationWorker(
                cancel_event=threading.Event()
            )
            worker.thread = threading.Thread(
                target=self._run_worker,
                args=(session_id, qr_version, worker),
                name=(
                    f'feishu-register-{session_id[-8:]}-'
                    f'{qr_version}'
                ),
                daemon=True,
            )
            self._workers[key] = worker
            worker.thread.start()

    def _run_worker(
        self,
        session_id: str,
        qr_version: int,
        worker: _RegistrationWorker,
    ) -> None:
        lease = None
        try:
            while (
                not self._shutdown.is_set()
                and not worker.cancel_event.is_set()
            ):
                lease = self._store.acquire_runtime_lease(
                    _registration_lease_key(session_id)
                )
                if lease is not None:
                    break
                worker.cancel_event.wait(2)
            if lease is None:
                return
            with self._lock:
                worker.lease = lease
            row = self._store.get_session_internal(session_id)
            if (
                not row
                or row['qr_version'] != qr_version
                or row['status']
                not in ACTIVE_CONNECTION_SESSION_STATUSES
            ):
                return
            if row.get('provider_state_ciphertext'):
                self._store.mark_failed(
                    session_id,
                    qr_version,
                    code='REGISTRATION_INTERRUPTED',
                    message=(
                        '飞书扫码注册已中断，请刷新二维码后重试'
                    ),
                    retryable=True,
                )
                self._cleanup_interrupted_registration(
                    row,
                    runtime_lease=lease,
                )
                return
            with _LeaseKeeper(lease) as keeper:
                registration = self._registrar.register(
                    on_qr_code=lambda url, expire_in: (
                        self._on_qr_code(
                            session_id,
                            qr_version,
                            url,
                            expire_in,
                        )
                    ),
                    on_status_change=lambda status: (
                        self._on_status_change(
                            session_id,
                            qr_version,
                            status,
                        )
                    ),
                    cancel_event=worker.cancel_event,
                )
                keeper.ensure_owned()
            if (
                self._shutdown.is_set()
                or worker.cancel_event.is_set()
            ):
                return
            row = self._store.get_session_internal(session_id)
            if (
                not row
                or row['qr_version'] != qr_version
                or row['status']
                not in ACTIVE_CONNECTION_SESSION_STATUSES
            ):
                return
            state = self._decrypt_state(row)
            updated = self._store.update_active_session(
                session_id=session_id,
                qr_version=qr_version,
                expected_revision=row['revision'],
                status='confirming',
                message=(
                    '正在完成飞书配置，首次连接可能需要几分钟'
                ),
                state_ciphertext=self._cipher.encrypt(
                    str(row['owner_user_id']),
                    state,
                ),
                expires_at=_utc_now() + _PROVISIONING_TTL,
            )
            if not updated:
                return
            with _LeaseKeeper(lease) as keeper:
                self._complete_registration(
                    updated,
                    registration,
                    keeper,
                    lease.fence,
                )
                keeper.ensure_owned()
        except RuntimeLeaseLostError:
            _logger.warning(
                'feishu_registration_lease_lost session_id=%s',
                session_id,
            )
        except FeishuRuntimeError as exc:
            _logger.warning(
                'feishu_registration_failed session_id=%s error=%s',
                session_id,
                exc,
            )
            self._fail_registration(
                session_id,
                qr_version,
                exc,
                runtime_lease=lease,
            )
        except Exception as exc:
            _logger.exception(
                'feishu_registration_failed session_id=%s',
                session_id,
            )
            self._fail_registration(
                session_id,
                qr_version,
                exc,
                runtime_lease=lease,
            )
        finally:
            if lease is not None:
                lease.close()
            with self._lock:
                worker.lease = None
                if self._workers.get(
                    (session_id, qr_version)
                ) is worker:
                    self._workers.pop(
                        (session_id, qr_version),
                        None,
                    )

    def _fail_registration(
        self,
        session_id: str,
        qr_version: int,
        error: BaseException,
        *,
        runtime_lease: RuntimeLease | None,
    ) -> None:
        if _tls_certificate_error(error):
            code = 'TLS_CERTIFICATE_VERIFY_FAILED'
            message = (
                '无法验证飞书服务的 HTTPS 证书，'
                '请检查系统证书或企业网络设置'
            )
            retryable = False
        else:
            code = 'FEISHU_REGISTRATION_FAILED'
            message = '飞书连接失败，请刷新二维码后重试'
            retryable = True
        self._store.mark_failed(
            session_id,
            qr_version,
            code=code,
            message=message,
            retryable=retryable,
        )
        self._cleanup_interrupted_session(
            session_id,
            qr_version,
            runtime_lease=runtime_lease,
        )

    def _on_qr_code(
        self,
        session_id: str,
        qr_version: int,
        url: str,
        expire_in: int,
    ) -> None:
        row = self._store.get_session_internal(session_id)
        if (
            not row
            or row['qr_version'] != qr_version
            or row['status'] != 'preparing'
        ):
            raise FeishuRuntimeError(
                'Feishu registration session is no longer active'
            )
        expires_at = min(
            row['expires_at'],
            _utc_now() + dt.timedelta(seconds=expire_in),
        )
        state = {'qr_payload': url}
        updated = self._store.set_qr_ready(
            session_id,
            self._cipher.encrypt(
                str(row['owner_user_id']),
                state,
            ),
            expires_at,
            '请使用飞书扫码并确认创建 LazyMind 助手',
        )
        if not updated:
            raise FeishuRuntimeError(
                'Feishu registration session changed'
            )

    def _on_status_change(
        self,
        session_id: str,
        qr_version: int,
        status: str,
    ) -> None:
        _logger.info(
            'feishu_registration_status session_id=%s '
            'qr_version=%s status=%s',
            session_id,
            qr_version,
            status,
        )

    def _complete_registration(
        self,
        row: dict[str, Any],
        registration,
        keeper: _LeaseKeeper,
        runtime_fence,
    ) -> None:
        owner_user_id = str(row['owner_user_id'])
        cleanup_started = self._store.begin_provisioning_cleanup(
            str(row['id']),
            int(row['qr_version']),
            runtime_fence=runtime_fence,
        )
        if not cleanup_started:
            raise FeishuRuntimeError(
                'Feishu connection session changed during provisioning'
            )
        credentials = FeishuAppCredentials(
            app_id=registration.app_id,
            app_secret=registration.app_secret,
            provider_account_id=registration.owner_open_id,
            provider_tenant_key=registration.tenant_key,
            display_name=registration.owner_name,
        )
        account = self._accounts.connect_registered_account(
            owner_user_id=owner_user_id,
            credentials=credentials,
            runtime_fence=runtime_fence,
            notify_runtime=False,
        )
        account_id = str(account['id'])
        attached = self._store.attach_provisioning_account(
            session_id=str(row['id']),
            qr_version=int(row['qr_version']),
            owner_user_id=owner_user_id,
            account_id=account_id,
            runtime_fence=runtime_fence,
        )
        if not attached:
            self._accounts.discard_provisioned_account(
                owner_user_id=owner_user_id,
                account_id=account_id,
                runtime_fence=runtime_fence,
            )
            raise FeishuRuntimeError(
                'Feishu connection session changed during provisioning'
            )
        try:
            keeper.ensure_owned()
            if not self._store.claim_welcome(account_id):
                raise FeishuRuntimeError(
                    'Feishu welcome state is unavailable'
                )
            keeper.ensure_owned()
            self._accounts.start_account_runtime(account_id)
            self._wait_for_runtime(owner_user_id, account_id)
            keeper.ensure_owned()
            self._open_direct_chat(
                credentials=credentials,
                account_id=account_id,
            )
            keeper.ensure_owned()
            updated = self._store.complete_provisioned_connection(
                session_id=str(row['id']),
                qr_version=int(row['qr_version']),
                owner_user_id=owner_user_id,
                account_id=account_id,
                message='飞书连接成功，LazyMind 单聊已创建',
                runtime_fence=runtime_fence,
            )
            if not updated:
                raise FeishuRuntimeError(
                    'Feishu connection session changed during provisioning'
                )
        except RuntimeLeaseLostError:
            raise
        except Exception:
            keeper.ensure_owned()
            self._accounts.discard_provisioned_account(
                owner_user_id=owner_user_id,
                account_id=account_id,
                runtime_fence=runtime_fence,
            )
            raise

    def _open_direct_chat(
        self,
        *,
        credentials: FeishuAppCredentials,
        account_id: str,
    ) -> None:
        sender = self._channels.create_sender(credentials)
        try:
            workspace = FeishuWorkspaceState()
            sender.send_card_to_user(
                open_id=credentials.provider_account_id,
                card=FeishuReplyRenderer.render(
                    provider_context={
                        'chat_id': '',
                        'workspace_state': workspace.to_dict(),
                    },
                    text=f'**👋 欢迎使用 LazyMind**\n\n{WELCOME_MESSAGE}',
                    status='✅ **连接完成**',
                ),
                idempotency_key=(
                    f'feishu-welcome:{account_id}'
                ),
            )
        finally:
            sender.close()

    def _cleanup_interrupted_session(
        self,
        session_id: str,
        qr_version: int,
        runtime_lease: RuntimeLease | None = None,
    ) -> None:
        row = self._store.get_session_internal(session_id)
        if row and int(row['qr_version']) == qr_version:
            self._cleanup_interrupted_registration(
                row,
                runtime_lease=runtime_lease,
            )

    def _cleanup_interrupted_registration(
        self,
        row: dict[str, Any],
        *,
        runtime_lease: RuntimeLease | None = None,
    ) -> None:
        lease = runtime_lease
        owns_lease = lease is None
        if lease is None:
            lease = self._store.acquire_runtime_lease(
                _registration_lease_key(
                    str(row['id']),
                )
            )
        if lease is None:
            return
        try:
            with _LeaseKeeper(lease) as keeper:
                current = self._store.get_session_internal(
                    str(row['id'])
                )
                if (
                    not current
                    or int(current['qr_version'])
                    != int(row['qr_version'])
                    or str(current.get('status') or '')
                    in ACTIVE_CONNECTION_SESSION_STATUSES
                    or str(current.get('status') or '') == 'connected'
                ):
                    return
                owner_user_id = str(current['owner_user_id'])
                account_id = str(current.get('account_id') or '')
                keeper.ensure_owned()
                if account_id:
                    cleaned = self._accounts.discard_provisioned_account(
                        owner_user_id=owner_user_id,
                        account_id=account_id,
                        runtime_fence=lease.fence,
                    )
                else:
                    cleaned = True
                keeper.ensure_owned()
                if cleaned:
                    self._store.complete_provisioning_cleanup(
                        str(current['id']),
                        int(current['qr_version']),
                        runtime_fence=lease.fence,
                    )
        finally:
            if owns_lease:
                lease.close()

    def _wait_for_runtime(
        self,
        owner_user_id: str,
        account_id: str,
    ) -> None:
        deadline = (
            dt.datetime.now().timestamp()
            + _RUNTIME_READY_TIMEOUT_SECONDS
        )
        last_error = ''
        while dt.datetime.now().timestamp() < deadline:
            if self._shutdown.is_set():
                raise FeishuRuntimeError(
                    'Feishu registration is stopping'
                )
            account = self._store.get_account(
                owner_user_id,
                account_id,
            )
            if not account:
                raise FeishuRuntimeError(
                    'Provisioned Feishu account disappeared'
                )
            if account.get('runtime_status') == 'running':
                return
            if account.get('runtime_status') == 'failed':
                last_error = str(
                    account.get('last_error')
                    or '飞书长连接启动失败'
                )
            self._shutdown.wait(0.5)
        message = '飞书长连接未能在规定时间内就绪'
        if last_error:
            message = f'{message}: {last_error}'
        raise FeishuRuntimeError(message)

    def _decrypt_state(
        self,
        row: dict[str, Any],
    ) -> dict[str, Any]:
        ciphertext = str(
            row.get('provider_state_ciphertext') or ''
        )
        if not ciphertext:
            return {}
        try:
            return self._cipher.decrypt(
                str(row['owner_user_id']),
                ciphertext,
            )
        except Exception as exc:
            raise FeishuRuntimeError(
                'Feishu registration state cannot be read'
            ) from exc

    def _session_view(
        self,
        row: dict[str, Any],
    ) -> dict[str, Any]:
        status = str(row['status'])
        qr = None
        if (
            status in ACTIVE_CONNECTION_SESSION_STATUSES
            and row.get('provider_state_ciphertext')
        ):
            state = self._decrypt_state(row)
            payload = str(state.get('qr_payload') or '')
            if payload:
                qr = {
                    'payload': payload,
                    'version': row['qr_version'],
                    'expires_at': _iso(row['expires_at']),
                }
        allowed_actions: list[str] = []
        if status in ACTIVE_CONNECTION_SESSION_STATUSES:
            allowed_actions.append('cancel')
        if status == 'expired' or (
            status == 'failed'
            and row.get('error_retryable') is True
        ):
            allowed_actions.append('refresh')
        account = None
        if status == 'connected' and row.get('account_id'):
            account_row = self._store.get_account(
                str(row['owner_user_id']),
                str(row['account_id']),
            )
            if account_row:
                account = account_view(account_row)
        error = None
        if row.get('error_code'):
            error = {
                'code': row['error_code'],
                'message': (
                    row.get('error_message')
                    or row['message']
                ),
                'retryable': bool(row.get('error_retryable')),
            }
        return {
            'id': row['id'],
            'provider': 'feishu',
            'mode': 'qr_code',
            'status': status,
            'revision': row['revision'],
            'message': row['message'],
            'qr': qr,
            'challenge': None,
            'poll_after_ms': 1000,
            'allowed_actions': allowed_actions,
            'account': account,
            'error': error,
        }

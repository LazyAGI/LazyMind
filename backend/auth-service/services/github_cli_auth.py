from concurrent.futures import ThreadPoolExecutor
from dataclasses import dataclass, field
from datetime import datetime, timedelta, timezone
import os
import re
import sys
import threading
import time
import uuid

from core.errors import AppException, ErrorCodes, raise_error
from services.cloud_oauth_service import cloud_oauth_service
from services.github_cli_runner import AUTH_TIMEOUT, DEVICE_URL, GitHubCLIError, GitHubCLIRunner


_TERMINAL = {'COMPLETED', 'CANCELLED', 'EXPIRED', 'FAILED'}


@dataclass
class _Session:
    owner: str
    attempt: str
    target: str
    deadline: float
    force_web: bool = False
    session_id: str = field(default_factory=lambda: uuid.uuid4().hex)
    status: str = 'PROBING'
    user_code: str = ''
    verification_uri: str = ''
    connection_id: str = ''
    error_code: str = ''
    cancel: threading.Event = field(default_factory=threading.Event)
    aliases: set[str] = field(default_factory=set)
    cancelled_before_start: bool = False


class GitHubCLIAuthService:
    def __init__(self, *, runner, connections, executor=None, clock=time.monotonic, host_credentials_enabled=True,
                 web_authorization_enabled=None):
        self.runner = runner
        self.connections = connections
        self.executor = executor
        self.clock = clock
        self.host_credentials_enabled = host_credentials_enabled
        self.web_authorization_enabled = (host_credentials_enabled if web_authorization_enabled is None
                                          else web_authorization_enabled)
        self._lock = threading.RLock()
        self._sessions = {}
        self._recovery_ready = threading.Event()
        self._recovery_ready.set()
        self._stopping = threading.Event()
        self._recovery_thread = None
        self._recovery_failed = False

    def start_recovery(self):
        if not self.web_authorization_enabled or self._recovery_thread is not None:
            return
        self._recovery_ready.clear()

        def recover():
            while not self._stopping.is_set():
                try:
                    self.runner.recover()
                except GitHubCLIError as exc:
                    if exc.code == 'GITHUB_CLI_BUSY':
                        self._stopping.wait(1)
                        continue
                    self._recovery_failed = True
                    return
                except Exception:
                    self._recovery_failed = True
                    return
                self._recovery_ready.set()
                return

        self._recovery_thread = threading.Thread(target=recover, name='github-recovery', daemon=True)
        self._recovery_thread.start()

    def _finish(self, session, status, code=''):
        session.status, session.error_code = status, code
        session.user_code = session.verification_uri = ''
        if status in {'CANCELLED', 'EXPIRED'}:
            session.cancel.set()

    def _check(self, session):
        if session.status in _TERMINAL:
            return False
        if self.clock() >= session.deadline:
            self._finish(session, 'EXPIRED', 'GITHUB_AUTH_TIMEOUT')
            return False
        return True

    def _snapshot(self, session):
        self._check(session)
        result = {
            'session_id': session.session_id, 'status': session.status,
            'expires_at': (datetime.now(timezone.utc) + timedelta(
                seconds=max(0, session.deadline - self.clock()),
            )).isoformat(),
        }
        if session.status == 'WAITING_USER':
            result.update(user_code=session.user_code, verification_uri=session.verification_uri)
        if session.connection_id:
            result['connection_id'] = session.connection_id
        if session.error_code:
            result['error_code'] = session.error_code
        return result

    def _owned(self, session_id, owner_user_id):
        if not owner_user_id:
            raise_error(ErrorCodes.UNAUTHORIZED)
        session = self._sessions.get(session_id)
        if session is None:
            raise_error(ErrorCodes.GITHUB_CLI_SESSION_NOT_FOUND)
        if session.owner != owner_user_id:
            raise_error(ErrorCodes.FORBIDDEN)
        return session

    def _prune(self):
        for key, session in list(self._sessions.items()):
            self._check(session)
            if self.clock() >= session.deadline + 300 and session.status in _TERMINAL:
                del self._sessions[key]

    def _check_capacity(self):
        if sum(1 + len(session.aliases) for session in self._sessions.values()) >= 256:
            raise_error(ErrorCodes.GITHUB_CLI_BUSY)

    @staticmethod
    def _attempt_owner(attempt_id, owner_user_id):
        owner = (owner_user_id or '').strip()
        if not owner:
            raise_error(ErrorCodes.UNAUTHORIZED)
        if not isinstance(attempt_id, str) or not 1 <= len(attempt_id) <= 128:
            raise_error(ErrorCodes.INVALID_REQUEST)
        return owner

    def _find_attempt(self, attempt_id, owner):
        return next((session for session in self._sessions.values() if session.owner == owner
                     and (session.attempt == attempt_id or attempt_id in session.aliases)), None)

    def get_attempt(self, attempt_id, *, owner_user_id):
        owner = self._attempt_owner(attempt_id, owner_user_id)
        with self._lock:
            self._prune()
            session = self._find_attempt(attempt_id, owner)
            if session is None:
                raise_error(ErrorCodes.GITHUB_CLI_SESSION_NOT_FOUND)
            return self._snapshot(session)

    def cancel_attempt(self, attempt_id, *, owner_user_id):
        owner = self._attempt_owner(attempt_id, owner_user_id)
        with self._lock:
            self._prune()
            session = self._find_attempt(attempt_id, owner)
            if session is None:
                # The create request may still be in transit. Retain cancellation
                # under the same owner/attempt so a late POST cannot start login.
                self._check_capacity()
                session = _Session(owner, attempt_id, '', self.clock() + AUTH_TIMEOUT,
                                   cancelled_before_start=True)
                self._sessions[session.session_id] = session
            if self._check(session):
                self._finish(session, 'CANCELLED')
            return self._snapshot(session)

    def start(self, *, owner_user_id, attempt_id, reauthorize_connection_id='', force_web=False):
        owner = self._attempt_owner(attempt_id, owner_user_id)
        if not self.web_authorization_enabled:
            raise_error(ErrorCodes.GITHUB_CLI_DISABLED)
        if self._recovery_failed:
            raise_error(
                ErrorCodes.GITHUB_CLI_FAILED if self.host_credentials_enabled else ErrorCodes.GITHUB_CLI_DISABLED
            )
        if not self._recovery_ready.is_set():
            raise_error(ErrorCodes.GITHUB_CLI_BUSY)
        with self._lock:
            self._prune()
            existing = self._find_attempt(attempt_id, owner)
            if existing is not None:
                if not existing.cancelled_before_start and (
                    existing.target != reauthorize_connection_id or existing.force_web != force_web
                ):
                    raise_error(ErrorCodes.INVALID_REQUEST)
                return self._snapshot(existing)
            for session in self._sessions.values():
                if session.owner == owner:
                    if session.target == reauthorize_connection_id and session.status not in _TERMINAL:
                        if session.force_web != force_web:
                            raise_error(ErrorCodes.GITHUB_CLI_BUSY)
                        self._check_capacity()
                        session.aliases.add(attempt_id)
                        return self._snapshot(session)
            self._check_capacity()
            if reauthorize_connection_id:
                target = self.connections.get_connection(reauthorize_connection_id, user_id=owner)
                if target['provider'] != 'github' or target.get('token_source') != 'github_cli':
                    raise_error(ErrorCodes.GITHUB_CLI_SOURCE_CONFLICT)
                if target['status'] == 'REVOKED':
                    raise_error(ErrorCodes.CLOUD_CONNECTION_NOT_FOUND)
            session = _Session(owner, attempt_id, reauthorize_connection_id, self.clock() + AUTH_TIMEOUT,
                               force_web=force_web)
            self._sessions[session.session_id] = session
            if self.executor is None:
                self.executor = ThreadPoolExecutor(max_workers=2, thread_name_prefix='github-authorization')
            try:
                self.executor.submit(self._run, session, force_web)
            except Exception:
                self._finish(session, 'FAILED', 'GITHUB_CLI_UNAVAILABLE')
            return self._snapshot(session)

    def get(self, session_id, *, owner_user_id):
        with self._lock:
            return self._snapshot(self._owned(session_id, owner_user_id))

    def cancel(self, session_id, *, owner_user_id):
        with self._lock:
            session = self._owned(session_id, owner_user_id)
            if self._check(session):
                self._finish(session, 'CANCELLED')
            return self._snapshot(session)

    def disconnect(self, connection_id, *, owner_user_id):
        with self._lock:
            self.connections.get_connection(connection_id, user_id=owner_user_id)
            for session in self._sessions.values():
                # Unbound attempts may resolve to this account during the later identity check.
                if session.owner == owner_user_id and session.target in ('', connection_id) and self._check(session):
                    self._finish(session, 'CANCELLED')
            return self.connections.delete_connection(connection_id, user_id=owner_user_id)

    def shutdown(self):
        self._stopping.set()
        if self._recovery_thread is not None:
            self._recovery_thread.join()
        with self._lock:
            for session in self._sessions.values():
                if session.status not in _TERMINAL:
                    self._finish(session, 'CANCELLED')
        if self.executor is not None:
            self.executor.shutdown(wait=True)

    def _run(self, session, force_web):
        try:
            with self._lock:
                if not self._check(session):
                    return
            if not session.target and not force_web:
                items = self.connections.list_connections(owner_user_id=session.owner, provider='github')['items']
                if items:
                    if len(items) != 1:
                        raise GitHubCLIError('GITHUB_ACCOUNT_SELECTION_REQUIRED')
                    result = self.connections.verify_saved_github_connection(
                        items[0]['connection_id'], owner_user_id=session.owner,
                    )
                    with self._lock:
                        if self._check(session):
                            session.connection_id = result['connection_id']
                            self._finish(session, 'COMPLETED')
                    return
            credential = None if force_web or not self.host_credentials_enabled else self.runner.probe()

            def on_device(code, uri, expires_in):
                if uri != DEVICE_URL or not re.fullmatch('[A-Z0-9]{4}-[A-Z0-9]{4}', code):
                    raise GitHubCLIError('GITHUB_CLI_OUTPUT_INVALID')
                with self._lock:
                    if self._check(session):
                        session.deadline = min(session.deadline, self.clock() + max(0, expires_in))
                        session.status, session.user_code, session.verification_uri = 'WAITING_USER', code, uri
            if credential is None:
                credential = self.runner.authorize(cancel_event=session.cancel, on_device=on_device)
            # One lock arbitrates cancellation/disconnect against the entire atomic save.
            with self._lock:
                if not self._check(session):
                    return
                session.status = 'VERIFYING'

                def check_current():
                    if not self._check(session):
                        raise_error(ErrorCodes.GITHUB_CLI_SESSION_NOT_FOUND)
                result = self.connections.save_github_cli_token(
                    owner_user_id=session.owner, access_token=credential['access_token'],
                    reauthorize_connection_id=session.target, _check_current=check_current,
                )
                session.connection_id = result['connection_id']
                self._finish(session, 'COMPLETED')
        except Exception as exc:
            if isinstance(exc, GitHubCLIError):
                code = exc.code
            elif isinstance(exc, AppException):
                code = {
                    ErrorCodes.GITHUB_CLI_NETWORK_UNAVAILABLE[1]: 'GITHUB_NETWORK_UNAVAILABLE',
                    ErrorCodes.GITHUB_CLI_RECONNECT_REQUIRED[1]: 'GITHUB_TOKEN_INVALID',
                    ErrorCodes.GITHUB_CLI_SCOPE_MISSING[1]: 'GITHUB_SCOPE_MISSING',
                    ErrorCodes.GITHUB_CLI_SCOPE_UNVERIFIED[1]: 'GITHUB_SCOPE_UNVERIFIED',
                    ErrorCodes.GITHUB_CLI_FORBIDDEN[1]: 'GITHUB_ACCESS_FORBIDDEN',
                }.get(exc.code, 'GITHUB_AUTH_FAILED')
            else:
                code = 'GITHUB_AUTH_FAILED'
            with self._lock:
                if self._check(session):
                    self._finish(session, 'EXPIRED' if code == 'GITHUB_AUTH_TIMEOUT' else 'FAILED', code)


_single_worker = os.environ.get('WEB_CONCURRENCY', '1') == '1'
_host_credentials_enabled = sys.platform == 'darwin' and os.environ.get('LAZYMIND_RUNTIME_MODE') == 'local'
_container_web_enabled = sys.platform == 'linux' and os.environ.get('LAZYMIND_GITHUB_CLI_CONTAINER_ENABLED') == '1'

github_cli_auth_service = GitHubCLIAuthService(
    runner=GitHubCLIRunner.from_runtime(), connections=cloud_oauth_service,
    host_credentials_enabled=_host_credentials_enabled and _single_worker,
    web_authorization_enabled=(_host_credentials_enabled or _container_web_enabled) and _single_worker,
)

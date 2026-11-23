"""One durable, account-bound support intent for the fixed LazyMind project."""
import hashlib
import hmac
import json
import logging
import threading
from datetime import datetime, timedelta, timezone
from uuid import UUID

from core.cloud_crypto import github_star_binding_key
from core.errors import AppException, ErrorCodes, raise_error
from core.permissions import get_effective_permission_codes
from models import CloudAuthConnection, GitHubStarIntent
from repositories import UserRepository
from services.github_cli_runner import GitHubCLIError
from services.github_star_service import GitHubStarError, REPOSITORY, _STAR_PATH, _error


_INTERVAL = timedelta(hours=6)
_LIFETIME = timedelta(days=7)
_MAX_ATTEMPTS = 28
_LOGGER = logging.getLogger(__name__)


def _utc(value):
    return value.replace(tzinfo=timezone.utc) if value.tzinfo is None else value.astimezone(timezone.utc)


class _Stopped(Exception):
    pass


class GitHubStarDeferredService:
    def __init__(self, *, session_factory, star_service, probe, enabled,
                 clock=lambda: datetime.now(timezone.utc)):
        self.sessions = session_factory
        self.star = star_service
        self.probe = probe
        self.enabled = enabled
        self.clock = clock
        self._lock = threading.RLock()
        self._running = set()
        self._wake = threading.Event()
        self._stopping = threading.Event()
        self._thread = None

    def _now(self):
        return _utc(self.clock())

    @staticmethod
    def _snapshot(row):
        if row is None:
            return None
        result = {name: getattr(row, name) for name in (
            'intent_id', 'status', 'source', 'connection_id', 'account_id',
            'attempt_count', 'reason', 'remote_state',
        )}
        for name in ('created_at', 'expires_at', 'next_attempt_at', 'rate_limit_not_before'):
            value = getattr(row, name)
            result[name] = _utc(value).isoformat() if value is not None else None
        return result

    @staticmethod
    def _owned(db, owner):
        return db.query(GitHubStarIntent).filter_by(owner_user_id=owner, repository=REPOSITORY).first()

    def _can_write(self, owner):
        try:
            identity = UUID(owner)
        except (ValueError, TypeError, AttributeError):
            return False
        with self.sessions() as db:
            user = UserRepository.get_by_id(db, identity, load_role=True, load_permission_groups=True,
                                            load_groups=True, load_group_permission_groups=True)
            return bool(user and not user.disabled and (
                user.role.name == 'system-admin' or 'model.write' in get_effective_permission_codes(user)
            ))

    def status(self, *, owner_user_id):
        with self.sessions() as db:
            saved = self._snapshot(self._owned(db, owner_user_id))
            rows = db.query(CloudAuthConnection).filter_by(
                owner_user_id=owner_user_id, provider='github', auth_mode='oauth_user', status='ACTIVE',
            ).all()
            accounts = []
            for row in rows:
                if not row.provider_account_id:
                    continue
                try:
                    meta = json.loads(row.provider_account_meta or '{}')
                except ValueError:
                    meta = {}
                login = meta.get('login') if isinstance(meta, dict) else None
                accounts.append({'connection_id': row.connection_id, 'account_id': row.provider_account_id,
                                 'login': login or row.display_name})
        return {'repository': REPOSITORY, 'can_receive': bool(self.enabled and self._can_write(owner_user_id)),
                'local_supported': bool(self.enabled), 'accounts': accounts, 'intent': saved}

    def receive(self, *, owner_user_id, source, confirmed, connection_id='', expected_account_id=''):
        if confirmed is not True or source not in ('local', 'connection'):
            raise_error(ErrorCodes.INVALID_REQUEST)
        if source == 'local' and (connection_id or expected_account_id):
            raise_error(ErrorCodes.INVALID_REQUEST)
        if source == 'connection' and (not connection_id or not expected_account_id.isdigit()):
            raise_error(ErrorCodes.INVALID_REQUEST)
        with self._lock:
            with self.sessions() as db:
                row = self._owned(db, owner_user_id)
                if row is not None:
                    return {'repository': REPOSITORY, 'accepted': True, 'intent': self._snapshot(row)}
            if not self.enabled:
                raise_error(ErrorCodes.GITHUB_CLI_DISABLED)
            if not self._can_write(owner_user_id):
                raise_error(ErrorCodes.FORBIDDEN)
            if source == 'connection':
                connection = self.star._connection(owner_user_id, connection_id)
                if connection['provider_account_id'] != expected_account_id:
                    raise_error(ErrorCodes.GITHUB_STAR_ACCOUNT_CHANGED)
            now = self._now()
            with self.sessions() as db:
                row = GitHubStarIntent(owner_user_id=owner_user_id, repository=REPOSITORY, source=source,
                                       connection_id=connection_id or None,
                                       account_id=expected_account_id or None,
                                       created_at=now, updated_at=now, expires_at=now + _LIFETIME,
                                       status='needs_attention', reason='binding_not_captured', attempt_count=0,
                                       remote_state='unknown')
                db.add(row)
                db.commit()
                identity = row.intent_id

        # Receipt is committed before capture. A failed/interrupted capture is never resumed by a worker.
        fingerprint, reason = None, 'ready'
        if source == 'local':
            try:
                key = github_star_binding_key()
            except RuntimeError:
                reason = 'binding_key_unavailable'
            else:
                try:
                    credential = self.probe() if self.probe is not None else None
                except GitHubCLIError:
                    credential = None
                token = credential.get('access_token') if isinstance(credential, dict) else None
                if isinstance(token, str) and token:
                    fingerprint = hmac.new(key, token.encode('utf-8'), hashlib.sha256).hexdigest()
                else:
                    reason = 'credential_unavailable'
        with self._lock, self.sessions() as db:
            row = db.get(GitHubStarIntent, identity)
            if row.status == 'needs_attention' and row.reason == 'binding_not_captured':
                row.reason, row.updated_at = reason, self._now()
                if self._now() >= _utc(row.expires_at):
                    row.status, row.reason = 'exhausted', 'expired'
                elif reason == 'ready':
                    row.status, row.credential_fingerprint, row.next_attempt_at = 'pending', fingerprint, now
                db.commit()
            result = {'repository': REPOSITORY, 'accepted': True, 'intent': self._snapshot(row)}
        self._wake.set()
        return result

    def cancel(self, *, owner_user_id, intent_id):
        with self._lock, self.sessions() as db:
            row = self._owned(db, owner_user_id)
            if row is None or row.intent_id != intent_id:
                raise_error(ErrorCodes.RESOURCE_NOT_FOUND)
            if row.status in ('pending', 'needs_attention'):
                row.status, row.reason, row.next_attempt_at = 'cancelled', 'cancelled', None
                row.updated_at = self._now()
                db.commit()
            result = {'repository': REPOSITORY, 'intent': self._snapshot(row)}
        self._wake.set()
        return result

    def _guard(self, identity):
        # This is the dispatch decision; the lock is released before any blocking HTTP.
        with self._lock, self.sessions() as db:
            row = db.get(GitHubStarIntent, identity)
            if self._stopping.is_set() or row.status != 'pending' or identity not in self._running:
                raise _Stopped()
            if self._now() >= _utc(row.expires_at):
                row.status, row.reason, row.next_attempt_at = 'exhausted', 'expired', None
                row.updated_at = self._now()
                db.commit()
                raise _Stopped()
            reason = None
            if not self._can_write(row.owner_user_id):
                reason = 'owner_unavailable'
            elif row.source == 'connection':
                try:
                    connection = self.star._connection(row.owner_user_id, row.connection_id)
                except AppException:
                    reason = 'connection_unavailable'
                else:
                    if connection['provider_account_id'] != row.account_id:
                        reason = 'account_changed'
            if reason is not None:
                row.status, row.reason, row.next_attempt_at = 'needs_attention', reason, None
                row.updated_at = self._now()
                db.commit()
                raise _Stopped()

    def _claim(self, identity):
        with self._lock, self.sessions() as db:
            row = db.get(GitHubStarIntent, identity)
            if self._stopping.is_set() or identity in self._running or row.status != 'pending':
                return False
            now = self._now()
            if now >= _utc(row.expires_at) or row.attempt_count >= _MAX_ATTEMPTS:
                row.status, row.reason, row.next_attempt_at = 'exhausted', 'expired', None
                row.updated_at = now
                db.commit()
                return False
            due = max(_utc(value) for value in (row.next_attempt_at, row.rate_limit_not_before) if value is not None)
            if due > now:
                return False
            row.attempt_count += 1
            row.next_attempt_at, row.updated_at = now + _INTERVAL, now
            db.commit()
            self._running.add(identity)
            return True

    def _finish(self, identity, *, reason, attention=False, starred=False, wait_until=None):
        with self._lock, self.sessions() as db:
            row = db.get(GitHubStarIntent, identity)
            if starred:
                row.remote_state = 'starred'  # A late GET can record evidence without reviving a terminal intent.
            if row.status == 'pending':
                now = self._now()
                row.reason, row.updated_at = reason, now
                if wait_until is not None:
                    row.rate_limit_not_before = wait_until
                if now >= _utc(row.expires_at):
                    row.status, row.reason = 'exhausted', 'expired'
                elif attention:
                    row.status = 'needs_attention'
                elif starred:
                    row.status = 'succeeded'
                elif (row.attempt_count >= _MAX_ATTEMPTS
                      or (wait_until is not None and wait_until >= _utc(row.expires_at))):
                    row.status = 'exhausted'
                if row.status != 'pending':
                    row.next_attempt_at = None
            db.commit()

    def _failure(self, identity, error, *, unknown=False):
        reason, attention, wait_until = 'result_unknown' if unknown else 'remote_unavailable', False, None
        if isinstance(error, GitHubStarError) and error.http_code == 429:
            waits = []
            if error.retry_at is not None:
                waits.append(_utc(error.retry_at))
            if error.retry_after_seconds is not None:
                waits.append(self._now() + timedelta(seconds=error.retry_after_seconds))
            wait_until = max(waits) if waits else None
            reason = 'rate_limited'
        elif isinstance(error, AppException) and (error.http_code in (401, 403, 404, 409, 500)
                                                  or getattr(error, 'remote_status', None) == 404):
            reason, attention = 'credential_unavailable', True
        self._finish(identity, reason=reason, attention=attention, wait_until=wait_until)

    def _execute(self, identity):
        with self.sessions() as db:
            row = db.get(GitHubStarIntent, identity)
            owner, source, connection_id = row.owner_user_id, row.source, row.connection_id
            account_id, fingerprint = row.account_id, row.credential_fingerprint
        try:
            self._guard(identity)
            if source == 'local':
                try:
                    key = github_star_binding_key()
                except RuntimeError:
                    self._finish(identity, reason='binding_key_unavailable', attention=True)
                    return
                self._guard(identity)
                try:
                    credential = self.probe() if self.probe is not None else None
                except GitHubCLIError:
                    credential = None
                token = credential.get('access_token') if isinstance(credential, dict) else None
                tag = hmac.new(key, token.encode('utf-8'), hashlib.sha256).hexdigest() if token else ''
                if not fingerprint or not hmac.compare_digest(fingerprint, tag):
                    self._finish(identity, reason='binding_changed', attention=True)
                    return
            else:
                payload = self.star.connections.get_access_token(connection_id, user_id=owner, allow_refresh=False)
                token = payload.get('access_token')
                if payload['provider'] != 'github' or not token:
                    raise _error(ErrorCodes.GITHUB_STAR_AUTH_REQUIRED, 'reauthorize')

            def guard():
                self._guard(identity)

            verified_id, _ = self.star._inspect_token(token, expected_account_id=account_id, before_request=guard)
            with self._lock, self.sessions() as db:
                self._guard(identity)
                row = db.get(GitHubStarIntent, identity)
                row.account_id, row.updated_at = verified_id, self._now()
                db.commit()
            if self.star._http(_STAR_PATH, token, not_found=True, before_request=guard):
                self._finish(identity, reason='verified', starred=True)
                return
            try:
                self.star._http(_STAR_PATH, token, method='PUT', before_request=guard)
            except GitHubStarError as error:
                if error.http_code in (401, 403, 429) or error.remote_status == 404:
                    raise
                # The write may have committed. Reconcile once, still inside this cycle's gates.
            try:
                verified = self.star._http(_STAR_PATH, token, not_found=True, before_request=guard)
            except GitHubStarError as error:
                self._failure(identity, error, unknown=True)
            else:
                self._finish(identity, reason='verified' if verified else 'result_unknown', starred=verified)
        except _Stopped:
            pass
        except AppException as error:
            self._failure(identity, error)

    def run_due_once(self):
        if not self.enabled or self._stopping.is_set():
            return
        with self.sessions() as db:
            identities = [row.intent_id for row in db.query(GitHubStarIntent).filter_by(
                repository=REPOSITORY, status='pending',
            ).order_by(GitHubStarIntent.created_at).all()]
        for identity in identities:
            if not self._claim(identity):
                continue
            try:
                self._execute(identity)
            finally:
                with self._lock:
                    self._running.discard(identity)

    def _delay(self):
        with self._lock, self.sessions() as db:
            times = []
            for row in db.query(GitHubStarIntent).filter_by(repository=REPOSITORY, status='pending').all():
                if row.intent_id in self._running:
                    continue
                due = max(_utc(value) for value in (row.next_attempt_at, row.rate_limit_not_before) if value is not None)
                times.append(min(due, _utc(row.expires_at)))
            return max(0, (min(times) - self._now()).total_seconds()) if times else None

    def _loop(self):
        while not self._stopping.is_set():
            self._wake.clear()
            try:
                self.run_due_once()
                delay = self._delay()
            except Exception:
                # Do not log exceptions containing provider responses or credential-helper output.
                _LOGGER.warning('Deferred GitHub support processing is unavailable')
                delay = 60
            if not self._stopping.is_set():
                self._wake.wait(delay)

    def start(self):
        with self._lock:
            if not self.enabled or (self._thread is not None and self._thread.is_alive()):
                return
            self._stopping.clear()
            self._thread = threading.Thread(target=self._loop, name='github-star-deferred', daemon=True)
            self._thread.start()

    def shutdown(self):
        self._stopping.set()
        self._wake.set()
        thread = self._thread
        if thread is not None and thread is not threading.current_thread():
            thread.join()

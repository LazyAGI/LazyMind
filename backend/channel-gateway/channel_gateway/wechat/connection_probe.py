"""Probe only confirmed recipients; success does not prove send eligibility."""
from concurrent.futures import ThreadPoolExecutor, wait as wait_for_futures
import logging
import threading
import time

from channel_gateway.common.errors import GatewayError
from channel_gateway.wechat.domain import WeChatRejectedError


_logger = logging.getLogger(__name__)


class WeChatConnectionProbe:
    def __init__(self, *, store, credentials, client, core):
        self._store, self._credentials, self._client, self._core = store, credentials, client, core
        self._lock = threading.Lock()
        self._inflight, self._startup_revisions, self._catalog = {}, {}, {}
        self._scheduled_order, self._schedule_sequence = {}, 0
        self._scheduled_at = {}
        self._catalog_account_versions = {}
        self._catalog_loading = set()
        self._stopped = False
        self._executor = ThreadPoolExecutor(max_workers=4, thread_name_prefix='wechat-connection-check')

    def check_target(self, owner, account_id, recipient_id, *, wait=True):
        if not recipient_id:
            raise GatewayError(422, 'NOTIFICATION_TARGET_REQUIRED', '请先确认通知接收对象')
        account = self._store.get_account(owner, account_id)
        if not account:
            raise GatewayError(404, 'ACCOUNT_NOT_FOUND', '频道账号不存在')
        if account['provider'] != 'wechat' or account['status'] != 'connected':
            raise GatewayError(422, 'NOTIFICATION_TARGET_UNAVAILABLE', '通知账号不可用')
        targets = self._store.notification_targets(owner, account_id, recipient_id=recipient_id)['items']
        if not any(item['recipient_id'] == recipient_id for item in targets):
            raise GatewayError(422, 'NOTIFICATION_TARGET_UNAVAILABLE', '请先确认通知接收对象')
        futures = self._schedule([(account, recipient_id)], force=True)
        if wait and futures:
            wait_for_futures(futures, timeout=9)

    def check_accounts(self, accounts, *, force=False, wait=False):
        """Background/list compatibility: Core's saved rules are authoritative."""
        started = time.monotonic()
        rows = {row['id']: row for row in accounts
                if row.get('provider') == 'wechat' and row.get('status') == 'connected'}
        owners = {row['owner_user_id'] for row in rows.values()}
        confirmed = []
        for owner in sorted(owners):
            versions = tuple(sorted((row['id'], row['credential_revision']) for row in rows.values()
                                    if row['owner_user_id'] == owner))
            with self._lock:
                changed_accounts = self._catalog_account_versions.get(owner) != versions
                self._catalog_account_versions[owner] = versions
            # Startup/new credentials may bypass the per-target interval, so
            # refresh Core's gates before allowing that immediate probe.
            for pair in self._confirmed_targets(owner, force or changed_accounts, rows):
                account = rows.get(pair['account_id'])
                if account and account['owner_user_id'] == owner:
                    confirmed.append((account, pair['recipient_id']))
        futures = self._schedule(confirmed, force=force)
        if wait and futures:
            wait_for_futures(futures, timeout=max(0, 9 - (time.monotonic() - started)))

    def _due(self, row, recipient, now):
        key = (row['id'], recipient)
        return (self._startup_revisions.get(key) != row['credential_revision']
                or now - self._scheduled_at.get(key, float('-inf')) >= 20 * 60)

    def _confirmed_targets(self, owner, force, accounts):
        now = time.monotonic()
        with self._lock:
            if self._stopped:
                return []
            cached = self._catalog.get(owner)
            if not force and cached and now - cached[0] < cached[2]:
                # The cache is only a hint that work may be due, never an
                # authorization to probe. Re-read Core before each due batch,
                # including recipients left over from a bounded startup batch.
                if not any((row := accounts.get(pair['account_id']))
                           and row['owner_user_id'] == owner
                           and self._due(row, pair['recipient_id'], now)
                           for pair in cached[1]):
                    return []
            if owner in self._catalog_loading:
                return []
            self._catalog_loading.add(owner)
        pairs, interval = [], 20 * 60
        try:
            pairs = self._core.wechat_notification_targets(owner)
        except Exception:
            interval = 20  # Retry unavailable Core without probing guessed targets.
            _logger.warning('wechat_connection_catalog_unavailable')
        finally:
            with self._lock:
                self._catalog[owner] = (time.monotonic(), pairs, interval)
                self._catalog_loading.discard(owner)
        return pairs

    def _schedule(self, confirmed, *, force):
        futures, seen = [], set()
        with self._lock:
            if self._stopped:
                return futures
            # Rotate bounded batches even when their first targets are still
            # in the persisted debounce window. One account's early recipients
            # must not repeatedly occupy all slots and starve later recipients.
            pairs = sorted(confirmed, key=lambda item: (
                self._scheduled_order.get((item[0]['id'], item[1]), 0), item[0]['id'], item[1]))
            for row, recipient in pairs:
                key = (row['id'], recipient)
                if key in seen:
                    continue
                seen.add(key)
                future = self._inflight.get(key)
                if future is None:
                    if not force and not self._due(row, recipient, time.monotonic()):
                        continue
                    if len(self._inflight) >= 16:
                        break
                    first_check = self._startup_revisions.get(key) != row['credential_revision']
                    future = self._executor.submit(self._check_target, row, recipient, force or first_check)
                    self._inflight[key] = future
                    self._startup_revisions[key] = row['credential_revision']
                    self._scheduled_at[key] = time.monotonic()
                    self._schedule_sequence += 1
                    self._scheduled_order[key] = self._schedule_sequence
                futures.append(future)
        return futures

    def _check_target(self, row, recipient, force):
        account_id = row['id']
        try:
            account = self._credentials.load_runtime_account(account_id)
            if account.get('owner_user_id') != row.get('owner_user_id'):
                return
            credentials = account['credentials']
            snapshot = self._store.begin_wechat_connection_check(account, recipient, force=force)
            if snapshot is None:
                return
            status, reason = 'unavailable', 'WECHAT_CONNECTION_UNAVAILABLE'
            try:
                payload = self._client.get_config(
                    base_url=credentials['base_url'], token=credentials['token'],
                    to_user_id=recipient, context_token=snapshot['context_token'], timeout_seconds=2.0,
                )
                if any(type(payload.get(key)) is int and payload[key] == 0 for key in ('ret', 'errcode')):
                    status, reason = 'connected', ''
            except WeChatRejectedError as error:
                if not error.retryable and type(error.http_status) is int:
                    if error.http_status in {401, 403}:
                        status, reason = 'activation_required', 'WECHAT_REAUTHORIZATION_REQUIRED'
                    elif 200 <= error.http_status < 300:
                        status, reason = 'activation_required', 'WECHAT_NOTIFICATION_CONTEXT_REQUIRED'
            except Exception:
                pass  # Transient failures never revoke a conversation context.
            with self._lock:
                stopped = self._stopped
            if not stopped:
                self._store.finish_wechat_connection_check(snapshot, status, reason)
        except Exception:
            _logger.warning('wechat_connection_check_unavailable account_id=%s', account_id)
        finally:
            with self._lock:
                self._inflight.pop((account_id, recipient), None)

    def stop(self):
        with self._lock:
            self._stopped = True
        self._executor.shutdown(wait=False, cancel_futures=True)

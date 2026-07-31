from __future__ import annotations

import hashlib
import json
import logging
import threading
from dataclasses import dataclass, field

from channel_gateway.common.domain.channel import InboundEnvelope
from channel_gateway.common.ports.providers import (
    ReceiverRepository,
    RuntimeCredentialStore,
    RuntimeLease,
)
from channel_gateway.feishu.domain import (
    FeishuAddressFactory,
    FeishuAppCredentials,
    FeishuInboundAction,
    FeishuInboundMessage,
    FeishuRuntimeError,
)
from channel_gateway.feishu.ports import (
    FeishuReceiverClient,
    FeishuReceiverFactory,
)
_logger = logging.getLogger(__name__)


@dataclass(frozen=True, slots=True)
class _AccountRoute:
    account_id: str
    owner_user_id: str
    app_id: str
    sender_id: str
    revision: int


@dataclass(slots=True)
class _AppWorker:
    app_id: str
    stop_event: threading.Event
    reload_event: threading.Event
    account_ids: set[str] = field(default_factory=set)
    thread: threading.Thread | None = None
    channel: FeishuReceiverClient | None = None
    lease: RuntimeLease | None = None


class FeishuRuntime:
    """Owns one leased Feishu WebSocket per app and routes owner DMs."""

    def __init__(
        self,
        *,
        store: ReceiverRepository,
        credentials: RuntimeCredentialStore,
        channels: FeishuReceiverFactory,
        addresses: FeishuAddressFactory,
    ):
        self._store = store
        self._credentials = credentials
        self._channels = channels
        self._addresses = addresses
        self._shutdown = threading.Event()
        self._lock = threading.Lock()
        self._workers: dict[str, _AppWorker] = {}
        self._accounts: dict[str, _AccountRoute] = {}
        self._owner_routes: dict[tuple[str, str], str] = {}

    def reconcile_accounts(
        self,
        accounts: list[dict],
    ) -> None:
        desired = {
            str(account['id']): int(account['credential_revision'])
            for account in accounts
        }
        with self._lock:
            current = {
                account_id: route.revision
                for account_id, route in self._accounts.items()
            }
        for account_id in current.keys() - desired.keys():
            self.stop_account(account_id)
        for account_id, revision in desired.items():
            if current.get(account_id) != revision:
                try:
                    self.start_account(
                        account_id,
                        revision=revision,
                    )
                except Exception as exc:
                    self._store.set_runtime_status(
                        account_id,
                        'failed',
                        str(exc)[:500],
                    )
                    _logger.exception(
                        'feishu_account_start_failed account_id=%s',
                        account_id,
                    )

    def stop(self) -> None:
        self._shutdown.set()
        with self._lock:
            workers = list(self._workers.values())
        for worker in workers:
            worker.stop_event.set()
            if worker.channel:
                worker.channel.stop()
        for worker in workers:
            if (
                worker.thread
                and worker.thread is not threading.current_thread()
            ):
                worker.thread.join(timeout=5)

    def start_account(
        self,
        account_id: str,
        *,
        revision: int = 0,
    ) -> None:
        account = self._credentials.load_runtime_account(account_id)
        credentials = account['credentials']
        route = _AccountRoute(
            account_id=account_id,
            owner_user_id=str(account['owner_user_id']),
            app_id=credentials.app_id,
            sender_id=credentials.provider_account_id,
            revision=revision or int(account['credential_revision']),
        )
        workers_to_stop: list[_AppWorker] = []
        with self._lock:
            existing = self._accounts.get(account_id)
            if existing == route:
                return
            if existing is not None:
                stopped = self._remove_account_locked(existing)
                if stopped:
                    workers_to_stop.append(stopped)
            route_key = (route.app_id, route.sender_id)
            conflict = self._owner_routes.get(route_key)
            if conflict not in (None, account_id):
                _logger.error(
                    'feishu_route_conflict account_id=%s conflict=%s',
                    account_id,
                    conflict,
                )
                return
            self._accounts[account_id] = route
            self._owner_routes[route_key] = account_id
            worker = self._workers.get(route.app_id)
            if worker is None:
                worker = _AppWorker(
                    app_id=route.app_id,
                    stop_event=threading.Event(),
                    reload_event=threading.Event(),
                    account_ids={account_id},
                )
                worker.thread = threading.Thread(
                    target=self._run_app,
                    args=(worker,),
                    name=f'channel-feishu-{route.app_id[-8:]}',
                    daemon=True,
                )
                self._workers[route.app_id] = worker
                worker.thread.start()
            else:
                worker.account_ids.add(account_id)
                worker.reload_event.set()
        self._stop_workers(workers_to_stop)

    def restart_account(self, account_id: str) -> None:
        self.start_account(account_id)

    def stop_account(self, account_id: str) -> None:
        stopped = None
        with self._lock:
            route = self._accounts.get(account_id)
            if route is not None:
                stopped = self._remove_account_locked(route)
        if stopped:
            self._stop_workers([stopped])

    def _remove_account_locked(
        self,
        route: _AccountRoute,
    ) -> _AppWorker | None:
        self._accounts.pop(route.account_id, None)
        self._owner_routes.pop(
            (route.app_id, route.sender_id),
            None,
        )
        worker = self._workers.get(route.app_id)
        if worker is None:
            return None
        worker.account_ids.discard(route.account_id)
        if worker.account_ids:
            worker.reload_event.set()
            return None
        if self._workers.get(route.app_id) is worker:
            self._workers.pop(route.app_id, None)
        worker.stop_event.set()
        return worker

    @staticmethod
    def _stop_workers(workers: list[_AppWorker]) -> None:
        for worker in workers:
            if worker.channel:
                worker.channel.stop()
        for worker in workers:
            if (
                worker.thread
                and worker.thread is not threading.current_thread()
            ):
                worker.thread.join(timeout=5)

    def _run_app(self, worker: _AppWorker) -> None:
        failures = 0
        while (
            not self._shutdown.is_set()
            and not worker.stop_event.is_set()
        ):
            lease = None
            try:
                lease = self._store.acquire_runtime_lease(
                    f'feishu-app:{worker.app_id}'
                )
                if lease is None:
                    worker.stop_event.wait(5)
                    continue
                with self._lock:
                    worker.lease = lease
                self._run_connected(worker, lease)
                failures = 0
            except Exception as exc:
                failures += 1
                if lease is not None:
                    self._set_worker_status(
                        worker,
                        lease,
                        'failed',
                        str(exc)[:500],
                    )
                _logger.exception(
                    'feishu_runtime_failed app_id=%s attempt=%s',
                    worker.app_id,
                    failures,
                )
                worker.stop_event.wait(
                    min(30, 2 ** min(failures, 5))
                )
            finally:
                if lease is not None:
                    if (
                        self._shutdown.is_set()
                        or worker.stop_event.is_set()
                    ):
                        self._set_worker_status(
                            worker,
                            lease,
                            'stopped',
                        )
                    with self._lock:
                        if worker.lease is lease:
                            worker.lease = None
                    lease.close()
        with self._lock:
            if self._workers.get(worker.app_id) is worker:
                self._workers.pop(worker.app_id, None)

    def _run_connected(
        self,
        worker: _AppWorker,
        lease: RuntimeLease,
    ) -> None:
        credentials = self._seed_credentials(worker)
        worker.reload_event.clear()
        channel = self._channels.create_receiver(
            credentials,
            lambda message: self._handle_message(worker, message),
            lambda action: self._handle_action(worker, action),
        )
        with self._lock:
            worker.channel = channel
        self._set_worker_status(worker, lease, 'starting')
        start_error: list[Exception] = []

        def start_channel() -> None:
            try:
                channel.start()
            except Exception as exc:
                start_error.append(exc)

        channel_thread = threading.Thread(
            target=start_channel,
            name=f'feishu-sdk-{worker.app_id[-8:]}',
            daemon=True,
        )
        channel_thread.start()
        runtime_status = 'starting'
        try:
            while (
                not self._shutdown.is_set()
                and not worker.stop_event.is_set()
                and not worker.reload_event.is_set()
            ):
                lease.keepalive()
                connection_state = channel.connection_state()
                if (
                    channel.is_ready()
                    and connection_state == 'connected'
                    and runtime_status != 'running'
                ):
                    if self._set_worker_status(
                        worker,
                        lease,
                        'running',
                    ):
                        runtime_status = 'running'
                elif (
                    connection_state == 'reconnecting'
                    and runtime_status != 'degraded'
                ):
                    if self._set_worker_status(
                        worker,
                        lease,
                        'degraded',
                        '飞书长连接正在重连',
                    ):
                        runtime_status = 'degraded'
                if not channel_thread.is_alive():
                    if start_error:
                        raise FeishuRuntimeError(
                            str(start_error[0])
                        ) from start_error[0]
                    raise FeishuRuntimeError(
                        'Feishu channel stopped unexpectedly'
                    )
                worker.stop_event.wait(5)
        finally:
            channel.stop()
            channel_thread.join(timeout=5)
            with self._lock:
                if worker.channel is channel:
                    worker.channel = None

    def _handle_message(
        self,
        worker: _AppWorker,
        message: FeishuInboundMessage,
    ) -> None:
        if (
            message.sender_is_bot
            or not message.message_id
            or not message.chat_id
            or not message.sender_id
            or not message.text
        ):
            return
        with self._lock:
            account_id = self._owner_routes.get(
                (worker.app_id, message.sender_id)
            )
            route = (
                self._accounts.get(account_id)
                if account_id
                else None
            )
            lease = worker.lease
        if route is None:
            route = self._load_route_for_message(
                worker,
                message.sender_id,
            )
            if route is None:
                return
        if (
            route is None
            or route.sender_id != message.sender_id
        ):
            return
        if lease is None:
            raise FeishuRuntimeError(
                'Feishu runtime lease is unavailable'
            )
        address = self._addresses.direct(
            route.account_id,
            message.chat_id,
            message.sender_id,
        )
        address_hash = address.route_hash
        message_key = hashlib.sha256(
            message.message_id.encode('utf-8')
        ).hexdigest()
        self._store.ingest_batch(
            route.account_id,
            [
                InboundEnvelope(
                    provider='feishu',
                    account_id=route.account_id,
                    message_key=message_key,
                    order_key=address_hash,
                    external_address_hash=address_hash,
                    owner_user_id=route.owner_user_id,
                    recipient_id=message.chat_id,
                    text=message.text,
                    provider_context={
                        'chat_id': message.chat_id,
                    },
                )
            ],
            None,
            lease.fence,
        )

    def _handle_action(
        self,
        worker: _AppWorker,
        action: FeishuInboundAction,
    ) -> None:
        if (
            action.action not in {'select', 'ask', 'command'}
            or not action.message_id
            or not action.chat_id
            or not action.sender_id
            or not action.text
            or action.intended_chat_id != action.chat_id
        ):
            return
        with self._lock:
            account_id = self._owner_routes.get(
                (worker.app_id, action.sender_id)
            )
            route = self._accounts.get(account_id) if account_id else None
            lease = worker.lease
        if (
            route is None
            or route.sender_id != action.sender_id
        ):
            return
        if lease is None:
            raise FeishuRuntimeError(
                'Feishu runtime lease is unavailable'
            )
        address = self._addresses.direct(
            route.account_id,
            action.chat_id,
            action.sender_id,
        )
        message_key = hashlib.sha256(
            json.dumps(
                {
                    'message_id': action.message_id,
                    'sender_id': action.sender_id,
                    'action': action.action,
                    'text': action.text,
                    'selection': action.selection,
                    'selection_id': action.selection_id,
                    'ask_answers_structured': (
                        action.ask_answers_structured
                    ),
                    'command_action': action.command_action,
                },
                ensure_ascii=False,
                sort_keys=True,
                separators=(',', ':'),
            ).encode('utf-8')
        ).hexdigest()
        self._store.ingest_batch(
            route.account_id,
            [
                InboundEnvelope(
                    provider='feishu',
                    account_id=route.account_id,
                    message_key=message_key,
                    order_key=address.route_hash,
                    external_address_hash=address.route_hash,
                    owner_user_id=route.owner_user_id,
                    recipient_id=action.chat_id,
                    text=action.text,
                    provider_context={
                        'chat_id': action.chat_id,
                        'ask_answers_structured': (
                            action.ask_answers_structured
                        ),
                        'selection_action': (
                            {
                                'selection_id': action.selection_id,
                                'index': action.selection,
                            }
                            if action.action == 'select'
                            else None
                        ),
                        'command_action': (
                            action.command_action
                            if action.action == 'command'
                            else None
                        ),
                    },
                )
            ],
            None,
            lease.fence,
        )

    def _load_route_for_message(
        self,
        worker: _AppWorker,
        sender_id: str,
    ) -> _AccountRoute | None:
        external_id_hash = hashlib.sha256(
            f'{worker.app_id}:{sender_id}'.encode('utf-8')
        ).hexdigest()
        account = self._store.find_connected_account(
            'feishu',
            external_id_hash,
        )
        if account is None:
            return None
        route = _AccountRoute(
            account_id=str(account['id']),
            owner_user_id=str(account['owner_user_id']),
            app_id=worker.app_id,
            sender_id=sender_id,
            revision=int(account['credential_revision']),
        )
        with self._lock:
            current = self._owner_routes.get(
                (worker.app_id, sender_id)
            )
            if current:
                return self._accounts.get(current)
            self._accounts[route.account_id] = route
            self._owner_routes[
                (worker.app_id, sender_id)
            ] = route.account_id
            worker.account_ids.add(route.account_id)
        return route

    def _seed_credentials(
        self,
        worker: _AppWorker,
    ) -> FeishuAppCredentials:
        failures: list[Exception] = []
        for route in self._ordered_routes(worker):
            account_id = route.account_id
            try:
                account = self._credentials.load_runtime_account(
                    account_id
                )
            except Exception as exc:
                failures.append(exc)
                continue
            credentials = account['credentials']
            if credentials.app_id == worker.app_id:
                return credentials
            failures.append(
                FeishuRuntimeError(
                    'Feishu app identity changed; reconnect the account'
                )
            )
        if failures:
            raise FeishuRuntimeError(str(failures[0])) from failures[0]
        raise FeishuRuntimeError(
            'Feishu app has no connected channel account'
        )

    def _ordered_routes(
        self,
        worker: _AppWorker,
    ) -> list[_AccountRoute]:
        with self._lock:
            routes = [
                self._accounts[account_id]
                for account_id in worker.account_ids
                if account_id in self._accounts
            ]
        routes.sort(
            key=lambda route: (route.revision, route.account_id),
            reverse=True,
        )
        return routes

    def _set_worker_status(
        self,
        worker: _AppWorker,
        lease: RuntimeLease,
        status: str,
        error: str | None = None,
    ) -> bool:
        with self._lock:
            account_ids = list(worker.account_ids)
        succeeded = True
        for account_id in account_ids:
            try:
                self._store.set_runtime_status(
                    account_id,
                    status,
                    error,
                    lease.fence,
                )
            except Exception:
                succeeded = False
                _logger.exception(
                    'feishu_runtime_status_failed account_id=%s',
                    account_id,
                )
        return succeeded

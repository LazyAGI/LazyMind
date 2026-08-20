from __future__ import annotations

import base64
import hashlib
import json
import logging
import math
import threading
import time
from dataclasses import dataclass, field, replace
from typing import Any

from channel_gateway.common.domain.channel import (
    InboundEnvelope,
)
from channel_gateway.common.domain.chat import (
    ChannelAttachment,
    ChannelExecutionContext,
)
from channel_gateway.common.domain.commands import ASSISTANT_PROVIDERS
from channel_gateway.common.ports.providers import (
    RuntimeCredentialStore,
    RuntimeLease,
)
from channel_gateway.feishu.domain import (
    FeishuAddressFactory,
    FeishuAppCredentials,
    FeishuInboundAction,
    FeishuInboundMenu,
    FeishuInboundMessage,
    FeishuRuntimeError,
    workspace_card_expired,
)
from channel_gateway.feishu.ports import (
    FeishuReceiverClient,
    FeishuReceiverFactory,
    FeishuRuntimeRepository,
)
from channel_gateway.feishu.registration import configure_bot_menu
from channel_gateway.feishu.presentation import FeishuReplyRenderer
from channel_gateway.feishu.workspace import (
    FeishuWorkspaceRenderer,
    FeishuWorkspaceState,
    MENU_EVENT_VIEWS,
    menu_command,
    stale_workspace_card,
)
_logger = logging.getLogger(__name__)


_MAX_INBOUND_IMAGE_BYTES = 10 * 1024 * 1024
_ACTION_REFRESH_DELAY_SECONDS = 0.35
_ACTION_REFRESH_RETRY_DELAYS = (0.5, 1.0, 2.0, 4.0)
_BOT_MENU_CONFIG_VERSION = 6
_BOT_MENU_RETRY_SECONDS = 30.0


_RESULT_WORKSPACE_ACTIONS = {
    'maintenance.new_conversation',
    'new_session.create',
    'prompt.run',
}


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
    menu_error: str = ''
    next_menu_attempt_at: float = 0.0


class FeishuRuntime:
    """Owns one leased Feishu WebSocket per app and routes owner DMs."""

    def __init__(
        self,
        *,
        store: FeishuRuntimeRepository,
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
        self._direct_chats: dict[str, str] = {}

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
                worker.menu_error = ''
                worker.next_menu_attempt_at = 0.0
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
            lambda menu: self._handle_menu(worker, menu),
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
        runtime_error = ''
        try:
            while (
                not self._shutdown.is_set()
                and not worker.stop_event.is_set()
                and not worker.reload_event.is_set()
            ):
                lease.keepalive()
                connection_state = channel.connection_state()
                if channel.is_ready() and connection_state == 'connected':
                    self._refresh_bot_menu(worker, credentials)
                if (
                    channel.is_ready()
                    and connection_state == 'connected'
                ):
                    desired_status = (
                        'degraded' if worker.menu_error else 'running'
                    )
                    desired_error = worker.menu_error
                    if (
                        (
                            runtime_status != desired_status
                            or runtime_error != desired_error
                        )
                        and self._set_worker_status(
                            worker,
                            lease,
                            desired_status,
                            worker.menu_error or None,
                        )
                    ):
                        runtime_status = desired_status
                        runtime_error = desired_error
                elif (
                    connection_state == 'reconnecting'
                    and (
                        runtime_status != 'degraded'
                        or runtime_error != '飞书长连接正在重连'
                    )
                ):
                    if self._set_worker_status(
                        worker,
                        lease,
                        'degraded',
                        '飞书长连接正在重连',
                    ):
                        runtime_status = 'degraded'
                        runtime_error = '飞书长连接正在重连'
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

    @staticmethod
    def _refresh_bot_menu(
        worker: _AppWorker,
        credentials: FeishuAppCredentials,
    ) -> None:
        now = time.monotonic()
        if now < worker.next_menu_attempt_at:
            return
        try:
            configure_bot_menu(
                credentials.app_id,
                credentials.app_secret,
                publish_version=f'1.0.{_BOT_MENU_CONFIG_VERSION}',
            )
        except FeishuRuntimeError as exc:
            worker.menu_error = str(exc)[:500]
            worker.next_menu_attempt_at = now + _BOT_MENU_RETRY_SECONDS
            _logger.warning(
                'feishu_bot_menu_configuration_pending error=%s',
                exc.__class__.__name__,
            )
            return
        worker.menu_error = ''
        worker.next_menu_attempt_at = float('inf')

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
        ):
            return
        with self._lock:
            account_id = self._owner_routes.get(
                (worker.app_id, message.sender_id)
            )
            route = self._accounts.get(account_id) if account_id else None
            lease = worker.lease
        if route is None:
            route = self._load_route_for_message(worker, message.sender_id)
        if route is None or route.sender_id != message.sender_id:
            return
        if lease is None:
            raise FeishuRuntimeError(
                'Feishu runtime lease is unavailable'
            )
        if not message.text and not message.image_key:
            if message.message_type:
                self._send_input_notice(
                    route=route,
                    chat_id=message.chat_id,
                    message_id=message.message_id,
                    text='目前支持文字、富文本和单张图片，请换一种方式发送。',
                )
            return

        address = self._addresses.direct(
            route.account_id,
            message.chat_id,
            message.sender_id,
        )
        address_hash = address.route_hash
        self._remember_direct_chat(route.account_id, message.chat_id)
        effective_text = message.text or '请描述并分析这张图片。'
        attachments: tuple[ChannelAttachment, ...] = ()
        if message.image_key:
            account = self._credentials.load_runtime_account(
                route.account_id
            )
            sender = self._channels.create_sender(account['credentials'])
            try:
                content = sender.download_image(
                    image_key=message.image_key,
                    message_id=message.message_id,
                )
            finally:
                sender.close()
            if not content or len(content) > _MAX_INBOUND_IMAGE_BYTES:
                self._send_input_notice(
                    route=route,
                    chat_id=message.chat_id,
                    message_id=message.message_id,
                    text='图片为空或超过 10 MB，本次消息未提交。',
                )
                return
            attachment = ChannelAttachment.from_dict({
                'input_type': 'image',
                'input_base64': _image_data_url(content),
            })
            attachments = (attachment,) if attachment is not None else ()

        workspace = FeishuWorkspaceState.from_dict(
            self._store.get_feishu_workspace_state(
                route.account_id,
                address_hash,
            )
        )
        message_key = hashlib.sha256(
            message.message_id.encode('utf-8')
        ).hexdigest()
        source_revision = workspace.revision
        source_message_id = workspace.message_id
        source_operation_id = workspace.active_operation_id
        workspace.begin_operation(message_key)
        workspace.advance()
        conversation_id = self._store.get_route(
            route.account_id,
            address_hash,
        )
        provider_context = self._workspace_provider_context(
            account_id=route.account_id,
            address_hash=address_hash,
            workspace=workspace,
            chat_id=message.chat_id,
            conversation_id=conversation_id,
        )
        public_execution = ChannelExecutionContext.from_provider_context(
            provider_context
        )
        execution = replace(
            public_execution,
            attachments=attachments,
        )
        provider_context = {
            **provider_context,
            'workspace_surface': 'reply',
            'workspace_reanchor_to_bottom': True,
            'channel_execution': public_execution.to_dict(),
            'command_action': _chat_command_action(
                effective_text,
                required=bool(attachments),
            ),
        }
        envelope = InboundEnvelope(
            provider='feishu',
            account_id=route.account_id,
            message_key=message_key,
            order_key=address_hash,
            external_address_hash=address_hash,
            owner_user_id=route.owner_user_id,
            recipient_id=message.chat_id,
            text=effective_text,
            provider_context=provider_context,
            sensitive_context=(
                {'channel_execution': execution.to_dict()}
                if attachments
                else {}
            ),
        )
        if not self._store.claim_feishu_workspace_and_ingest(
            route.account_id,
            address_hash,
            workspace.to_dict(),
            source_revision,
            source_message_id,
            source_operation_id,
            envelope,
            lease.fence,
        ):
            self._send_input_notice(
                route=route,
                chat_id=message.chat_id,
                message_id=message.message_id,
                text='上一项操作仍在处理中，本次消息未提交，请稍后重试。',
            )

    def _send_input_notice(
        self,
        *,
        route: _AccountRoute,
        chat_id: str,
        message_id: str,
        text: str,
    ) -> None:
        account = self._credentials.load_runtime_account(
            route.account_id
        )
        sender = self._channels.create_sender(account['credentials'])
        try:
            sender.send_markdown(
                chat_id=chat_id,
                text=text,
                idempotency_key=f'feishu-input-notice:{message_id}',
            )
        finally:
            sender.close()

    @staticmethod
    def _send_card_to_bottom(
        *,
        sender: Any,
        chat_id: str,
        card: dict[str, Any],
        idempotency_key: str,
    ) -> str:
        new_message_id = sender.send_card(
            chat_id=chat_id,
            card=card,
            idempotency_key=idempotency_key,
        )
        if not new_message_id:
            raise FeishuRuntimeError('Feishu workspace card send returned no id')
        return str(new_message_id)

    def _handle_action(
        self,
        worker: _AppWorker,
        action: FeishuInboundAction,
    ) -> dict[str, Any] | None:
        if (
            action.action not in {'select', 'ask', 'command', 'local'}
            or not action.message_id
            or not action.chat_id
            or not action.sender_id
            or not action.text
            or action.intended_chat_id not in {'', action.chat_id}
        ):
            return None
        started_at = time.monotonic()
        with self._lock:
            account_id = self._owner_routes.get(
                (worker.app_id, action.sender_id)
            )
            route = self._accounts.get(account_id) if account_id else None
            lease = worker.lease
        if route is None or route.sender_id != action.sender_id:
            return None
        if lease is None:
            raise FeishuRuntimeError(
                'Feishu runtime lease is unavailable'
            )

        address_hash = self._addresses.direct(
            route.account_id,
            action.chat_id,
            action.sender_id,
        ).route_hash
        self._remember_direct_chat(route.account_id, action.chat_id)
        conversation_id = self._store.get_route(
            route.account_id,
            address_hash,
        )
        workspace = FeishuWorkspaceState.from_dict(
            self._store.get_feishu_workspace_state(
                route.account_id,
                address_hash,
            )
        )
        message_key = hashlib.sha256(
            (
                f'action:{action.event_id}:{action.sender_id}'
                if action.event_id
                else json.dumps(
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
                        'workspace_action': action.workspace_action,
                    },
                    ensure_ascii=False,
                    sort_keys=True,
                    separators=(',', ':'),
                )
            ).encode('utf-8')
        ).hexdigest()
        if workspace.message_id and workspace.message_id != action.message_id:
            return stale_workspace_card(workspace.output_language)

        action_data = action.workspace_action or {}
        action_kind = str(action_data.get('kind') or '')
        if action_kind and (
            str(action_data.get('expected_view') or '') != workspace.view
            or action_data.get('expected_revision') != workspace.revision
            or str(action_data.get('expected_operation_id') or '')
            != workspace.active_operation_id
        ):
            return stale_workspace_card(workspace.output_language)
        if action_kind == 'setting.update':
            command_action = action.command_action
            parameters = (
                command_action.get('parameters')
                if isinstance(command_action, dict)
                else None
            )
            expected_conversation_id = str(
                action_data.get('expected_conversation_id') or ''
            )
            change = (
                parameters.get('change')
                if isinstance(parameters, dict)
                else None
            )
            if (
                str(action_data.get('view') or 'capabilities')
                not in {'capabilities', 'assistant', 'settings'}
                or not isinstance(parameters, dict)
                or not isinstance(change, dict)
                or str(parameters.get('expected_conversation_id') or '')
                != expected_conversation_id
                or expected_conversation_id != conversation_id
            ):
                return stale_workspace_card(workspace.output_language)
        if action.action == 'local' and not action_kind:
            return None

        source_revision = workspace.revision
        source_message_id = workspace.message_id
        source_operation_id = workspace.active_operation_id
        workspace.bind_message(action.message_id)
        if action_kind == 'setting.update':
            workspace.active_operation_id = message_key
        self._apply_workspace_action(
            workspace=workspace,
            action=action.workspace_action,
        )
        if action_kind == 'history.switch':
            workspace.begin_operation(message_key)
            target_view = str(action_data.get('view') or 'conversations')
            workspace.view = (
                target_view
                if target_view in {'assistant', 'conversations'}
                else 'conversations'
            )
        elif action_kind == 'operation.cancel':
            workspace.begin_operation(message_key)
        elif (
            action.action == 'ask'
            or action_kind in _RESULT_WORKSPACE_ACTIONS
        ):
            workspace.begin_operation(message_key)
        workspace.advance()

        if action.action == 'local':
            if not self._store.save_feishu_workspace_state_if_revision(
                route.account_id,
                address_hash,
                workspace.to_dict(),
                source_revision,
            ):
                return None
            workspace = FeishuWorkspaceState.from_dict(
                self._store.get_feishu_workspace_state(
                    route.account_id,
                    address_hash,
                )
            )
            conversation_id = self._store.get_route(
                route.account_id,
                address_hash,
            ) or ''

        provider_context = self._workspace_provider_context(
            account_id=route.account_id,
            address_hash=address_hash,
            workspace=workspace,
            chat_id=action.chat_id,
            conversation_id=conversation_id,
            workspace_action=action.workspace_action,
        )
        public_execution = ChannelExecutionContext.from_provider_context(
            provider_context
        )
        execution = replace(
            public_execution,
            ask_answers_structured=(
                dict(action.ask_answers_structured)
                if isinstance(action.ask_answers_structured, dict)
                else None
            ),
        )
        provider_context = {
            **provider_context,
            'workspace_operation_id': workspace.active_operation_id,
            'channel_execution': public_execution.to_dict(),
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
            'workspace_surface': (
                'reply'
                if action_kind == 'operation.cancel'
                else 'management'
                if (
                    isinstance(action.workspace_action, dict)
                    and action_kind not in _RESULT_WORKSPACE_ACTIONS
                )
                else 'reply'
            ),
            '_parallel_inbound': action_kind == 'operation.cancel',
        }
        if action.action == 'local':
            card = FeishuWorkspaceRenderer.render(
                provider_context=provider_context,
                presentations=[],
            )
            self._log_action_ready(
                action,
                started_at=started_at,
                cached=True,
            )
            return card

        envelope = InboundEnvelope(
            provider='feishu',
            account_id=route.account_id,
            message_key=message_key,
            order_key=address_hash,
            external_address_hash=address_hash,
            owner_user_id=route.owner_user_id,
            recipient_id=action.chat_id,
            text=action.text,
            provider_context=provider_context,
            sensitive_context=(
                {'channel_execution': execution.to_dict()}
                if execution.ask_answers_structured is not None
                else {}
            ),
        )
        if not self._store.claim_feishu_workspace_and_ingest(
            route.account_id,
            address_hash,
            workspace.to_dict(),
            source_revision,
            source_message_id,
            source_operation_id,
            envelope,
            lease.fence,
        ):
            return stale_workspace_card(workspace.output_language)
        self._log_action_ready(
            action,
            started_at=started_at,
            cached=False,
        )
        if action_kind == 'operation.cancel':
            return FeishuReplyRenderer.render(
                provider_context=provider_context,
                text='已请求停止当前会话的生成与后台执行。',
                status='⏹️ **停止请求已提交**',
            )
        return None

    def _handle_menu(
        self,
        worker: _AppWorker,
        menu: FeishuInboundMenu,
    ) -> None:
        view = MENU_EVENT_VIEWS.get(menu.event_key)
        if not view or not menu.event_id or not menu.sender_id:
            return
        with self._lock:
            account_id = self._owner_routes.get(
                (worker.app_id, menu.sender_id)
            )
            route = self._accounts.get(account_id) if account_id else None
            lease = worker.lease
            chat_id = (
                self._direct_chats.get(route.account_id, '')
                if route is not None
                else ''
            )
        if route is None or route.sender_id != menu.sender_id:
            return
        if lease is None:
            raise FeishuRuntimeError('Feishu runtime lease is unavailable')

        message_key = hashlib.sha256(
            (
                f'menu:{menu.event_id}:{menu.sender_id}:'
                f'{menu.event_key}'
            ).encode('utf-8')
        ).hexdigest()
        source_message_id = ''
        source_operation_id = ''
        source_revision = 0
        state = FeishuWorkspaceState(view=view)
        if chat_id:
            address_hash = self._addresses.direct(
                route.account_id,
                chat_id,
                menu.sender_id,
            ).route_hash
            state = FeishuWorkspaceState.from_dict(
                self._store.get_feishu_workspace_state(
                    route.account_id,
                    address_hash,
                )
            )
            source_message_id = state.message_id
            source_operation_id = state.active_operation_id
            source_revision = state.revision
            if state.view == view and state.active_operation_id == message_key:
                return
        state.navigate(view)
        state.active_operation_id = message_key
        state.advance()
        prepared_state = state.to_dict()
        card = FeishuWorkspaceRenderer.render(
            provider_context={
                'chat_id': chat_id,
                'workspace_state': prepared_state,
            },
            presentations=[],
        )

        sender = self._channels.create_sender(
            self._credentials.load_runtime_account(
                route.account_id
            )['credentials']
        )
        try:
            if state.message_id and chat_id:
                message_id = self._send_card_to_bottom(
                    sender=sender,
                    chat_id=chat_id,
                    card=card,
                    idempotency_key=(
                        f'feishu-menu-card:{route.account_id}:'
                        f'{menu.event_id}:{view}'
                    ),
                )
            else:
                message_id, resolved_chat_id = (
                    sender.send_card_to_user_with_chat(
                        open_id=menu.sender_id,
                        card=card,
                        idempotency_key=(
                            f'feishu-menu-card:{route.account_id}:'
                            f'{menu.event_id}:{view}'
                        ),
                    )
                )
                chat_id = resolved_chat_id or chat_id
        finally:
            sender.close()
        if not chat_id or not message_id:
            raise FeishuRuntimeError(
                'Feishu menu response is missing chat or message id'
            )
        self._remember_direct_chat(route.account_id, chat_id)
        address_hash = self._addresses.direct(
            route.account_id,
            chat_id,
            menu.sender_id,
        ).route_hash
        conversation_id = self._store.get_route(
            route.account_id,
            address_hash,
        )
        current = FeishuWorkspaceState.from_dict(
            self._store.get_feishu_workspace_state(
                route.account_id,
                address_hash,
            )
        )
        if not source_message_id:
            source_message_id = current.message_id
            source_operation_id = current.active_operation_id
            source_revision = current.revision
            state = current
            state.navigate(view)
            state.active_operation_id = message_key
            state.advance()
            prepared_state = state.to_dict()
        if (
            source_message_id
            and (
                current.message_id != source_message_id
                or current.active_operation_id != source_operation_id
                or current.revision != source_revision
            )
        ):
            return

        state = FeishuWorkspaceState.from_dict(prepared_state)
        state.message_id = str(message_id)
        command = menu_command(view, state.assistant)
        if command is not None:
            provider_context = {
                **self._workspace_provider_context(
                    account_id=route.account_id,
                    address_hash=address_hash,
                    workspace=state,
                    chat_id=chat_id,
                    conversation_id=conversation_id,
                    workspace_action={'kind': 'navigate', 'view': view},
                ),
                'workspace_surface': 'management',
                'command_action': command,
            }
            envelope = InboundEnvelope(
                provider='feishu',
                account_id=route.account_id,
                message_key=message_key,
                order_key=address_hash,
                external_address_hash=address_hash,
                owner_user_id=route.owner_user_id,
                recipient_id=chat_id,
                text={
                    'capabilities': '查看能力',
                    'conversations': '切换会话',
                    'assistant': '查看助理',
                    'settings': '查看设置',
                }[view],
                provider_context=provider_context,
            )
            claimed = self._store.claim_feishu_workspace_and_ingest(
                route.account_id,
                address_hash,
                state.to_dict(),
                source_revision,
                source_message_id,
                source_operation_id,
                envelope,
                lease.fence,
            )
            if claimed:
                self._retire_replaced_workspace_card(
                    account_id=route.account_id,
                    address_hash=address_hash,
                    previous_message_id=source_message_id,
                    current_message_id=state.message_id,
                    language=state.output_language,
                )
            return

        if not self._store.save_feishu_workspace_state_if_revision(
            route.account_id,
            address_hash,
            state.to_dict(),
            source_revision,
            preserve_current_message=False,
        ):
            return
        self._retire_replaced_workspace_card(
            account_id=route.account_id,
            address_hash=address_hash,
            previous_message_id=source_message_id,
            current_message_id=state.message_id,
            language=state.output_language,
        )

    def _remember_direct_chat(self, account_id: str, chat_id: str) -> None:
        if not account_id or not chat_id:
            return
        with self._lock:
            direct_chats = getattr(self, '_direct_chats', None)
            if direct_chats is None:
                direct_chats = {}
                self._direct_chats = direct_chats
            direct_chats[account_id] = chat_id

    def _schedule_action_card_refresh(
        self,
        account_id: str,
        message_id: str,
        card: dict[str, Any],
        *,
        address_hash: str = '',
        expected_revision: int | None = None,
        expected_operation_id: str = '',
        follow_current_message: bool = False,
    ) -> None:
        timer = threading.Timer(
            _ACTION_REFRESH_DELAY_SECONDS,
            self._refresh_action_card,
            args=(
                account_id,
                message_id,
                card,
                address_hash,
                expected_revision,
                expected_operation_id,
                follow_current_message,
            ),
        )
        timer.daemon = True
        timer.name = 'feishu-card-action-refresh'
        timer.start()

    def _expire_workspace_card(
        self,
        *,
        account_id: str,
        address_hash: str,
        message_id: str,
        current_message_id: str,
        language: str,
    ) -> None:
        del address_hash
        if not message_id or message_id == current_message_id:
            return
        account = self._credentials.load_runtime_account(account_id)
        sender = self._channels.create_sender(account['credentials'])
        try:
            sender.update_card(
                message_id=message_id,
                card=stale_workspace_card(language),
            )
        except Exception as exc:
            if not workspace_card_expired(exc):
                _logger.warning(
                    'feishu_workspace_card_expire_failed '
                    'account_id=%s message_id=%s',
                    account_id,
                    message_id,
                )
        finally:
            sender.close()

    def _retire_replaced_workspace_card(
        self,
        *,
        account_id: str,
        address_hash: str,
        previous_message_id: str,
        current_message_id: str,
        language: str,
    ) -> None:
        self._expire_workspace_card(
            account_id=account_id,
            address_hash=address_hash,
            message_id=previous_message_id,
            current_message_id=current_message_id,
            language=language,
        )

    def _refresh_action_card(
        self,
        account_id: str,
        message_id: str,
        card: dict[str, Any],
        address_hash: str = '',
        expected_revision: int | None = None,
        expected_operation_id: str = '',
        follow_current_message: bool = False,
    ) -> None:
        for attempt in range(len(_ACTION_REFRESH_RETRY_DELAYS) + 1):
            sender = None
            try:
                if address_hash:
                    current = FeishuWorkspaceState.from_dict(
                        self._store.get_feishu_workspace_state(
                            account_id,
                            address_hash,
                        )
                    )
                    if (
                        expected_revision is not None
                        and current.revision != expected_revision
                        or expected_operation_id
                        and current.active_operation_id
                        != expected_operation_id
                    ):
                        return
                    if follow_current_message:
                        if not current.message_id:
                            return
                        message_id = current.message_id
                    elif current.message_id != message_id:
                        return
                account = self._credentials.load_runtime_account(account_id)
                sender = self._channels.create_sender(account['credentials'])
                sender.update_card(message_id=message_id, card=card)
                _logger.info(
                    'feishu_card_action_refresh_succeeded '
                    'account_id=%s message_id=%s',
                    account_id,
                    message_id,
                )
                if follow_current_message and address_hash:
                    latest = FeishuWorkspaceState.from_dict(
                        self._store.get_feishu_workspace_state(
                            account_id,
                            address_hash,
                        )
                    )
                    if (
                        latest.message_id
                        and latest.message_id != message_id
                        and (
                            expected_revision is None
                            or latest.revision == expected_revision
                        )
                        and (
                            not expected_operation_id
                            or latest.active_operation_id
                            == expected_operation_id
                        )
                    ):
                        message_id = latest.message_id
                        continue
                return
            except Exception as exc:
                retry_error = exc
                if (
                    workspace_card_expired(exc)
                    and address_hash
                    and expected_revision is not None
                ):
                    try:
                        with self._lock:
                            chat_id = str(
                                self._direct_chats.get(account_id, '')
                            )
                        if chat_id and sender is not None:
                            new_message_id = sender.send_card(
                                chat_id=chat_id,
                                card=card,
                                idempotency_key=(
                                    f'feishu-card-recover:{message_id}:'
                                    f'{expected_revision}'
                                ),
                            )
                            if not new_message_id:
                                raise FeishuRuntimeError(
                                    'Feishu replacement card returned no message id'
                                )
                            saved = self._store.save_feishu_workspace_message(
                                account_id,
                                address_hash,
                                new_message_id,
                                expected_operation_id,
                                message_id,
                                expected_revision,
                            )
                            if saved.get('message_id') != new_message_id:
                                self._expire_workspace_card(
                                    account_id=account_id,
                                    address_hash=address_hash,
                                    message_id=new_message_id,
                                    current_message_id=str(
                                        saved.get('message_id') or ''
                                    ),
                                    language=str(
                                        saved.get('output_language') or 'zh'
                                    ),
                                )
                            return
                    except Exception as recovery_exc:
                        retry_error = recovery_exc
                        if attempt == len(_ACTION_REFRESH_RETRY_DELAYS):
                            _logger.exception(
                                'feishu_card_action_recovery_failed '
                                'account_id=%s message_id=%s',
                                account_id,
                                message_id,
                            )
                if attempt == len(_ACTION_REFRESH_RETRY_DELAYS):
                    _logger.exception(
                        'feishu_card_action_refresh_failed '
                        'account_id=%s message_id=%s',
                        account_id,
                        message_id,
                    )
                else:
                    retry_after = getattr(
                        retry_error,
                        'retry_after_seconds',
                        None,
                    )
                    delay = _ACTION_REFRESH_RETRY_DELAYS[attempt]
                    if isinstance(retry_after, (int, float)):
                        retry_after = float(retry_after)
                        if math.isfinite(retry_after) and retry_after >= 0:
                            delay = max(delay, retry_after)
                    time.sleep(delay)
            finally:
                if sender is not None:
                    sender.close()

    @staticmethod
    def _log_action_ready(
        action: FeishuInboundAction,
        *,
        started_at: float,
        cached: bool,
    ) -> None:
        workspace_action = action.workspace_action or {}
        _logger.info(
            'feishu_card_action_ready action=%s view=%s cached=%s '
            'elapsed_ms=%.1f',
            action.action,
            str(workspace_action.get('view') or ''),
            cached,
            (time.monotonic() - started_at) * 1000,
        )

    def _apply_workspace_action(
        self,
        *,
        workspace: FeishuWorkspaceState,
        action: dict | None,
    ) -> None:
        if not action:
            return
        kind = str(action.get('kind') or '')
        if kind == 'navigate':
            target = str(action.get('view') or '')
            workspace.navigate(target)
        elif kind == 'capability.open':
            workspace.open_capability_category(
                category=str(
                    action.get('category')
                    or workspace.capability_category
                ),
            )
        elif kind == 'capability.page':
            page = action.get('page')
            if isinstance(page, int) and not isinstance(page, bool):
                workspace.capability_page = max(0, page)
            workspace.view = 'capabilities'
        elif kind == 'history.switch':
            target_view = str(action.get('view') or 'conversations')
            workspace.view = (
                target_view
                if target_view in {'assistant', 'conversations'}
                else 'conversations'
            )
        elif kind == 'history.open':
            target_view = str(action.get('view') or 'conversations')
            workspace.view = (
                target_view
                if target_view in {'assistant', 'conversations'}
                else 'conversations'
            )
        elif kind == 'assistant.select':
            assistant = str(action.get('assistant') or '')
            if assistant in ASSISTANT_PROVIDERS:
                workspace.assistant = assistant
            workspace.view = 'assistant'
        elif kind == 'new_session.open':
            workspace.open_new_session()
        elif kind == 'new_session.cancel':
            workspace.new_session_open = False
            workspace.view = 'conversations'
        elif kind == 'new_session.create':
            workspace.prepare_new_session()
        elif kind == 'setting.update':
            target_view = str(action.get('view') or 'capabilities')
            if target_view in {'capabilities', 'assistant', 'settings'}:
                workspace.view = target_view
        elif kind == 'preference':
            name = str(action.get('name') or '')
            value = action.get('value')
            if name == 'thinking_depth' and value in {
                'low',
                'medium',
                'high',
                'max',
            }:
                workspace.thinking_depth = str(value)
            elif name == 'output_language' and value in {
                'zh',
                'en',
            }:
                workspace.output_language = str(value)
            elif name == 'show_sources' and isinstance(value, bool):
                workspace.show_sources = value
            workspace.view = 'settings'
        elif kind == 'maintenance.reset_preferences':
            workspace.reset_preferences()
            workspace.view = 'settings'
        elif kind == 'maintenance.new_conversation':
            workspace.prepare_new_session()

    def _workspace_provider_context(
        self,
        *,
        account_id: str,
        address_hash: str,
        workspace: FeishuWorkspaceState,
        chat_id: str,
        conversation_id: str,
        workspace_action: dict | None = None,
    ) -> dict:
        navigation: dict[str, Any] = {}
        if not conversation_id:
            navigation = self._store.get_navigation_state(
                account_id,
                address_hash,
            ) or {}
        execution = ChannelExecutionContext(
            thinking_depth=(
                workspace.thinking_depth
                if workspace.thinking_depth in {
                    'low',
                    'medium',
                    'high',
                    'max',
                }
                else None
            ),
            include_capability_settings=(
                workspace.view == 'capabilities'
                and bool(
                    conversation_id
                    or navigation.get('mode') == 'new_pending'
                )
            ),
            include_assistant_catalog=(workspace.view == 'assistant'),
        )
        return {
            'chat_id': chat_id,
            'surface': 'card',
            'workspace_conversation_id': conversation_id,
            'workspace_message_id': workspace.message_id,
            'workspace_operation_id': workspace.active_operation_id,
            'workspace_state': workspace.to_dict(),
            'new_conversation_pending': (
                navigation.get('mode') == 'new_pending'
            ),
            'channel_execution': execution.to_dict(),
            'workspace_action': dict(workspace_action or {}),
        }

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


def _image_data_url(content: bytes) -> str:
    if content.startswith(b'\x89PNG\r\n\x1a\n'):
        media_type = 'image/png'
    elif content.startswith((b'GIF87a', b'GIF89a')):
        media_type = 'image/gif'
    elif content.startswith(b'RIFF') and content[8:12] == b'WEBP':
        media_type = 'image/webp'
    else:
        media_type = 'image/jpeg'
    encoded = base64.b64encode(content).decode('ascii')
    return f'data:{media_type};base64,{encoded}'


def _chat_command_action(
    message: str,
    *,
    required: bool,
) -> dict[str, Any] | None:
    if not required:
        return None
    return {
        'schema_version': '1',
        'command': 'chat',
        'parameters': {
            'message': message,
        },
    }

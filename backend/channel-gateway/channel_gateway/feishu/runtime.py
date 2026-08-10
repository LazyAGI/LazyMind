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
    ClaimedInbound,
    InboundEnvelope,
    OutboundMessage,
)
from channel_gateway.common.domain.chat import (
    ChannelAttachment,
    ChannelExecutionContext,
)
from channel_gateway.common.ports.core import LazyMindCore
from channel_gateway.common.ports.providers import (
    RuntimeCredentialStore,
    RuntimeLease,
)
from channel_gateway.feishu.assistant import (
    assistant_view_with_ui,
    detail_conversation_id,
    detail_readonly,
    detail_run_status,
    detail_snapshot,
    detail_with_prompt,
    detail_view,
    projects_view,
    sessions_view,
    user_input_answers,
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
from channel_gateway.feishu.presentation import (
    FeishuReplyRenderer,
)
from channel_gateway.feishu.registration import configure_bot_menu
from channel_gateway.feishu.workspace import (
    FeishuWorkspaceRenderer,
    FeishuWorkspaceState,
    MENU_EVENT_VIEWS,
    menu_command,
    new_conversation_resources,
)
_logger = logging.getLogger(__name__)


_MAX_INBOUND_IMAGE_BYTES = 10 * 1024 * 1024
_ACTION_REFRESH_DELAY_SECONDS = 0.35
_ACTION_REFRESH_RETRY_DELAYS = (0.5, 1.0, 2.0, 4.0)
_ASSISTANT_PROVIDER = 'codex'
_ASSISTANT_TURN_PAGE_SIZE = 1
_ASSISTANT_PROJECT_PAGE_SIZE = 6
_ASSISTANT_SESSION_PAGE_SIZE = 4
_BOT_MENU_CONFIG_VERSION = 4


_RESULT_WORKSPACE_ACTIONS = {
    'maintenance.clear_conversation',
    'new_session.create',
    'prompt.run',
}
_REMOTE_ASSISTANT_ACTIONS = {
    'assistant.refresh',
    'assistant.retry',
    'assistant.project',
    'assistant.projects',
    'assistant.projects_page',
    'assistant.new',
    'assistant.open',
    'assistant.back',
    'assistant.sessions_page',
    'assistant.turns_page',
    'assistant.answer_page',
    'assistant.respond',
    'assistant.release',
    'assistant.cancel',
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


class FeishuRuntime:
    """Owns one leased Feishu WebSocket per app and routes owner DMs."""

    def __init__(
        self,
        *,
        store: FeishuRuntimeRepository,
        credentials: RuntimeCredentialStore,
        channels: FeishuReceiverFactory,
        addresses: FeishuAddressFactory,
        core: LazyMindCore,
    ):
        self._store = store
        self._credentials = credentials
        self._channels = channels
        self._addresses = addresses
        self._core = core
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
        try:
            configure_bot_menu(
                credentials.app_id,
                credentials.app_secret,
                publish_version=f'1.0.{_BOT_MENU_CONFIG_VERSION}',
            )
        except FeishuRuntimeError as exc:
            _logger.warning(
                'feishu_bot_menu_configuration_pending '
                'account_id=%s error=%s',
                account_id,
                str(exc)[:500],
            )
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
            or (not message.text and not message.image_key)
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
        self._remember_direct_chat(route.account_id, message.chat_id)
        effective_text = message.text or '请描述并分析这张图片。'
        chat_inputs: list[dict[str, str]] = []
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
                raise FeishuRuntimeError('飞书图片为空或超过 10 MB')
            chat_inputs.append(
                {
                    'input_type': 'image',
                    'input_base64': _image_data_url(content),
                }
            )
        workspace = FeishuWorkspaceState.from_dict(
            self._store.get_feishu_workspace_state(
                route.account_id,
                address_hash,
            )
        )
        workspace_revision = workspace.revision
        message_key = hashlib.sha256(
            message.message_id.encode('utf-8')
        ).hexdigest()
        assistant_detail = (
            workspace.view == 'assistant'
            and workspace.assistant_mode == 'detail'
        )
        if workspace.view == 'assistant' and not assistant_detail:
            self._send_input_notice(
                route=route,
                chat_id=message.chat_id,
                message_id=message.message_id,
                text='请先在 Codex 卡片中选择项目和会话，再从底部输入框发送任务。',
            )
            return
        assistant_view: dict[str, Any] = {}
        if assistant_detail and not workspace.assistant_selected_thread_id:
            self._send_input_notice(
                route=route,
                chat_id=message.chat_id,
                message_id=message.message_id,
                text='Codex 会话尚未准备完成，本次消息未提交，请稍后重试。',
            )
            return
        if assistant_detail:
            try:
                assistant_view = self._read_assistant_detail(
                    owner_user_id=route.owner_user_id,
                    request_id=f'feishu_assistant_read_{message_key[:24]}',
                    provider=_ASSISTANT_PROVIDER,
                    thread_id=workspace.assistant_selected_thread_id,
                )
            except Exception:
                self._send_input_notice(
                    route=route,
                    chat_id=message.chat_id,
                    message_id=message.message_id,
                    text='Codex 会话读取失败，本次消息未提交；最后一张有效卡片已保留。',
                )
                return
        assistant_selected = (
            assistant_detail
            and bool(workspace.assistant_selected_thread_id)
        )
        assistant_run_status = detail_run_status(assistant_view)
        if assistant_selected and (
            assistant_run_status in {
                'running',
                'waiting_for_input',
                'releasing',
                'release_failed',
            }
        ):
            self._send_input_notice(
                route=route,
                chat_id=message.chat_id,
                message_id=message.message_id,
                text='Codex 正在处理上一项任务，本次消息未提交。',
            )
            return
        if assistant_selected and detail_readonly(assistant_view):
            self._send_input_notice(
                route=route,
                chat_id=message.chat_id,
                message_id=message.message_id,
                text='当前 Codex 会话为只读，本次消息未提交。',
            )
            return
        assistant_active = (
            assistant_selected
            and bool(detail_conversation_id(assistant_view))
        )
        if assistant_selected and not assistant_active:
            try:
                self._core.bind_external_thread(
                    owner_user_id=route.owner_user_id,
                    request_id=f'feishu_assistant_bind_{message_key[:24]}',
                    provider=_ASSISTANT_PROVIDER,
                    provider_thread_id=workspace.assistant_selected_thread_id,
                )
                assistant_view = self._read_assistant_detail(
                    owner_user_id=route.owner_user_id,
                    request_id=f'feishu_assistant_bound_{message_key[:24]}',
                    provider=_ASSISTANT_PROVIDER,
                    thread_id=workspace.assistant_selected_thread_id,
                )
                assistant_active = bool(
                    detail_conversation_id(assistant_view)
                )
                if not assistant_active:
                    raise FeishuRuntimeError(
                        'Codex binding did not return a conversation'
                    )
            except Exception as exc:
                if workspace.message_id:
                    self._schedule_action_card_refresh(
                        route.account_id,
                        workspace.message_id,
                        FeishuWorkspaceRenderer.render(
                            provider_context={
                                'chat_id': message.chat_id,
                                'workspace_state': workspace.to_dict(),
                                'assistant_view': assistant_view_with_ui(
                                    assistant_view,
                                    'error',
                                    str(exc),
                                ),
                            },
                            presentations=[],
                        ),
                        address_hash=address_hash,
                        expected_revision=workspace.revision,
                        expected_operation_id=workspace.active_operation_id,
                    )
                return
        conversation_id = self._store.get_route(
            route.account_id,
            address_hash,
        )
        expected_revision = workspace_revision
        expected_message_id = workspace.message_id
        expected_operation_id = workspace.active_operation_id
        is_new_operation = workspace.active_operation_id != message_key
        assistant_render_view = assistant_view
        if assistant_active:
            workspace.active_operation_id = message_key
            workspace.assistant_answer_page = 0
            workspace.images = []
            assistant_view = detail_with_prompt(
                assistant_view,
                effective_text,
            )
            assistant_render_view = assistant_view_with_ui(
                assistant_view,
                'dispatching',
            )
        else:
            workspace.begin_operation(message_key)
        workspace.advance()
        workspace_context = (
            {
                'chat_id': message.chat_id,
                'surface': 'card',
                'workspace_message_id': workspace.message_id,
                'workspace_operation_id': workspace.active_operation_id,
                'workspace_state': workspace.to_dict(),
            }
            if assistant_active
            else self._workspace_provider_context(
                account_id=route.account_id,
                address_hash=address_hash,
                workspace=workspace,
                chat_id=message.chat_id,
                conversation_id=conversation_id,
            )
        )
        execution = ChannelExecutionContext.from_provider_context(
            workspace_context
        )
        if assistant_active:
            execution = ChannelExecutionContext(
                external_agent_conversation_id=detail_conversation_id(
                    assistant_view
                )
            )
        elif chat_inputs:
            execution = replace(
                execution,
                attachments=tuple(
                    attachment
                    for item in chat_inputs
                    if (
                        attachment := ChannelAttachment.from_dict(item)
                    ) is not None
                ),
            )
        envelope = InboundEnvelope(
            provider='feishu',
            account_id=route.account_id,
            message_key=message_key,
            order_key=address_hash,
            external_address_hash=address_hash,
            owner_user_id=route.owner_user_id,
            recipient_id=message.chat_id,
            text=effective_text,
            provider_context={
                **workspace_context,
                'workspace_surface': (
                    'assistant' if assistant_active else 'reply'
                ),
                'workspace_message_id': (
                    workspace.message_id
                    if assistant_active
                    else ''
                ),
                'channel_execution': execution.to_dict(),
                **({
                    'assistant_view': assistant_view,
                } if assistant_active else {}),
                'command_action': _chat_command_action(
                    effective_text,
                    required=bool(assistant_active or chat_inputs),
                ),
            },
        )
        try:
            if is_new_operation and assistant_active:
                workspace.message_id = self._move_assistant_card_to_bottom(
                    route=route,
                    chat_id=message.chat_id,
                    inbound_message_id=message.message_id,
                    workspace=workspace,
                    assistant_view=assistant_render_view,
                )
                envelope.provider_context['workspace_message_id'] = (
                    workspace.message_id
                )
            envelope.provider_context['workspace_state'] = workspace.to_dict()
            envelope.provider_context['workspace_message_id'] = (
                workspace.message_id
                if assistant_active
                else ''
            )
            claimed = self._store.claim_feishu_workspace_and_ingest(
                route.account_id,
                address_hash,
                workspace.to_dict(),
                expected_revision,
                expected_message_id,
                expected_operation_id,
                envelope,
                lease.fence,
            )
            if not claimed:
                if assistant_active:
                    current = FeishuWorkspaceState.from_dict(
                        self._store.get_feishu_workspace_state(
                            route.account_id,
                            address_hash,
                        )
                    )
                    self._expire_workspace_card(
                        account_id=route.account_id,
                        address_hash=address_hash,
                        message_id=workspace.message_id,
                        current_message_id=current.message_id,
                        language=current.output_language,
                    )
                else:
                    self._send_input_notice(
                        route=route,
                        chat_id=message.chat_id,
                        message_id=message.message_id,
                        text='上一项操作仍在处理中，本次消息未提交，请稍后重试。',
                    )
                return
        except Exception:
            current = FeishuWorkspaceState.from_dict(
                self._store.get_feishu_workspace_state(
                    route.account_id,
                    address_hash,
                )
            )
            if (
                assistant_active
                and current.message_id != workspace.message_id
            ):
                self._expire_workspace_card(
                    account_id=route.account_id,
                    address_hash=address_hash,
                    message_id=workspace.message_id,
                    current_message_id=current.message_id,
                    language=current.output_language,
                )
            raise

    def _move_assistant_card_to_bottom(
        self,
        *,
        route: _AccountRoute,
        chat_id: str,
        inbound_message_id: str,
        workspace: FeishuWorkspaceState,
        assistant_view: dict[str, Any],
    ) -> str:
        old_message_id = workspace.message_id
        sender = None
        try:
            account = self._credentials.load_runtime_account(
                route.account_id
            )
            sender = self._channels.create_sender(account['credentials'])
            provider_context = {
                'chat_id': chat_id,
                'workspace_state': workspace.to_dict(),
                'assistant_view': assistant_view,
            }
            card = FeishuWorkspaceRenderer.render(
                provider_context=provider_context,
                presentations=[],
                streaming=True,
            )
            return self._send_card_to_bottom(
                sender=sender,
                chat_id=chat_id,
                card=card,
                idempotency_key=(
                    f'feishu-workspace-bottom:{inbound_message_id}'
                ),
            )
        except Exception:
            _logger.warning(
                'feishu_workspace_card_move_failed message_id=%s',
                old_message_id,
                exc_info=True,
            )
            return old_message_id
        finally:
            if sender is not None:
                sender.close()

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
                idempotency_key=f'feishu-assistant-input:{message_id}',
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

    def handle_inbound_action(
        self,
        message: ClaimedInbound,
    ) -> OutboundMessage | None:
        context = dict(message.provider_context)
        action = context.get('assistant_action')
        if (
            context.get('workspace_surface') != 'assistant'
            or not isinstance(action, dict)
        ):
            return None
        source = FeishuWorkspaceState.from_dict(context.get('workspace_state'))
        current = FeishuWorkspaceState.from_dict(
            self._store.get_feishu_workspace_state(
                message.account_id,
                message.order_key,
            )
        )
        if (
            current.view != 'assistant'
            or current.message_id != source.message_id
            or current.active_operation_id != source.active_operation_id
            or current.active_operation_id != message.message_key
        ):
            return self._assistant_action_outbound(
                message,
                current,
                {},
                suppressed=True,
            )
        request_id = f'channel_{message.message_key[:24]}'
        if current.revision == source.revision + 1:
            try:
                view = self._load_current_assistant_view(
                    current,
                    message.owner_user_id,
                    request_id + '_recover',
                )
            except Exception as exc:
                view = assistant_view_with_ui({}, 'error', str(exc))
            return self._assistant_action_outbound(message, current, view)
        if current.revision != source.revision:
            return self._assistant_action_outbound(
                message,
                current,
                {},
                suppressed=True,
            )
        try:
            view = self._execute_remote_assistant_action(
                workspace=current,
                owner_user_id=message.owner_user_id,
                request_id=request_id,
                values=action,
            )
        except Exception as exc:
            _logger.warning(
                'feishu_assistant_action_failed kind=%s',
                str(action.get('kind') or ''),
                exc_info=True,
            )
            try:
                view = self._load_current_assistant_view(
                    current,
                    message.owner_user_id,
                    request_id + '_error_view',
                )
            except Exception:
                view = {}
            view = assistant_view_with_ui(view, 'error', str(exc))
        expected_revision = current.revision
        current.advance()
        if not self._store.save_feishu_workspace_state_if_revision(
            message.account_id,
            message.order_key,
            current.to_dict(),
            expected_revision,
        ):
            authoritative = FeishuWorkspaceState.from_dict(
                self._store.get_feishu_workspace_state(
                    message.account_id,
                    message.order_key,
                )
            )
            if (
                authoritative.revision != source.revision + 1
                or authoritative.active_operation_id
                != source.active_operation_id
            ):
                return self._assistant_action_outbound(
                    message,
                    authoritative,
                    {},
                    suppressed=True,
                )
            current = authoritative
        return self._assistant_action_outbound(message, current, view)

    def _assistant_action_outbound(
        self,
        message: ClaimedInbound,
        workspace: FeishuWorkspaceState,
        view: dict[str, Any],
        *,
        suppressed: bool = False,
    ) -> OutboundMessage:
        context = {
            **message.provider_context,
            'workspace_state': workspace.to_dict(),
            'workspace_message_id': workspace.message_id,
            'workspace_operation_id': workspace.active_operation_id,
            'assistant_view': view,
        }
        context.pop('assistant_action', None)
        if suppressed:
            context['_workspace_delivery_suppressed'] = True
        return OutboundMessage(
            provider=message.provider,
            account_id=message.account_id,
            order_key=message.order_key,
            recipient_id=message.recipient_id,
            provider_context=context,
            text='',
            intent_kind='external_agent',
        )

    def _load_current_assistant_view(
        self,
        workspace: FeishuWorkspaceState,
        owner_user_id: str,
        request_id: str,
    ) -> dict[str, Any]:
        if workspace.assistant_mode == 'projects':
            return self._load_assistant_projects(
                workspace,
                owner_user_id,
                request_id,
                workspace.assistant_projects_cursor,
            )
        if workspace.assistant_mode == 'sessions':
            return self._load_assistant_threads(
                workspace,
                owner_user_id,
                request_id,
                workspace.assistant_threads_cursor,
            )
        return self._read_assistant_detail(
            owner_user_id=owner_user_id,
            request_id=request_id,
            provider=_ASSISTANT_PROVIDER,
            thread_id=workspace.assistant_selected_thread_id,
        )

    def _handle_assistant_action(
        self,
        *,
        route: _AccountRoute,
        action: FeishuInboundAction,
        address_hash: str,
        workspace: FeishuWorkspaceState,
        conversation_id: str,
        message_key: str,
        runtime_fence: Any,
    ) -> dict[str, Any] | None:
        values = dict(action.workspace_action or {})
        if (
            str(values.get('kind') or '') == 'assistant.respond'
            and isinstance(action.ask_answers_structured, dict)
        ):
            values['answers'] = user_input_answers(
                action.ask_answers_structured
            )
        kind = str(values.get('kind') or '')
        expected_mode = {
            'assistant.project': 'projects',
            'assistant.projects_page': 'projects',
            'assistant.projects': 'sessions',
            'assistant.sessions_page': 'sessions',
            'assistant.open': 'sessions',
            'assistant.new': 'sessions',
            'assistant.back': 'detail',
            'assistant.turns_page': 'detail',
            'assistant.answer_page': 'detail',
            'assistant.respond': 'detail',
            'assistant.release': 'detail',
        }.get(kind)
        if expected_mode and workspace.assistant_mode != expected_mode:
            return None
        required_values = {
            'assistant.project': {
                'expected_projects_cursor',
                'expected_project_page',
            },
            'assistant.projects_page': {
                'expected_projects_cursor',
                'expected_project_page',
            },
            'assistant.projects': {'expected_project_cwd'},
            'assistant.sessions_page': {
                'expected_project_cwd',
                'expected_threads_cursor',
                'expected_threads_page',
            },
            'assistant.open': {
                'thread_id',
                'expected_project_cwd',
                'expected_threads_cursor',
                'expected_threads_page',
            },
            'assistant.new': {
                'expected_project_cwd',
                'expected_threads_cursor',
                'expected_threads_page',
            },
            'assistant.back': {'thread_id'},
            'assistant.turns_page': {'thread_id'},
            'assistant.answer_page': {
                'thread_id',
                'expected_answer_page',
            },
            'assistant.respond': {
                'thread_id',
                'request_id',
                'request_kind',
            },
            'assistant.release': {'thread_id'},
        }.get(kind, set())
        if not required_values.issubset(values):
            return None
        expected_navigation = {
            'expected_project_cwd': workspace.assistant_project_cwd,
            'expected_projects_cursor': workspace.assistant_projects_cursor,
            'expected_project_page': workspace.assistant_project_page,
            'expected_threads_cursor': workspace.assistant_threads_cursor,
            'expected_threads_page': workspace.assistant_threads_page,
            'expected_answer_page': workspace.assistant_answer_page,
        }
        if any(
            key in values and str(values.get(key)) != str(current)
            for key, current in expected_navigation.items()
        ):
            return None
        action_thread_id = str(values.get('thread_id') or '')
        if (
            kind in {
                'assistant.back',
                'assistant.turns_page',
                'assistant.answer_page',
                'assistant.respond',
                'assistant.release',
            }
            and action_thread_id
            and action_thread_id != workspace.assistant_selected_thread_id
        ):
            return None
        if kind in _REMOTE_ASSISTANT_ACTIONS:
            source_revision = workspace.revision
            source_message_id = workspace.message_id
            source_operation_id = workspace.active_operation_id
            workspace.bind_message(action.message_id)
            workspace.begin_operation(message_key)
            workspace.advance()
            provider_context = {
                **self._workspace_provider_context(
                    account_id=route.account_id,
                    address_hash=address_hash,
                    workspace=workspace,
                    chat_id=action.chat_id,
                    conversation_id=conversation_id,
                    workspace_action=values,
                ),
                'workspace_surface': 'assistant',
                'assistant_action': values,
            }
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
            )
            claimed = self._store.claim_feishu_workspace_and_ingest(
                route.account_id,
                address_hash,
                workspace.to_dict(),
                source_revision,
                source_message_id,
                source_operation_id,
                envelope,
                runtime_fence,
            )
            if not claimed:
                return None
            return None
        card = FeishuWorkspaceRenderer.render(
            provider_context={
                'chat_id': action.chat_id,
                'workspace_state': workspace.to_dict(),
                'assistant_view': assistant_view_with_ui(
                    {},
                    'error',
                    'Unsupported assistant action',
                ),
            },
            presentations=[],
        )
        card['config'].pop('streaming_config', None)
        return card

    def _execute_remote_assistant_action(
        self,
        *,
        workspace: FeishuWorkspaceState,
        owner_user_id: str,
        request_id: str,
        values: dict[str, Any],
    ) -> dict[str, Any]:
        kind = str(values.get('kind') or '')
        if kind in {'assistant.refresh', 'assistant.retry'}:
            if workspace.assistant_mode == 'projects':
                return self._load_assistant_projects(
                    workspace,
                    owner_user_id,
                    request_id,
                )
            if workspace.assistant_mode == 'sessions':
                workspace.assistant_threads_previous_cursors = []
                workspace.assistant_threads_page = 0
                return self._load_assistant_threads(
                    workspace,
                    owner_user_id,
                    request_id,
                    '',
                )
            if not workspace.assistant_selected_thread_id:
                workspace.leave_assistant_thread()
                return self._load_assistant_threads(
                    workspace,
                    owner_user_id,
                    request_id,
                    workspace.assistant_threads_cursor,
                )
            return self._read_assistant_detail(
                owner_user_id=owner_user_id,
                request_id=request_id,
                provider=_ASSISTANT_PROVIDER,
                thread_id=workspace.assistant_selected_thread_id,
            )
        if kind == 'assistant.project':
            cwd = str(values.get('project_cwd') or '')[:500]
            if not cwd:
                raise FeishuRuntimeError('Codex project cwd is missing')
            view = self._load_assistant_threads(
                workspace,
                owner_user_id,
                request_id,
                '',
                cwd=cwd,
            )
            workspace.assistant_mode = 'sessions'
            workspace.assistant_project_cwd = cwd
            workspace.assistant_threads_previous_cursors = []
            workspace.assistant_threads_page = 0
            return view
        if kind == 'assistant.projects':
            view = self._load_assistant_projects(
                workspace,
                owner_user_id,
                request_id,
            )
            workspace.assistant_mode = 'projects'
            workspace.assistant_project_page = 0
            workspace.assistant_project_cwd = ''
            workspace.assistant_projects_previous_cursors = []
            return view
        if kind == 'assistant.projects_page':
            direction = str(values.get('direction') or '')
            cursor = workspace.assistant_projects_cursor
            previous = list(
                workspace.assistant_projects_previous_cursors
            )
            page = workspace.assistant_project_page
            if direction == 'next':
                if not workspace.assistant_projects_next_cursor:
                    return self._load_assistant_projects(
                        workspace,
                        owner_user_id,
                        request_id,
                        cursor,
                    )
                previous.append(cursor)
                cursor = workspace.assistant_projects_next_cursor
                page += 1
            elif direction == 'previous':
                if not previous:
                    return self._load_assistant_projects(
                        workspace,
                        owner_user_id,
                        request_id,
                        cursor,
                    )
                cursor = previous.pop()
                page = max(0, page - 1)
            else:
                raise FeishuRuntimeError('Unsupported project page direction')
            view = self._load_assistant_projects(
                workspace,
                owner_user_id,
                request_id,
                cursor,
            )
            workspace.assistant_projects_previous_cursors = previous
            workspace.assistant_project_page = page
            return view
        if kind == 'assistant.sessions_page':
            direction = str(values.get('direction') or '')
            cursor = workspace.assistant_threads_cursor
            previous = list(workspace.assistant_threads_previous_cursors)
            page = workspace.assistant_threads_page
            if direction == 'older':
                if not workspace.assistant_threads_next_cursor:
                    return self._load_assistant_threads(
                        workspace,
                        owner_user_id,
                        request_id,
                        cursor,
                    )
                previous.append(cursor)
                cursor = workspace.assistant_threads_next_cursor
                page += 1
            elif direction == 'newer':
                if not previous:
                    return self._load_assistant_threads(
                        workspace,
                        owner_user_id,
                        request_id,
                        cursor,
                    )
                cursor = previous.pop()
                page = max(0, page - 1)
            else:
                raise FeishuRuntimeError('Unsupported assistant page direction')
            view = self._load_assistant_threads(
                workspace,
                owner_user_id,
                request_id,
                cursor,
            )
            workspace.assistant_threads_previous_cursors = previous
            workspace.assistant_threads_page = page
            return view
        if kind == 'assistant.back':
            if not workspace.assistant_selected_thread_id:
                workspace.leave_assistant_thread()
                return self._load_assistant_threads(
                    workspace,
                    owner_user_id,
                    request_id,
                    workspace.assistant_threads_cursor,
                )
            view = self._read_assistant_detail(
                owner_user_id=owner_user_id,
                request_id=request_id,
                provider=_ASSISTANT_PROVIDER,
                thread_id=workspace.assistant_selected_thread_id,
            )
            if detail_run_status(view) in {
                'running',
                'waiting_for_input',
                'releasing',
                'release_failed',
            }:
                return view
            conversation_id = detail_conversation_id(view)
            if conversation_id:
                self._core.release_external_conversation(
                    owner_user_id=owner_user_id,
                    request_id=request_id,
                    conversation_id=conversation_id,
                )
            workspace.leave_assistant_thread()
            return self._load_assistant_threads(
                workspace,
                owner_user_id,
                request_id,
                workspace.assistant_threads_cursor,
            )
        if kind == 'assistant.open':
            thread_id = str(values.get('thread_id') or '')
            view = self._read_assistant_detail(
                owner_user_id=owner_user_id,
                request_id=request_id,
                provider=_ASSISTANT_PROVIDER,
                thread_id=thread_id,
            )
            workspace.assistant_mode = 'detail'
            workspace.assistant_selected_thread_id = thread_id
            workspace.assistant_answer_page = 0
            workspace.images = []
            return view
        if kind == 'assistant.new':
            cwd = str(values.get('cwd') or workspace.assistant_project_cwd)
            binding = self._core.bind_external_thread(
                owner_user_id=owner_user_id,
                request_id=request_id,
                provider=_ASSISTANT_PROVIDER,
                provider_thread_id='',
                new_session=True,
                cwd=cwd,
                display_name=str(values.get('display_name') or '')[:200],
            )
            binding_data = dict(binding.get('binding') or binding)
            native_thread = dict(binding.get('thread') or {})
            thread_id = str(
                binding_data.get('provider_thread_id')
                or native_thread.get('id')
                or binding.get('thread_id')
                or ''
            )
            if not thread_id:
                raise FeishuRuntimeError(
                    'Codex did not return a thread binding'
                )
            workspace.assistant_mode = 'detail'
            workspace.assistant_selected_thread_id = thread_id
            workspace.assistant_answer_page = 0
            workspace.images = []
            try:
                view = self._read_assistant_detail(
                    owner_user_id=owner_user_id,
                    request_id=request_id,
                    provider=_ASSISTANT_PROVIDER,
                    thread_id=thread_id,
                )
            except Exception as exc:
                native_thread.update({
                    'id': thread_id,
                    'cwd': str(native_thread.get('cwd') or cwd),
                    'conversation_id': str(
                        binding_data.get('conversation_id') or ''
                    ),
                    'created_by_lazymind': True,
                    'available': False,
                    'controlled_by_lazymind': False,
                })
                return assistant_view_with_ui(
                    detail_view({
                        'thread': native_thread,
                        'turns': [],
                        'offset': 0,
                        'total_turns': 0,
                        'snapshot': {
                            'conversation_id': str(
                                binding_data.get('conversation_id') or ''
                            ),
                        },
                    }),
                    'error',
                    (
                        '会话已创建，但 Codex 详情暂时不可用：'
                        f'{str(exc)[:300]}'
                    ),
                )
            return view
        if kind in {'assistant.turns_page', 'assistant.answer_page'}:
            direction = str(values.get('direction') or '')
            offset = max(0, int(values.get('offset') or 0))
            total = max(0, int(values.get('total_turns') or 0))
            selection = values.get('selection')
            if kind == 'assistant.turns_page':
                if selection is not None and str(selection).strip():
                    page = max(0, int(str(selection)))
                    offset = min(
                        max(0, total - _ASSISTANT_TURN_PAGE_SIZE),
                        page * _ASSISTANT_TURN_PAGE_SIZE,
                    )
                elif direction == 'older':
                    offset = max(0, offset - _ASSISTANT_TURN_PAGE_SIZE)
                elif direction == 'newer':
                    offset = min(
                        max(0, total - _ASSISTANT_TURN_PAGE_SIZE),
                        offset + _ASSISTANT_TURN_PAGE_SIZE,
                    )
            page = self._core.read_external_thread(
                owner_user_id=owner_user_id,
                request_id=request_id,
                provider=_ASSISTANT_PROVIDER,
                thread_id=workspace.assistant_selected_thread_id,
                offset=offset,
                limit=_ASSISTANT_TURN_PAGE_SIZE,
            )
            view = detail_view(page)
            if kind == 'assistant.turns_page':
                workspace.assistant_answer_page = 0
            elif direction == 'previous':
                workspace.assistant_answer_page = max(
                    0,
                    workspace.assistant_answer_page - 1,
                )
            elif direction == 'next':
                workspace.assistant_answer_page += 1
            return view
        if kind == 'assistant.respond':
            view = self._read_assistant_detail(
                owner_user_id=owner_user_id,
                request_id=f'{request_id}_read',
                provider=_ASSISTANT_PROVIDER,
                thread_id=workspace.assistant_selected_thread_id,
            )
            snapshot = detail_snapshot(view)
            pending = snapshot.get('pending_request')
            pending = dict(pending) if isinstance(pending, dict) else {}
            external_request_id = str(values.get('request_id') or '')
            if external_request_id != str(pending.get('request_id') or ''):
                return view
            request_kind = str(values.get('request_kind') or '')
            if request_kind != str(pending.get('kind') or ''):
                return view
            action_id = str(values.get('action_id') or '').strip()
            if not action_id:
                raise FeishuRuntimeError('Codex request action is missing')
            answers = values.get('answers')
            answers = answers if isinstance(answers, dict) else None
            self._core.respond_external_request(
                owner_user_id=owner_user_id,
                request_id=request_id,
                external_request_id=external_request_id,
                action_id=action_id,
                answers=answers,
            )
            view = self._read_assistant_detail(
                owner_user_id=owner_user_id,
                request_id=request_id,
                provider=_ASSISTANT_PROVIDER,
                thread_id=workspace.assistant_selected_thread_id,
            )
            return view
        if kind == 'assistant.release':
            view = self._read_assistant_detail(
                owner_user_id=owner_user_id,
                request_id=f'{request_id}_read',
                provider=_ASSISTANT_PROVIDER,
                thread_id=workspace.assistant_selected_thread_id,
            )
            snapshot = detail_snapshot(view)
            conversation_id = detail_conversation_id(view)
            if not conversation_id:
                raise FeishuRuntimeError('Codex conversation is missing')
            control_release = str(snapshot.get('control_release') or '')
            if (
                str(snapshot.get('status') or '') == 'releasing'
                or control_release == 'pending'
                or control_release in {
                    'unsubscribed',
                    'notSubscribed',
                    'notLoaded',
                    'not_loaded',
                }
            ):
                return view
            try:
                self._core.release_external_conversation(
                    owner_user_id=owner_user_id,
                    request_id=request_id,
                    conversation_id=conversation_id,
                )
            except Exception:
                view = self._read_assistant_detail(
                    owner_user_id=owner_user_id,
                    request_id=request_id,
                    provider=_ASSISTANT_PROVIDER,
                    thread_id=workspace.assistant_selected_thread_id,
                )
                snapshot = detail_snapshot(view)
                if str(snapshot.get('control_release') or '') in {
                    'pending',
                    'failed',
                    'unsubscribed',
                    'notSubscribed',
                    'notLoaded',
                    'not_loaded',
                }:
                    return view
                raise
            view = self._read_assistant_detail(
                owner_user_id=owner_user_id,
                request_id=request_id,
                provider=_ASSISTANT_PROVIDER,
                thread_id=workspace.assistant_selected_thread_id,
            )
            return view
        if kind == 'assistant.cancel':
            view = self._read_assistant_detail(
                owner_user_id=owner_user_id,
                request_id=f'{request_id}_read',
                provider=_ASSISTANT_PROVIDER,
                thread_id=workspace.assistant_selected_thread_id,
            )
            snapshot = detail_snapshot(view)
            conversation_id = detail_conversation_id(view)
            expected_run_id = str(values.get('run_id') or '')
            if (
                detail_run_status(view) not in {
                    'running',
                    'waiting_for_input',
                }
                or not conversation_id
                or str(snapshot.get('run_id') or '') != expected_run_id
            ):
                return view
            self._core.interrupt_external_conversation(
                owner_user_id=owner_user_id,
                request_id=request_id,
                conversation_id=conversation_id,
                expected_run_id=expected_run_id,
            )
            return assistant_view_with_ui(view, 'cancelling')
        raise FeishuRuntimeError('Unsupported assistant action')

    def _read_assistant_detail(
        self,
        *,
        owner_user_id: str,
        request_id: str,
        provider: str,
        thread_id: str,
    ) -> dict[str, Any]:
        if not thread_id:
            raise FeishuRuntimeError('Codex thread is missing')
        page = self._core.read_external_thread(
            owner_user_id=owner_user_id,
            request_id=request_id,
            provider=provider,
            thread_id=thread_id,
            offset=0,
            limit=_ASSISTANT_TURN_PAGE_SIZE,
            tail=True,
        )
        return detail_view(page)

    def _schedule_assistant_threads(
        self,
        *,
        route: _AccountRoute,
        address_hash: str,
        chat_id: str,
        revision: int,
    ) -> None:
        thread = threading.Thread(
            target=self._refresh_assistant_threads,
            args=(route, address_hash, chat_id, revision),
            name='feishu-assistant-threads',
            daemon=True,
        )
        thread.start()

    def _refresh_assistant_threads(
        self,
        route: _AccountRoute,
        address_hash: str,
        chat_id: str,
        revision: int,
    ) -> None:
        workspace = FeishuWorkspaceState.from_dict(
            self._store.get_feishu_workspace_state(route.account_id, address_hash)
        )
        if workspace.revision != revision or workspace.view != 'assistant':
            return
        assistant_view: dict[str, Any] = {}
        load_failed = False
        try:
            if workspace.assistant_mode == 'projects':
                assistant_view = self._load_assistant_projects(
                    workspace,
                    route.owner_user_id,
                    f'feishu_assistant_projects_{revision}',
                )
            else:
                assistant_view = self._load_assistant_threads(
                    workspace,
                    route.owner_user_id,
                    f'feishu_assistant_list_{revision}',
                    '',
                )
        except Exception as exc:
            load_failed = True
            assistant_view = assistant_view_with_ui({}, 'error', str(exc))
        current = FeishuWorkspaceState.from_dict(
            self._store.get_feishu_workspace_state(route.account_id, address_hash)
        )
        if current.revision != revision or current.view != 'assistant':
            return
        if load_failed:
            workspace = current
        else:
            workspace.advance()
            if not self._store.save_feishu_workspace_state_if_revision(
                route.account_id,
                address_hash,
                workspace.to_dict(),
                revision,
            ):
                return
        if not workspace.message_id:
            return
        self._schedule_action_card_refresh(
            route.account_id,
            workspace.message_id,
            FeishuWorkspaceRenderer.render(
                provider_context={
                    'chat_id': chat_id,
                    'workspace_state': workspace.to_dict(),
                    'assistant_view': assistant_view,
                },
                presentations=[],
            ),
            address_hash=address_hash,
            expected_revision=workspace.revision,
            expected_operation_id=workspace.active_operation_id,
        )

    def _load_assistant_projects(
        self,
        workspace: FeishuWorkspaceState,
        owner_user_id: str,
        request_id: str,
        cursor: str | None = None,
    ) -> dict[str, Any]:
        cursor = (
            workspace.assistant_projects_cursor
            if cursor is None
            else str(cursor)
        )
        response = self._core.list_external_projects(
            owner_user_id=owner_user_id,
            request_id=request_id,
            provider=_ASSISTANT_PROVIDER,
            cursor=cursor,
            limit=_ASSISTANT_PROJECT_PAGE_SIZE,
        )
        view = projects_view(response)
        workspace.assistant_projects_cursor = cursor[:512]
        workspace.assistant_projects_next_cursor = view['next_cursor']
        return view

    def _load_assistant_threads(
        self,
        workspace: FeishuWorkspaceState,
        owner_user_id: str,
        request_id: str,
        cursor: str,
        *,
        cwd: str | None = None,
    ) -> dict[str, Any]:
        project_cwd = (
            workspace.assistant_project_cwd
            if cwd is None
            else str(cwd)
        )
        response = self._core.list_external_threads(
            owner_user_id=owner_user_id,
            request_id=request_id,
            provider=_ASSISTANT_PROVIDER,
            cursor=str(cursor or ''),
            cwd=project_cwd,
            limit=_ASSISTANT_SESSION_PAGE_SIZE,
        )
        view = sessions_view(response)
        workspace.assistant_threads_cursor = str(cursor or '')[:512]
        workspace.assistant_threads_next_cursor = view['next_cursor']
        return view

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
            return
        started_at = time.monotonic()
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
        address_hash = address.route_hash
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
            self._expire_workspace_card(
                account_id=route.account_id,
                address_hash=address_hash,
                message_id=action.message_id,
                current_message_id=workspace.message_id,
                language=workspace.output_language,
            )
            return None
        action_kind = str((action.workspace_action or {}).get('kind') or '')
        action_data = action.workspace_action or {}
        if (
            action_kind
            and not action_kind.startswith('assistant.')
            and action_kind != 'operation.cancel'
        ):
            if (
                str(action_data.get('expected_view') or '')
                != workspace.view
                or action_data.get('expected_revision')
                != workspace.revision
                or str(action_data.get('expected_operation_id') or '')
                != workspace.active_operation_id
            ):
                if action.action == 'local' and workspace.view in {
                    'chat',
                    'settings',
                }:
                    card = FeishuWorkspaceRenderer.render(
                        provider_context=self._workspace_provider_context(
                            account_id=route.account_id,
                            address_hash=address_hash,
                            workspace=workspace,
                            chat_id=action.chat_id,
                            conversation_id=conversation_id,
                        ),
                        presentations=[],
                    )
                    self._log_action_ready(
                        action,
                        started_at=started_at,
                        cached=True,
                    )
                    return card
                return None
        if action_kind == 'setting.update':
            command_action = action.command_action
            parameters = (
                command_action.get('parameters')
                if isinstance(command_action, dict)
                else None
            )
            inner_conversation_id = (
                str(parameters.get('expected_conversation_id') or '')
                if isinstance(parameters, dict)
                else ''
            )
            target_view = str(
                action_data.get('view')
                or 'capabilities'
            )
            outer_conversation_id = str(
                action_data.get('expected_conversation_id') or ''
            )
            if (
                target_view not in {'capabilities', 'settings'}
                or not outer_conversation_id
                or inner_conversation_id != outer_conversation_id
                or outer_conversation_id != conversation_id
            ):
                return None
        if action_kind == 'new_session.workflow_mode':
            navigation = self._store.get_navigation_state(
                route.account_id,
                address_hash,
            ) or {}
            draft = self._store.get_new_conversation_draft(
                route.account_id,
                address_hash,
            )
            current_mode = str(draft.get('workflow_mode') or 'dynamic')
            if current_mode not in {'auto', 'dynamic'}:
                current_mode = 'dynamic'
            if (
                conversation_id
                or navigation.get('mode') != 'new_pending'
                or not workspace.active_operation_id
                or str(action_data.get('expected_mode') or '')
                != current_mode
            ):
                return None
        if action_kind.startswith('assistant.'):
            if workspace.view != 'assistant':
                return None
            return self._handle_assistant_action(
                route=route,
                action=action,
                address_hash=address_hash,
                workspace=workspace,
                conversation_id=conversation_id or '',
                message_key=message_key,
                runtime_fence=lease.fence,
            )
        if action_kind == 'operation.cancel':
            action_thread_id = str(
                (action.workspace_action or {}).get('thread_id') or ''
            )
            action_operation_id = str(
                (action.workspace_action or {}).get('operation_id') or ''
            )
            action_run_id = str(
                (action.workspace_action or {}).get('run_id') or ''
            )
            assistant_cancel = any((
                action_thread_id,
                action_operation_id,
                action_run_id,
            ))
            if assistant_cancel and workspace.view != 'assistant':
                return None
            if (
                workspace.view == 'assistant'
                and (
                    not action_thread_id
                    or not action_operation_id
                    or not action_run_id
                    or action_thread_id
                    != workspace.assistant_selected_thread_id
                    or action_operation_id
                    != workspace.active_operation_id
                )
            ):
                return None
            if workspace.view == 'assistant':
                cancel_action = replace(
                    action,
                    workspace_action={
                        **dict(action.workspace_action or {}),
                        'kind': 'assistant.cancel',
                    },
                )
                return self._handle_assistant_action(
                    route=route,
                    action=cancel_action,
                    address_hash=address_hash,
                    workspace=workspace,
                    conversation_id=conversation_id or '',
                    message_key=message_key,
                    runtime_fence=lease.fence,
                )
            return None
        if action.action == 'local' and (
            not action_kind or workspace.view == 'assistant'
        ):
            return None
        source_revision = workspace.revision
        source_message_id = workspace.message_id
        source_operation_id = workspace.active_operation_id
        workspace.bind_message(action.message_id)
        if action_kind == 'history.switch':
            selection = self._store.get_selection_context(
                route.account_id,
                address_hash,
            )
            current_selection_id = (
                str(selection.get('id') or '')
                if isinstance(selection, dict)
                else ''
            )
            if (
                not current_selection_id
                or current_selection_id != action.selection_id
            ):
                workspace.view = 'conversations'
                expected_revision = workspace.revision
                workspace.advance()
                if not self._store.save_feishu_workspace_state_if_revision(
                    route.account_id,
                    address_hash,
                    workspace.to_dict(),
                    expected_revision,
                ):
                    return None
                claimed_operation_id = workspace.active_operation_id
                workspace = FeishuWorkspaceState.from_dict(
                    self._store.get_feishu_workspace_state(
                        route.account_id,
                        address_hash,
                    )
                )
                if workspace.active_operation_id != claimed_operation_id:
                    return None
                provider_context = self._workspace_provider_context(
                    account_id=route.account_id,
                    address_hash=address_hash,
                    workspace=workspace,
                    chat_id=action.chat_id,
                    conversation_id=conversation_id,
                    workspace_action=action.workspace_action,
                )
                provider_context['_history_switch_expired'] = True
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
        if action_kind == 'setting.update':
            workspace.active_operation_id = message_key
        self._apply_workspace_action(
            workspace=workspace,
            action=action.workspace_action,
        )
        if action_kind == 'history.switch':
            workspace.begin_operation(message_key)
            workspace.view = 'conversations'
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
        execution = replace(
            ChannelExecutionContext.from_provider_context(provider_context),
            ask_answers_structured=(
                dict(action.ask_answers_structured)
                if isinstance(action.ask_answers_structured, dict)
                else None
            ),
        )
        provider_context = {
            **provider_context,
            'workspace_operation_id': workspace.active_operation_id,
            'channel_execution': execution.to_dict(),
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
                'management'
                if (
                    isinstance(action.workspace_action, dict)
                    and action_kind not in _RESULT_WORKSPACE_ACTIONS
                )
                else 'reply'
            ),
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
        )
        draft_action = None
        if not conversation_id and action_kind == 'capability.toggle':
            draft_action = {
                'kind': 'resource_toggle',
                'resource': dict(action_data.get('resource') or {}),
            }
        elif not conversation_id and action_kind == 'new_session.workflow_mode':
            draft_action = {
                'kind': 'workflow_mode',
                'expected_mode': str(action_data.get('expected_mode') or ''),
                'mode': str(action_data.get('mode') or ''),
            }
        if not self._store.claim_feishu_workspace_and_ingest(
            route.account_id,
            address_hash,
            workspace.to_dict(),
            source_revision,
            source_message_id,
            source_operation_id,
            envelope,
            lease.fence,
            new_conversation_action=draft_action,
        ):
            return None
        self._log_action_ready(
            action,
            started_at=started_at,
            cached=False,
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
                getattr(self, '_direct_chats', {}).get(route.account_id, '')
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
        menu_card_key = (
            f'feishu-menu-card:{route.account_id}:'
            f'{menu.event_id}:{view}'
        )

        navigation_blocked = False
        card_pinned = False
        assistant_view: dict[str, Any] = {}
        detail_unavailable = False
        source_lineage_loaded = False
        source_message_id = ''
        source_operation_id = ''
        source_revision = 0
        sender = self._channels.create_sender(
            self._credentials.load_runtime_account(
                route.account_id
            )['credentials']
        )
        try:
            state = FeishuWorkspaceState(view=view)
            message_id = ''
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
                source_lineage_loaded = True
                source_message_id = state.message_id
                source_operation_id = state.active_operation_id
                source_revision = state.revision
                if state.view == 'assistant' and view == 'assistant':
                    if state.active_operation_id == message_key:
                        self._schedule_assistant_threads(
                            route=route,
                            address_hash=address_hash,
                            chat_id=chat_id,
                            revision=state.revision,
                        )
                    return
                if (
                    state.view == view
                    and state.active_operation_id == message_key
                ):
                    return
                if (
                    state.view == 'assistant'
                    and state.assistant_mode == 'detail'
                    and state.assistant_selected_thread_id
                ):
                    try:
                        assistant_view = self._read_assistant_detail(
                            owner_user_id=route.owner_user_id,
                            request_id=(
                                'feishu_assistant_menu_read_'
                                + hashlib.sha256(
                                    (menu.event_id or menu.event_key).encode(
                                        'utf-8'
                                    )
                                ).hexdigest()[:24]
                            ),
                            provider=_ASSISTANT_PROVIDER,
                            thread_id=state.assistant_selected_thread_id,
                        )
                    except Exception:
                        _logger.warning(
                            'feishu_assistant_menu_read_failed',
                            exc_info=True,
                        )
                        if view == 'assistant':
                            return
                        detail_unavailable = True
                if (
                    state.view == 'assistant'
                    and state.assistant_mode == 'detail'
                ):
                    card_pinned = detail_unavailable or detail_run_status(
                        assistant_view
                    ) in {
                        'running',
                        'waiting_for_input',
                        'releasing',
                        'release_failed',
                    }
                navigation_blocked = self._assistant_navigation_blocked(
                    state,
                    account_id=route.account_id,
                    address_hash=address_hash,
                    target_view=view,
                    assistant_view=assistant_view,
                    detail_unavailable=detail_unavailable,
                )
                if navigation_blocked:
                    if chat_id:
                        sender.send_markdown(
                            chat_id=chat_id,
                            text='Codex 正在处理任务，菜单切换未执行。',
                            idempotency_key=(
                                f'feishu-menu-blocked:{route.account_id}:'
                                f'{menu.event_id or menu.event_key}'
                            ),
                        )
                    return
                if state.view == 'assistant' and view != 'assistant':
                    try:
                        assistant_view = self._release_idle_assistant(
                            state,
                            account_id=route.account_id,
                            address_hash=address_hash,
                            owner_user_id=route.owner_user_id,
                            request_id=(
                                'feishu_assistant_menu_release_'
                                + hashlib.sha256(
                                    menu.event_id.encode('utf-8')
                                ).hexdigest()[:24]
                            ),
                        )
                    except Exception as exc:
                        _logger.warning(
                            'feishu_assistant_menu_release_failed',
                            exc_info=True,
                        )
                        navigation_blocked = True
                        view = 'assistant'
                        card_pinned = True
                        assistant_view = assistant_view_with_ui(
                            assistant_view,
                            'error',
                            str(exc),
                        )
                message_id = state.message_id
            entering_assistant = (
                view == 'assistant'
                and (
                    not source_lineage_loaded
                    or state.view != 'assistant'
                )
            )
            state.navigate(view)
            if not navigation_blocked:
                state.active_operation_id = message_key
            if entering_assistant and not navigation_blocked:
                state.assistant_mode = 'projects'
                state.assistant_project_cwd = ''
                state.assistant_project_page = 0
                state.assistant_projects_cursor = ''
                state.assistant_projects_next_cursor = ''
                state.assistant_projects_previous_cursors = []
                state.assistant_threads_cursor = ''
                state.assistant_threads_next_cursor = ''
                state.assistant_threads_previous_cursors = []
                state.assistant_threads_page = 0
            state.advance()
            prepared_state = state.to_dict()
            card = FeishuWorkspaceRenderer.render(
                provider_context={
                    'chat_id': chat_id,
                    'workspace_state': prepared_state,
                    'assistant_view': assistant_view,
                },
                presentations=[],
            )
            if message_id and card_pinned:
                try:
                    sender.update_card(message_id=message_id, card=card)
                except Exception as exc:
                    if not workspace_card_expired(exc):
                        raise
                    message_id = self._send_card_to_bottom(
                        sender=sender,
                        chat_id=chat_id,
                        card=card,
                        idempotency_key=menu_card_key,
                    )
            elif message_id:
                message_id = self._send_card_to_bottom(
                    sender=sender,
                    chat_id=chat_id,
                    card=card,
                    idempotency_key=menu_card_key,
                )
            else:
                message_id, resolved_chat_id = (
                    sender.send_card_to_user_with_chat(
                        open_id=menu.sender_id,
                        card=card,
                        idempotency_key=menu_card_key,
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
        current_state = FeishuWorkspaceState.from_dict(
            self._store.get_feishu_workspace_state(
                route.account_id,
                address_hash,
            )
        )
        lineage_changed = source_lineage_loaded and (
            current_state.message_id != source_message_id
            or current_state.active_operation_id != source_operation_id
            or current_state.revision != source_revision
        )
        if lineage_changed:
            self._expire_workspace_card(
                account_id=route.account_id,
                address_hash=address_hash,
                message_id=message_id,
                current_message_id=current_state.message_id,
                language=current_state.output_language,
            )
            if (
                message_id == current_state.message_id
                and current_state.view == 'assistant'
            ):
                try:
                    request_id = (
                        'feishu_assistant_menu_stale_reconcile_'
                        + hashlib.sha256(
                            menu.event_id.encode('utf-8')
                        ).hexdigest()[:24]
                    )
                    if (
                        current_state.assistant_mode == 'detail'
                        and current_state.assistant_selected_thread_id
                    ):
                        current_assistant_view = self._read_assistant_detail(
                            owner_user_id=route.owner_user_id,
                            request_id=request_id,
                            provider=_ASSISTANT_PROVIDER,
                            thread_id=(
                                current_state.assistant_selected_thread_id
                            ),
                        )
                    elif current_state.assistant_mode == 'sessions':
                        current_assistant_view = self._load_assistant_threads(
                            current_state,
                            route.owner_user_id,
                            request_id,
                            current_state.assistant_threads_cursor,
                        )
                    else:
                        current_assistant_view = self._load_assistant_projects(
                            current_state,
                            route.owner_user_id,
                            request_id,
                        )
                except Exception:
                    return
                self._schedule_action_card_refresh(
                    route.account_id,
                    current_state.message_id,
                    FeishuWorkspaceRenderer.render(
                        provider_context={
                            'chat_id': chat_id,
                            'workspace_state': current_state.to_dict(),
                            'assistant_view': current_assistant_view,
                        },
                        presentations=[],
                    ),
                    address_hash=address_hash,
                    expected_revision=current_state.revision,
                    expected_operation_id=(
                        current_state.active_operation_id
                    ),
                )
            return
        state_revision = current_state.revision
        assistant_state_write = (
            current_state.view == 'assistant' or view == 'assistant'
        )
        state = FeishuWorkspaceState.from_dict(prepared_state)
        state.message_id = message_id
        command = menu_command(view)
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
                'assistant_view': assistant_view,
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
                }[view],
                provider_context=provider_context,
            )
            if self._store.claim_feishu_workspace_and_ingest(
                route.account_id,
                address_hash,
                state.to_dict(),
                state_revision,
                source_message_id,
                source_operation_id,
                envelope,
                lease.fence,
            ):
                return
            current = FeishuWorkspaceState.from_dict(
                self._store.get_feishu_workspace_state(
                    route.account_id,
                    address_hash,
                )
            )
            if current.message_id != message_id:
                self._expire_workspace_card(
                    account_id=route.account_id,
                    address_hash=address_hash,
                    message_id=message_id,
                    current_message_id=current.message_id,
                    language=current.output_language,
                )
            return
        saved = self._store.save_feishu_workspace_state_if_revision(
            route.account_id,
            address_hash,
            state.to_dict(),
            state_revision,
        )
        if not saved:
            state = FeishuWorkspaceState.from_dict(
                self._store.get_feishu_workspace_state(
                    route.account_id,
                    address_hash,
                )
            )
            if not assistant_state_write:
                self._expire_workspace_card(
                    account_id=route.account_id,
                    address_hash=address_hash,
                    message_id=message_id,
                    current_message_id=state.message_id,
                    language=state.output_language,
                )
                return
            if assistant_state_write:
                if source_lineage_loaded and (
                    state.message_id != source_message_id
                    or state.active_operation_id != source_operation_id
                ):
                    self._expire_workspace_card(
                        account_id=route.account_id,
                        address_hash=address_hash,
                        message_id=message_id,
                        current_message_id=state.message_id,
                        language=state.output_language,
                    )
                    return
                state.message_id = message_id
                state = FeishuWorkspaceState.from_dict(
                    self._store.save_feishu_workspace_message(
                        route.account_id,
                        address_hash,
                        message_id,
                        state.active_operation_id,
                        source_message_id,
                    )
                )
                if state.message_id != message_id:
                    return
                view = state.view
                entering_assistant = False
                if (
                    state.view == 'assistant'
                    and state.assistant_mode == 'detail'
                    and state.assistant_selected_thread_id
                ):
                    thread = assistant_view.get('thread')
                    thread = dict(thread) if isinstance(thread, dict) else {}
                    if str(thread.get('id') or '') != (
                        state.assistant_selected_thread_id
                    ):
                        try:
                            assistant_view = self._read_assistant_detail(
                                owner_user_id=route.owner_user_id,
                                request_id='feishu_assistant_menu_reconcile',
                                provider=_ASSISTANT_PROVIDER,
                                thread_id=state.assistant_selected_thread_id,
                            )
                        except Exception:
                            return
                elif state.view == 'assistant':
                    try:
                        assistant_view = (
                            self._load_assistant_projects(
                                state,
                                route.owner_user_id,
                                'feishu_assistant_menu_reconcile',
                            )
                            if state.assistant_mode == 'projects'
                            else self._load_assistant_threads(
                                state,
                                route.owner_user_id,
                                'feishu_assistant_menu_reconcile',
                                state.assistant_threads_cursor,
                            )
                        )
                    except Exception:
                        return
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
            'assistant_view': assistant_view,
        }
        self._schedule_action_card_refresh(
            route.account_id,
            message_id,
            FeishuWorkspaceRenderer.render(
                provider_context=provider_context,
                presentations=[],
            ),
            address_hash=address_hash,
            expected_revision=state.revision,
            expected_operation_id=state.active_operation_id,
        )
        if entering_assistant and not navigation_blocked:
            self._schedule_assistant_threads(
                route=route,
                address_hash=address_hash,
                chat_id=chat_id,
                revision=state.revision,
            )

    def _assistant_navigation_blocked(
        self,
        state: FeishuWorkspaceState,
        *,
        account_id: str,
        address_hash: str,
        target_view: str,
        assistant_view: dict[str, Any],
        detail_unavailable: bool,
    ) -> bool:
        run_status = (
            detail_run_status(assistant_view)
            if assistant_view.get('kind') == 'detail'
            else 'idle'
        )
        blocked = (
            state.view == 'assistant'
            and target_view != 'assistant'
            and (
                detail_unavailable
                or self._store.has_active_inbound(
                    account_id,
                    address_hash,
                )
                or run_status in {
                    'running',
                    'waiting_for_input',
                    'releasing',
                    'release_failed',
                }
            )
        )
        return blocked

    def _release_idle_assistant(
        self,
        state: FeishuWorkspaceState,
        *,
        account_id: str,
        address_hash: str,
        owner_user_id: str,
        request_id: str,
    ) -> dict[str, Any]:
        if self._store.has_active_inbound(account_id, address_hash):
            raise FeishuRuntimeError(
                'Codex conversation is still being submitted'
            )
        if not state.assistant_selected_thread_id:
            return {}
        view = self._read_assistant_detail(
            owner_user_id=owner_user_id,
            request_id=f'{request_id}_read',
            provider=_ASSISTANT_PROVIDER,
            thread_id=state.assistant_selected_thread_id,
        )
        if detail_run_status(view) in {
            'running',
            'waiting_for_input',
            'releasing',
            'release_failed',
        }:
            raise FeishuRuntimeError(
                'Codex conversation cannot be released yet'
            )
        conversation_id = detail_conversation_id(view)
        if conversation_id:
            self._core.release_external_conversation(
                owner_user_id=owner_user_id,
                request_id=request_id,
                conversation_id=conversation_id,
            )
        return view

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
        if not message_id or message_id == current_message_id:
            return
        card = FeishuReplyRenderer.render(
            provider_context={
                'workspace_state': {
                    'output_language': language,
                    'show_process': False,
                },
            },
            text=(
                'This message was not submitted. Use the latest Codex card.'
                if language == 'en'
                else '本条消息未提交，请使用最新的 Codex 卡片。'
            ),
            status=(
                '⚠️ **Message expired**'
                if language == 'en'
                else '⚠️ **消息已过期**'
            ),
            thinking='',
        )
        timer = threading.Timer(
            _ACTION_REFRESH_DELAY_SECONDS,
            self._refresh_expired_workspace_card,
            args=(account_id, address_hash, message_id, card),
        )
        timer.daemon = True
        timer.name = 'feishu-expired-card-refresh'
        timer.start()

    def _refresh_expired_workspace_card(
        self,
        account_id: str,
        address_hash: str,
        message_id: str,
        card: dict[str, Any],
    ) -> None:
        current = FeishuWorkspaceState.from_dict(
            self._store.get_feishu_workspace_state(
                account_id,
                address_hash,
            )
        )
        if current.message_id == message_id:
            return
        self._refresh_action_card(account_id, message_id, card)

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
                                advance_revision=False,
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
            workspace.view = 'conversations'
        elif kind == 'history.open':
            workspace.view = 'conversations'
        elif kind == 'capability.toggle':
            workspace.view = 'capabilities'
        elif kind == 'new_session.open':
            workspace.open_new_session()
        elif kind == 'new_session.cancel':
            workspace.new_session_open = False
            workspace.view = 'conversations'
        elif kind == 'new_session.create':
            workspace.prepare_new_session()
        elif kind == 'setting.update':
            target_view = str(action.get('view') or 'capabilities')
            if target_view in {'capabilities', 'settings'}:
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
            elif name == 'show_process' and isinstance(value, bool):
                workspace.show_process = value
            elif (
                name == 'auto_collapse_process'
                and isinstance(value, bool)
            ):
                workspace.auto_collapse_process = value
            elif name == 'show_sources' and isinstance(value, bool):
                workspace.show_sources = value
            workspace.view = 'settings'
        elif kind == 'maintenance.reset_preferences':
            workspace.reset_preferences()
            workspace.view = 'settings'
        elif kind == 'maintenance.clear_conversation':
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
        draft: dict[str, Any] = {}
        if not conversation_id:
            navigation = self._store.get_navigation_state(
                account_id,
                address_hash,
            ) or {}
            draft = self._store.get_new_conversation_draft(
                account_id,
                address_hash,
            )
        pending_workflow_mode = str(
            draft.get('workflow_mode') or 'dynamic'
        )
        if pending_workflow_mode not in {'auto', 'dynamic'}:
            pending_workflow_mode = 'dynamic'
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
            include_capability_settings=bool(
                isinstance(workspace_action, dict)
                and workspace_action.get('kind') == 'navigate'
                and workspace_action.get('view') == 'capabilities'
            ),
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
            'pending_capability_resources': [
                item.to_dict()
                for item in new_conversation_resources(draft)
            ],
            'pending_workflow_mode': pending_workflow_mode,
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
            'resource_changes': [],
        },
    }

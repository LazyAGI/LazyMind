from __future__ import annotations

import base64
import hashlib
import json
import logging
import threading
import time
from dataclasses import dataclass, field
from typing import Any

from channel_gateway.common.domain.channel import InboundEnvelope
from channel_gateway.common.ports.core import LazyMindCore
from channel_gateway.common.ports.providers import (
    RuntimeCredentialStore,
    RuntimeLease,
)
from channel_gateway.feishu.assistant import (
    codex_turns_for_card,
    is_user_facing_codex_thread,
)
from channel_gateway.feishu.domain import (
    FeishuAddressFactory,
    FeishuAppCredentials,
    FeishuInboundAction,
    FeishuInboundMenu,
    FeishuInboundMessage,
    FeishuRuntimeError,
)
from channel_gateway.feishu.ports import (
    FeishuReceiverClient,
    FeishuReceiverFactory,
    FeishuRuntimeRepository,
)
from channel_gateway.feishu.presentation import (
    FeishuReplyRenderer,
    workspace_ask_elements,
)
from channel_gateway.feishu.registration import configure_bot_menu
from channel_gateway.feishu.workspace import (
    FeishuWorkspaceRenderer,
    FeishuWorkspaceState,
    MENU_EVENT_VIEWS,
    menu_command,
)
_logger = logging.getLogger(__name__)


_MAX_INBOUND_IMAGE_BYTES = 10 * 1024 * 1024
_ACTION_REFRESH_DELAY_SECONDS = 0.35
_ASSISTANT_TURN_PAGE_SIZE = 1
_ASSISTANT_SESSION_PAGE_SIZE = 5
_MAX_ASSISTANT_THREADS = 500
_BOT_MENU_CONFIG_STATE_KEY = 'feishu-bot-menu-config'
_BOT_MENU_CONFIG_VERSION = 2
_LOCAL_WORKSPACE_ACTIONS = {
    'navigate',
    'capability.toggle',
    'capability.save',
    'context.open',
    'context.category',
    'context.page',
    'context.toggle',
    'context.save',
    'preference',
    'new_session.open',
    'new_session.mode',
    'new_session.cancel',
    'maintenance.clear_turn',
    'maintenance.reset_preferences',
    'assistant.refresh',
    'assistant.project',
    'assistant.projects',
    'assistant.sessions',
    'assistant.sessions_page',
    'assistant.open',
    'assistant.back',
    'assistant.turns_page',
    'assistant.answer_page',
    'assistant.respond',
    'assistant.retry',
}
_RESULT_WORKSPACE_ACTIONS = {
    'maintenance.clear_conversation',
    'new_session.create',
    'prompt.run',
}
_REMOTE_ASSISTANT_ACTIONS = {
    'assistant.refresh',
    'assistant.retry',
    'assistant.open',
    'assistant.back',
    'assistant.turns_page',
    'assistant.respond',
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
        menu_state = self._store.get_feishu_workspace_state(
            account_id,
            _BOT_MENU_CONFIG_STATE_KEY,
        )
        if not isinstance(menu_state, dict):
            menu_state = {}
        if int(menu_state.get('version') or 0) < _BOT_MENU_CONFIG_VERSION:
            try:
                configure_bot_menu(
                    credentials.app_id,
                    credentials.app_secret,
                )
                self._store.save_feishu_workspace_state(
                    account_id,
                    _BOT_MENU_CONFIG_STATE_KEY,
                    {'version': _BOT_MENU_CONFIG_VERSION},
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
        message_key = hashlib.sha256(
            message.message_id.encode('utf-8')
        ).hexdigest()
        assistant_detail = (
            workspace.view == 'assistant'
            and workspace.assistant_mode == 'detail'
        )
        if (
            assistant_detail
            and (
                not workspace.assistant_conversation_id
                or not workspace.assistant_selected_thread_id
            )
        ):
            try:
                self._materialize_assistant_draft(
                    workspace=workspace,
                    owner_user_id=route.owner_user_id,
                    request_id=f'feishu_assistant_new_{message_key[:24]}',
                )
            except Exception as exc:
                workspace.assistant_status = 'error'
                workspace.assistant_error = str(exc)[:500]
                workspace.chat_status = '⚠️ **创建 ChatGPT 对话失败，请重试**'
                workspace.advance()
                self._store.save_feishu_workspace_state(
                    route.account_id, address_hash, workspace.to_dict()
                )
                if workspace.message_id:
                    self._schedule_action_card_refresh(
                        route.account_id,
                        workspace.message_id,
                        FeishuWorkspaceRenderer.render(
                            provider_context={
                                'chat_id': message.chat_id,
                                'workspace_state': workspace.to_dict(),
                            },
                            text='',
                            presentations=[],
                        ),
                    )
                return
        assistant_active = (
            assistant_detail
            and bool(workspace.assistant_conversation_id)
            and bool(workspace.assistant_selected_thread_id)
        )
        if assistant_active and workspace.assistant_thread_readonly:
            workspace.chat_status = 'ℹ️ **此 Codex 会话当前为只读状态**'
            workspace.chat_thinking = '请等待其他端的运行结束，或切换到可继续的会话。'
            workspace.advance()
            self._store.save_feishu_workspace_state(
                route.account_id, address_hash, workspace.to_dict()
            )
            if workspace.message_id:
                self._schedule_action_card_refresh(
                    route.account_id,
                    workspace.message_id,
                    FeishuWorkspaceRenderer.render(
                        provider_context={
                            'chat_id': message.chat_id,
                            'workspace_state': workspace.to_dict(),
                        },
                        text='',
                        presentations=[],
                    ),
                )
            return
        pending_turn = self._store.get_pending_turn(
            route.account_id,
            address_hash,
        )
        conversation_id = self._store.get_route(
            route.account_id,
            address_hash,
        )
        is_new_operation = workspace.active_operation_id != message_key
        workspace.begin_operation(message_key, effective_text)
        if is_new_operation:
            workspace.message_id = self._move_workspace_card_to_bottom(
                route=route,
                chat_id=message.chat_id,
                inbound_message_id=message.message_id,
                workspace=workspace,
                assistant_active=assistant_active,
            )
        workspace.advance()
        self._store.save_feishu_workspace_state(
            route.account_id,
            address_hash,
            workspace.to_dict(),
        )
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
                    text=effective_text,
                    provider_context={
                        **self._workspace_provider_context(
                            workspace=workspace,
                            chat_id=message.chat_id,
                            conversation_id=conversation_id,
                            pending_turn=pending_turn,
                        ),
                        'workspace_surface': (
                            'assistant' if assistant_active else 'reply'
                        ),
                        'workspace_message_id': _workspace_stream_message_id(
                            workspace
                        ),
                        'chat_inputs': chat_inputs,
                        'external_agent_binding': (
                            {
                                'conversation_id': workspace.assistant_conversation_id,
                                'provider_thread_id': workspace.assistant_selected_thread_id,
                                'provider': workspace.assistant_provider,
                            }
                            if assistant_active else None
                        ),
                        'command_action': _chat_command_action(
                            effective_text,
                            required=bool(assistant_active or chat_inputs),
                        ),
                    },
                )
            ],
            None,
            lease.fence,
        )

    def _move_workspace_card_to_bottom(
        self,
        *,
        route: _AccountRoute,
        chat_id: str,
        inbound_message_id: str,
        workspace: FeishuWorkspaceState,
        assistant_active: bool,
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
            }
            card = (
                FeishuWorkspaceRenderer.render(
                    provider_context=provider_context,
                    text='',
                    presentations=[],
                    streaming=True,
                )
                if assistant_active
                else FeishuReplyRenderer.render(
                    provider_context=provider_context,
                    text='',
                    presentations=[],
                    status='⏳ **正在理解你的问题**',
                    thinking='正在分析问题…',
                    streaming=True,
                )
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

    def _handle_assistant_action(
        self,
        *,
        route: _AccountRoute,
        action: FeishuInboundAction,
        address_hash: str,
        workspace: FeishuWorkspaceState,
    ) -> dict[str, Any]:
        values = action.workspace_action or {}
        kind = str(values.get('kind') or '')
        request_id = 'feishu_assistant_' + hashlib.sha256(
            f'{action.message_id}:{kind}:{time.time_ns()}'.encode('utf-8')
        ).hexdigest()[:24]
        if kind in _REMOTE_ASSISTANT_ACTIONS:
            self._prepare_remote_assistant_action(workspace, values)
            workspace.assistant_status = 'loading'
            workspace.assistant_error = ''
            workspace.bind_message(action.message_id)
            workspace.advance()
            revision = workspace.revision
            self._store.save_feishu_workspace_state(
                route.account_id, address_hash, workspace.to_dict()
            )
            self._schedule_assistant_action(
                route=route,
                action=action,
                address_hash=address_hash,
                revision=revision,
                request_id=request_id,
            )
            card = FeishuWorkspaceRenderer.render(
                provider_context={
                    'chat_id': action.chat_id,
                    'workspace_state': workspace.to_dict(),
                },
                text='',
                presentations=[],
            )
            card['config'].pop('streaming_config', None)
            return card
        try:
            self._apply_local_assistant_action(workspace, values)
        except Exception as exc:
            _logger.warning(
                'feishu_assistant_action_failed kind=%s',
                kind,
                exc_info=True,
            )
            workspace.assistant_status = 'error'
            workspace.assistant_error = str(exc)[:500]
        workspace.bind_message(action.message_id)
        workspace.advance()
        self._store.save_feishu_workspace_state(
            route.account_id, address_hash, workspace.to_dict()
        )
        card = FeishuWorkspaceRenderer.render(
            provider_context={
                'chat_id': action.chat_id,
                'workspace_state': workspace.to_dict(),
            },
            text='',
            presentations=[],
        )
        card['config'].pop('streaming_config', None)
        return card

    @staticmethod
    def _prepare_remote_assistant_action(
        workspace: FeishuWorkspaceState,
        values: dict[str, Any],
    ) -> None:
        kind = str(values.get('kind') or '')
        if kind == 'assistant.open':
            thread_id = str(values.get('thread_id') or '')
            selected = next(
                (
                    item
                    for item in workspace.assistant_threads
                    if str(item.get('id') or '') == thread_id
                ),
                {},
            )
            workspace.assistant_mode = 'detail'
            workspace.assistant_selected_thread_id = thread_id
            workspace.assistant_conversation_id = ''
            workspace.assistant_thread_title = str(
                selected.get('title')
                or selected.get('name')
                or 'Codex 会话'
            )[:200]
            workspace.assistant_thread_cwd = str(
                selected.get('cwd') or workspace.assistant_project_cwd
            )[:500]
            workspace.assistant_thread_available = bool(
                selected.get('available', False)
            )
            workspace.assistant_managed = bool(
                selected.get('managed')
                or selected.get('managed_by_lazymind')
            )
            workspace.assistant_turns = []
            workspace.assistant_answer_page = 0
            workspace.user_text = ''
            workspace.chat_text = ''
            workspace.chat_status = ''
            workspace.chat_thinking = ''
        elif kind == 'assistant.turns_page':
            workspace.assistant_answer_page = 0

    @staticmethod
    def _apply_local_assistant_action(
        workspace: FeishuWorkspaceState,
        values: dict[str, Any],
    ) -> None:
        kind = str(values.get('kind') or '')
        if kind == 'assistant.project':
            workspace.assistant_project_cwd = str(
                values.get('project_cwd') or ''
            )[:500]
            workspace.assistant_project_page = 0
            workspace.assistant_mode = 'sessions'
        elif kind == 'assistant.projects':
            workspace.assistant_mode = 'projects'
            workspace.assistant_project_page = 0
        elif kind == 'assistant.sessions':
            workspace.assistant_mode = 'sessions'
        elif kind == 'assistant.new':
            workspace.assistant_mode = 'detail'
            workspace.assistant_selected_thread_id = ''
            workspace.assistant_conversation_id = ''
            workspace.assistant_thread_title = str(
                values.get('display_name') or '新 Codex 会话'
            )[:200]
            workspace.assistant_thread_cwd = str(
                values.get('cwd')
                or workspace.assistant_project_cwd
            )[:500]
            workspace.assistant_thread_available = True
            workspace.assistant_managed = True
            workspace.assistant_turns = []
            workspace.assistant_answer_page = 0
            workspace.user_text = ''
            workspace.chat_text = ''
            workspace.chat_status = ''
            workspace.chat_thinking = ''
        elif kind == 'assistant.sessions_page':
            count = sum(
                1
                for item in workspace.assistant_threads
                if str(item.get('cwd') or '')
                == workspace.assistant_project_cwd
            )
            max_page = max(
                0,
                (count + _ASSISTANT_SESSION_PAGE_SIZE - 1)
                // _ASSISTANT_SESSION_PAGE_SIZE
                - 1,
            )
            selection = values.get('selection')
            direction = str(values.get('direction') or '')
            if selection is not None and str(selection).strip():
                workspace.assistant_project_page = min(
                    max_page,
                    max(0, int(str(selection))),
                )
            elif direction == 'previous':
                workspace.assistant_project_page = max(
                    0,
                    workspace.assistant_project_page - 1,
                )
            elif direction == 'next':
                workspace.assistant_project_page = min(
                    max_page,
                    workspace.assistant_project_page + 1,
                )
        elif kind == 'assistant.answer_page':
            direction = str(values.get('direction') or '')
            if direction == 'previous':
                workspace.assistant_answer_page = max(
                    0,
                    workspace.assistant_answer_page - 1,
                )
            elif direction == 'next':
                workspace.assistant_answer_page += 1
        else:
            raise FeishuRuntimeError('Unsupported assistant action')
        workspace.assistant_status = 'ready'
        workspace.assistant_error = ''

    def _schedule_assistant_action(
        self,
        *,
        route: _AccountRoute,
        action: FeishuInboundAction,
        address_hash: str,
        revision: int,
        request_id: str,
    ) -> None:
        thread = threading.Thread(
            target=self._run_assistant_action,
            kwargs={
                'route': route,
                'action': action,
                'address_hash': address_hash,
                'revision': revision,
                'request_id': request_id,
            },
            name='feishu-assistant-action',
            daemon=True,
        )
        thread.start()

    def _run_assistant_action(
        self,
        *,
        route: _AccountRoute,
        action: FeishuInboundAction,
        address_hash: str,
        revision: int,
        request_id: str,
    ) -> None:
        workspace = FeishuWorkspaceState.from_dict(
            self._store.get_feishu_workspace_state(
                route.account_id,
                address_hash,
            )
        )
        if workspace.revision != revision or workspace.view != 'assistant':
            return
        values = action.workspace_action or {}
        kind = str(values.get('kind') or '')
        try:
            self._execute_remote_assistant_action(
                workspace=workspace,
                owner_user_id=route.owner_user_id,
                request_id=request_id,
                values=values,
            )
        except Exception as exc:
            _logger.warning(
                'feishu_assistant_action_failed kind=%s',
                kind,
                exc_info=True,
            )
            workspace.assistant_status = 'error'
            workspace.assistant_error = str(exc)[:500]
        current = FeishuWorkspaceState.from_dict(
            self._store.get_feishu_workspace_state(
                route.account_id,
                address_hash,
            )
        )
        if current.revision != revision or current.view != 'assistant':
            return
        workspace.bind_message(action.message_id)
        workspace.advance()
        self._store.save_feishu_workspace_state(
            route.account_id,
            address_hash,
            workspace.to_dict(),
        )
        card = FeishuWorkspaceRenderer.render(
            provider_context={
                'chat_id': action.chat_id,
                'workspace_state': workspace.to_dict(),
            },
            text='',
            presentations=[],
        )
        card['config'].pop('streaming_config', None)
        self._schedule_action_card_refresh(
            route.account_id,
            action.message_id,
            card,
        )

    def _materialize_assistant_draft(
        self,
        *,
        workspace: FeishuWorkspaceState,
        owner_user_id: str,
        request_id: str,
    ) -> None:
        if (
            workspace.assistant_conversation_id
            and workspace.assistant_selected_thread_id
        ):
            return
        cwd = workspace.assistant_thread_cwd or workspace.assistant_project_cwd
        binding = self._core.bind_external_thread(
            owner_user_id=owner_user_id,
            request_id=request_id,
            provider=workspace.assistant_provider,
            new_session=True,
            cwd=cwd,
            display_name=workspace.assistant_thread_title,
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
            raise FeishuRuntimeError('Codex did not return a new thread')
        native_thread.setdefault('id', thread_id)
        native_thread.setdefault('cwd', cwd)
        native_thread.setdefault('available', True)
        native_thread.setdefault('status', {'type': 'idle'})
        self._apply_assistant_thread(
            workspace,
            binding,
            {
                'thread': native_thread,
                'turns': [],
                'offset': 0,
                'total_turns': 0,
            },
            thread_id,
        )

    def _execute_remote_assistant_action(
        self,
        *,
        workspace: FeishuWorkspaceState,
        owner_user_id: str,
        request_id: str,
        values: dict[str, Any],
    ) -> None:
        kind = str(values.get('kind') or '')
        if kind in {'assistant.refresh', 'assistant.retry'}:
            self._load_assistant_threads_into(
                workspace,
                owner_user_id,
                request_id,
                '',
            )
            return
        if kind == 'assistant.back':
            if workspace.assistant_conversation_id:
                self._core.release_external_conversation(
                    owner_user_id=owner_user_id,
                    request_id=request_id,
                    conversation_id=workspace.assistant_conversation_id,
                )
            workspace.leave_assistant_thread()
            self._load_assistant_threads_into(
                workspace,
                owner_user_id,
                request_id,
                '',
            )
            return
        if kind == 'assistant.open':
            thread_id = str(values.get('thread_id') or '')
            binding = self._core.bind_external_thread(
                owner_user_id=owner_user_id,
                request_id=request_id,
                provider=workspace.assistant_provider,
                provider_thread_id=thread_id,
            )
            try:
                thread = self._read_latest_assistant_thread(
                    owner_user_id=owner_user_id,
                    request_id=request_id,
                    provider=workspace.assistant_provider,
                    thread_id=thread_id,
                )
            except Exception as exc:
                if 'not materialized yet' not in str(exc):
                    raise
                thread = {
                    'thread': dict(binding.get('thread') or {}),
                    'turns': [],
                    'offset': 0,
                    'total_turns': 0,
                    'has_more': False,
                }
            self._apply_assistant_thread(
                workspace,
                binding,
                thread,
                thread_id,
            )
            return
        if kind == 'assistant.turns_page':
            direction = str(values.get('direction') or '')
            offset = workspace.assistant_turns_offset
            selection = values.get('selection')
            if selection is not None and str(selection).strip():
                offset = min(
                    max(0, workspace.assistant_turns_total - 1),
                    max(0, int(str(selection))),
                )
            elif direction == 'older':
                offset = max(0, offset - _ASSISTANT_TURN_PAGE_SIZE)
            elif direction == 'newer':
                offset = min(
                    max(
                        0,
                        workspace.assistant_turns_total
                        - _ASSISTANT_TURN_PAGE_SIZE,
                    ),
                    offset + _ASSISTANT_TURN_PAGE_SIZE,
                )
            thread = self._core.read_external_thread(
                owner_user_id=owner_user_id,
                request_id=request_id,
                provider=workspace.assistant_provider,
                thread_id=workspace.assistant_selected_thread_id,
                offset=offset,
                limit=_ASSISTANT_TURN_PAGE_SIZE,
            )
            self._apply_assistant_thread(
                workspace,
                {
                    'conversation_id': workspace.assistant_conversation_id,
                    'provider_thread_id': (
                        workspace.assistant_selected_thread_id
                    ),
                    'managed_by_lazymind': workspace.assistant_managed,
                },
                thread,
                workspace.assistant_selected_thread_id,
            )
            return
        if kind == 'assistant.respond':
            pending = workspace.assistant_pending_request or {}
            external_request_id = str(
                values.get('request_id')
                or pending.get('request_id')
                or ''
            )
            self._core.respond_external_request(
                owner_user_id=owner_user_id,
                request_id=request_id,
                external_request_id=external_request_id,
                response=_assistant_request_payload(values, pending),
            )
            snapshot = self._core.snapshot_external_conversation(
                owner_user_id=owner_user_id,
                request_id=request_id,
                conversation_id=workspace.assistant_conversation_id,
            )
            self._apply_assistant_snapshot(workspace, snapshot)
            return
        raise FeishuRuntimeError('Unsupported assistant action')

    def _read_latest_assistant_thread(
        self,
        *,
        owner_user_id: str,
        request_id: str,
        provider: str,
        thread_id: str,
    ) -> dict[str, Any]:
        first = self._core.read_external_thread(
            owner_user_id=owner_user_id,
            request_id=request_id,
            provider=provider,
            thread_id=thread_id,
            offset=0,
            limit=_ASSISTANT_TURN_PAGE_SIZE,
        )
        total = int(first.get('total_turns') or 0)
        latest_offset = max(0, total - _ASSISTANT_TURN_PAGE_SIZE)
        if latest_offset == 0:
            return first
        return self._core.read_external_thread(
            owner_user_id=owner_user_id,
            request_id=request_id,
            provider=provider,
            thread_id=thread_id,
            offset=latest_offset,
            limit=_ASSISTANT_TURN_PAGE_SIZE,
        )

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
        try:
            self._load_assistant_threads_into(
                workspace,
                route.owner_user_id,
                f'feishu_assistant_list_{revision}',
                '',
            )
        except Exception as exc:
            workspace.assistant_status = 'error'
            workspace.assistant_error = str(exc)[:500]
        current = FeishuWorkspaceState.from_dict(
            self._store.get_feishu_workspace_state(route.account_id, address_hash)
        )
        if current.revision != revision or current.view != 'assistant':
            return
        workspace.advance()
        self._store.save_feishu_workspace_state(
            route.account_id, address_hash, workspace.to_dict()
        )
        if not workspace.message_id:
            return
        self._schedule_action_card_refresh(
            route.account_id,
            workspace.message_id,
            FeishuWorkspaceRenderer.render(
                provider_context={
                    'chat_id': chat_id,
                    'workspace_state': workspace.to_dict(),
                },
                text='',
                presentations=[],
            ),
        )

    def _load_assistant_threads_into(
        self,
        workspace: FeishuWorkspaceState,
        owner_user_id: str,
        request_id: str,
        cursor: str,
    ) -> None:
        workspace.assistant_status = 'loading'
        threads: list[dict[str, Any]] = []
        next_cursor = str(cursor or '')
        while len(threads) < _MAX_ASSISTANT_THREADS:
            response = self._core.list_external_threads(
                owner_user_id=owner_user_id,
                request_id=request_id,
                provider=workspace.assistant_provider,
                cursor=next_cursor,
                limit=100,
            )
            page = (
                response.get('data')
                or response.get('threads')
                or response.get('items')
                or []
            )
            threads.extend(
                dict(item) for item in page if isinstance(item, dict)
            )
            next_value = (
                response.get('nextCursor')
                or response.get('next_cursor')
                or response.get('cursor')
                or ''
            )
            if isinstance(next_value, dict):
                next_value = next_value.get('value') or ''
            next_cursor = str(next_value or '')
            if not next_cursor or not page:
                break
        visible_threads = [
            dict(item)
            for item in threads[:_MAX_ASSISTANT_THREADS]
            if isinstance(item, dict)
            and is_user_facing_codex_thread(item)
        ]
        workspace.assistant_threads = visible_threads
        workspace.assistant_status = 'ready'
        workspace.assistant_error = ''
        workspace.view_snapshots['assistant'] = {
            'text': '',
            'presentations': [],
            'threads': list(workspace.assistant_threads),
        }

    @staticmethod
    def _apply_assistant_thread(
        workspace: FeishuWorkspaceState,
        binding: dict[str, Any],
        thread: dict[str, Any],
        thread_id: str,
    ) -> None:
        binding_data = dict(binding.get('binding') or binding)
        data = dict(thread.get('thread') or thread)
        turns = thread.get('turns')
        if turns is None:
            turns = data.get('turns')
        workspace.assistant_mode = 'detail'
        workspace.assistant_status = 'ready'
        workspace.assistant_error = ''
        workspace.assistant_selected_thread_id = str(
            thread_id
            or binding_data.get('provider_thread_id')
            or data.get('id')
            or ''
        )
        workspace.assistant_conversation_id = str(
            binding_data.get('conversation_id')
            or binding.get('conversation_id')
            or ''
        )
        name = data.get('name')
        if isinstance(name, dict):
            name = name.get('value')
        workspace.assistant_thread_title = str(
            name
            or data.get('title')
            or data.get('preview')
            or binding.get('display_name')
            or 'Codex 会话'
        )[:200]
        workspace.assistant_thread_source = str(data.get('source') or 'Codex')[:100]
        workspace.assistant_thread_cwd = str(data.get('cwd') or '')[:500]
        workspace.assistant_project_cwd = workspace.assistant_thread_cwd
        workspace.assistant_thread_updated_at = str(
            data.get('updatedAt') or data.get('updated_at') or ''
        )[:100]
        workspace.assistant_thread_available = bool(data.get('available', False))
        workspace.assistant_managed = bool(
            binding_data.get('managed_by_lazymind')
            or data.get('managed_by_lazymind')
            or data.get('managed')
            or binding.get('managed')
            or False
        )
        workspace.assistant_turns = codex_turns_for_card(turns)
        workspace.assistant_turns_offset = int(
            thread.get('offset') or data.get('offset') or 0
        )
        workspace.assistant_turns_total = int(
            thread.get('total_turns')
            or thread.get('total')
            or data.get('total_turns')
            or len(workspace.assistant_turns)
        )
        workspace.assistant_answer_page = 0
        workspace.run_status = 'idle'
        workspace.user_text = ''
        workspace.chat_text = ''
        workspace.chat_status = ''
        workspace.chat_thinking = ''

    @staticmethod
    def _apply_assistant_snapshot(
        workspace: FeishuWorkspaceState,
        snapshot: dict[str, Any],
    ) -> None:
        status = str(snapshot.get('status') or snapshot.get('state') or '')
        pending = None
        if snapshot.get('pending_request_id'):
            pending = {
                'request_id': str(snapshot.get('pending_request_id')),
                'kind': 'request',
                'summary': 'Codex 仍在等待确认',
            }
        elif isinstance(snapshot.get('pending_request'), dict):
            pending = dict(snapshot['pending_request'])
        workspace.assistant_pending_request = pending
        if pending:
            workspace.run_status = 'waiting_for_input'
        elif status in {
            'starting', 'running', 'waiting', 'active',
        }:
            workspace.run_status = 'running'
        elif status in {'failed'}:
            workspace.run_status = 'failed'
        elif status in {'interrupted'}:
            workspace.run_status = 'cancelled'
        else:
            workspace.run_status = 'completed' if status else 'idle'
        if snapshot.get('answer'):
            workspace.chat_text = str(snapshot.get('answer'))[:16000]
        workspace.chat_status = (
            '💬 **等待你的确认或输入**'
            if workspace.assistant_pending_request
            else '⏳ **Codex 正在继续执行**'
            if workspace.run_status == 'running'
            else '✅ **Codex 已同步**'
        )

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
        pending_turn = self._store.get_pending_turn(
            route.account_id,
            address_hash,
        )
        workspace = FeishuWorkspaceState.from_dict(
            self._store.get_feishu_workspace_state(
                route.account_id,
                address_hash,
            )
        )
        action_kind = str((action.workspace_action or {}).get('kind') or '')
        if action_kind.startswith('assistant.'):
            return self._handle_assistant_action(
                route=route,
                action=action,
                address_hash=address_hash,
                workspace=workspace,
            )
        if action_kind == 'operation.cancel':
            return self._cancel_generation(
                route=route,
                action=action,
                address_hash=address_hash,
                conversation_id=conversation_id,
                workspace=workspace,
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
                    'workspace_action': action.workspace_action,
                },
                ensure_ascii=False,
                sort_keys=True,
                separators=(',', ':'),
            ).encode('utf-8')
        ).hexdigest()
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
                workspace.expire_conversation_switch(action.selection)
                workspace.advance()
                self._store.save_feishu_workspace_state(
                    route.account_id,
                    address_hash,
                    workspace.to_dict(),
                )
                provider_context = self._workspace_provider_context(
                    workspace=workspace,
                    chat_id=action.chat_id,
                    conversation_id=conversation_id,
                    pending_turn=pending_turn,
                    workspace_action=action.workspace_action,
                )
                card = FeishuWorkspaceRenderer.render(
                    provider_context=provider_context,
                    text='',
                    presentations=[],
                )
                self._schedule_action_card_refresh(
                    route.account_id,
                    action.message_id,
                    card,
                )
                self._log_action_ready(
                    action,
                    started_at=started_at,
                    cached=True,
                )
                return card
        workspace_ask_validated = bool(
            action.action == 'ask'
            and isinstance(action.ask_answers_structured, dict)
            and workspace.pending_ask_id
            and str(
                action.ask_answers_structured.get('ask_id') or ''
            ) == workspace.pending_ask_id
        )
        self._apply_workspace_action(
            workspace=workspace,
            action=action.workspace_action,
            conversation_id=conversation_id,
            pending_turn=pending_turn,
            account_id=route.account_id,
            address_hash=address_hash,
            owner_user_id=route.owner_user_id,
            request_id=message_key,
        )
        if action_kind == 'history.switch':
            workspace.begin_conversation_switch(
                message_key,
                action.selection,
            )
        elif (
            action.action == 'ask'
            or action_kind in _RESULT_WORKSPACE_ACTIONS
        ):
            workspace.begin_operation(message_key, action.text)
        workspace.advance()
        self._store.save_feishu_workspace_state(
            route.account_id,
            address_hash,
            workspace.to_dict(),
        )
        provider_context = {
            **self._workspace_provider_context(
                workspace=workspace,
                chat_id=action.chat_id,
                conversation_id=conversation_id,
                pending_turn=pending_turn,
                workspace_action=action.workspace_action,
            ),
            'workspace_operation_id': message_key,
            'workspace_ask_validated': workspace_ask_validated,
            'ask_answers_structured': action.ask_answers_structured,
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
        cached = self._cached_workspace_card(action, provider_context)
        if cached is not None:
            self._schedule_action_card_refresh(
                route.account_id,
                action.message_id,
                cached,
            )
            self._log_action_ready(
                action,
                started_at=started_at,
                cached=True,
            )
            return cached
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
                    recipient_id=action.chat_id,
                    text=action.text,
                    provider_context=provider_context,
                )
            ],
            None,
            lease.fence,
        )
        card = FeishuWorkspaceRenderer.render(
            provider_context=provider_context,
            text='',
            presentations=[],
        )
        self._schedule_action_card_refresh(
            route.account_id,
            action.message_id,
            card,
        )
        self._log_action_ready(
            action,
            started_at=started_at,
            cached=False,
        )
        return card

    def _cancel_generation(
        self,
        *,
        route: _AccountRoute,
        action: FeishuInboundAction,
        address_hash: str,
        conversation_id: str,
        workspace: FeishuWorkspaceState,
    ) -> dict[str, Any]:
        language = workspace.output_language
        if (
            workspace.view == 'assistant'
            and workspace.assistant_conversation_id
            and workspace.run_status == 'running'
        ):
            try:
                self._core.interrupt_external_conversation(
                    owner_user_id=route.owner_user_id,
                    request_id='feishu_assistant_cancel_' + hashlib.sha256(
                        action.message_id.encode('utf-8')
                    ).hexdigest()[:24],
                    conversation_id=workspace.assistant_conversation_id,
                )
                workspace.cancel_operation()
            except Exception:
                workspace.chat_status = '⚠️ **取消 Codex 任务失败，请重试**'
            workspace.advance()
            self._store.save_feishu_workspace_state(
                route.account_id, address_hash, workspace.to_dict()
            )
            card = FeishuWorkspaceRenderer.render(
                provider_context={
                    'chat_id': action.chat_id,
                    'workspace_state': workspace.to_dict(),
                },
                text='',
                presentations=[],
            )
            card['config'].pop('streaming_config', None)
            return card
        streaming = workspace.run_status == 'running'
        if not streaming:
            status = (
                'ℹ️ **This generation is no longer running**'
                if language == 'en'
                else 'ℹ️ **本次生成已经结束**'
            )
            thinking = workspace.chat_thinking
        elif not conversation_id or not workspace.generation_history_id:
            status = (
                '⏳ **Starting the task; please retry in a moment**'
                if language == 'en'
                else '⏳ **任务正在建立，请稍后再次取消**'
            )
            thinking = workspace.chat_thinking
        elif self._core.stop_conversation(
            owner_user_id=route.owner_user_id,
            conversation_id=conversation_id,
            history_id=workspace.generation_history_id,
            request_id=(
                'feishu_cancel_'
                + hashlib.sha256(
                    (
                        action.message_id
                        + workspace.active_operation_id
                    ).encode('utf-8')
                ).hexdigest()[:24]
            ),
        ):
            workspace.cancel_operation()
            workspace.advance()
            self._store.save_feishu_workspace_state(
                route.account_id,
                address_hash,
                workspace.to_dict(),
            )
            status = workspace.chat_status
            thinking = workspace.chat_thinking
            streaming = False
        else:
            status = (
                '⚠️ **Cancel failed; please retry**'
                if language == 'en'
                else '⚠️ **取消失败，请再次尝试**'
            )
            thinking = workspace.chat_thinking
        return FeishuReplyRenderer.render(
            provider_context={
                'chat_id': action.chat_id,
                'workspace_state': workspace.to_dict(),
            },
            text=(
                workspace.chat_text
                or (
                    'No partial answer was produced.'
                    if not streaming and language == 'en'
                    else '取消前尚未生成可展示的回答。'
                    if not streaming
                    else ''
                )
            ),
            presentations=workspace.chat_presentations,
            status=status,
            thinking=thinking,
            streaming=streaming,
        )

    def _handle_menu(
        self,
        worker: _AppWorker,
        menu: FeishuInboundMenu,
    ) -> None:
        view = MENU_EVENT_VIEWS.get(menu.event_key)
        if not view or not menu.sender_id:
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
                state.navigate(
                    view,
                    conversation_id=self._store.get_route(
                        route.account_id,
                        address_hash,
                    ),
                    pending_turn=self._store.get_pending_turn(
                        route.account_id,
                        address_hash,
                    ),
                )
                message_id = state.message_id
            card = FeishuWorkspaceRenderer.render(
                provider_context={
                    'chat_id': chat_id,
                    'workspace_state': state.to_dict(),
                },
                text='',
                presentations=[],
            )
            if message_id:
                message_id = self._send_card_to_bottom(
                    sender=sender,
                    chat_id=chat_id,
                    card=card,
                    idempotency_key=(
                        f'feishu-menu-bottom:{route.account_id}:'
                        f'{menu.event_id or menu.event_key}'
                    ),
                )
            else:
                message_id, resolved_chat_id = (
                    sender.send_card_to_user_with_chat(
                        open_id=menu.sender_id,
                        card=card,
                        idempotency_key=(
                            f'feishu-menu:{route.account_id}:'
                            f'{menu.event_id or menu.event_key}'
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
        pending_turn = self._store.get_pending_turn(
            route.account_id,
            address_hash,
        )
        state = FeishuWorkspaceState.from_dict(
            self._store.get_feishu_workspace_state(
                route.account_id,
                address_hash,
            )
        )
        state.navigate(
            view,
            conversation_id=conversation_id,
            pending_turn=pending_turn,
        )
        state.message_id = message_id
        if view == 'assistant':
            state.assistant_mode = 'projects'
            state.assistant_status = 'loading'
            state.assistant_error = ''
        state.advance()
        self._store.save_feishu_workspace_state(
            route.account_id,
            address_hash,
            state.to_dict(),
        )
        command = menu_command(view)
        provider_context = {
            **self._workspace_provider_context(
                workspace=state,
                chat_id=chat_id,
                conversation_id=conversation_id,
                pending_turn=pending_turn,
                workspace_action={'kind': 'navigate', 'view': view},
            ),
            'workspace_surface': 'management',
            'command_action': command,
        }
        if command is None:
            sender = self._channels.create_sender(
                self._credentials.load_runtime_account(
                    route.account_id
                )['credentials']
            )
            try:
                sender.update_card(
                    message_id=message_id,
                    card=FeishuWorkspaceRenderer.render(
                        provider_context=provider_context,
                        text='',
                        presentations=[],
                    ),
                )
            finally:
                sender.close()
            if view == 'assistant':
                self._schedule_assistant_threads(
                    route=route,
                    address_hash=address_hash,
                    chat_id=chat_id,
                    revision=state.revision,
                )
            return
        message_key = hashlib.sha256(
            (
                f'menu:{menu.event_id}:{menu.sender_id}:'
                f'{menu.event_key}'
            ).encode('utf-8')
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
                    recipient_id=chat_id,
                    text={
                        'capabilities': '查看能力',
                        'conversations': '切换会话',
                        'settings': '查看设置',
                    }[view],
                    provider_context=provider_context,
                )
            ],
            None,
            lease.fence,
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
    ) -> None:
        timer = threading.Timer(
            _ACTION_REFRESH_DELAY_SECONDS,
            self._refresh_action_card,
            args=(account_id, message_id, card),
        )
        timer.daemon = True
        timer.name = 'feishu-card-action-refresh'
        timer.start()

    def _refresh_action_card(
        self,
        account_id: str,
        message_id: str,
        card: dict[str, Any],
    ) -> None:
        sender = None
        try:
            account = self._credentials.load_runtime_account(account_id)
            sender = self._channels.create_sender(account['credentials'])
            sender.update_card(message_id=message_id, card=card)
            _logger.info(
                'feishu_card_action_refresh_succeeded '
                'account_id=%s message_id=%s',
                account_id,
                message_id,
            )
        except Exception:
            _logger.exception(
                'feishu_card_action_refresh_failed '
                'account_id=%s message_id=%s',
                account_id,
                message_id,
            )
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

    @staticmethod
    def _cached_workspace_card(
        action: FeishuInboundAction,
        provider_context: dict[str, Any],
    ) -> dict[str, Any] | None:
        workspace = FeishuWorkspaceState.from_dict(
            provider_context.get('workspace_state')
        )
        if action.action not in {'command', 'local'}:
            return None
        workspace_action = action.workspace_action or {}
        kind = str(workspace_action.get('kind') or '')
        if (
            kind not in _LOCAL_WORKSPACE_ACTIONS
            and not (
                action.action == 'command' and kind == 'navigate'
            )
        ):
            return None
        view = workspace.view
        if view == 'chat':
            _text, presentations = workspace.snapshot_for_view('chat')
            return FeishuWorkspaceRenderer.render(
                provider_context=provider_context,
                text='',
                presentations=[],
                extra_chat_elements=workspace_ask_elements(
                    presentations,
                    provider_context,
                ),
            )
        _text, presentations = workspace.snapshot_for_view(view)
        if kind in {
            'context.open',
            'context.category',
            'context.page',
        } and not any(
            str(group.get('resource_type') or '')
            == workspace.context_category
            for presentation in presentations
            if presentation.get('kind') == 'capability'
            for group in (
                presentation.get('groups')
                if isinstance(presentation.get('groups'), list)
                else []
            )
            if isinstance(group, dict)
        ):
            return None
        if not presentations and view not in {'assistant', 'settings'}:
            return None
        return FeishuWorkspaceRenderer.render(
            provider_context=provider_context,
            text='',
            presentations=[],
        )

    def _apply_workspace_action(
        self,
        *,
        workspace: FeishuWorkspaceState,
        action: dict | None,
        conversation_id: str,
        pending_turn: dict,
        account_id: str,
        address_hash: str,
        owner_user_id: str,
        request_id: str,
    ) -> None:
        if not action:
            return
        kind = str(action.get('kind') or '')
        if kind == 'navigate':
            target = str(action.get('view') or '')
            workspace.navigate(
                target,
                conversation_id=conversation_id,
                pending_turn=pending_turn,
            )
        elif kind == 'context.open':
            workspace.open_context(
                scope=str(action.get('scope') or 'turn'),
                category=str(
                    action.get('category')
                    or workspace.context_category
                ),
                conversation_id=conversation_id,
                pending_turn=pending_turn,
            )
        elif kind == 'context.category':
            category = str(action.get('category') or '')
            if category in {
                'knowledge_base',
                'skill',
                'plugin',
                'tool',
                'prompt',
                'conversation',
            }:
                workspace.context_category = category
            scope = str(action.get('scope') or workspace.context_scope)
            if scope in {'global', 'conversation', 'turn'}:
                workspace.context_scope = scope
            workspace.context_page = 0
            workspace.view = 'context'
        elif kind == 'context.page':
            page = action.get('page')
            if isinstance(page, int) and not isinstance(page, bool):
                workspace.context_page = max(0, page)
            workspace.view = 'context'
        elif kind == 'context.toggle':
            workspace.toggle_context(action.get('resource'))
        elif kind == 'context.save':
            turn = workspace.save_context(conversation_id)
            if workspace.context_scope == 'turn':
                next_turn = dict(pending_turn)
                next_turn['mentions'] = [
                    item.to_mention() for item in turn
                ]
                self._store.save_pending_turn(
                    account_id,
                    address_hash,
                    next_turn,
                )
                pending_turn.clear()
                pending_turn.update(next_turn)
        elif kind == 'history.switch':
            workspace.view = 'conversations'
        elif kind == 'history.open':
            workspace.view = 'conversations'
        elif kind == 'capability.toggle':
            workspace.context_scope = 'conversation'
            workspace.view = 'capabilities'
            workspace.toggle_context(action.get('resource'))
        elif kind == 'capability.save':
            self._save_workspace_capabilities(
                workspace=workspace,
                conversation_id=conversation_id,
                owner_user_id=owner_user_id,
                request_id=request_id,
            )
        elif kind == 'new_session.open':
            workspace.open_new_session()
        elif kind == 'new_session.mode':
            mode = str(action.get('mode') or 'blank')
            workspace.new_session_mode = (
                mode if mode in {'blank', 'inherit'} else 'blank'
            )
            workspace.new_session_open = True
            workspace.view = 'conversations'
        elif kind == 'new_session.cancel':
            workspace.new_session_open = False
            workspace.view = 'conversations'
        elif kind == 'new_session.create':
            workspace.prepare_new_session(
                mode=str(action.get('mode') or 'blank'),
                conversation_id=conversation_id,
            )
        elif kind == 'setting.update':
            workspace.navigate(
                str(action.get('view') or 'capabilities'),
                conversation_id=conversation_id,
                pending_turn=pending_turn,
            )
        elif kind == 'preference':
            name = str(action.get('name') or '')
            value = action.get('value')
            changed = False
            if name == 'settings_save':
                workspace.preferences_dirty = False
            elif name == 'thinking_depth' and value in {
                'low',
                'medium',
                'high',
                'max',
            }:
                workspace.thinking_depth = str(value)
                changed = True
            elif name == 'output_language' and value in {
                'zh',
                'en',
            }:
                workspace.output_language = str(value)
                changed = True
            elif name == 'show_process' and isinstance(value, bool):
                workspace.show_process = value
                changed = True
            elif (
                name == 'auto_collapse_process'
                and isinstance(value, bool)
            ):
                workspace.auto_collapse_process = value
                changed = True
            elif name == 'show_sources' and isinstance(value, bool):
                workspace.show_sources = value
                changed = True
            elif name == 'ready_marker' and isinstance(value, bool):
                workspace.ready_marker = value
                changed = True
                if not value:
                    workspace.unread_results = 0
            elif name == 'restore_last_view' and isinstance(value, bool):
                workspace.restore_last_view = value
                changed = True
            if changed:
                workspace.preferences_dirty = True
            workspace.view = 'settings'
        elif kind == 'maintenance.clear_turn':
            next_turn = dict(pending_turn)
            next_turn['mentions'] = []
            self._store.save_pending_turn(
                account_id,
                address_hash,
                next_turn,
            )
            pending_turn.clear()
            pending_turn.update(next_turn)
            workspace.context_draft = []
            workspace.view = 'settings'
        elif kind == 'maintenance.reset_preferences':
            workspace.reset_preferences()
            workspace.view = 'settings'
        elif kind == 'maintenance.clear_conversation':
            workspace.conversations.pop(conversation_id, None)
            workspace.prepare_new_session(
                mode='blank',
                conversation_id=conversation_id,
            )
            workspace.clear_chat()
            self._store.save_pending_turn(
                account_id,
                address_hash,
                {},
            )
            pending_turn.clear()

    def _save_workspace_capabilities(
        self,
        *,
        workspace: FeishuWorkspaceState,
        conversation_id: str,
        owner_user_id: str,
        request_id: str,
    ) -> None:
        if conversation_id:
            dataset_ids = [
                resource.id
                for resource in workspace.context_draft
                if resource.type == 'knowledge_base' and resource.id
            ]
            self._core.update_conversation_search_config(
                owner_user_id=owner_user_id,
                conversation_id=conversation_id,
                request_id=f'{request_id}_capability_kb',
                dataset_ids=list(dict.fromkeys(dataset_ids)),
            )
        workspace.save_capabilities(conversation_id)

    @staticmethod
    def _workspace_provider_context(
        *,
        workspace: FeishuWorkspaceState,
        chat_id: str,
        conversation_id: str,
        pending_turn: dict,
        workspace_action: dict | None = None,
    ) -> dict:
        resources = workspace.effective_resources(
            conversation_id,
            pending_turn,
        )
        return {
            'chat_id': chat_id,
            'surface': 'card',
            'workspace_conversation_id': conversation_id,
            'workspace_message_id': workspace.message_id,
            'workspace_operation_id': workspace.active_operation_id,
            'workspace_state': workspace.to_dict(),
            'workspace_resources': [
                item.to_dict()
                for item in resources
            ],
            'workspace_mentions': [
                item.to_mention()
                for item in resources
            ],
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


def _workspace_stream_message_id(
    workspace: FeishuWorkspaceState,
) -> str:
    return workspace.message_id


def _assistant_request_payload(
    values: dict[str, Any],
    pending: dict[str, Any],
) -> dict[str, Any]:
    kind = str(values.get('kind') or pending.get('kind') or '')
    decision = str(values.get('decision') or '').strip()
    if decision in {'approve', 'accept', 'yes'}:
        decision = 'accept'
    elif decision in {'deny', 'decline', 'no', 'reject'}:
        decision = 'decline'
    elif decision in {'acceptForSession', 'session'}:
        decision = 'acceptForSession'
    elif decision in {'cancel'}:
        decision = 'cancel'
    if kind == 'user_input' or values.get('answers') is not None:
        answers = values.get('answers')
        if not isinstance(answers, dict):
            answers = {}
        return {'answers': answers}
    if kind == 'permissions_approval':
        permissions = values.get('permissions')
        if not isinstance(permissions, dict):
            permissions = {}
        return {'permissions': permissions}
    if not decision:
        decision = 'accept'
    return {'decision': decision}

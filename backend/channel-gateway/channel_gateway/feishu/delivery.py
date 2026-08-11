import hashlib
import logging
import threading
import time
from dataclasses import replace
from typing import Any

from channel_gateway.common.domain.channel import (
    ClaimedInbound,
    ClaimedOutbound,
)
from channel_gateway.common.domain.chat import (
    ChannelExecutionContext,
    CoreStreamUpdate,
)
from channel_gateway.common.domain.outbound import (
    OutboundRenderer,
    inline_artifact_bytes,
)
from channel_gateway.common.errors import (
    InvalidStaticAssetError,
)
from channel_gateway.common.ports.core import StaticAssetClient
from channel_gateway.common.ports.providers import RuntimeCredentialStore
from channel_gateway.common.ports.messaging import ReplyStream
from channel_gateway.feishu.domain import (
    FeishuRuntimeError,
    workspace_card_expired,
)
from channel_gateway.feishu.assistant import (
    assistant_view_with_ui,
    detail_run_status,
    detail_view,
    detail_with_prompt,
)
from channel_gateway.feishu.ports import (
    FeishuOutboundFactory,
    FeishuWorkspaceRepository,
)
from channel_gateway.feishu.presentation import (
    FeishuPresentationRenderer,
    FeishuReplyRenderer,
    media_free_feishu_text,
    streamable_feishu_text,
    streaming_reply_card,
    workflow_progress_text,
)
from channel_gateway.feishu.task_monitor import workflow_tasks
from channel_gateway.feishu.workspace import (
    FeishuWorkspaceRenderer,
    FeishuWorkspaceState,
)


_MAX_FEISHU_IMAGE_BYTES = 10 * 1024 * 1024
_MAX_FEISHU_FILE_BYTES = 30 * 1024 * 1024
_STREAM_STATE_CHECK_SECONDS = 1.0
_LIVE_TASK_POLL_SECONDS = 1.5


_logger = logging.getLogger(__name__)


def _expire_workspace_card(
    sender: Any,
    *,
    message_id: str,
    workspace: dict[str, Any],
) -> None:
    # Stale CardKit actions are fenced by message/revision/operation.  Do not
    # create a second user-visible replacement card for an already stale card.
    del sender, message_id, workspace


def _external_agent_conversation_id(
    provider_context: dict[str, Any],
) -> str:
    return ChannelExecutionContext.from_provider_context(
        provider_context
    ).external_agent_conversation_id


class _ManagedReplyStream:
    def __init__(
        self,
        stream: ReplyStream,
        sender,
        provider_context: dict[str, Any],
        store: FeishuWorkspaceRepository,
        account_id: str,
        address_hash: str,
        *,
        core: Any = None,
        owner_user_id: str = '',
    ):
        self._stream = stream
        self._sender = sender
        self._provider_context = provider_context
        self._store = store
        self._account_id = account_id
        self._address_hash = address_hash
        self._core = core
        self._owner_user_id = owner_user_id
        self._conversation_id = ''
        self._state_access_failed = False
        self._interaction_ready = False
        self._latest_snapshot = CoreStreamUpdate()
        self._snapshot_lock = threading.Lock()
        self._task_stop = threading.Event()
        self._task_thread: threading.Thread | None = None
        self._task_anchor_id = ''

    def update(self, snapshot: CoreStreamUpdate) -> None:
        assistant = bool(
            _external_agent_conversation_id(self._provider_context)
        )
        if (
            snapshot.conversation_id
            and snapshot.conversation_id != self._conversation_id
        ):
            self._activate_conversation(snapshot.conversation_id)
        with self._snapshot_lock:
            if (
                not snapshot.workflow_progress
                and self._latest_snapshot.workflow_progress
            ):
                snapshot = replace(
                    snapshot,
                    workflow_progress=(
                        self._latest_snapshot.workflow_progress
                    ),
                )
            self._latest_snapshot = snapshot
        if not assistant and snapshot.task_created:
            self._start_task_progress(
                snapshot.task_created,
                snapshot.conversation_id,
            )
        if assistant:
            workspace = FeishuWorkspaceState.from_dict(
                self._provider_context.get('workspace_state')
            )
            expected_thread_id = workspace.assistant_selected_thread_id
            rebind_patch: dict[str, Any] = {}
            if snapshot.external_event:
                rebind_patch = self._apply_external_thread_rebind(
                    snapshot.external_event
                )
                view = self._provider_context.get('assistant_view')
                canonical = snapshot.external_event.get('snapshot')
                if isinstance(view, dict) and isinstance(canonical, dict):
                    view['snapshot'] = dict(canonical)
            if rebind_patch:
                workspace = self._provider_context.get('workspace_state')
                if isinstance(workspace, dict):
                    workspace.update(rebind_patch)
                try:
                    self._patch_workspace(
                        rebind_patch,
                        expected_thread_id=expected_thread_id,
                    )
                    self._log_state_recovered()
                except Exception:
                    self._log_state_failure('thread_rebind')
        self._stream.update(snapshot)
        if assistant and not self._interaction_ready:
            view = self._provider_context.get('assistant_view')
            canonical = (
                view.get('snapshot')
                if isinstance(view, dict)
                else None
            )
            pending = (
                canonical.get('pending_request')
                if isinstance(canonical, dict)
                else None
            )
            if isinstance(pending, dict) and pending.get('request_id'):
                pause = getattr(
                    self._stream,
                    'pause_for_interaction',
                    None,
                )
                if callable(pause):
                    pause()
                    self._interaction_ready = True

    def _start_task_progress(
        self,
        task: dict[str, Any],
        conversation_id: str,
    ) -> None:
        task_id = str(task.get('task_id') or '')[:512]
        if (
            not task_id
            or not conversation_id
            or not self._owner_user_id
            or not callable(
                getattr(self._core, 'list_conversation_tasks', None)
            )
            or self._task_thread is not None
        ):
            return
        self._task_anchor_id = task_id
        self._task_stop.clear()
        self._task_thread = threading.Thread(
            target=self._poll_task_progress,
            args=(conversation_id,),
            name='feishu-live-workflow',
            daemon=True,
        )
        self._task_thread.start()

    def _poll_task_progress(self, conversation_id: str) -> None:
        request_id = str(
            self._provider_context.get('workspace_operation_id')
            or self._task_anchor_id
        )
        while not self._task_stop.is_set():
            try:
                tasks = self._core.list_conversation_tasks(
                    owner_user_id=self._owner_user_id,
                    conversation_id=conversation_id,
                    request_id=f'{request_id}_live_tasks',
                    summary_only=True,
                )
                progress = workflow_progress_text(
                    workflow_tasks(tasks, self._task_anchor_id)
                )
                if progress:
                    with self._snapshot_lock:
                        snapshot = replace(
                            self._latest_snapshot,
                            task_created=None,
                            workflow_progress=progress,
                        )
                        self._latest_snapshot = snapshot
                    self._stream.update(snapshot)
            except Exception:
                _logger.warning(
                    'feishu_live_workflow_refresh_failed task_id=%s',
                    self._task_anchor_id,
                    exc_info=True,
                )
            self._task_stop.wait(_LIVE_TASK_POLL_SECONDS)

    def _stop_task_progress(self) -> None:
        self._task_stop.set()
        if self._task_thread is not None:
            self._task_thread.join(timeout=2)
            self._task_thread = None

    def _apply_external_thread_rebind(
        self,
        event: dict[str, Any],
    ) -> dict[str, Any]:
        raw_event = event.get('event')
        if not isinstance(raw_event, dict):
            return {}
        event = raw_event
        event_type = str(
            event.get('type') or event.get('event_type') or ''
        )
        if event_type != 'thread_forked':
            return {}
        payload = event.get('payload')
        data = dict(payload) if isinstance(payload, dict) else dict(event)
        thread_id = str(data.get('thread_id') or '').strip()[:512]
        return {'assistant_selected_thread_id': thread_id}

    def _activate_conversation(self, conversation_id: str) -> None:
        if _external_agent_conversation_id(self._provider_context):
            self._conversation_id = conversation_id
            return
        try:
            self._store.activate_conversation(
                self._account_id,
                self._address_hash,
                conversation_id,
            )
        except Exception:
            self._log_state_failure('activate')
            return
        self._log_state_recovered()
        self._provider_context['workspace_conversation_id'] = conversation_id
        self._conversation_id = conversation_id

    def _log_state_failure(self, operation: str) -> None:
        if self._state_access_failed:
            return
        self._state_access_failed = True
        _logger.warning(
            'feishu_stream_state_%s_failed account_id=%s',
            operation,
            self._account_id,
            exc_info=True,
        )

    def _log_state_recovered(self) -> None:
        if not self._state_access_failed:
            return
        self._state_access_failed = False
        _logger.info(
            'feishu_stream_state_recovered account_id=%s',
            self._account_id,
        )

    def finish(self, final_text: str) -> bool:
        try:
            self._stop_task_progress()
            operation_id = str(
                self._provider_context.get('workspace_operation_id') or ''
            )
            assistant = (
                bool(_external_agent_conversation_id(self._provider_context))
            )
            if assistant:
                state = self._assistant_finish_state(operation_id)
                if state is None:
                    self._provider_context[
                        '_workspace_stream_suppress_final'
                    ] = True
                else:
                    self._provider_context['workspace_state'] = (
                        state.to_dict()
                    )
            if assistant and state is not None:
                self._refresh_external_detail()
            streamed = self._stream.finish(final_text)
            message_id = str(
                getattr(self._stream, 'message_id', '') or ''
            )
            if message_id:
                self._provider_context['workspace_stream_message_id'] = (
                    message_id
                )
            return streamed
        finally:
            self._sender.close()

    def _assistant_finish_state(
        self,
        operation_id: str,
    ) -> FeishuWorkspaceState | None:
        try:
            state = FeishuWorkspaceState.from_dict(
                self._store.get_feishu_workspace_state(
                    self._account_id,
                    self._address_hash,
                )
            )
        except Exception:
            self._log_state_failure('finish_read')
            return None
        if (
            operation_id
            and state.active_operation_id
            and state.active_operation_id != operation_id
        ):
            return None
        if (
            state.view != 'assistant'
            or state.assistant_mode != 'detail'
            or not state.assistant_selected_thread_id
        ):
            return None
        return state

    def _refresh_external_detail(self) -> None:
        read_thread = getattr(self._core, 'read_external_thread', None)
        workspace = FeishuWorkspaceState.from_dict(
            self._provider_context.get('workspace_state')
        )
        if (
            not _external_agent_conversation_id(self._provider_context)
            or not callable(read_thread)
            or not self._owner_user_id
        ):
            return
        thread_id = workspace.assistant_selected_thread_id
        if not thread_id:
            return
        request_id = str(
            self._provider_context.get('workspace_operation_id')
            or 'feishu_external'
        )
        try:
            page = read_thread(
                owner_user_id=self._owner_user_id,
                request_id=f'{request_id}_detail',
                provider='codex',
                thread_id=thread_id,
                offset=0,
                limit=1,
                tail=True,
            )
            prompt = ''
            current_view = self._provider_context.get('assistant_view')
            if isinstance(current_view, dict):
                prompt = str(current_view.get('prompt') or '')[:4000]
            view = detail_with_prompt(detail_view(page), prompt)
            self._provider_context['assistant_view'] = view
        except Exception:
            _logger.warning(
                'feishu_external_detail_refresh_failed thread_id=%s',
                thread_id,
                exc_info=True,
            )

    def _patch_workspace(
        self,
        patch: dict[str, Any],
        *,
        expected_thread_id: str = '',
    ) -> dict[str, Any]:
        operation_id = str(
            self._provider_context.get('workspace_operation_id') or ''
        )
        for _ in range(3):
            workspace = self._store.get_feishu_workspace_state(
                self._account_id,
                self._address_hash,
            )
            state = FeishuWorkspaceState.from_dict(workspace)
            if operation_id and state.active_operation_id != operation_id:
                self._provider_context['workspace_state'] = workspace
                return workspace
            if (
                expected_thread_id
                and (
                    state.assistant_selected_thread_id != expected_thread_id
                )
            ):
                self._provider_context['workspace_state'] = workspace
                return workspace
            expected_revision = state.revision
            merged = {**workspace, **patch}
            state = FeishuWorkspaceState.from_dict(merged)
            state.advance()
            if self._store.save_feishu_workspace_state_if_revision(
                self._account_id,
                self._address_hash,
                state.to_dict(),
                expected_revision,
            ):
                saved = state.to_dict()
                self._provider_context['workspace_state'] = saved
                return saved
        workspace = self._store.get_feishu_workspace_state(
            self._account_id,
            self._address_hash,
        )
        self._provider_context['workspace_state'] = workspace
        return workspace

    def abort(self) -> None:
        try:
            self._stop_task_progress()
            if _external_agent_conversation_id(self._provider_context):
                self._refresh_external_detail()
            self._stream.abort()
        finally:
            self._sender.close()


class FeishuDeliveryProvider:
    def __init__(
        self,
        *,
        store: FeishuWorkspaceRepository,
        credentials: RuntimeCredentialStore,
        channels: FeishuOutboundFactory,
        renderer: OutboundRenderer,
        lazymind: StaticAssetClient,
    ):
        self._store = store
        self._credentials = credentials
        self._channels = channels
        self._renderer = FeishuPresentationRenderer(renderer)
        self._lazymind = lazymind

    def open_stream(
        self,
        message: ClaimedInbound,
    ) -> ReplyStream | None:
        command_action = message.provider_context.get('command_action')
        if (
            isinstance(command_action, dict)
            and str(command_action.get('command') or '')
            not in {'chat', 'workflow.invoke'}
        ):
            return None
        chat_id = str(
            message.provider_context.get('chat_id')
            or message.recipient_id
        )
        if not chat_id:
            return None
        account = self._credentials.load_runtime_account(
            message.account_id
        )
        sender = self._channels.create_sender(
            account['credentials']
        )
        workspace_message_id = str(
            message.provider_context.get('workspace_message_id')
            or ''
        )
        management = (
            message.provider_context.get('workspace_surface') in {
                'management',
                'assistant',
            }
        )
        assistant = (
            message.provider_context.get('workspace_surface') == 'assistant'
        )
        stream_context = {
            **message.provider_context,
            'chat_id': chat_id,
        }
        initial_card = (
            FeishuWorkspaceRenderer.render(
                provider_context=stream_context,
                presentations=[],
                streaming=True,
            )
            if assistant
            else streaming_reply_card(stream_context)
        )
        stream_holder: dict[str, ReplyStream] = {}
        try:
            stream = sender.start_card_stream(
                chat_id=chat_id,
                initial_card=initial_card,
                message_id=workspace_message_id,
                should_render=(
                    (lambda: self._workspace_chat_is_visible(
                        message,
                        stream_holder.get('stream'),
                    ))
                    if management
                    else None
                ),
                render_card=(
                    (lambda snapshot, finished, aborted:
                        self._assistant_stream_card(
                            message=message,
                            chat_id=chat_id,
                            snapshot=snapshot,
                            finished=finished,
                            aborted=aborted,
                        )
                     )
                    if assistant
                    else None
                ),
            )
            stream_holder['stream'] = stream
        except Exception as exc:
            sender.close()
            if assistant:
                self._fail_assistant_stream_open(message, exc)
            raise
        return _ManagedReplyStream(
            stream,
            sender,
            message.provider_context,
            self._store,
            message.account_id,
            message.order_key,
            core=self._lazymind,
            owner_user_id=message.owner_user_id,
        )

    def _fail_assistant_stream_open(
        self,
        message: ClaimedInbound,
        error: Exception,
    ) -> None:
        view = message.provider_context.get('assistant_view')
        view = view if isinstance(view, dict) else {}
        message.provider_context['assistant_view'] = assistant_view_with_ui(
            view,
            'error',
            str(error),
        )

    @staticmethod
    def _assistant_stream_card(
        *,
        message: ClaimedInbound,
        chat_id: str,
        snapshot: CoreStreamUpdate,
        finished: bool,
        aborted: bool,
    ) -> dict[str, Any]:
        workspace = FeishuWorkspaceState.from_dict(
            message.provider_context.get('workspace_state')
        )
        assistant_view = message.provider_context.get('assistant_view')
        if (
            isinstance(assistant_view, dict)
            and assistant_view.get('kind') == 'detail'
        ):
            view_snapshot = assistant_view.get('snapshot')
            view_snapshot = (
                dict(view_snapshot)
                if isinstance(view_snapshot, dict)
                else {}
            )
            if snapshot.answer:
                view_snapshot['answer'] = streamable_feishu_text(
                    snapshot.answer
                )[:16000]
            current_status = detail_run_status(assistant_view)
            if aborted and current_status not in {
                'failed',
                'cancelled',
                'releasing',
                'release_failed',
            }:
                view_snapshot['status'] = 'interrupted'
            elif not finished and current_status not in {
                'waiting_for_input',
                'failed',
                'cancelled',
                'releasing',
                'release_failed',
            }:
                view_snapshot['status'] = 'running'
            assistant_view['snapshot'] = view_snapshot
        streaming = (
            not finished
            and not aborted
            and isinstance(assistant_view, dict)
            and detail_run_status(assistant_view) == 'running'
        )
        card = FeishuWorkspaceRenderer.render(
            provider_context={
                **message.provider_context,
                'chat_id': chat_id,
                'workspace_state': workspace.to_dict(),
            },
            presentations=[],
            streaming=streaming,
        )
        if not streaming:
            card['config'].pop('streaming_config', None)
        return card

    def _workspace_chat_is_visible(
        self,
        message: ClaimedInbound,
        stream: ReplyStream | None = None,
    ) -> bool:
        context = message.provider_context
        if context.get('_workspace_stream_suppress_final'):
            return False
        assistant = context.get('workspace_surface') == 'assistant'
        now = time.monotonic()
        checked_at = float(
            context.get('_workspace_visibility_checked_at') or 0.0
        )
        if (
            not assistant
            and now - checked_at < _STREAM_STATE_CHECK_SECONDS
        ):
            return bool(context.get('_workspace_chat_visible', True))
        context['_workspace_visibility_checked_at'] = now
        try:
            state = FeishuWorkspaceState.from_dict(
                self._store.get_feishu_workspace_state(
                    message.account_id,
                    message.order_key,
                )
            )
        except Exception:
            _logger.warning(
                'feishu_stream_visibility_check_failed account_id=%s',
                message.account_id,
                exc_info=True,
            )
            context['_workspace_chat_visible'] = False
            return False
        operation_id = str(
            message.provider_context.get('workspace_operation_id') or ''
        )
        operation_matches = bool(
            not operation_id
            or state.active_operation_id == operation_id
        )
        stream_message_id = str(
            context.get('workspace_message_id') or ''
        )
        reply_card_is_current = bool(
            context.get('workspace_surface') == 'reply'
            and stream_message_id
            and state.message_id == stream_message_id
        )
        assistant_thread_id = state.assistant_selected_thread_id
        assistant_lineage_is_current = bool(
            assistant
            and state.view == 'assistant'
            and state.assistant_mode == 'detail'
            and assistant_thread_id
            and state.assistant_selected_thread_id == assistant_thread_id
        )
        if (
            assistant_lineage_is_current
            and operation_matches
            and stream_message_id
            and state.message_id
            and state.message_id != stream_message_id
        ):
            retarget = getattr(stream, 'retarget_message', None)
            if callable(retarget):
                retarget(state.message_id)
                context['workspace_message_id'] = state.message_id
                stream_message_id = state.message_id
            else:
                context['_workspace_chat_visible'] = False
                return False
        assistant_card_is_current = bool(
            assistant_lineage_is_current
            and (
                not stream_message_id
                or state.message_id == stream_message_id
            )
        )
        visible = bool(
            operation_matches
            and (reply_card_is_current or assistant_card_is_current)
        )
        context['_workspace_chat_visible'] = visible
        return visible

    def render(
        self,
        message: ClaimedOutbound,
    ) -> list[dict[str, Any]]:
        message = self._persist_workspace_result(message)
        parts = self._renderer.render(message)
        sources = [
            str(part.get('source') or '')
            for part in parts
            if part.get('kind') in {'image', 'file'}
            and part.get('source')
        ]
        if not sources:
            return parts
        account = self._credentials.load_runtime_account(
            message.account_id
        )
        try:
            for source in sources:
                self._lazymind.validate_static_asset(
                    source=source,
                    owner_user_id=str(account['owner_user_id']),
                )
        except InvalidStaticAssetError:
            return self._renderer.render(
                replace(
                    message,
                    text=(
                        'LazyMind 没有返回可读取的图片或文件。'
                        '它可能未实际生成，或临时链接已经失效；'
                        '请重新生成。'
                    ),
                    intent_kind='failed',
                    metadata={},
                )
            )
        return parts

    def _persist_workspace_result(
        self,
        message: ClaimedOutbound,
    ) -> ClaimedOutbound:
        context = dict(message.provider_context)
        if not context.get('workspace_state'):
            return message
        surface = context.get('workspace_surface')
        if surface == 'assistant':
            return message
        if surface != 'management':
            return message
        state = FeishuWorkspaceState.from_dict(
            self._store.get_feishu_workspace_state(
                message.account_id,
                message.order_key,
            )
        )
        source_state = FeishuWorkspaceState.from_dict(
            context.get('workspace_state')
        )
        presentations = [
            dict(item)
            for item in (
                message.metadata.get('presentations')
                if isinstance(
                    message.metadata.get('presentations'),
                    list,
                )
                else []
            )
            if isinstance(item, dict)
        ]
        workspace_action = context.get('workspace_action')
        action_kind = (
            str(workspace_action.get('kind') or '')
            if isinstance(workspace_action, dict)
            else ''
        )
        expected_conversation_id = (
            str(workspace_action.get('expected_conversation_id') or '')
            if isinstance(workspace_action, dict)
            else ''
        )
        active_conversation_id = self._store.get_route(
            message.account_id,
            message.order_key,
        ) or ''
        source_conversation_id = str(
            context.get('workspace_conversation_id') or ''
        )
        stale = bool(
            state.revision != source_state.revision
            or state.active_operation_id
            != source_state.active_operation_id
            or state.view != source_state.view
            or (
                action_kind != 'history.switch'
                and active_conversation_id != source_conversation_id
            )
            or (
                expected_conversation_id
                and active_conversation_id != expected_conversation_id
            )
        )
        if stale:
            context['_workspace_delivery_suppressed'] = True
            return replace(
                message,
                text='',
                metadata={**message.metadata, 'presentations': []},
                provider_context=context,
            )
        context['workspace_state'] = state.to_dict()
        context['workspace_conversation_id'] = active_conversation_id
        context['workspace_message_id'] = state.message_id
        context['_workspace_result_complete'] = True
        if not presentations and message.text:
            context['_workspace_notice'] = str(message.text)[:2000]
        return replace(message, provider_context=context)

    def prepare_part(
        self,
        message: ClaimedOutbound,
        part: dict[str, Any],
        *,
        part_index: int,
        saved_state: dict[str, Any],
    ) -> dict[str, Any]:
        return saved_state

    def send_part(
        self,
        message: ClaimedOutbound,
        part: dict[str, Any],
        *,
        part_index: int,
        idempotency_key: str,
        saved_state: dict[str, Any],
    ) -> dict[str, Any] | None:
        chat_id = str(
            message.provider_context.get('chat_id')
            or message.recipient_id
        )
        if not chat_id:
            raise FeishuRuntimeError(
                'Feishu destination chat is missing'
            )
        kind = str(part.get('kind') or '')
        account = self._credentials.load_runtime_account(
            message.account_id
        )
        sender = self._channels.create_sender(
            account['credentials']
        )
        try:
            if kind == 'text':
                sender.send_markdown(
                    chat_id=chat_id,
                    text=str(part.get('text') or ''),
                    idempotency_key=idempotency_key,
                )
                return
            if kind == 'card':
                if saved_state.get('message_id'):
                    return saved_state
                card = part.get('card')
                if not isinstance(card, dict):
                    raise FeishuRuntimeError(
                        'Feishu card payload is invalid'
                    )
                workspace = message.provider_context.get('workspace_state')
                workspace = (
                    dict(workspace) if isinstance(workspace, dict) else {}
                )
                expected_message_id = str(
                    workspace.get('message_id') or ''
                )
                expected_operation_id = str(
                    message.provider_context.get('workspace_operation_id')
                    or ''
                )
                expected_revision = int(workspace.get('revision') or 0)
                assistant_surface = (
                    message.provider_context.get('workspace_surface')
                    == 'assistant'
                )
                workspace_matches = True
                workspace_stale = False
                adopted_revision = expected_revision
                current_workspace = workspace
                if part.get('workspace') is True:
                    current_workspace = self._store.get_feishu_workspace_state(
                        message.account_id,
                        message.order_key,
                    )
                    current_workspace = (
                        dict(current_workspace)
                        if isinstance(current_workspace, dict)
                        else {}
                    )
                    current_message_id = str(
                        current_workspace.get('message_id') or ''
                    )
                    lineage_matches = bool(
                        str(
                            current_workspace.get('active_operation_id')
                            or ''
                        )
                        == expected_operation_id
                        and int(current_workspace.get('revision') or 0)
                        == expected_revision
                    )
                    if (
                        lineage_matches
                        and not assistant_surface
                        and current_message_id
                        and current_message_id != expected_message_id
                    ):
                        expected_message_id = current_message_id
                        message.provider_context['workspace_message_id'] = (
                            current_message_id
                        )
                    workspace_matches = bool(
                        lineage_matches
                        and current_message_id == expected_message_id
                    )
                    if not workspace_matches and not assistant_surface:
                        message.provider_context['workspace_state'] = (
                            current_workspace
                        )
                        return {
                            **saved_state,
                            'message_id': str(
                                current_workspace.get('message_id') or ''
                            ),
                            'workspace_revision': int(
                                current_workspace.get('revision') or 0
                            ),
                            'workspace_operation_id': str(
                                current_workspace.get(
                                    'active_operation_id'
                                ) or ''
                            ),
                            'workspace_stale': True,
                        }
                    message.provider_context['workspace_state'] = (
                        current_workspace
                    )
                target_message_id = str(
                    part.get('replace_message_id')
                    or (
                        expected_message_id
                        if part.get('workspace') is True
                        else ''
                    )
                    or ''
                )
                if target_message_id:
                    try:
                        sender.update_card(
                            message_id=target_message_id,
                            card=card,
                        )
                        message_id = target_message_id
                    except Exception as exc:
                        if not workspace_card_expired(exc):
                            raise
                        message_id = sender.send_card(
                            chat_id=chat_id,
                            card=card,
                            idempotency_key=idempotency_key,
                        )
                else:
                    message_id = sender.send_card(
                        chat_id=chat_id,
                        card=card,
                        idempotency_key=idempotency_key,
                    )
                if part.get('workspace') is True:
                    if message_id != expected_message_id:
                        if not workspace_matches:
                            saved_workspace = current_workspace
                        else:
                            saved_workspace = (
                                self._store.save_feishu_workspace_message(
                                    message.account_id,
                                    message.order_key,
                                    message_id,
                                    expected_operation_id,
                                    expected_message_id,
                                    expected_revision,
                                    advance_revision=assistant_surface,
                                )
                            )
                        adopted_message_id = str(
                            saved_workspace.get('message_id') or ''
                        )
                        adopted_operation_id = str(
                            saved_workspace.get('active_operation_id') or ''
                        )
                        adopted_revision = int(
                            saved_workspace.get('revision') or 0
                        )
                        replay_adopted = (
                            adopted_message_id == message_id
                            and adopted_operation_id == expected_operation_id
                        )
                        if replay_adopted:
                            workspace_stale = (
                                adopted_revision
                                != expected_revision
                                + (1 if assistant_surface else 0)
                            )
                        else:
                            workspace_stale = True
                            _expire_workspace_card(
                                sender,
                                message_id=message_id,
                                workspace=saved_workspace,
                            )
                            message_id = adopted_message_id
                        message.provider_context['workspace_state'] = (
                            saved_workspace
                        )
                        message.provider_context[
                            'workspace_message_id'
                        ] = message_id
                        message.provider_context[
                            'workspace_stream_message_id'
                        ] = message_id
                        if (
                            replay_adopted
                            and not workspace_stale
                            and assistant_surface
                            and target_message_id
                            and target_message_id != message_id
                        ):
                            _expire_workspace_card(
                                sender,
                                message_id=target_message_id,
                                workspace=saved_workspace,
                            )
                    else:
                        saved_workspace = current_workspace
                        adopted_revision = int(
                            saved_workspace.get('revision') or 0
                        )
                return {
                    **saved_state,
                    'message_id': message_id,
                    'workspace_revision': int(
                        (
                            message.provider_context.get('workspace_state') or {}
                        ).get('revision') or adopted_revision
                    ),
                    'workspace_operation_id': str(
                        (
                            message.provider_context.get('workspace_state') or {}
                        ).get('active_operation_id')
                        or expected_operation_id
                    ),
                    'workspace_stale': workspace_stale,
                }
            source = str(part.get('source') or '')
            if kind == 'image':
                if saved_state.get('image_key'):
                    return saved_state
                assistant_surface = (
                    message.provider_context.get('workspace_surface')
                    == 'assistant'
                )
                caption = str(
                    part.get('caption') or part.get('alt') or ''
                )
                image_identity = hashlib.sha256(
                    (source or idempotency_key).encode('utf-8')
                ).hexdigest()
                image_delivery_id = idempotency_key[:512]
                image_state_committed = False
                message_state_maybe_committed = False
                recovered_image_key = ''
                if assistant_surface:
                    prior_workspace_state: dict[str, Any] = {}
                    for prior_index in range(part_index - 1, -1, -1):
                        candidate = message.provider_state.get(
                            str(prior_index)
                        )
                        if isinstance(candidate, dict) and (
                            candidate.get('workspace_stale') is True
                            or candidate.get('message_id')
                        ):
                            prior_workspace_state = dict(candidate)
                            break
                    if prior_workspace_state.get('workspace_stale') is True:
                        return {
                            **saved_state,
                            'workspace_stale': True,
                        }
                    expected_workspace = message.provider_context.get(
                        'workspace_state'
                    )
                    expected_workspace = (
                        dict(expected_workspace)
                        if isinstance(expected_workspace, dict)
                        else {}
                    )
                    expected_message_id = str(
                        prior_workspace_state.get('message_id')
                        or expected_workspace.get('message_id')
                        or ''
                    )
                    expected_operation_id = str(
                        prior_workspace_state.get('workspace_operation_id')
                        or message.provider_context.get(
                            'workspace_operation_id'
                        )
                        or ''
                    )
                    expected_revision = int(
                        prior_workspace_state.get('workspace_revision')
                        if prior_workspace_state.get('workspace_revision')
                        is not None
                        else expected_workspace.get('revision')
                        or 0
                    )
                    current_workspace = self._store.get_feishu_workspace_state(
                        message.account_id,
                        message.order_key,
                    )
                    current_workspace = (
                        dict(current_workspace)
                        if isinstance(current_workspace, dict)
                        else {}
                    )
                    current_message_id = str(
                        current_workspace.get('message_id') or ''
                    )
                    current_operation_id = str(
                        current_workspace.get('active_operation_id') or ''
                    )
                    current_revision = int(
                        current_workspace.get('revision') or 0
                    )
                    normalized_workspace = FeishuWorkspaceState.from_dict(
                        current_workspace
                    )
                    for image in normalized_workspace.images:
                        if (
                            isinstance(image, dict)
                            and str(image.get('delivery_id') or '')
                            == image_delivery_id
                        ):
                            recovered_image_key = str(
                                image.get('image_key') or ''
                            )
                            break
                    workspace_matches = (
                        current_message_id == expected_message_id
                        and current_operation_id == expected_operation_id
                        and current_revision == expected_revision
                    )
                    image_state_committed = (
                        current_message_id == expected_message_id
                        and current_operation_id == expected_operation_id
                        and current_revision == expected_revision + 1
                        and bool(recovered_image_key)
                    )
                    message_state_maybe_committed = (
                        current_operation_id == expected_operation_id
                        and current_revision == expected_revision + 2
                        and bool(recovered_image_key)
                    )
                    if not (
                        workspace_matches
                        or image_state_committed
                        or message_state_maybe_committed
                    ):
                        message.provider_context['workspace_state'] = (
                            current_workspace
                        )
                        return {
                            **saved_state,
                            'workspace_stale': True,
                        }
                    message.provider_context['workspace_state'] = (
                        current_workspace
                    )
                target_message_id = str(
                    expected_message_id
                    if assistant_surface
                    else message.provider_context.get(
                        'workspace_stream_message_id'
                    )
                    or ''
                )
                if not recovered_image_key:
                    try:
                        content = self._lazymind.download_static_image(
                            source=source,
                            owner_user_id=str(account['owner_user_id']),
                        )
                    except InvalidStaticAssetError:
                        self._send_asset_failure(
                            sender=sender,
                            chat_id=chat_id,
                            idempotency_key=idempotency_key,
                            kind='图片',
                        )
                        return
                    if len(content) > _MAX_FEISHU_IMAGE_BYTES:
                        raise FeishuRuntimeError(
                            '飞书图片不能超过 10 MB'
                        )
                if not target_message_id:
                    sender.send_image(
                        chat_id=chat_id,
                        content=content,
                        caption=caption,
                        idempotency_key=idempotency_key,
                    )
                    return {
                        **saved_state,
                        'image_key': str(part.get('source') or idempotency_key),
                    }
                image_key = recovered_image_key
                if assistant_surface:
                    if workspace_matches:
                        if not image_key:
                            image_key = sender.upload_image(content=content)
                            if not image_key:
                                raise FeishuRuntimeError('飞书图片上传失败')
                        workspace = FeishuWorkspaceState.from_dict(
                            message.provider_context.get('workspace_state')
                        )
                        workspace.add_image(
                            image_key=image_key,
                            caption=caption,
                            identity=image_identity,
                            delivery_id=image_delivery_id,
                        )
                        workspace.advance()
                        workspace_payload = workspace.to_dict()
                        if not (
                            self._store.save_feishu_workspace_state_if_revision(
                                message.account_id,
                                message.order_key,
                                workspace_payload,
                                expected_revision,
                            )
                        ):
                            raise FeishuRuntimeError(
                                'Feishu assistant image state changed during delivery'
                            )
                        persisted = workspace_payload
                    else:
                        persisted = current_workspace
                    image_revision = expected_revision + 1
                else:
                    image_key = sender.upload_image(content=content)
                    if not image_key:
                        raise FeishuRuntimeError('飞书图片上传失败')
                    workspace = FeishuWorkspaceState.from_dict(
                        message.provider_context.get('workspace_state')
                    )
                    workspace.add_image(
                        image_key=image_key,
                        caption=caption,
                        identity=image_identity,
                        delivery_id=image_delivery_id,
                    )
                    workspace_payload = workspace.to_dict()
                    try:
                        persisted = self._store.patch_feishu_workspace_state(
                            message.account_id,
                            message.order_key,
                            {'images': workspace_payload['images']},
                            operation_id=str(
                                message.provider_context.get(
                                    'workspace_operation_id'
                                )
                                or ''
                            ),
                        )
                    except Exception:
                        _logger.warning(
                            'feishu_image_state_save_failed account_id=%s',
                            message.account_id,
                            exc_info=True,
                        )
                        persisted = workspace_payload
                    image_revision = int(persisted.get('revision') or 0)
                message.provider_context['workspace_state'] = persisted
                presentations = [
                    dict(item)
                    for item in (
                        message.metadata.get('presentations')
                        if isinstance(
                            message.metadata.get('presentations'),
                            list,
                        )
                        else []
                    )
                    if isinstance(item, dict)
                ]
                language = (
                    'en'
                    if persisted.get('output_language') == 'en'
                    else 'zh'
                )
                final_card = (
                    FeishuWorkspaceRenderer.render(
                        provider_context=message.provider_context,
                        presentations=presentations,
                    )
                    if assistant_surface
                    else FeishuReplyRenderer.render(
                        provider_context=message.provider_context,
                        text=media_free_feishu_text(message.text),
                        status=(
                            '✅ **Answer complete**'
                            if language == 'en'
                            else '✅ **回答完成**'
                        ),
                    )
                )
                if assistant_surface:
                    replacement_message_id = sender.send_card(
                        chat_id=chat_id,
                        card=final_card,
                        idempotency_key=idempotency_key,
                    )
                    if message_state_maybe_committed:
                        saved_workspace = current_workspace
                    else:
                        saved_workspace = (
                            self._store.save_feishu_workspace_message(
                                message.account_id,
                                message.order_key,
                                replacement_message_id,
                                expected_operation_id,
                                target_message_id,
                                image_revision,
                            )
                        )
                    adopted_message_id = str(
                        saved_workspace.get('message_id') or ''
                    )
                    adopted_operation_id = str(
                        saved_workspace.get('active_operation_id') or ''
                    )
                    adopted_revision = int(
                        saved_workspace.get('revision') or 0
                    )
                    replay_adopted = (
                        adopted_message_id == replacement_message_id
                        and adopted_operation_id == expected_operation_id
                    )
                    workspace_stale = not (
                        replay_adopted
                        and adopted_revision == expected_revision + 2
                    )
                    message.provider_context['workspace_state'] = saved_workspace
                    if not replay_adopted:
                        _expire_workspace_card(
                            sender,
                            message_id=replacement_message_id,
                            workspace=saved_workspace,
                        )
                    else:
                        _expire_workspace_card(
                            sender,
                            message_id=target_message_id,
                            workspace=saved_workspace,
                        )
                    target_message_id = adopted_message_id
                    message.provider_context['workspace_message_id'] = (
                        adopted_message_id
                    )
                    message.provider_context[
                        'workspace_stream_message_id'
                    ] = adopted_message_id
                else:
                    sender.update_card(
                        message_id=target_message_id,
                        card=final_card,
                    )
                return {
                    **saved_state,
                    'image_key': image_key,
                    'message_id': target_message_id,
                    'workspace_revision': int(
                        (
                            message.provider_context.get('workspace_state')
                            or {}
                        ).get('revision')
                        or 0
                    ),
                    'workspace_operation_id': (
                        str(
                            (
                                message.provider_context.get('workspace_state')
                                or {}
                            ).get('active_operation_id')
                            or expected_operation_id
                        )
                        if assistant_surface
                        else ''
                    ),
                    'workspace_stale': (
                        workspace_stale if assistant_surface else False
                    ),
                }
            if kind == 'file':
                artifact_index = str(
                    part.get('artifact_index') or ''
                )
                if artifact_index:
                    content = inline_artifact_bytes(
                        message.metadata,
                        artifact_index,
                    )
                    if content is None:
                        raise FeishuRuntimeError(
                            'LazyMind inline artifact is invalid'
                        )
                else:
                    try:
                        content = self._lazymind.download_static_file(
                            source=source,
                            owner_user_id=str(account['owner_user_id']),
                        )
                    except InvalidStaticAssetError:
                        self._send_asset_failure(
                            sender=sender,
                            chat_id=chat_id,
                            idempotency_key=idempotency_key,
                            kind='文件',
                        )
                        return
                if len(content) > _MAX_FEISHU_FILE_BYTES:
                    raise FeishuRuntimeError(
                        '飞书文件不能超过 30 MB'
                    )
                sender.send_file(
                    chat_id=chat_id,
                    content=content,
                    filename=str(
                        part.get('filename')
                        or 'lazymind-output'
                    ),
                    idempotency_key=idempotency_key,
                )
                return
            raise FeishuRuntimeError(
                'Unsupported Feishu outbound part'
            )
        finally:
            sender.close()

    @staticmethod
    def _send_asset_failure(
        *,
        sender,
        chat_id: str,
        idempotency_key: str,
        kind: str,
    ) -> None:
        sender.send_markdown(
            chat_id=chat_id,
            text=(
                f'⚠️ LazyMind 没有返回可读取的{kind}文件。'
                '它可能未实际生成，或临时链接已经失效；请重新生成。'
            ),
            idempotency_key=idempotency_key,
        )

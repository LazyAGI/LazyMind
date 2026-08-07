import logging
import time
from dataclasses import replace
from typing import Any

from channel_gateway.common.domain.channel import (
    ClaimedInbound,
    ClaimedOutbound,
)
from channel_gateway.common.domain.chat import CoreStreamUpdate
from channel_gateway.common.domain.outbound import (
    OutboundRenderer,
    inline_artifact_bytes,
)
from channel_gateway.common.errors import InvalidStaticAssetError
from channel_gateway.common.ports.core import StaticAssetClient
from channel_gateway.common.ports.providers import RuntimeCredentialStore
from channel_gateway.common.ports.messaging import ReplyStream
from channel_gateway.feishu.domain import FeishuRuntimeError
from channel_gateway.feishu.assistant import codex_turns_for_card
from channel_gateway.feishu.ports import (
    FeishuOutboundFactory,
    FeishuWorkspaceRepository,
)
from channel_gateway.feishu.presentation import (
    FeishuPresentationRenderer,
    FeishuReplyRenderer,
    media_free_feishu_text,
    presentable_feishu_text,
    streamable_feishu_text,
    streaming_reply_card,
)
from channel_gateway.feishu.workspace import (
    FeishuWorkspaceRenderer,
    FeishuWorkspaceState,
)


_MAX_FEISHU_IMAGE_BYTES = 10 * 1024 * 1024
_MAX_FEISHU_FILE_BYTES = 30 * 1024 * 1024
_STREAM_STATE_CHECK_SECONDS = 1.0
_STREAM_STATE_SAVE_SECONDS = 10.0


_logger = logging.getLogger(__name__)


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
        management: bool,
        core: Any = None,
        owner_user_id: str = '',
    ):
        self._stream = stream
        self._sender = sender
        self._provider_context = provider_context
        self._store = store
        self._account_id = account_id
        self._address_hash = address_hash
        self._management = management
        self._core = core
        self._owner_user_id = owner_user_id
        self._conversation_id = ''
        self._last_state_check = 0.0
        self._last_state_save = 0.0
        self._state_access_failed = False
        self._cancelled = False
        self._saved_history_id = ''

    def update(self, snapshot: CoreStreamUpdate) -> None:
        now = time.monotonic()
        if self._is_cancelled(now):
            return
        if (
            snapshot.conversation_id
            and snapshot.conversation_id != self._conversation_id
        ):
            self._activate_conversation(snapshot.conversation_id)
        language = self._language()
        status = (
            '✍️ **Generating an answer**'
            if language == 'en' and snapshot.answer
            else '⏳ **Understanding your question**'
            if language == 'en'
            else '✍️ **正在生成回答**'
            if snapshot.answer
            else '⏳ **正在理解你的问题**'
        )
        if snapshot.thinking_seconds is not None:
            unit = 's' if language == 'en' else '秒'
            status += f' · {snapshot.thinking_seconds} {unit}'
        patch = {
            'run_status': 'running',
            'chat_text': streamable_feishu_text(snapshot.answer),
            'chat_status': status,
            'chat_thinking': presentable_feishu_text(snapshot.thinking),
            'generation_history_id': snapshot.history_id,
        }
        if snapshot.external_event:
            self._apply_external_thread_rebind(snapshot.external_event)
            patch.update(_external_event_workspace_patch(snapshot.external_event))
        self._update_local_workspace(patch)
        self._stream.update(snapshot)
        self._save_progress(patch, snapshot.history_id, now)

    def _apply_external_thread_rebind(self, event: dict[str, Any]) -> None:
        event_type = str(
            event.get('type') or event.get('event_type') or ''
        )
        if event_type != 'thread_forked':
            return
        thread_id = str(event.get('thread_id') or '').strip()
        binding = self._provider_context.get('external_agent_binding')
        if thread_id and isinstance(binding, dict):
            binding['provider_thread_id'] = thread_id

    def _is_cancelled(self, now: float) -> bool:
        if self._cancelled:
            return True
        if now - self._last_state_check < _STREAM_STATE_CHECK_SECONDS:
            return False
        self._last_state_check = now
        try:
            state = FeishuWorkspaceState.from_dict(
                self._store.get_feishu_workspace_state(
                    self._account_id,
                    self._address_hash,
                )
            )
        except Exception:
            self._log_state_failure('read')
            return False
        self._log_state_recovered()
        self._cancelled = state.run_status == 'cancelled'
        return self._cancelled

    def _activate_conversation(self, conversation_id: str) -> None:
        if isinstance(
            self._provider_context.get('external_agent_binding'),
            dict,
        ):
            self._conversation_id = conversation_id
            return
        try:
            self._store.activate_conversation(
                self._account_id,
                self._address_hash,
                conversation_id,
                consume_pending_turn=True,
            )
        except Exception:
            self._log_state_failure('activate')
            return
        self._log_state_recovered()
        self._provider_context['workspace_conversation_id'] = conversation_id
        self._conversation_id = conversation_id

    def _save_progress(
        self,
        patch: dict[str, Any],
        history_id: str,
        now: float,
    ) -> None:
        history_changed = bool(
            history_id and history_id != self._saved_history_id
        )
        if (
            not history_changed
            and now - self._last_state_save < _STREAM_STATE_SAVE_SECONDS
        ):
            return
        try:
            self._patch_workspace(patch)
        except Exception:
            self._log_state_failure('save')
            return
        self._log_state_recovered()
        self._last_state_save = now
        if history_id:
            self._saved_history_id = history_id

    def _update_local_workspace(self, patch: dict[str, Any]) -> None:
        workspace = self._provider_context.get('workspace_state')
        if isinstance(workspace, dict):
            workspace.update(patch)

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
            operation_id = str(
                self._provider_context.get('workspace_operation_id') or ''
            )
            state = FeishuWorkspaceState.from_dict(
                self._provider_context.get('workspace_state')
            )
            try:
                state = FeishuWorkspaceState.from_dict(
                    self._store.get_feishu_workspace_state(
                        self._account_id,
                        self._address_hash,
                    )
                )
            except Exception:
                self._log_state_failure('finish_read')
            if state.run_status == 'cancelled':
                self._stream.abort()
                return True
            if not state.restore_last_view and state.view != 'assistant':
                state.view = 'chat'
            state.mark_result_ready(operation_id)
            final_patch = {
                'view': state.view,
                'run_status': 'completed',
                'chat_text': streamable_feishu_text(final_text),
                'chat_status': (
                    '✅ **Answer complete**'
                    if self._language() == 'en'
                    else '✅ **回答完成**'
                ),
                'unread_results': state.unread_results,
                'result_notice_operation_id': (
                    state.result_notice_operation_id
                ),
                'generation_history_id': '',
            }
            self._update_local_workspace(final_patch)
            try:
                self._patch_workspace(final_patch)
            except Exception:
                self._log_state_failure('finish_save')
            self._refresh_external_history()
            streamed = self._stream.finish(final_text)
            message_id = str(
                getattr(self._stream, 'message_id', '') or ''
            )
            if message_id:
                self._provider_context['workspace_stream_message_id'] = (
                    message_id
                )
            if message_id and self._management:
                self._provider_context['workspace_message_id'] = message_id
                workspace = self._provider_context.get('workspace_state')
                if isinstance(workspace, dict):
                    workspace['message_id'] = message_id
                    try:
                        self._store.save_feishu_workspace_message(
                            self._account_id,
                            self._address_hash,
                            message_id,
                        )
                    except Exception:
                        self._log_state_failure('message_save')
            return streamed
        finally:
            self._sender.close()

    def _refresh_external_history(self) -> None:
        binding = self._provider_context.get('external_agent_binding')
        read_thread = getattr(self._core, 'read_external_thread', None)
        if (
            not isinstance(binding, dict)
            or not callable(read_thread)
            or not self._owner_user_id
        ):
            return
        thread_id = str(binding.get('provider_thread_id') or '')
        provider = str(binding.get('provider') or 'codex')
        if not thread_id:
            return
        request_id = str(
            self._provider_context.get('workspace_operation_id')
            or 'feishu_external'
        )
        try:
            page = read_thread(
                owner_user_id=self._owner_user_id,
                request_id=f'{request_id}_history_probe',
                provider=provider,
                thread_id=thread_id,
                offset=0,
                limit=1,
            )
            total = int(page.get('total_turns') or 0)
            latest_offset = max(0, total - 1)
            if latest_offset:
                page = read_thread(
                    owner_user_id=self._owner_user_id,
                    request_id=f'{request_id}_history_latest',
                    provider=provider,
                    thread_id=thread_id,
                    offset=latest_offset,
                    limit=1,
                )
            thread = (
                dict(page.get('thread'))
                if isinstance(page.get('thread'), dict)
                else {}
            )
            patch = {
                'assistant_turns': codex_turns_for_card(
                    page.get('turns')
                ),
                'assistant_turns_offset': int(
                    page.get('offset') or latest_offset
                ),
                'assistant_turns_total': total,
                'assistant_thread_updated_at': str(
                    thread.get('updatedAt')
                    or thread.get('updated_at')
                    or ''
                )[:100],
                'assistant_thread_available': bool(
                    thread.get('available', True)
                ),
                'user_text': '',
                'chat_text': '',
                'chat_thinking': '',
            }
            self._update_local_workspace(patch)
            self._patch_workspace(patch)
        except Exception:
            _logger.warning(
                'feishu_external_history_refresh_failed thread_id=%s',
                thread_id,
                exc_info=True,
            )

    def _patch_workspace(self, patch: dict[str, Any]) -> dict[str, Any]:
        workspace = self._store.patch_feishu_workspace_state(
            self._account_id,
            self._address_hash,
            patch,
            operation_id=str(
                self._provider_context.get('workspace_operation_id') or ''
            ),
        )
        self._provider_context['workspace_state'] = workspace
        return workspace

    def _language(self) -> str:
        workspace = self._provider_context.get('workspace_state')
        if not isinstance(workspace, dict):
            return 'zh'
        return 'en' if workspace.get('output_language') == 'en' else 'zh'

    def abort(self) -> None:
        try:
            self._stream.abort()
        finally:
            self._sender.close()


def _external_event_workspace_patch(event: dict[str, Any]) -> dict[str, Any]:
    """Translate external-agent stream events into the existing card state."""
    event_type = str(event.get('type') or event.get('event_type') or '')
    payload = event.get('payload')
    data = dict(payload) if isinstance(payload, dict) else dict(event)
    status = str(data.get('status') or data.get('summary') or '')[:300]
    message = str(
        data.get('text') or data.get('delta') or data.get('message') or ''
    )
    cumulative_answer = str(data.get('message') or '')
    patch: dict[str, Any] = {}
    if event_type in {'progress', 'agent_message_delta', 'run_attached', 'turn_started'}:
        patch['chat_status'] = status or '⏳ **Codex 正在处理**'
        if event_type == 'agent_message_delta' and cumulative_answer:
            patch['chat_text'] = cumulative_answer[:16000]
        elif event_type != 'agent_message_delta' and message:
            patch['chat_thinking'] = message[:4000]
        if event_type in {'run_attached', 'turn_started', 'progress', 'agent_message_delta'}:
            patch['run_status'] = 'running'
    elif event_type == 'thread_forked':
        thread_id = str(data.get('thread_id') or '').strip()
        patch.update({
            'run_status': 'running',
            'chat_status': '🔀 **已创建 Codex 原生续接会话**',
            'chat_thinking': message[:4000],
            'assistant_managed': True,
        })
        if thread_id:
            patch['assistant_selected_thread_id'] = thread_id
    elif event_type == 'request_required':
        request_id = str(
            data.get('request_id') or data.get('id') or ''
        )
        if request_id:
            patch['assistant_pending_request'] = {
                'request_id': request_id,
                'kind': str(
                    data.get('request_kind') or data.get('kind') or 'request'
                ),
                'summary': str(data.get('summary') or message),
                'payload': data,
            }
        patch['run_status'] = 'waiting_for_input'
        patch['chat_status'] = '💬 **等待你的确认或输入**'
    elif event_type in {'turn_completed', 'turn_failed', 'turn_interrupted'}:
        patch['assistant_pending_request'] = None
        patch['run_status'] = (
            'failed' if event_type == 'turn_failed'
            else 'cancelled' if event_type == 'turn_interrupted'
            else 'completed'
        )
        if message:
            patch['chat_text'] = message[:16000]
        patch['chat_status'] = {
            'turn_completed': '✅ **Codex 已完成**',
            'turn_failed': '⚠️ **Codex 执行失败**',
            'turn_interrupted': '⏹️ **Codex 已取消**',
        }[event_type]
    elif event_type == 'artifact_available':
        patch['chat_status'] = status or '📎 **Codex 生成了产物**'
    return patch


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
        management = bool(workspace_message_id) or (
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
                text='',
                presentations=[],
                streaming=True,
            )
            if assistant
            else streaming_reply_card(stream_context)
        )
        try:
            stream = sender.start_card_stream(
                chat_id=chat_id,
                initial_card=initial_card,
                message_id=workspace_message_id if management else '',
                should_render=(
                    (lambda: self._workspace_chat_is_visible(message))
                    if management
                    else None
                ),
                collapse_process=bool(
                    (
                        message.provider_context.get('workspace_state')
                        or {}
                    ).get('auto_collapse_process', True)
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
        except Exception:
            sender.close()
            raise
        return _ManagedReplyStream(
            stream,
            sender,
            message.provider_context,
            self._store,
            message.account_id,
            message.order_key,
            management=management,
            core=self._lazymind,
            owner_user_id=message.owner_user_id,
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
        if snapshot.answer and not (
            finished and workspace.assistant_turns
        ):
            workspace.chat_text = streamable_feishu_text(snapshot.answer)
        if snapshot.thinking:
            workspace.chat_thinking = presentable_feishu_text(
                snapshot.thinking
            )
        if aborted:
            workspace.run_status = 'cancelled'
            workspace.chat_status = '⏹️ **Codex 已取消**'
        elif finished:
            workspace.run_status = 'completed'
            workspace.chat_status = '✅ **Codex 已完成**'
        elif workspace.run_status not in {'waiting_for_input', 'completed'}:
            workspace.run_status = 'running'
        streaming = workspace.run_status == 'running'
        message.provider_context['workspace_state'] = workspace.to_dict()
        card = FeishuWorkspaceRenderer.render(
            provider_context={
                **message.provider_context,
                'chat_id': chat_id,
                'workspace_state': workspace.to_dict(),
            },
            text='',
            presentations=[],
            streaming=streaming,
        )
        if not streaming:
            card['config'].pop('streaming_config', None)
        return card

    def _workspace_chat_is_visible(self, message: ClaimedInbound) -> bool:
        context = message.provider_context
        now = time.monotonic()
        checked_at = float(
            context.get('_workspace_visibility_checked_at') or 0.0
        )
        if now - checked_at < _STREAM_STATE_CHECK_SECONDS:
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
            return bool(context.get('_workspace_chat_visible', True))
        if state.run_status == 'cancelled':
            context['workspace_state'] = state.to_dict()
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
        visible = bool(
            operation_matches
            and (
                reply_card_is_current
                or state.view in {'chat', 'assistant'}
            )
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
        state = FeishuWorkspaceState.from_dict(
            self._store.get_feishu_workspace_state(
                message.account_id,
                message.order_key,
            )
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
        command = context.get('command_action')
        command_name = (
            str(command.get('command') or '')
            if isinstance(command, dict)
            else str(message.intent_kind or '')
        )
        data_view = {
            'capability.list': 'capabilities',
            'conversation.list': 'conversations',
            'conversation.settings': 'settings',
            'conversation.settings.update': 'settings',
        }.get(command_name)
        workspace_action = context.get('workspace_action')
        action_kind = (
            str(workspace_action.get('kind') or '')
            if isinstance(workspace_action, dict)
            else ''
        )
        if (
            command_name == 'conversation.settings.update'
            and action_kind == 'setting.update'
            and workspace_action.get('view') == 'capabilities'
        ):
            data_view = 'capabilities'
        conversation = next(
            (
                item
                for item in presentations
                if item.get('kind') == 'conversation'
                and item.get('state') in {'switched', 'history'}
            ),
            None,
        )
        if conversation is not None:
            conversation_state = str(conversation.get('state') or '')
            if conversation_state == 'switched':
                state.replace_chat_history(
                    title=str(conversation.get('title') or ''),
                    turns=conversation.get('turns'),
                    reached_start=bool(
                        conversation.get('reached_start', False)
                    ),
                )
            else:
                if not state.conversation_title:
                    state.conversation_title = str(
                        conversation.get('title') or ''
                    )[:200]
                state.prepend_chat_history(
                    turns=conversation.get('turns'),
                    reached_start=bool(
                        conversation.get('reached_start', False)
                    ),
                )
            state_payload = state.to_dict()
            persisted = self._store.patch_feishu_workspace_state(
                message.account_id,
                message.order_key,
                {
                    key: state_payload[key]
                    for key in (
                        'view',
                        'run_status',
                        'user_text',
                        'chat_text',
                        'chat_status',
                        'chat_thinking',
                        'conversation_title',
                        'conversation_switch_index',
                        'conversation_switch_status',
                        'chat_history',
                        'chat_history_page',
                        'chat_history_reached_start',
                        'chat_presentations',
                        'pending_ask_id',
                        'images',
                    )
                },
            )
            active_conversation_id = self._store.get_route(
                message.account_id,
                message.order_key,
            )
            pending_turn = self._store.get_pending_turn(
                message.account_id,
                message.order_key,
            )
            resources = FeishuWorkspaceState.from_dict(
                persisted
            ).effective_resources(
                active_conversation_id,
                pending_turn,
            )
            context['workspace_state'] = persisted
            context['workspace_conversation_id'] = active_conversation_id
            context['workspace_resources'] = [
                item.to_dict() for item in resources
            ]
            context['workspace_mentions'] = [
                item.to_mention() for item in resources
            ]
            context['workspace_message_id'] = str(
                persisted.get('message_id')
                or context.get('workspace_message_id')
                or ''
            )
            return replace(
                message,
                text='',
                provider_context=context,
            )
        if data_view:
            view = (
                state.view
                if action_kind and state.view in {
                    'capabilities',
                    'conversations',
                    'assistant',
                    'settings',
                    'context',
                }
                else data_view
            )
            state.view = view
            if data_view == 'conversations':
                state.conversation_switch_index = 0
                state.conversation_switch_status = ''
            state.cache_view(
                data_view,
                text=presentable_feishu_text(message.text),
                presentations=presentations,
                merge=(
                    data_view == 'capabilities'
                    and command_name.startswith('conversation.settings')
                ),
            )
            persisted = self._store.patch_feishu_workspace_state(
                message.account_id,
                message.order_key,
                {
                    'view': view,
                    'view_snapshots': state.to_dict()['view_snapshots'],
                    'conversation_switch_index': (
                        state.conversation_switch_index
                    ),
                    'conversation_switch_status': (
                        state.conversation_switch_status
                    ),
                },
            )
        else:
            pending_ask_id = next(
                (
                    str(item.get('ask_id') or '')
                    for item in presentations
                    if item.get('kind') == 'ask'
                ),
                '',
            )
            failed = message.intent_kind == 'failed'
            operation_id = str(
                context.get('workspace_operation_id') or ''
            )
            if not state.restore_last_view and state.view != 'assistant':
                state.view = 'chat'
            state.mark_result_ready(operation_id)
            active_conversation_id = self._store.get_route(
                message.account_id,
                message.order_key,
            ) or ''
            if active_conversation_id and state.pending_conversation_resources:
                state.complete_new_session(
                    active_conversation_id
                )
            persisted = self._store.patch_feishu_workspace_state(
                message.account_id,
                message.order_key,
                {
                    'view': state.view,
                    'run_status': (
                        'failed'
                        if failed
                        else 'waiting_for_input'
                        if pending_ask_id
                        else 'completed'
                    ),
                    'chat_text': streamable_feishu_text(message.text),
                    'chat_status': (
                        '⚠️ **Answer failed**'
                        if failed and state.output_language == 'en'
                        else '⚠️ **回答失败**'
                        if failed
                        else '💬 **等待补充信息**'
                        if pending_ask_id
                        else '✅ **回答完成**'
                    ),
                    'chat_presentations': presentations,
                    'pending_ask_id': pending_ask_id,
                    'generation_history_id': '',
                    'unread_results': state.unread_results,
                    'result_notice_operation_id': (
                        state.result_notice_operation_id
                    ),
                    'conversations': state.to_dict()['conversations'],
                    'pending_conversation_resources': (
                        state.to_dict()['pending_conversation_resources']
                    ),
                },
                operation_id=operation_id,
            )
        context['workspace_state'] = persisted
        context['workspace_message_id'] = str(
            persisted.get('message_id')
            or context.get('workspace_message_id')
            or ''
        )
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
                target_message_id = str(
                    part.get('replace_message_id')
                    or (
                        message.provider_context.get(
                            'workspace_message_id'
                        )
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
                        if not _workspace_card_expired(exc):
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
                    workspace = message.provider_context.get(
                        'workspace_state'
                    )
                    if isinstance(workspace, dict):
                        workspace['message_id'] = message_id
                        self._store.save_feishu_workspace_message(
                            message.account_id,
                            message.order_key,
                            message_id,
                        )
                return {
                    **saved_state,
                    'message_id': message_id,
                }
            source = str(part.get('source') or '')
            if kind == 'image':
                if saved_state.get('image_key'):
                    return saved_state
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
                target_message_id = str(
                    message.provider_context.get(
                        'workspace_stream_message_id'
                    )
                    or ''
                )
                caption = str(
                    part.get('caption') or part.get('alt') or ''
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
                image_key = sender.upload_image(content=content)
                if not image_key:
                    raise FeishuRuntimeError('飞书图片上传失败')
                workspace = FeishuWorkspaceState.from_dict(
                    message.provider_context.get('workspace_state')
                )
                workspace.add_image(
                    image_key=image_key,
                    caption=caption,
                    identity=(
                        str(part.get('source') or '')
                        or idempotency_key
                    ),
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
                sender.update_card(
                    message_id=target_message_id,
                    card=FeishuReplyRenderer.render(
                        provider_context=message.provider_context,
                        text=media_free_feishu_text(message.text),
                        presentations=presentations,
                        status=(
                            '✅ **Answer complete**'
                            if language == 'en'
                            else '✅ **回答完成**'
                        ),
                        thinking=(
                            'Analysis and processing complete.'
                            if language == 'en'
                            else '分析与处理已完成。'
                        ),
                    ),
                )
                return {
                    **saved_state,
                    'image_key': image_key,
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


def _workspace_card_expired(exc: Exception) -> bool:
    message = str(exc).casefold()
    return any(
        marker in message
        for marker in (
            '200740',
            '200750',
            'card entity does not exist',
            'card entity has expired',
        )
    )

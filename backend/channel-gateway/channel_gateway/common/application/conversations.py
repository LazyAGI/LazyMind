from __future__ import annotations

import datetime as dt
import re
from dataclasses import dataclass
from collections.abc import Callable
from typing import Any, Sequence

from channel_gateway.common.application.capabilities import (
    ActionMessage,
    CapabilityActions,
    ResolvedChanges,
)
from channel_gateway.common.domain.commands import (
    AssistantProvider,
    CommandEnvelope,
    ConversationSwitchCommand,
    PreparedConversationTarget,
    RESOLVED_CONVERSATION_TARGET_KEY,
    ResourceChange,
    SelectionContinuation,
)
from channel_gateway.common.errors import LazyMindError, LazyMindHTTPError
from channel_gateway.common.domain.chat import (
    ChatOptions,
    CoreStreamUpdate,
    CoreTurnResult,
)
from channel_gateway.common.domain.channel import sanitize_channel_text
from channel_gateway.common.domain.outbound import (
    ConversationPresentation,
    ConversationTurnPresentation,
    ReplyPresentation,
)
from channel_gateway.common.ports.core import ConversationClient
from channel_gateway.common.ports.repository import NavigationRepository


_LIST_LIMIT = 10
_CORE_PAGE_SIZE = 100
_SNAPSHOT_TTL = dt.timedelta(minutes=10)
_HISTORY_PAGE_SIZE = 3
_QUERY_PREVIEW_LIMIT = 120
_ANSWER_PREVIEW_LIMIT = 300
_CHINA_TIMEZONE = dt.timezone(dt.timedelta(hours=8))


@dataclass(frozen=True, slots=True)
class ConversationResult:
    text: str
    turn: CoreTurnResult | None = None
    presentations: tuple[ReplyPresentation, ...] = ()


class ConversationActions:
    """Owns conversation operations and all user-facing conversation formatting."""

    def __init__(
        self,
        *,
        store: NavigationRepository,
        client: ConversationClient,
        capabilities: CapabilityActions,
    ):
        self._store = store
        self._client = client
        self._capabilities = capabilities

    def chat(
        self,
        *,
        account_id: str,
        external_address_hash: str,
        owner_user_id: str,
        request_id: str,
        message: str,
        changes: list[ResourceChange],
        resolved_changes: ResolvedChanges | None = None,
        source_command: CommandEnvelope | None = None,
        source_messages: Sequence[str] = (),
        catalog: dict[str, Any],
        activate_route: bool = True,
        ask_answers_structured: dict[str, Any] | None = None,
        inputs: Sequence[dict[str, str]] = (),
        mentions: Sequence[dict[str, str]] = (),
        thinking_depth: str | None = None,
        conversation_id_override: str | None = None,
        on_stream: Callable[[CoreStreamUpdate], None] | None = None,
        recover_missing_route: bool = False,
    ) -> ConversationResult:
        conversation_id = (
            conversation_id_override
            if conversation_id_override is not None
            else self._store.get_route(account_id, external_address_hash)
        )
        if recover_missing_route and conversation_id:
            try:
                detail = self._client.get_conversation_detail(
                    owner_user_id=owner_user_id,
                    conversation_id=conversation_id,
                    request_id=f'{request_id}_route',
                )
            except LazyMindHTTPError as exc:
                if exc.status_code != 404:
                    raise
                self._store.begin_new_conversation(
                    account_id,
                    external_address_hash,
                )
                conversation_id = None
            else:
                if str(detail.get('chat_executor') or 'lazymind') != 'lazymind':
                    self._store.begin_new_conversation(
                        account_id,
                        external_address_hash,
                    )
                    conversation_id = None
        state = self._store.get_navigation_state(account_id, external_address_hash) or {}
        explicit_new = state.get('mode') == 'new_pending'
        resolved = (
            resolved_changes
            if resolved_changes is not None
            else self._capabilities.resolve_changes(
                changes,
                catalog,
                account_id=account_id,
                external_address_hash=external_address_hash,
                source_command=source_command,
                source_messages=source_messages,
            )
        )
        if conversation_id:
            self._capabilities.apply_persistent_changes(
                resolved=resolved,
                conversation_id=conversation_id,
                owner_user_id=owner_user_id,
                request_id=request_id,
            )
            needs_base_datasets = any(
                change.scope == 'turn'
                and change.resource_type == 'knowledge_base'
                and change.operation != 'use'
                for change, _ in resolved
            )
            base_dataset_ids = (
                self._capabilities.conversation_dataset_ids(
                    owner_user_id=owner_user_id,
                    conversation_id=conversation_id,
                    request_id=f'{request_id}_resources',
                )
                if needs_base_datasets
                else []
            )
            options = self._capabilities.turn_options(resolved, base_dataset_ids)
            options = ChatOptions.from_dict(
                self._store.get_pending_turn(
                    account_id,
                    external_address_hash,
                )
            ).merged(options)
        else:
            default_ids = self._capabilities.default_dataset_ids(catalog)
            options = self._capabilities.new_conversation_options([], default_ids)
            options = options.merged(
                ChatOptions.from_dict(
                    self._store.get_pending_turn(
                        account_id,
                        external_address_hash,
                    )
                ),
            )
            if explicit_new:
                options = options.merged(
                    ChatOptions.from_dict(
                        self._store.get_new_conversation_draft(
                            account_id,
                            external_address_hash,
                        )
                    ),
                )
            if resolved:
                options = options.merged(
                    self._capabilities.new_conversation_options(
                        resolved,
                        default_ids,
                    ),
                )
            self._capabilities.apply_global_changes(
                resolved=resolved,
                owner_user_id=owner_user_id,
                request_id=request_id,
            )

        try:
            if ask_answers_structured is not None:
                self._validate_pending_ask(
                    owner_user_id=owner_user_id,
                    conversation_id=conversation_id,
                    request_id=request_id,
                    structured=ask_answers_structured,
                )
            options.inputs.extend(dict(item) for item in inputs)
            options.ask_answers_structured = ask_answers_structured
            options.mentions.extend(dict(item) for item in mentions)
            options.thinking_depth = (
                thinking_depth
                if thinking_depth in {'low', 'medium', 'high', 'max'}
                else None
            )
            turn = self._client.chat(
                owner_user_id=owner_user_id,
                text=message,
                conversation_id=conversation_id,
                request_id=request_id,
                options=options,
                on_stream=on_stream,
            )
        except LazyMindHTTPError as exc:
            if conversation_id and (
                exc.status_code == 404
                or (
                    exc.status_code == 409
                    and '2002115' in exc.message
                )
            ):
                self._store.begin_new_conversation(
                    account_id,
                    external_address_hash,
                )
                if recover_missing_route:
                    return self.chat(
                        account_id=account_id,
                        external_address_hash=external_address_hash,
                        owner_user_id=owner_user_id,
                        request_id=f'{request_id}_recreated',
                        message=message,
                        changes=[],
                        resolved_changes=[],
                        source_command=source_command,
                        source_messages=source_messages,
                        catalog={},
                        activate_route=activate_route,
                        inputs=inputs,
                        thinking_depth=thinking_depth,
                        conversation_id_override='',
                        on_stream=on_stream,
                        recover_missing_route=True,
                    )
                raise ActionMessage(
                    '当前会话已经不存在或已在回收站，已进入新会话状态；'
                    '刚才的任务没有发送，请再发一次。'
                ) from exc
            if (
                exc.status_code == 409
                and '2001310' in exc.message
            ):
                raise ActionMessage(
                    '当前 Workflow 仍在运行或等待操作，暂时不能启动另一个 Workflow。'
                    '请等待当前任务结束后直接发送新任务，无需新建会话。'
                ) from exc
            raise
        if activate_route:
            self._store.activate_conversation(
                account_id,
                external_address_hash,
                turn.conversation_id,
                consume_pending_turn=True,
            )
        answer = turn.answer or self._event_fallback(turn)
        if explicit_new:
            return ConversationResult(
                text=(
                    '── 已创建并切换到新会话 ──\n'
                    f'首条消息：'
                    f'{self._truncate(message, _QUERY_PREVIEW_LIMIT)}\n\n'
                    f'{answer}'
                ),
                turn=turn,
                presentations=(
                    ConversationPresentation(
                        kind='conversation',
                        state='new',
                        title='新会话',
                    ),
                ),
            )
        return ConversationResult(
            text=answer,
            turn=turn,
        )

    def _validate_pending_ask(
        self,
        *,
        owner_user_id: str,
        conversation_id: str,
        request_id: str,
        structured: dict[str, Any],
    ) -> None:
        ask_id = str(structured.get('ask_id') or '')
        if not conversation_id or not ask_id:
            raise ActionMessage('这张问题卡已经失效，请在当前会话中重新回答。')
        history = self._client.get_conversation_history(
            owner_user_id=owner_user_id,
            conversation_id=conversation_id,
            request_id=f'{request_id}_ask_check',
            page_size=10,
        )
        items = history.get('history')
        pending_ask_id = ''
        for item in items if isinstance(items, list) else []:
            if not isinstance(item, dict) or item.get('ask_answered'):
                continue
            pending = item.get('ask_pending')
            if isinstance(pending, dict):
                pending_ask_id = str(pending.get('ask_id') or '')
                if pending_ask_id:
                    break
        if pending_ask_id != ask_id:
            raise ActionMessage(
                '这张问题卡已过期，没有提交答案。请回答当前最新的问题。'
            )

    def new(
        self,
        *,
        account_id: str,
        external_address_hash: str,
        owner_user_id: str,
        request_id: str,
        message: str,
        changes: list[ResourceChange],
        source_command: CommandEnvelope,
        source_messages: Sequence[str],
        catalog: dict[str, Any],
        on_stream: Callable[[CoreStreamUpdate], None] | None = None,
    ) -> str | ConversationResult:
        resolved = self._capabilities.resolve_changes(
            changes,
            catalog,
            account_id=account_id,
            external_address_hash=external_address_hash,
            source_command=source_command,
            source_messages=source_messages,
        )
        self._capabilities.apply_global_changes(
            resolved=resolved,
            owner_user_id=owner_user_id,
            request_id=request_id,
        )
        draft = self._capabilities.new_conversation_options(
            resolved,
            self._capabilities.default_dataset_ids(catalog),
        )
        current_id = self._store.get_route(account_id, external_address_hash)
        previous_title = self._safe_title(
            owner_user_id=owner_user_id,
            conversation_id=current_id,
            request_id=f'{request_id}_previous',
        )
        self._store.begin_new_conversation(
            account_id,
            external_address_hash,
            draft.to_dict(),
        )
        if message:
            return self.chat(
                account_id=account_id,
                external_address_hash=external_address_hash,
                owner_user_id=owner_user_id,
                request_id=request_id,
                message=message,
                changes=[],
                resolved_changes=[],
                source_command=source_command,
                source_messages=source_messages,
                catalog=catalog,
                on_stream=on_stream,
            )
        lines = ['── 已进入新会话 ──']
        if current_id:
            lines.append(f'已离开：{previous_title}')
        if draft.search_config:
            names = self._capabilities.dataset_names(
                draft.search_config.get('dataset_list'),
                catalog,
            )
            lines.append(f'默认知识库：{"、".join(names) if names else "无"}')
        lines.append('请发送新会话的第一条消息。')
        return ConversationResult(
            text='\n'.join(lines),
            presentations=(
                ConversationPresentation(
                    kind='conversation',
                    state='new',
                    title='等待第一条消息',
                ),
            ),
        )

    def list_conversations(
        self,
        *,
        account_id: str,
        external_address_hash: str,
        owner_user_id: str,
        request_id: str,
        assistant: AssistantProvider = 'lazymind',
    ) -> str | ConversationResult:
        items = self._all_conversations(
            owner_user_id,
            request_id,
            assistant=assistant,
        )
        snapshot = [
            {
                'conversation_id': str(item.get('conversation_id') or ''),
                'host_id': str(item.get('host_id') or ''),
                'provider_thread_id': str(item.get('provider_thread_id') or ''),
                'display_name': self._display_name(item),
                'update_time': str(item.get('update_time') or ''),
                'assistant': assistant,
                'project_key': str(item.get('project_key') or ''),
                'project_name': str(item.get('project_name') or ''),
            }
            for item in items
            if item.get('conversation_id') or item.get('provider_thread_id')
        ]
        if assistant != 'lazymind':
            projects: dict[str, list[dict[str, str]]] = {}
            for item in snapshot:
                projects.setdefault(item['project_key'] or 'unassigned', []).append(item)
            snapshot = [item for project in projects.values() for item in project]
        if not snapshot:
            self._store.clear_selection_snapshot(account_id, external_address_hash)
            label = self._assistant_label(assistant)
            return f'{label} 暂时没有可继续的历史会话。'
        self._store.save_selection_snapshot(
            account_id,
            external_address_hash,
            'conversation',
            snapshot,
            dt.datetime.now(dt.timezone.utc) + _SNAPSHOT_TTL,
        )
        current_id = self._store.get_route(account_id, external_address_hash)
        lines = [f'{self._assistant_label(assistant)} 最近会话：']
        for index, item in enumerate(snapshot[:_LIST_LIMIT], start=1):
            marker = '●' if item['conversation_id'] == current_id else ' '
            lines.append(
                f'{marker} {index}. {item["display_name"]}    '
                f'{self._format_time(item["update_time"])}'
            )
        lines.extend(('', '请在卡片中选择；纯文本渠道可回复会话编号。'))
        if len(snapshot) > _LIST_LIMIT:
            lines.append(f'飞书卡片可分页查看全部 {len(snapshot)} 个会话。')
        return ConversationResult(
            text='\n'.join(lines),
        )

    def switch(
        self,
        *,
        command: ConversationSwitchCommand,
        source_messages: Sequence[str],
        account_id: str,
        external_address_hash: str,
        selection_external_address_hash: str,
        owner_user_id: str,
        request_id: str,
        catalog: dict[str, Any],
        on_stream: Callable[[CoreStreamUpdate], None] | None = None,
    ) -> str | ConversationResult:
        parameters = command.parameters
        prepared = catalog.get(RESOLVED_CONVERSATION_TARGET_KEY)
        if isinstance(prepared, dict) and prepared.get('conversation_id'):
            target_id = str(prepared['conversation_id'])
            display_index = None
        else:
            target_id, display_index = self._resolve_switch_target(
                command=command,
                source_messages=source_messages,
                account_id=account_id,
                selection_external_address_hash=(
                    selection_external_address_hash
                ),
                owner_user_id=owner_user_id,
                request_id=request_id,
            )
        resolved_changes: ResolvedChanges | None = None
        if parameters.resource_changes:
            resolved_changes = self._capabilities.resolve_changes(
                parameters.resource_changes,
                catalog,
                account_id=account_id,
                external_address_hash=external_address_hash,
                source_command=command,
                source_messages=source_messages,
                prepared_conversation_target=PreparedConversationTarget(
                    conversation_id=target_id,
                ),
            )
            self._capabilities.validate_resolved_changes(resolved_changes)
        transition = self._switch_to(
            account_id=account_id,
            external_address_hash=external_address_hash,
            owner_user_id=owner_user_id,
            request_id=request_id,
            target_id=target_id,
            display_index=display_index,
            activate=(
                not parameters.message
                and not parameters.resource_changes
            ),
        )
        if not parameters.message:
            if parameters.resource_changes:
                configured = self._capabilities.configure_capabilities(
                    changes=parameters.resource_changes,
                    resolved_changes=resolved_changes,
                    source_command=command,
                    source_messages=source_messages,
                    catalog=catalog,
                    account_id=account_id,
                    external_address_hash=external_address_hash,
                    owner_user_id=owner_user_id,
                    request_id=f'{request_id}_configure',
                    conversation_id_override=target_id,
                )
                self._store.activate_conversation(
                    account_id,
                    external_address_hash,
                    target_id,
                )
                return ConversationResult(
                    text=f'{transition.text}\n\n{configured}',
                    presentations=transition.presentations,
                )
            return transition
        answer = self.chat(
            account_id=account_id,
            external_address_hash=external_address_hash,
            owner_user_id=owner_user_id,
            request_id=f'{request_id}_continue',
            message=parameters.message,
            changes=parameters.resource_changes,
            resolved_changes=resolved_changes,
            source_command=command,
            source_messages=source_messages,
            catalog=catalog,
            conversation_id_override=target_id,
            on_stream=on_stream,
        )
        return ConversationResult(
            text=(
                f'{transition.text}\n\n'
                f'── 新任务回复 ──\n{answer.text}'
            ),
            turn=answer.turn,
            presentations=transition.presentations,
        )

    def _resolve_switch_target(
        self,
        *,
        command: ConversationSwitchCommand,
        source_messages: Sequence[str],
        account_id: str,
        selection_external_address_hash: str,
        owner_user_id: str,
        request_id: str,
    ) -> tuple[str, int | None]:
        parameters = command.parameters
        if parameters.target.kind == 'index':
            snapshot = self._store.get_selection_snapshot(
                account_id,
                selection_external_address_hash,
                expected_kind='conversation',
            )
            if snapshot is None:
                raise ActionMessage(
                    '上一次会话列表不存在或已超过 10 分钟，当前会话没有改变。'
                    '请先说“查看历史会话”。'
                )
            try:
                index = int(parameters.target.value)
            except ValueError as exc:
                raise ActionMessage(
                    '会话编号无效，当前会话没有改变。'
                ) from exc
            if index < 1 or index > len(snapshot):
                raise ActionMessage(
                    f'当前列表只有 1～{len(snapshot)}，当前会话没有改变。'
                )
            return self._materialize_switch_target(
                snapshot[index - 1],
                owner_user_id=owner_user_id,
                request_id=request_id,
            ), index
        matches = self._match_conversations(
            self._all_conversations(owner_user_id, request_id),
            parameters.target.value,
        )
        if len(matches) > 1:
            snapshot = [
                {
                    'conversation_id': str(
                        item.get('conversation_id') or ''
                    ),
                    'display_name': self._display_name(item),
                    'update_time': str(item.get('update_time') or ''),
                }
                for item in matches[:_LIST_LIMIT]
            ]
            self._store.save_selection_snapshot(
                account_id,
                selection_external_address_hash,
                'conversation',
                snapshot,
                dt.datetime.now(dt.timezone.utc) + _SNAPSHOT_TTL,
                continuation=SelectionContinuation(
                    selection_field='conversation_target',
                    command=command.model_dump(mode='json'),
                    grounding_messages=list(source_messages),
                ).model_dump(mode='json'),
            )
            lines = ['找到多个同名或相近会话，请再选一个：']
            lines.extend(
                f'{index}. {item["display_name"]}'
                for index, item in enumerate(snapshot, start=1)
            )
            lines.extend(('', '当前会话没有改变。'))
            raise ActionMessage('\n'.join(lines))
        if not matches:
            raise ActionMessage(
                '没有找到这个会话，当前会话没有改变。请先说“查看历史会话”。'
            )
        return str(matches[0].get('conversation_id') or ''), None

    def _materialize_switch_target(
        self,
        item: dict[str, Any],
        *,
        owner_user_id: str,
        request_id: str,
    ) -> str:
        conversation_id = str(item.get('conversation_id') or '')
        if conversation_id:
            return conversation_id
        provider = str(item.get('assistant') or '')
        host_id = str(item.get('host_id') or '')
        thread_id = str(item.get('provider_thread_id') or '')
        if provider not in {'codex', 'cursor', 'workbuddy'} or not host_id or not thread_id:
            raise ActionMessage('这个外部会话当前不可继续，请刷新会话列表。')
        binding = self._client.bind_external_agent_session(
            owner_user_id=owner_user_id,
            request_id=f'{request_id}_bind',
            provider=provider,
            host_id=host_id,
            provider_thread_id=thread_id,
        )
        conversation_id = str(binding.get('conversation_id') or '')
        if not conversation_id:
            raise ActionMessage('外部会话当前不可继续，请刷新会话列表。')
        item['conversation_id'] = conversation_id
        return conversation_id

    def _load_switch_target(
        self,
        *,
        owner_user_id: str,
        request_id: str,
        target_id: str,
    ) -> tuple[dict[str, Any], dict[str, Any]]:
        try:
            detail = self._client.get_conversation_detail(
                owner_user_id=owner_user_id,
                conversation_id=target_id,
                request_id=f'{request_id}_target',
            )
            history = self._client.get_conversation_history(
                owner_user_id=owner_user_id,
                conversation_id=target_id,
                request_id=f'{request_id}_history',
                page_size=_HISTORY_PAGE_SIZE,
            )
        except LazyMindHTTPError as exc:
            if exc.status_code == 404:
                raise ActionMessage(
                    '目标会话已经不存在，当前会话没有改变，请重新查看历史会话。'
                ) from exc
            raise
        return detail, history

    def current(
        self,
        *,
        account_id: str,
        external_address_hash: str,
        owner_user_id: str,
        request_id: str,
    ) -> str | ConversationResult:
        conversation_id = self._store.get_route(account_id, external_address_hash)
        if not conversation_id:
            state = self._store.get_navigation_state(
                account_id,
                external_address_hash,
            )
            if state and state.get('mode') == 'new_pending':
                return '当前处于新会话状态。发送第一条任务后才会正式创建。'
            return '当前还没有会话。直接发送任务即可创建，或先查看历史会话。'
        try:
            detail = self._client.get_conversation_detail(
                owner_user_id=owner_user_id,
                conversation_id=conversation_id,
                request_id=request_id,
            )
        except LazyMindHTTPError as exc:
            if exc.status_code != 404:
                raise
            self._store.begin_new_conversation(account_id, external_address_hash)
            return '当前会话已经不存在，已进入新会话状态。'
        title = self._display_name(detail)
        updated_at = self._format_time(
            str(detail.get('update_time') or '')
        )
        return ConversationResult(
            text=(
                f'当前会话：{title}\n'
                f'最后更新：{updated_at}'
            ),
            presentations=(
                ConversationPresentation(
                    kind='conversation',
                    state='current',
                    title=title,
                ),
            ),
        )

    def stop(
        self,
        *,
        account_id: str,
        external_address_hash: str,
        owner_user_id: str,
        request_id: str,
    ) -> str:
        conversation_id = self._store.get_route(
            account_id,
            external_address_hash,
        )
        if not conversation_id:
            return '当前没有正在执行的会话。'
        self._client.stop_chat_generation(
            owner_user_id=owner_user_id,
            conversation_id=conversation_id,
            request_id=request_id,
        )
        return '已请求停止当前会话的生成与后台执行。'

    def more_history(
        self,
        *,
        account_id: str,
        external_address_hash: str,
        owner_user_id: str,
        request_id: str,
    ) -> str | ConversationResult:
        conversation_id = self._store.get_route(account_id, external_address_hash)
        if not conversation_id:
            return '当前还没有可读取历史的会话，请先发送任务或切换会话。'
        state = self._store.get_navigation_state(account_id, external_address_hash) or {}
        initialized = state.get('history_conversation_id') == conversation_id
        if initialized and not state.get('history_next_page_token'):
            return '当前会话已经到最早一条记录。'
        page_token = (
            str(state.get('history_next_page_token') or '') if initialized else ''
        )
        try:
            detail = self._client.get_conversation_detail(
                owner_user_id=owner_user_id,
                conversation_id=conversation_id,
                request_id=f'{request_id}_detail',
            )
            history = self._client.get_conversation_history(
                owner_user_id=owner_user_id,
                conversation_id=conversation_id,
                request_id=request_id,
                page_size=_HISTORY_PAGE_SIZE,
                page_token=page_token,
            )
        except LazyMindHTTPError as exc:
            if exc.status_code != 404:
                raise
            self._store.begin_new_conversation(account_id, external_address_hash)
            return '当前会话已经不存在，已解除当前会话指针。'
        next_token = str(history.get('next_page_token') or '')
        self._store.set_history_cursor(
            account_id,
            external_address_hash,
            conversation_id,
            next_token,
        )
        heading = (
            f'── 更早的 3 轮 · {self._display_name(detail)} ──'
            if initialized
            else f'── 最近 3 轮 · {self._display_name(detail)} ──'
        )
        lines = self._format_history(history, heading=heading)
        if not next_token:
            lines.extend(('', '已经到最早一条记录。'))
        return ConversationResult(
            text='\n'.join(lines),
            presentations=(
                ConversationPresentation(
                    kind='conversation',
                    state='history',
                    title=self._display_name(detail),
                    turns=self._history_turns(history),
                    reached_start=not next_token,
                ),
            ),
        )

    def _switch_to(
        self,
        *,
        account_id: str,
        external_address_hash: str,
        owner_user_id: str,
        request_id: str,
        target_id: str,
        display_index: int | None,
        activate: bool = True,
    ) -> ConversationResult:
        detail, history = self._load_switch_target(
            owner_user_id=owner_user_id,
            request_id=request_id,
            target_id=target_id,
        )
        previous_id = self._store.get_route(account_id, external_address_hash)
        previous_title = (
            self._safe_title(
                owner_user_id=owner_user_id,
                conversation_id=previous_id,
                request_id=f'{request_id}_previous',
            )
            if previous_id and previous_id != target_id
            else ''
        )
        if activate:
            self._store.activate_conversation(
                account_id,
                external_address_hash,
                target_id,
                history_next_page_token=str(
                    history.get('next_page_token') or ''
                ),
                preserve_selection=True,
            )
        title = self._display_name(detail)
        label = f'{display_index}. {title}' if display_index else title
        lines = ['── 已切换会话 ──']
        if previous_title:
            lines.append(f'已离开：{previous_title}')
        lines.extend(
            (
                f'当前会话：{label}',
                f'最后更新：{self._format_time(str(detail.get("update_time") or ""))}',
                '',
            )
        )
        lines.extend(self._format_history(history, heading='最近 3 轮：'))
        lines.extend(
            (
                '',
                '── 从这里继续 ──',
                '可以直接发送下一条消息，或说“查看更多历史”。',
            )
        )
        return ConversationResult(
            text='\n'.join(lines),
            presentations=(
                ConversationPresentation(
                    kind='conversation',
                    state='switched',
                    title=title,
                    turns=self._history_turns(history),
                    reached_start=not bool(
                        history.get('next_page_token')
                    ),
                ),
            ),
        )

    def _all_conversations(
        self,
        owner_user_id: str,
        request_id: str,
        assistant: str = '',
    ) -> list[dict[str, Any]]:
        items: list[dict[str, Any]] = []
        page_token = ''
        seen_tokens: set[str] = set()
        while True:
            if page_token in seen_tokens:
                return items
            seen_tokens.add(page_token)
            if assistant and assistant != 'lazymind':
                payload = self._client.list_external_agent_sessions(
                    owner_user_id=owner_user_id,
                    request_id=request_id,
                    provider=assistant,
                    page_size=_CORE_PAGE_SIZE,
                    page_token=page_token,
                )
                raw = payload.get('sessions')
            else:
                payload = self._client.list_conversations(
                    owner_user_id=owner_user_id,
                    request_id=request_id,
                    page_size=_CORE_PAGE_SIZE,
                    page_token=page_token,
                    assistant=assistant,
                )
                raw = payload.get('conversations')
            if isinstance(raw, list):
                items.extend(dict(item) for item in raw if isinstance(item, dict))
            next_token = str(payload.get('next_page_token') or '')
            if not next_token or next_token == page_token:
                return items
            page_token = next_token
        return items

    @staticmethod
    def _assistant_label(assistant: str) -> str:
        return {
            'lazymind': 'LazyMind',
            'codex': 'Codex Desktop',
            'cursor': 'Cursor CLI',
            'workbuddy': 'WorkBuddy / CodeBuddy CLI',
        }.get(assistant, 'LazyMind')

    def _match_conversations(
        self,
        items: list[dict[str, Any]],
        target: str,
    ) -> list[dict[str, Any]]:
        wanted = self._normalize(target)
        exact = [
            item
            for item in items
            if self._normalize(self._display_name(item)) == wanted
        ]
        return exact or [
            item
            for item in items
            if wanted and wanted in self._normalize(self._display_name(item))
        ]

    def _safe_title(
        self,
        *,
        owner_user_id: str,
        conversation_id: str,
        request_id: str,
    ) -> str:
        if not conversation_id:
            return '无'
        try:
            detail = self._client.get_conversation_detail(
                owner_user_id=owner_user_id,
                conversation_id=conversation_id,
                request_id=request_id,
            )
        except LazyMindError:
            return '原会话'
        return self._display_name(detail)

    @staticmethod
    def _display_name(item: dict[str, Any] | None) -> str:
        if not item:
            return '未命名会话'
        return str(item.get('display_name') or '').strip() or '未命名会话'

    @staticmethod
    def _normalize(value: str) -> str:
        return re.sub(r'\s+', ' ', value.strip()).casefold()

    @staticmethod
    def _format_time(value: str) -> str:
        if not value:
            return '时间未知'
        try:
            parsed = dt.datetime.fromisoformat(value.replace('Z', '+00:00'))
            if parsed.tzinfo is None:
                parsed = parsed.replace(tzinfo=dt.timezone.utc)
            return parsed.astimezone(_CHINA_TIMEZONE).strftime('%m-%d %H:%M')
        except ValueError:
            return '时间未知'

    @classmethod
    def _history_turns(
        cls,
        payload: dict[str, Any],
    ) -> tuple[ConversationTurnPresentation, ...]:
        raw_history = payload.get('history')
        if not isinstance(raw_history, list):
            return ()
        turns: list[ConversationTurnPresentation] = []
        history = [
            item
            for item in raw_history
            if isinstance(item, dict)
        ]
        for item in reversed(history):
            query = (
                str(item.get('query') or '').strip()
                or '非文本内容，请在网页端查看'
            )
            answer = (
                sanitize_channel_text(str(item.get('result') or ''))
                or '该轮没有文字结果'
            )
            turns.append(
                ConversationTurnPresentation(
                    query=cls._truncate(
                        query,
                        _QUERY_PREVIEW_LIMIT,
                    ),
                    answer=cls._truncate(
                        answer,
                        _ANSWER_PREVIEW_LIMIT,
                    ),
                )
            )
        return tuple(turns)

    @classmethod
    def _format_history(
        cls,
        payload: dict[str, Any],
        *,
        heading: str,
    ) -> list[str]:
        turns = cls._history_turns(payload)
        if not turns:
            return [heading, '暂无历史记录。']
        lines = [heading]
        for turn in turns:
            lines.extend(
                (
                    f'[用户] {turn.query}',
                    f'[LazyMind] {turn.answer}',
                    '',
                )
            )
        if lines[-1] == '':
            lines.pop()
        return lines

    @staticmethod
    def _truncate(value: str, limit: int) -> str:
        normalized = re.sub(r'\s+', ' ', value).strip()
        if len(normalized) <= limit:
            return normalized
        return normalized[:limit].rstrip() + '……'

    @staticmethod
    def _event_fallback(turn: CoreTurnResult) -> str:
        event_types = {event.type for event in turn.events}
        if 'ask_pending' in event_types:
            return 'LazyMind 正在等待补充信息。'
        if 'task_created' in event_types:
            return 'LazyMind 已创建后台任务。'
        if 'artifact_created' in event_types:
            return 'LazyMind 已生成新的结果文件。'
        if 'tool_limit_pending' in event_types:
            return 'LazyMind 已达到工具轮次上限，并自动请求汇总当前结果。'
        return 'LazyMind 已处理这条消息。'

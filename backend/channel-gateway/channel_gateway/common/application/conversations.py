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
    CommandEnvelope,
    ConversationSwitchCommand,
    ResourceChange,
    SelectionContinuation,
)
from channel_gateway.common.errors import LazyMindError, LazyMindHTTPError
from channel_gateway.common.domain.chat import (
    ChannelFeatureProfile,
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
_SWITCH_TARGET_KEY = '_channel_gateway_switch_target'


@dataclass(frozen=True, slots=True)
class ConversationResult:
    text: str
    turn: CoreTurnResult | None = None
    presentations: tuple[ReplyPresentation, ...] = ()
    suppress_text_when_presented: bool = False


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
        features: ChannelFeatureProfile,
        conversation_id_override: str | None = None,
        executor: dict[str, Any] | None = None,
        activate_route: bool = True,
        ask_answers_structured: dict[str, Any] | None = None,
        ask_already_validated: bool = False,
        inputs: Sequence[dict[str, str]] = (),
        mentions: Sequence[dict[str, str]] = (),
        workspace_dataset_ids: Sequence[str] | None = None,
        disabled_tools: Sequence[str] = (),
        enable_workflow: bool | None = None,
        workflow_mode: str | None = None,
        thinking_depth: str | None = None,
        on_stream: Callable[[CoreStreamUpdate], None] | None = None,
    ) -> ConversationResult:
        conversation_id = (
            conversation_id_override
            if conversation_id_override is not None
            else self._store.get_route(
                account_id,
                external_address_hash,
            )
        )
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
            options = self._capabilities.merge_options(
                self._capabilities.options_from_dict(
                    self._store.get_pending_turn(
                        account_id,
                        external_address_hash,
                    )
                ),
                options,
            )
        else:
            default_ids = self._capabilities.default_dataset_ids(catalog)
            options = self._capabilities.new_conversation_options([], default_ids)
            options = self._capabilities.merge_options(
                options,
                self._capabilities.options_from_dict(
                    self._store.get_pending_turn(
                        account_id,
                        external_address_hash,
                    )
                ),
            )
            if explicit_new:
                options = self._capabilities.merge_options(
                    options,
                    self._capabilities.options_from_dict(
                        self._store.get_new_conversation_draft(
                            account_id,
                            external_address_hash,
                        )
                    ),
                )
            if resolved:
                options = self._capabilities.merge_options(
                    options,
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
            if (
                ask_answers_structured is not None
                and not ask_already_validated
            ):
                self._validate_pending_ask(
                    owner_user_id=owner_user_id,
                    conversation_id=conversation_id,
                    request_id=request_id,
                    structured=ask_answers_structured,
                )
            options.features = features
            options.executor = dict(executor) if executor is not None else None
            options.inputs.extend(dict(item) for item in inputs)
            options.ask_answers_structured = ask_answers_structured
            options.mentions.extend(dict(item) for item in mentions)
            if workspace_dataset_ids is not None:
                options.search_config = self._capabilities.search_config(
                    [str(value) for value in workspace_dataset_ids if value]
                )
            options.disabled_tools.extend(str(item) for item in disabled_tools)
            options.enable_workflow = enable_workflow
            options.workflow_mode = workflow_mode
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
            if conversation_id and exc.status_code == 404:
                self._store.begin_new_conversation(
                    account_id,
                    external_address_hash,
                )
                raise ActionMessage(
                    '当前会话已经不存在，已进入新会话状态；刚才的任务没有发送，请再发一次。'
                ) from exc
            if (
                exc.status_code == 409
                and '2001310' in exc.message
            ):
                raise ActionMessage(
                    '当前插件仍在运行或等待操作，暂时不能启动另一个插件。'
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
        suppress_text_when_presented = (
            not turn.answer
            and any(
                event.type in {'ask_pending', 'task_created'}
                for event in turn.events
            )
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
                        feature_labels=features.enabled_feature_labels,
                        footer='后续消息会继续这个新会话。',
                    ),
                ),
            )
        return ConversationResult(
            text=answer,
            turn=turn,
            suppress_text_when_presented=(
                suppress_text_when_presented
            ),
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
        features: ChannelFeatureProfile,
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
            self._capabilities.options_to_dict(draft),
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
                features=features,
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
                    previous_title=previous_title if current_id else '',
                    feature_labels=features.enabled_feature_labels,
                    footer='发送下一条消息后，LazyMind 会正式创建会话。',
                ),
            ),
            suppress_text_when_presented=True,
        )

    def list_conversations(
        self,
        *,
        account_id: str,
        external_address_hash: str,
        owner_user_id: str,
        request_id: str,
    ) -> str | ConversationResult:
        items = self._all_conversations(owner_user_id, request_id)[:_LIST_LIMIT]
        snapshot = [
            {
                'conversation_id': str(item.get('conversation_id') or ''),
                'display_name': self._display_name(item),
                'update_time': str(item.get('update_time') or ''),
            }
            for item in items
            if item.get('conversation_id')
        ]
        if not snapshot:
            self._store.clear_selection_snapshot(account_id, external_address_hash)
            return '暂时没有历史会话。你可以说“帮我创建一个新会话”。'
        self._store.save_selection_snapshot(
            account_id,
            external_address_hash,
            'conversation',
            snapshot,
            dt.datetime.now(dt.timezone.utc) + _SNAPSHOT_TTL,
        )
        current_id = self._store.get_route(account_id, external_address_hash)
        lines = ['最近会话：']
        for index, item in enumerate(snapshot, start=1):
            marker = '●' if item['conversation_id'] == current_id else ' '
            lines.append(
                f'{marker} {index}. {item["display_name"]}    '
                f'{self._format_time(item["update_time"])}'
            )
        lines.extend(('', '直接说“切到第几个会话”即可。'))
        return ConversationResult(
            text='\n'.join(lines),
            suppress_text_when_presented=True,
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
        features: ChannelFeatureProfile,
        on_stream: Callable[[CoreStreamUpdate], None] | None = None,
    ) -> str | ConversationResult:
        parameters = command.parameters
        prepared = catalog.get(_SWITCH_TARGET_KEY)
        if isinstance(prepared, dict) and prepared.get('target_id'):
            target_id = str(prepared['target_id'])
            raw_display_index = prepared.get('display_index')
            display_index = (
                int(raw_display_index)
                if isinstance(raw_display_index, int)
                else None
            )
            detail = prepared.get('detail')
            history = prepared.get('history')
            prepared_detail = detail if isinstance(detail, dict) else None
            prepared_history = history if isinstance(history, dict) else None
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
            prepared_detail = None
            prepared_history = None
        resolved_changes: ResolvedChanges | None = None
        if parameters.resource_changes:
            resolved_changes = self._capabilities.resolve_changes(
                parameters.resource_changes,
                catalog,
                account_id=account_id,
                external_address_hash=external_address_hash,
                source_command=command,
                source_messages=source_messages,
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
            prepared_detail=prepared_detail,
            prepared_history=prepared_history,
            features=features,
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
                    suppress_text_when_presented=True,
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
            features=features,
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
            return (
                str(snapshot[index - 1].get('conversation_id') or ''),
                index,
            )
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
        features: ChannelFeatureProfile,
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
                f'最后更新：{updated_at}\n'
                + self._feature_summary(features)
            ),
            presentations=(
                ConversationPresentation(
                    kind='conversation',
                    state='current',
                    title=title,
                    updated_at=updated_at,
                    feature_labels=features.enabled_feature_labels,
                    footer='后续消息会继续当前会话。',
                ),
            ),
            suppress_text_when_presented=True,
        )

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
                    updated_at=self._format_time(
                        str(detail.get('update_time') or '')
                    ),
                    history_label=(
                        '更早的对话'
                        if initialized
                        else '最近对话'
                    ),
                    turns=self._history_turns(history),
                    footer=(
                        '已经到最早一条记录。'
                        if not next_token
                        else '还可以继续查看更多历史。'
                    ),
                    reached_start=not next_token,
                ),
            ),
            suppress_text_when_presented=True,
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
        prepared_detail: dict[str, Any] | None = None,
        prepared_history: dict[str, Any] | None = None,
        features: ChannelFeatureProfile,
    ) -> ConversationResult:
        if prepared_detail is not None and prepared_history is not None:
            detail, history = prepared_detail, prepared_history
        else:
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
                self._feature_summary(features),
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
                    previous_title=previous_title,
                    updated_at=self._format_time(
                        str(detail.get('update_time') or '')
                    ),
                    feature_labels=features.enabled_feature_labels,
                    history_label='最近对话',
                    turns=self._history_turns(history),
                    footer='后续消息会继续当前会话。',
                    reached_start=not bool(
                        history.get('next_page_token')
                    ),
                ),
            ),
            suppress_text_when_presented=True,
        )

    @staticmethod
    def _feature_summary(features: ChannelFeatureProfile) -> str:
        enabled = features.enabled_feature_labels
        if not enabled:
            return (
                '当前渠道只执行基础聊天，不会推进原有 '
                'Workflow、Task、SubAgent 或 Ask 流程。'
            )
        return (
            f'当前渠道已开放：{"、".join(enabled)}。'
            '实际能否调用取决于你的账号与会话配置。'
        )

    def _all_conversations(
        self,
        owner_user_id: str,
        request_id: str,
    ) -> list[dict[str, Any]]:
        items: list[dict[str, Any]] = []
        page_token = ''
        seen_tokens: set[str] = set()
        for _ in range(20):
            if page_token in seen_tokens:
                return items
            seen_tokens.add(page_token)
            payload = self._client.list_conversations(
                owner_user_id=owner_user_id,
                request_id=request_id,
                page_size=_CORE_PAGE_SIZE,
                page_token=page_token,
            )
            raw = payload.get('conversations')
            if isinstance(raw, list):
                items.extend(dict(item) for item in raw if isinstance(item, dict))
            next_token = str(payload.get('next_page_token') or '')
            if not next_token or next_token == page_token:
                return items
            page_token = next_token
        return items

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

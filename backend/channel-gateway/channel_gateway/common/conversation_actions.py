from __future__ import annotations

import datetime as dt
import re
from typing import Any, Sequence

from channel_gateway.common.capability_actions import (
    ActionMessage,
    CapabilityActions,
    ResolvedChanges,
)
from channel_gateway.common.commands import (
    CommandEnvelope,
    ConversationSwitchCommand,
    ResourceChange,
    SelectionContinuation,
)
from channel_gateway.common.database import GatewayStore
from channel_gateway.common.lazymind import (
    LazyMindClient,
    LazyMindError,
    LazyMindHTTPError,
)


_LIST_LIMIT = 10
_CORE_PAGE_SIZE = 100
_SNAPSHOT_TTL = dt.timedelta(minutes=10)
_HISTORY_PAGE_SIZE = 3
_QUERY_PREVIEW_LIMIT = 120
_ANSWER_PREVIEW_LIMIT = 300
_CHINA_TIMEZONE = dt.timezone(dt.timedelta(hours=8))


class ConversationActions:
    """Owns conversation operations and all user-facing conversation formatting."""

    def __init__(
        self,
        *,
        store: GatewayStore,
        client: LazyMindClient,
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
    ) -> str:
        conversation_id = self._store.get_route(account_id, external_address_hash)
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
            resolved_id, answer = self._client.chat(
                owner_user_id=owner_user_id,
                text=message,
                conversation_id=conversation_id,
                request_id=request_id,
                options=options,
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
            raise
        self._store.activate_conversation(
            account_id,
            external_address_hash,
            resolved_id,
            consume_pending_turn=True,
        )
        if explicit_new:
            return (
                '── 已创建并切换到新会话 ──\n'
                f'首条消息：{self._truncate(message, _QUERY_PREVIEW_LIMIT)}\n\n'
                f'{answer}'
            )
        return answer

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
    ) -> str:
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
        return '\n'.join(lines)

    def list_conversations(
        self,
        *,
        account_id: str,
        external_address_hash: str,
        owner_user_id: str,
        request_id: str,
    ) -> str:
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
        return '\n'.join(lines)

    def switch(
        self,
        *,
        command: ConversationSwitchCommand,
        source_messages: Sequence[str],
        account_id: str,
        external_address_hash: str,
        owner_user_id: str,
        request_id: str,
        catalog: dict[str, Any],
    ) -> str:
        parameters = command.parameters
        if parameters.target.kind == 'index':
            snapshot = self._store.get_selection_snapshot(
                account_id,
                external_address_hash,
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
                raise ActionMessage('会话编号无效，当前会话没有改变。') from exc
            if index < 1 or index > len(snapshot):
                raise ActionMessage(
                    f'当前列表只有 1～{len(snapshot)}，当前会话没有改变。'
                )
            target_id = str(snapshot[index - 1].get('conversation_id') or '')
            display_index: int | None = index
        else:
            matches = self._match_conversations(
                self._all_conversations(owner_user_id, request_id),
                parameters.target.value,
            )
            if len(matches) > 1:
                snapshot = [
                    {
                        'conversation_id': str(item.get('conversation_id') or ''),
                        'display_name': self._display_name(item),
                        'update_time': str(item.get('update_time') or ''),
                    }
                    for item in matches[:_LIST_LIMIT]
                ]
                self._store.save_selection_snapshot(
                    account_id,
                    external_address_hash,
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
            target_id = str(matches[0].get('conversation_id') or '')
            display_index = None
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
        marker = self._switch_to(
            account_id=account_id,
            external_address_hash=external_address_hash,
            owner_user_id=owner_user_id,
            request_id=request_id,
            target_id=target_id,
            display_index=display_index,
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
                )
                return f'{marker}\n\n{configured}'
            return marker
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
        )
        return f'{marker}\n\n── 新任务回复 ──\n{answer}'

    def current(
        self,
        *,
        account_id: str,
        external_address_hash: str,
        owner_user_id: str,
        request_id: str,
    ) -> str:
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
        return (
            f'当前会话：{self._display_name(detail)}\n'
            f'最后更新：{self._format_time(str(detail.get("update_time") or ""))}\n'
            '当前渠道在这个会话中只执行基础聊天。'
        )

    def more_history(
        self,
        *,
        account_id: str,
        external_address_hash: str,
        owner_user_id: str,
        request_id: str,
    ) -> str:
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
        return '\n'.join(lines)

    def _switch_to(
        self,
        *,
        account_id: str,
        external_address_hash: str,
        owner_user_id: str,
        request_id: str,
        target_id: str,
        display_index: int | None,
    ) -> str:
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
        self._store.activate_conversation(
            account_id,
            external_address_hash,
            target_id,
            history_next_page_token=str(history.get('next_page_token') or ''),
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
                '当前渠道只执行基础聊天，不会推进原有 Plugin、Task、SubAgent 或 Ask 流程。',
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
        return '\n'.join(lines)

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
    def _format_history(
        cls,
        payload: dict[str, Any],
        *,
        heading: str,
    ) -> list[str]:
        raw_history = payload.get('history')
        if not isinstance(raw_history, list) or not raw_history:
            return [heading, '暂无历史记录。']
        lines = [heading]
        history = [item for item in raw_history if isinstance(item, dict)]
        for item in reversed(history):
            query = (
                str(item.get('query') or '').strip()
                or '[非文本内容，请在网页端查看]'
            )
            answer = str(item.get('result') or '').strip() or '[暂无文字回答]'
            lines.extend(
                (
                    f'[用户] {cls._truncate(query, _QUERY_PREVIEW_LIMIT)}',
                    f'[LazyMind] {cls._truncate(answer, _ANSWER_PREVIEW_LIMIT)}',
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

from __future__ import annotations

import datetime as dt
import re
from typing import Any, Literal, Sequence

from channel_gateway.common.domain.chat import (
    ChannelFeatureProfile,
    ChatOptions,
)
from channel_gateway.common.domain.commands import (
    CommandEnvelope,
    ConversationSettingChange,
    PreparedConversationTarget,
    RESOLVED_RESOURCE_SELECTIONS_KEY,
    ResourceChange,
    SelectionContinuation,
)
from channel_gateway.common.domain.outbound import (
    CapabilityPresentation,
    ConversationSettingsPresentation,
)
from channel_gateway.common.ports.core import CapabilityClient
from channel_gateway.common.ports.repository import NavigationRepository


_LIST_LIMIT = 10
_PRESENTATION_LIST_LIMIT = 100
_IDENTIFIER_LIMIT = 512
_SNAPSHOT_TTL = dt.timedelta(minutes=10)
_CAPABILITY_LABELS = {
    'knowledge_base': '知识库',
    'skill': 'Skill',
    'tool': '工具',
    'prompt': 'Prompt',
    'conversation': '会话',
    'personalization': '个人习惯',
    'workflow': '工作流',
}

ResolvedChanges = list[tuple[ResourceChange, list[dict[str, Any]]]]


def _selection_items(
    items: Sequence[dict[str, Any]],
) -> list[dict[str, Any]]:
    return [
        {
            'id': str(item.get('id') or '')[:_IDENTIFIER_LIMIT],
            'name': str(item.get('name') or '')[:200],
            'can_disable': bool(item.get('can_disable', True)),
        }
        for item in items[:_LIST_LIMIT]
    ]


class ActionMessage(RuntimeError):
    pass


class CapabilityActions:
    """Resolves capability changes and applies their chat or persistent effects."""

    def __init__(
        self,
        *,
        store: NavigationRepository,
        client: CapabilityClient,
    ):
        self._store = store
        self._client = client

    def conversation_settings(
        self,
        *,
        account_id: str,
        external_address_hash: str,
        owner_user_id: str,
        request_id: str,
        updated: bool = False,
    ) -> tuple[str, ConversationSettingsPresentation]:
        conversation_id = self._store.get_route(
            account_id,
            external_address_hash,
        )
        if not conversation_id:
            raise ActionMessage(
                '当前还没有可设置的会话，请先新建或切换到一个会话。'
            )
        detail = self._client.get_conversation_detail(
            owner_user_id=owner_user_id,
            conversation_id=conversation_id,
            request_id=f'{request_id}_conversation_settings',
        )
        workflow_enabled = bool(
            detail.get('enable_workflow')
            if detail.get('enable_workflow') is not None
            else True
        )
        raw_workflow_mode = str(
            detail.get('workflow_mode') or 'dynamic'
        )
        workflow_mode: Literal['auto', 'dynamic'] = (
            'auto' if raw_workflow_mode == 'auto' else 'dynamic'
        )
        subagent_enabled = bool(
            detail.get('enable_subagent')
            if detail.get('enable_subagent') is not None
            else True
        )
        title = str(detail.get('display_name') or '未命名会话')
        search_config = detail.get('search_config')
        dataset_list = (
            search_config.get('dataset_list')
            if isinstance(search_config, dict)
            else []
        )
        dataset_ids = tuple(dict.fromkeys(
            str(item.get('id') or '')[:_IDENTIFIER_LIMIT]
            for item in (
                dataset_list if isinstance(dataset_list, list) else []
            )[:100]
            if isinstance(item, dict) and str(item.get('id') or '')
        ))
        text = (
            f'当前会话“{title}”的设置已保存。'
            if updated
            else f'当前会话“{title}”的设置：'
        )
        return (
            text,
            ConversationSettingsPresentation(
                kind='conversation_settings',
                dataset_ids=dataset_ids,
                workflow_enabled=workflow_enabled,
                workflow_mode=workflow_mode,
                subagent_enabled=subagent_enabled,
            ),
        )

    def update_conversation_setting(
        self,
        *,
        change: ConversationSettingChange,
        expected_conversation_id: str | None = None,
        catalog: dict[str, Any],
        account_id: str,
        external_address_hash: str,
        owner_user_id: str,
        request_id: str,
    ) -> tuple[str, ConversationSettingsPresentation]:
        conversation_id = self._store.get_route(
            account_id,
            external_address_hash,
        )
        if (
            expected_conversation_id
            and conversation_id != expected_conversation_id
        ):
            raise ActionMessage(
                '会话已切换，这张设置卡已经过期。'
            )
        if not conversation_id:
            raise ActionMessage(
                '当前还没有可设置的会话，请先新建或切换到一个会话。'
            )
        if change.setting == 'knowledge_base':
            datasets = [
                item
                for item in (
                    catalog.get('knowledge_base')
                    if isinstance(
                        catalog.get('knowledge_base'),
                        list,
                    )
                    else []
                )
                if isinstance(item, dict)
                and str(item.get('id') or '') == change.dataset_id
            ]
            if not datasets:
                raise ActionMessage(
                    '这个知识库已不可用，请重新打开会话设置。'
                )
            dataset_ids = self.conversation_dataset_ids(
                owner_user_id=owner_user_id,
                conversation_id=conversation_id,
                request_id=f'{request_id}_existing_kb',
            )
            if change.enabled:
                dataset_ids = list(
                    dict.fromkeys([*dataset_ids, change.dataset_id])
                )
            else:
                dataset_ids = [
                    value
                    for value in dataset_ids
                    if value != change.dataset_id
                ]
            self._client.update_conversation_search_config(
                owner_user_id=owner_user_id,
                conversation_id=conversation_id,
                request_id=f'{request_id}_save_kb',
                dataset_ids=dataset_ids,
            )
        elif change.setting in ('workflow_enabled', 'workflow_mode', 'subagent'):
            if change.setting == 'workflow_enabled':
                settings = {'enable_workflow': change.enabled}
            elif change.setting == 'workflow_mode':
                settings = {
                    'enable_workflow': True,
                    'workflow_mode': change.mode,
                }
            else:
                settings = {'enable_subagent': change.enabled}
            self._client.update_conversation_agent_settings(
                owner_user_id=owner_user_id,
                conversation_id=conversation_id,
                request_id=f'{request_id}_save_agent',
                settings=settings,
            )
        elif change.setting == 'skill':
            self._require_setting_item(
                catalog,
                'skill',
                change.skill_id,
            )
            self._client.set_skill_enabled(
                owner_user_id=owner_user_id,
                skill_id=change.skill_id,
                enabled=change.enabled,
                request_id=f'{request_id}_save_skill',
            )
            self._mark_setting_enabled(
                catalog,
                'skill',
                change.skill_id,
                change.enabled,
            )
        elif change.setting == 'tool':
            tool = self._require_setting_item(
                catalog,
                'tool',
                change.tool_name,
            )
            if (
                not change.enabled
                and not bool(tool.get('can_disable', True))
            ):
                raise ActionMessage('这个系统必需工具不能关闭。')
            self._client.set_tool_enabled(
                owner_user_id=owner_user_id,
                tool_name=change.tool_name,
                enabled=change.enabled,
                request_id=f'{request_id}_save_tool',
            )
            self._mark_setting_enabled(
                catalog,
                'tool',
                change.tool_name,
                change.enabled,
            )
        elif change.setting == 'personalization':
            self._client.set_personalization_enabled(
                owner_user_id=owner_user_id,
                enabled=change.enabled,
                request_id=f'{request_id}_save_personalization',
            )
            self._mark_setting_enabled(
                catalog,
                'personalization',
                'personalization',
                change.enabled,
            )
        else:
            self._require_setting_item(
                catalog,
                'workflow',
                change.workflow_ref,
            )
            self._client.set_workflow_enabled(
                owner_user_id=owner_user_id,
                workflow_ref=change.workflow_ref,
                enabled=change.enabled,
                request_id=f'{request_id}_save_workflow',
            )
            self._mark_setting_enabled(
                catalog,
                'workflow',
                change.workflow_ref,
                change.enabled,
            )
        self._store.clear_selection_snapshot(
            account_id,
            external_address_hash,
        )
        return self.conversation_settings(
            account_id=account_id,
            external_address_hash=external_address_hash,
            owner_user_id=owner_user_id,
            request_id=f'{request_id}_verify',
            updated=True,
        )

    @staticmethod
    def _require_setting_item(
        catalog: dict[str, Any],
        kind: str,
        item_id: str,
    ) -> dict[str, Any]:
        values = catalog.get(kind)
        for item in (
            values if isinstance(values, list) else []
        ):
            if (
                isinstance(item, dict)
                and str(item.get('id') or '') == item_id
            ):
                return item
        raise ActionMessage('这个能力已不可用，请重新打开设置中心。')

    @staticmethod
    def _mark_setting_enabled(
        catalog: dict[str, Any],
        kind: str,
        item_id: str,
        enabled: bool,
    ) -> None:
        values = catalog.get(kind)
        for item in (
            values if isinstance(values, list) else []
        ):
            if (
                isinstance(item, dict)
                and str(item.get('id') or '') == item_id
            ):
                item['enabled'] = enabled

    def list_capabilities(
        self,
        *,
        kinds: list[str],
        catalog: dict[str, Any],
        account_id: str,
        external_address_hash: str,
        features: ChannelFeatureProfile,
        save_selection: bool = True,
    ) -> tuple[str, CapabilityPresentation]:
        kinds = list(dict.fromkeys(kinds))
        if save_selection:
            self._store.clear_selection_snapshot(
                account_id,
                external_address_hash,
            )
        lines = ['当前渠道可用能力：']
        groups: list[dict[str, Any]] = []
        for kind in kinds:
            items = catalog.get(kind)
            values = (
                [item for item in items if isinstance(item, dict)]
                if isinstance(items, list)
                else []
            )
            lines.append(f'\n【{_CAPABILITY_LABELS[kind]}】')
            if not values:
                if kind == 'skill':
                    lines.append('暂无已安装且已发布的 Skill。')
                else:
                    lines.append('暂无可用项。')
            else:
                for index, item in enumerate(
                    values[:_LIST_LIMIT],
                    start=1,
                ):
                    if kind == 'knowledge_base':
                        status = (
                            '默认'
                            if bool(item.get('default', False))
                            else '可用'
                        )
                    else:
                        status = (
                            '已启用'
                            if bool(item.get('enabled', True))
                            else '已关闭'
                        )
                    name = str(item.get('name') or '未命名')
                    lines.append(f'{index}. {name}（{status}）')
            presented_items = []
            for item in values[:_PRESENTATION_LIST_LIMIT]:
                presented = {
                    'id': str(item.get('id') or '')[:_IDENTIFIER_LIMIT],
                    'name': str(item.get('name') or '未命名')[:200],
                    'enabled': bool(item.get('enabled', True)),
                }
                if kind == 'prompt' and str(item.get('content') or '').strip():
                    presented['content'] = str(item['content']).strip()[:4000]
                presented_items.append(presented)
            groups.append(
                {
                    'resource_type': kind,
                    'label': _CAPABILITY_LABELS[kind],
                    'items': presented_items,
                    'total': len(values),
                }
            )
            if (
                len(kinds) == 1
                and save_selection
                and values
                and kind in {
                    'knowledge_base',
                    'skill',
                    'tool',
                    'personalization',
                }
            ):
                self._store.save_selection_snapshot(
                    account_id,
                    external_address_hash,
                    kind,
                    _selection_items(values),
                    dt.datetime.now(dt.timezone.utc) + _SNAPSHOT_TTL,
                )
        lines.extend(
            (
                '',
                '你可以直接说“这轮使用哪个知识库”或“以后关闭哪个工具”。',
            )
        )
        enabled_features = features.enabled_feature_labels
        if not enabled_features:
            lines.append(
                '当前渠道不提供 Workflow、SubAgent、后台 Task 和结构化 Ask。'
            )
        else:
            lines.append(
                '当前渠道已开放 Core 能力：'
                f'{"、".join(enabled_features)}。'
                '实际可用项取决于你的账号与会话配置。'
            )
        return (
            '\n'.join(lines),
            CapabilityPresentation(
                kind='capability',
                groups=tuple(groups),
            ),
        )

    def configure_capabilities(
        self,
        *,
        changes: list[ResourceChange],
        resolved_changes: ResolvedChanges | None = None,
        source_command: CommandEnvelope | None = None,
        source_messages: Sequence[str] = (),
        catalog: dict[str, Any],
        account_id: str,
        external_address_hash: str,
        owner_user_id: str,
        request_id: str,
        conversation_id_override: str | None = None,
    ) -> str:
        if not changes and resolved_changes is None:
            return '请说明要使用或关闭哪个知识库、Skill、工具或个人习惯。'
        resolved = (
            resolved_changes
            if resolved_changes is not None
            else self.resolve_changes(
                changes,
                catalog,
                account_id=account_id,
                external_address_hash=external_address_hash,
                source_command=source_command,
                source_messages=source_messages,
            )
        )
        conversation_id = (
            conversation_id_override
            if conversation_id_override is not None
            else self._store.get_route(account_id, external_address_hash)
        )
        state = self._store.get_navigation_state(account_id, external_address_hash) or {}
        persistent = [
            pair for pair in resolved if pair[0].scope in ('conversation', 'global')
        ]
        if any(change.scope == 'conversation' for change, _ in persistent):
            if not conversation_id and state.get('mode') != 'new_pending':
                raise ActionMessage('当前还没有会话，无法保存会话级知识库配置。')
        if conversation_id:
            self.apply_persistent_changes(
                resolved=persistent,
                conversation_id=conversation_id,
                owner_user_id=owner_user_id,
                request_id=request_id,
            )
        else:
            self.apply_global_changes(
                resolved=persistent,
                owner_user_id=owner_user_id,
                request_id=request_id,
            )

        turn_pairs = [pair for pair in resolved if pair[0].scope == 'turn']
        if turn_pairs:
            base_ids = (
                self.conversation_dataset_ids(
                    owner_user_id=owner_user_id,
                    conversation_id=conversation_id,
                    request_id=f'{request_id}_resources',
                )
                if conversation_id
                else self.default_dataset_ids(catalog)
            )
            turn_options = self.turn_options(turn_pairs, base_ids)
            if state.get('mode') == 'new_pending':
                draft = self.merge_options(
                    self.options_from_dict(
                        self._store.get_new_conversation_draft(
                            account_id,
                            external_address_hash,
                        )
                    ),
                    turn_options,
                )
                self._store.begin_new_conversation(
                    account_id,
                    external_address_hash,
                    self.options_to_dict(draft),
                )
            else:
                turn_options = self.merge_options(
                    self.options_from_dict(
                        self._store.get_pending_turn(
                            account_id,
                            external_address_hash,
                        )
                    ),
                    turn_options,
                )
                self._store.save_pending_turn(
                    account_id,
                    external_address_hash,
                    self.options_to_dict(turn_options),
                )
        if not conversation_id and state.get('mode') == 'new_pending':
            conversation_pairs = [
                pair for pair in resolved if pair[0].scope == 'conversation'
            ]
            if conversation_pairs:
                draft = self.merge_options(
                    self.options_from_dict(
                        self._store.get_new_conversation_draft(
                            account_id,
                            external_address_hash,
                        )
                    ),
                    self.new_conversation_options(
                        conversation_pairs,
                        self.default_dataset_ids(catalog),
                    ),
                )
                self._store.begin_new_conversation(
                    account_id,
                    external_address_hash,
                    self.options_to_dict(draft),
                )
        self._store.clear_selection_snapshot(account_id, external_address_hash)
        lines = ['配置已更新：']
        for change, items in resolved:
            names = '、'.join(str(item.get('name') or '') for item in items) or '全部'
            scope = {'turn': '下一轮', 'conversation': '当前会话', 'global': '以后默认'}[
                change.scope
            ]
            action = '使用' if change.operation == 'use' else '关闭'
            lines.append(
                f'- {scope}{action}{_CAPABILITY_LABELS[change.resource_type]}：{names}'
            )
        if turn_pairs:
            lines.append('请直接发送下一条任务；该单轮配置成功执行后自动清除。')
        return '\n'.join(lines)

    def resolve_changes(
        self,
        changes: list[ResourceChange],
        catalog: dict[str, Any],
        *,
        account_id: str,
        external_address_hash: str,
        source_command: CommandEnvelope | None = None,
        source_messages: Sequence[str] = (),
        prepared_conversation_target: PreparedConversationTarget | None = None,
    ) -> ResolvedChanges:
        resolved: ResolvedChanges = []
        saved_resolved = catalog.get(RESOLVED_RESOURCE_SELECTIONS_KEY)
        saved_by_position = (
            saved_resolved
            if isinstance(saved_resolved, dict)
            else {}
        )
        for position, change in enumerate(changes):
            values = catalog.get(change.resource_type)
            items = (
                [item for item in values if isinstance(item, dict)]
                if isinstance(values, list)
                else []
            )
            saved = saved_by_position.get(str(position))
            if (
                isinstance(saved, dict)
                and saved.get('resource_type') == change.resource_type
                and isinstance(saved.get('items'), list)
            ):
                saved_items = [
                    item
                    for item in saved['items']
                    if isinstance(item, dict)
                ]
                saved_id = (
                    str(saved_items[0].get('id') or '')
                    if len(saved_items) == 1
                    else ''
                )
                fresh = next(
                    (
                        item
                        for item in items
                        if str(item.get('id') or '') == saved_id
                    ),
                    None,
                )
                if fresh is None:
                    raise ActionMessage('能力目录已变化，请重新选择。')
                resolved.append((change, [fresh]))
                continue
            if change.resource_type == 'skill' and change.operation != 'use':
                raise ActionMessage(
                    '当前渠道只支持本轮指定 Skill，不支持在渠道中关闭 Skill。'
                )
            if change.operation == 'clear':
                resolved.append((change, items))
                continue
            if change.resource_type == 'personalization':
                resolved.append((change, items[:1]))
                continue
            selector = getattr(change, 'selector', None)
            if selector is None:
                raise ActionMessage('资源选择参数无效，配置没有改变。')
            selected: list[dict[str, Any]] = []
            if selector.kind == 'index':
                snapshot = self._store.get_selection_snapshot(
                    account_id,
                    external_address_hash,
                    expected_kind=change.resource_type,
                )
                index = int(selector.value)
                if snapshot is None or index < 1 or index > len(snapshot):
                    raise ActionMessage(
                        f'没有可用的{_CAPABILITY_LABELS[change.resource_type]}编号列表，'
                        '请先查看该类能力。'
                    )
                selected_id = str(snapshot[index - 1].get('id') or '')
                fresh = next(
                    (
                        item
                        for item in items
                        if str(item.get('id') or '') == selected_id
                    ),
                    None,
                )
                if fresh is None:
                    raise ActionMessage('能力目录已变化，请重新选择。')
                selected = [fresh]
            else:
                wanted = self._normalize(selector.value)
                exact = [
                    item
                    for item in items
                    if self._normalize(str(item.get('name') or '')) == wanted
                ]
                selected = exact or [
                    item
                    for item in items
                    if wanted
                    and wanted in self._normalize(str(item.get('name') or ''))
                ]
            if len(selected) > 1:
                self._store.save_selection_snapshot(
                    account_id,
                    external_address_hash,
                    change.resource_type,
                    _selection_items(selected),
                    dt.datetime.now(dt.timezone.utc) + _SNAPSHOT_TTL,
                    continuation=(
                        SelectionContinuation(
                            selection_field='resource_change',
                            command=source_command.model_dump(mode='json'),
                            grounding_messages=list(source_messages),
                            resource_change_index=position,
                            prepared_conversation_target=(
                                prepared_conversation_target
                            ),
                            prepared_resources=self._continuation_resources(
                                catalog,
                                resolved,
                            ),
                        ).model_dump(mode='json')
                        if source_command is not None and source_messages
                        else None
                    ),
                )
                lines = [
                    f'找到多个{_CAPABILITY_LABELS[change.resource_type]}，请再选一个：'
                ]
                lines.extend(
                    f'{index}. {item.get("name") or "未命名"}'
                    for index, item in enumerate(selected[:_LIST_LIMIT], start=1)
                )
                raise ActionMessage('\n'.join(lines))
            if not selected:
                raise ActionMessage(
                    f'没有找到{_CAPABILITY_LABELS[change.resource_type]}'
                    f'“{selector.value}”，'
                    '配置没有改变。'
                )
            resolved.append((change, selected))
        return resolved

    @staticmethod
    def _continuation_resources(
        catalog: dict[str, Any],
        resolved: ResolvedChanges,
    ) -> dict[str, Any]:
        saved_value = catalog.get(RESOLVED_RESOURCE_SELECTIONS_KEY)
        saved = (
            dict(saved_value)
            if isinstance(saved_value, dict)
            else {}
        )
        for position, (change, items) in enumerate(resolved):
            if len(items) != 1 or not isinstance(items[0], dict):
                continue
            item = _selection_items(items)[0]
            resource_id = str(item.get('id') or '').strip()
            if not resource_id:
                continue
            saved[str(position)] = {
                'resource_type': change.resource_type,
                'item': {
                    'id': resource_id,
                    'name': str(item.get('name') or '').strip(),
                    'can_disable': bool(item.get('can_disable', True)),
                },
            }
        return {
            key: value
            for key, value in saved.items()
            if key in {str(position) for position in range(8)}
        }

    def apply_persistent_changes(
        self,
        *,
        resolved: ResolvedChanges,
        conversation_id: str,
        owner_user_id: str,
        request_id: str,
    ) -> None:
        self._validate_global_changes(resolved)
        conversation_pairs = [
            pair
            for pair in resolved
            if pair[0].scope == 'conversation'
            and pair[0].resource_type == 'knowledge_base'
        ]
        if conversation_pairs:
            ids = self.conversation_dataset_ids(
                owner_user_id=owner_user_id,
                conversation_id=conversation_id,
                request_id=f'{request_id}_existing_kb',
            )
            ids = self._apply_dataset_changes(ids, conversation_pairs)
            self._client.update_conversation_search_config(
                owner_user_id=owner_user_id,
                conversation_id=conversation_id,
                request_id=f'{request_id}_save_kb',
                dataset_ids=ids,
            )
        self._apply_global_changes(
            resolved=resolved,
            owner_user_id=owner_user_id,
            request_id=request_id,
        )

    def validate_resolved_changes(self, resolved: ResolvedChanges) -> None:
        """Run deterministic policy checks before a compound command mutates state."""
        self._validate_global_changes(resolved)

    def apply_global_changes(
        self,
        *,
        resolved: ResolvedChanges,
        owner_user_id: str,
        request_id: str,
    ) -> None:
        self._validate_global_changes(resolved)
        self._apply_global_changes(
            resolved=resolved,
            owner_user_id=owner_user_id,
            request_id=request_id,
        )

    def _apply_global_changes(
        self,
        *,
        resolved: ResolvedChanges,
        owner_user_id: str,
        request_id: str,
    ) -> None:
        for position, (change, items) in enumerate(resolved):
            if change.scope != 'global':
                continue
            enabled = change.operation == 'use'
            if change.resource_type == 'knowledge_base':
                for item_position, item in enumerate(items):
                    self._client.set_default_dataset(
                        owner_user_id=owner_user_id,
                        request_id=(
                            f'{request_id}_global_kb_{position}_{item_position}'
                        ),
                        dataset_id=str(item.get('id') or ''),
                        name=str(item.get('name') or ''),
                        enabled=enabled,
                    )
            elif change.resource_type == 'tool':
                for item_position, item in enumerate(items):
                    self._client.set_tool_enabled(
                        owner_user_id=owner_user_id,
                        request_id=(
                            f'{request_id}_global_tool_{position}_{item_position}'
                        ),
                        tool_name=str(item.get('id') or ''),
                        enabled=enabled,
                    )
            elif change.resource_type == 'personalization':
                self._client.set_personalization_enabled(
                    owner_user_id=owner_user_id,
                    request_id=f'{request_id}_global_personalization',
                    enabled=enabled,
                )

    @staticmethod
    def _validate_global_changes(resolved: ResolvedChanges) -> None:
        global_pairs = [
            (change, items)
            for change, items in resolved
            if change.scope == 'global'
        ]
        if len(global_pairs) > 1 or any(len(items) > 1 for _, items in global_pairs):
            raise ActionMessage(
                '当前渠道暂不批量修改全局配置，请一次只修改一个具体项目，配置没有改变。'
            )
        blocked_tools = [
            str(item.get('name') or '')
            for change, items in resolved
            if change.scope == 'global'
            and change.resource_type == 'tool'
            and change.operation != 'use'
            for item in items
            if not bool(item.get('can_disable', False))
        ]
        if blocked_tools:
            raise ActionMessage(
                f'工具“{"、".join(blocked_tools)}”不能被全局关闭，配置没有改变。'
            )

    def turn_options(
        self,
        resolved: ResolvedChanges,
        base_dataset_ids: list[str],
    ) -> ChatOptions:
        options = ChatOptions()
        for change, items in resolved:
            if change.scope != 'turn':
                continue
            if change.resource_type == 'knowledge_base':
                if change.operation == 'use':
                    options.mentions.extend(
                        self._client.mention('knowledge_base', item) for item in items
                    )
                else:
                    remaining = list(base_dataset_ids)
                    if change.operation == 'clear':
                        remaining = []
                    else:
                        removed = {str(item.get('id') or '') for item in items}
                        remaining = [
                            value for value in remaining if value not in removed
                        ]
                    options.filters = {'kb_id': remaining}
            elif change.resource_type == 'skill':
                options.mentions.extend(
                    self._client.mention('skill', item) for item in items
                )
            elif change.resource_type == 'tool':
                if change.operation == 'use':
                    options.mentions.extend(
                        self._client.mention('tool', item) for item in items
                    )
                else:
                    options.disabled_tools.extend(
                        str(item.get('id') or '') for item in items
                    )
            elif change.resource_type == 'personalization':
                options.use_memory = change.operation == 'use'
        return options

    def new_conversation_options(
        self,
        resolved: ResolvedChanges,
        default_dataset_ids: list[str],
    ) -> ChatOptions:
        conversation_pairs = [
            pair
            for pair in resolved
            if pair[0].scope == 'conversation'
            and pair[0].resource_type == 'knowledge_base'
        ]
        dataset_ids = self._apply_dataset_changes(
            default_dataset_ids,
            conversation_pairs,
        )
        options = self.turn_options(resolved, dataset_ids)
        options.search_config = self.search_config(dataset_ids)
        return options

    def conversation_dataset_ids(
        self,
        *,
        owner_user_id: str,
        conversation_id: str,
        request_id: str,
    ) -> list[str]:
        detail = self._client.get_conversation_detail(
            owner_user_id=owner_user_id,
            conversation_id=conversation_id,
            request_id=request_id,
        )
        search = detail.get('search_config')
        if not isinstance(search, dict):
            return []
        values = search.get('dataset_list')
        if not isinstance(values, list):
            return []
        return [
            str(item.get('id') or '')
            for item in values
            if isinstance(item, dict) and item.get('id')
        ]

    @staticmethod
    def default_dataset_ids(catalog: dict[str, Any]) -> list[str]:
        values = catalog.get('knowledge_base')
        if not isinstance(values, list):
            return []
        return [
            str(item.get('id') or '')
            for item in values
            if isinstance(item, dict)
            and bool(item.get('default', False))
            and item.get('id')
        ]

    @staticmethod
    def search_config(dataset_ids: list[str]) -> dict[str, Any]:
        return {
            'dataset_list': [{'id': value} for value in dict.fromkeys(dataset_ids)],
            'top_k': 3,
            'confidence': 0.5,
        }

    @staticmethod
    def merge_options(base: ChatOptions, override: ChatOptions) -> ChatOptions:
        mentions: dict[tuple[str, str], dict[str, str]] = {}
        for mention in [*base.mentions, *override.mentions]:
            key = (
                str(mention.get('type') or ''),
                str(mention.get('resource_id') or ''),
            )
            mentions[key] = mention
        disabled_tools = set(base.disabled_tools)
        for mention in override.mentions:
            if mention.get('type') == 'tool':
                disabled_tools.discard(str(mention.get('resource_id') or ''))
        for tool_id in override.disabled_tools:
            disabled_tools.add(tool_id)
            mentions.pop(('tool', tool_id), None)
        search_config = (
            override.search_config
            if override.search_config is not None
            else base.search_config
        )
        return ChatOptions(
            search_config=search_config,
            mentions=list(mentions.values()),
            workflow_mode=(
                override.workflow_mode
                if override.workflow_mode is not None
                else base.workflow_mode
            ),
            enable_workflow=(
                override.enable_workflow
                if override.enable_workflow is not None
                else base.enable_workflow
            ),
            use_memory=(
                override.use_memory
                if override.use_memory is not None
                else base.use_memory
            ),
            disabled_tools=list(disabled_tools),
            filters=override.filters if override.filters is not None else base.filters,
        )

    @staticmethod
    def options_to_dict(options: ChatOptions) -> dict[str, Any]:
        return {
            'search_config': options.search_config,
            'mentions': options.mentions,
            'workflow_mode': options.workflow_mode,
            'enable_workflow': options.enable_workflow,
            'use_memory': options.use_memory,
            'disabled_tools': options.disabled_tools,
            'filters': options.filters,
        }

    @staticmethod
    def options_from_dict(value: dict[str, Any]) -> ChatOptions:
        search_config = value.get('search_config')
        mentions = value.get('mentions')
        disabled_tools = value.get('disabled_tools')
        filters = value.get('filters')
        workflow_mode = value.get('workflow_mode')
        return ChatOptions(
            search_config=search_config if isinstance(search_config, dict) else None,
            mentions=(
                [dict(item) for item in mentions if isinstance(item, dict)]
                if isinstance(mentions, list)
                else []
            ),
            workflow_mode=(
                workflow_mode
                if workflow_mode in {'auto', 'dynamic'}
                else None
            ),
            enable_workflow=(
                value.get('enable_workflow')
                if isinstance(value.get('enable_workflow'), bool)
                else None
            ),
            use_memory=(
                value.get('use_memory')
                if isinstance(value.get('use_memory'), bool)
                else None
            ),
            disabled_tools=(
                [str(item) for item in disabled_tools if str(item)]
                if isinstance(disabled_tools, list)
                else []
            ),
            filters=filters if isinstance(filters, dict) else None,
        )

    @staticmethod
    def dataset_names(value: Any, catalog: dict[str, Any]) -> list[str]:
        ids = (
            {
                str(item.get('id') or '')
                for item in value
                if isinstance(item, dict)
            }
            if isinstance(value, list)
            else set()
        )
        datasets = catalog.get('knowledge_base')
        return (
            [
                str(item.get('name') or '')
                for item in datasets
                if isinstance(item, dict) and str(item.get('id') or '') in ids
            ]
            if isinstance(datasets, list)
            else []
        )

    @staticmethod
    def _apply_dataset_changes(
        current: list[str],
        pairs: ResolvedChanges,
    ) -> list[str]:
        values = list(dict.fromkeys(current))
        for change, items in pairs:
            selected = [
                str(item.get('id') or '') for item in items if item.get('id')
            ]
            if change.operation == 'use':
                values = list(dict.fromkeys([*values, *selected]))
            elif change.operation == 'clear':
                values = []
            else:
                removed = set(selected)
                values = [value for value in values if value not in removed]
        return values

    @staticmethod
    def _normalize(value: str) -> str:
        return re.sub(r'\s+', ' ', value.strip()).casefold()

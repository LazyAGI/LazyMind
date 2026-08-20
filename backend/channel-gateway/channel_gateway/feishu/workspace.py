from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any, Literal

from channel_gateway.common.domain.commands import ASSISTANT_PROVIDERS


WorkspaceView = Literal[
    'chat',
    'capabilities',
    'conversations',
    'assistant',
    'settings',
]
_VIEWS = {
    'chat',
    'capabilities',
    'conversations',
    'assistant',
    'settings',
}
_RESOURCE_TYPES = (
    'knowledge_base',
    'skill',
    'workflow',
    'tool',
)
_CAPABILITY_OVERVIEW_TYPES = (
    'knowledge_base',
    'skill',
    'workflow',
    'tool',
)
_CAPABILITY_CATEGORIES = (
    'cloud_document',
    'knowledge_base',
    'skill',
    'workflow',
    'tool',
    'prompt',
)
_MAX_WORKSPACE_IMAGES = 6
_MAX_CAPABILITY_ITEMS_PER_GROUP = 6
_CAPABILITY_PAGE_SIZE = 10
_CAPABILITY_LABELS = {
    'cloud_document': '云文档',
    'knowledge_base': '知识库',
    'skill': 'Skill',
    'workflow': 'Workflow',
    'tool': 'Tool',
    'prompt': 'Prompt',
}
def _assistant_display_name(assistant: str) -> str:
    return {
        'lazymind': 'LazyMind',
        'codex': 'Codex',
        'cursor': 'Cursor',
        'workbuddy': 'WorkBuddy',
    }.get(assistant, 'LazyMind')


def _capability_command(
    evidence: str,
    capabilities: tuple[str, ...] = _CAPABILITY_OVERVIEW_TYPES,
) -> dict[str, Any]:
    return {
        'schema_version': '1',
        'command': 'capability.list',
        'parameters': {
            'capabilities': list(capabilities),
            'evidence': [evidence],
        },
    }


def _cloud_document_list_command(
    evidence: str,
    page_token: str = '',
) -> dict[str, Any]:
    return {
        'schema_version': '1',
        'command': 'cloud_document.list',
        'parameters': {
            'page_token': page_token,
            'evidence': [evidence],
        },
    }


def _cloud_document_get_command(
    evidence: str,
    source_id: str,
    page_token: str = '',
    node_ref: str = '',
    target_type: str = '',
    target_ref: str = '',
) -> dict[str, Any]:
    return {
        'schema_version': '1',
        'command': 'cloud_document.get',
        'parameters': {
            'source_id': source_id,
            'page_token': page_token,
            'node_ref': node_ref,
            'target_type': target_type,
            'target_ref': target_ref,
            'evidence': [evidence],
        },
    }


def _cloud_document_search_command(
    evidence: str,
    source_id: str,
    query: str,
    page_token: str = '',
) -> dict[str, Any]:
    return {
        'schema_version': '1',
        'command': 'cloud_document.search',
        'parameters': {
            'source_id': source_id,
            'query': query,
            'page_token': page_token,
            'evidence': [evidence],
        },
    }


MENU_EVENT_VIEWS = {
    'lazymind_capabilities': 'capabilities',
    'lazymind_conversations': 'conversations',
    'lazymind_settings': 'settings',
    'lazymind_assistant': 'assistant',
}


def _history_command(
    evidence: str,
    assistant: str = 'lazymind',
) -> dict[str, Any]:
    return {
        'schema_version': '1',
        'command': 'conversation.list',
        'parameters': {
            'assistant': (
                assistant if assistant in ASSISTANT_PROVIDERS else 'lazymind'
            ),
            'evidence': [evidence],
        },
    }


def _conversation_settings_command(evidence: str) -> dict[str, Any]:
    return {
        'schema_version': '1',
        'command': 'conversation.settings',
        'parameters': {
            'section': 'executor',
            'evidence': [evidence],
        },
    }


def menu_command(
    view: str,
    assistant: str = 'lazymind',
) -> dict[str, Any] | None:
    if view == 'capabilities':
        return _capability_command('查看能力')
    if view == 'conversations':
        return _history_command('切换会话', assistant)
    if view == 'assistant':
        return _history_command('查看助理', assistant)
    if view == 'settings':
        return _conversation_settings_command('查看设置')
    return None


def stale_workspace_card(language: str = 'zh') -> dict[str, Any]:
    return {
        'schema': '2.0',
        'config': {'wide_screen_mode': True},
        'header': {
            'title': {'tag': 'plain_text', 'content': 'LazyMind'},
            'template': 'grey',
        },
        'body': {
            'elements': [{
                'tag': 'markdown',
                'content': (
                    'This card has expired. Use the latest LazyMind card.'
                    if language == 'en'
                    else '这张卡片已过期，请使用会话中最新的 LazyMind 卡片。'
                ),
            }],
        },
    }


def _localized(state: FeishuWorkspaceState, zh: str, en: str) -> str:
    return en if state.output_language == 'en' else zh


def is_feishu_image_key(value: Any) -> bool:
    """Reject URLs and local paths before they can poison a CardKit card."""
    key = str(value or '').strip()
    return bool(
        key
        and len(key) <= 1024
        and not any(character.isspace() for character in key)
        and '/' not in key
        and '?' not in key
        and '://' not in key
    )


@dataclass(slots=True)
class FeishuWorkspaceState:
    view: WorkspaceView = 'chat'
    message_id: str = ''
    revision: int = 0
    capability_category: str = ''
    capability_page: int = 0
    assistant: str = 'lazymind'
    thinking_depth: str = 'medium'
    output_language: str = 'zh'
    show_sources: bool = True
    new_session_open: bool = False
    active_operation_id: str = ''
    images: list[dict[str, str]] = field(default_factory=list)

    @classmethod
    def from_dict(cls, value: Any) -> FeishuWorkspaceState:
        raw = value if isinstance(value, dict) else {}
        view = str(raw.get('view') or 'chat')
        category = str(raw.get('capability_category') or '')
        return cls(
            view=view if view in _VIEWS else 'chat',
            message_id=str(raw.get('message_id') or ''),
            revision=max(0, _integer(raw.get('revision'))),
            capability_category=(
                category
                if category in _CAPABILITY_CATEGORIES
                else ''
            ),
            capability_page=max(0, _integer(raw.get('capability_page'))),
            assistant=(
                str(raw.get('assistant'))
                if str(raw.get('assistant')) in ASSISTANT_PROVIDERS
                else 'lazymind'
            ),
            thinking_depth=(
                str(raw.get('thinking_depth'))
                if str(raw.get('thinking_depth'))
                in {'low', 'medium', 'high', 'max'}
                else 'medium'
            ),
            output_language=(
                str(raw.get('output_language'))
                if str(raw.get('output_language')) in {'zh', 'en'}
                else 'zh'
            ),
            show_sources=bool(raw.get('show_sources', True)),
            new_session_open=bool(raw.get('new_session_open', False)),
            active_operation_id=str(raw.get('active_operation_id') or ''),
            images=_workspace_images(raw.get('images')),
        )

    def to_dict(self) -> dict[str, Any]:
        return {
            'view': self.view,
            'message_id': self.message_id,
            'revision': self.revision,
            'capability_category': self.capability_category,
            'capability_page': self.capability_page,
            'assistant': self.assistant,
            'thinking_depth': self.thinking_depth,
            'output_language': self.output_language,
            'show_sources': self.show_sources,
            'new_session_open': self.new_session_open,
            'active_operation_id': self.active_operation_id,
            'images': _workspace_images(self.images),
        }

    def advance(self) -> None:
        self.revision += 1

    def bind_message(self, message_id: str) -> None:
        if not self.message_id and message_id:
            self.message_id = message_id

    def begin_operation(self, operation_id: str) -> None:
        self.active_operation_id = operation_id
        self.images = []
        self.new_session_open = False

    def navigate(
        self,
        view: str,
    ) -> None:
        if view not in {
            'chat',
            'capabilities',
            'conversations',
            'assistant',
            'settings',
        }:
            return
        self.view = view
        self.new_session_open = False
        if view == 'capabilities':
            self.capability_category = ''
            self.capability_page = 0

    def open_new_session(self) -> None:
        self.view = 'conversations'
        self.new_session_open = True

    def prepare_new_session(self) -> None:
        self.images = []
        self.new_session_open = False
        self.view = 'conversations'

    def reset_preferences(self) -> None:
        self.thinking_depth = 'medium'
        self.output_language = 'zh'
        self.show_sources = True

    def add_image(
        self,
        *,
        image_key: str,
        caption: str = '',
        identity: str = '',
        delivery_id: str = '',
    ) -> None:
        if not is_feishu_image_key(image_key):
            raise ValueError('Invalid Feishu image key')
        images = {
            str(item.get('identity') or item.get('image_key') or ''): item
            for item in self.images
        }
        images[identity or image_key] = {
            'image_key': image_key,
            'caption': caption[:300],
            'identity': (identity or image_key)[:512],
            'delivery_id': delivery_id[:512],
        }
        self.images = list(images.values())[-_MAX_WORKSPACE_IMAGES:]

    def open_capability_category(
        self,
        *,
        category: str,
    ) -> None:
        if category in _CAPABILITY_CATEGORIES:
            self.capability_category = category
        self.capability_page = 0
        self.view = 'capabilities'


class FeishuWorkspaceRenderer:
    @classmethod
    def render(
        cls,
        *,
        provider_context: dict[str, Any],
        presentations: list[dict[str, Any]],
        streaming: bool = False,
    ) -> dict[str, Any]:
        state = FeishuWorkspaceState.from_dict(
            provider_context.get('workspace_state')
        )
        result_complete = (
            provider_context.get('_workspace_result_complete') is True
        )
        chat_id = str(provider_context.get('chat_id') or '')
        elements: list[dict[str, Any]] = []
        notice = str(provider_context.get('_workspace_notice') or '').strip()
        if notice:
            elements.append({
                'tag': 'markdown',
                'content': notice[:2000],
            })
            elements.append({'tag': 'hr'})
        if state.view == 'chat':
            elements.append({
                'tag': 'markdown',
                'content': _localized(
                    state,
                    '已返回对话。请直接发送消息继续。',
                    'Back to chat. Send a message to continue.',
                ),
            })
        elif state.view == 'capabilities':
            elements.extend(
                cls._capabilities(
                    state,
                    presentations,
                    chat_id,
                    conversation_id=str(
                        provider_context.get('workspace_conversation_id')
                        or ''
                    ),
                    new_conversation_pending=(
                        provider_context.get('new_conversation_pending')
                        is True
                    ),
                    result_complete=result_complete,
                )
            )
        elif state.view == 'conversations':
            elements.extend(
                cls._conversation_history(
                    state,
                    presentations,
                    chat_id,
                    provider_context,
                    result_complete=result_complete,
                )
            )
        elif state.view == 'assistant':
            elements.extend(
                cls._assistant(
                    state,
                    presentations,
                    chat_id,
                    conversation_id=str(
                        provider_context.get('workspace_conversation_id')
                        or ''
                    ),
                    result_complete=result_complete,
                )
            )
        elif state.view == 'settings':
            elements.extend(
                cls._settings(
                    state,
                    presentations,
                    chat_id,
                    conversation_id=str(
                        provider_context.get('workspace_conversation_id')
                        or ''
                    ),
                    result_complete=result_complete,
                )
            )
        return {
            'schema': '2.0',
            'config': {
                'wide_screen_mode': True,
                'streaming_mode': streaming,
                'update_multi': True,
                'streaming_config': {
                    'print_frequency_ms': {
                        'default': 20,
                        'android': 20,
                        'ios': 20,
                        'pc': 20,
                    },
                    'print_step': {
                        'default': 4,
                        'android': 4,
                        'ios': 4,
                        'pc': 4,
                    },
                    'print_strategy': 'fast',
                },
                'summary': {
                    'content': (
                        'LazyMind is loading'
                        if streaming and state.output_language == 'en'
                        else 'LazyMind 正在加载'
                        if streaming
                        else 'LazyMind menu'
                        if state.output_language == 'en'
                        else 'LazyMind 菜单'
                    ),
                },
            },
            'header': {
                'title': {
                    'tag': 'plain_text',
                    'content': 'LazyMind',
                },
                'template': 'blue',
            },
            'body': {'elements': elements},
        }

    @staticmethod
    def _capabilities(
        state: FeishuWorkspaceState,
        presentations: list[dict[str, Any]],
        chat_id: str,
        *,
        conversation_id: str,
        new_conversation_pending: bool,
        result_complete: bool,
    ) -> list[dict[str, Any]]:
        has_conversation = bool(conversation_id)
        if state.capability_category:
            return FeishuWorkspaceRenderer._capability_catalog(
                state,
                presentations,
                chat_id,
                conversation_id=conversation_id,
                result_complete=result_complete,
            )
        groups = _capability_groups(presentations)
        settings = next(
            (
                item
                for item in presentations
                if item.get('kind') == 'conversation_settings'
            ),
            {},
        )
        selected = {
            (str(group.get('resource_type') or ''), str(item.get('id') or ''))
            for group in groups
            for item in (
                group.get('items')
                if isinstance(group.get('items'), list)
                else []
            )
            if isinstance(item, dict)
            and item.get('id')
            and item.get('enabled') is True
        }
        if has_conversation or new_conversation_pending:
            selected = {
                value for value in selected if value[0] != 'knowledge_base'
            }
            selected.update({
                ('knowledge_base', str(item))
                for item in (
                    settings.get('dataset_ids')
                    if isinstance(settings.get('dataset_ids'), list)
                    else []
                )
                if str(item)
            })
        workflow_count = sum(
            item_type == 'workflow' for item_type, _ in selected
        )
        workflow_mode = (
            str(settings.get('workflow_mode') or 'dynamic')
            if has_conversation or new_conversation_pending
            else 'dynamic'
        )
        subagent_enabled = bool(settings.get('subagent_enabled', True))
        workflow_action = _setting_action(
            chat_id,
            {
                'setting': 'workflow_mode',
                'mode': 'dynamic' if workflow_mode == 'auto' else 'auto',
            },
            '切换 Workflow 运行方式',
            view='capabilities',
            expected_revision=state.revision,
            expected_operation_id=state.active_operation_id,
            expected_conversation_id=conversation_id,
        )
        selection_scope_note = _localized(
            state,
            (
                '知识库按本会话生效；其他能力按账号生效'
                if has_conversation
                else '知识库作为新会话默认；其他能力按账号生效'
            ),
            (
                'Knowledge bases apply to this conversation; other capabilities apply to the account'
                if has_conversation
                else 'Knowledge bases become the new-conversation default; other capabilities apply to the account'
            ),
        )
        elements: list[dict[str, Any]] = [
            _heading_action(
                title=(
                    _localized(state, '会话与能力', 'Conversation and capabilities')
                    if has_conversation
                    else _localized(state, '新会话能力', 'New conversation capabilities')
                ),
                description=(
                    _localized(
                        state,
                        '知识库只影响本会话；Skill、Workflow 和 Tool 是账号能力。',
                        'Knowledge bases affect this conversation; Skills, '
                        'Workflows and Tools are account capabilities.',
                    )
                    if has_conversation
                    else _localized(
                        state,
                        '选择知识库和执行能力；新会话会直接继承。',
                        'Choose knowledge bases and execution capabilities for new conversations.',
                    )
                ),
                button={
                    'label': (
                        _localized(state, '刷新列表', 'Refresh')
                        if has_conversation
                        else _localized(
                            state,
                            '设置会自动保存',
                            'Settings save automatically',
                        )
                        if new_conversation_pending
                        else _localized(
                            state,
                            '刷新列表',
                            'Refresh',
                        )
                    ),
                    'style': 'default',
                    'action': (
                        _command_action(
                            chat_id=chat_id,
                            text='刷新能力配置',
                            command=_capability_command('刷新能力配置'),
                            workspace_action={
                                'kind': 'navigate',
                                'view': 'capabilities',
                                'expected_view': state.view,
                                'expected_revision': state.revision,
                                'expected_operation_id': state.active_operation_id,
                            },
                        )
                        if has_conversation
                        else _command_action(
                            chat_id=chat_id,
                            text='刷新能力列表',
                            command=_capability_command('刷新能力列表'),
                            workspace_action={
                                'kind': 'navigate',
                                'view': 'capabilities',
                                'expected_view': state.view,
                                'expected_revision': state.revision,
                                'expected_operation_id': state.active_operation_id,
                            },
                        )
                    ),
                },
            ),
            {
                'tag': 'markdown',
                'content': (
                    f'<font color="grey">{selection_scope_note}</font>'
                ),
            },
        ]
        elements.extend([
            {
                'tag': 'markdown',
                'content': _localized(
                    state,
                    '**会话资源**　<font color="grey">只影响当前会话</font>'
                    if has_conversation
                    else '**新会话默认**　<font color="grey">首条消息会使用这些资源</font>',
                    '**Conversation resources**　<font color="grey">Only affects this conversation</font>'
                    if has_conversation
                    else '**New-conversation defaults**　<font color="grey">Used by the first message</font>',
                ),
            },
        ])
        if not groups:
            elements.append(
                {
                    'tag': 'markdown',
                    'content': _localized(
                        state,
                        (
                            '<font color="grey">暂无可用能力。</font>'
                            if result_complete
                            else '<font color="grey">正在同步能力列表…</font>'
                        ),
                        (
                            '<font color="grey">No capabilities are available.</font>'
                            if result_complete
                            else '<font color="grey">Syncing capabilities…</font>'
                        ),
                    ),
                }
            )
        account_section_added = False
        for group in groups:
            items = group.get('items')
            values = items if isinstance(items, list) else []
            resource_type = str(group.get('resource_type') or '')
            if resource_type != 'knowledge_base' and not account_section_added:
                elements.extend([
                    {'tag': 'hr'},
                    {
                        'tag': 'markdown',
                        'content': _localized(
                            state,
                            '**账号能力**　<font color="grey">在所有会话中可用</font>',
                            '**Account capabilities**　<font color="grey">Available in all conversations</font>',
                        ),
                    },
                ])
                account_section_added = True
            label = _localized(
                state,
                str(group.get('label') or '能力'),
                {
                    'knowledge_base': 'Conversation knowledge bases',
                    'skill': 'Available Skills',
                    'workflow': 'Available Workflows',
                    'tool': 'Available Tools',
                }.get(resource_type, 'Capabilities'),
            )
            option_elements: list[dict[str, Any]] = []
            visible_values = values[:_MAX_CAPABILITY_ITEMS_PER_GROUP]
            for start in range(0, len(visible_values), 2):
                buttons: list[dict[str, Any]] = []
                for item in visible_values[start:start + 2]:
                    if not isinstance(item, dict) or not item.get('id'):
                        continue
                    item_id = str(item.get('id') or '')
                    is_selected = (resource_type, item_id) in selected
                    action = _resource_toggle_action(
                        chat_id=chat_id,
                        state=state,
                        category=resource_type,
                        item=item,
                        enabled=not is_selected,
                        conversation_id=conversation_id,
                    )
                    buttons.append({
                        'label': (
                            f'{"✓" if is_selected else "＋"} '
                            f'{str(item.get("name") or "未命名")[:28]}'
                        ),
                        'style': 'primary' if is_selected else 'default',
                        'action': action,
                    })
                if not buttons:
                    continue
                option_elements.append(
                    _button_row(buttons)
                )
            elements.extend(
                [
                    {
                        'tag': 'markdown',
                        'content': (
                            f'**{label}**　'
                            f'<font color="blue">'
                            f'{sum(item_type == resource_type for item_type, _ in selected)} '
                            f'{_localized(state, "项已选", "selected")}'
                            '</font>'
                        ),
                    },
                    *(
                        option_elements
                        or [
                            {
                                'tag': 'markdown',
                                'content': (
                                    '<font color="grey">'
                                    + _localized(
                                        state,
                                        '当前分类暂无可用资源。',
                                        'No resources are available in this category.',
                                    )
                                    + '</font>'
                                ),
                            }
                        ]
                    ),
                ]
            )
            if resource_type == 'workflow':
                workflow_note = (
                    _localized(
                        state,
                        (
                            f'已选择 {workflow_count} 个 Workflow，即为启用；'
                            '清空选择即关闭。'
                        ),
                        (
                            f'{workflow_count} Workflow(s) selected; '
                            'clear the selection to disable Workflow.'
                        ),
                    )
                    if workflow_count
                    else _localized(
                        state,
                        '未选择 Workflow，当前为关闭状态。',
                        'No Workflow is selected, so Workflow is disabled.',
                    )
                )
                elements.append({
                    'tag': 'markdown',
                    'content': f'<font color="grey">{workflow_note}</font>',
                })
                if workflow_count and (has_conversation or new_conversation_pending):
                    elements.extend([
                        {
                            'tag': 'markdown',
                            'content': _localized(
                                state,
                                '**Workflow 运行方式**',
                                '**Workflow execution mode**',
                            ),
                        },
                        _button_row([
                            {
                                'label': _localized(
                                    state,
                                    '自动连续运行'
                                    if workflow_mode == 'auto'
                                    else '每步完成后等待',
                                    'Run continuously'
                                    if workflow_mode == 'auto'
                                    else 'Wait after each step',
                                ),
                                'style': (
                                    'primary'
                                    if workflow_mode == 'auto'
                                    else 'default'
                                ),
                                'action': workflow_action,
                            }
                        ]),
                        {
                            'tag': 'markdown',
                            'content': _localized(
                                state,
                                (
                                    '<font color="grey">每步等待会在当前步骤完成后暂停，'
                                    '由你的下一条消息继续；自动运行会连续执行后续步骤。</font>'
                                ),
                                (
                                    '<font color="grey">Wait mode pauses after each step; '
                                    'continuous mode proceeds automatically.</font>'
                                ),
                            ),
                        },
                    ])
                elif workflow_count:
                    elements.append({
                        'tag': 'markdown',
                        'content': _localized(
                            state,
                            '<font color="grey">新建会话后可设置运行方式。</font>',
                            '<font color="grey">Create a conversation to set the execution mode.</font>',
                        ),
                    })
        elements.extend([
            {'tag': 'hr'},
            {
                'tag': 'markdown',
                'content': _localized(
                    state,
                    (
                        '**自主子任务（SubAgent）**\n'
                        '<font color="grey">Workflow 是你选择的固定流程；'
                        '自主子任务让模型临时拆分并委派任务。两者相互独立，'
                        '启用 Workflow 不会自动启用自主子任务。</font>'
                    ),
                    (
                        '**Autonomous subtasks (SubAgent)**\n'
                        '<font color="grey">Workflows are selected reusable flows; '
                        'SubAgent lets the model split and delegate tasks. They are '
                        'independent, so enabling Workflow does not enable SubAgent.</font>'
                    ),
                ),
            },
        ])
        if has_conversation:
            elements.append(
                _button_row([
                    {
                        'label': _localized(
                            state,
                            '自主子任务：已开启'
                            if subagent_enabled
                            else '自主子任务：已关闭',
                            'Autonomous subtasks: on'
                            if subagent_enabled
                            else 'Autonomous subtasks: off',
                        ),
                        'style': 'primary' if subagent_enabled else 'default',
                        'action': _setting_action(
                            chat_id,
                            {
                                'setting': 'subagent',
                                'enabled': not subagent_enabled,
                            },
                            '切换自主子任务',
                            view='capabilities',
                            expected_revision=state.revision,
                            expected_operation_id=state.active_operation_id,
                            expected_conversation_id=conversation_id,
                        ),
                    }
                ])
            )
        else:
            elements.append({
                'tag': 'markdown',
                'content': _localized(
                    state,
                    '<font color="grey">新会话会沿用 Core 中的账号默认设置。</font>',
                    '<font color="grey">New conversations use the account default from Core.</font>',
                ),
            })
        elements.extend([
            {'tag': 'hr'},
            {
                'tag': 'markdown',
                'content': _localized(
                    state,
                    '<font color="grey">目录页可浏览完整账号能力与 Prompt。</font>',
                    '<font color="grey">Open the catalog to browse all '
                    'account capabilities and prompts.</font>',
                ),
            },
            _button_row([
                {
                    'label': _localized(
                        state,
                        '管理全部能力',
                        'Manage all capabilities',
                    ),
                    'style': 'default',
                    'action': _capability_catalog_action(
                        chat_id,
                        state,
                        kind='capability.open',
                        category='knowledge_base',
                    ),
                }
            ]),
        ])
        return elements

    @staticmethod
    def _assistant(
        state: FeishuWorkspaceState,
        presentations: list[dict[str, Any]],
        chat_id: str,
        *,
        conversation_id: str,
        result_complete: bool,
    ) -> list[dict[str, Any]]:
        refresh_text = '刷新助理与会话'
        elements: list[dict[str, Any]] = [
            _heading_action(
                title=_localized(state, '选择助理', 'Choose assistant'),
                description=_localized(
                    state,
                    '选择会话来源，再进入该助理已有会话继续任务；不会修改执行引擎。',
                    'Choose a conversation source, then continue one of that assistant’s existing conversations. This does not change the execution engine.',
                ),
                button={
                    'label': _localized(state, '刷新列表', 'Refresh'),
                    'style': 'default',
                    'action': _command_action(
                        chat_id=chat_id,
                        text=refresh_text,
                        command=_history_command(
                            refresh_text,
                            state.assistant,
                        ),
                        workspace_action={
                            'kind': 'navigate',
                            'view': 'assistant',
                            'expected_view': state.view,
                            'expected_revision': state.revision,
                            'expected_operation_id': state.active_operation_id,
                        },
                    ),
                },
            ),
        ]
        catalog = next(
            (
                item
                for item in presentations
                if item.get('kind') == 'assistant_catalog'
            ),
            {},
        )
        assistants = [
            item
            for item in (
                catalog.get('assistants')
                if isinstance(catalog.get('assistants'), list)
                else []
            )[:8]
            if isinstance(item, dict)
            and str(item.get('id') or '') in ASSISTANT_PROVIDERS
        ]
        if not assistants:
            elements.append({
                'tag': 'markdown',
                'content': _localized(
                    state,
                    (
                        '<font color="grey">暂无可选择的助理。</font>'
                        if result_complete
                        else '<font color="grey">正在同步助理与会话…</font>'
                    ),
                    (
                        '<font color="grey">No assistants are available.</font>'
                        if result_complete
                        else '<font color="grey">Syncing assistants and conversations…</font>'
                    ),
                ),
            })
            return elements
        unavailable: list[str] = []
        for start in range(0, len(assistants), 2):
            buttons: list[dict[str, Any]] = []
            for assistant in assistants[start:start + 2]:
                assistant_id = str(assistant.get('id') or '')
                display_name = str(
                    assistant.get('display_name') or assistant_id
                )[:100]
                selected = assistant_id == state.assistant
                buttons.append({
                    'label': f'{"✓" if selected else "＋"} {display_name}',
                    'style': 'primary' if selected else 'default',
                    'action': _assistant_select_action(
                        chat_id,
                        state,
                        assistant_id=assistant_id,
                        display_name=display_name,
                    ),
                })
                if assistant.get('available') is not True:
                    reason = str(
                        assistant.get('unavailable_reason')
                        or '当前离线，可查看历史但暂时不能继续执行'
                    )[:160]
                    unavailable.append(f'{display_name}：{reason}')
            elements.append(_button_row(buttons))
        if unavailable:
            elements.append({
                'tag': 'markdown',
                'content': '<font color="grey">' + '<br>'.join(unavailable) + '</font>',
            })
        conversation = next(
            (
                item
                for item in presentations
                if item.get('kind') == 'conversation'
            ),
            {},
        )
        if conversation.get('state') == 'switched':
            elements.append({
                'tag': 'markdown',
                'content': _localized(
                    state,
                    f'✅ 已进入 **{str(conversation.get("title") or "所选会话")}**，可以直接发送消息继续。',
                    f'✅ Opened **{str(conversation.get("title") or "the selected conversation")}**. Send a message to continue.',
                ),
            })
        elif conversation_id:
            elements.append({
                'tag': 'markdown',
                'content': _localized(
                    state,
                    '<font color="grey">选择下方会话后，当前飞书聊天会切换到对应上下文。</font>',
                    '<font color="grey">Choose a conversation below to switch this Feishu chat to that context.</font>',
                ),
            })
        elements.extend(
            FeishuWorkspaceRenderer._selection(
                presentations,
                chat_id,
                workspace_action={
                    'kind': 'history.switch',
                    'view': 'assistant',
                    'expected_view': state.view,
                    'expected_revision': state.revision,
                    'expected_operation_id': state.active_operation_id,
                },
                empty=_localized(
                    state,
                    f'<font color="grey">{_assistant_display_name(state.assistant)} 暂无可继续的历史会话。</font>',
                    f'<font color="grey">No existing {_assistant_display_name(state.assistant)} conversations are available.</font>',
                ),
            )
        )
        return elements

    @staticmethod
    def _conversation_history(
        state: FeishuWorkspaceState,
        presentations: list[dict[str, Any]],
        chat_id: str,
        provider_context: dict[str, Any],
        *,
        result_complete: bool,
    ) -> list[dict[str, Any]]:
        loading = not presentations and not result_complete
        conversation = next(
            (
                item
                for item in presentations
                if item.get('kind') == 'conversation'
            ),
            {},
        )
        conversation_state = str(conversation.get('state') or '')
        conversation_title = ' '.join(
            str(conversation.get('title') or '').split()
        )[:200]
        selection_action = provider_context.get('selection_action')
        selection_action = (
            selection_action if isinstance(selection_action, dict) else {}
        )
        switch_index = str(selection_action.get('index') or '')
        workspace_action = provider_context.get('workspace_action')
        workspace_action = (
            workspace_action if isinstance(workspace_action, dict) else {}
        )
        switching = (
            workspace_action.get('kind') == 'history.switch'
            and conversation_state != 'switched'
            and not provider_context.get('_history_switch_expired')
        )
        panel_elements: list[dict[str, Any]] = [
            _heading_action(
                title=_localized(
                    state,
                    f'切换 {_assistant_display_name(state.assistant)} 会话',
                    f'Switch {_assistant_display_name(state.assistant)} conversation',
                ),
                description=_localized(
                    state,
                    '会话列表跟随当前助理；选择后继续原上下文，不会新建或合并会话。',
                    'The list follows the selected assistant. Choosing one resumes its original context without creating or merging conversations.',
                ),
                button=(
                    {
                        'label': _localized(state, '＋ 新建', '＋ New'),
                        'style': 'primary',
                        'action': _new_session_action(
                            chat_id,
                            state,
                            kind='new_session.open',
                        ),
                    }
                    if state.assistant == 'lazymind'
                    else {
                        'label': _localized(state, '刷新', 'Refresh'),
                        'style': 'default',
                        'action': _history_refresh_action(chat_id, state),
                    }
                ),
            ),
        ]
        if conversation_title:
            panel_elements.append(
                {
                    'tag': 'markdown',
                    'content': (
                        f'**{_localized(state, "当前会话", "Current conversation")}**　'
                        f'{conversation_title}'
                    ),
                }
            )
        if provider_context.get('_history_switch_expired'):
            panel_elements.append({
                'tag': 'markdown',
                'content': _localized(
                    state,
                    '⚠️ **会话列表已更新，请刷新后重新选择**',
                    '⚠️ **The conversation list changed. Refresh and choose again.**',
                ),
            })
            panel_elements.append(
                _button_row(
                    [
                        {
                            'label': _localized(
                                state,
                                '刷新会话列表',
                                'Refresh conversations',
                            ),
                            'style': 'primary',
                            'action': _history_refresh_action(chat_id, state),
                        }
                    ]
                )
            )
        elif switching:
            panel_elements.append({
                'tag': 'markdown',
                'content': _localized(
                    state,
                    (
                        f'⏳ **正在切换到第 {switch_index} 个会话…**'
                        if switch_index
                        else '⏳ **正在切换会话…**'
                    ),
                    (
                        f'⏳ **Switching to conversation {switch_index}…**'
                        if switch_index
                        else '⏳ **Switching conversation…**'
                    ),
                ),
            })
        elif conversation_state == 'switched':
            panel_elements.append({
                'tag': 'markdown',
                'content': _localized(
                    state,
                    '✅ **所选会话已生效**',
                    '✅ **The selected conversation is now active**',
                ),
            })
        if state.new_session_open and state.assistant == 'lazymind':
            panel_elements.extend(
                [
                    {
                        'tag': 'markdown',
                        'content': _localized(
                            state,
                            '**新建会话起点**',
                            '**New conversation starting point**',
                        ),
                    },
                    {
                        'tag': 'markdown',
                        'content': _localized(
                            state,
                            '<font color="grey">创建空白会话；发送第一条消息后正式建立。</font>',
                            '<font color="grey">Creates a blank conversation; '
                            'it becomes active after your first message.</font>',
                        ),
                    },
                    _button_row(
                        [
                            {
                                'label': _localized(state, '取消', 'Cancel'),
                                'style': 'default',
                                'action': _new_session_action(
                                    chat_id,
                                    state,
                                    kind='new_session.cancel',
                                ),
                            },
                            {
                                'label': _localized(state, '创建会话', 'Create'),
                                'style': 'primary',
                                'action': _new_session_action(
                                    chat_id,
                                    state,
                                    kind='new_session.create',
                                    create=True,
                                ),
                            },
                        ]
                    ),
                    {'tag': 'hr'},
                ]
            )
        if loading:
            panel_elements.append(
                _button_row(
                    [
                        {
                            'label': _localized(state, '同步会话', 'Sync conversations'),
                            'style': 'default',
                            'action': _history_refresh_action(chat_id, state),
                        }
                    ]
                )
            )
        panel_elements.extend(
            FeishuWorkspaceRenderer._selection(
                presentations,
                chat_id,
                workspace_action={
                    'kind': 'history.switch',
                    'view': 'conversations',
                    'expected_view': state.view,
                    'expected_revision': state.revision,
                    'expected_operation_id': state.active_operation_id,
                },
                selected_value=switch_index,
                loading=switching,
                empty=(
                    _localized(
                        state,
                        '<font color="grey">尚未同步历史会话。</font>',
                        '<font color="grey">Conversation history has not been synced.</font>',
                    )
                    if loading
                    else _localized(
                        state,
                        f'{_assistant_display_name(state.assistant)} 暂时没有历史会话。',
                        f'No {_assistant_display_name(state.assistant)} conversations yet.',
                    )
                ),
            )
        )
        return panel_elements

    @staticmethod
    def _settings(
        state: FeishuWorkspaceState,
        presentations: list[dict[str, Any]],
        chat_id: str,
        *,
        conversation_id: str,
        result_complete: bool,
    ) -> list[dict[str, Any]]:
        elements: list[dict[str, Any]] = [
            _heading_action(
                title=_localized(state, '设置', 'Settings'),
                description=_localized(
                    state,
                    '设置当前会话的执行引擎，以及飞书交互体验。',
                    'Configure the current conversation’s execution engine and Feishu experience.',
                ),
                button={
                    'label': _localized(state, '自动保存', 'Auto-saved'),
                    'style': 'default',
                    'disabled': True,
                    'action': _local_action(
                        chat_id=chat_id,
                        text='设置已自动保存',
                        workspace_action={
                            'kind': 'navigate',
                            'view': 'settings',
                            'expected_view': state.view,
                            'expected_revision': state.revision,
                            'expected_operation_id': state.active_operation_id,
                        },
                    ),
                },
            ),
        ]
        settings = next(
            (
                item
                for item in presentations
                if item.get('kind') == 'conversation_settings'
            ),
            {},
        )
        executors = [
            item
            for item in (
                settings.get('executors')
                if isinstance(settings.get('executors'), list)
                else []
            )[:8]
            if isinstance(item, dict)
            and str(item.get('id') or '')
            and str(item.get('display_name') or '')
        ]
        if conversation_id:
            elements.extend(
                _executor_setting_elements(
                    state=state,
                    chat_id=chat_id,
                    executors=executors,
                    selected=str(settings.get('chat_executor') or ''),
                    conversation_id=conversation_id,
                    expected_revision=state.revision,
                    expected_operation_id=state.active_operation_id,
                    view='settings',
                    result_complete=result_complete,
                )
            )
        else:
            elements.extend([
                {'tag': 'hr'},
                {
                    'tag': 'markdown',
                    'content': _localized(
                        state,
                        '<font color="grey">当前没有会话；发送消息或切换会话后可选择执行引擎。</font>',
                        '<font color="grey">There is no current conversation. Send a message or switch conversations before choosing an execution engine.</font>',
                    ),
                },
            ])
        elements.extend([
            {
                'tag': 'markdown',
                'content': _localized(
                    state,
                    '**思考深度**',
                    '**Thinking depth**',
                ),
            },
            *[
                _button_row(
                    [
                        {
                            'label': label,
                            'style': (
                                'primary'
                                if state.thinking_depth == value
                                else 'default'
                            ),
                            'action': _preference_action(
                                chat_id,
                                state,
                                'thinking_depth',
                                value,
                            ),
                        }
                        for label, value in row
                    ]
                )
                for row in (
                    (
                        (_localized(state, '简洁', 'Concise'), 'low'),
                        (_localized(state, '标准', 'Standard'), 'medium'),
                    ),
                    (
                        (_localized(state, '深入', 'In-depth'), 'high'),
                        (_localized(state, '极致（Max）', 'Maximum'), 'max'),
                    ),
                )
            ],
            {'tag': 'hr'},
            {
                'tag': 'markdown',
                'content': _localized(
                    state,
                    '**界面语言**',
                    '**Interface language**',
                ),
            },
            _button_row(
                [
                    {
                        'label': label,
                        'style': (
                            'primary'
                            if state.output_language == value
                            else 'default'
                        ),
                        'action': _preference_action(
                            chat_id,
                            state,
                            'output_language',
                            value,
                        ),
                    }
                    for label, value in (
                        ('中文', 'zh'),
                        ('English', 'en'),
                    )
                ]
            ),
            {'tag': 'hr'},
            {
                'tag': 'markdown',
                'content': _localized(
                    state,
                    '**呈现与通知**',
                    '**Presentation and notifications**',
                ),
            },
            _button_row(
                [
                    {
                        'label': (
                            _localized(state, '✓ 显示参考来源', '✓ Show sources')
                            if state.show_sources
                            else _localized(state, '＋ 显示参考来源', '＋ Show sources')
                        ),
                        'style': 'primary' if state.show_sources else 'default',
                        'action': _preference_action(
                            chat_id,
                            state,
                            'show_sources',
                            not state.show_sources,
                        ),
                    },
                ]
            ),
            {'tag': 'hr'},
            {
                'tag': 'markdown',
                'content': _localized(
                    state,
                    '**会话维护**',
                    '**Conversation maintenance**',
                ),
            },
            _button_row(
                [
                    {
                        'label': _localized(state, '恢复默认设置', 'Restore defaults'),
                        'style': 'danger',
                        'action': _maintenance_action(
                            chat_id,
                            state,
                            kind='maintenance.reset_preferences',
                        ),
                        'confirm': {
                            'title': _localized(
                                state,
                                '恢复默认设置？',
                                'Restore default settings?',
                            ),
                            'text': _localized(
                                state,
                                '将覆盖当前体验设置并恢复系统默认值；能力选择不受影响。',
                                (
                                    'Resets experience settings without changing '
                                    'capability selections.'
                                ),
                            ),
                        },
                    },
                ]
            ),
            _button_row(
                [
                    {
                        'label': _localized(state, '开始空白新会话', 'Start a blank conversation'),
                        'style': 'danger_filled',
                        'action': _maintenance_action(
                            chat_id,
                            state,
                            kind='maintenance.new_conversation',
                            create=True,
                        ),
                        'confirm': {
                            'title': _localized(
                                state,
                                '开始空白新会话？',
                                'Start a blank conversation?',
                            ),
                            'text': _localized(
                                state,
                                (
                                    '将离开当前会话并等待你的第一条新消息；'
                                    '原会话仍可从历史列表切回，已有后台任务不受影响。'
                                ),
                                (
                                    'Leaves the current conversation and waits for your first new message. '
                                    'The old conversation remains in history and existing background tasks continue.'
                                ),
                            ),
                        },
                    }
                ]
            ),
        ])
        return elements

    @staticmethod
    def _capability_catalog(
        state: FeishuWorkspaceState,
        presentations: list[dict[str, Any]],
        chat_id: str,
        *,
        conversation_id: str,
        result_complete: bool,
    ) -> list[dict[str, Any]]:
        selected = _selected_capabilities(presentations)
        selected_summary = _localized(
            state,
            f'{len(selected)} 项已选',
            f'{len(selected)} selected',
        )
        elements: list[dict[str, Any]] = [
            {
                'tag': 'markdown',
                'content': (
                    _localized(
                        state,
                        '**管理能力**　',
                        '**Manage capabilities**　',
                    )
                    + f'<font color="blue">{selected_summary}</font>\n'
                    + _localized(
                        state,
                        '<font color="grey">点击选项切换，设置会自动保存。</font>',
                        '<font color="grey">Toggle an item below; settings save automatically.</font>',
                    )
                ),
            },
            {'tag': 'hr'},
        ]
        categories = [
            (
                category,
                _localized(
                    state,
                    label,
                    {
                        'cloud_document': 'Cloud documents',
                        'knowledge_base': 'Knowledge bases',
                        'skill': 'Skills',
                        'workflow': 'Workflows',
                        'tool': 'Tools',
                        'prompt': 'Prompts',
                    }[category],
                ),
            )
            for category, label in _CAPABILITY_LABELS.items()
        ]
        for start in range(0, len(categories), 3):
            elements.append(
                _button_row(
                    [
                        {
                            'label': label,
                            'style': (
                                'primary'
                                if state.capability_category == category
                                else 'default'
                            ),
                            'action': _capability_catalog_action(
                                chat_id,
                                state,
                                kind='capability.open',
                                category=category,
                            ),
                        }
                        for category, label in categories[start:start + 3]
                    ]
                )
            )
        if state.capability_category == 'cloud_document':
            elements.extend(
                _cloud_document_elements(
                    state,
                    presentations,
                    chat_id,
                )
            )
            return elements
        group = next(
            (
                group
                for group in _capability_groups(presentations)
                if group.get('resource_type') == state.capability_category
            ),
            {},
        )
        items = group.get('items')
        values = items if isinstance(items, list) else []
        page_count = max(
            1,
            (len(values) + _CAPABILITY_PAGE_SIZE - 1)
            // _CAPABILITY_PAGE_SIZE,
        )
        page = min(state.capability_page, page_count - 1)
        page_start = page * _CAPABILITY_PAGE_SIZE
        page_values = values[
            page_start:page_start + _CAPABILITY_PAGE_SIZE
        ]
        elements.append({'tag': 'hr'})
        if state.capability_category == 'prompt':
            elements.append(
                {
                    'tag': 'markdown',
                    'content': _localized(
                        state,
                        '<font color="grey">点击 Prompt 会直接作为一条新消息发送。</font>',
                        '<font color="grey">Selecting a prompt sends it as a new message.</font>',
                    ),
                }
            )
        if not values:
            elements.append(
                {
                    'tag': 'markdown',
                    'content': _localized(
                        state,
                        (
                            '当前分类暂无可用资源。'
                            if result_complete
                            else '正在同步当前分类…'
                        ),
                        (
                            'No resources are available in this category.'
                            if result_complete
                            else 'Syncing this category…'
                        ),
                    ),
                }
            )
        else:
            for start in range(0, len(page_values), 2):
                elements.append(
                    _button_row(
                        [
                            {
                                'label': (
                                    '▶ '
                                    + str(
                                        item.get('name')
                                        or _localized(
                                            state,
                                            '未命名',
                                            'Untitled',
                                        )
                                    )[:32]
                                    if state.capability_category == 'prompt'
                                    else (
                                        (
                                            '✓ '
                                            if (
                                                state.capability_category,
                                                str(item.get('id') or ''),
                                            ) in selected
                                            else '＋ '
                                        )
                                        + str(
                                            item.get('name')
                                            or _localized(
                                                state,
                                                '未命名',
                                                'Untitled',
                                            )
                                        )[:32]
                                    )
                                ),
                                'style': (
                                    'primary'
                                    if (
                                        state.capability_category != 'prompt'
                                        and (
                                            state.capability_category,
                                            str(item.get('id') or ''),
                                        )
                                        in selected
                                    )
                                    else 'default'
                                ),
                                'action': (
                                    _prompt_action(chat_id, state, item)
                                    if state.capability_category == 'prompt'
                                    else _capability_toggle_action(
                                        chat_id,
                                        state,
                                        item,
                                        conversation_id=conversation_id,
                                        enabled=(
                                            (
                                                state.capability_category,
                                                str(item.get('id') or ''),
                                            )
                                            not in selected
                                        ),
                                    )
                                ),
                            }
                            for item in page_values[start:start + 2]
                            if isinstance(item, dict) and item.get('id')
                        ]
                    )
                )
        if page_count > 1:
            elements.append(
                _button_row(
                    [
                        {
                            'label': _localized(state, '上一页', 'Previous'),
                            'style': 'default',
                            'disabled': page == 0,
                            'action': _capability_page_action(
                                chat_id,
                                state,
                                page - 1,
                                state.capability_category,
                            ),
                        },
                        {
                            'label': f'{page + 1} / {page_count}',
                            'style': 'default',
                            'disabled': True,
                            'action': _capability_page_action(
                                chat_id,
                                state,
                                page,
                                state.capability_category,
                            ),
                        },
                        {
                            'label': _localized(state, '下一页', 'Next'),
                            'style': 'default',
                            'disabled': page + 1 == page_count,
                            'action': _capability_page_action(
                                chat_id,
                                state,
                                page + 1,
                                state.capability_category,
                            ),
                        },
                    ]
                )
            )
        elements.extend(
            [
                {'tag': 'hr'},
                _button_row([{
                    'label': _localized(
                        state,
                        f'返回 · 已保存 {len(selected)} 项',
                        f'Back · {len(selected)} saved',
                    ),
                    'style': 'primary',
                    'action': _command_action(
                        chat_id=chat_id,
                        text='返回能力',
                        command=_capability_command('返回能力'),
                        workspace_action={
                            'kind': 'navigate',
                            'view': 'capabilities',
                            'expected_view': state.view,
                            'expected_revision': state.revision,
                            'expected_operation_id': state.active_operation_id,
                        },
                    ),
                }]),
            ]
        )
        return elements

    @staticmethod
    def _selection(
        presentations: list[dict[str, Any]],
        chat_id: str,
        *,
        empty: str = '',
        workspace_action: dict[str, Any] | None = None,
        selected_value: str = '',
        loading: bool = False,
    ) -> list[dict[str, Any]]:
        selection = next(
            (
                item
                for item in presentations
                if item.get('kind') == 'selection'
            ),
            None,
        )
        if not isinstance(selection, dict):
            return [{'tag': 'markdown', 'content': empty}] if empty else []
        options = selection.get('options')
        values = options if isinstance(options, list) else []
        elements: list[dict[str, Any]] = []
        for start in range(0, len(values), 2):
            row = values[start:start + 2]
            elements.append(
                _button_row(
                    [
                        {
                            'label': (
                                f'{"⏳" if loading else "✓"} {start + offset + 1}. '
                                f'{str(option.get("label") or "")}'
                                if str(start + offset + 1) == selected_value
                                else (
                                    f'{start + offset + 1}. '
                                    f'{str(option.get("label") or "")}'
                                )
                            )[:40],
                            'style': (
                                'primary'
                                if str(start + offset + 1) == selected_value
                                else 'default'
                            ),
                            'disabled': loading,
                            'action': {
                                'lazymind_action': 'select',
                                'selection_id': str(
                                    selection.get('selection_id') or ''
                                ),
                                'selection': str(start + offset + 1),
                                'text': str(start + offset + 1),
                                'intended_chat_id': chat_id,
                                'workspace_action': {
                                    **dict(
                                        workspace_action
                                        or {
                                            'kind': 'navigate',
                                            'view': 'chat',
                                        }
                                    ),
                                    **(
                                        {
                                            'target_conversation_id': str(
                                                option.get('value') or ''
                                            )[:512]
                                        }
                                        if (
                                            (workspace_action or {}).get('kind')
                                            == 'history.switch'
                                        )
                                        else {}
                                    ),
                                },
                            },
                        }
                        for offset, option in enumerate(row)
                        if isinstance(option, dict)
                    ]
                )
            )
        return elements


def _workspace_images(value: Any) -> list[dict[str, str]]:
    images: list[dict[str, str]] = []
    for item in value if isinstance(value, list) else []:
        if not isinstance(item, dict):
            continue
        image_key = str(item.get('image_key') or '')
        if not is_feishu_image_key(image_key):
            continue
        images.append(
            {
                'image_key': image_key[:1024],
                'caption': str(item.get('caption') or '')[:300],
                'identity': str(
                    item.get('identity') or image_key
                )[:512],
                'delivery_id': str(item.get('delivery_id') or '')[:512],
            }
        )
    return images[-_MAX_WORKSPACE_IMAGES:]


def _integer(value: Any) -> int:
    try:
        return int(value)
    except (TypeError, ValueError):
        return 0


def _button(
    item: dict[str, Any],
    *,
    width: str = 'fill',
    size: str = 'medium',
) -> dict[str, Any]:
    result = {
        'tag': 'button',
        'text': {
            'tag': 'plain_text',
            'content': str(item.get('label') or ''),
        },
        'type': str(item.get('style') or 'default'),
        'size': str(item.get('size') or size),
        'width': str(item.get('width') or width),
        'value': dict(item.get('action') or {}),
    }
    confirm = item.get('confirm')
    if isinstance(confirm, dict):
        result['confirm'] = {
            'title': {
                'tag': 'plain_text',
                'content': str(confirm.get('title') or '确认操作？'),
            },
            'text': {
                'tag': 'plain_text',
                'content': str(confirm.get('text') or ''),
            },
        }
    if bool(item.get('disabled')):
        result['disabled'] = True
    return result


def _button_row(items: list[dict[str, Any]]) -> dict[str, Any]:
    return {
        'tag': 'column_set',
        'flex_mode': 'none',
        'horizontal_spacing': '8px',
        'columns': [
            {
                'tag': 'column',
                'width': 'weighted',
                'weight': 1,
                'elements': [_button(item)],
            }
            for item in items
        ],
    }


def _heading_action(
    *,
    title: str,
    description: str,
    button: dict[str, Any],
) -> dict[str, Any]:
    return {
        'tag': 'column_set',
        'flex_mode': 'none',
        'vertical_align': 'top',
        'columns': [
            {
                'tag': 'column',
                'width': 'weighted',
                'weight': 4,
                'elements': [{
                    'tag': 'markdown',
                    'content': (
                        f'**{title}**\n'
                        f'<font color="grey">{description}</font>'
                    ),
                }],
            },
            {
                'tag': 'column',
                'width': 'auto',
                'elements': [
                    _button(button, width='default', size='small')
                ],
            },
        ],
    }


def _command_action(
    *,
    chat_id: str,
    text: str,
    command: dict[str, Any],
    workspace_action: dict[str, Any],
) -> dict[str, Any]:
    return {
        'lazymind_action': 'command',
        'text': text,
        'intended_chat_id': chat_id,
        'command_action': command,
        'workspace_action': workspace_action,
    }


def _local_action(
    *,
    chat_id: str,
    text: str,
    workspace_action: dict[str, Any],
) -> dict[str, Any]:
    return {
        'lazymind_action': 'local',
        'text': text,
        'intended_chat_id': chat_id,
        'workspace_action': workspace_action,
    }


def _history_refresh_action(
    chat_id: str,
    state: FeishuWorkspaceState,
) -> dict[str, Any]:
    return _command_action(
        chat_id=chat_id,
        text='同步历史会话',
        command=_history_command('同步历史会话', state.assistant),
        workspace_action={
            'kind': 'history.open',
            'view': state.view,
            'expected_view': state.view,
            'expected_revision': state.revision,
            'expected_operation_id': state.active_operation_id,
        },
    )


def _assistant_select_action(
    chat_id: str,
    state: FeishuWorkspaceState,
    *,
    assistant_id: str,
    display_name: str,
) -> dict[str, Any]:
    return _command_action(
        chat_id=chat_id,
        text=f'选择 {display_name} 助理',
        command=_history_command(
            f'选择 {display_name} 助理',
            assistant_id,
        ),
        workspace_action={
            'kind': 'assistant.select',
            'assistant': assistant_id,
            'view': 'assistant',
            'expected_view': state.view,
            'expected_revision': state.revision,
            'expected_operation_id': state.active_operation_id,
        },
    )


def _capability_catalog_action(
    chat_id: str,
    state: FeishuWorkspaceState,
    *,
    kind: str,
    category: str,
    page: int | None = None,
) -> dict[str, Any]:
    workspace_action: dict[str, Any] = {
        'kind': kind,
        'category': category,
        'expected_view': state.view,
        'expected_revision': state.revision,
        'expected_operation_id': state.active_operation_id,
    }
    if page is not None:
        workspace_action['page'] = max(0, page)
    text = f'查看{_CAPABILITY_LABELS.get(category, "知识库")}'
    command = (
        _cloud_document_list_command(text)
        if category == 'cloud_document'
        else _capability_command(text, (category,))
    )
    return _command_action(
        chat_id=chat_id,
        text=text,
        command=command,
        workspace_action=workspace_action,
    )


def _capability_toggle_action(
    chat_id: str,
    state: FeishuWorkspaceState,
    item: dict[str, Any],
    *,
    conversation_id: str,
    enabled: bool,
) -> dict[str, Any]:
    return _resource_toggle_action(
        chat_id=chat_id,
        state=state,
        category=state.capability_category,
        item=item,
        enabled=enabled,
        conversation_id=conversation_id,
    )


def _capability_page_action(
    chat_id: str,
    state: FeishuWorkspaceState,
    page: int,
    category: str,
) -> dict[str, Any]:
    return _capability_catalog_action(
        chat_id,
        state,
        kind='capability.page',
        category=category,
        page=page,
    )


def _resource_toggle_action(
    *,
    chat_id: str,
    state: FeishuWorkspaceState,
    category: str,
    item: dict[str, Any],
    enabled: bool,
    conversation_id: str,
) -> dict[str, Any]:
    item_id = str(item.get('id') or '')
    change = {
        'knowledge_base': {
            'setting': 'knowledge_base',
            'dataset_id': item_id,
            'enabled': enabled,
        },
        'skill': {
            'setting': 'skill',
            'skill_id': item_id,
            'enabled': enabled,
        },
        'workflow': {
            'setting': 'workflow',
            'workflow_ref': item_id,
            'enabled': enabled,
        },
        'tool': {
            'setting': 'tool',
            'tool_name': item_id,
            'enabled': enabled,
        },
    }.get(category)
    if change is None:
        raise ValueError(f'Unsupported capability category: {category}')
    label = str(item.get('name') or _CAPABILITY_LABELS[category])[:100]
    return _setting_action(
        chat_id,
        change,
        f'{"启用" if enabled else "关闭"}{label}',
        view='capabilities',
        expected_revision=state.revision,
        expected_operation_id=state.active_operation_id,
        expected_conversation_id=conversation_id,
    )


def _new_session_action(
    chat_id: str,
    state: FeishuWorkspaceState,
    *,
    kind: str,
    create: bool = False,
) -> dict[str, Any]:
    if not create:
        return _command_action(
            chat_id=chat_id,
            text='设置新会话起点',
            command=_history_command('设置新会话起点'),
            workspace_action={
                'kind': kind,
                'expected_view': state.view,
                'expected_revision': state.revision,
                'expected_operation_id': state.active_operation_id,
            },
        )
    return _command_action(
        chat_id=chat_id,
        text='创建会话',
        command={
            'schema_version': '1',
            'command': 'conversation.new',
            'parameters': {
                'message': '',
                'evidence': ['创建会话'],
            },
        },
        workspace_action={
            'kind': kind,
            'expected_view': state.view,
            'expected_revision': state.revision,
            'expected_operation_id': state.active_operation_id,
        },
    )


def _maintenance_action(
    chat_id: str,
    state: FeishuWorkspaceState,
    *,
    kind: str,
    create: bool = False,
) -> dict[str, Any]:
    if not create:
        return _local_action(
            chat_id=chat_id,
            text='确认会话维护操作',
            workspace_action={
                'kind': kind,
                'expected_view': state.view,
                'expected_revision': state.revision,
                'expected_operation_id': state.active_operation_id,
            },
        )
    return _command_action(
        chat_id=chat_id,
        text='确认会话维护操作',
        command={
            'schema_version': '1',
            'command': 'conversation.new',
            'parameters': {
                'message': '',
                'evidence': ['确认会话维护操作'],
            },
        },
        workspace_action={
            'kind': kind,
            'expected_view': state.view,
            'expected_revision': state.revision,
            'expected_operation_id': state.active_operation_id,
        },
    )


def _prompt_action(
    chat_id: str,
    state: FeishuWorkspaceState,
    item: dict[str, Any],
) -> dict[str, Any]:
    content = (
        str(item.get('content') or '').strip()
        or str(item.get('name') or '').strip()
        or '使用 Prompt'
    )
    return _command_action(
        chat_id=chat_id,
        text=content,
        command={
            'schema_version': '1',
            'command': 'chat',
            'parameters': {
                'message': content,
            },
        },
        workspace_action={
            'kind': 'prompt.run',
            'expected_view': state.view,
            'expected_revision': state.revision,
            'expected_operation_id': state.active_operation_id,
        },
    )


def _preference_action(
    chat_id: str,
    state: FeishuWorkspaceState,
    name: str,
    value: Any,
) -> dict[str, Any]:
    return _local_action(
        chat_id=chat_id,
        text='更新呈现设置',
        workspace_action={
            'kind': 'preference',
            'name': name,
            'value': value,
            'expected_view': state.view,
            'expected_revision': state.revision,
            'expected_operation_id': state.active_operation_id,
        },
    )


def _executor_setting_elements(
    *,
    state: FeishuWorkspaceState,
    chat_id: str,
    executors: list[dict[str, Any]],
    selected: str,
    conversation_id: str,
    expected_revision: int,
    expected_operation_id: str,
    view: str = 'capabilities',
    result_complete: bool = False,
) -> list[dict[str, Any]]:
    elements: list[dict[str, Any]] = [
        {'tag': 'hr'},
        {
            'tag': 'markdown',
            'content': _localized(
                state,
                '**执行引擎**　<font color="grey">决定当前会话下一轮由谁完成任务；各 Agent 保留各自的一对一会话映射</font>',
                '**Execution engine**　<font color="grey">Choose who executes the next turn; every Agent keeps its own one-to-one conversation mapping</font>',
            ),
        },
    ]
    if not executors:
        elements.append({
            'tag': 'markdown',
            'content': _localized(
                state,
                (
                    '<font color="grey">暂无可用执行器。</font>'
                    if result_complete
                    else '<font color="grey">正在同步执行器状态…</font>'
                ),
                (
                    '<font color="grey">No executors are available.</font>'
                    if result_complete
                    else '<font color="grey">Syncing executor status…</font>'
                ),
            ),
        })
        return elements
    unavailable: list[str] = []
    for start in range(0, len(executors), 2):
        buttons: list[dict[str, Any]] = []
        for executor in executors[start:start + 2]:
            executor_id = str(executor.get('id') or '')[:64]
            display_name = str(
                executor.get('display_name') or executor_id
            )[:100]
            available = executor.get('available') is True
            is_selected = executor_id == selected
            buttons.append({
                'label': f'{"✓" if is_selected else "＋"} {display_name}',
                'style': 'primary' if is_selected else 'default',
                'disabled': not available,
                'action': _setting_action(
                    chat_id,
                    {
                        'setting': 'executor',
                        'executor_id': executor_id,
                    },
                    f'使用 {display_name} 执行当前会话',
                    view=view,
                    expected_revision=expected_revision,
                    expected_operation_id=expected_operation_id,
                    expected_conversation_id=conversation_id,
                ),
            })
            if not available:
                reason = str(
                    executor.get('unavailable_reason') or '当前不可用'
                )[:160]
                unavailable.append(f'{display_name}：{reason}')
        elements.append(_button_row(buttons))
    if unavailable:
        elements.append({
            'tag': 'markdown',
            'content': '<font color="grey">' + '<br>'.join(unavailable) + '</font>',
        })
    return elements


def _setting_action(
    chat_id: str,
    change: dict[str, Any],
    text: str,
    *,
    view: str = 'settings',
    expected_revision: int,
    expected_operation_id: str,
    expected_conversation_id: str,
) -> dict[str, Any]:
    parameters: dict[str, Any] = {
        'change': change,
        'evidence': [text],
    }
    if expected_conversation_id:
        parameters['expected_conversation_id'] = expected_conversation_id
    return _command_action(
        chat_id=chat_id,
        text=text,
        command={
            'schema_version': '1',
            'command': 'conversation.settings.update',
            'parameters': parameters,
        },
        workspace_action={
            'kind': 'setting.update',
            'view': view,
            'expected_view': view,
            'expected_revision': expected_revision,
            'expected_operation_id': expected_operation_id,
            'expected_conversation_id': expected_conversation_id,
        },
    )


def _cloud_document_elements(
    state: FeishuWorkspaceState,
    presentations: list[dict[str, Any]],
    chat_id: str,
) -> list[dict[str, Any]]:
    presentation = next(
        (
            item
            for item in presentations
            if item.get('kind') == 'cloud_document'
        ),
        {},
    )
    if not presentation:
        return [{
            'tag': 'markdown',
            'content': _localized(
                state,
                '<font color="grey">正在读取 LazyMind 已授权云文档账号…</font>',
                '<font color="grey">Loading cloud accounts connected to LazyMind…</font>',
            ),
        }]
    mode = str(presentation.get('mode') or 'sources')
    raw_items = presentation.get('items')
    items = [
        dict(item)
        for item in (raw_items if isinstance(raw_items, list) else [])
        if isinstance(item, dict)
    ]
    source = (
        dict(presentation['source'])
        if isinstance(presentation.get('source'), dict)
        else {}
    )
    if mode == 'sources':
        elements = _cloud_source_elements(state, chat_id, items)
    else:
        elements = _cloud_document_page_elements(
            state,
            chat_id,
            mode,
            source,
            items,
        )
    next_page_token = str(presentation.get('next_page_token') or '')
    if next_page_token:
        elements.append(_cloud_next_page_element(
            state,
            chat_id,
            mode,
            source,
            str(presentation.get('query') or ''),
            next_page_token,
        ))
    return elements


def _cloud_source_elements(
    state: FeishuWorkspaceState,
    chat_id: str,
    items: list[dict[str, Any]],
) -> list[dict[str, Any]]:
    elements: list[dict[str, Any]] = [{
        'tag': 'markdown',
        'content': _localized(
            state,
            '**已授权云文档账号**　<font color="grey">在线只读，不扫盘、不建索引</font>',
            '**Connected cloud accounts**　<font color="grey">Online read-only; no scan or index</font>',
        ),
    }]
    if not items:
        elements.append({
            'tag': 'markdown',
            'content': _localized(
                state,
                '<font color="grey">暂无已授权并启用对话的云文档账号。</font>',
                '<font color="grey">No authorized cloud account is enabled for chat.</font>',
            ),
        })
    for item in items:
        source_id = str(item.get('id') or '')
        if not source_id:
            continue
        name = str(item.get('name') or source_id)[:80]
        text = f'浏览云文档账号 {name}'
        elements.append(_button_row([{
            'label': name,
            'action': _command_action(
                chat_id=chat_id,
                text=text,
                command=_cloud_document_get_command(text, source_id),
                workspace_action=_cloud_workspace_action(state),
            ),
        }]))
    return elements


def _cloud_document_page_elements(
    state: FeishuWorkspaceState,
    chat_id: str,
    mode: str,
    source: dict[str, Any],
    items: list[dict[str, Any]],
) -> list[dict[str, Any]]:
    source_id = str(source.get('id') or '')
    source_name = str(source.get('name') or source_id or '云文档')[:100]
    suffix = '在线搜索结果' if mode == 'search' else '在线文档'
    elements: list[dict[str, Any]] = [
        {'tag': 'markdown', 'content': f'**{source_name} · {suffix}**'}
    ]
    for index, item in enumerate(items, start=1):
        name = str(
            item.get('display_name')
            or item.get('name')
            or item.get('key')
            or item.get('id')
            or '未命名'
        )[:160]
        kind = '目录' if item.get('has_children') else '文档'
        elements.append({
            'tag': 'markdown',
            'content': f'{index}. **{name}**　<font color="grey">{kind}</font>',
        })
        if item.get('has_children'):
            text = f'打开云文档目录 {name}'
            elements.append(_button_row([{
                'label': _localized(state, '打开', 'Open'),
                'action': _command_action(
                    chat_id=chat_id,
                    text=text,
                    command=_cloud_document_get_command(
                        text,
                        source_id,
                        node_ref=str(item.get('node_ref') or ''),
                        target_type=str(item.get('target_type') or ''),
                        target_ref=str(item.get('target_ref') or ''),
                    ),
                    workspace_action=_cloud_workspace_action(state),
                ),
            }]))
    back_text = '返回云文档账号'
    elements.append(_button_row([{
        'label': back_text,
        'action': _command_action(
            chat_id=chat_id,
            text=back_text,
            command=_cloud_document_list_command(back_text),
            workspace_action=_cloud_workspace_action(state),
        ),
    }]))
    return elements


def _cloud_next_page_element(
    state: FeishuWorkspaceState,
    chat_id: str,
    mode: str,
    source: dict[str, Any],
    query: str,
    page_token: str,
) -> dict[str, Any]:
    text = '下一页云文档'
    source_id = str(source.get('id') or '')
    if mode == 'documents':
        command = _cloud_document_get_command(
            text,
            source_id,
            page_token,
            node_ref=str(source.get('node_ref') or ''),
            target_type=str(source.get('target_type') or ''),
            target_ref=str(source.get('target_ref') or ''),
        )
    elif mode == 'search':
        command = _cloud_document_search_command(
            text,
            source_id,
            query,
            page_token,
        )
    else:
        command = _cloud_document_list_command(text, page_token)
    return _button_row([{
        'label': _localized(state, '下一页', 'Next page'),
        'style': 'primary',
        'action': _command_action(
            chat_id=chat_id,
            text=text,
            command=command,
            workspace_action=_cloud_workspace_action(state),
        ),
    }])


def _cloud_workspace_action(
    state: FeishuWorkspaceState,
) -> dict[str, Any]:
    return {
        'kind': 'capability.open',
        'category': 'cloud_document',
        'expected_view': state.view,
        'expected_revision': state.revision,
        'expected_operation_id': state.active_operation_id,
    }


def _capability_groups(
    presentations: list[dict[str, Any]],
) -> list[dict[str, Any]]:
    presentation = next(
        (
            item
            for item in presentations
            if item.get('kind') == 'capability'
        ),
        {},
    )
    groups = presentation.get('groups')
    return [
        dict(group)
        for group in (groups if isinstance(groups, list) else [])
        if isinstance(group, dict)
    ]


def _selected_capabilities(
    presentations: list[dict[str, Any]],
) -> set[tuple[str, str]]:
    selected = {
        (resource_type, resource_id)
        for group in _capability_groups(presentations)
        if (resource_type := str(group.get('resource_type') or ''))
        in _RESOURCE_TYPES
        for value in (
            group.get('items')
            if isinstance(group.get('items'), list)
            else []
        )[:100]
        if isinstance(value, dict) and value.get('enabled') is True
        if (resource_id := str(value.get('id') or '').strip())
        and len(resource_id) <= 512
    }
    settings = next(
        (
            item
            for item in presentations
            if item.get('kind') == 'conversation_settings'
        ),
        {},
    )
    dataset_ids = settings.get('dataset_ids')
    if isinstance(dataset_ids, list):
        selected = {
            value for value in selected if value[0] != 'knowledge_base'
        }
        selected.update(
            ('knowledge_base', str(value)[:512])
            for value in dataset_ids[:100]
            if str(value)
        )
    return selected

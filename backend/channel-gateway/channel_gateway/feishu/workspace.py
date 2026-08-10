from __future__ import annotations

import re
from dataclasses import dataclass, field
from datetime import datetime
from pathlib import PurePath
from typing import Any, Literal

from channel_gateway.feishu.assistant import (
    assistant_ui_error,
    assistant_ui_status,
    detail_run_status,
    project_name,
)
from channel_gateway.feishu.cardkit import assistant_user_input_form


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
    'knowledge_base',
    'skill',
    'workflow',
    'tool',
    'prompt',
)
_MAX_WORKSPACE_IMAGES = 6
_MAX_CAPABILITY_ITEMS_PER_GROUP = 6
_CAPABILITY_PAGE_SIZE = 10
_ASSISTANT_ANSWER_PAGE_CHARS = 5000
_ASSISTANT_TURN_PAGE_SIZE = 1
_ASSISTANT_PROJECT_PAGE_SIZE = 6
_CAPABILITY_LABELS = {
    'knowledge_base': '知识库',
    'skill': 'Skill',
    'workflow': 'Workflow',
    'tool': 'Tool',
    'prompt': 'Prompt',
}


def _markdown_code(value: Any, language: str = '') -> str:
    text = str(value or '')
    longest = max(
        (len(run) for run in re.findall(r'`+', text)),
        default=0,
    )
    fence = '`' * max(3, longest + 1)
    return f'{fence}{language}\n{text}\n{fence}'


def _capability_command() -> dict[str, Any]:
    return {
        'schema_version': '1',
        'command': 'capability.list',
        'parameters': {
            'capabilities': list(_CAPABILITY_OVERVIEW_TYPES),
        },
    }


MENU_EVENT_VIEWS = {
    'lazymind_capabilities': 'capabilities',
    'lazymind_conversations': 'conversations',
    'lazymind_settings': 'settings',
    'lazymind_assistant': 'assistant',
}


def _history_command() -> dict[str, Any]:
    return {
        'schema_version': '1',
        'command': 'conversation.list',
        'parameters': {},
    }


def menu_command(view: str) -> dict[str, Any] | None:
    if view == 'capabilities':
        return _capability_command()
    if view == 'conversations':
        return _history_command()
    return None


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
    pending_workflow_mode: str = 'dynamic'
    thinking_depth: str = 'medium'
    output_language: str = 'zh'
    show_sources: bool = True
    new_session_open: bool = False
    active_operation_id: str = ''
    images: list[dict[str, str]] = field(default_factory=list)
    assistant_mode: str = 'projects'
    assistant_project_cwd: str = ''
    assistant_project_page: int = 0
    assistant_projects_cursor: str = ''
    assistant_projects_next_cursor: str = ''
    assistant_projects_previous_cursors: list[str] = field(default_factory=list)
    assistant_selected_thread_id: str = ''
    assistant_threads_cursor: str = ''
    assistant_threads_next_cursor: str = ''
    assistant_threads_previous_cursors: list[str] = field(default_factory=list)
    assistant_threads_page: int = 0
    assistant_answer_page: int = 0

    @classmethod
    def from_dict(cls, value: Any) -> FeishuWorkspaceState:
        raw = value if isinstance(value, dict) else {}
        view = str(raw.get('view') or 'chat')
        category = str(raw.get('capability_category') or '')
        assistant_mode = (
            str(raw.get('assistant_mode'))
            if str(raw.get('assistant_mode')) in {
                'projects',
                'sessions',
                'detail',
            }
            else 'projects'
        )
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
            pending_workflow_mode=(
                str(raw.get('pending_workflow_mode'))
                if str(raw.get('pending_workflow_mode')) in {'auto', 'dynamic'}
                else 'dynamic'
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
            assistant_mode=assistant_mode,
            assistant_project_cwd=str(
                raw.get('assistant_project_cwd') or ''
            )[:500],
            assistant_project_page=max(
                0,
                _integer(raw.get('assistant_project_page')),
            ),
            assistant_projects_cursor=str(
                raw.get('assistant_projects_cursor') or ''
            )[:512],
            assistant_projects_next_cursor=str(
                raw.get('assistant_projects_next_cursor') or ''
            )[:512],
            assistant_projects_previous_cursors=[
                str(item)[:512]
                for item in (
                    raw.get('assistant_projects_previous_cursors')
                    if isinstance(
                        raw.get('assistant_projects_previous_cursors'),
                        list,
                    )
                    else []
                )
                if isinstance(item, str)
            ][-100:],
            assistant_selected_thread_id=str(raw.get('assistant_selected_thread_id') or '')[:512],
            assistant_threads_cursor=str(
                raw.get('assistant_threads_cursor') or ''
            )[:512],
            assistant_threads_next_cursor=str(
                raw.get('assistant_threads_next_cursor') or ''
            )[:512],
            assistant_threads_previous_cursors=[
                str(item)[:512]
                for item in (
                    raw.get('assistant_threads_previous_cursors')
                    if isinstance(
                        raw.get('assistant_threads_previous_cursors'),
                        list,
                    )
                    else []
                )
                if isinstance(item, str)
            ][-100:],
            assistant_threads_page=max(
                0,
                _integer(raw.get('assistant_threads_page')),
            ),
            assistant_answer_page=max(
                0,
                _integer(raw.get('assistant_answer_page')),
            ),
        )

    def to_dict(self) -> dict[str, Any]:
        return {
            'view': self.view,
            'message_id': self.message_id,
            'revision': self.revision,
            'capability_category': self.capability_category,
            'capability_page': self.capability_page,
            'pending_workflow_mode': self.pending_workflow_mode,
            'thinking_depth': self.thinking_depth,
            'output_language': self.output_language,
            'show_sources': self.show_sources,
            'new_session_open': self.new_session_open,
            'active_operation_id': self.active_operation_id,
            'images': _workspace_images(self.images),
            'assistant_mode': self.assistant_mode,
            'assistant_project_cwd': self.assistant_project_cwd,
            'assistant_project_page': self.assistant_project_page,
            'assistant_projects_cursor': self.assistant_projects_cursor,
            'assistant_projects_next_cursor': self.assistant_projects_next_cursor,
            'assistant_projects_previous_cursors': list(
                self.assistant_projects_previous_cursors
            ),
            'assistant_selected_thread_id': self.assistant_selected_thread_id,
            'assistant_threads_cursor': self.assistant_threads_cursor,
            'assistant_threads_next_cursor': self.assistant_threads_next_cursor,
            'assistant_threads_previous_cursors': list(
                self.assistant_threads_previous_cursors
            ),
            'assistant_threads_page': self.assistant_threads_page,
            'assistant_answer_page': self.assistant_answer_page,
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

    def leave_assistant_thread(self) -> None:
        self.assistant_mode = 'sessions'
        self.assistant_selected_thread_id = ''
        self.assistant_answer_page = 0
        self.images = []

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
        assistant_view = provider_context.get('assistant_view')
        assistant_view = (
            dict(assistant_view) if isinstance(assistant_view, dict) else {}
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
            elements.extend(render_assistant(state, chat_id, assistant_view))
        elif state.view == 'settings':
            elements.extend(cls._settings(state, chat_id))
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
        if has_conversation:
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
            if has_conversation
            else state.pending_workflow_mode
        )
        subagent_enabled = bool(settings.get('subagent_enabled', True))
        workflow_action = (
            _setting_action(
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
            if has_conversation
            else _command_action(
                chat_id=chat_id,
                text='切换 Workflow 运行方式',
                command=_capability_command(),
                workspace_action={
                    'kind': 'new_session.workflow_mode',
                    'mode': (
                        'dynamic' if workflow_mode == 'auto' else 'auto'
                    ),
                    'expected_mode': workflow_mode,
                    'expected_view': state.view,
                    'expected_revision': state.revision,
                    'expected_operation_id': state.active_operation_id,
                },
            )
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
                            command=_capability_command(),
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
                            command=_capability_command(),
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
        ]
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
                        kind='capability.toggle',
                        category=resource_type,
                        item=item,
                        enabled=not is_selected,
                        expected_revision=state.revision,
                        expected_operation_id=state.active_operation_id,
                        catalog_only=False,
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
                title=_localized(state, '切换会话', 'Switch conversation'),
                description=_localized(
                    state,
                    '选择会话后，后续原生聊天会继续使用对应上下文与能力。',
                    'Select a conversation to continue with its context and capabilities.',
                ),
                button={
                    'label': _localized(state, '＋ 新建', '＋ New'),
                    'style': 'primary',
                    'action': _new_session_action(
                        chat_id,
                        state,
                        kind='new_session.open',
                    ),
                },
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
        if state.new_session_open:
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
                        '暂时没有历史会话。',
                        'No previous conversations yet.',
                    )
                ),
            )
        )
        return panel_elements

    @staticmethod
    def _settings(
        state: FeishuWorkspaceState,
        chat_id: str,
    ) -> list[dict[str, Any]]:
        elements = [
            _heading_action(
                title=_localized(state, '体验设置', 'Experience settings'),
                description=_localized(
                    state,
                    '控制 LazyMind 的思考、语言、呈现和会话行为。',
                    'Control LazyMind thinking, language, presentation, and conversation behavior.',
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
                    '**语言设置**',
                    '**Language settings**',
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
                        'label': _localized(state, '清空当前会话上下文', 'Clear conversation context'),
                        'style': 'danger_filled',
                        'action': _maintenance_action(
                            chat_id,
                            state,
                            kind='maintenance.clear_conversation',
                            create=True,
                        ),
                        'confirm': {
                            'title': _localized(
                                state,
                                '清空当前会话上下文？',
                                'Clear conversation context?',
                            ),
                            'text': _localized(
                                state,
                                (
                                    '将清除当前会话记忆与任务状态，后续回答不再引用'
                                    '当前会话内容。此操作不可撤销。'
                                ),
                                (
                                    'Clears current memory and task state. '
                                    'This cannot be undone.'
                                ),
                            ),
                        },
                    }
                ]
            ),
        ]
        return elements

    @staticmethod
    def _capability_catalog(
        state: FeishuWorkspaceState,
        presentations: list[dict[str, Any]],
        chat_id: str,
        *,
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
                        command=_capability_command(),
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


def render_assistant(
    state: Any,
    chat_id: str,
    assistant_view: dict[str, Any],
) -> list[dict[str, Any]]:
    if state.assistant_mode == 'detail':
        return _detail(state, chat_id, assistant_view)
    if state.assistant_mode == 'sessions':
        return _sessions(state, chat_id, assistant_view)
    return _projects(state, chat_id, assistant_view)


def _action(
    chat_id: str,
    kind: str,
    text: str,
    **values: Any,
) -> dict[str, Any]:
    return {
        'lazymind_action': 'local',
        'text': text,
        'intended_chat_id': chat_id,
        'workspace_action': {'kind': kind, **values},
    }


def _turn_pagination(
    state: Any,
    chat_id: str,
    current_page: int,
    page_count: int,
    action_values: dict[str, Any] | None = None,
) -> dict[str, Any]:
    current_page = min(max(0, current_page), max(0, page_count - 1))
    page_control = {
        'tag': 'markdown',
        'content': f'**{current_page + 1} / {page_count}**',
    }
    previous = _button({
        'label': _localized(state, '上一页', 'Previous'),
        'disabled': current_page == 0,
        'action': _action(
            chat_id,
            'assistant.turns_page',
            'Codex 上一页',
            direction='older',
            **(action_values or {}),
        ),
    })
    next_button = _button({
        'label': _localized(state, '下一页', 'Next'),
        'disabled': current_page >= page_count - 1,
        'action': _action(
            chat_id,
            'assistant.turns_page',
            'Codex 下一页',
            direction='newer',
            **(action_values or {}),
        ),
    })
    return {
        'tag': 'column_set',
        'flex_mode': 'none',
        'horizontal_spacing': '8px',
        'columns': [
            {
                'tag': 'column',
                'width': 'weighted',
                'weight': 1,
                'elements': [element],
            }
            for element in (previous, page_control, next_button)
        ],
    }


def _project_location(cwd: str) -> str:
    parts = PurePath(str(cwd or '').rstrip('/\\')).parts
    return '/'.join(parts[-2:]) if parts else '未归属项目'


def _time_label(value: Any) -> str:
    raw = str(value or '').strip()
    if not raw:
        return '—'
    try:
        return datetime.fromtimestamp(int(float(raw))).strftime(
            '%Y-%m-%d %H:%M'
        )
    except (TypeError, ValueError, OverflowError, OSError):
        return raw


def _source_label(value: Any) -> str:
    source = str(value or '').strip()
    return {
        'vscode': 'Codex Desktop',
        'cli': 'Codex CLI',
        'appserver': 'LazyMind',
    }.get(source.lower(), source or 'Codex')


def _error_text(state: Any, assistant_view: dict[str, Any]) -> str:
    error = assistant_ui_error(assistant_view).strip()
    normalized = error.lower()
    if '2001600' in normalized:
        return _localized(
            state,
            'Codex 已连接，但当前没有可展示的项目会话。',
            'Codex is connected, but no project sessions are visible.',
        )
    if (
        'codex assistant is not configured' in normalized
        or 'codex executable not found' in normalized
    ):
        return _localized(
            state,
            (
                '当前设备未找到可用 Codex。请先在本机安装并登录 Codex，'
                '然后返回飞书重试；其他 LazyMind 功能不受影响。'
            ),
            (
                'Codex is unavailable on this device. Install and sign in to '
                'Codex locally, then retry in Feishu. Other LazyMind features '
                'are unaffected.'
            ),
        )
    return error or _localized(state, '请重试', 'Please retry')


def _projects(
    state: Any,
    chat_id: str,
    assistant_view: dict[str, Any],
) -> list[dict[str, Any]]:
    ui_status = assistant_ui_status(assistant_view)
    projects = assistant_view.get('projects')
    projects = projects if isinstance(projects, list) else []
    unique_projects: list[dict[str, Any]] = []
    seen_cwds: set[str] = set()
    for project in projects:
        if not isinstance(project, dict):
            continue
        cwd = str(project.get('cwd') or '').rstrip('/\\')
        identity = cwd.casefold()
        if not cwd or identity in seen_cwds:
            continue
        seen_cwds.add(identity)
        unique_projects.append(project)
    projects = unique_projects
    project_names = [
        str(project.get('name') or project_name(str(project.get('cwd') or '')))
        for project in projects
    ]
    duplicate_names = {
        name.casefold()
        for name in project_names
        if sum(
            candidate.casefold() == name.casefold()
            for candidate in project_names
        ) > 1
    }
    total = max(0, _integer(assistant_view.get('total')))
    page_count = max(
        1,
        (total + _ASSISTANT_PROJECT_PAGE_SIZE - 1)
        // _ASSISTANT_PROJECT_PAGE_SIZE,
    )
    page = min(max(0, state.assistant_project_page), page_count - 1)
    status = (
        _localized(state, '● 已连接', '● Connected')
        if ui_status == 'ready'
        else _localized(state, '○ 正在检查连接', '○ Checking connection')
    )
    elements = [
        _heading_action(
            title=_localized(state, '选择 Codex 项目', 'Choose a Codex project'),
            description=_localized(
                state,
                '项目来自 Codex 原生会话的工作目录。',
                'Projects come from the working directories of native Codex sessions.',
            ),
            button={
                'label': _localized(state, '刷新', 'Refresh'),
                'action': _action(
                    chat_id,
                    'assistant.refresh',
                    '刷新 Codex 项目',
                ),
            },
        ),
        {
            'tag': 'markdown',
            'content': f'**Codex**　<font color="green">{status}</font>',
        },
        {'tag': 'hr'},
    ]
    if ui_status == 'error':
        elements.extend([
            {
                'tag': 'markdown',
                'content': f'<font color="red">同步失败：{_error_text(state, assistant_view)}</font>',
            },
            _button_row([{
                'label': _localized(state, '重试', 'Retry'),
                'action': _action(
                    chat_id,
                    'assistant.retry',
                    '重试同步 Codex 项目',
                ),
            }]),
        ])
    elif not projects:
        elements.append({
            'tag': 'markdown',
            'content': _localized(
                state,
                '<font color="grey">暂无包含会话的 Codex 项目。</font>',
                '<font color="grey">No Codex projects with sessions are available.</font>',
            ),
        })
    for project in projects:
        cwd = str(project.get('cwd') or '')
        count = int(project.get('thread_count') or 0)
        name = str(project.get('name') or project_name(cwd))
        parent_name = PurePath(cwd.rstrip('/\\')).parent.name
        display_name = (
            f'{name} · {parent_name}'
            if name.casefold() in duplicate_names
            else name
        )
        elements.extend([
            {
                'tag': 'markdown',
                'content': (
                    f'**{display_name}**\n'
                    f'<font color="grey">{_project_location(cwd)}</font>\n'
                    f'<font color="grey">{count} 个会话 · '
                    f'{_time_label(project.get("updated_at"))}</font>'
                ),
            },
            _button_row([{
                'label': _localized(state, '查看会话', 'View sessions'),
                'action': _action(
                    chat_id,
                    'assistant.project',
                    f'打开 Codex 项目 {display_name}',
                    project_cwd=cwd,
                    expected_projects_cursor=state.assistant_projects_cursor,
                    expected_project_page=state.assistant_project_page,
                ),
            }]),
        ])
    if page_count > 1:
        elements.extend([
            {
                'tag': 'markdown',
                'content': _localized(
                    state,
                    f'<font color="grey">第 {page + 1} 页 · 共 {total} 个项目</font>',
                    f'<font color="grey">Page {page + 1} · {total} projects</font>',
                ),
            },
            _button_row([
                {
                    'label': _localized(state, '上一页', 'Previous'),
                    'disabled': not state.assistant_projects_previous_cursors,
                    'action': _action(
                        chat_id,
                        'assistant.projects_page',
                        'Codex 项目上一页',
                        direction='previous',
                        expected_projects_cursor=state.assistant_projects_cursor,
                        expected_project_page=state.assistant_project_page,
                    ),
                },
                {
                    'label': _localized(state, '下一页', 'Next'),
                    'disabled': not state.assistant_projects_next_cursor,
                    'action': _action(
                        chat_id,
                        'assistant.projects_page',
                        'Codex 项目下一页',
                        direction='next',
                        expected_projects_cursor=state.assistant_projects_cursor,
                        expected_project_page=state.assistant_project_page,
                    ),
                },
            ]),
        ])
    return elements


def _sessions(
    state: Any,
    chat_id: str,
    assistant_view: dict[str, Any],
) -> list[dict[str, Any]]:
    ui_status = assistant_ui_status(assistant_view)
    sessions = assistant_view.get('threads')
    sessions = sessions if isinstance(sessions, list) else []
    status = (
        _localized(state, '● 已连接', '● Connected')
        if ui_status == 'ready'
        else _localized(state, '○ 正在检查连接', '○ Checking connection')
    )
    total = max(0, _integer(assistant_view.get('total')))
    elements = [
        _heading_action(
            title=project_name(state.assistant_project_cwd),
            description=_localized(
                state,
                '只显示该工作目录下的 Codex 原生会话。',
                'Only native Codex sessions in this working directory are shown.',
            ),
            button={
                'label': _localized(state, '返回项目', 'Back to projects'),
                'action': _action(
                    chat_id,
                    'assistant.projects',
                    '返回 Codex 项目',
                    expected_project_cwd=state.assistant_project_cwd,
                ),
            },
        ),
        {
            'tag': 'markdown',
            'content': (
                f'**Codex**　<font color="green">{status}</font>\n'
                '<font color="grey">'
                + _localized(
                    state,
                    '会话按最近更新时间排列；点击即可读取原生记录并继续。',
                    'Sessions are sorted by update time; open one to read and continue.',
                )
                + '</font>'
            ),
        },
        _button_row([{
            'label': _localized(state, '＋ 新建 Codex 会话', '＋ New Codex session'),
            'style': 'primary',
            'action': _action(
                chat_id,
                'assistant.new',
                f'在 {project_name(state.assistant_project_cwd)} 新建 Codex 会话',
                cwd=state.assistant_project_cwd,
                display_name=f'{project_name(state.assistant_project_cwd)} 会话',
                expected_project_cwd=state.assistant_project_cwd,
                expected_threads_cursor=state.assistant_threads_cursor,
                expected_threads_page=state.assistant_threads_page,
            ),
        }]),
        {'tag': 'hr'},
    ]
    if ui_status == 'error':
        elements.extend([
            {
                'tag': 'markdown',
                'content': f'<font color="red">同步失败：{_error_text(state, assistant_view)}</font>',
            },
            _button_row([{
                'label': _localized(state, '重试', 'Retry'),
                'action': _action(
                    chat_id,
                    'assistant.retry',
                    '重试同步 Codex 会话',
                ),
            }]),
        ])
    elif not sessions:
        elements.append({
            'tag': 'markdown',
            'content': _localized(
                state,
                '<font color="grey">暂无可用 Codex 会话。</font>',
                '<font color="grey">No Codex sessions are available.</font>',
            ),
        })
    for thread in sessions:
        available = bool(thread.get('available'))
        created_by_lazymind = bool(thread.get('created_by_lazymind'))
        controlled_by_lazymind = bool(
            thread.get('controlled_by_lazymind')
        )
        thread_status = (
            _localized(state, '飞书执行中', 'Running in Feishu')
            if controlled_by_lazymind
            else
            _localized(state, '可继续', 'Ready')
            if available
            else _localized(state, '运行中', 'Running')
            if created_by_lazymind
            else _localized(state, '运行中·只读', 'Running · read-only')
        )
        source = _source_label(
            'appServer' if created_by_lazymind else thread.get('source')
        )
        elements.extend([
            {
                'tag': 'markdown',
                'content': (
                    f'**{thread.get("name") or thread.get("preview") or "未命名 Codex 会话"}**　'
                    f'<font color="{"blue" if available else "orange"}">'
                    f'{thread_status}</font>\n'
                    f'<font color="grey">{thread.get("preview") or ""}</font>\n'
                    f'<font color="grey">{source} · '
                    f'{thread.get("cwd") or "—"} · '
                    f'{_time_label(thread.get("updatedAt"))}</font>'
                ),
            },
            _button_row([{
                'label': _localized(state, '进入会话', 'Open session'),
                'action': _action(
                    chat_id,
                    'assistant.open',
                    '打开 Codex 会话',
                    thread_id=str(thread.get('id') or ''),
                    expected_project_cwd=state.assistant_project_cwd,
                    expected_threads_cursor=state.assistant_threads_cursor,
                    expected_threads_page=state.assistant_threads_page,
                ),
            }]),
        ])
    if sessions or state.assistant_threads_page:
        elements.extend([
            {
                'tag': 'markdown',
                'content': (
                    _localized(
                        state,
                        f'第 {state.assistant_threads_page + 1} 页 · 共 {total} 个',
                        f'Page {state.assistant_threads_page + 1} · {total} total',
                    )
                    if total
                    else _localized(
                        state,
                        f'第 {state.assistant_threads_page + 1} 页',
                        f'Page {state.assistant_threads_page + 1}',
                    )
                ),
            },
            _button_row([
                {
                    'label': _localized(state, '较新的会话', 'Newer sessions'),
                    'disabled': not state.assistant_threads_previous_cursors,
                    'action': _action(
                        chat_id,
                        'assistant.sessions_page',
                        '查看较新的 Codex 会话',
                        direction='newer',
                        expected_project_cwd=state.assistant_project_cwd,
                        expected_threads_cursor=state.assistant_threads_cursor,
                        expected_threads_page=state.assistant_threads_page,
                    ),
                },
                {
                    'label': _localized(state, '更早的会话', 'Older sessions'),
                    'disabled': not state.assistant_threads_next_cursor,
                    'action': _action(
                        chat_id,
                        'assistant.sessions_page',
                        '查看更早的 Codex 会话',
                        direction='older',
                        expected_project_cwd=state.assistant_project_cwd,
                        expected_threads_cursor=state.assistant_threads_cursor,
                        expected_threads_page=state.assistant_threads_page,
                    ),
                },
            ]),
        ])
    return elements


def _assistant_content_pages(text: str) -> list[str]:
    remaining = str(text or '').strip()
    pages: list[str] = []
    while len(remaining) > _ASSISTANT_ANSWER_PAGE_CHARS:
        cut = remaining.rfind('\n\n', 0, _ASSISTANT_ANSWER_PAGE_CHARS + 1)
        if cut < _ASSISTANT_ANSWER_PAGE_CHARS // 2:
            cut = remaining.rfind('\n', 0, _ASSISTANT_ANSWER_PAGE_CHARS + 1)
        if cut < _ASSISTANT_ANSWER_PAGE_CHARS // 2:
            cut = _ASSISTANT_ANSWER_PAGE_CHARS
        pages.append(remaining[:cut].strip())
        remaining = remaining[cut:].strip()
    if remaining:
        pages.append(remaining)
    return pages


def _detail(
    state: Any,
    chat_id: str,
    assistant_view: dict[str, Any],
) -> list[dict[str, Any]]:
    ui_status = assistant_ui_status(assistant_view)
    detail = assistant_view if assistant_view.get('kind') == 'detail' else {}
    thread = detail.get('thread')
    thread = dict(thread) if isinstance(thread, dict) else {}
    snapshot = detail.get('snapshot')
    snapshot = dict(snapshot) if isinstance(snapshot, dict) else {}
    turns = detail.get('turns')
    turns = list(turns) if isinstance(turns, list) else []
    offset = max(0, _integer(detail.get('offset')))
    total_turns = max(0, _integer(detail.get('total_turns')))
    run_status = detail_run_status(assistant_view)
    dispatching = ui_status == 'dispatching'
    cancelling = ui_status == 'cancelling'
    running = run_status == 'running'
    releasing = run_status == 'releasing'
    release_failed = run_status == 'release_failed'
    busy = dispatching or cancelling or run_status in {
        'running',
        'waiting_for_input',
        'releasing',
        'release_failed',
    }
    controlled = bool(thread.get('controlled_by_lazymind'))
    readonly = (
        releasing
        or release_failed
        or (not bool(thread.get('available')) and not controlled)
    )
    conversation_id = str(snapshot.get('conversation_id') or '')
    status = (
        _localized(state, '正在取消', 'Cancelling')
        if cancelling
        else _localized(state, '正在提交', 'Submitting')
        if dispatching
        else _localized(state, '运行中', 'Running')
        if running
        else _localized(state, '正在归还控制权', 'Releasing control')
        if releasing
        else _localized(state, '控制权归还失败', 'Control release failed')
        if release_failed
        else _localized(state, '运行中·只读', 'Running · read-only')
        if readonly
        else _localized(state, '已绑定，可从飞书继续', 'Bound · ready in Feishu')
        if conversation_id
        else _localized(state, '可继续', 'Ready')
    )
    transcript = '\n\n'.join(
        (
            f'**{_localized(state, "你", "You")}**\n\n'
            if str(item.get('role') or '').lower() in {'user', 'you'}
            else '**Codex**\n\n'
        )
        + str(item.get('text') or '')
        for item in turns
        if str(item.get('text') or '').strip()
    )
    pages = _assistant_content_pages(transcript)
    page = min(state.assistant_answer_page, max(0, len(pages) - 1))
    elements = [
        _heading_action(
            title=(
                f'← {thread.get("name") or thread.get("preview") or "Codex 会话"}'
            ),
            description=(
                f'{project_name(str(thread.get("cwd") or ""))} · '
                f'{_source_label("appServer" if thread.get("created_by_lazymind") else thread.get("source"))} · '
                f'{_time_label(thread.get("updatedAt"))}'
            ),
            button={
                'label': _localized(state, '返回会话', 'Sessions'),
                'disabled': busy,
                'action': _action(
                    chat_id,
                    'assistant.back',
                    '返回 Codex 会话列表',
                    thread_id=str(thread.get('id') or ''),
                ),
            },
        ),
        {
            'tag': 'markdown',
            'content': (
                f'<font color="{"orange" if readonly else "green"}">'
                f'● {status}</font>\n'
                f'<font color="grey">{_project_location(str(thread.get("cwd") or ""))}</font>'
            ),
        },
        {'tag': 'hr'},
    ]
    if dispatching:
        elements.append({
            'tag': 'markdown',
            'content': _localized(
                state,
                '<font color="blue">⏳ 正在将任务提交给 Codex…</font>',
                '<font color="blue">⏳ Submitting the task to Codex…</font>',
            ),
        })
        prompt = str(assistant_view.get('prompt') or '').strip()
        if prompt:
            elements.append({
                'tag': 'markdown',
                'content': (
                    f'**{_localized(state, "飞书本轮", "Latest from Feishu")}**'
                    f'\n\n{prompt}'
                ),
            })
    elif cancelling:
        elements.append({
            'tag': 'markdown',
            'content': _localized(
                state,
                '<font color="blue">⏳ 已发送中断请求，正在等待 Codex 终止并归还控制权…</font>',
                '<font color="blue">⏳ Interrupt sent; waiting for Codex to stop and release control…</font>',
            ),
        })
    elif ui_status == 'error':
        elements.extend([
            {
                'tag': 'markdown',
                'content': f'<font color="red">打开失败：{_error_text(state, assistant_view)}</font>',
            },
            _button_row([{
                'label': _localized(state, '返回项目会话', 'Project sessions'),
                'disabled': busy,
                'action': _action(
                    chat_id,
                    'assistant.back',
                    '返回项目会话',
                    thread_id=str(thread.get('id') or ''),
                ),
            }]),
        ])
    if pages:
        page_label = (
            f'　<font color="grey">{page + 1} / {len(pages)}</font>'
            if len(pages) > 1 else ''
        )
        elements.append({
            'tag': 'markdown',
            'content': f'{page_label}\n\n{pages[page]}' if page_label else pages[page],
        })
        if len(pages) > 1 and not busy:
            elements.append(_button_row([
                {
                    'label': _localized(state, '上一段', 'Previous part'),
                    'disabled': page == 0,
                    'action': _action(
                        chat_id,
                        'assistant.answer_page',
                        '查看 Codex 会话上一段',
                        direction='previous',
                        offset=offset,
                        total_turns=total_turns,
                        thread_id=str(thread.get('id') or ''),
                        expected_answer_page=state.assistant_answer_page,
                    ),
                },
                {
                    'label': _localized(state, '下一段', 'Next part'),
                    'disabled': page >= len(pages) - 1,
                    'action': _action(
                        chat_id,
                        'assistant.answer_page',
                        '查看 Codex 会话下一段',
                        direction='next',
                        offset=offset,
                        total_turns=total_turns,
                        thread_id=str(thread.get('id') or ''),
                        expected_answer_page=state.assistant_answer_page,
                    ),
                },
            ]))
    if not pages and not running and ui_status == 'ready':
        elements.append({
            'tag': 'markdown',
            'content': _localized(
                state,
                '<font color="grey">该会话暂无消息，可直接从底部输入框开始。</font>',
                '<font color="grey">No messages yet. Start from the input below.</font>',
            ),
        })
    if total_turns and not busy:
        turn_page_count = max(
            1,
            (
                total_turns
                + _ASSISTANT_TURN_PAGE_SIZE
                - 1
            )
            // _ASSISTANT_TURN_PAGE_SIZE,
        )
        elements.append(_turn_pagination(
            state,
            chat_id,
            offset // _ASSISTANT_TURN_PAGE_SIZE,
            turn_page_count,
            {
                'offset': offset,
                'total_turns': total_turns,
                'thread_id': str(thread.get('id') or ''),
            },
        ))
    if dispatching:
        footer = _localized(
            state,
            '<font color="grey">任务正在提交，完成前暂不接收新消息。</font>',
            '<font color="grey">The task is being submitted; new messages are paused until it is accepted.</font>',
        )
    elif running and not cancelling:
        run_id = str(snapshot.get('run_id') or '')
        progress = _localized(
            state,
            '正在生成回答…',
            'Generating an answer…',
        )
        running_elements = [
            {
                'tag': 'markdown',
                'content': (
                    '<font color="blue">Codex 正在处理</font>\n'
                    f'<font color="grey">{progress}</font>'
                ),
            },
            {
                'tag': 'markdown',
                'content': _localized(
                    state,
                    (
                        '<font color="grey">LazyMind 正在控制此 Codex Thread；'
                        'Codex Desktop 会暂时显示“已在另一个应用中打开”。'
                        '完成或取消后将释放订阅。</font>'
                    ),
                    (
                        '<font color="grey">LazyMind currently controls this '
                        'Codex Thread. The subscription is released after '
                        'completion or cancellation.</font>'
                    ),
                ),
            },
        ]
        if run_id:
            running_elements.append(_button_row([{
                'label': _localized(state, '取消', 'Cancel'),
                'style': 'danger',
                'action': _action(
                    chat_id,
                    'operation.cancel',
                    '取消 Codex 任务',
                    thread_id=str(thread.get('id') or ''),
                    operation_id=state.active_operation_id,
                    run_id=run_id,
                ),
            }]))
        else:
            running_elements.append({
                'tag': 'markdown',
                'content': _localized(
                    state,
                    '<font color="orange">运行标识尚未同步，请刷新后再取消。</font>',
                    '<font color="orange">The run identifier is not ready; refresh before cancelling.</font>',
                ),
            })
        elements.extend(running_elements)
        live_answer = str(snapshot.get('answer') or '')
        if live_answer:
            elements.insert(
                -2,
                {
                    'tag': 'markdown',
                    'content': (
                        f'**{_localized(state, "飞书本轮", "Latest from Feishu")}**'
                        f'\n\n{assistant_view.get("prompt") or ""}\n\n**Codex**\n\n{live_answer[:16000]}'
                    ),
                },
            )
    elif releasing or release_failed:
        elements.extend([
            {
                'tag': 'markdown',
                'content': _localized(
                    state,
                    (
                        '<font color="blue">任务已结束，正在归还 Codex 控制权。</font>'
                        if releasing
                        else '<font color="red">控制权尚未归还，当前会话保持只读。</font>'
                    ),
                    (
                        '<font color="blue">The task ended; releasing Codex control.</font>'
                        if releasing
                        else '<font color="red">Control is not released; this session remains read-only.</font>'
                    ),
                ),
            },
        ])
        if release_failed:
            elements.append(_button_row([{
                'label': _localized(state, '重试归还控制权', 'Retry release'),
                'style': 'primary',
                'action': _action(
                    chat_id,
                    'assistant.release',
                    '重试归还 Codex 控制权',
                    thread_id=str(thread.get('id') or ''),
                ),
            }]))
    elif snapshot.get('answer') and not turns:
        elements.extend([
            {'tag': 'hr'},
            {
                'tag': 'markdown',
                'content': (
                    f'**{_localized(state, "飞书本轮", "Latest from Feishu")}**'
                    f'\n\n{assistant_view.get("prompt") or ""}\n\n**Codex**\n\n{str(snapshot.get("answer"))[:16000]}'
                ),
            },
        ])
    request = snapshot.get('pending_request')
    request = (
        dict(request)
        if isinstance(request, dict) and request.get('request_id')
        else None
    )
    if request:
        elements.extend(_assistant_request_elements(
            state,
            chat_id,
            request,
            str(thread.get('id') or ''),
            str(snapshot.get('run_id') or ''),
        ))
    for image in state.images:
        image_key = str(image.get('image_key') or '')
        if not is_feishu_image_key(image_key):
            continue
        element: dict[str, Any] = {'tag': 'img', 'img_key': image_key}
        caption = str(image.get('caption') or '').strip()
        if caption:
            element['alt'] = {
                'tag': 'plain_text',
                'content': caption[:300],
            }
        elements.append(element)
    if cancelling:
        footer = _localized(
            state,
            '<font color="grey">只有收到 Codex 终止事件并完成控制权归还后，本卡片才会显示已取消。</font>',
            '<font color="grey">The card shows cancelled only after Codex stops and control is released.</font>',
        )
    elif running:
        footer = _localized(
            state,
            '<font color="grey">Codex 任务正在运行；完成或取消前不会接收新消息。</font>',
            '<font color="grey">Codex is running; new messages are paused until it completes or is cancelled.</font>',
        )
    elif run_status == 'waiting_for_input':
        footer = _localized(
            state,
            '<font color="grey">请使用上方确认按钮或表单，暂不接收底部输入框的新消息。</font>',
            '<font color="grey">Use the approval controls above; new composer messages are paused.</font>',
        )
    elif releasing:
        footer = _localized(
            state,
            '<font color="grey">任务已结束，LazyMind 正在归还会话控制权。</font>',
            '<font color="grey">The task ended; LazyMind is releasing session control.</font>',
        )
    elif release_failed:
        footer = _localized(
            state,
            '<font color="grey">控制权尚未归还，当前会话保持只读，请使用上方重试。</font>',
            '<font color="grey">Control is not released. This session remains read-only; retry above.</font>',
        )
    else:
        footer = _localized(
            state,
            (
                '<font color="grey">从飞书底部输入框继续；'
                '新消息、执行进度和回答都会更新在本卡片。</font>'
                if not readonly
                else '<font color="grey">此会话正在其他端运行，飞书暂时只读。</font>'
            ),
            (
                '<font color="grey">Continue from the Feishu input; new messages, '
                'progress, and answers update here.</font>'
                if not readonly
                else '<font color="grey">This session is running elsewhere and is read-only in Feishu.</font>'
            ),
        )
    elements.extend([
        {'tag': 'hr'},
        {'tag': 'markdown', 'content': footer},
        _button_row([{
            'label': _localized(state, '返回项目会话', 'Project sessions'),
            'disabled': busy,
            'action': _action(
                chat_id,
                'assistant.back',
                '返回项目会话',
                thread_id=str(thread.get('id') or ''),
            ),
        }]),
    ])
    return elements


def _assistant_request_elements(
    state: Any,
    chat_id: str,
    request: dict[str, Any],
    thread_id: str,
    run_id: str,
) -> list[dict[str, Any]]:
    request_id = str(request.get('request_id') or '')
    kind = str(request.get('kind') or 'request')
    summary = str(request.get('summary') or '')
    request_titles = {
        'command_approval': ('命令执行审批', 'Command approval'),
        'file_change_approval': ('文件变更审批', 'File change approval'),
        'permissions_approval': ('权限审批', 'Permission approval'),
        'user_input': ('需要补充信息', 'Input required'),
    }
    chinese_title, english_title = request_titles.get(
        kind,
        ('需要你的操作', 'Action required'),
    )
    title = _localized(state, chinese_title, english_title)
    elements: list[dict[str, Any]] = [
        {
            'tag': 'markdown',
            'content': f'**{title}**',
        }
    ]
    if kind not in request_titles and summary:
        elements.append({
            'tag': 'markdown',
            'content': _markdown_code(summary),
        })
    error = str(request.get('error') or '')
    if error:
        elements.append({
            'tag': 'markdown',
            'content': f'<font color="red">{error}</font>',
        })
    if kind == 'user_input' and not error:
        form = assistant_user_input_form(request, chat_id, thread_id)
        if form is not None:
            elements.append(form)
        else:
            elements.append({
                'tag': 'markdown',
                'content': '<font color="red">该输入请求超出飞书卡片容量，无法完整提交。</font>',
            })
    field_labels = {
        'command': '命令',
        'cwd': '工作目录',
        'reason': '原因',
        'additional_permissions': '附加权限',
        'policy_amendment': '可选的持久执行规则',
        'permissions': '申请的权限范围',
        'environmentId': 'Environment',
        'itemId': 'Item',
        'item_id': '变更项 ID',
        'file_scope': '文件变更范围',
        'file': '变更文件',
        'diff': 'Diff',
    }
    detail_lines = []
    for detail_field in request.get('fields') or []:
        if not isinstance(detail_field, dict):
            continue
        value = str(detail_field.get('value') or '')
        if not value:
            continue
        field_kind = str(detail_field.get('kind') or 'detail')
        detail_lines.append(
            f'**{field_labels.get(field_kind, field_kind)}**\n'
            + _markdown_code(
                value,
                str(detail_field.get('language') or ''),
            )
        )
    if detail_lines:
        elements.append({'tag': 'markdown', 'content': '\n\n'.join(detail_lines)})

    labels = {
        'allow_once': ('允许一次', 'Allow once'),
        'allow_session': ('本会话允许', 'Allow session'),
        'grant_turn': ('允许本轮', 'Allow this turn'),
        'grant_session': ('允许会话', 'Allow session'),
        'deny': ('拒绝', 'Deny'),
        'policy': ('应用策略', 'Apply policy'),
    }
    approval_buttons = []
    for action in request.get('actions') or []:
        if not isinstance(action, dict):
            continue
        action_id = str(action.get('id') or '')
        action_kind = str(action.get('kind') or '')
        if (
            not action_id
            or action_kind == 'submit'
            or action_kind not in labels
        ):
            continue
        chinese, english = labels.get(action_kind, ('继续', 'Continue'))
        approval_buttons.append({
            'label': str(action.get('label') or _localized(
                state, chinese, english,
            ))[:200],
            'style': (
                'danger' if action_kind == 'deny'
                else 'primary' if action_kind in {'allow_once', 'grant_turn'}
                else 'default'
            ),
            'action': _action(
                chat_id,
                'assistant.respond',
                '响应 Codex 请求',
                request_id=request_id,
                request_kind=kind,
                action_id=action_id,
                thread_id=thread_id,
            ),
        })
    for index in range(0, len(approval_buttons), 3):
        elements.append(_button_row(approval_buttons[index:index + 3]))
    if not approval_buttons and run_id:
        elements.append(_button_row([{
            'label': _localized(state, '取消任务', 'Cancel task'),
            'style': 'danger',
            'action': _action(
                chat_id,
                'operation.cancel',
                '取消无法安全响应的 Codex 请求',
                thread_id=thread_id,
                operation_id=state.active_operation_id,
                run_id=run_id,
            ),
        }]))
    return elements


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
        command=_history_command(),
        workspace_action={
            'kind': 'history.open',
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
    return _command_action(
        chat_id=chat_id,
        text=f'查看{_CAPABILITY_LABELS.get(category, "知识库")}',
        command={
            'schema_version': '1',
            'command': 'capability.list',
            'parameters': {
                'capabilities': [category],
            },
        },
        workspace_action=workspace_action,
    )


def _capability_toggle_action(
    chat_id: str,
    state: FeishuWorkspaceState,
    item: dict[str, Any],
    *,
    enabled: bool,
) -> dict[str, Any]:
    return _resource_toggle_action(
        chat_id=chat_id,
        kind='capability.toggle',
        category=state.capability_category,
        item=item,
        enabled=enabled,
        expected_revision=state.revision,
        expected_operation_id=state.active_operation_id,
        catalog_only=True,
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
    kind: str,
    category: str,
    item: dict[str, Any],
    enabled: bool,
    expected_revision: int,
    expected_operation_id: str,
    catalog_only: bool,
) -> dict[str, Any]:
    return _command_action(
        chat_id=chat_id,
        text=f'切换{_CAPABILITY_LABELS.get(category, "资源")}',
        command={
            'schema_version': '1',
            'command': 'capability.list',
            'parameters': {
                'capabilities': (
                    [category]
                    if catalog_only
                    else list(_CAPABILITY_OVERVIEW_TYPES)
                ),
            },
        },
        workspace_action={
            'kind': kind,
            'category': category,
            'expected_revision': expected_revision,
            'expected_operation_id': expected_operation_id,
            'expected_view': 'capabilities',
            'resource': {
                'type': category,
                'id': str(item.get('id') or ''),
                'name': str(item.get('name') or '')[:100],
                'enabled': enabled,
            },
        },
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
            command=_history_command(),
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
    return _command_action(
        chat_id=chat_id,
        text=text,
        command={
            'schema_version': '1',
            'command': 'conversation.settings.update',
            'parameters': {
                'change': change,
                'expected_conversation_id': expected_conversation_id,
            },
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

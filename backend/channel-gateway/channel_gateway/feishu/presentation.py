from __future__ import annotations

import json
import re
from typing import Any

from lark_channel import new_card

from channel_gateway.common.domain.channel import (
    ClaimedOutbound,
    sanitize_channel_text,
)
from channel_gateway.common.domain.outbound import OutboundRenderer


_CARD_HEADERS = {
    'conversation.new': ('✨ 新会话', 'green'),
    'conversation.list': ('🕘 历史会话', 'blue'),
    'conversation.switch': ('✅ 会话已切换', 'green'),
    'conversation.current': ('📍 当前会话', 'blue'),
    'history.more': ('📖 会话历史', 'blue'),
    'capability.list': ('🧰 LazyMind 能力', 'purple'),
    'capability.configure': ('✅ 能力已更新', 'green'),
    'conversation.settings': ('⚙️ 会话设置', 'purple'),
    'conversation.settings.update': ('✅ 会话设置已保存', 'green'),
    'clarify': ('💬 需要确认', 'orange'),
    'failed': ('⚠️ 暂时无法处理', 'red'),
    'welcome': ('👋 欢迎使用 LazyMind', 'turquoise'),
}
_MAX_ASK_BUTTON_CHOICES = 8
_MAX_ASK_QUESTION_CHARS = 500
_MAX_ASK_CHOICE_CHARS = 80
_MAX_ASK_ACTION_BYTES = 16 * 1024
_MAX_MERGED_REFERENCE_CHARS = 6000
_ASK_OTHER_OPTION = '其他'
_STREAM_ONLY_PREFLIGHT_MARKERS = (
    'preflight_failed',
    'only supports stream mode',
    'enable the stream parameter',
)
_MARKDOWN_IMAGE = re.compile(r'!\[[^\]]*\]\(([^)\s]+)\)')
_CAPABILITY_RESOURCE_TYPES = (
    'knowledge_base',
    'skill',
    'tool',
    'personalization',
)
_WORKSPACE_ACTIONS = (
    (
        '＋ 新建会话',
        '新建会话',
        {
            'schema_version': '1',
            'command': 'conversation.new',
            'parameters': {
                'message': '',
                'resource_changes': [],
                'evidence': ['新建会话'],
            },
        },
        'primary',
    ),
    (
        '⇄ 切换会话',
        '切换会话',
        {
            'schema_version': '1',
            'command': 'conversation.list',
            'parameters': {'evidence': ['切换会话']},
        },
        'default',
    ),
    (
        '⚙ 会话设置',
        '会话设置',
        {
            'schema_version': '1',
            'command': 'conversation.settings',
            'parameters': {
                'section': 'overview',
                'evidence': ['会话设置'],
            },
        },
        'default',
    ),
    (
        '📖 会话历史',
        '查看更多历史',
        {
            'schema_version': '1',
            'command': 'history.more',
            'parameters': {
                'evidence': ['查看更多历史'],
            },
        },
        'default',
    ),
)


def presentable_feishu_text(value: str) -> str:
    """Keep provider cards readable when Core returns an internal error."""
    cleaned = sanitize_channel_text(value)
    normalized = cleaned.casefold()
    if all(
        marker in normalized
        for marker in _STREAM_ONLY_PREFLIGHT_MARKERS
    ):
        return (
            '当前工作流无法启动：所选模型与工作流的启动检查方式'
            '不兼容。请在 LazyMind 网页端更换兼容模型后重试。'
        )
    return cleaned


def streamable_feishu_text(value: str) -> str:
    """Keep media references out of CardKit text-stream updates."""
    cleaned = presentable_feishu_text(value)
    image_count = len(_MARKDOWN_IMAGE.findall(cleaned))
    if not image_count:
        return cleaned
    text = _MARKDOWN_IMAGE.sub('', cleaned).strip()
    notice = (
        f'🖼️ 已生成 {image_count} 张图片，'
        '正在作为飞书原图发送…'
    )
    return f'{text}\n\n{notice}' if text else notice


def parse_ask_form_submission(
    value: dict[str, Any],
    form_value: Any,
) -> tuple[str, dict[str, Any] | None]:
    raw_questions = value.get('ask_form_questions')
    if not isinstance(raw_questions, list) or not isinstance(
        form_value,
        dict,
    ):
        return '', None
    answered: list[dict[str, Any]] = []
    lines: list[str] = []
    for raw_question in raw_questions:
        if not isinstance(raw_question, dict):
            return '', None
        name = str(raw_question.get('name') or '')
        text = str(raw_question.get('text') or '')
        question_type = str(raw_question.get('type') or '')
        choices = [
            str(choice)
            for choice in (
                raw_question.get('choices')
                if isinstance(raw_question.get('choices'), list)
                else []
            )
        ]
        answer = _ask_form_answer(
            question_type,
            form_value.get(name),
            str(
                form_value.get(
                    str(raw_question.get('other_name') or ''),
                    '',
                )
                or ''
            ).strip(),
        )
        if not name or not text or answer is None:
            return '', None
        answered.append(
            {
                'text': text,
                'type': question_type,
                'choices': choices,
                'custom_choices': choices,
                'answer': answer,
            }
        )
        lines.append(f'{text}: {_ask_answer_text(answer)}')
    if not answered:
        return '', None
    return (
        '\n'.join(lines),
        {
            'ask_id': str(value.get('ask_id') or ''),
            'questions': answered,
        },
    )


def _ask_form_answer(
    question_type: str,
    raw: Any,
    other_text: str,
) -> dict[str, Any] | None:
    if question_type == 'multiple':
        values = [
            str(item).strip()
            for item in (raw if isinstance(raw, list) else [])
            if str(item).strip()
        ]
        if not values:
            return None
        return {
            'type': 'multiple',
            'value': values,
            'otherText': other_text,
        }
    value = str(raw or '').strip()
    if not value:
        return None
    if question_type == 'boolean':
        return {'type': 'boolean', 'value': value}
    if question_type == 'single':
        return {
            'type': 'single',
            'value': value,
            'otherText': other_text,
        }
    if question_type == 'text':
        return {'type': 'text', 'value': value}
    return None


def _ask_answer_text(answer: dict[str, Any]) -> str:
    value = answer.get('value')
    if isinstance(value, list):
        rendered = '、'.join(str(item) for item in value)
    else:
        rendered = str(value or '')
    other_text = str(answer.get('otherText') or '').strip()
    if other_text and (
        value == '其他'
        or isinstance(value, list) and '其他' in value
    ):
        return rendered.replace('其他', other_text)
    return rendered


def streaming_reply_card(
    provider_context: dict[str, Any],
) -> dict[str, Any]:
    elements: list[dict[str, Any]] = [
        {
            'tag': 'markdown',
            'element_id': 'lazymind_status',
            'content': '⏳ **正在理解你的问题**',
        },
        {
            'tag': 'collapsible_panel',
            'expanded': False,
            'background_color': 'grey',
            'header': {
                'title': {
                    'tag': 'plain_text',
                    'content': '处理过程',
                },
            },
            'elements': [
                {
                    'tag': 'markdown',
                    'element_id': 'lazymind_thinking',
                    'content': '正在分析问题…',
                },
            ],
        },
        {
            'tag': 'markdown',
            'element_id': 'lazymind_answer',
            'content': '<font color="grey">正在准备回答…</font>',
        },
    ]
    actions = _workspace_action_rows(provider_context)
    if actions:
        elements.extend(
            [
                {'tag': 'hr'},
                {
                    'tag': 'markdown',
                    'content': '**快捷操作**',
                },
                *actions,
            ]
        )
    return {
        'schema': '2.0',
        'config': {
            'wide_screen_mode': True,
            'streaming_mode': True,
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
                'content': 'LazyMind 正在回答',
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


class FeishuPresentationRenderer:
    """Renders common reply parts as Feishu cards without changing Core data."""

    def __init__(self, base: OutboundRenderer):
        self._base = base

    def render(self, message: ClaimedOutbound) -> list[dict[str, Any]]:
        presentations = self._presentations(message)
        parts = _merge_reference_parts(self._base.render(message))
        if message.metadata.get('streamed_text') is True:
            parts = [
                part
                for part in parts
                if part.get('kind') != 'text'
            ]
        if message.metadata.get('suppress_text_when_presented') is True:
            parts = [
                part
                for part in parts
                if part.get('kind') != 'text'
            ]
        text_indexes = [
            index
            for index, part in enumerate(parts)
            if part.get('kind') == 'text'
        ]
        if not text_indexes:
            return [
                *parts,
                *self._presentation_cards(
                    message,
                    presentations,
                    include_selection=True,
                ),
            ]
        last_text_index = text_indexes[-1]
        rendered: list[dict[str, Any]] = []
        for index, part in enumerate(parts):
            if part.get('kind') != 'text':
                rendered.append(part)
                continue
            rendered.append(
                {
                    'kind': 'card',
                    'card': self._card(
                        message,
                        str(part.get('text') or ''),
                        presentations,
                        include_actions=index == last_text_index,
                    ),
                }
            )
        rendered.extend(
            self._presentation_cards(
                message,
                presentations,
                include_selection=False,
            )
        )
        return rendered

    def _card(
        self,
        message: ClaimedOutbound,
        text: str,
        presentations: list[dict[str, Any]],
        *,
        include_actions: bool,
    ) -> dict[str, Any]:
        title, template = _CARD_HEADERS.get(
            message.intent_kind,
            ('LazyMind', 'blue'),
        )
        if message.purpose == 'welcome':
            title, template = _CARD_HEADERS['welcome']
        builder = (
            new_card()
            .config(wide_screen_mode=True)
            .header(title, template=template)
        )
        answer, references = _split_reference_section(
            presentable_feishu_text(text)
        )
        if answer:
            builder.markdown(answer)
        if references:
            if answer:
                builder.divider()
            builder.markdown(f'**参考来源**\n{references}')
        selection = next(
            (
                presentation
                for presentation in presentations
                if presentation.get('kind') == 'selection'
            ),
            None,
        )
        if include_actions and selection is not None:
            self._add_selection(
                builder,
                selection,
                message.provider_context,
            )
        if include_actions and (
            message.purpose == 'welcome'
            or selection is not None
        ):
            _add_workspace_actions(
                builder,
                message.provider_context,
            )
        card = builder.build().data
        if include_actions and selection is not None:
            option_count = _selection_option_count(selection)
            if option_count:
                _add_header_tags(
                    card,
                    [(f'{option_count} 个选项', 'blue')],
                )
        return card

    @staticmethod
    def _add_selection(
        builder,
        presentation: dict[str, Any],
        provider_context: dict[str, Any],
    ) -> None:
        if presentation.get('kind') != 'selection':
            return
        raw_options = presentation.get('options')
        if not isinstance(raw_options, list):
            return
        options = [
            {
                'label': str(item.get('label') or ''),
                'value': str(item.get('value') or ''),
            }
            for item in raw_options
            if isinstance(item, dict)
            and item.get('label')
            and item.get('value')
        ]
        if not options:
            return
        builder.divider().markdown(
            f'**{str(presentation.get("title") or "请选择")}**'
        )
        action_context = {
            'lazymind_action': 'select',
            'selection_id': str(
                presentation.get('selection_id') or ''
            ),
            'intended_chat_id': str(
                provider_context.get('chat_id') or ''
            ),
        }
        for start in range(0, len(options), 2):
            row = [
                {
                    'label': _selection_button_label(
                        option['value'],
                        option['label'],
                    ),
                    'action': {
                        **action_context,
                        'selection': option['value'],
                    },
                    'style': (
                        'primary'
                        if start == 0 and offset == 0
                        else 'default'
                    ),
                }
                for offset, option in enumerate(
                    options[start:start + 2]
                )
            ]
            _add_button_grid_row(builder, row)

    def _presentation_cards(
        self,
        message: ClaimedOutbound,
        presentations: list[dict[str, Any]],
        *,
        include_selection: bool,
    ) -> list[dict[str, Any]]:
        cards: list[dict[str, Any]] = []
        capability = next(
            (
                presentation
                for presentation in presentations
                if presentation.get('kind') == 'capability'
            ),
            None,
        )
        selection = next(
            (
                presentation
                for presentation in presentations
                if presentation.get('kind') == 'selection'
            ),
            None,
        )
        for presentation in presentations:
            kind = str(presentation.get('kind') or '')
            if (
                kind == 'selection'
                and include_selection
                and capability is None
            ):
                cards.append(
                    {
                        'kind': 'card',
                        'card': self._card(
                            message,
                            '',
                            presentations,
                            include_actions=True,
                        ),
                    }
                )
            elif kind == 'capability':
                cards.append(
                    {
                        'kind': 'card',
                        'card': self._capability_card(
                            presentation,
                            selection,
                            message.provider_context,
                        ),
                    }
                )
            elif kind == 'conversation_settings':
                cards.append(
                    {
                        'kind': 'card',
                        'card': self._conversation_settings_card(
                            presentation,
                            message.provider_context,
                        ),
                    }
                )
            elif kind == 'ask':
                cards.append(
                    {
                        'kind': 'card',
                        'card': self._ask_card(
                            presentation,
                            message.provider_context,
                        ),
                    }
                )
            elif kind == 'task':
                cards.append(
                    {
                        'kind': 'card',
                        'card': self._task_card(presentation),
                        'task_id': str(
                            presentation.get('task_id') or ''
                        ),
                        'conversation_id': str(
                            presentation.get('conversation_id') or ''
                        ),
                    }
                )
            elif kind == 'conversation':
                cards.append(
                    {
                        'kind': 'card',
                        'card': self._conversation_card(
                            presentation,
                            message.provider_context,
                        ),
                    }
                )
        return cards

    @staticmethod
    def _conversation_settings_card(
        payload: dict[str, Any],
        provider_context: dict[str, Any],
    ) -> dict[str, Any]:
        section = str(payload.get('section') or 'overview')
        updated = bool(payload.get('updated', False))
        account_sections = {
            'skill',
            'tool',
            'personalization',
            'workflow',
        }
        section_title = {
            'overview': '⚙️ 会话设置',
            'knowledge_base': '📚 会话知识库',
            'workflow_mode': '🧩 Workflow 执行策略',
            'subagent': '🧠 SubAgent',
            'skill': '🪄 Skill',
            'tool': '🧰 工具',
            'personalization': '🧭 个人习惯',
            'workflow': '🧩 可用 Workflow',
        }.get(section, '⚙️ 会话设置')
        title = (
            '✅ 会话设置已保存'
            if updated
            else section_title
        )
        subtitle = (
            '管理当前会话可使用的能力'
            if section == 'overview'
            else (
                '修改后当前会话立即生效'
                if section in account_sections
                else '仅作用于当前会话'
            )
        )
        builder = (
            new_card()
            .config(wide_screen_mode=True)
            .header(
                title,
                subtitle=subtitle,
                template='green' if updated else 'purple',
            )
        )
        knowledge_bases = [
            dict(item)
            for item in (
                payload.get('knowledge_bases')
                if isinstance(payload.get('knowledge_bases'), list)
                else []
            )
            if isinstance(item, dict)
            and item.get('id')
            and item.get('name')
        ]
        skills = _settings_card_items(payload, 'skills')
        tools = _settings_card_items(payload, 'tools')
        workflows = _settings_card_items(payload, 'workflows')
        channel_features = [
            str(label)
            for label in (
                payload.get('channel_features')
                if isinstance(
                    payload.get('channel_features'),
                    list,
                )
                else []
            )
            if str(label)
        ]
        workflow_enabled = bool(
            payload.get('workflow_enabled', True)
        )
        workflow_mode = str(
            payload.get('workflow_mode') or 'dynamic'
        )
        subagent_enabled = bool(
            payload.get('subagent_enabled', True)
        )
        personalization_enabled = bool(
            payload.get('personalization_enabled', True)
        )
        if section == 'overview':
            enabled_kb_count = sum(
                bool(item.get('enabled'))
                for item in knowledge_bases
            )
            workflow_status = (
                '自动执行'
                if workflow_enabled and workflow_mode == 'auto'
                else (
                    '执行前确认'
                    if workflow_enabled
                    else '已关闭'
                )
            )
            builder.markdown(
                '**当前配置**\n'
                f'知识库：{enabled_kb_count} / '
                f'{len(knowledge_bases)} 个已启用\n'
                f'Workflow 执行策略：{workflow_status}\n'
                f'SubAgent：'
                f'{"已启用" if subagent_enabled else "已关闭"}\n'
                f'Skill：{_enabled_count(skills)} / {len(skills)} 个启用\n'
                f'工具：{_enabled_count(tools)} / {len(tools)} 个启用\n'
                f'可用 Workflow：{_enabled_count(workflows)} / '
                f'{len(workflows)} 个启用\n'
                f'个人习惯：'
                f'{"已启用" if personalization_enabled else "已关闭"}'
            )
            actions = (
                ('📚 知识库', 'knowledge_base'),
                ('🧩 Workflow 策略', 'workflow_mode'),
                ('🧠 SubAgent', 'subagent'),
                ('🪄 Skill', 'skill'),
                ('🧰 工具', 'tool'),
                ('🧩 可用 Workflow', 'workflow'),
                ('🧭 个人习惯', 'personalization'),
            )
            for start in range(0, len(actions), 2):
                _add_button_grid_row(
                    builder,
                    [
                        {
                            'label': label,
                            'style': (
                                'primary'
                                if start == 0 and offset == 0
                                else 'default'
                            ),
                            'action': _conversation_settings_action(
                                target,
                                provider_context,
                            ),
                        }
                        for offset, (label, target) in enumerate(
                            actions[start:start + 2]
                        )
                    ],
                )
            readonly_features = [
                label
                for label in channel_features
                if label in ('Ask', 'Task')
            ]
            if readonly_features:
                builder.divider()
                builder.markdown(
                    '**渠道内置能力**\n'
                    + '、'.join(readonly_features)
                    + '\n\n这些能力由渠道开放状态决定，无需单独配置。'
                )
        elif section == 'knowledge_base':
            if not knowledge_bases:
                builder.markdown('当前账号没有可用知识库。')
            else:
                builder.markdown(
                    '选择后会持续作用于当前会话；再次点击可关闭。'
                )
                for start in range(0, len(knowledge_bases), 2):
                    _add_button_grid_row(
                        builder,
                        [
                            {
                                'label': (
                                    f'{"✓" if bool(item.get("enabled")) else "＋"} '
                                    f'{str(item.get("name") or "")}'
                                ),
                                'style': (
                                    'primary'
                                    if bool(item.get('enabled'))
                                    else 'default'
                                ),
                                'action': (
                                    _conversation_setting_update_action(
                                        {
                                            'setting': 'knowledge_base',
                                            'dataset_id': str(
                                                item.get('id') or ''
                                            ),
                                            'enabled': not bool(
                                                item.get('enabled')
                                            ),
                                        },
                                        (
                                            '关闭知识库'
                                            if bool(item.get('enabled'))
                                            else '启用知识库'
                                        )
                                        + str(item.get('name') or ''),
                                        provider_context,
                                    )
                                ),
                            }
                            for item in knowledge_bases[start:start + 2]
                        ],
                    )
        elif section == 'workflow_mode':
            builder.markdown(
                '设置当前会话调用 Workflow 时的执行方式。'
            )
            options = (
                (
                    '自动执行',
                    workflow_enabled and workflow_mode == 'auto',
                    {
                        'setting': 'workflow_mode',
                        'mode': 'auto',
                    },
                ),
                (
                    '执行前确认',
                    workflow_enabled and workflow_mode == 'dynamic',
                    {
                        'setting': 'workflow_mode',
                        'mode': 'dynamic',
                    },
                ),
                (
                    '关闭',
                    not workflow_enabled,
                    {
                        'setting': 'workflow',
                        'enabled': False,
                    },
                ),
            )
            for start in range(0, len(options), 2):
                _add_button_grid_row(
                    builder,
                    [
                        {
                            'label': (
                                f'{"✓ " if selected else ""}{label}'
                            ),
                            'style': (
                                'primary'
                                if selected
                                else 'default'
                            ),
                            'action': (
                                _conversation_setting_update_action(
                                    change,
                                    f'Workflow {label}',
                                    provider_context,
                                )
                            ),
                        }
                        for label, selected, change
                        in options[start:start + 2]
                    ],
                )
        elif section == 'subagent':
            builder.markdown(
                '设置当前会话是否允许 SubAgent 拆分并行任务。'
            )
            options = (
                ('启用', True),
                ('关闭', False),
            )
            _add_button_grid_row(
                builder,
                [
                    {
                        'label': (
                            f'{"✓ " if subagent_enabled == enabled else ""}'
                            f'{label}'
                        ),
                        'style': (
                            'primary'
                            if subagent_enabled == enabled
                            else 'default'
                        ),
                        'action': _conversation_setting_update_action(
                            {
                                'setting': 'subagent',
                                'enabled': enabled,
                            },
                            f'SubAgent {label}',
                            provider_context,
                        ),
                    }
                    for label, enabled in options
                ],
            )
        elif section == 'skill':
            builder.markdown(
                '选择当前会话可使用的 Skill；设置会同步到其他终端。'
            )
            _add_account_setting_buttons(
                builder,
                items=skills,
                setting='skill',
                id_field='skill_id',
                empty_text='当前账号没有已安装且已发布的 Skill。',
                provider_context=provider_context,
            )
        elif section == 'tool':
            builder.markdown(
                '选择当前会话可使用的工具；系统必需工具只能查看。'
            )
            _add_account_setting_buttons(
                builder,
                items=tools,
                setting='tool',
                id_field='tool_name',
                empty_text='当前账号没有可配置工具。',
                provider_context=provider_context,
                respect_can_disable=True,
            )
        elif section == 'workflow':
            builder.markdown(
                '选择当前会话可使用的 Workflow；执行时仍受 '
                'Workflow 执行策略控制。'
            )
            _add_account_setting_buttons(
                builder,
                items=workflows,
                setting='workflow',
                id_field='workflow_ref',
                empty_text='当前账号没有可配置 Workflow。',
                provider_context=provider_context,
            )
        elif section == 'personalization':
            builder.markdown(
                '控制当前会话是否使用个人习惯；'
                '设置会同步到其他会话和终端。'
            )
            _add_button_grid_row(
                builder,
                [
                    {
                        'label': (
                            f'{"✓ " if personalization_enabled else ""}'
                            '启用'
                        ),
                        'style': (
                            'primary'
                            if personalization_enabled
                            else 'default'
                        ),
                        'action': _conversation_setting_update_action(
                            {
                                'setting': 'personalization',
                                'enabled': True,
                            },
                            '启用个人习惯',
                            provider_context,
                        ),
                    },
                    {
                        'label': (
                            f'{"✓ " if not personalization_enabled else ""}'
                            '关闭'
                        ),
                        'style': (
                            'primary'
                            if not personalization_enabled
                            else 'default'
                        ),
                        'action': _conversation_setting_update_action(
                            {
                                'setting': 'personalization',
                                'enabled': False,
                            },
                            '关闭个人习惯',
                            provider_context,
                        ),
                    },
                ],
            )
        if section != 'overview':
            _add_button_grid_row(
                builder,
                [
                    {
                        'label': '← 返回会话设置',
                        'style': 'default',
                        'action': _conversation_settings_action(
                            'overview',
                            provider_context,
                        ),
                    }
                ],
            )
        if section in account_sections:
            builder.footer(
                '此项由 LazyMind 账号统一保存；当前会话立即生效，'
                '并同步到其他会话和终端。'
            )
        elif section == 'overview':
            builder.footer(
                '这里集中管理当前会话可使用的能力；'
                '具体页面会注明设置是否同步到其他会话。'
            )
        else:
            builder.footer(
                '设置保存在 LazyMind 当前会话中，'
                '飞书与网页端会使用同一份配置。'
            )
        return builder.build().data

    @staticmethod
    def _capability_card(
        payload: dict[str, Any],
        selection: dict[str, Any] | None,
        provider_context: dict[str, Any],
    ) -> dict[str, Any]:
        groups = [
            dict(group)
            for group in (
                payload.get('groups')
                if isinstance(payload.get('groups'), list)
                else []
            )
            if isinstance(group, dict)
            and group.get('resource_type')
            and group.get('label')
        ]
        single_group = len(groups) == 1
        title = (
            f'⚙️ {str(groups[0].get("label") or "")}配置'
            if single_group
            else '⚙️ 配置能力'
        )
        builder = (
            new_card()
            .config(wide_screen_mode=True)
            .header(
                title,
                subtitle=(
                    '点击选项即可应用'
                    if single_group
                    else '选择一类能力开始配置'
                ),
                template='purple',
            )
        )
        if not groups:
            builder.markdown('当前没有可配置的能力。')
            return builder.build().data
        if not single_group:
            builder.markdown(
                '不同能力会沿用 LazyMind 网页端的账号和会话配置。'
            )
            actions = [
                {
                    'label': (
                        f'{str(group.get("label") or "")}'
                        f' · {max(0, int(group.get("total") or 0))} 项'
                    ),
                    'style': (
                        'primary'
                        if index == 0
                        else 'default'
                    ),
                    'action': _capability_action(
                        str(group.get('resource_type') or ''),
                        str(group.get('label') or ''),
                        provider_context,
                    ),
                }
                for index, group in enumerate(groups)
            ]
            for start in range(0, len(actions), 2):
                _add_button_grid_row(
                    builder,
                    actions[start:start + 2],
                )
            enabled_features = [
                str(label)
                for label in (
                    payload.get('enabled_features')
                    if isinstance(
                        payload.get('enabled_features'),
                        list,
                    )
                    else []
                )
                if str(label)
            ]
            if enabled_features:
                builder.footer(
                    '当前会话还可调用：'
                    + '、'.join(enabled_features)
                )
            else:
                builder.footer('点击分类后，可直接选择具体能力。')
            return builder.build().data

        group = groups[0]
        items = [
            dict(item)
            for item in (
                group.get('items')
                if isinstance(group.get('items'), list)
                else []
            )
            if isinstance(item, dict) and item.get('name')
        ]
        if items:
            builder.markdown(
                '\n'.join(
                    f'**{index}. {str(item.get("name") or "")}**'
                    f'　<font color="grey">'
                    f'{str(item.get("status") or "")}</font>'
                    for index, item in enumerate(items, start=1)
                )
            )
            if selection is not None:
                FeishuPresentationRenderer._add_selection(
                    builder,
                    selection,
                    provider_context,
                )
        else:
            builder.markdown(
                f'当前没有可用的{str(group.get("label") or "能力")}。'
            )
        _add_button_grid_row(
            builder,
            [
                {
                    'label': '← 返回能力总览',
                    'style': 'default',
                    'action': _capability_action(
                        '',
                        '配置能力',
                        provider_context
                    ),
                }
            ],
        )
        builder.footer(
            '按钮配置会直接作用于当前飞书会话，不经过模型判断。'
        )
        card = builder.build().data
        _add_header_tags(
            card,
            [(f'{len(items)} 个可用项', 'purple')],
        )
        return card

    @staticmethod
    def _conversation_card(
        payload: dict[str, Any],
        provider_context: dict[str, Any],
    ) -> dict[str, Any]:
        state = str(payload.get('state') or 'current')
        header = {
            'new': ('✨ 新会话', 'green'),
            'current': ('📍 当前会话', 'blue'),
            'switched': ('✅ 会话已切换', 'green'),
            'history': ('📖 会话历史', 'blue'),
        }.get(state, ('LazyMind 会话', 'blue'))
        title = str(payload.get('title') or '未命名会话')
        builder = (
            new_card()
            .config(wide_screen_mode=True)
            .header(header[0], template=header[1])
            .markdown(f'**{title}**')
        )
        metadata: list[str] = []
        previous_title = str(payload.get('previous_title') or '')
        if previous_title:
            metadata.append(f'已离开：{previous_title}')
        updated_at = str(payload.get('updated_at') or '')
        if updated_at:
            metadata.append(f'更新于 {updated_at}')
        if metadata:
            builder.markdown(
                f'<font color="grey">{" · ".join(metadata)}</font>'
            )
        turns = [
            dict(turn)
            for turn in (
                payload.get('turns')
                if isinstance(payload.get('turns'), list)
                else []
            )
            if isinstance(turn, dict)
        ]
        history_label = str(payload.get('history_label') or '')
        if history_label:
            builder.divider().markdown(f'**{history_label}**')
            if turns:
                for turn in turns:
                    query = str(turn.get('query') or '')
                    answer = str(turn.get('answer') or '')
                    builder.markdown(
                        f'**{query}**\n'
                        f'<font color="grey">{answer}</font>'
                    )
            else:
                builder.markdown(
                    '<font color="grey">这个会话还没有历史记录。</font>'
                )
        footer = str(payload.get('footer') or '')
        if footer:
            builder.divider().markdown(
                f'<font color="grey">{footer}</font>'
            )
        _add_workspace_actions(builder, provider_context)
        card = builder.build().data
        feature_labels = [
            str(label)
            for label in (
                payload.get('feature_labels')
                if isinstance(payload.get('feature_labels'), list)
                else []
            )
            if str(label)
        ]
        if feature_labels:
            _add_header_tags(
                card,
                [(label, 'purple') for label in feature_labels[:5]],
            )
        return card

    @staticmethod
    def _task_card(payload: dict[str, Any]) -> dict[str, Any]:
        title = str(payload.get('title') or '后台任务')
        mode = str(payload.get('mode') or '')
        status = str(payload.get('status') or 'pending')
        status_label, template = _task_status(status)
        agent_type = str(payload.get('agent_type') or '')
        if agent_type.lower() == 'workflow_step':
            return FeishuPresentationRenderer.task_workflow_card(
                [
                    {
                        **payload,
                        'progress_pct': payload.get('progress'),
                    }
                ],
                waiting_for_next_step=False,
            )
        agent_label = _task_agent_label(agent_type)
        builder = (
            new_card()
            .config(wide_screen_mode=True)
            .header(
                title[:80],
                subtitle=(
                    agent_label
                    or (
                        '后台任务'
                        if mode == 'manual'
                        else 'LazyMind 任务'
                    )
                ),
                template=template,
            )
        )
        phase = presentable_feishu_text(
            str(payload.get('current_phase') or '')
        )
        summary = presentable_feishu_text(
            str(payload.get('summary') or '')
        )
        progress = _optional_percent(payload.get('progress'))
        estimated_sec = _optional_non_negative_int(
            payload.get('estimated_sec')
        )
        has_primary_content = False
        if phase:
            builder.markdown(f'**当前阶段**\n{phase[:300]}')
            has_primary_content = True
        elif not summary:
            builder.markdown(f'当前状态：**{status_label}**')
            has_primary_content = True
        details: list[str] = []
        if estimated_sec is not None and not _task_terminal(status):
            details.append(f'预计剩余约 {estimated_sec} 秒')
        if details:
            builder.markdown(
                f'<font color="grey">{" · ".join(details)}</font>'
            )
            has_primary_content = True
        if summary:
            if has_primary_content:
                builder.divider()
            builder.markdown(
                f'**结果摘要**\n{summary[:1500]}'
                + ('…' if len(summary) > 1500 else '')
            )
        if _task_terminal(status):
            builder.footer(
                '任务已经结束；若有图片或文件，'
                '会作为飞书原生附件一并发送。'
            )
        else:
            builder.footer(
                '状态会在这张卡片中自动更新。'
            )
        card = builder.build().data
        tags = [(status_label, template)]
        if progress is not None:
            tags.append((f'{progress}%', 'blue'))
        _add_header_tags(card, tags)
        return card

    @staticmethod
    def task_workflow_card(
        tasks: list[dict[str, Any]],
        *,
        waiting_for_next_step: bool,
    ) -> dict[str, Any]:
        ordered = sorted(
            tasks,
            key=lambda task: int(
                task.get('seq_in_conversation') or 0
            ),
        )
        current = ordered[-1] if ordered else {}
        status = str(current.get('status') or 'pending')
        status_label, template = _task_status(status)
        waiting_for_retry = (
            waiting_for_next_step
            and status.lower() in {
                'failed',
                'cancelled',
                'canceled',
                'stopped',
                'interrupted',
            }
        )
        if waiting_for_retry:
            status_label, template = '等待自动重试', 'orange'
        elif waiting_for_next_step:
            status_label, template = '准备下一步', 'blue'
        title = _workflow_title(
            str(current.get('title') or '插件工作流')
        )
        builder = (
            new_card()
            .config(wide_screen_mode=True)
            .header(
                title,
                subtitle='LazyMind 插件工作流',
                template=template,
            )
        )
        if ordered:
            attempts: dict[str, int] = {}
            lines: list[str] = []
            for index, task in enumerate(ordered, start=1):
                step_key = _workflow_step_key(task)
                attempts[step_key] = attempts.get(step_key, 0) + 1
                lines.append(
                    _workflow_step_line(
                        index,
                        task,
                        attempt=attempts[step_key],
                    )
                )
            builder.markdown(
                '\n'.join(lines)
            )
        phase = presentable_feishu_text(
            str(current.get('current_phase') or '')
        )
        summary = _presentable_task_summary(
            str(current.get('summary') or '')
        )
        if phase and phase not in {'执行中...', '执行中…'}:
            builder.divider().markdown(
                f'**当前阶段**\n{phase[:500]}'
            )
        if summary and _task_terminal(status):
            builder.divider().markdown(
                f'**结果摘要**\n{summary[:1800]}'
                + ('…' if len(summary) > 1800 else '')
            )
        if waiting_for_retry:
            builder.footer(
                '本次尝试失败，Auto 模式正在等待并检测自动重试；'
                '后续步骤会继续更新在这张卡片中。'
            )
        elif waiting_for_next_step:
            builder.footer(
                '当前步骤已完成，正在等待插件进入下一步。'
            )
        elif _task_terminal(status):
            builder.footer(
                '插件工作流已经结束；最终图片或文件会继续以'
                '飞书原生消息发送。'
            )
        else:
            builder.footer('状态会在这张卡片中自动更新。')
        card = builder.build().data
        progress = _workflow_progress(ordered)
        tags = [(status_label, template)]
        if progress is not None:
            tags.append((f'{progress}%', 'blue'))
        _add_header_tags(card, tags)
        return card

    @staticmethod
    def _presentations(
        message: ClaimedOutbound,
    ) -> list[dict[str, Any]]:
        raw = message.metadata.get('presentations')
        return [
            dict(presentation)
            for presentation in (
                raw if isinstance(raw, list) else []
            )
            if isinstance(presentation, dict)
        ]

    @staticmethod
    def _ask_card(
        payload: dict[str, Any],
        provider_context: dict[str, Any],
    ) -> dict[str, Any]:
        title = str(
            payload.get('title') or '需要补充信息'
        )[:80]
        description = str(
            payload.get('description') or ''
        )[:1000]
        raw_questions = payload.get('questions')
        questions = [
            dict(question)
            for question in (
                raw_questions
                if isinstance(raw_questions, list)
                else []
            )
            if isinstance(question, dict)
        ]
        builder = (
            new_card()
            .config(wide_screen_mode=True)
            .header(title, template='orange')
        )
        button_rows = (
            FeishuPresentationRenderer._ask_button_rows(
                payload,
                questions[0],
                provider_context,
            )
            if len(questions) == 1
            else []
        )
        if description:
            builder.markdown(description).divider()
        form = None
        if not button_rows:
            form = _ask_form(payload, questions, provider_context)
        if form is not None:
            builder.raw(form)
        else:
            for index, question in enumerate(
                questions[:10],
                start=1,
            ):
                text = str(question.get('text') or '')
                display_text = text[:_MAX_ASK_QUESTION_CHARS]
                if len(text) > _MAX_ASK_QUESTION_CHARS:
                    display_text += '…'
                choices = question.get('choices')
                values = [
                    str(choice)
                    for choice in (
                        choices if isinstance(choices, list) else []
                    )
                    if str(choice)
                ]
                builder.markdown(f'**{index}. {display_text}**')
                if values and not (index == 1 and button_rows):
                    builder.markdown(
                        '　'.join(
                            (
                                f'{position}. '
                                f'{choice[:_MAX_ASK_CHOICE_CHARS]}'
                                + (
                                    '…'
                                    if len(choice)
                                    > _MAX_ASK_CHOICE_CHARS
                                    else ''
                                )
                            )
                            for position, choice in enumerate(
                                values[:10],
                                start=1,
                            )
                        )
                    )
            if len(questions) > 10:
                builder.markdown(
                    f'另有 {len(questions) - 10} 个问题，'
                    '请在对话中逐项补充。'
                )
            for row in button_rows:
                _add_button_grid_row(builder, row)
        if form is not None:
            footer = '请填写后提交，LazyMind 会在当前对话继续。'
        elif button_rows:
            footer = '请选择一个选项，LazyMind 会在当前对话继续。'
        else:
            footer = (
                '请在当前对话逐项说明，系统会把它作为'
                '继续任务的补充。'
            )
        builder.footer(footer)
        card = builder.build().data
        _add_header_tags(card, [('待回答', 'orange')])
        return card

    @staticmethod
    def _ask_button_rows(
        payload: dict[str, Any],
        question: dict[str, Any],
        provider_context: dict[str, Any],
    ) -> list[list[dict[str, Any]]]:
        question_type = str(question.get('type') or '')
        raw_choices = question.get('choices')
        choices = [
            str(choice)
            for choice in (
                raw_choices
                if isinstance(raw_choices, list)
                else []
            )
            if str(choice)
        ]
        if (
            question_type not in {'boolean', 'single'}
            or not choices
            or len(choices) > _MAX_ASK_BUTTON_CHOICES
            or len(str(question.get('text') or ''))
            > _MAX_ASK_QUESTION_CHARS
            or any(
                len(choice) > _MAX_ASK_CHOICE_CHARS
                for choice in choices
            )
        ):
            return []
        usable_choices = [
            choice for choice in choices if choice != _ASK_OTHER_OPTION
        ]
        rows: list[list[dict[str, Any]]] = []
        for start in range(0, len(usable_choices), 2):
            buttons = []
            for position, choice in enumerate(
                usable_choices[start:start + 2],
                start=start + 1,
            ):
                answer = {
                    'type': question_type,
                    'value': choice,
                }
                if question_type == 'single':
                    answer['otherText'] = ''
                structured = {
                    'ask_id': str(payload.get('ask_id') or ''),
                    'questions': [
                        {
                            'text': str(question.get('text') or ''),
                            'type': question_type,
                            'choices': choices,
                            'custom_choices': choices,
                            'answer': answer,
                        }
                    ],
                }
                buttons.append(
                    {
                        'label': (
                            choice
                            if len(choice) <= 40
                            else f'选择 {position}'
                        ),
                        'action': {
                            'lazymind_action': 'ask',
                            'text': (
                                f'{str(question.get("text") or "")}: '
                                f'{choice}'
                            ),
                            'ask_answers_structured': structured,
                            'intended_chat_id': str(
                                provider_context.get('chat_id')
                                or ''
                            ),
                        },
                        'style': (
                            'primary'
                            if start == 0 and not buttons
                            else 'default'
                        ),
                    }
                )
            rows.append(buttons)
        if (
            len(
                json.dumps(
                    rows,
                    ensure_ascii=False,
                    separators=(',', ':'),
                ).encode('utf-8')
            )
            > _MAX_ASK_ACTION_BYTES
        ):
            return []
        return rows


def _settings_card_items(
    payload: dict[str, Any],
    key: str,
) -> list[dict[str, Any]]:
    return [
        dict(item)
        for item in (
            payload.get(key)
            if isinstance(payload.get(key), list)
            else []
        )
        if isinstance(item, dict)
        and item.get('id')
        and item.get('name')
    ]


def _enabled_count(items: list[dict[str, Any]]) -> int:
    return sum(bool(item.get('enabled')) for item in items)


def _add_account_setting_buttons(
    builder,
    *,
    items: list[dict[str, Any]],
    setting: str,
    id_field: str,
    empty_text: str,
    provider_context: dict[str, Any],
    respect_can_disable: bool = False,
) -> None:
    if not items:
        builder.markdown(empty_text)
        return
    buttons: list[dict[str, Any]] = []
    for item in items:
        enabled = bool(item.get('enabled'))
        locked = (
            respect_can_disable
            and enabled
            and not bool(item.get('can_disable', True))
        )
        name = str(item.get('name') or '')[:40]
        label = f'{"✓" if enabled else "＋"} {name}'
        if locked:
            label += ' · 系统必需'
        buttons.append(
            {
                'label': label,
                'style': 'primary' if enabled else 'default',
                'disabled': locked,
                'action': _conversation_setting_update_action(
                    {
                        'setting': setting,
                        id_field: str(item.get('id') or ''),
                        'enabled': not enabled,
                    },
                    (
                        ('关闭' if enabled else '启用')
                        + name
                    ),
                    provider_context,
                ),
            }
        )
    for start in range(0, len(buttons), 2):
        _add_button_grid_row(builder, buttons[start:start + 2])


def _add_button_grid_row(
    builder,
    items: list[dict[str, Any]],
) -> None:
    columns: list[dict[str, Any]] = []
    for item in items:
        button = {
            'tag': 'button',
            'text': {
                'tag': 'plain_text',
                'content': str(item.get('label') or ''),
            },
            'type': str(item.get('style') or 'default'),
            'width': 'fill',
            'value': dict(item.get('action') or {}),
        }
        if bool(item.get('disabled')):
            button['disabled'] = True
        columns.append(
            {
                'tag': 'column',
                'width': 'weighted',
                'weight': 1,
                'vertical_align': 'center',
                'elements': [button],
            }
        )
    if len(columns) == 1:
        columns.append(
            {
                'tag': 'column',
                'width': 'weighted',
                'weight': 1,
                'elements': [
                    {
                        'tag': 'div',
                        'text': {
                            'tag': 'plain_text',
                            'content': ' ',
                        },
                    }
                ],
            }
        )
    builder.raw(
        {
            'tag': 'column_set',
            'flex_mode': 'bisect',
            'horizontal_spacing': '8px',
            'columns': columns,
        }
    )


def _capability_action(
    resource_type: str,
    label: str,
    provider_context: dict[str, Any],
) -> dict[str, Any]:
    resource_types = (
        [resource_type]
        if resource_type
        else list(_CAPABILITY_RESOURCE_TYPES)
    )
    text = f'查看{label}' if resource_type else label
    return {
        'lazymind_action': 'command',
        'text': text,
        'intended_chat_id': str(
            provider_context.get('chat_id') or ''
        ),
        'command_action': {
            'schema_version': '1',
            'command': 'capability.list',
            'parameters': {
                'capabilities': resource_types,
                'evidence': [label],
            },
        },
    }


def _conversation_settings_action(
    section: str,
    provider_context: dict[str, Any],
) -> dict[str, Any]:
    label = {
        'overview': '会话设置',
        'knowledge_base': '会话知识库',
        'workflow_mode': 'Workflow 执行方式',
        'subagent': 'SubAgent 设置',
        'skill': 'Skill 设置',
        'tool': '工具设置',
        'personalization': '个人习惯设置',
        'workflow': '可用 Workflow 设置',
    }.get(section, '会话设置')
    return {
        'lazymind_action': 'command',
        'text': label,
        'intended_chat_id': str(
            provider_context.get('chat_id') or ''
        ),
        'command_action': {
            'schema_version': '1',
            'command': 'conversation.settings',
            'parameters': {
                'section': section,
                'evidence': [label],
            },
        },
    }


def _conversation_setting_update_action(
    change: dict[str, Any],
    label: str,
    provider_context: dict[str, Any],
) -> dict[str, Any]:
    return {
        'lazymind_action': 'command',
        'text': label,
        'intended_chat_id': str(
            provider_context.get('chat_id') or ''
        ),
        'command_action': {
            'schema_version': '1',
            'command': 'conversation.settings.update',
            'parameters': {
                'change': dict(change),
                'evidence': [label],
            },
        },
    }


def _add_workspace_actions(
    builder,
    provider_context: dict[str, Any],
) -> None:
    rows = _workspace_action_rows(provider_context)
    if not rows:
        return
    builder.divider().markdown('**快捷操作**')
    for row in rows:
        builder.raw(row)


def _workspace_action_rows(
    provider_context: dict[str, Any],
) -> list[dict[str, Any]]:
    chat_id = str(provider_context.get('chat_id') or '')
    if not chat_id:
        return []
    builder = new_card()
    for start in range(0, len(_WORKSPACE_ACTIONS), 2):
        _add_button_grid_row(
            builder,
            [
                {
                    'label': label,
                    'style': style,
                    'action': {
                        'lazymind_action': 'command',
                        'text': text,
                        'intended_chat_id': chat_id,
                        'command_action': command,
                    },
                }
                for label, text, command, style
                in _WORKSPACE_ACTIONS[start:start + 2]
            ],
        )
    return list(builder.build().data['body']['elements'])


def _ask_form(
    payload: dict[str, Any],
    questions: list[dict[str, Any]],
    provider_context: dict[str, Any],
) -> dict[str, Any] | None:
    usable = questions[:10]
    if not usable:
        return None
    fields: list[dict[str, Any]] = []
    schema: list[dict[str, Any]] = []
    for index, question in enumerate(usable, start=1):
        field_name = f'ask_q_{index}'
        question_text = str(question.get('text') or '')
        question_type = str(question.get('type') or 'text')
        choices = _ask_choices(question)
        field = _ask_form_field(
            field_name,
            question_type,
            choices,
        )
        if field is None:
            return None
        fields.append(
            {
                'tag': 'markdown',
                'content': (
                    f'**{index}. '
                    f'{question_text[:_MAX_ASK_QUESTION_CHARS]}**'
                ),
            }
        )
        fields.append(field)
        other_name = ''
        if (
            question_type in {'single', 'multiple'}
            and _ASK_OTHER_OPTION in choices
        ):
            other_name = f'{field_name}_other'
            fields.append(
                {
                    'tag': 'markdown',
                    'content': (
                        '<font color="grey">'
                        '选择“其他”时请补充说明</font>'
                    ),
                }
            )
            fields.append(
                {
                    'tag': 'input',
                    'element_id': f'ask_other_{index}',
                    'name': other_name,
                    'required': False,
                    'input_type': 'text',
                    'width': 'fill',
                    'max_length': 1000,
                    'placeholder': {
                        'tag': 'plain_text',
                        'content': '请输入补充内容',
                    },
                }
            )
        schema.append(
            {
                'name': field_name,
                'other_name': other_name,
                'text': question_text,
                'type': question_type,
                'choices': choices,
            }
        )
    action = {
        'lazymind_action': 'ask',
        'ask_id': str(payload.get('ask_id') or ''),
        'ask_form_questions': schema,
        'intended_chat_id': str(
            provider_context.get('chat_id') or ''
        ),
    }
    if (
        len(
            json.dumps(
                action,
                ensure_ascii=False,
                separators=(',', ':'),
            ).encode('utf-8')
        )
        > _MAX_ASK_ACTION_BYTES
    ):
        return None
    fields.append(
        {
            'tag': 'column_set',
            'flex_mode': 'none',
            'horizontal_spacing': '8px',
            'columns': [
                {
                    'tag': 'column',
                    'width': 'weighted',
                    'weight': 1,
                    'elements': [
                        {
                            'tag': 'button',
                            'name': 'ask_submit',
                            'text': {
                                'tag': 'plain_text',
                                'content': '提交回答',
                            },
                            'type': 'primary',
                            'width': 'fill',
                            'form_action_type': 'submit',
                            'value': action,
                        }
                    ],
                }
            ],
        }
    )
    return {
        'tag': 'form',
        'name': 'ask_form',
        'elements': fields,
    }


def _ask_form_field(
    field_name: str,
    question_type: str,
    choices: list[str],
) -> dict[str, Any] | None:
    common = {
        'element_id': f'{field_name}_field',
        'name': field_name,
        'required': True,
        'width': 'fill',
    }
    if question_type == 'text':
        return {
            'tag': 'input',
            **common,
            'input_type': 'multiline_text',
            'rows': 2,
            'auto_resize': True,
            'max_rows': 5,
            'max_length': 1000,
            'placeholder': {
                'tag': 'plain_text',
                'content': '请输入回答',
            },
        }
    if question_type in {'boolean', 'single'} and choices:
        return {
            'tag': 'select_static',
            **common,
            'placeholder': {
                'tag': 'plain_text',
                'content': '请选择',
            },
            'options': _ask_select_options(choices),
        }
    if question_type == 'multiple' and choices:
        return {
            'tag': 'multi_select_static',
            **common,
            'placeholder': {
                'tag': 'plain_text',
                'content': '可选择多项',
            },
            'options': _ask_select_options(choices),
        }
    return None


def _ask_choices(question: dict[str, Any]) -> list[str]:
    raw = question.get('choices')
    return [
        str(choice)
        for choice in (raw if isinstance(raw, list) else [])
        if str(choice)
    ]


def _ask_select_options(choices: list[str]) -> list[dict[str, Any]]:
    return [
        {
            'text': {
                'tag': 'plain_text',
                'content': choice[:_MAX_ASK_CHOICE_CHARS],
            },
            'value': choice,
        }
        for choice in choices[:20]
    ]


def _selection_option_count(payload: dict[str, Any]) -> int:
    options = payload.get('options')
    if not isinstance(options, list):
        return 0
    return sum(
        1
        for option in options
        if isinstance(option, dict)
        and option.get('label')
        and option.get('value')
    )


def _selection_button_label(value: str, label: str) -> str:
    rendered = f'{value}. {label}'
    return rendered if len(rendered) <= 40 else f'选择 {value}'


def _merge_reference_parts(
    parts: list[dict[str, Any]],
) -> list[dict[str, Any]]:
    merged: list[dict[str, Any]] = []
    for part in parts:
        if (
            part.get('kind') == 'text'
            and str(part.get('text') or '').startswith('参考来源：')
            and merged
            and merged[-1].get('kind') == 'text'
        ):
            previous = str(merged[-1].get('text') or '')
            references = str(part.get('text') or '')
            combined = f'{previous}\n\n{references}'
            if len(combined) <= _MAX_MERGED_REFERENCE_CHARS:
                merged[-1] = {
                    **merged[-1],
                    'text': combined,
                }
                continue
        merged.append(part)
    return merged


def _split_reference_section(text: str) -> tuple[str, str]:
    marker = '\n\n参考来源：'
    if marker in text:
        answer, references = text.split(marker, 1)
        return answer.strip(), references.strip()
    if text.startswith('参考来源：'):
        return '', text[len('参考来源：'):].strip()
    return text, ''


def _add_header_tags(
    card: dict[str, Any],
    tags: list[tuple[str, str]],
) -> None:
    header = card.get('header')
    if not isinstance(header, dict):
        return
    header['text_tag_list'] = [
        {
            'tag': 'text_tag',
            'text': {
                'tag': 'plain_text',
                'content': label,
            },
            'color': _header_tag_color(color),
        }
        for label, color in tags[:3]
        if label
    ]


def _header_tag_color(color: str) -> str:
    return {
        'grey': 'neutral',
        'default': 'neutral',
    }.get(color, color)


def _optional_percent(value: Any) -> int | None:
    number = _optional_non_negative_int(value)
    return min(number, 100) if number is not None else None


def _optional_non_negative_int(value: Any) -> int | None:
    if isinstance(value, bool):
        return None
    try:
        return max(0, int(value)) if value is not None else None
    except (TypeError, ValueError):
        return None


def _task_terminal(status: str) -> bool:
    return status.lower() in {
        'completed',
        'succeeded',
        'success',
        'failed',
        'cancelled',
        'canceled',
        'stopped',
        'interrupted',
    }


def _task_status(status: str) -> tuple[str, str]:
    normalized = status.lower()
    return {
        'pending': ('等待执行', 'blue'),
        'created': ('已创建', 'blue'),
        'running': ('执行中', 'wathet'),
        'completed': ('已完成', 'green'),
        'succeeded': ('已完成', 'green'),
        'success': ('已完成', 'green'),
        'failed': ('执行失败', 'red'),
        'cancelled': ('已取消', 'grey'),
        'canceled': ('已取消', 'grey'),
        'stopped': ('已停止', 'grey'),
        'interrupted': ('已中断', 'grey'),
    }.get(normalized, (status or '已创建', 'blue'))


def _task_agent_label(agent_type: str) -> str:
    return {
        'workflow_step': '工作流',
        'subagent': '智能任务',
        'task': '后台任务',
    }.get(agent_type.lower(), agent_type)


def _workflow_title(task_title: str) -> str:
    plugin = task_title.split(':', 1)[0].strip().lower()
    return {
        'writer-workflow': 'AI Writer 写作工作流',
        'image-workflow': 'AI 绘图工作流',
        'ppt-plugin': 'AI PPT 工作流',
    }.get(
        plugin,
        (
            f'{plugin.removesuffix("-plugin")} 工作流'
            if plugin
            else '插件工作流'
        ),
    )


def _workflow_step_line(
    index: int,
    task: dict[str, Any],
    *,
    attempt: int,
) -> str:
    status = str(task.get('status') or 'pending').lower()
    icon = {
        'completed': '✅',
        'succeeded': '✅',
        'success': '✅',
        'failed': '❌',
        'cancelled': '⏹️',
        'canceled': '⏹️',
        'stopped': '⏹️',
        'interrupted': '⏸️',
        'running': '🔄',
    }.get(status, '⏳')
    step = _workflow_step_key(task)
    label = {
        'prepare': '准备素材与上下文',
        'outline': '生成大纲',
        'write_document': '撰写正文',
        'write-document': '撰写正文',
        'deliver': '交付结果',
        'generate': '生成内容',
        'analyze_subject': '分析主题',
        'collect_materials': '收集素材',
        'optimize_prompt': '优化提示词',
        'generate_image': '生成图片',
        'enhance_image': '编辑图片',
        'video_to_gif': '转换为动图',
    }.get(step.lower(), step.replace('_', ' ') or f'步骤 {index}')
    if attempt > 1:
        label = f'{label}（重试 {attempt - 1}）'
    status_label, _template = _task_status(status)
    return f'{icon} **{index}. {label}**　{status_label}'


def _workflow_step_key(task: dict[str, Any]) -> str:
    raw_title = str(task.get('title') or '')
    return raw_title.split(':', 1)[-1].strip().lower()


def _workflow_progress(
    tasks: list[dict[str, Any]],
) -> int | None:
    if not tasks:
        return None
    return _optional_percent(
        tasks[-1].get(
            'progress_pct',
            tasks[-1].get('progress'),
        )
    )


def _presentable_task_summary(value: str) -> str:
    summary = presentable_feishu_text(value)
    image_count = len(_MARKDOWN_IMAGE.findall(summary))
    summary = _MARKDOWN_IMAGE.sub('', summary).strip()
    if image_count:
        summary = (
            f'{summary}\n\n'
            f'🖼️ 已生成 {image_count} 张图片，'
            '将以飞书原图发送。'
        ).strip()
    for marker in (
        '\n执行路径：',
        '\n执行路径:',
        '\n[assistant]',
        '\n[tool:',
    ):
        summary = summary.split(marker, 1)[0]
    if len(summary) > 800:
        return f'{summary[:800].rstrip()}…'
    return summary

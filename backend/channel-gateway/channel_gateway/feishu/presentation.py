from __future__ import annotations

import json
from typing import Any

from lark_channel import new_card

from channel_gateway.common.domain.channel import ClaimedOutbound
from channel_gateway.common.domain.outbound import OutboundRenderer


_CARD_HEADERS = {
    'conversation.new': ('✨ 新会话', 'green'),
    'conversation.list': ('🕘 历史会话', 'blue'),
    'conversation.switch': ('✅ 会话已切换', 'green'),
    'conversation.current': ('📍 当前会话', 'blue'),
    'history.more': ('📖 会话历史', 'blue'),
    'capability.list': ('🧰 LazyMind 能力', 'purple'),
    'capability.configure': ('✅ 能力已更新', 'green'),
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


class FeishuPresentationRenderer:
    """Renders common reply parts as Feishu cards without changing Core data."""

    def __init__(self, base: OutboundRenderer):
        self._base = base

    def render(self, message: ClaimedOutbound) -> list[dict[str, Any]]:
        presentations = self._presentations(message)
        parts = _merge_reference_parts(self._base.render(message))
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
            self._presentation_cards(message, presentations)
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
        answer, references = _split_reference_section(text)
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
            'root_message_id': str(
                provider_context.get('root_message_id') or ''
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
    ) -> list[dict[str, Any]]:
        cards: list[dict[str, Any]] = []
        for presentation in presentations:
            kind = str(presentation.get('kind') or '')
            if kind == 'ask':
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
                    }
                )
        return cards

    @staticmethod
    def _task_card(payload: dict[str, Any]) -> dict[str, Any]:
        title = str(payload.get('title') or '后台任务')
        mode = str(payload.get('mode') or '')
        status = str(payload.get('status') or 'pending')
        status_label, template = _task_status(status)
        agent_type = str(payload.get('agent_type') or '')
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
        phase = str(payload.get('current_phase') or '')
        summary = str(payload.get('summary') or '')
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
                '可在当前话题中继续询问任务进度；'
                '当前不会主动推送异步完成通知。'
            )
        card = builder.build().data
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
                    '请在话题中逐项补充。'
                )
            for row in button_rows:
                _add_button_grid_row(builder, row)
        if form is not None:
            footer = '请填写后提交，LazyMind 会在当前话题继续。'
        elif button_rows:
            footer = '请选择一个选项，LazyMind 会在当前话题继续。'
        else:
            footer = (
                '请在当前话题逐项说明，系统会把它作为'
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
                            'root_message_id': str(
                                provider_context.get(
                                    'root_message_id'
                                )
                                or ''
                            ),
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


def _add_button_grid_row(
    builder,
    items: list[dict[str, Any]],
) -> None:
    columns = [
        {
            'tag': 'column',
            'width': 'weighted',
            'weight': 1,
            'vertical_align': 'center',
            'elements': [
                {
                    'tag': 'button',
                    'text': {
                        'tag': 'plain_text',
                        'content': str(item.get('label') or ''),
                    },
                    'type': str(item.get('style') or 'default'),
                    'width': 'fill',
                    'value': dict(item.get('action') or {}),
                }
            ],
        }
        for item in items
    ]
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
            index,
            field_name,
            question_text,
            question_type,
            choices,
        )
        if field is None:
            return None
        fields.append(field)
        other_name = ''
        if (
            question_type in {'single', 'multiple'}
            and _ASK_OTHER_OPTION in choices
        ):
            other_name = f'{field_name}_other'
            fields.append(
                {
                    'tag': 'input',
                    'element_id': f'ask_other_{index}',
                    'name': other_name,
                    'required': False,
                    'input_type': 'text',
                    'width': 'fill',
                    'max_length': 1000,
                    'label': {
                        'tag': 'plain_text',
                        'content': '其他说明（选择“其他”时填写）',
                    },
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
        'root_message_id': str(
            provider_context.get('root_message_id') or ''
        ),
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
                            'action_type': 'form_submit',
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
    index: int,
    field_name: str,
    question_text: str,
    question_type: str,
    choices: list[str],
) -> dict[str, Any] | None:
    label = f'{index}. {question_text}'
    common = {
        'element_id': f'ask_field_{index}',
        'name': field_name,
        'required': True,
        'width': 'fill',
        'label': {
            'tag': 'plain_text',
            'content': label[:500],
        },
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
    }.get(normalized, (status or '已创建', 'blue'))


def _task_agent_label(agent_type: str) -> str:
    return {
        'plugin_step': '工作流',
        'subagent': '智能任务',
        'task': '后台任务',
    }.get(agent_type.lower(), agent_type)

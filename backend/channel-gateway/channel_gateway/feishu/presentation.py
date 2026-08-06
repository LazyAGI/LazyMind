from __future__ import annotations

import json
import re
from dataclasses import replace
from typing import Any

from lark_channel import new_card

from channel_gateway.common.domain.channel import (
    ClaimedOutbound,
    sanitize_channel_text,
)
from channel_gateway.common.domain.outbound import OutboundRenderer
from channel_gateway.feishu.workspace import (
    FeishuWorkspaceRenderer,
    is_feishu_image_key,
)


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
    text = media_free_feishu_text(cleaned)
    notice = (
        f'🖼️ 已生成 {image_count} 张图片，'
        '正在作为飞书原图发送…'
    )
    return f'{text}\n\n{notice}' if text else notice


def media_free_feishu_text(value: str) -> str:
    return _MARKDOWN_IMAGE.sub('', presentable_feishu_text(value)).strip()


class FeishuReplyRenderer:
    """Renders one native-chat reply without workspace navigation or input."""

    @staticmethod
    def render(
        *,
        provider_context: dict[str, Any],
        text: str,
        presentations: list[dict[str, Any]],
        status: str = '✅ **回答完成**',
        thinking: str = '分析与处理已完成。',
        streaming: bool = False,
        extra_elements: list[dict[str, Any]] | None = None,
    ) -> dict[str, Any]:
        state = provider_context.get('workspace_state')
        state = state if isinstance(state, dict) else {}
        language = str(state.get('output_language') or 'zh')
        show_process = bool(state.get('show_process', True))
        collapse_process = bool(
            state.get('auto_collapse_process', True)
        )
        process_title = (
            'Execution summary' if language == 'en' else '执行摘要'
        )
        answer = text or (
            '<font color="grey">Preparing the answer…</font>'
            if language == 'en'
            else '<font color="grey">正在准备回答…</font>'
        )
        elements: list[dict[str, Any]] = [
            {
                'tag': 'markdown',
                'element_id': 'lazymind_status',
                'content': status,
            }
        ]
        if show_process:
            elements.append(
                {
                    'tag': 'collapsible_panel',
                    'expanded': streaming or not collapse_process,
                    'header': {
                        'title': {
                            'tag': 'plain_text',
                            'content': process_title,
                        }
                    },
                    'elements': [
                        {
                            'tag': 'markdown',
                            'element_id': 'lazymind_thinking',
                            'content': thinking,
                        }
                    ],
                }
            )
        elements.append(
            {
                'tag': 'markdown',
                'element_id': 'lazymind_answer',
                'content': answer,
            }
        )
        for image in (
            state.get('images')
            if isinstance(state.get('images'), list)
            else []
        ):
            if not isinstance(image, dict):
                continue
            image_key = str(image.get('image_key') or '')
            if not is_feishu_image_key(image_key):
                continue
            element: dict[str, Any] = {
                'tag': 'img',
                'img_key': image_key,
            }
            caption = str(image.get('caption') or '').strip()
            if caption:
                element['alt'] = {
                    'tag': 'plain_text',
                    'content': caption[:300],
                }
            elements.append(element)
        if streaming:
            elements.append(
                {
                    'tag': 'button',
                    'name': 'cancel_generation',
                    'text': {
                        'tag': 'plain_text',
                        'content': (
                            'Cancel generation'
                            if language == 'en'
                            else '取消本次生成'
                        ),
                    },
                    'type': 'danger',
                    'width': 'fill',
                    'value': {
                        'lazymind_action': 'local',
                        'text': (
                            'Cancel generation'
                            if language == 'en'
                            else '取消本次生成'
                        ),
                        'workspace_action': {
                            'kind': 'operation.cancel',
                        },
                        'intended_chat_id': str(
                            provider_context.get('chat_id') or ''
                        ),
                    },
                }
            )
        elements.extend(extra_elements or [])
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
                        'LazyMind is replying'
                        if streaming and language == 'en'
                        else 'LazyMind 正在回答'
                        if streaming
                        else 'LazyMind'
                    )
                },
            },
            'header': {
                'title': {'tag': 'plain_text', 'content': 'LazyMind'},
                'template': 'blue',
            },
            'body': {'elements': elements},
        }


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
        value == _ASK_OTHER_OPTION
        or isinstance(value, list) and _ASK_OTHER_OPTION in value
    ):
        return rendered.replace(_ASK_OTHER_OPTION, other_text)
    return rendered


def streaming_reply_card(
    provider_context: dict[str, Any],
) -> dict[str, Any]:
    language = _reply_language(provider_context)
    return FeishuReplyRenderer.render(
        provider_context=provider_context,
        text='',
        presentations=[],
        status=(
            '⏳ **Understanding your question**'
            if language == 'en'
            else '⏳ **正在理解你的问题**'
        ),
        thinking=(
            'Analyzing the request…'
            if language == 'en'
            else '正在分析问题…'
        ),
        streaming=True,
    )


def _reply_language(provider_context: dict[str, Any]) -> str:
    workspace = provider_context.get('workspace_state')
    if not isinstance(workspace, dict):
        return 'zh'
    return 'en' if workspace.get('output_language') == 'en' else 'zh'


class FeishuPresentationRenderer:
    """Renders every reply into the current CardKit workspace."""

    def __init__(self, base: OutboundRenderer):
        self._base = base

    def render(self, message: ClaimedOutbound) -> list[dict[str, Any]]:
        presentations = self._presentations(message)
        workspace = message.provider_context.get('workspace_state')
        workspace = workspace if isinstance(workspace, dict) else {}
        render_message = (
            replace(
                message,
                metadata={**message.metadata, 'sources': []},
            )
            if not bool(workspace.get('show_sources', True))
            else message
        )
        parts = _merge_reference_parts(self._base.render(render_message))
        text = '\n\n'.join(
            str(part.get('text') or '')
            for part in parts
            if part.get('kind') == 'text'
        ) or message.text
        extra_elements = _ask_elements(
            presentations,
            message.provider_context,
        )
        if (
            message.provider_context.get('workspace_surface')
            == 'management'
        ):
            return [
                {
                    'kind': 'card',
                    'card': FeishuWorkspaceRenderer.render(
                        provider_context=message.provider_context,
                        text=presentable_feishu_text(text),
                        presentations=presentations,
                    ),
                    'workspace': True,
                    'workspace_text': presentable_feishu_text(text),
                    'workspace_presentations': presentations,
                }
            ]
        task = next(
            (
                presentation
                for presentation in presentations
                if presentation.get('kind') == 'task'
            ),
            {},
        )
        workspace_text = presentable_feishu_text(text)
        language = _reply_language(message.provider_context)
        non_text_parts = [
            part for part in parts if part.get('kind') != 'text'
        ]
        has_sources = bool(
            workspace.get('show_sources', True)
            and isinstance(message.metadata.get('sources'), list)
            and message.metadata.get('sources')
        )
        if (
            message.metadata.get('streamed_text') is True
            and not extra_elements
            and message.intent_kind != 'failed'
            and not has_sources
        ):
            return non_text_parts
        card_part = {
            'kind': 'card',
            'card': FeishuReplyRenderer.render(
                provider_context=message.provider_context,
                text=workspace_text,
                presentations=presentations,
                status=(
                    '⚠️ **Answer failed**'
                    if message.intent_kind == 'failed' and language == 'en'
                    else '⚠️ **回答失败**'
                    if message.intent_kind == 'failed'
                    else '✅ **Answer complete**'
                    if language == 'en'
                    else '✅ **回答完成**'
                ),
                thinking=(
                    'Analysis and processing complete.'
                    if language == 'en'
                    else '分析与处理已完成。'
                ),
                extra_elements=extra_elements,
            ),
            'workspace': False,
            'replace_message_id': str(
                message.provider_context.get(
                    'workspace_stream_message_id'
                )
                or ''
            ),
            'workspace_text': workspace_text,
            'workspace_presentations': presentations,
            'task_id': str(task.get('task_id') or ''),
            'conversation_id': str(task.get('conversation_id') or ''),
        }
        return [
            card_part,
            *non_text_parts,
        ]

    @staticmethod
    def task_workflow_card(
        tasks: list[dict[str, Any]],
        *,
        waiting_for_next_step: bool,
    ) -> dict[str, Any]:
        """Render legacy in-flight task outbounds during rolling upgrades."""
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
        builder = (
            new_card()
            .config(wide_screen_mode=True)
            .header(
                _workflow_title(
                    str(current.get('title') or '插件工作流')
                ),
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
            builder.markdown('\n'.join(lines))
        phase = presentable_feishu_text(
            str(current.get('current_phase') or '')
        )
        summary = _presentable_task_summary(
            str(current.get('summary') or '')
        )
        if phase and phase not in {'执行中...', '执行中…'}:
            builder.divider().markdown(f'**当前阶段**\n{phase[:500]}')
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


def _ask_elements(
    presentations: list[dict[str, Any]],
    provider_context: dict[str, Any],
) -> list[dict[str, Any]]:
    ask = next(
        (
            presentation
            for presentation in presentations
            if presentation.get('kind') == 'ask'
        ),
        None,
    )
    if ask is None:
        return []
    questions = [
        dict(question)
        for question in (
            ask.get('questions')
            if isinstance(ask.get('questions'), list)
            else []
        )
        if isinstance(question, dict) and question.get('text')
    ]
    form = _ask_form(ask, questions, provider_context)
    elements: list[dict[str, Any]] = [
        {'tag': 'hr'},
        {
            'tag': 'markdown',
            'content': (
                '💬 **需要你的回答**\n'
                f'**{str(ask.get("title") or "补充信息")}**\n'
                f'{str(ask.get("description") or "")}\n'
                '<font color="grey">直接在卡片内选择或填写，提交后任务会自动继续。</font>'
            ).strip(),
        },
    ]
    quick_choices = _ask_quick_choice_buttons(
        ask,
        questions,
        provider_context,
    )
    if quick_choices:
        elements.extend(quick_choices)
        return elements
    if form is not None:
        elements.append(form)
        return elements
    question_text = '\n'.join(
        f'{index}. {str(question.get("text") or "")}'
        for index, question in enumerate(questions, start=1)
    )
    elements.append(
        {
            'tag': 'markdown',
            'content': (
                f'{question_text}\n\n'
                '<font color="grey">问题暂时无法在卡片中提交，请刷新后重试。</font>'
            ).strip(),
        }
    )
    return elements


def _ask_quick_choice_buttons(
    payload: dict[str, Any],
    questions: list[dict[str, Any]],
    provider_context: dict[str, Any],
) -> list[dict[str, Any]]:
    if len(questions) != 1:
        return []
    question = questions[0]
    question_type = str(question.get('type') or 'text')
    choices = _ask_choices(question)
    if question_type not in {'boolean', 'single'} or not 1 <= len(choices) <= 6:
        return []
    question_text = str(question.get('text') or '')
    rows: list[dict[str, Any]] = [
        {
            'tag': 'markdown',
            'content': f'**{question_text[:_MAX_ASK_QUESTION_CHARS]}**',
        }
    ]
    for start in range(0, len(choices), 2):
        columns = []
        for offset, choice in enumerate(choices[start:start + 2]):
            answer = {
                'type': question_type,
                'value': choice,
            }
            if question_type == 'single':
                answer['otherText'] = ''
            action = {
                'lazymind_action': 'ask',
                'text': f'{question_text}: {choice}',
                'ask_id': str(payload.get('ask_id') or ''),
                'ask_answers_structured': {
                    'ask_id': str(payload.get('ask_id') or ''),
                    'questions': [
                        {
                            'text': question_text,
                            'type': question_type,
                            'choices': choices,
                            'custom_choices': choices,
                            'answer': answer,
                        }
                    ],
                },
                'intended_chat_id': str(provider_context.get('chat_id') or ''),
            }
            columns.append(
                {
                    'tag': 'column',
                    'width': 'weighted',
                    'weight': 1,
                    'elements': [
                        {
                            'tag': 'button',
                            'name': f'ask_quick_answer_{start + offset + 1}',
                            'text': {
                                'tag': 'plain_text',
                                'content': choice[:_MAX_ASK_CHOICE_CHARS],
                            },
                            'type': 'primary',
                            'width': 'fill',
                            'value': action,
                        }
                    ],
                }
            )
        rows.append(
            {
                'tag': 'column_set',
                'flex_mode': 'none',
                'horizontal_spacing': '8px',
                'columns': columns,
            }
        )
    return rows


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
            fields.extend(
                [
                    {
                        'tag': 'markdown',
                        'content': (
                            '<font color="grey">'
                            '选择“其他”时请补充说明</font>'
                        ),
                    },
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
                    },
                ]
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
        'intended_chat_id': str(provider_context.get('chat_id') or ''),
    }
    if len(
        json.dumps(
            action,
            ensure_ascii=False,
            separators=(',', ':'),
        ).encode('utf-8')
    ) > _MAX_ASK_ACTION_BYTES:
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
            'rows': 3,
            'auto_resize': True,
            'max_rows': 8,
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
    choices = [
        str(choice)
        for choice in (raw if isinstance(raw, list) else [])
        if str(choice)
    ]
    if not choices and str(question.get('type') or '') == 'boolean':
        return ['是', '否']
    return choices


def workspace_ask_elements(
    presentations: list[dict[str, Any]],
    provider_context: dict[str, Any],
) -> list[dict[str, Any]]:
    return _ask_elements(presentations, provider_context)


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
                merged[-1] = {**merged[-1], 'text': combined}
                continue
        merged.append(part)
    return merged


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
            'text': {'tag': 'plain_text', 'content': label},
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
    if isinstance(value, bool):
        return None
    try:
        return max(0, min(100, int(value))) if value is not None else None
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


def _workflow_title(task_title: str) -> str:
    plugin = task_title.split(':', 1)[0].strip().lower()
    return {
        'writer-plugin': 'AI Writer 写作工作流',
        'image-plugin': 'AI 绘图工作流',
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
            f'🖼️ 已生成 {image_count} 张图片，将以飞书原图发送。'
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

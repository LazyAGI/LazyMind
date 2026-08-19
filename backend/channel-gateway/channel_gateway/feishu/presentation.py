from __future__ import annotations

import re
from dataclasses import replace
from typing import Any

from lark_channel import new_card

from channel_gateway.common.domain.channel import (
    ClaimedOutbound,
    sanitize_channel_text,
)
from channel_gateway.common.domain.outbound import OutboundRenderer, optional_int
from channel_gateway.feishu.cardkit import (
    ASK_OTHER_OPTION,
    MAX_ASK_QUESTION_CHARS,
    ask_form,
)
from channel_gateway.feishu.workspace import (
    FeishuWorkspaceRenderer,
    is_feishu_image_key,
)


_MAX_MERGED_REFERENCE_CHARS = 6000
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
        status: str = '✅ **回答完成**',
        streaming: bool = False,
        extra_elements: list[dict[str, Any]] | None = None,
    ) -> dict[str, Any]:
        state = provider_context.get('workspace_state')
        state = state if isinstance(state, dict) else {}
        language = str(state.get('output_language') or 'zh')
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
                'id': str(raw_question.get('id') or ''),
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
        value == ASK_OTHER_OPTION
        or isinstance(value, list) and ASK_OTHER_OPTION in value
    ):
        return rendered.replace(ASK_OTHER_OPTION, other_text)
    return rendered


def streaming_reply_card(
    provider_context: dict[str, Any],
) -> dict[str, Any]:
    language = _reply_language(provider_context)
    return FeishuReplyRenderer.render(
        provider_context=provider_context,
        text='',
        status=(
            '⏳ **Understanding your question**'
            if language == 'en'
            else '⏳ **正在理解你的问题**'
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
        if message.provider_context.get('_workspace_delivery_suppressed'):
            return []
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
        extra_elements = [
            *_execution_elements(presentations, message.provider_context),
            *_ask_elements(presentations, message.provider_context),
        ]
        workspace_surface = message.provider_context.get(
            'workspace_surface'
        )
        if workspace_surface == 'management':
            return [{
                'kind': 'card',
                'card': FeishuWorkspaceRenderer.render(
                    provider_context=message.provider_context,
                    presentations=presentations,
                ),
                'workspace': True,
            }]
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
            and not task
            and message.intent_kind != 'failed'
            and not has_sources
        ):
            return non_text_parts
        card_part = {
            'kind': 'card',
            'card': FeishuReplyRenderer.render(
                provider_context=message.provider_context,
                text=workspace_text,
                status=(
                    '⚠️ **Answer failed**'
                    if message.intent_kind == 'failed' and language == 'en'
                    else '⚠️ **回答失败**'
                    if message.intent_kind == 'failed'
                    else '✅ **Answer complete**'
                    if language == 'en'
                    else '✅ **回答完成**'
                ),
                extra_elements=extra_elements,
            ),
            'workspace': (
                message.provider_context.get(
                    'workspace_reanchor_to_bottom'
                ) is True
            ),
            'replace_message_id': str(
                message.provider_context.get(
                    'workspace_stream_message_id'
                )
                or ''
            ),
            'task_id': str(task.get('task_id') or ''),
            'conversation_id': str(task.get('conversation_id') or ''),
        }
        return [
            card_part,
            *non_text_parts,
        ]

    @staticmethod
    def task_card(
        task: dict[str, Any],
        *,
        inflight_image_count: int,
        failed_image_count: int,
        omitted_image_count: int,
    ) -> dict[str, Any]:
        """Render the task state reported by Core without inferring a workflow."""
        status = str(task.get('status') or 'pending')
        status_label, template = _task_status(status)
        title = presentable_feishu_text(
            str(task.get('title') or '后台任务')
        )[:200]
        builder = (
            new_card()
            .config(wide_screen_mode=True)
            .header(
                title,
                subtitle='LazyMind 后台任务',
                template=template,
            )
        )
        progress = _optional_percent(
            task.get('progress_pct', task.get('progress'))
        )
        task_line = f'**状态**　{status_label}'
        if progress is not None:
            task_line += f'　{progress}%'
        builder.markdown(task_line)
        phase = presentable_feishu_text(
            str(task.get('current_phase') or '')
        )
        summary = _presentable_task_summary(
            str(task.get('summary') or '')
        )
        if phase and phase not in {'执行中...', '执行中…'}:
            builder.divider().markdown(f'**当前阶段**\n{phase[:500]}')
        if summary and _task_terminal(status):
            builder.divider().markdown(
                f'**结果摘要**\n{summary[:1800]}'
                + ('…' if len(summary) > 1800 else '')
            )
        delivery_notices: list[str] = []
        if inflight_image_count > 0:
            delivery_notices.append(
                f'{inflight_image_count} 张图片正在通过飞书原生消息投递'
            )
        if failed_image_count > 0:
            delivery_notices.append(
                f'{failed_image_count} 张图片投递失败'
            )
        if omitted_image_count > 0:
            delivery_notices.append(
                f'{omitted_image_count} 张历史图片超过每个任务 20 张的展示上限'
            )
        if delivery_notices:
            builder.footer('；'.join(delivery_notices) + '。')
        elif _task_terminal(status):
            builder.footer('任务已结束；最终图片会继续以飞书原生消息发送。')
        else:
            builder.footer('状态会在这张卡片中自动更新。')
        card = builder.build().data
        tags = [(status_label, template)]
        if progress is not None:
            tags.append((f'{progress}%', 'blue'))
        _add_header_tags(card, tags)
        return card

    @staticmethod
    def task_replaced_card() -> dict[str, Any]:
        return (
            new_card()
            .config(wide_screen_mode=True)
            .header(
                '任务卡已更新',
                subtitle='LazyMind 后台任务',
                template='grey',
            )
            .markdown('请使用此会话中最新的任务卡片。')
            .build()
            .data
        )

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
    form = ask_form(ask, questions, provider_context)
    elements: list[dict[str, Any]] = [
        {'tag': 'hr'},
        {
            'tag': 'markdown',
            'content': (
                '💬 **需要你的回答**\n'
                f'**{str(ask.get("title") or "补充信息")[:200]}**\n'
                f'{str(ask.get("description") or "")[:1000]}\n'
                '<font color="grey">直接在卡片内选择或填写，提交后任务会自动继续。</font>'
            ).strip(),
        },
    ]
    submittable = ask.get('submittable') is not False
    if submittable and form is not None:
        elements.append(form)
        return elements
    question_text = '\n'.join(
        f'{index}. {str(question.get("text") or "")[:MAX_ASK_QUESTION_CHARS]}'
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


def _execution_elements(
    presentations: list[dict[str, Any]],
    provider_context: dict[str, Any],
) -> list[dict[str, Any]]:
    execution = next(
        (
            presentation
            for presentation in presentations
            if presentation.get('kind') == 'execution'
        ),
        None,
    )
    if execution is None:
        return []
    language = _reply_language(provider_context)
    provider = str(execution.get('provider') or '').capitalize()
    status = str(execution.get('status') or '')
    status_label = {
        'pending': 'Waiting' if language == 'en' else '等待执行',
        'running': 'Running' if language == 'en' else '执行中',
        'completed': 'Completed' if language == 'en' else '执行完成',
        'failed': 'Failed' if language == 'en' else '执行失败',
        'stopped': 'Stopped' if language == 'en' else '已停止',
    }.get(status, status)
    lines = [f'**{provider} · {status_label}**']
    invocation_count = optional_int(execution.get('invocation_count')) or 0
    tools = [
        str(tool)
        for tool in (
            execution.get('tools')
            if isinstance(execution.get('tools'), list)
            else []
        )
        if str(tool)
    ]
    if invocation_count or tools:
        call_label = (
            f'{invocation_count} LazyMind call(s)'
            if language == 'en'
            else f'{invocation_count} 次 LazyMind 调用'
        )
        lines.append(f'{call_label}' + (f' · {", ".join(tools)}' if tools else ''))
    recovery_count = optional_int(execution.get('recovery_count')) or 0
    if recovery_count:
        lines.append(
            f'Recovered {recovery_count} time(s)'
            if language == 'en'
            else f'已恢复 {recovery_count} 次'
        )
    workflows = execution.get('workflows')
    if isinstance(workflows, list) and workflows:
        lines.append(f'Workflow · {", ".join(str(item) for item in workflows)}')
    artifact_count = optional_int(execution.get('artifact_count')) or 0
    revision_count = optional_int(execution.get('artifact_revision_count')) or 0
    if revision_count:
        lines.append(
            f'{artifact_count} artifact(s) · {revision_count} revision(s)'
            if language == 'en'
            else f'{artifact_count} 个产物 · {revision_count} 个版本'
        )
    host_id = str(execution.get('host_id') or '')
    if host_id:
        online = execution.get('host_online') is True
        lines.append(
            f'Host · {host_id} · {"online" if online else "offline"}'
            if language == 'en'
            else f'执行端 · {host_id} · {"在线" if online else "已离线"}'
        )
    error = presentable_feishu_text(
        str(execution.get('error_message') or '')
    ).strip()
    if error:
        lines.append(f'<font color="red">{error[:500]}</font>')
    return [
        {'tag': 'hr'},
        {'tag': 'markdown', 'content': '\n'.join(lines)[:3500]},
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


def task_progress_text(task: dict[str, Any] | None) -> str:
    """Compact live projection of the task state reported by Core."""
    if task is None:
        return ''
    status = str(task.get('status') or 'pending')
    status_label, _template = _task_status(status)
    title = presentable_feishu_text(
        str(task.get('title') or '后台任务')
    )[:200]
    lines = [f'**后台任务：{title}**', f'状态：{status_label}']
    progress = _optional_percent(
        task.get('progress_pct', task.get('progress'))
    )
    phase = presentable_feishu_text(
        str(task.get('current_phase') or '')
    )
    detail = []
    if progress is not None:
        detail.append(f'{progress}%')
    if phase and phase not in {'执行中...', '执行中…'}:
        detail.append(phase[:200])
    if detail:
        lines.append(f'<font color="grey">当前：{" · ".join(detail)}</font>')
    return '\n'.join(lines)[:3500]


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
        '\n[tool:',
    ):
        summary = summary.split(marker, 1)[0]
    if len(summary) > 800:
        return f'{summary[:800].rstrip()}…'
    return summary

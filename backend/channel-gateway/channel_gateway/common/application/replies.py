import json
from dataclasses import dataclass
from typing import Any
from urllib.parse import urlsplit

from channel_gateway.common.application.conversations import ConversationResult
from channel_gateway.common.domain.chat import CoreEvent
from channel_gateway.common.domain.commands import (
    ActionKind,
    CommandEnvelope,
    command_kind,
)
from channel_gateway.common.domain.outbound import (
    AskPresentation,
    AskQuestionPresentation,
    ExecutionPresentation,
    ReplyPresentation,
    SelectionOption,
    SelectionPresentation,
    TaskPresentation,
    optional_int,
)
from channel_gateway.common.ports.repository import NavigationRepository


_MAX_DURABLE_ARTIFACTS = 20
_MAX_INLINE_ARTIFACT_BYTES = 2 * 1024 * 1024
_MAX_DURABLE_SOURCES = 20


def project_core_presentations(
    events: tuple[CoreEvent, ...],
) -> tuple[ReplyPresentation, ...]:
    presentations: list[ReplyPresentation] = []
    for event in events:
        if event.type == 'ask_pending':
            presentation = _ask(event.payload)
        elif event.type == 'task_created':
            presentation = _task(event.payload)
        elif event.type == 'execution_projection':
            presentation = _execution(event.payload)
        else:
            presentation = None
        if presentation is not None:
            presentations.append(presentation)
    return tuple(presentations)


def _ask(payload: dict) -> AskPresentation | None:
    ask_id = str(payload.get('ask_id') or '')
    raw_questions = payload.get('questions')
    raw_questions = raw_questions if isinstance(raw_questions, list) else []
    submittable = bool(ask_id and len(ask_id) <= 512)
    if len(raw_questions) > 10:
        submittable = False
    bounded_questions: list[AskQuestionPresentation] = []
    for question in raw_questions[:10]:
        if not isinstance(question, dict):
            submittable = False
            continue
        text = str(question.get('text') or '')
        question_type = str(question.get('type') or 'text')
        raw_choices = (
            question.get('choices')
            if isinstance(question.get('choices'), list)
            else []
        )
        if (
            not text
            or len(text) > 1000
            or question_type not in {'text', 'single', 'multiple', 'boolean'}
            or len(raw_choices) > 20
            or any(len(str(choice)) > 200 for choice in raw_choices)
        ):
            submittable = False
        if not text:
            continue
        bounded_questions.append(AskQuestionPresentation(
            text=text[:1000],
            type=(
                question_type
                if question_type in {'text', 'single', 'multiple', 'boolean'}
                else 'text'
            ),
            choices=tuple(
                str(choice)[:200]
                for choice in raw_choices[:20]
                if str(choice)
            ),
        ))
    questions = tuple(
        bounded_questions
    )
    if not ask_id or not questions:
        return None
    return AskPresentation(
        kind='ask',
        ask_id=ask_id[:512],
        title=str(payload.get('title') or '')[:200],
        description=str(payload.get('description') or '')[:1000],
        questions=questions,
        submittable=submittable,
    )


def _task(payload: dict) -> TaskPresentation | None:
    task_id = str(payload.get('task_id') or '')
    if not task_id or len(task_id) > 512:
        return None
    return TaskPresentation(
        kind='task',
        task_id=task_id,
        conversation_id=str(payload.get('conversation_id') or '')[:512],
        title=str(payload.get('title') or '后台任务')[:200],
        mode=str(payload.get('mode') or '')[:64],
        status=str(payload.get('status') or '已创建')[:64],
        agent_type=str(payload.get('agent_type') or '')[:64],
        progress=optional_int(
            payload.get('progress', payload.get('progress_pct'))
        ),
        current_phase=str(payload.get('current_phase') or '')[:200],
        estimated_sec=optional_int(payload.get('estimated_sec')),
        summary=str(payload.get('summary') or '')[:1000],
    )


def _execution(payload: dict) -> ExecutionPresentation | None:
    provider = str(payload.get('provider') or '').strip().lower()
    status = str(payload.get('status') or '').strip().lower()
    if (
        not provider
        or len(provider) > 32
        or status not in {'pending', 'running', 'completed', 'failed', 'stopped'}
    ):
        return None
    invocation = payload.get('invocation')
    invocation = invocation if isinstance(invocation, dict) else {}
    raw_workflows = payload.get('workflows')
    workflows: list[str] = []
    for workflow in raw_workflows if isinstance(raw_workflows, list) else []:
        if not isinstance(workflow, dict):
            continue
        workflow_id = str(workflow.get('workflow_id') or '')[:64]
        workflow_status = str(workflow.get('status') or '')[:32]
        if workflow_id:
            workflows.append(
                f'{workflow_id} · {workflow_status}'
                if workflow_status
                else workflow_id
            )
    return ExecutionPresentation(
        kind='execution',
        provider=provider,
        status=status,
        host_id=str(payload.get('host_id') or '')[:128],
        host_online=payload.get('host_online') is True,
        recovery_count=max(0, optional_int(payload.get('recovery_count')) or 0),
        invocation_count=max(0, optional_int(invocation.get('total')) or 0),
        tools=tuple(
            str(tool)[:128]
            for tool in (
                invocation.get('tools')
                if isinstance(invocation.get('tools'), list)
                else []
            )[:20]
            if str(tool)
        ),
        workflows=tuple(workflows[:10]),
        artifact_count=max(0, optional_int(payload.get('artifact_count')) or 0),
        artifact_revision_count=max(
            0,
            optional_int(payload.get('artifact_revision_count')) or 0,
        ),
        error_message=str(payload.get('error_message') or '')[:500],
    )


def _durable_core_events(
    events: tuple[CoreEvent, ...],
) -> tuple[dict[str, Any], ...]:
    durable: list[dict[str, Any]] = []
    for event in events:
        if event.type != 'artifact_created' or len(durable) >= _MAX_DURABLE_ARTIFACTS:
            continue
        payload = event.payload
        content_type = str(payload.get('content_type') or '').lower()
        value = payload.get('value')
        if isinstance(value, str):
            try:
                value = json.loads(value)
            except json.JSONDecodeError:
                continue
        if not isinstance(value, dict):
            continue
        bounded_value: dict[str, Any]
        if content_type == 'text' and isinstance(value.get('text'), str):
            text = value['text']
            if len(text.encode('utf-8')) > _MAX_INLINE_ARTIFACT_BYTES:
                continue
            bounded_value = {'text': text}
        elif content_type == 'json' and 'data' in value:
            try:
                encoded = json.dumps(
                    value['data'],
                    ensure_ascii=False,
                    separators=(',', ':'),
                ).encode('utf-8')
            except (TypeError, ValueError):
                continue
            if len(encoded) > _MAX_INLINE_ARTIFACT_BYTES:
                continue
            bounded_value = {'data': value['data']}
        elif content_type == 'file_list':
            paths = value.get('paths')
            bounded_value = {
                'paths': [
                    path
                    for raw in (paths if isinstance(paths, list) else [])[:20]
                    if len(path := str(raw or '')) <= 2048
                    and urlsplit(path).path.startswith('/static-files/')
                ]
            }
            if not bounded_value['paths']:
                continue
        elif content_type in {'image', 'file'} or content_type.startswith('image/'):
            source = str(value.get('url') or '')
            if (
                not source
                or len(source) > 2048
                or not urlsplit(source).path.startswith('/static-files/')
            ):
                continue
            bounded_value = {'url': source}
        else:
            continue
        durable.append({
            'type': 'artifact_created',
            'payload': {
                'content_type': content_type,
                'filename': str(payload.get('filename') or '')[:255],
                'value': bounded_value,
            },
        })
    return tuple(durable)


def _durable_sources(values: tuple[Any, ...]) -> tuple[dict[str, str], ...]:
    result: list[dict[str, str]] = []
    for value in values:
        if not isinstance(value, dict):
            continue
        url = str(value.get('url') or value.get('link') or '').strip()
        if (
            not url
            or len(url) > 2048
            or urlsplit(url).scheme not in {'http', 'https'}
        ):
            continue
        result.append({
            'url': url,
            'title': str(
                value.get('title') or value.get('name') or '参考来源'
            ).strip()[:200],
        })
        if len(result) >= _MAX_DURABLE_SOURCES:
            break
    return tuple(result)


@dataclass(frozen=True)
class ChannelReply:
    intent_kind: ActionKind
    text: str
    core_events: tuple[dict[str, Any], ...] = ()
    sources: tuple[Any, ...] = ()
    presentations: tuple[ReplyPresentation, ...] = ()


class ChannelReplyBuilder:
    """Converts action results into the provider-neutral reply model."""

    def __init__(self, store: NavigationRepository):
        self._store = store

    def build(
        self,
        *,
        command: CommandEnvelope,
        result: str | ConversationResult,
        account_id: str,
        external_address_hash: str,
        extra_presentations: tuple[ReplyPresentation, ...] = (),
    ) -> ChannelReply:
        selection = self._selection(
            account_id,
            external_address_hash,
        )
        if isinstance(result, ConversationResult):
            core_presentations = project_core_presentations(
                result.turn.events
                if result.turn is not None
                else ()
            )
            presentations = (
                *extra_presentations,
                *result.presentations,
                *core_presentations,
            )
            if selection is not None:
                presentations = (*presentations, selection)
            return ChannelReply(
                intent_kind=command_kind(command),
                text=result.text,
                core_events=_durable_core_events(
                    result.turn.events
                    if result.turn is not None
                    else ()
                ),
                sources=_durable_sources(
                    result.turn.sources
                    if result.turn is not None
                    else ()
                ),
                presentations=presentations,
            )
        intent_kind = command_kind(command)
        presentations = extra_presentations
        if selection is not None:
            presentations = (*presentations, selection)
        return ChannelReply(
            intent_kind=intent_kind,
            text=result,
            presentations=presentations,
        )

    def _selection(
        self,
        account_id: str,
        external_address_hash: str,
    ) -> SelectionPresentation | None:
        selection = self._store.get_selection_context(
            account_id,
            external_address_hash,
        )
        if not selection:
            return None
        raw_items = selection.get('items')
        if not isinstance(raw_items, list):
            return None
        options = tuple(
            SelectionOption(
                label=self._selection_label(item, index),
                value=str(index),
            )
            for index, item in enumerate(raw_items, start=1)
            if isinstance(item, dict)
        )
        if not options:
            return None
        labels = {
            'conversation': '选择要继续的会话',
            'knowledge_base': '选择知识库',
            'skill': '选择 Skill',
            'tool': '选择工具',
            'personalization': '选择个人习惯',
        }
        kind = str(selection.get('kind') or '')
        return SelectionPresentation(
            kind='selection',
            selection_id=str(selection.get('id') or ''),
            title=labels.get(kind, '请选择'),
            options=options,
        )

    @staticmethod
    def _selection_label(item: dict[str, Any], index: int) -> str:
        return str(
            item.get('display_name')
            or item.get('name')
            or f'选项 {index}'
        )

from __future__ import annotations

import json
import re
from dataclasses import dataclass
from typing import Any, Iterable, Literal, TypeAlias
from urllib.parse import urlsplit

from channel_gateway.common.domain.channel import ClaimedOutbound


_MARKDOWN_IMAGE = re.compile(r'!\[([^\]]*)\]\(([^)\s]+)\)')


@dataclass(frozen=True, slots=True)
class SelectionOption:
    label: str
    value: str

    def to_dict(self) -> dict[str, str]:
        return {'label': self.label, 'value': self.value}


@dataclass(frozen=True, slots=True)
class SelectionPresentation:
    kind: Literal['selection']
    selection_id: str
    title: str
    options: tuple[SelectionOption, ...]

    def to_dict(self) -> dict[str, Any]:
        return {
            'kind': self.kind,
            'selection_id': self.selection_id,
            'title': self.title,
            'options': [option.to_dict() for option in self.options],
        }


@dataclass(frozen=True, slots=True)
class AskQuestionPresentation:
    text: str
    type: str
    choices: tuple[str, ...]

    def to_dict(self) -> dict[str, Any]:
        return {
            'text': self.text,
            'type': self.type,
            'choices': list(self.choices),
        }


@dataclass(frozen=True, slots=True)
class AskPresentation:
    kind: Literal['ask']
    ask_id: str
    title: str
    description: str
    questions: tuple[AskQuestionPresentation, ...]

    def to_dict(self) -> dict[str, Any]:
        return {
            'kind': self.kind,
            'ask_id': self.ask_id,
            'title': self.title,
            'description': self.description,
            'questions': [
                question.to_dict()
                for question in self.questions
            ],
        }


@dataclass(frozen=True, slots=True)
class TaskPresentation:
    kind: Literal['task']
    task_id: str
    title: str
    mode: str
    status: str
    agent_type: str = ''
    progress: int | None = None
    current_phase: str = ''
    estimated_sec: int | None = None
    summary: str = ''

    def to_dict(self) -> dict[str, Any]:
        return {
            'kind': self.kind,
            'task_id': self.task_id,
            'title': self.title,
            'mode': self.mode,
            'status': self.status,
            'agent_type': self.agent_type,
            'progress': self.progress,
            'current_phase': self.current_phase,
            'estimated_sec': self.estimated_sec,
            'summary': self.summary,
        }


ReplyPresentation: TypeAlias = (
    SelectionPresentation
    | AskPresentation
    | TaskPresentation
)


def presentation_from_dict(
    value: dict[str, Any],
) -> ReplyPresentation | None:
    kind = str(value.get('kind') or '')
    if kind == 'selection':
        options = tuple(
            SelectionOption(
                label=str(item.get('label') or ''),
                value=str(item.get('value') or ''),
            )
            for item in _dict_items(value.get('options'))
            if item.get('label') and item.get('value')
        )
        if not options:
            return None
        return SelectionPresentation(
            kind='selection',
            selection_id=str(value.get('selection_id') or ''),
            title=str(value.get('title') or '请选择'),
            options=options,
        )
    if kind == 'ask':
        questions = tuple(
            AskQuestionPresentation(
                text=str(item.get('text') or ''),
                type=str(item.get('type') or 'text'),
                choices=tuple(
                    str(choice)
                    for choice in (
                        item.get('choices')
                        if isinstance(item.get('choices'), list)
                        else []
                    )
                    if str(choice)
                ),
            )
            for item in _dict_items(value.get('questions'))
            if item.get('text')
        )
        ask_id = str(value.get('ask_id') or '')
        if not ask_id or not questions:
            return None
        return AskPresentation(
            kind='ask',
            ask_id=ask_id,
            title=str(value.get('title') or ''),
            description=str(value.get('description') or ''),
            questions=questions,
        )
    if kind == 'task':
        task_id = str(value.get('task_id') or '')
        if not task_id:
            return None
        return TaskPresentation(
            kind='task',
            task_id=task_id,
            title=str(value.get('title') or '后台任务'),
            mode=str(value.get('mode') or ''),
            status=str(value.get('status') or '已创建'),
            agent_type=str(value.get('agent_type') or ''),
            progress=optional_int(value.get('progress')),
            current_phase=str(value.get('current_phase') or ''),
            estimated_sec=optional_int(value.get('estimated_sec')),
            summary=str(value.get('summary') or ''),
        )
    return None


def _dict_items(value: Any) -> list[dict[str, Any]]:
    if not isinstance(value, list):
        return []
    return [
        dict(item)
        for item in value
        if isinstance(item, dict)
    ]


def optional_int(value: Any) -> int | None:
    if isinstance(value, bool):
        return None
    try:
        return int(value) if value is not None else None
    except (TypeError, ValueError):
        return None


@dataclass(frozen=True, slots=True)
class OutboundPart:
    kind: Literal['text', 'image']
    text: str = ''
    source: str = ''


def split_outbound_parts(reply: str) -> list[OutboundPart]:
    """Split LazyMind's Markdown image contract into ordered channel parts."""
    parts: list[OutboundPart] = []
    cursor = 0
    for match in _MARKDOWN_IMAGE.finditer(reply):
        source = match.group(2).strip()
        if not _is_core_static_file(source):
            continue
        _append_text(parts, reply[cursor:match.start()])
        parts.append(
            OutboundPart(
                kind='image',
                text=match.group(1).strip(),
                source=source,
            )
        )
        cursor = match.end()
    _append_text(parts, reply[cursor:])
    return parts


def _append_text(parts: list[OutboundPart], value: str) -> None:
    text = value.strip()
    if text:
        parts.append(OutboundPart(kind='text', text=text))


def _is_core_static_file(source: str) -> bool:
    parsed = urlsplit(source)
    return parsed.path.startswith('/static-files/')


class OutboundRenderer:
    """Projects Core text and artifacts into provider-neutral ordered parts."""

    def __init__(self, text_chunk_size: int):
        self._text_chunk_size = text_chunk_size

    def render(self, message: ClaimedOutbound) -> list[dict[str, str]]:
        rendered: list[dict[str, str]] = []
        image_sources: set[str] = set()
        for part in split_outbound_parts(message.text):
            if part.kind == 'image':
                image_sources.add(part.source)
                rendered.append(
                    {
                        'kind': 'image',
                        'source': part.source,
                        'alt': part.text,
                    }
                )
                continue
            rendered.extend(
                {'kind': 'text', 'text': chunk}
                for chunk in self._split_text(part.text)
            )
        for artifact in self._artifacts(message.metadata):
            source = str(artifact.get('source') or '')
            kind = str(artifact.get('kind') or '')
            if kind == 'image':
                if source in image_sources:
                    continue
                image_sources.add(source)
                rendered.append(
                    {
                        'kind': 'image',
                        'source': source,
                        'alt': str(artifact.get('filename') or ''),
                    }
                )
            elif kind == 'file':
                rendered_part = {
                    'kind': 'file',
                    'filename': str(
                        artifact.get('filename')
                        or 'lazymind-output'
                    ),
                }
                artifact_index = str(
                    artifact.get('artifact_index') or ''
                )
                if source:
                    rendered_part['source'] = source
                if artifact_index:
                    rendered_part['artifact_index'] = artifact_index
                rendered.append(rendered_part)
        source_text = self._source_text(message.metadata)
        if source_text:
            rendered.extend(
                {'kind': 'text', 'text': chunk}
                for chunk in self._split_text(source_text)
            )
        return rendered

    def _split_text(self, text: str) -> Iterable[str]:
        remaining = text.strip()
        while remaining:
            if len(remaining) <= self._text_chunk_size:
                yield remaining
                return
            cut = remaining.rfind(
                '\n',
                0,
                self._text_chunk_size + 1,
            )
            if cut < self._text_chunk_size // 2:
                cut = remaining.rfind(
                    '。',
                    0,
                    self._text_chunk_size + 1,
                )
                if cut >= self._text_chunk_size // 2:
                    cut += 1
            if cut < self._text_chunk_size // 2:
                cut = self._text_chunk_size
            yield remaining[:cut].strip()
            remaining = remaining[cut:].strip()

    @staticmethod
    def _artifacts(metadata: dict) -> list[dict[str, str]]:
        result: list[dict[str, str]] = []
        events = metadata.get('core_events')
        for event_index, event in enumerate(
            events if isinstance(events, list) else []
        ):
            if not isinstance(event, dict):
                continue
            if event.get('type') != 'artifact_created':
                continue
            payload = event.get('payload')
            if not isinstance(payload, dict):
                continue
            value = payload.get('value')
            if isinstance(value, str):
                try:
                    value = json.loads(value)
                except json.JSONDecodeError:
                    value = {}
            if not isinstance(value, dict):
                continue
            content_type = str(
                payload.get('content_type') or ''
            ).lower()
            if content_type in ('text', 'json'):
                extension = (
                    'txt' if content_type == 'text' else 'json'
                )
                result.append(
                    {
                        'kind': 'file',
                        'artifact_index': str(event_index),
                        'filename': str(
                            payload.get('filename')
                            or f'lazymind-output.{extension}'
                        ),
                    }
                )
                continue
            if content_type == 'file_list':
                paths = value.get('paths')
                for source in paths if isinstance(paths, list) else []:
                    source = str(source or '')
                    if not _is_core_static_file(source):
                        continue
                    result.append(
                        {
                            'kind': 'file',
                            'source': source,
                            'filename': (
                                urlsplit(source).path.rsplit('/', 1)[-1]
                                or 'lazymind-output'
                            ),
                        }
                    )
                continue
            source = str(value.get('url') or '')
            if not _is_core_static_file(source):
                continue
            kind = (
                'image'
                if content_type == 'image'
                or content_type.startswith('image/')
                else 'file'
                if content_type == 'file'
                else ''
            )
            if kind:
                result.append(
                    {
                        'kind': kind,
                        'source': source,
                        'filename': str(
                            payload.get('filename') or ''
                        ),
                    }
                )
        return result

    @staticmethod
    def _source_text(metadata: dict) -> str:
        lines: list[str] = []
        sources = metadata.get('sources')
        for source in sources if isinstance(sources, list) else []:
            if not isinstance(source, dict):
                continue
            url = str(
                source.get('url') or source.get('link') or ''
            ).strip()
            if not url:
                continue
            title = str(
                source.get('title')
                or source.get('name')
                or '参考来源'
            ).strip()
            lines.append(f'- {title}: {url}')
        return '参考来源：\n' + '\n'.join(lines) if lines else ''


def inline_artifact_bytes(
    metadata: dict,
    artifact_index: str,
) -> bytes | None:
    """Resolve a persisted text/JSON artifact without duplicating it in outbox parts."""
    try:
        index = int(artifact_index)
    except (TypeError, ValueError):
        return None
    events = metadata.get('core_events')
    if not isinstance(events, list) or index < 0 or index >= len(events):
        return None
    event = events[index]
    if not isinstance(event, dict) or event.get('type') != 'artifact_created':
        return None
    payload = event.get('payload')
    if not isinstance(payload, dict):
        return None
    value = payload.get('value')
    if isinstance(value, str):
        try:
            value = json.loads(value)
        except json.JSONDecodeError:
            return None
    if not isinstance(value, dict):
        return None
    content_type = str(payload.get('content_type') or '').lower()
    if content_type == 'text':
        text = value.get('text')
        return str(text).encode('utf-8') if isinstance(text, str) else None
    if content_type == 'json' and 'data' in value:
        return json.dumps(
            value['data'],
            ensure_ascii=False,
            indent=2,
        ).encode('utf-8')
    return None

"""MD/LMD artifacts, envelopes, paths, conversion, and rendering."""

from __future__ import annotations
import json
import os
import re
import tempfile
import uuid
from pathlib import Path
from typing import Any, Mapping
from lazyllm.tools.agent import ToolExecutionError
from lazyllm.tools.writer.data_models import (
    SectionInstruction,
    WriterDocument,
)
from lazyllm.tools.writer.numbering import (
    apply_numbering_update_ir,
    apply_numbering_update_markdown,
    build_numbering_view_from_ir,
    build_numbering_view_from_markdown,
    compute_numbering,
    dematerialize_ir,
    dematerialize_markdown,
    ensure_markdown_heading_anchors,
    format_target_number,
    materialize_markdown,
)
from lazyllm.tools.writer.utils import (
    parse_document_markdown,
    save_artifact_json,
    set_document_editable,
    writer_document_from_lmd,
    writer_document_to_lmd,
    writer_document_to_markdown,
)

WRITER_DATA_MODEL_SCHEMA_PREFIX = 'lazyllm.tools.writer.data_models'
WRITER_IR_SCHEMA = f'{WRITER_DATA_MODEL_SCHEMA_PREFIX}.writer_ir.WriterDocument'
WRITER_BLOCK_SCHEMA = f'{WRITER_DATA_MODEL_SCHEMA_PREFIX}.writer_ir.WriterBlock'
_MARKDOWN_ATX_HEADING_RE = re.compile(
    r'^(?P<indent> {0,3})(?P<marks>#{1,6})[ \t]+(?P<title>.*?)(?:[ \t]+#+)?[ \t]*$'
)
_MARKDOWN_FENCE_RE = re.compile(r'^ {0,3}(?P<marks>`{3,}|~{3,})')
_MARKDOWN_DOCUMENT_RE = re.compile(
    r'(?m)^(?: {0,3}#{1,6}\s| {0,3}(?:[-*+] |\d+[.)] )| {0,3}> | {0,3}(?:```|~~~))'
    r'|\[[^\]]+\]\([^)]+\)'
)
_MARKDOWN_DRAFT_ROOT_ERROR = (
    'Markdown draft section must contain exactly one H2 root heading.'
)


def writer_schema(name: str) -> str:
    return f'{WRITER_DATA_MODEL_SCHEMA_PREFIX}.{name}'


def inspect_document(value: Any, schema: str = '') -> dict[str, Any]:
    """Identify Markdown or Writer IR without rendering or changing it."""
    declared_schema = str(schema or '').strip()
    display_source = None
    if isinstance(value, Mapping) and 'data' in value:
        meta = value.get('meta')
        display_source = meta.get('writer_source') if isinstance(meta, Mapping) else None
        declared_schema = declared_schema or str(value.get('schema_name') or '')
        value = value['data']

    markdown_declared = declared_schema in {'markdown', 'text/markdown'}
    writer_declared = declared_schema in {
        WRITER_IR_SCHEMA,
        'application/vnd.lazymind.writer+json',
    }

    if isinstance(value, str):
        if writer_declared:
            raise ValueError('Artifact does not match the Writer IR schema.')
        is_markdown = markdown_declared or bool(_MARKDOWN_DOCUMENT_RE.search(value))
        if not is_markdown:
            return _non_document_inspection()
        result = _document_inspection('markdown', 'text/markdown', False)
        if display_source:
            from .source_display import project_source_display
            context = project_source_display(value, display_source)
            if context:
                result['render_context'] = context
        return result

    if not isinstance(value, Mapping):
        return _non_document_inspection()
    try:
        document = WriterDocument.model_validate(value)
    except (TypeError, ValueError) as exc:
        if writer_declared:
            raise ValueError('Artifact does not match the Writer IR schema.') from exc
        return _non_document_inspection()
    return _document_inspection(
        'ir',
        'application/vnd.lazymind.writer+json',
        bool(document.provider_binding),
    )


def _document_inspection(
    representation: str,
    schema: str,
    provider_binding: bool,
) -> dict[str, Any]:
    return {
        'is_document': True,
        'representation': representation,
        'schema': schema,
        'features': {
            'headings': True,
            'numbering': True,
            'cross_references': True,
            'provider_binding': provider_binding,
        },
    }


def _non_document_inspection() -> dict[str, Any]:
    return {
        'is_document': False,
        'representation': None,
        'schema': None,
        'features': {
            'headings': False,
            'numbering': False,
            'cross_references': False,
            'provider_binding': False,
        },
    }


def persist_artifact_json(
    data: Any,
    path: str,
    *,
    schema_name: str,
    created_by: str,
    extra_meta: dict[str, Any] | None = None,
) -> str:
    """Persist an LMD/JSON envelope through the shared Writer artifact format."""
    return save_artifact_json(
        data,
        path,
        schema_name=schema_name,
        created_by=created_by,
        extra_meta=extra_meta,
    )


def normalize_writer_document(
    value: str | dict[str, Any],
    *,
    expected_stage: str | None = None,
    editable: bool = False,
) -> str:
    """Normalize Writer IR while preserving Markdown as plain text."""
    if isinstance(value, str):
        try:
            payload = _json_loads(value, {})
        except json.JSONDecodeError:
            return value
    else:
        payload = dict(value or {})
    if isinstance(payload, str):
        return payload
    document = WriterDocument.model_validate(payload)
    if expected_stage is not None and document.stage != expected_stage:
        raise ValueError(
            f'WriterDocument must have stage={expected_stage!r}; '
            f'got {document.stage!r}.'
        )
    if document.metadata.get('kind') == 'step_status':
        raise ValueError('A writer status placeholder cannot be used as a document artifact.')
    if expected_stage == 'outline' and len(document.blocks) < 3:
        raise ValueError(
            'An outline WriterDocument must contain at least three top-level blocks.'
        )
    if editable:
        document.ui_editable = True
    return document.model_dump_json(exclude_defaults=True)


def detach_provider_binding(value: Any) -> dict[str, Any]:
    """Turn imported Writer IR into an independent local document."""
    document = WriterDocument.model_validate(value)
    document.revision = None
    document.provider_binding.clear()
    for key in ('source', 'provider_metadata', 'block_count', 'source_block_count'):
        document.metadata.pop(key, None)
    for block in document.iter_blocks():
        block.provider_binding.clear()
        block.provider_payload.clear()
    return document.model_dump(exclude_defaults=True)


def markdown_filename(title: str) -> str:
    """Return a safe Markdown download filename for a document title."""
    filename = re.sub(r'[\\/:*?"<>|\x00-\x1f]+', '_', title).strip(' ._')
    return f"{filename[:80] or '文稿'}.md"


def _json_dumps(value: Any) -> str:
    return json.dumps(value, ensure_ascii=False, indent=2)


def _json_loads(value: str, default: Any = None) -> Any:
    text = (value or '').strip()
    if not text:
        return default
    parsed = json.loads(text)
    if isinstance(parsed, dict) and 'data' in parsed:
        return parsed['data']
    return parsed


def _read_artifact_data(path: str) -> Any:
    if Path(path).suffix.lower() in {'.md', '.markdown'}:
        return Path(path).read_text(encoding='utf-8')
    with open(path, 'r', encoding='utf-8') as fh:
        raw = json.load(fh)
    if isinstance(raw, dict) and 'data' in raw:
        return raw['data']
    return raw


def _temp_root() -> Path:
    from lazymind.chat.engine.tools.workspace_context import (
        get_tool_resolution_context,
    )

    request = get_tool_resolution_context()
    base = request.managed_roots[0] if request and request.managed_roots else None
    parent = (
        Path(base).resolve() / '.writer-tools'
        if base
        else Path(tempfile.gettempdir()) / 'lazymind-writer-tools'
    )
    root = parent / uuid.uuid4().hex
    if base and os.path.commonpath([str(Path(base).resolve()), str(root.resolve())]) != str(
        Path(base).resolve()
    ):
        raise ToolExecutionError('Writer workspace contains an escaping directory link.')
    os.makedirs(str(root), exist_ok=True)
    return root


def _write_input_artifact(
    root: Path, filename: str, data: Any, schema_name: str
) -> str:
    return save_artifact_json(
        data,
        str(root / filename),
        schema_name=schema_name,
        created_by='WriterToolkit',
    )


def _document_value(value: str) -> Any:
    try:
        return _json_loads(value, {})
    except json.JSONDecodeError:
        return value


def _write_document_input(root: Path, name: str, value: str) -> str:
    content = _document_value(value)
    if isinstance(content, str):
        path = root / f'{name}.md'
        path.write_text(content, encoding='utf-8')
        return str(path)
    return _write_input_artifact(root, f'{name}.lmd', content, WRITER_IR_SCHEMA)


def _inline_draft_sections(root: Path, value: Any) -> Any:
    """Materialize inline Markdown without interpreting path-like text as a path."""
    if isinstance(value, str):
        return _write_document_input(root, uuid.uuid4().hex, _json_dumps(value))
    if isinstance(value, list):
        return [_inline_draft_sections(root, item) for item in value]
    return value


def _primary_data(result: dict) -> Any:
    artifact_path = result.get('artifact_path')
    if not artifact_path:
        raise ToolExecutionError('Writer tool did not return its primary artifact.')
    return _read_artifact_data(artifact_path)


def _markdown_heading_key(value: str) -> str:
    text = re.sub(r'^\s*\d+(?:\.\d+)*[、.．\s　]+', '', str(value or ''))
    return re.sub(r'[\s\W_]+', '', text, flags=re.UNICODE).lower()


def _markdown_heading_rows(lines: list[str]) -> list[tuple[int, int, str]]:
    rows: list[tuple[int, int, str]] = []
    fence_mark = ''
    for index, line in enumerate(lines):
        fence = _MARKDOWN_FENCE_RE.match(line)
        if fence:
            mark = fence.group('marks')
            if not fence_mark:
                fence_mark = mark
            elif mark[0] == fence_mark[0] and len(mark) >= len(fence_mark):
                fence_mark = ''
            continue
        if fence_mark:
            continue
        heading = _MARKDOWN_ATX_HEADING_RE.match(line)
        if heading:
            rows.append(
                (index, len(heading.group('marks')), heading.group('title').strip())
            )
    return rows


def _normalize_streamed_markdown_section(
    markdown: str,
    instruction: SectionInstruction,
) -> str:
    """Repair relative Markdown headings without spending another model call."""
    title = instruction.section_title.strip()
    if not title:
        raise ValueError('Markdown draft section title must not be empty.')
    lines = (
        str(markdown or '')
        .replace('\r\n', '\n')
        .replace('\r', '\n')
        .strip()
        .split('\n')
    )
    rows = _markdown_heading_rows(lines)
    title_key = _markdown_heading_key(title)
    root_index = next(
        (
            index
            for index, _, heading_title in rows
            if _markdown_heading_key(heading_title) == title_key
        ),
        -1,
    )

    removed: set[int] = set()
    if root_index >= 0:
        lines[root_index] = f'## {title}'
        removed.update(
            index
            for index, _, heading_title in rows
            if index != root_index and _markdown_heading_key(heading_title) == title_key
        )
    else:
        lines = [f'## {title}', '', *lines]
        root_index = 0
        rows = [
            (index + 2, level, heading_title) for index, level, heading_title in rows
        ]

    child_rows = [
        (index, level, heading_title)
        for index, level, heading_title in rows
        if index != root_index and index not in removed
    ]
    if child_rows:
        shift = 3 - min(level for _, level, _ in child_rows)
        for index, level, heading_title in child_rows:
            lines[index] = f"{'#' * min(6, max(3, level + shift))} {heading_title}"

    result = '\n'.join(
        line for index, line in enumerate(lines) if index not in removed
    ).strip()
    top_rows = [
        row for row in _markdown_heading_rows(result.splitlines()) if row[1] <= 2
    ]
    if len(top_rows) != 1 or top_rows[0][1] != 2:
        raise ValueError(_MARKDOWN_DRAFT_ROOT_ERROR)
    if _markdown_heading_key(top_rows[0][2]) != title_key:
        raise ValueError(
            'Markdown draft section heading does not match its content_ref.'
        )
    return result


def _result_data(result: dict, key: str) -> Any:
    path = ((result.get('metadata') or {}).get('artifact_paths') or {}).get(key)
    if not path:
        raise ToolExecutionError(f'Writer tool did not return artifact {key!r}.')
    return _read_artifact_data(path)


def _set_document_editable(value: Any, *, stage: str | None = None) -> WriterDocument:
    return set_document_editable(value, stage=stage)


def _document_text(document: WriterDocument) -> str:
    return '\n'.join(block.content for block in document.iter_blocks() if block.content)


def markdown_to_writer_document(
    markdown: str,
    *,
    document_id: str,
    stage: str = 'final',
) -> WriterDocument:
    """Convert the established Markdown subset to Writer IR."""
    return parse_document_markdown(
        markdown,
        document_id=document_id,
        stage=stage,
    )


def markdown_to_lmd(
    markdown: str,
    *,
    document_id: str,
    stage: str = 'final',
) -> str:
    """Convert Markdown to a serialized LMD Artifact envelope."""
    return writer_document_to_lmd(
        markdown_to_writer_document(
            markdown,
            document_id=document_id,
            stage=stage,
        )
    )


def lmd_to_markdown(value: str) -> str:
    """Convert serialized LMD to Markdown with the existing Writer converter."""
    return writer_document_to_markdown(writer_document_from_lmd(value))


def _numbering_payload(view: Any, numbering: Mapping[str, Any]) -> dict[str, Any]:
    entries: dict[str, dict[str, Any]] = {}
    for node_id, entry in numbering.items():
        payload: dict[str, Any] = {'label': format_target_number(entry)}
        if entry.kind == 'section':
            payload.update(mode=entry.mode, restart=entry.restart)
        entries[node_id] = payload
    return {'ordered_style': view.ordered_style, 'entries': entries}


def render_document(value: Any) -> dict[str, Any]:
    """Render canonical editable content and its generated numbering sidecar."""
    if isinstance(value, str):
        document = ensure_markdown_heading_anchors(value)
        view = build_numbering_view_from_markdown(document)
        numbering = compute_numbering(view)
        title_match = re.search(r'^#\s+(.+)$', document, re.MULTILINE)
        return {
            'title': title_match.group(1).strip() if title_match else '',
            'representation': 'markdown',
            'document': document,
            'export_document': materialize_markdown(document, view, numbering),
            'numbering': _numbering_payload(view, numbering),
        }
    source = WriterDocument.model_validate(value)
    view = build_numbering_view_from_ir(source)
    numbering = compute_numbering(view)
    return {
        'title': source.title,
        'representation': 'ir',
        'document': source.model_dump(exclude_defaults=True),
        'numbering': _numbering_payload(view, numbering),
    }


def save_document(
    value: Any,
    base_value: Any,
    numbering_update: Mapping[str, Any] | None = None,
) -> dict[str, Any]:
    """Normalize an editor value to canonical source plus numbering sidecar."""
    if isinstance(value, str):
        if not isinstance(base_value, str):
            raise ValueError(
                'base artifact representation does not match Markdown edit'
            )
        base_numbering = compute_numbering(
            build_numbering_view_from_markdown(base_value)
        )
        clean = ensure_markdown_heading_anchors(
            dematerialize_markdown(value, base_numbering)
        )
        if numbering_update is not None:
            clean = apply_numbering_update_markdown(clean, numbering_update)
        rendered = render_document(clean)
        return {'source_document': clean, **rendered}

    current = WriterDocument.model_validate(value)
    base = WriterDocument.model_validate(base_value)
    base_numbering = compute_numbering(build_numbering_view_from_ir(base))
    clean = dematerialize_ir(current, base_numbering)
    if numbering_update is not None:
        clean = apply_numbering_update_ir(clean, numbering_update)
    view = build_numbering_view_from_ir(clean)
    numbering = compute_numbering(view)
    document = clean.model_dump(exclude_defaults=True)
    return {
        'source_document': document,
        'title': clean.title,
        'representation': 'ir',
        'document': document,
        'numbering': _numbering_payload(view, numbering),
    }


class WriterArtifactCapabilities:
    WRITER_IR_SCHEMA = WRITER_IR_SCHEMA
    WRITER_BLOCK_SCHEMA = WRITER_BLOCK_SCHEMA

    def render_markdown(self, writer_document_json: str) -> str:
        """Return the current document title and Markdown content."""
        value = _document_value(writer_document_json)
        if isinstance(value, str):
            title_match = re.search(r'^#\s+(.+)$', value, re.MULTILINE)
            view = build_numbering_view_from_markdown(value)
            return _json_dumps(
                {
                    'title': title_match.group(1).strip() if title_match else '',
                    'markdown': materialize_markdown(
                        value, view, compute_numbering(view)
                    ),
                }
            )
        document = WriterDocument.model_validate(value)
        return _json_dumps(
            {
                'title': document.title,
                'markdown': writer_document_to_markdown(document),
            }
        )


__all__ = [
    'WRITER_BLOCK_SCHEMA',
    'WRITER_DATA_MODEL_SCHEMA_PREFIX',
    'WRITER_IR_SCHEMA',
    'WriterArtifactCapabilities',
    'WriterDocument',
    'inspect_document',
    'lmd_to_markdown',
    'markdown_to_lmd',
    'markdown_filename',
    'markdown_to_writer_document',
    'normalize_writer_document',
    'detach_provider_binding',
    'persist_artifact_json',
    'render_document',
    'save_document',
    'writer_schema',
]

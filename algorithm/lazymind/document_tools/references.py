"""Cross-reference discovery, binding, and editing helpers."""

from __future__ import annotations

import re
import uuid
from copy import deepcopy
from typing import Any, Literal

from lazyllm.tools.writer.data_models import (
    ContentRef,
    PatchHunk,
    PatchSet,
    StringReplace,
    StringReplaceSet,
    WriterDocument,
    WriterSpan,
)
from lazyllm.tools.writer.numbering import (
    build_numbering_view_from_ir,
    build_numbering_view_from_markdown,
    ensure_markdown_heading_anchors,
)
from lazyllm.tools.writer.tools import apply_patch_to_ir


CrossReferenceOperation = Literal['add', 'remove', 'retarget']
_MARKDOWN_INTERNAL_LINK_RE = re.compile(
    r'\[([^\]]+)\]\(#block-([A-Za-z0-9_.:-]+)\)'
)


class CrossReferenceError(ValueError):
    retryable = False

    def __init__(self, code: str, message: str,
                 details: dict[str, Any] | None = None) -> None:
        super().__init__(code, message, details)
        self.error_code = code
        self.message = message
        self.details = details or {}

    def __str__(self) -> str:
        return self.message


def _bind_document_cross_reference_targets(instructions: list[Any]) -> None:
    targets = list(
        dict.fromkeys(
            str(target)
            for instruction in instructions
            if isinstance(instruction, dict)
            for target in [
                (instruction.get('meta') or {}).get('outline_node_id'),
                *[
                    item.get('target')
                    for item in (instruction.get('meta') or {}).get('cross_references')
                    or []
                    if isinstance(item, dict)
                ],
            ]
            if target
        )
    )
    for instruction in instructions:
        if isinstance(instruction, dict):
            instruction.setdefault('meta', {})['cross_reference_targets'] = targets


def bind_cross_reference_targets(instructions: list[Any]) -> None:
    _bind_document_cross_reference_targets(instructions)


def list_cross_reference_targets(document: str | dict[str, Any]) -> dict[str, Any]:
    if isinstance(document, str):
        normalized = ensure_markdown_heading_anchors(document)
        view = build_numbering_view_from_markdown(normalized)
        targets = [
            {
                'target_id': target.id,
                'type': 'heading' if target.kind == 'section' else 'image',
                'title': target.caption or '',
            }
            for target in view.targets
            if target.kind in {'section', 'figure'}
            and not target.id.startswith('md-')
        ]
        reference_target_ids = {
            match.group(2)
            for match in _MARKDOWN_INTERNAL_LINK_RE.finditer(normalized)
        }
    else:
        writer_document = WriterDocument.model_validate(document)
        view = build_numbering_view_from_ir(writer_document)
        targets = [
            {
                'target_id': block.node_id,
                'type': block.type,
                'title': block.content,
            }
            for block in writer_document.iter_blocks()
            if block.type in {'heading', 'image'}
        ]
        reference_target_ids = {
            reference.target_id for reference in view.references
        }

    target_ids = {target.id for target in view.targets}
    invalid = sorted(reference_target_ids - target_ids)
    return {
        'representation': 'markdown' if isinstance(document, str) else 'ir',
        'targets': targets,
        'invalid_references': [{'target_id': target_id} for target_id in invalid],
    }


def update_cross_reference(
    document: str | dict[str, Any],
    operation: CrossReferenceOperation,
    selection: dict[str, Any],
    target_id: str = '',
) -> dict[str, Any]:
    if isinstance(document, str):
        if selection.get('type') != 'markdown':
            raise CrossReferenceError(
                'CROSS_REFERENCE_SELECTION_INVALID',
                "Markdown artifacts require selection.type='markdown'.",
            )
        return _update_markdown_reference(
            document, operation, str(selection.get('selected_text') or ''), target_id
        )
    if selection.get('type') != 'ir':
        raise CrossReferenceError(
            'CROSS_REFERENCE_SELECTION_INVALID',
            "IR artifacts require selection.type='ir'.",
        )
    return _update_ir_reference(
        WriterDocument.model_validate(document),
        operation,
        str(selection.get('node_id') or ''),
        str(selection.get('selected_text') or ''),
        target_id,
    )


def _require_target(document: str | WriterDocument, target_id: str) -> None:
    targets = list_cross_reference_targets(
        document if isinstance(document, str) else document.model_dump()
    )['targets']
    if target_id not in {target['target_id'] for target in targets}:
        raise CrossReferenceError(
            'CROSS_REFERENCE_TARGET_NOT_FOUND',
            f'Cross-reference target {target_id!r} does not exist.',
            {'target_id': target_id},
        )


def _unique_text_range(content: str, selected_text: str) -> tuple[int, int]:
    if not selected_text or content.count(selected_text) != 1:
        raise CrossReferenceError(
            'CROSS_REFERENCE_SELECTION_INVALID',
            'The selected text must identify exactly one location.',
        )
    start = content.index(selected_text)
    return start, start + len(selected_text)


def _update_markdown_reference(
    document: str,
    operation: CrossReferenceOperation,
    selected_text: str,
    target_id: str,
) -> dict[str, Any]:
    normalized = ensure_markdown_heading_anchors(document)
    if operation == 'add':
        _require_target(normalized, target_id)
        start, end = _unique_text_range(normalized, selected_text)
        candidate = (
            normalized[:start]
            + f'[{selected_text}](#block-{target_id})'
            + normalized[end:]
        )
    else:
        matches = [
            match for match in _MARKDOWN_INTERNAL_LINK_RE.finditer(normalized)
            if match.group(1) == selected_text
        ]
        if len(matches) != 1:
            raise CrossReferenceError(
                'CROSS_REFERENCE_SELECTION_INVALID',
                'The selected text must identify exactly one internal reference.',
            )
        match = matches[0]
        if operation == 'remove':
            replacement = selected_text
        else:
            _require_target(normalized, target_id)
            replacement = f'[{selected_text}](#block-{target_id})'
        candidate = normalized[:match.start()] + replacement + normalized[match.end():]

    patch = StringReplaceSet(
        replace_set_id=f'cross-reference-{uuid.uuid4().hex}',
        replacements=[StringReplace(
            replacement_id='update-cross-reference',
            old_string=normalized,
            new_string=candidate,
            content_ref=ContentRef(document_root=True),
        )],
        meta={'source': 'cross_reference_action'},
    )
    return {
        'representation': 'markdown',
        'document': candidate,
        'patch': {'type': 'string_replace_set', 'payload': patch.model_dump()},
    }


def _update_ir_reference(
    document: WriterDocument,
    operation: CrossReferenceOperation,
    node_id: str,
    selected_text: str,
    target_id: str,
) -> dict[str, Any]:
    block = document.block_by_id(node_id)
    if block is None:
        raise CrossReferenceError(
            'CROSS_REFERENCE_SELECTION_INVALID',
            f'Writer IR node {node_id!r} does not exist.',
        )
    start, end = _unique_text_range(block.content, selected_text)
    if operation != 'remove':
        _require_target(document, target_id)

    updated = block.model_copy(deep=True)
    spans = updated.spans
    if ''.join(span.text for span in spans) != updated.content:
        spans = [WriterSpan(text=updated.content)]
    updated.spans = _update_ir_spans(spans, start, end, operation, target_id)

    patch = PatchSet(
        patch_id=f'cross-reference-{uuid.uuid4().hex}',
        target_doc_id=document.document_id,
        hunks=[PatchHunk(
            hunk_id='update-cross-reference',
            target_node_id=node_id,
            modify_type='update',
            block=updated,
        )],
        meta={'source': 'cross_reference_action'},
    )
    candidate, _ = apply_patch_to_ir(document, patch)
    return {
        'representation': 'ir',
        'document': candidate.model_dump(exclude_defaults=True),
        'patch': {'type': 'writer_ir_patch', 'payload': patch.model_dump()},
    }


def _update_ir_spans(
    spans: list[WriterSpan],
    start: int,
    end: int,
    operation: CrossReferenceOperation,
    target_id: str,
) -> list[WriterSpan]:
    result: list[WriterSpan] = []
    offset = 0
    selected_links: list[dict[str, Any] | None] = []
    for span in spans:
        span_end = offset + len(span.text)
        overlap_start, overlap_end = max(start, offset), min(end, span_end)
        if overlap_start >= overlap_end:
            result.append(span.model_copy(deep=True))
            offset = span_end
            continue
        local_start, local_end = overlap_start - offset, overlap_end - offset
        if local_start:
            result.append(WriterSpan(text=span.text[:local_start], style=deepcopy(span.style)))
        style = deepcopy(span.style)
        link = style.get('link')
        selected_links.append(link if isinstance(link, dict) else None)
        if operation == 'remove':
            style.pop('link', None)
        else:
            style['link'] = {'type': 'internal_ref', 'target_node_id': target_id}
        result.append(WriterSpan(text=span.text[local_start:local_end], style=style))
        if local_end < len(span.text):
            result.append(WriterSpan(text=span.text[local_end:], style=deepcopy(span.style)))
        offset = span_end

    if operation in {'remove', 'retarget'} and (
        not selected_links
        or any(link is None or link.get('type') != 'internal_ref' for link in selected_links)
    ):
        raise CrossReferenceError(
            'CROSS_REFERENCE_SELECTION_INVALID',
            'The selected text is not an internal reference.',
        )
    return result


__all__ = [
    'CrossReferenceError',
    'CrossReferenceOperation',
    'bind_cross_reference_targets',
    'list_cross_reference_targets',
    'update_cross_reference',
]

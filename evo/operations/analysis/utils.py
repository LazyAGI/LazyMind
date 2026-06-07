from __future__ import annotations

import json
from typing import Any

from ...artifacts import ArtifactRef
from ...runtime import OperationContext

METRICS = ('answer_correctness', 'faithfulness', 'doc_recall', 'context_recall')


def jsonish(value: Any) -> Any:
    for _ in range(4):
        if not isinstance(value, str):
            return value
        text = value.strip()
        if not text or text[:1] not in {'"', '{', '['}:
            return value
        try:
            value = json.loads(text)
        except json.JSONDecodeError:
            return value
    return value


def values(value: Any) -> set[str]:
    items = value if isinstance(value, (list, tuple, set)) else [value] if value else []
    return {str(item).strip() for item in items if str(item).strip()}


def score(value: Any) -> float:
    try:
        return float(value)
    except (TypeError, ValueError):
        return 0.0


def short(value: Any, limit: int = 500) -> str:
    text = json.dumps(value, ensure_ascii=False) if isinstance(value, (dict, list)) else str(value or '')
    return text[:limit]


def typed_payload(ctx: OperationContext, ref: ArtifactRef, schema: str) -> dict[str, Any]:
    if ctx.artifact_graph.schema_name(ref) != schema:
        raise ValueError(f'artifact is not {schema}: {ref}')
    payload = ctx.artifact_graph.get(ref)
    if not isinstance(payload, dict):
        raise ValueError(f'{ref} payload must be object')
    return payload


def clean_contexts(contexts: Any) -> list[str]:
    output = []
    for item in contexts if isinstance(contexts, list) else []:
        text = _context_text(item)
        if text:
            output.append(text)
    return output


def _context_text(item: Any) -> str:
    if isinstance(item, str):
        return item.strip()
    if not isinstance(item, dict):
        return ''
    text = next(
        (
            str(item[key]).strip()
            for key in ('text', 'content', 'context', 'page_content', 'chunk_text')
            if item.get(key)
        ),
        '',
    )
    loc = ' '.join(
        f'{key}={item[key]}'
        for key in ('doc_id', 'document_id', 'chunk_id', 'segment_id', 'filename')
        if item.get(key)
    )
    return f'{loc}\n{text}'.strip() if text else ''

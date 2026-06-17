from __future__ import annotations

import json
from typing import Any

from ...artifacts import ArtifactRef
from ...runtime import OperationContext

METRICS = ('answer_correctness', 'faithfulness', 'doc_recall', 'context_recall')
CONTEXT_TEXT_KEYS = ('text', 'content', 'context', 'page_content', 'chunk_text')
CONTEXT_LOC_KEYS = ('doc_id', 'document_id', 'chunk_id', 'segment_id', 'filename')


def jsonish(value: Any) -> Any:
    for _ in range(4):
        if not isinstance(value, str) or not (text := value.strip()) or text[:1] not in {'"', '{', '['}:
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
    return (json.dumps(value, ensure_ascii=False) if isinstance(value, (dict, list)) else str(value or ''))[:limit]


def has_structure(text: str) -> bool:
    return any(mark in text for mark in ('|', '\t', '=', '∑', '公式', '表', 'Table', 'List', '1.', '- '))


def check_fields(name: str, payload: dict[str, Any], expected: dict[str, str]) -> None:
    for key, value in expected.items():
        if str(payload.get(key) or '') != value:
            raise ValueError(f'{name} {key} mismatch: {payload.get(key)!r} != {value!r}')


def typed_payload(ctx: OperationContext, ref: ArtifactRef, schema: str) -> dict[str, Any]:
    if ctx.artifact_graph.schema_name(ref) != schema:
        raise ValueError(f'artifact is not {schema}: {ref}')
    payload = ctx.artifact_graph.get(ref)
    if not isinstance(payload, dict):
        raise ValueError(f'{ref} payload must be object')
    return payload


def bound_input_ref(ctx: OperationContext, raw_ref: Any, schema: str) -> ArtifactRef:
    requested = ArtifactRef.parse(str(raw_ref or ''))
    ref = next((item for item in ctx.input_refs if item.artifact_id == requested.artifact_id), requested)
    if ctx.artifact_graph.schema_name(ref) != schema:
        raise ValueError(f'artifact is not {schema}: {ref}')
    return ref


def clean_contexts(contexts: Any) -> list[str]:
    out = []
    for item in contexts if isinstance(contexts, list) else []:
        if isinstance(item, str):
            if item.strip():
                out.append(item.strip())
        elif isinstance(item, dict):
            text = next((str(item[key]).strip() for key in CONTEXT_TEXT_KEYS if item.get(key)), '')
            loc = ' '.join(f'{key}={item[key]}' for key in CONTEXT_LOC_KEYS if item.get(key))
            if text:
                out.append(f'{loc}\n{text}'.strip())
    return out

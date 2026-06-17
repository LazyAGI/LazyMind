import json
import re
from typing import Any

from json_repair import loads as repair_json_loads

from ... import QUESTION_TYPES, validate_case_id

__all__ = ['QUESTION_TYPES', 'validate_case_id', 'bounded_int', 'strings', 'expected_ref', 'json_object', 'progress']


def bounded_int(value: Any, default: int, minimum: int, maximum: int) -> int:
    try:
        return max(minimum, min(maximum, int(value)))
    except (TypeError, ValueError):
        return max(minimum, min(maximum, default))


def strings(value: Any) -> list[str]:
    if value is None:
        return []
    values = [value] if isinstance(value, str) else value if isinstance(value, (list, tuple, set)) else [value]
    return [str(item).strip() for item in values if item is not None and str(item).strip()]


def expected_ref(ctx, draft) -> str:
    try:
        return f'{draft.artifact_id}@v{ctx.artifact_graph.latest_ref(draft.artifact_id).version + 1}'
    except KeyError:
        return f'{draft.artifact_id}@v1'


def progress(ctx, phase: str, status: str, message: str, **kwargs: Any) -> None:
    ctx.report_progress(phase=phase, status=status, message=message, **kwargs)


def json_object(value: Any) -> dict[str, Any]:
    if isinstance(value, dict):
        return value
    raw = str(value).strip()
    if raw.endswith('```'):
        raw = raw[: raw.rfind('```')].rstrip()
    if raw.endswith('</think>'):
        raw = raw[: -len('</think>')].rstrip()
    decoder = json.JSONDecoder()
    for match in reversed(list(re.finditer(r'\{', raw))):
        try:
            data, end = decoder.raw_decode(raw[match.start():])
        except json.JSONDecodeError:
            continue
        if isinstance(data, dict) and not raw[match.start() + end:].strip():
            return data
    data = repair_json_loads(raw)
    if not isinstance(data, dict):
        raise ValueError('expected JSON object')
    return data

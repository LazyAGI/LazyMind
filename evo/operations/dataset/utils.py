from __future__ import annotations

import json
import re
from typing import Any

from json_repair import loads as repair_json_loads

from ...ids import validate_id


QUESTION_TYPES = {'single_hop', 'single_doc_multi_hop', 'multi_doc_multi_hop', 'table_list', 'formula'}
RESERVED_CASE_IDS = QUESTION_TYPES | {f'case_{item}' for item in QUESTION_TYPES}


def bounded_int(value: Any, default: int, minimum: int, maximum: int) -> int:
    try:
        parsed = int(value)
    except (TypeError, ValueError):
        parsed = default
    return max(minimum, min(maximum, parsed))


def strings(value: Any) -> list[str]:
    if value is None:
        return []
    values = [value] if isinstance(value, str) else value if isinstance(value, (list, tuple, set)) else [value]
    return [str(item).strip() for item in values if item is not None and str(item).strip()]


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


def validate_case_id(value: str) -> str:
    case_id = validate_id(value.strip(), 'case_id')
    if case_id in RESERVED_CASE_IDS or case_id.startswith('case_preparation_'):
        raise ValueError(f'case_id must be a unique sample id, not a question type label: {case_id}')
    return case_id

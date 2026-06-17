from __future__ import annotations

import json
import re
import threading
from typing import Any

import jsonschema
from json_repair import repair_json
from lazyllm import LOG
from pydantic import BaseModel, TypeAdapter

from lazymind.review.skill_review.config import DEFAULT_LLM_CALL_TIMEOUT_SECONDS

# Approximate token usage by summing prompt/response string lengths across LLM calls.
TOTAL_INPUT_TOKEN_CHARS = 0
TOTAL_OUTPUT_TOKEN_CHARS = 0
_TOKEN_USAGE_LOCK = threading.Lock()


def call_json(
    llm,
    prompt: str,
    schema: Any,
    *,
    max_retries: int = 3,
) -> dict[str, Any]:
    """Call an LLM and return a JSON object."""
    if max_retries < 1:
        raise ValueError('max_retries must be at least 1')

    last_error: Exception | None = None
    last_raw: Any = None
    for round in range(max_retries):
        try:
            if round > 0:
                LOG.warning(f'LLM JSON call failed after {round} attempts, retrying...')
            raw = llm(
                prompt,
                response_format={'type': 'json_object'},
                timeout=DEFAULT_LLM_CALL_TIMEOUT_SECONDS,
            )
            last_raw = raw
            _record_token_usage(prompt, raw)
            parsed = _json_object(raw)
            return _validate_json_object(parsed, schema)
        except Exception as exc:
            last_error = exc

    snippet = re.sub(r'\s+', ' ', str(last_raw or '')).strip()[:500]
    raise ValueError(
        f'LLM JSON call failed after {max_retries} attempts: {last_error}; response={snippet}'
    ) from last_error


def _record_token_usage(prompt: str, raw: Any) -> None:
    global TOTAL_INPUT_TOKEN_CHARS, TOTAL_OUTPUT_TOKEN_CHARS
    with _TOKEN_USAGE_LOCK:
        TOTAL_INPUT_TOKEN_CHARS += len(prompt)
        TOTAL_OUTPUT_TOKEN_CHARS += len(str(raw))
        input_chars = TOTAL_INPUT_TOKEN_CHARS
        output_chars = TOTAL_OUTPUT_TOKEN_CHARS
    LOG.info(f'[SkillReview] Total input token chars: {input_chars}, total output token chars: {output_chars}')


def _json_object(raw: Any) -> dict[str, Any]:
    if isinstance(raw, BaseModel):
        return raw.model_dump()
    if isinstance(raw, dict):
        return raw
    parsed = _parse_json_object(str(raw))
    if not isinstance(parsed, dict):
        raise ValueError(f'LLM response JSON must be an object, got {type(parsed).__name__}')
    return parsed


def _parse_json_object(text: str) -> Any:
    text = re.sub(r'<think>.*?</think>', '', text, flags=re.S).strip()
    fenced = re.search(r'```(?:json)?\s*(\{.*\})\s*```', text, re.S)
    if fenced:
        text = fenced.group(1)
    else:
        start = text.find('{')
        end = text.rfind('}')
        if start >= 0 and end > start:
            text = text[start:end + 1]
    try:
        return json.loads(text)
    except json.JSONDecodeError:
        return repair_json(text, return_objects=True)


def _validate_json_object(payload: dict[str, Any], schema: Any) -> dict[str, Any]:
    if isinstance(schema, type) and issubclass(schema, BaseModel):
        return schema.model_validate(payload).model_dump()
    if isinstance(schema, dict):
        jsonschema.validate(payload, schema)
        return payload

    validated = TypeAdapter(schema).validate_python(payload)
    if isinstance(validated, BaseModel):
        return validated.model_dump()
    if isinstance(validated, dict):
        return validated
    raise ValueError(f'LLM response schema must validate to an object, got {type(validated).__name__}')

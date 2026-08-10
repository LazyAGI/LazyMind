from __future__ import annotations

import json
import re
from collections.abc import Mapping
from typing import Any

from json_repair import repair_json


_THINK_BLOCK = re.compile(r'<think\b[^>]*>.*?</think>', re.DOTALL | re.IGNORECASE)
_JSON_FENCE = re.compile(r'```(?:json)?\s*(.*?)\s*```', re.DOTALL | re.IGNORECASE)


class LazyLLMClient:
    def __init__(self, *, llm_config: Mapping[str, Any] | None = None, model: str | None = None) -> None:
        self.llm_config = dict(llm_config or {})
        self.model = _model_role(self.llm_config, model)
        self.session_id = f'evo-llm-{id(self)}'
        self._llm: Any | None = None

    def __call__(self, prompt: str, **kwargs: Any) -> Any:
        _activate_session(self.session_id, self.llm_config)
        if self._llm is None:
            self._llm = _lazyllm_model(self.model)
        return self._llm(prompt, **kwargs)


def parse_json_object(raw: Any) -> Mapping[str, Any]:
    if isinstance(raw, Mapping):
        return raw
    if callable(model_dump := getattr(raw, 'model_dump', None)):
        value = model_dump()
        if isinstance(value, Mapping):
            return value

    text = raw.decode('utf-8', errors='replace') if isinstance(raw, bytes | bytearray) else str(raw or '')
    text = _THINK_BLOCK.sub('', text).strip()
    try:
        value = json.loads(text)
    except json.JSONDecodeError:
        pass
    else:
        if isinstance(value, Mapping):
            return value
        raise ValueError('LLM response JSON must be an object')

    candidates = [
        *reversed(_JSON_FENCE.findall(text)),
        *reversed(_balanced_json_objects(text)),
        text,
    ]
    seen: set[str] = set()
    for candidate in candidates:
        candidate = candidate.strip()
        if not candidate or candidate in seen:
            continue
        seen.add(candidate)
        try:
            value = json.loads(candidate)
        except json.JSONDecodeError:
            try:
                value = repair_json(candidate, return_objects=True)
            except Exception:
                continue
        if isinstance(value, Mapping):
            return value
    raise ValueError('LLM response does not contain a JSON object')


def _balanced_json_objects(text: str) -> list[str]:
    objects: list[str] = []
    start: int | None = None
    depth = 0
    in_string = False
    escaped = False
    for index, char in enumerate(text):
        if start is None:
            if char == '{':
                start, depth = index, 1
            continue
        if in_string:
            if escaped:
                escaped = False
            elif char == '\\':
                escaped = True
            elif char == '"':
                in_string = False
            continue
        if char == '"':
            in_string = True
        elif char == '{':
            depth += 1
        elif char == '}':
            depth -= 1
            if depth == 0:
                objects.append(text[start:index + 1])
                start = None
    return objects


def _lazyllm_model(model: str) -> Any:
    from lazyllm import AutoModel

    return AutoModel(model=model)


def _activate_session(session_id: str, llm_config: Mapping[str, Any]) -> None:
    import lazyllm

    from lazymind.model_config import inject_model_config

    lazyllm.globals._init_sid(sid=session_id)
    lazyllm.locals._init_sid(session_id)
    if llm_config:
        inject_model_config(dict(llm_config))


def _model_role(llm_config: Mapping[str, Any], model: str | None) -> str:
    return str(model or 'evo_llm').strip() or 'evo_llm'

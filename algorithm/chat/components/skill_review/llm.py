from __future__ import annotations

import json
import re
from typing import Any

class LLMJsonError(RuntimeError):
    pass


class SkillReviewLLM:
    def __init__(self) -> None:
        self._llm = None
        self._load_error: Exception | None = None

    @property
    def llm(self):
        if self._load_error is not None:
            raise self._load_error
        if self._llm is None:
            from lazyllm import AutoModel
            from chat.utils.load_config import get_config_path

            try:
                self._llm = AutoModel(model='llm_instruct', config=get_config_path())
            except Exception as exc:
                self._load_error = exc
                raise
        return self._llm

    def complete_json(self, prompt: str) -> dict[str, Any]:
        raw = self.llm(prompt)
        return extract_json_object(raw)


def extract_json_object(raw: Any) -> dict[str, Any]:
    if isinstance(raw, dict):
        return raw
    text = str(raw or '').strip()
    if not text:
        raise LLMJsonError('empty LLM response')
    fenced = re.search(r'```(?:json)?\s*(\{.*?\})\s*```', text, re.S)
    if fenced:
        text = fenced.group(1)
    else:
        start = text.find('{')
        end = text.rfind('}')
        if start >= 0 and end > start:
            text = text[start:end + 1]
    try:
        parsed = json.loads(text)
    except json.JSONDecodeError as exc:
        raise LLMJsonError(f'failed to parse LLM JSON: {exc}') from exc
    if not isinstance(parsed, dict):
        raise LLMJsonError('LLM JSON root must be an object')
    return parsed

"""Model transport and isolated execution for Conversation services."""
from __future__ import annotations

from copy import deepcopy
import json
import re
from typing import Any, Callable
from uuid import uuid4

import lazyllm
import requests
from json_repair import repair_json
from lazyllm import AutoModel, LOG
from lazyllm.common import new_session
from lazyllm.module.llms.onlinemodule.base import ModelCallError, ModelFinish
from pydantic import BaseModel

from lazymind.model_config import inject_model_config
from .schemas import ConversationResult


class ConversationError(RuntimeError):
    pass


class ConversationCallError(ConversationError):
    def __init__(self, code: str, retryable: bool = False, calls: int = 0, usage: dict[str, Any] | None = None):
        super().__init__(code, retryable, calls, usage)
        self.code = code
        self.retryable = retryable
        self.calls = calls
        self.usage = usage or {}

    def __str__(self) -> str:
        return self.code


def validate_model_config(config: dict) -> None:
    selected = config.get('llm')
    if (not isinstance(selected, dict)
            or any(not isinstance(selected.get(key), str) or not selected[key].strip()
                   for key in ('source', 'model'))):
        raise ConversationCallError('model_configuration')


def execute(request: Any, operation: Callable) -> ConversationResult:
    """Own the session until the result and usage have been captured."""
    task_id = str(uuid4())
    global_sid, local_sid = lazyllm.globals._sid, lazyllm.locals._sid
    try:
        with new_session(f'conversation_{task_id}'):
            try:
                validate_model_config(request.llm_config)
                inject_model_config(deepcopy(request.llm_config))
            except Exception as exc:
                raise ConversationCallError('model_configuration') from exc
            output, usage = operation(request)
            return ConversationResult(status='succeeded', task_id=task_id, output=output,
                                      text=json.dumps(output, ensure_ascii=False), usage=deepcopy(usage))
    except ConversationCallError as exc:
        return ConversationResult(status='failed', task_id=task_id, error=str(exc), error_code=exc.code,
                                  retryable=exc.retryable, usage={**exc.usage, 'model_calls': exc.calls})
    except Exception:
        LOG.exception('[Conversation] execution failed')
        return ConversationResult(status='failed', task_id=task_id, error='model_failed', error_code='model_failed')
    finally:
        lazyllm.globals._init_sid(global_sid)
        lazyllm.locals._init_sid(local_sid)


def call_model(request: Any, prompt: str, *, stream_output: bool = True,
               default_timeout: int = 600, **options: Any) -> Any:
    try:
        timeout = int(request.options.get('timeout_seconds', default_timeout))
        if timeout <= 0:
            raise ValueError('timeout must be positive')
    except (TypeError, ValueError) as exc:
        raise ConversationCallError('invalid_task_config') from exc
    try:
        validate_model_config(request.llm_config)
        model = AutoModel(source='dynamic', type='llm', name='llm', dynamic_auth=True)
    except Exception as exc:
        raise ConversationCallError('model_configuration') from exc
    return model(prompt, stream_output=stream_output, temperature=request.options.get('temperature', 0),
                 timeout=timeout, **options)


def call_error(exc: Exception) -> ConversationCallError:
    current = exc
    while current is not None:
        if isinstance(current, ConversationCallError):
            return current
        if isinstance(current, ModelCallError):
            if current.terminal.finish == ModelFinish.LENGTH:
                return ConversationCallError('output_too_large', calls=1)
            failure = current.terminal.failure
            code = failure.code.value if failure else 'model_failed'
            status = failure.provider_http_status if failure else None
            retryable = status in (408, 429, 500, 502, 503, 504) or code in ('request_timeout', 'transport_error')
            return ConversationCallError(code, retryable=retryable, calls=1)
        if isinstance(current, (requests.Timeout, requests.ConnectionError)):
            return ConversationCallError('transport_error', retryable=True, calls=1)
        current = current.__cause__ or current.__context__
    code = 'invalid_output' if isinstance(exc, ValueError) else 'model_failed'
    return ConversationCallError(code, calls=1)


def call_structured(request: Any, prompt: str,
                    schema: type[BaseModel]) -> tuple[dict[str, Any], dict[str, Any]]:
    selected = request.llm_config['llm']
    identity = {'role': 'llm', 'source': selected.get('source', ''), 'model': selected.get('model', '')}
    usage = {'model_id': identity, 'truncated': False}
    try:
        raw = call_model(request, prompt, response_format={'type': 'json_object'},
                         stream_output=False, default_timeout=60, max_retries=1)
        # Strict tasks leave retries to the caller and never repair incomplete JSON.
        output = schema.model_validate_json(raw).model_dump()
    except ConversationCallError as exc:
        exc.usage = usage
        raise
    except Exception as exc:
        error = call_error(exc)
        error.usage = usage
        raise error from exc
    return output, {**usage, 'model_calls': 1, 'provider_usage': dict(lazyllm.globals['usage'])}


def json_object(raw: Any) -> dict[str, Any]:
    if isinstance(raw, BaseModel):
        return raw.model_dump()
    if isinstance(raw, dict):
        return raw
    parsed = _parse_json(str(raw))
    if not isinstance(parsed, dict):
        raise ValueError(f'expected JSON object, got {type(parsed).__name__}')
    return parsed


def _parse_json(text: str) -> Any:
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

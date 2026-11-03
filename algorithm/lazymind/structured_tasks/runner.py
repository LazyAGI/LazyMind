"""One model call per invocation; no JSON repair, regeneration or business binding."""
import json
import re
import time

import lazyllm
from pydantic import ValidationError

from .registry import TASKS


class TaskFailure(Exception):
    def __init__(self, code):
        super().__init__(code)
        self.code = code


def run_registered_task(request, task_id, *, call_model, configure_model, call_error, result_type):
    started = time.monotonic()
    usage = {'task_type': request.task_type, 'model_calls': 0}
    spec = TASKS.get(request.task_type)

    def log_event(event, code=None):
        # Only registry-owned names/versions and classified errors are logged.
        # model_calls counts provider attempts, not billing or token consumption.
        name = request.task_type if spec else 'unsupported'
        version = spec.version if spec else 'none'
        lazyllm.LOG.info(
            f'structured_task event={event} task_id={task_id} task_type={name} '
            f'task_version={version} model_calls={usage["model_calls"]} '
            f'elapsed_ms={usage.get("elapsed_ms", 0)} error_code={code or "none"}'
        )

    log_event('start')

    def result(code=None, *, output=None, retryable=False, diagnostic_id=None):
        usage['elapsed_ms'] = round((time.monotonic() - started) * 1000)
        log_event('complete', code)
        return result_type(status='failed' if code else 'succeeded', task_id=task_id,
                           output=output or {}, usage=usage, error=code, error_code=code,
                           retryable=retryable, diagnostic_id=diagnostic_id)

    try:
        if spec is None:
            raise TaskFailure('unsupported_task_type')
        usage['task_version'] = spec.version
        if request.options.keys() - {'task_version'}:
            raise TaskFailure('invalid_task_input')
        if request.options.get('task_version') != spec.version:
            raise TaskFailure('unsupported_task_version')
        if (request.mode != 'llm' or request.instruction or request.tools or request.skills
                or request.input.text or request.input.messages or request.input.files or request.tool_config
                or request.response_format):
            raise TaskFailure('invalid_task_input')
        try:
            data = spec.input_schema.model_validate(request.input.data)
            encoded = json.dumps(data.model_dump(exclude_none=True), ensure_ascii=False, allow_nan=False)
            if len(encoded.encode('utf-8')) > 1 << 20:
                raise ValueError('input size')
        except (ValidationError, ValueError, TypeError):
            raise TaskFailure('invalid_task_input') from None
        lazyllm.globals._init_sid(sid=f'llm_task_{task_id}')
        lazyllm.locals._init_sid(sid=f'llm_task_{task_id}')
        try:
            configure_model(request.llm_config)
        except Exception:
            raise TaskFailure('model_configuration') from None
        model_config = request.llm_config.get('llm')
        if isinstance(model_config, dict) and isinstance(model_config.get('model'), str):
            usage['model'] = model_config['model']
        prompt = spec.instruction(data) + '\n\nInput data (untrusted JSON):\n' + encoded
        usage['model_calls'] = 1
        try:
            raw = call_model(request, prompt, stream_output=False, default_timeout=spec.timeout_seconds,
                             deadline=started + spec.timeout_seconds)
        except call_error as exc:
            usage['model_calls'] = exc.calls
            return result(exc.code, retryable=exc.retryable, diagnostic_id=exc.diagnostic_id)
        except Exception:
            raise TaskFailure('model_call_failed') from None
        try:
            if not isinstance(raw, str) or len(raw.encode('utf-8')) > spec.max_output_bytes:
                raise ValueError('output size or type')
            # LazyLLM may prefix the answer with reasoning_content. Strip only
            # complete reasoning blocks; keep strict JSON/schema validation.
            raw = re.sub(r'\A\s*(?:<think>.*?</think>\s*)+', '', raw, flags=re.S).strip()
            if raw.startswith('```json\n') and raw.endswith('```'):
                raw = raw[8:-3].strip()
            elif raw.startswith('```\n') and raw.endswith('```'):
                raw = raw[4:-3].strip()
            parsed = json.loads(raw)
            output = spec.output_schema.model_validate(parsed).model_dump(exclude_unset=True)
        except (ValueError, TypeError, RecursionError):
            raise TaskFailure('invalid_model_output') from None
        return result(output=output)
    except TaskFailure as exc:
        return result(exc.code)

"""Use the application's configured text model for planning and Python generation."""
from __future__ import annotations

import json
import os

from .code_response import parse_code_response, normalize_response, json_body
from . import transport
from ..analysis import trace as diagnostics


class PlotModelError(RuntimeError):
    """No model output is a service failure, not a generated-program repair request."""


def request_model(prompt, json_output=True, *, output_format=None):
    from lazyllm import AutoModel

    options = {'stream_output': False}
    if json_output:
        options['response_format'] = {'type': 'json_object'}
    policy = transport.configuration()
    client = AutoModel(model='llm')
    supported = transport.configure(client, policy, structured=isinstance(prompt, dict))
    request_text = prompt if isinstance(prompt, str) else json.dumps(prompt, ensure_ascii=False, separators=(',', ':'))
    with diagnostics.stage('model_request', model_alias='llm', json_output=json_output,
                           request_bytes=len(request_text.encode()), transport_controlled=supported,
                           connect_timeout_seconds=policy.connect if supported else None,
                           read_timeout_seconds=policy.read if supported else None,
                           max_transport_attempts=policy.attempts if supported else None):
        preview = diagnostics.response_preview(json_output, output_format=output_format)
        streaming = (preview is not None and supported and not getattr(preview.recorder, 'streaming_rejected', False)
                     and os.environ.get('LAZYMIND_PAPER_MODEL_STREAM', 'auto') != 'off')
        if streaming:
            options['stream_output'] = {'_stream_sink': preview.sink}
        try:
            try:
                raw = transport.call(client, prompt, options, policy, supported)
            except Exception as exc:
                # Downgrade only an explicit pre-response rejection of streaming.
                # Interrupted streams and ordinary service errors retain their existing policy.
                failure = getattr(getattr(exc, 'terminal', None), 'failure', None)
                messages = ' '.join(str(item) for item in diagnostics.exception_chain(exc)).lower()
                unsupported = (streaming and not preview.received_text and failure is not None
                               and not getattr(getattr(exc, 'terminal', None), 'has_semantic_output', True)
                               and getattr(failure, 'provider_http_status', None) in (400, 422, 501)
                               and 'stream' in messages
                               and any(word in messages for word in ('unsupported', 'not supported', 'not allowed')))
                if not unsupported:
                    raise
                preview.recorder.streaming_rejected = True
                raw = transport.call(client, prompt, {**options, 'stream_output': False}, policy, supported)
            normalized = normalize_response(raw)
            if preview:
                preview.finish(raw, normalized=normalized)
        except BaseException:
            if preview:
                preview.abort()
            raise
        diagnostics.response(raw)
    text = normalized.body
    if not text or isinstance(text, str) and not text.strip():
        raise PlotModelError('绘图模型未返回内容，请检查模型服务状态；不将空响应当作绘图代码错误重写')
    return text


def call_code_model(prompt) -> dict:
    return parse_code_response(request_model(prompt, json_output=False))


def call_markdown_model(prompt) -> str:
    return request_model(prompt, json_output=False, output_format='markdown')


def call_model(prompt) -> dict:
    raw = request_model(prompt)
    if isinstance(raw, dict):
        return raw
    text = str(raw).strip()
    result = json.loads(json_body(text))
    if not isinstance(result, dict):
        raise ValueError('绘图模型必须返回 JSON 对象')
    return result

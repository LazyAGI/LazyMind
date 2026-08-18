#!/usr/bin/env python3
"""Small, dependency-free OpenAI-compatible server for local UI error testing.

The response scenario is selected from the latest user query, never from the
model name.  Use an explicit marker such as ``[fake:length]`` in the prompt.
This server is intentionally local-only test infrastructure and must not be
used as a production proxy.
"""

from __future__ import annotations

import argparse
import json
import re
import sys
import time
import uuid
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from typing import Any
from urllib.parse import urlparse


MARKER_RE = re.compile(r"\[\s*fake\s*:\s*([a-z0-9_-]+)\s*\]", re.IGNORECASE)

SCENARIO_ALIASES = {
    "ok": "stop",
    "normal": "stop",
    "stop": "stop",
    "length": "length",
    "content_filter": "content_filter",
    "sensitive_input": "http_400_sensitive_input",
    "sensitive_output": "content_filter",
    "tool_calls": "tool_calls",
    "insufficient_system_resource": "insufficient_system_resource",
    "unknown_finish": "unknown_finish",
    "400": "http_400",
    "bad_request": "http_400",
    "401": "http_401",
    "auth": "http_401",
    "authentication": "http_401",
    "402": "http_402",
    "insufficient_balance": "http_402",
    "balance": "http_402",
    "422": "http_422",
    "invalid_params": "http_422",
    "429": "http_429",
    "429_quota": "http_429_quota",
    "429_unknown": "http_429_unknown",
    "rate_limit": "http_429",
    "ratelimit": "http_429",
    "500": "http_500",
    "server_error": "http_500",
    "503": "http_503",
    "overloaded": "http_503",
    "server_overloaded": "http_503",
    "protocol_json": "protocol_json",
    "malformed_json": "protocol_json",
    "protocol_sse": "protocol_sse",
    "malformed_sse": "protocol_sse",
    "eof": "eof",
    "minimax_1002": "minimax_1002",
    "minimax_1008": "minimax_1008",
    "minimax_1026": "minimax_1026",
    "minimax_1027": "minimax_1027",
}

HTTP_ERRORS = {
    "http_400": (400, "invalid_request_error", "invalid_request", "Invalid request format."),
    "http_401": (401, "authentication_error", "invalid_api_key", "Authentication failed."),
    "http_402": (402, "request_error", "payment_required", "The provider rejected the request."),
    "http_422": (422, "invalid_request_error", "invalid_parameters", "Invalid request parameters."),
    "http_400_sensitive_input": (
        400,
        "invalid_request_error",
        "content_policy_violation",
        "Request rejected by the content safety policy.",
    ),
    "http_429": (429, "rate_limit_error", "rate_limit_exceeded", "Rate limit reached."),
    "http_429_quota": (
        429,
        "insufficient_quota",
        "credit_balance_exhausted",
        "Credit balance exhausted.",
    ),
    "http_429_unknown": (429, "provider_error", "gateway_limit", "Request was limited."),
    "http_500": (500, "server_error", "internal_server_error", "The upstream server failed."),
    "http_503": (503, "server_error", "server_overloaded", "The upstream server is overloaded."),
}

MINIMAX_ERRORS = {
    "minimax_1002": (1002, "rate limit"),
    "minimax_1008": (1008, "insufficient balance"),
    "minimax_1026": (1026, "input sensitive"),
    "minimax_1027": (1027, "output sensitive"),
}


def _request_id() -> str:
    return f"req_fake_{uuid.uuid4().hex}"


def _text_from_content(content: Any) -> str:
    if isinstance(content, str):
        return content
    if isinstance(content, list):
        parts: list[str] = []
        for item in content:
            if isinstance(item, dict) and item.get("type") in {"text", "input_text"}:
                parts.append(str(item.get("text", "")))
        return "".join(parts)
    return ""


def latest_user_query(payload: dict[str, Any]) -> str:
    messages = payload.get("messages")
    if not isinstance(messages, list):
        return ""
    for message in reversed(messages):
        if isinstance(message, dict) and message.get("role") == "user":
            return _text_from_content(message.get("content"))
    return ""


def scenario_for_query(query: str) -> str:
    match = MARKER_RE.search(query)
    if match:
        return SCENARIO_ALIASES.get(match.group(1).lower(), match.group(1).lower())

    # Friendly aliases make ad-hoc browser testing convenient while keeping
    # the explicit marker as the documented and deterministic form.
    lowered = query.lower()
    if "服务器负载过高" in query or "server overloaded" in lowered:
        return "http_503"
    if "限流" in query or "rate limit" in lowered:
        return "http_429"
    if "余额不足" in query or "insufficient balance" in lowered:
        return "http_402"
    if "输入敏感词" in query or "sensitive input" in lowered:
        return "http_400_sensitive_input"
    if "敏感词" in query or "content filter" in lowered:
        return "content_filter"
    if "达到长度上限" in query or "finish_reason=length" in lowered:
        return "length"
    return "stop"


def _usage(query: str, content: str) -> dict[str, int]:
    # This is deliberately approximate; it only makes the fixture shape look
    # realistic.  The fake service does not pretend to tokenize provider text.
    prompt_tokens = max(1, len(query) // 4)
    completion_tokens = len(content) // 4
    return {
        "prompt_tokens": prompt_tokens,
        "completion_tokens": completion_tokens,
        "total_tokens": prompt_tokens + completion_tokens,
    }


def _base_response(model: str, choices: list[dict[str, Any]], usage: dict[str, int]) -> dict[str, Any]:
    return {
        "id": f"chatcmpl-fake-{uuid.uuid4().hex}",
        "object": "chat.completion",
        "created": int(time.time()),
        "model": model or "fake-openai",
        "choices": choices,
        "usage": usage,
        "system_fingerprint": "fake-openai-local",
    }


def _text_choice(content: str, finish_reason: str, usage: dict[str, int]) -> dict[str, Any]:
    return {
        "index": 0,
        "message": {"role": "assistant", "content": content},
        "logprobs": None,
        "finish_reason": finish_reason,
    }


def completion_payload(model: str, query: str, scenario: str) -> dict[str, Any]:
    if scenario == "length":
        content = "这是达到输出长度上限前保留的部分内容。"
        return _base_response(model, [_text_choice(content, "length", _usage(query, content))], _usage(query, content))
    if scenario == "content_filter":
        return _base_response(model, [_text_choice("", "content_filter", _usage(query, ""))], _usage(query, ""))
    if scenario == "insufficient_system_resource":
        content = "后端资源不足，以下是已经生成的部分内容。"
        return _base_response(
            model,
            [_text_choice(content, "insufficient_system_resource", _usage(query, content))],
            _usage(query, content),
        )
    if scenario == "unknown_finish":
        content = "服务返回了一个未识别的终止原因。"
        return _base_response(model, [_text_choice(content, "provider_custom_finish", _usage(query, content))], _usage(query, content))
    if scenario == "tool_calls":
        tool_calls = [
            {
                "id": "call_fake_weather",
                "type": "function",
                "function": {"name": "fake_weather", "arguments": '{"city":"Shanghai"}'},
            }
        ]
        choice = {
            "index": 0,
            "message": {"role": "assistant", "content": None, "tool_calls": tool_calls},
            "logprobs": None,
            "finish_reason": "tool_calls",
        }
        return _base_response(model, [choice], _usage(query, ""))

    content = "Fake OpenAI-compatible response: stop."
    return _base_response(model, [_text_choice(content, "stop", _usage(query, content))], _usage(query, content))


def _sse_chunk(response_id: str, model: str, delta: dict[str, Any], finish_reason: str | None = None) -> str:
    payload = {
        "id": response_id,
        "object": "chat.completion.chunk",
        "created": int(time.time()),
        "model": model or "fake-openai",
        "system_fingerprint": "fake-openai-local",
        "choices": [{"index": 0, "delta": delta, "finish_reason": finish_reason}],
    }
    return f"data: {json.dumps(payload, ensure_ascii=False)}\n\n"


class FakeOpenAIHandler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, format: str, *args: Any) -> None:
        sys.stderr.write(f"{self.address_string()} - {format % args}\n")

    def _send_json(self, status: int, payload: dict[str, Any], headers: dict[str, str] | None = None) -> None:
        body = json.dumps(payload, ensure_ascii=False).encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.send_header("Connection", "close")
        self.send_header("X-Request-Id", _request_id())
        for key, value in (headers or {}).items():
            self.send_header(key, value)
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self) -> None:  # noqa: N802
        path = urlparse(self.path).path
        if path == "/health":
            self._send_json(200, {"status": "ok"})
            return
        if path.rstrip("/") == "/v1/models":
            self._send_json(
                200,
                {
                    "object": "list",
                    "data": [{"id": "fake-openai", "object": "model", "created": 0, "owned_by": "tests"}],
                },
            )
            return
        self._send_json(404, {"error": {"message": "Not found", "type": "invalid_request_error", "code": "not_found"}})

    def do_POST(self) -> None:  # noqa: N802
        path = urlparse(self.path).path.rstrip("/")
        if path != "/v1/chat/completions":
            self._send_json(404, {"error": {"message": "Not found", "type": "invalid_request_error", "code": "not_found"}})
            return

        try:
            length = int(self.headers.get("Content-Length", "0"))
            payload = json.loads(self.rfile.read(length))
        except (ValueError, json.JSONDecodeError):
            self._send_json(400, {"error": {"message": "Invalid JSON body", "type": "invalid_request_error", "code": "invalid_json"}})
            return
        if not isinstance(payload, dict) or not isinstance(payload.get("messages"), list):
            self._send_json(400, {"error": {"message": "messages must be an array", "type": "invalid_request_error", "code": "invalid_messages"}})
            return

        query = latest_user_query(payload)
        scenario = scenario_for_query(query)
        model = str(payload.get("model") or "fake-openai")

        if scenario in HTTP_ERRORS:
            status, error_type, code, message = HTTP_ERRORS[scenario]
            headers = {"Retry-After": "2"} if status in {429, 503} else {}
            self._send_json(status, {"error": {"message": message, "type": error_type, "param": None, "code": code}}, headers)
            return
        if scenario in MINIMAX_ERRORS:
            status_code, status_msg = MINIMAX_ERRORS[scenario]
            self._send_json(200, {
                "base_resp": {"status_code": status_code, "status_msg": status_msg},
            })
            return
        if scenario == "protocol_json":
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.send_header("Connection", "close")
            self.end_headers()
            self.wfile.write(b'{"id":"chatcmpl-fake",')
            return

        if payload.get("stream"):
            self._stream_response(payload, model, query, scenario)
            return
        response = completion_payload(model, query, scenario)
        self._send_json(200, response)

    def _stream_response(self, payload: dict[str, Any], model: str, query: str, scenario: str) -> None:
        response_id = f"chatcmpl-fake-{uuid.uuid4().hex}"
        self.send_response(200)
        self.send_header("Content-Type", "text/event-stream")
        self.send_header("Cache-Control", "no-cache")
        self.send_header("Connection", "close")
        self.send_header("X-Request-Id", _request_id())
        self.end_headers()

        def write(event: str) -> None:
            self.wfile.write(event.encode("utf-8"))
            self.wfile.flush()

        write(_sse_chunk(response_id, model, {"role": "assistant", "content": ""}))
        if scenario == "protocol_sse":
            write("data: {not-valid-json}\n\n")
            write("data: [DONE]\n\n")
            return
        if scenario == "eof":
            write(_sse_chunk(response_id, model, {"content": "已经输出了一部分，但连接会异常断开。"}))
            self.close_connection = True
            return
        if scenario == "tool_calls":
            write(_sse_chunk(response_id, model, {"tool_calls": [{"index": 0, "id": "call_fake_weather", "type": "function", "function": {"name": "fake_weather", "arguments": ""}}]}))
            write(_sse_chunk(response_id, model, {"tool_calls": [{"index": 0, "function": {"arguments": '{"city":"Shanghai"}'}}]}))
            terminal = "tool_calls"
        else:
            content = completion_payload(model, query, scenario)["choices"][0]["message"].get("content") or ""
            for start in range(0, len(content), 8):
                write(_sse_chunk(response_id, model, {"content": content[start : start + 8]}))
            terminal = {"content_filter": "content_filter", "length": "length", "insufficient_system_resource": "insufficient_system_resource", "unknown_finish": "provider_custom_finish"}.get(scenario, "stop")
        terminal_event = _sse_chunk(response_id, model, {"content": ""}, terminal)
        if isinstance(payload.get("stream_options"), dict) and payload["stream_options"].get("include_usage"):
            event_payload = json.loads(terminal_event[6:].strip())
            event_payload["usage"] = _usage(query, "")
            terminal_event = f"data: {json.dumps(event_payload, ensure_ascii=False)}\n\n"
        write(terminal_event)
        write("data: [DONE]\n\n")


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--host", default="127.0.0.1")
    parser.add_argument("--port", type=int, default=18081)
    args = parser.parse_args()
    server = ThreadingHTTPServer((args.host, args.port), FakeOpenAIHandler)
    print(f"fake OpenAI server listening on http://{args.host}:{args.port}", flush=True)
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        pass
    finally:
        server.server_close()


if __name__ == "__main__":
    main()

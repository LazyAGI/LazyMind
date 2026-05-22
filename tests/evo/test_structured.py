"""Unit tests for the structured LLM pipeline (schema + repair loop)."""

from __future__ import annotations

import json
import sys
import traceback
from typing import Callable

from evo.harness.schemas import SCHEMAS
from evo.harness.structured import invoke_structured, parse_and_validate
from evo.runtime.config import load_config
from evo.runtime.session import create_session, session_scope


def _h(name: str) -> None:
    print(f"\n=== {name} ===")


class _ScriptedInvoker:
    """Returns the next scripted response on each invoke()."""

    def __init__(self, *a, **kw) -> None:
        self._responses: list[str] = []
        self._idx: int = 0
        self.calls: list[str] = []

    def script(self, *responses: str) -> None:
        self._responses = list(responses)
        self._idx = 0
        self.calls.clear()

    def invoke(self, user_text: str, **kwargs) -> str:
        self.calls.append(user_text)
        if self._idx >= len(self._responses):
            return ""
        out = self._responses[self._idx]
        self._idx += 1
        return out


def _events(session, event_type: str) -> list[dict]:
    return [e.payload for e in session.telemetry.history if e.type == event_type]


def test_parse_and_validate_reports_path_and_message() -> None:
    _h("parse_and_validate: empty summary produces readable error")
    _, errors, _ = parse_and_validate(
        json.dumps({"actions": [], "guidance": "g"}),
        SCHEMAS["synthesizer"],
    )
    assert any("summary" in e for e in errors), errors
    _, errors, _ = parse_and_validate(
        json.dumps({"summary": "ok", "actions": []}),
        SCHEMAS["synthesizer"],
    )
    assert errors == []
    print("  -> OK")


def test_invoke_structured_happy_path_no_repair() -> None:
    _h("invoke_structured: valid first-shot output triggers no repair")
    session = create_session(load_config())
    invoker = _ScriptedInvoker()
    invoker.script(json.dumps({"summary": "ok", "actions": []}))
    with session_scope(session):
        parsed = invoke_structured(
            session, invoker, "user",
            agent="synthesizer", schema=SCHEMAS["synthesizer"],
        )
    assert parsed == {"summary": "ok", "actions": []}
    assert len(invoker.calls) == 1
    print("  -> OK")


def test_invoke_structured_repair_recovers() -> None:
    _h("invoke_structured: first bad output -> repair turn succeeds")
    session = create_session(load_config())
    invoker = _ScriptedInvoker()
    invoker.script(
        json.dumps({"actions": []}),
        json.dumps({"summary": "fixed", "actions": []}),
    )
    with session_scope(session):
        parsed = invoke_structured(
            session, invoker, "world snapshot",
            agent="synthesizer", schema=SCHEMAS["synthesizer"],
        )
    assert parsed == {"summary": "fixed", "actions": []}
    assert len(invoker.calls) == 2
    assert "validation errors" in invoker.calls[1].lower()
    assert _events(session, "schema_repair_failed") == []
    repair_llm = [e for e in _events(session, "llm_call")
                  if "repair1" in str(e.get("agent", ""))]
    assert repair_llm, "expected an llm_call tagged synthesizer:repair1"
    print("  -> OK")


def test_invoke_structured_emits_failure_event() -> None:
    _h("invoke_structured: repair exhausted -> schema_repair_failed emitted")
    session = create_session(load_config())
    invoker = _ScriptedInvoker()
    invoker.script("not json", json.dumps({"actions": []}))
    with session_scope(session):
        parsed = invoke_structured(
            session, invoker, "world snapshot",
            agent="synthesizer", schema=SCHEMAS["synthesizer"],
        )
    assert "summary" not in parsed
    assert len(invoker.calls) == 3
    fail_events = _events(session, "schema_repair_failed")
    assert len(fail_events) == 1
    assert fail_events[0]["agent"] == "synthesizer"
    assert any("summary" in err or "empty response" in err for err in fail_events[0]["errors"])
    print("  -> OK")


def test_invoke_structured_uses_custom_producer_only_on_first_attempt() -> None:
    _h("invoke_structured: producer runs on attempt 0, invoker on repair")
    session = create_session(load_config())
    invoker = _ScriptedInvoker()
    invoker.script(json.dumps({"summary": "fixed", "actions": []}))
    producer_calls: list[str] = []

    def producer(u: str) -> str:
        producer_calls.append(u)
        return json.dumps({"actions": []})

    with session_scope(session):
        parsed = invoke_structured(
            session, invoker, "task",
            agent="researcher", schema=SCHEMAS["synthesizer"],
            producer=producer,
        )
    assert parsed == {"summary": "fixed", "actions": []}
    assert len(producer_calls) == 1
    assert "task" in producer_calls[0]
    assert "OUTPUT FORMAT" in producer_calls[0]
    assert len(invoker.calls) == 1
    print("  -> OK")


def _run(tests: list[Callable[[], None]]) -> int:
    failures = 0
    for t in tests:
        try:
            t()
        except Exception:
            failures += 1
            print(f"FAILED: {t.__name__}")
            traceback.print_exc(limit=5)
    print(f"\n{len(tests) - failures}/{len(tests)} passed")
    return 0 if failures == 0 else 1


def main() -> int:
    return _run([
        test_parse_and_validate_reports_path_and_message,
        test_invoke_structured_happy_path_no_repair,
        test_invoke_structured_repair_recovers,
        test_invoke_structured_emits_failure_event,
        test_invoke_structured_uses_custom_producer_only_on_first_attempt,
    ])


if __name__ == "__main__":
    sys.exit(main())

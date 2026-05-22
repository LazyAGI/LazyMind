"""Phase 8 tests for Action Verifier.

Run: PYTHONPATH=. .venv/bin/python -m evo.tests.test_action_verifier
"""

from __future__ import annotations

import json
import sys
import traceback
from typing import Callable
from unittest.mock import patch

from evo.agents.action_verifier import (
    _clamp, _gather_handles, run_action_verifier, verify_actions,
)
from evo.conductor.synthesis import VerifiedAction
from evo.runtime.config import load_config
from evo.runtime.session import create_session, session_scope


def _h(name: str) -> None:
    print(f"\n=== {name} ===")


def _make_action(*, aid: str = "A1", handles: list[str] | None = None) -> VerifiedAction:
    return VerifiedAction(
        id=aid, finding_id="F1", hypothesis_id="H1",
        hypothesis_category="rerank_failure",
        title="t", rationale="r", suggested_changes="x",
        priority="P0", expected_impact_metric="chunk_recall_delta",
        expected_direction="+", confidence=0.85,
        evidence_handles=list(handles or ["h_0001"]),
    )


class _Scripted:
    responses: list[str] = []
    idx: int = 0

    def __init__(self, *a, **kw):
        pass

    def invoke(self, _: str, **kwargs) -> str:
        if _Scripted.idx >= len(_Scripted.responses):
            return ""
        out = _Scripted.responses[_Scripted.idx]
        _Scripted.idx += 1
        return out


def _scripted(*responses: str) -> None:
    _Scripted.responses = list(responses)
    _Scripted.idx = 0


def test_clamp_keeps_in_range() -> None:
    _h("_clamp: keeps within [0, 1]")
    assert _clamp(0.5) == 0.5
    assert _clamp(2.0) == 1.0
    assert _clamp(-1.0) == 0.0
    assert _clamp("0.7") == 0.7
    assert _clamp("not a num") == 0.0
    print("  -> OK")


def test_gather_handles_returns_full_records() -> None:
    _h("_gather_handles: pulls full result for each handle")
    session = create_session(load_config())
    h1 = session.handle_store.append("foo", {"a": 1}, {"x": "x1"})
    h2 = session.handle_store.append("bar", {}, [1, 2, 3])
    action = _make_action(handles=[h1, h2, "h_missing"])
    out = _gather_handles(session, action)
    assert len(out) == 2
    assert out[0]["tool"] == "foo" and out[0]["result"] == {"x": "x1"}
    assert out[1]["result"] == [1, 2, 3]
    print("  -> OK")


def test_run_verifier_fills_action_fields() -> None:
    _h("run_action_verifier: populates validity_score / supporting / contradicting / notes")
    session = create_session(load_config())
    session.handle_store.append("inspect", {}, {"y": 1})
    action = _make_action(handles=["h_0001"])
    fake = json.dumps({
        "validity_score": 0.85,
        "supporting_evidence": ["h_0001 显示 mean=0.0", "另一证据"],
        "contradicting_evidence": [],
        "notes": ["验证通过"],
    })
    _scripted(fake)
    with session_scope(session):
        with patch("evo.agents.action_verifier.LLMInvoker", _Scripted):
            r = run_action_verifier(session, action)
    assert r is action
    assert r.validity_score == 0.85
    assert len(r.supporting_evidence) == 2
    assert r.contradicting_evidence == []
    assert r.verifier_notes == ["验证通过"]
    print("  -> OK")


def test_run_verifier_handles_parse_failure() -> None:
    _h("run_action_verifier: non-JSON -> validity_score=0, lists empty")
    session = create_session(load_config())
    action = _make_action()
    _scripted("garbage")
    with session_scope(session):
        with patch("evo.agents.action_verifier.LLMInvoker", _Scripted):
            r = run_action_verifier(session, action)
    assert r.validity_score == 0.0
    assert r.supporting_evidence == [] and r.contradicting_evidence == []
    print("  -> OK")


def test_verify_actions_parallel_keeps_all() -> None:
    _h("verify_actions: parallel pool, returns same number of actions")
    session = create_session(load_config())
    actions = [_make_action(aid=f"A{i}") for i in range(1, 4)]
    fake = json.dumps({"validity_score": 0.5, "supporting_evidence": [],
                       "contradicting_evidence": [], "notes": []})
    _scripted(fake, fake, fake)
    with session_scope(session):
        with patch("evo.agents.action_verifier.LLMInvoker", _Scripted):
            out = verify_actions(session, actions)
    assert len(out) == 3
    assert all(a.validity_score == 0.5 for a in out)
    print("  -> OK")


def test_verify_actions_empty_noop() -> None:
    _h("verify_actions: empty list -> empty list (no LLM call)")
    session = create_session(load_config())
    with session_scope(session):
        out = verify_actions(session, [])
    assert out == []
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
        test_clamp_keeps_in_range,
        test_gather_handles_returns_full_records,
        test_run_verifier_fills_action_fields,
        test_run_verifier_handles_parse_failure,
        test_verify_actions_parallel_keeps_all,
        test_verify_actions_empty_noop,
    ])


if __name__ == "__main__":
    sys.exit(main())

"""Phase 6 tests for Critic + revise loop.

Run: PYTHONPATH=. .venv/bin/python -m evo.tests.test_critic
"""

from __future__ import annotations

import json
import sys
import traceback
from typing import Callable
from unittest.mock import patch

from evo.agents.critic import CriticVerdict, _gather_evidence, run_critic
from evo.conductor.world_model import Finding
from evo.runtime.config import load_config
from evo.runtime.session import create_session, session_scope


def _h(name: str) -> None:
    print(f"\n=== {name} ===")


def _seed_session_with_finding():
    session = create_session(load_config())
    f = Finding(id="F001", hypothesis_id="H1", claim="reranker drops gt",
                verdict="confirmed", confidence=0.85,
                evidence_handles=["h_0001"], critic_status="pending")
    session.world_store.update(lambda w: w.findings.append(f))
    return session, f


def test_gather_evidence_pulls_raw_handles() -> None:
    _h("_gather_evidence: returns full handle records")
    session, f = _seed_session_with_finding()
    session.handle_store.append("inspect", {"x": 1}, {"y": 2})
    f2 = Finding(id="F002", hypothesis_id="H2", claim="x", verdict="confirmed",
                 evidence_handles=["h_0001"])
    out = _gather_evidence(session, f2)
    assert len(out) == 1 and out[0]["tool"] == "inspect"
    assert out[0]["result"] == {"y": 2}
    print("  -> OK")


def test_critic_approved_writes_status_and_confidence() -> None:
    _h("run_critic approved: critic_status=approved + approved_confidence overrides")
    session, f = _seed_session_with_finding()
    fake = json.dumps({"verdict": "approved", "challenges": [],
                       "approved_confidence": 0.7})

    class FakeInvoker:
        def __init__(self, *a, **kw): pass
        def invoke(self, user: str, **kwargs) -> str:
            return fake

    with session_scope(session):
        with patch("evo.agents.critic.LLMInvoker", FakeInvoker):
            v = run_critic(session, f)
    assert isinstance(v, CriticVerdict) and v.status == "approved"
    updated = next(x for x in session.world_store.world.findings if x.id == "F001")
    assert updated.critic_status == "approved"
    assert updated.confidence == 0.7
    print("  -> OK")


def test_critic_needs_revision_records_notes() -> None:
    _h("run_critic needs_revision: challenges -> critic_notes")
    session, f = _seed_session_with_finding()
    fake = json.dumps({
        "verdict": "needs_revision",
        "challenges": [
            {"target": "claim", "issue": "n_cases too small", "must_do_one_of": ["x"]},
            {"target": "evidence", "issue": "handle does not say -0.92",
             "must_do_one_of": ["y"]},
        ],
    })

    class FakeInvoker:
        def __init__(self, *a, **kw): pass
        def invoke(self, user: str, **kwargs) -> str:
            return fake

    with session_scope(session):
        with patch("evo.agents.critic.LLMInvoker", FakeInvoker):
            v = run_critic(session, f)
    assert v.status == "needs_revision"
    updated = next(x for x in session.world_store.world.findings if x.id == "F001")
    assert updated.critic_status == "needs_revision"
    assert len(updated.critic_notes) == 2
    assert "n_cases" in updated.critic_notes[0]
    print("  -> OK")


def test_critic_invalid_verdict_falls_back_to_needs_revision() -> None:
    _h("run_critic invalid LLM verdict -> needs_revision")
    session, f = _seed_session_with_finding()
    fake = json.dumps({"verdict": "weird", "challenges": []})

    class FakeInvoker:
        def __init__(self, *a, **kw): pass
        def invoke(self, user: str, **kwargs) -> str:
            return fake

    with session_scope(session):
        with patch("evo.agents.critic.LLMInvoker", FakeInvoker):
            v = run_critic(session, f)
    assert v.status == "needs_revision"
    print("  -> OK")


def test_critic_handles_parse_failure() -> None:
    _h("run_critic non-JSON LLM output -> needs_revision verdict")
    session, f = _seed_session_with_finding()

    class FakeInvoker:
        def __init__(self, *a, **kw): pass
        def invoke(self, user: str, **kwargs) -> str:
            return "not a json"

    with session_scope(session):
        with patch("evo.agents.critic.LLMInvoker", FakeInvoker):
            v = run_critic(session, f)
    assert v.status == "needs_revision"
    assert v.challenges == []
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
        test_gather_evidence_pulls_raw_handles,
        test_critic_approved_writes_status_and_confidence,
        test_critic_needs_revision_records_notes,
        test_critic_invalid_verdict_falls_back_to_needs_revision,
        test_critic_handles_parse_failure,
    ])


if __name__ == "__main__":
    sys.exit(main())

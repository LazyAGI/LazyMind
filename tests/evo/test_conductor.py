"""Phase 7 tests for Conductor + spawner.

Run: PYTHONPATH=. .venv/bin/python -m evo.tests.test_conductor
"""

from __future__ import annotations

import json
import sys
import traceback
from typing import Callable
from unittest.mock import patch

from evo.conductor.conductor import Conductor, ConductorConfig, ConductorRunResult
from evo.conductor.spawner import _dispatch, execute_batch
from evo.conductor.world_model import Finding, Hypothesis
from evo.runtime.config import load_config
from evo.runtime.session import create_session, session_scope


def _h(name: str) -> None:
    print(f"\n=== {name} ===")


def _make_session():
    return create_session(load_config())


def _seed_hypothesis(session, *, hid: str = "H1", category: str = "rerank_failure",
                     status: str = "proposed") -> Hypothesis:
    h = Hypothesis(id=hid, claim=f"claim {hid}", category=category,
                   status=status, source="indexer")
    session.world_store.update(lambda w: w.hypotheses.append(h))
    return h


def _seed_finding(session, *, fid: str = "F1", hid: str = "H1",
                  critic_status: str = "pending") -> Finding:
    f = Finding(id=fid, hypothesis_id=hid, claim="x", verdict="confirmed",
                confidence=0.5, evidence_handles=[], critic_status=critic_status)
    session.world_store.update(lambda w: w.findings.append(f))
    return f


class _ScriptedInvoker:
    def __init__(self, *_, **__): pass

    responses: list[str] = []
    idx: int = 0

    def invoke(self, _: str, **kwargs) -> str:
        if _ScriptedInvoker.idx >= len(_ScriptedInvoker.responses):
            return json.dumps({"actions": [], "done": True})
        out = _ScriptedInvoker.responses[_ScriptedInvoker.idx]
        _ScriptedInvoker.idx += 1
        return out


def _scripted(*responses: str):
    _ScriptedInvoker.responses = list(responses)
    _ScriptedInvoker.idx = 0


def test_conductor_done_immediately_when_llm_says_done() -> None:
    _h("Conductor: LLM done=true on first iteration -> exit")
    session = _make_session()
    _scripted(json.dumps({"actions": [], "done": True}))
    with session_scope(session):
        with patch("evo.conductor.conductor.LLMInvoker", _ScriptedInvoker):
            r = Conductor(session).run()
    assert isinstance(r, ConductorRunResult)
    assert r.iterations == 1 and r.converged is True and r.total_actions == 0
    assert session.world_store.world.status == "converged"
    print("  -> OK")


def test_conductor_exits_when_no_actions_even_if_not_done() -> None:
    _h("Conductor: empty actions exits without burning iterations")
    session = _make_session()
    _scripted(json.dumps({"actions": [], "done": False}))
    with session_scope(session):
        with patch("evo.conductor.conductor.LLMInvoker", _ScriptedInvoker):
            r = Conductor(session, cfg=ConductorConfig(max_iterations=8)).run()
    assert r.iterations == 1 and r.converged is True and r.total_actions == 0
    print("  -> OK")


def test_conductor_max_iterations_cap() -> None:
    _h("Conductor: never-done LLM hits max_iterations cap")
    session = _make_session()
    _seed_hypothesis(session, hid="H1")

    busy = json.dumps({"actions": [{"kind": "research", "hypothesis_id": "H1"}],
                       "done": False})
    _scripted(*[busy] * 20)
    cfg = ConductorConfig(max_iterations=3, max_research_per_hypothesis=20)

    def fake_dispatch(_, action):
        return None

    with session_scope(session):
        with patch("evo.conductor.conductor.LLMInvoker", _ScriptedInvoker):
            with patch("evo.conductor.spawner._dispatch", fake_dispatch):
                r = Conductor(session, cfg=cfg).run()
    assert r.iterations == 3
    assert r.converged is False
    assert session.world_store.world.status == "converged"
    print("  -> OK")


def test_conductor_per_hypothesis_research_cap() -> None:
    _h("Conductor: same hypothesis research cap enforced by _filter")
    session = _make_session()
    _seed_hypothesis(session, hid="H1")
    spam = json.dumps({"actions": [
        {"kind": "research", "hypothesis_id": "H1"},
        {"kind": "research", "hypothesis_id": "H1"},
        {"kind": "research", "hypothesis_id": "H1"},
        {"kind": "research", "hypothesis_id": "H1"},
        {"kind": "research", "hypothesis_id": "H1"},
    ], "done": False})
    _scripted(spam)
    cfg = ConductorConfig(max_iterations=1, max_research_per_hypothesis=2,
                          max_actions_per_batch=10)

    dispatched: list[dict] = []

    def fake_dispatch(_, action):
        dispatched.append(action)
        return None

    with session_scope(session):
        with patch("evo.conductor.conductor.LLMInvoker", _ScriptedInvoker):
            with patch("evo.conductor.spawner._dispatch", fake_dispatch):
                Conductor(session, cfg=cfg).run()
    assert len(dispatched) == 2  # max_research_per_hypothesis 限流
    print("  -> OK")


def test_conductor_max_actions_per_batch() -> None:
    _h("Conductor: oversize action list trimmed to max_actions_per_batch")
    session = _make_session()
    for i in range(1, 11):
        _seed_hypothesis(session, hid=f"H{i}")
    _scripted(json.dumps({
        "actions": [{"kind": "research", "hypothesis_id": f"H{i}"} for i in range(1, 11)],
        "done": False,
    }))
    cfg = ConductorConfig(max_iterations=1, max_actions_per_batch=4,
                          max_research_per_hypothesis=10)
    dispatched: list[dict] = []

    def fake_dispatch(_, action):
        dispatched.append(action)
        return None

    with session_scope(session):
        with patch("evo.conductor.conductor.LLMInvoker", _ScriptedInvoker):
            with patch("evo.conductor.spawner._dispatch", fake_dispatch):
                Conductor(session, cfg=cfg).run()
    assert len(dispatched) == 4
    print("  -> OK")


def test_conductor_handles_parse_failure() -> None:
    _h("Conductor: non-JSON LLM output -> done=true exit")
    session = _make_session()
    _scripted("not json at all")
    with session_scope(session):
        with patch("evo.conductor.conductor.LLMInvoker", _ScriptedInvoker):
            r = Conductor(session).run()
    assert r.converged is True
    print("  -> OK")


def test_conductor_filter_drops_invalid_actions() -> None:
    _h("Conductor: _filter drops non-dict and missing-kind entries")
    session = _make_session()
    _seed_hypothesis(session, hid="H1")
    weird = json.dumps({"actions": [
        "junk_string",
        {"hypothesis_id": "H1"},  # missing kind
        {"kind": "research", "hypothesis_id": "H1"},
    ], "done": False})
    _scripted(weird)
    cfg = ConductorConfig(max_iterations=1, max_actions_per_batch=10,
                          max_research_per_hypothesis=10)
    dispatched: list[dict] = []

    def fake_dispatch(_, action):
        dispatched.append(action)
        return None

    with session_scope(session):
        with patch("evo.conductor.conductor.LLMInvoker", _ScriptedInvoker):
            with patch("evo.conductor.spawner._dispatch", fake_dispatch):
                Conductor(session, cfg=cfg).run()
    assert len(dispatched) == 1
    assert dispatched[0]["kind"] == "research"
    print("  -> OK")


def test_dispatch_routes_kinds() -> None:
    _h("_dispatch: research -> researcher, critic -> critic")
    session = _make_session()
    h = _seed_hypothesis(session, hid="H1")
    f = _seed_finding(session, fid="F1", hid="H1")

    calls: dict[str, str] = {}

    def fake_runner(s, hyp): calls["researcher"] = hyp.id; return "ok-r"
    def fake_critic(s, finding): calls["critic"] = finding.id; return "ok-c"

    with session_scope(session):
        with patch("evo.conductor.spawner.run_researcher", fake_runner):
            with patch("evo.conductor.spawner.run_critic", fake_critic):
                r1 = _dispatch(session, {"kind": "research", "hypothesis_id": "H1"})
                r2 = _dispatch(session, {"kind": "critic", "finding_id": "F1"})
                r3 = _dispatch(session, {"kind": "unknown", "x": 1})
    assert calls == {"researcher": "H1", "critic": "F1"}
    assert r1 == "ok-r" and r2 == "ok-c" and r3 is None
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
        test_conductor_done_immediately_when_llm_says_done,
        test_conductor_exits_when_no_actions_even_if_not_done,
        test_conductor_max_iterations_cap,
        test_conductor_per_hypothesis_research_cap,
        test_conductor_max_actions_per_batch,
        test_conductor_handles_parse_failure,
        test_conductor_filter_drops_invalid_actions,
        test_dispatch_routes_kinds,
    ])


if __name__ == "__main__":
    sys.exit(main())

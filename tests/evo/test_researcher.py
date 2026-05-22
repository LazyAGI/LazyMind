"""Phase 4 tests for Researcher + adapter.

Run: PYTHONPATH=. .venv/bin/python -m evo.tests.test_researcher
"""

from __future__ import annotations

import json
import sys
import traceback
from typing import Callable
from unittest.mock import patch

from evo.agents.researcher import ResearcherOutput, run_researcher
from evo.conductor.category_tools import CATEGORY_TOOLS, tools_for
from evo.conductor.world_model import Hypothesis
from evo.harness import analysis as analysis_steps, data_loader
from evo.runtime.config import load_config
from evo.runtime.session import create_session, session_scope
from evo.tools import register_all


def _h(name: str) -> None:
    print(f"\n=== {name} ===")


def _bootstrap_session():
    register_all()
    session = create_session(load_config())
    with session_scope(session):
        data_loader.load_corpus(session)
        analysis_steps.compute_step_features(session)
        analysis_steps.cluster_global(session, badcase_limit=5,
                                      score_field="answer_correctness")
    return session


def test_tools_for_category_always_includes_recall_handle() -> None:
    _h("tools_for: every category contains recall_handle")
    for cat in CATEGORY_TOOLS:
        assert "recall_handle" in tools_for(cat)
    assert "recall_handle" in tools_for("unknown_xyz")
    print("  -> OK")


def test_tools_for_unknown_category_returns_default() -> None:
    _h("tools_for: unknown category falls back to default set")
    tools = tools_for("totally_made_up")
    assert "summarize_step_metrics" in tools
    assert "summarize_metrics" in tools
    print("  -> OK")


def test_tools_for_dedupes() -> None:
    _h("tools_for: no duplicate tool names in any category")
    for cat in list(CATEGORY_TOOLS) + ["unknown"]:
        tools = tools_for(cat)
        assert len(tools) == len(set(tools)), f"dupes in {cat}: {tools}"
    print("  -> OK")


def test_run_researcher_writes_finding_with_id() -> None:
    _h("run_researcher: parses LLM JSON, writes Finding to WorldModel, returns finding_id")
    session = _bootstrap_session()
    fake = json.dumps({
        "hypothesis_id": "H1",
        "verdict": "confirmed",
        "confidence": 0.9,
        "refined_claim": "ModuleReranker drops GT consistently",
        "evidence_handles": ["h_0007", "h_0008"],
        "suggested_action": "raise top_n from 5 to 10",
        "reasoning": "metric chunk_recall_delta < -0.5 across all cases",
    })

    class FakeReActRunner:
        def __init__(self, *a, **kw):
            self.stats = type("S", (), {"rounds": 3, "tool_calls": {"summarize_step_metrics": 1}})()

        def run(self, task: str) -> str:
            return fake

    h = Hypothesis(id="H1", claim="reranker drops gt", category="rerank_failure",
                   status="proposed", confidence=0.7, source="indexer")
    session.world_store.update(lambda w: w.hypotheses.append(h))
    with session_scope(session):
        with patch("evo.agents.researcher.ReActRunner", FakeReActRunner):
            out = run_researcher(session, h)
    assert isinstance(out, ResearcherOutput)
    assert out.verdict == "confirmed"
    assert out.confidence == 0.9
    assert out.refined_claim.startswith("ModuleReranker")
    assert out.evidence_handles == ["h_0007", "h_0008"]
    assert out.finding_id.startswith("F")
    world = session.world_store.world
    f = next(f for f in world.findings if f.id == out.finding_id)
    assert f.hypothesis_id == "H1"
    assert f.critic_status == "pending"
    target_h = next(hh for hh in world.hypotheses if hh.id == "H1")
    assert target_h.status == "confirmed"
    print(f"  finding_id={out.finding_id}, hypothesis status={target_h.status}")
    print("  -> OK")


def test_run_researcher_handles_invalid_verdict_as_inconclusive() -> None:
    _h("run_researcher: invalid verdict is repaired to inconclusive")
    session = _bootstrap_session()
    session.llm_provider = lambda: (lambda _prompt: json.dumps({
        "hypothesis_id": "H1",
        "verdict": "inconclusive",
        "confidence": 0.5,
        "refined_claim": "x",
        "evidence_handles": [],
        "suggested_action": "?",
        "reasoning": "schema repair normalized invalid verdict",
    }))
    fake = json.dumps({
        "hypothesis_id": "H1", "verdict": "guessed",
        "confidence": 0.5, "refined_claim": "x", "evidence_handles": [],
        "suggested_action": "?", "reasoning": "?",
    })

    class FakeReActRunner:
        def __init__(self, *a, **kw):
            self.stats = type("S", (), {"rounds": 1, "tool_calls": {}})()

        def run(self, task: str) -> str:
            return fake

    h = Hypothesis(id="H1", claim="x", category="rerank_failure",
                   status="proposed", source="indexer")
    session.world_store.update(lambda w: w.hypotheses.append(h))
    with session_scope(session):
        with patch("evo.agents.researcher.ReActRunner", FakeReActRunner):
            out = run_researcher(session, h)
    assert out.verdict == "inconclusive"
    target = next(hh for hh in session.world_store.world.hypotheses if hh.id == "H1")
    assert target.status == "inconclusive"
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
        test_tools_for_category_always_includes_recall_handle,
        test_tools_for_unknown_category_returns_default,
        test_tools_for_dedupes,
        test_run_researcher_writes_finding_with_id,
        test_run_researcher_handles_invalid_verdict_as_inconclusive,
    ])


if __name__ == "__main__":
    sys.exit(main())

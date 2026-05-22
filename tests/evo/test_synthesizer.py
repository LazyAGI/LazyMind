"""Phase 8 tests for Synthesizer + bounded refine.

Run: PYTHONPATH=. .venv/bin/python -m evo.tests.test_synthesizer
"""

from __future__ import annotations

import json
import sys
import traceback
from typing import Callable
from unittest.mock import patch

from evo.agents.synthesizer import (
    SYNTHESIZER_NAME, _to_action, run_synthesizer,
)
from evo.conductor.synthesis import SynthesisResult, VerifiedAction
from evo.conductor.world_model import Finding, Hypothesis
from evo.runtime.config import load_config
from evo.runtime.session import create_session, session_scope


def _h(name: str) -> None:
    print(f"\n=== {name} ===")


def _seed(session, *, hyp_count: int = 1):
    def _apply(world):
        for i in range(hyp_count):
            world.hypotheses.append(Hypothesis(
                id=f"H{i+1}", claim=f"claim {i+1}", category="rerank_failure",
                status="confirmed", confidence=0.85, source="indexer",
            ))
            world.findings.append(Finding(
                id=f"F{i+1}", hypothesis_id=f"H{i+1}", claim=f"finding {i+1}",
                verdict="confirmed", confidence=0.85,
                evidence_handles=[f"h_000{i+1}"], critic_status="approved",
                suggested_action="raise top_n",
            ))
    session.world_store.update(_apply)


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


def _make_action_payload(aid: str = "A1") -> dict:
    return {
        "id": aid, "finding_id": "F1", "hypothesis_id": "H1",
        "hypothesis_category": "rerank_failure",
        "title": "raise reranker top_n",
        "rationale": "F1 evidence shows recall_delta=-0.92",
        "suggested_changes": "set reranker.top_n=10",
        "priority": "P0",
        "expected_impact_metric": "chunk_recall_delta",
        "expected_direction": "+",
        "confidence": 0.85,
        "evidence_handles": ["h_0001"],
        "code_map_target": "/LazyRAG/algorithm/chat/pipelines/naive.py",
    }


def test_synthesizer_single_pass_when_no_gaps() -> None:
    _h("Synthesizer: no gap_hypotheses -> iterations=1")
    session = create_session(load_config())
    _seed(session)
    _scripted(json.dumps({
        "summary": "S", "guidance": "G",
        "actions": [_make_action_payload()], "open_gaps": [],
    }))
    with session_scope(session):
        with patch("evo.agents.synthesizer.LLMInvoker", _Scripted):
            r = run_synthesizer(session)
    assert isinstance(r, SynthesisResult)
    assert r.iterations == 1
    assert len(r.actions) == 1
    assert r.actions[0].id == "A1" and r.actions[0].priority == "P0"
    assert _Scripted.idx == 1
    print("  -> OK")


def test_synthesizer_bounded_refine_when_gaps_present() -> None:
    _h("Synthesizer: gap_hypotheses -> second synthesis pass")
    session = create_session(load_config())
    _seed(session)
    round1 = json.dumps({
        "summary": "draft", "guidance": "draft",
        "actions": [], "open_gaps": [],
        "gap_hypotheses": [
            {"id": "GH1", "claim": "score scale mismatch",
             "category": "score_scale_mismatch",
             "investigation_paths": ["调 inspect_step_for_case"]},
        ],
    })
    round2 = json.dumps({
        "summary": "final", "guidance": "g",
        "actions": [_make_action_payload()], "open_gaps": ["边界情况"],
    })
    _scripted(round1, round2)
    with session_scope(session):
        with patch("evo.agents.synthesizer.LLMInvoker", _Scripted):
            r = run_synthesizer(session)
    assert r.iterations == 2
    assert r.summary == "final"
    assert len(r.actions) == 1
    assert _Scripted.idx == 2
    print("  -> OK")


def test_synthesizer_oversize_gap_hypotheses_triggers_single_refine() -> None:
    _h("Synthesizer: gap_hypotheses oversize -> still only triggers one refine pass")
    session = create_session(load_config())
    _seed(session)
    big = json.dumps({
        "summary": "x", "guidance": "x", "actions": [], "open_gaps": [],
        "gap_hypotheses": [
            {"id": f"GH{i}", "claim": f"c{i}", "category": "rerank_failure",
             "investigation_paths": []}
            for i in range(1, 7)
        ],
    })
    final = json.dumps({"summary": "x", "guidance": "x",
                        "actions": [], "open_gaps": []})
    _scripted(big, final)

    with session_scope(session):
        with patch("evo.agents.synthesizer.LLMInvoker", _Scripted):
            result = run_synthesizer(session)
    new_hids = [h.id for h in session.world_store.world.hypotheses
                if h.source == SYNTHESIZER_NAME]
    assert new_hids == []
    assert result.iterations == 2
    print("  -> OK")


def test_synthesizer_handles_parse_failure() -> None:
    _h("Synthesizer: non-JSON LLM output -> empty SynthesisResult")
    session = create_session(load_config())
    _seed(session)
    _scripted("not a json")
    with session_scope(session):
        with patch("evo.agents.synthesizer.LLMInvoker", _Scripted):
            r = run_synthesizer(session)
    assert r.iterations == 1
    assert r.actions == [] and r.summary == ""
    print("  -> OK")


def test_annotate_with_code_map_demotes_when_target_missing() -> None:
    _h("_annotate_with_code_map: nonexistent target -> demoted to P2 + warning")
    from dataclasses import replace
    from evo.agents.synthesizer import _annotate_with_code_map
    from evo.runtime.code_config import CodeAccessConfig, ReadScope, SubjectIndex

    cfg = load_config()
    cfg = replace(cfg, code_access=CodeAccessConfig(
        code_map={"/tmp/mock.py": "entry"},
        read_scope=ReadScope(), subject_index=SubjectIndex(),
    ))
    session = create_session(cfg)
    a_in = VerifiedAction(
        id="A1", finding_id="F1", hypothesis_id="H1",
        hypothesis_category="rerank_failure",
        title="t", rationale="r",
        suggested_changes="set top_n=10", priority="P0",
        expected_impact_metric="m", expected_direction="+",
        confidence=0.9,
        code_map_target="/tmp/mock.py",
    )
    a_out = VerifiedAction(
        id="A2", finding_id="F1", hypothesis_id="H1",
        hypothesis_category="rerank_failure",
        title="t2", rationale="r2",
        suggested_changes="edit ./formatters/foo.py",
        priority="P0",
        expected_impact_metric="m", expected_direction="+",
        confidence=0.9,
    )
    _annotate_with_code_map([a_in, a_out], session)
    assert a_in.code_map_in_scope and a_in.priority == "P0"
    assert not a_out.code_map_in_scope
    assert a_out.priority == "P2"
    assert "demoted" in a_out.code_map_warning
    assert a_out.verifier_notes == []
    print("  -> OK")


def test_to_action_validates_priority_and_direction() -> None:
    _h("_to_action: invalid priority/direction coerced to safe defaults")
    session = create_session(load_config())
    raw = {
        "id": "A1", "finding_id": "F1", "hypothesis_id": "H1",
        "title": "t", "rationale": "r", "suggested_changes": "x",
        "priority": "P9", "expected_impact_metric": "m",
        "expected_direction": "?", "confidence": "1.5",
        "evidence_handles": ["h_0001"],
        "code_map_target": "/LazyRAG/algorithm/chat/pipelines/naive.py",
    }
    a = _to_action(raw, session)
    assert isinstance(a, VerifiedAction)
    assert a.priority == "P2"
    assert a.expected_direction == "+"
    assert a.confidence == 1.0
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
        test_synthesizer_single_pass_when_no_gaps,
        test_synthesizer_bounded_refine_when_gaps_present,
        test_synthesizer_oversize_gap_hypotheses_triggers_single_refine,
        test_synthesizer_handles_parse_failure,
        test_to_action_validates_priority_and_direction,
        test_annotate_with_code_map_demotes_when_target_missing,
    ])


if __name__ == "__main__":
    sys.exit(main())

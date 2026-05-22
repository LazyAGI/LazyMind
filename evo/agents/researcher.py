from __future__ import annotations
import json
import time
from dataclasses import dataclass, field
from typing import Any
from evo.conductor.category_tools import tools_for
from evo.conductor.prompts import load as load_prompt
from evo.conductor.world_model import Finding, Hypothesis, WorldModel
from evo.harness.analysis import collect_exemplar_ids
from evo.harness.react import LLMInvoker, ReActConfig, ReActRunner
from evo.harness.schemas import SCHEMAS
from evo.harness.structured import invoke_structured
from evo.runtime.code_config import code_context_dict
from evo.runtime.session import AnalysisSession
from evo.utils import coerce_confidence

RESEARCHER_NAME = 'researcher'
_VALID_VERDICTS = ('confirmed', 'refuted', 'inconclusive')


@dataclass
class ResearcherOutput:
    hypothesis_id: str
    verdict: str
    confidence: float
    refined_claim: str
    finding_id: str = ''
    evidence_handles: list[str] = field(default_factory=list)
    suggested_action: str = ''
    reasoning: str = ''
    rounds: int = 0
    tool_calls: dict[str, int] = field(default_factory=dict)


def _seed_case_ids(session: AnalysisSession, max_ids: int = 10) -> list[dict[str, Any]]:
    score_field = session.config.badcase_score_field
    ids = collect_exemplar_ids(session, max_ids=max_ids) or list(session.parsed_judge)[:max_ids]
    seeds: list[dict[str, Any]] = []
    for did in ids:
        j = session.get_judge(did)
        if j is None:
            continue
        trace = session.get_trace(j.trace_id)
        seeds.append(
            {
                'dataset_id': did,
                'score': getattr(j, score_field, None),
                'query_preview': (trace.query if trace else '')[:80],
            }
        )
    return seeds


def _build_world_snapshot(session: AnalysisSession, target: Hypothesis) -> dict[str, Any]:
    w = session.world_store.world if session.world_store else None
    snap: dict[str, Any] = {
        'pipeline': list(session.trace_meta.pipeline),
        'total_cases': len(session.parsed_judge),
        'seed_case_ids': _seed_case_ids(session),
        'target': {
            'id': target.id,
            'claim': target.claim,
            'category': target.category,
            'prior_confidence': target.confidence,
            'investigation_paths': list(target.investigation_paths),
        },
        'other_hypotheses': [
            {'id': h.id, 'claim': h.claim, 'category': h.category, 'status': h.status}
            for h in (w.hypotheses if w else [])
            if h.id != target.id
        ][:10],
        'existing_findings': [
            {'id': f.id, 'hypothesis_id': f.hypothesis_id, 'verdict': f.verdict, 'critic_status': f.critic_status}
            for f in (w.findings if w else [])
        ][:10],
    }
    snap['code_context'] = code_context_dict(session.config.code_access)
    return snap


def _build_task(snapshot: dict[str, Any]) -> str:
    target = snapshot['target']
    ctx = {k: v for (k, v) in snapshot.items() if k != 'target'}
    return (
        f'## 待调查假设\n{json.dumps(target, ensure_ascii=False, indent=2)}\n\n'
        f'## 上下文\n{json.dumps(ctx, ensure_ascii=False, indent=2)}\n\n'
        '请按工作流验证或推翻该假设，最终输出严格 JSON。'
    )


def run_researcher(
    session: AnalysisSession, hypothesis: Hypothesis, *, llm: Any | None = None, max_rounds: int = 8
) -> ResearcherOutput:
    t_start = time.monotonic()
    snapshot = _build_world_snapshot(session, hypothesis)
    invoker = LLMInvoker(session=session, system_prompt=load_prompt('researcher'), llm=llm)
    runner = ReActRunner(
        session=session,
        tool_names=list(tools_for(hypothesis.category)),
        invoker=invoker,
        agent=f'{RESEARCHER_NAME}:{hypothesis.id}',
        cfg=ReActConfig(max_rounds=max_rounds, min_tool_calls=1, required_tools=(), use_memory_curator=True),
        logger=session.logger(f'react.{RESEARCHER_NAME}.{hypothesis.id}'),
    )
    try:
        parsed = invoke_structured(
            session,
            invoker,
            _build_task(snapshot),
            agent=f'{RESEARCHER_NAME}:{hypothesis.id}',
            schema=SCHEMAS['researcher'],
            producer=lambda task: runner.run(task),
            max_repair=2,
        )
    except Exception as exc:
        final = {
            'hypothesis_id': hypothesis.id,
            'verdict': 'inconclusive',
            'confidence': 0.0,
            'refined_claim': hypothesis.claim,
            'evidence_handles': [],
            'suggested_action': '检查 researcher 输出格式或重试该假设。',
            'reasoning': f'researcher failed before a valid final result: {type(exc).__name__}: {exc}',
        }
        session.telemetry.emit(
            'researcher.reasoning_summary',
            actor=f'{RESEARCHER_NAME}:{hypothesis.id}',
            rounds=runner.stats.rounds,
            tool_calls=dict(runner.stats.tool_calls),
            final_answer=json.dumps(final, ensure_ascii=False),
            error=str(exc),
        )
        raise
    verdict = str(parsed.get('verdict', 'inconclusive'))
    if verdict not in _VALID_VERDICTS:
        verdict = 'inconclusive'
    out = ResearcherOutput(
        hypothesis_id=hypothesis.id,
        verdict=verdict,
        confidence=coerce_confidence(parsed.get('confidence'), default=0.0),
        refined_claim=str(parsed.get('refined_claim') or hypothesis.claim),
        evidence_handles=[str(h) for h in parsed.get('evidence_handles', [])],
        suggested_action=str(parsed.get('suggested_action', '')),
        reasoning=str(parsed.get('reasoning', '')),
        rounds=runner.stats.rounds,
        tool_calls=dict(runner.stats.tool_calls),
    )
    if session.world_store is not None:
        finding_id = _record_finding(session, hypothesis, out)
        out.finding_id = finding_id
    session.telemetry.emit(
        'agent_run',
        agent=f'{RESEARCHER_NAME}:{hypothesis.id}',
        perspective=RESEARCHER_NAME,
        hypothesis_id=hypothesis.id,
        finding_id=out.finding_id,
        verdict=out.verdict,
        confidence=out.confidence,
        evidence_count=len(out.evidence_handles),
        rounds=out.rounds,
        tool_call_counts=out.tool_calls,
        elapsed_s=round(time.monotonic() - t_start, 4),
    )
    return out


def _record_finding(session: AnalysisSession, hypothesis: Hypothesis, out: ResearcherOutput) -> str:
    box: dict[str, str] = {}

    def _apply(world: WorldModel) -> None:
        fid = f'F{len(world.findings) + 1:03d}'
        world.findings.append(
            Finding(
                id=fid,
                hypothesis_id=hypothesis.id,
                claim=out.refined_claim,
                verdict=out.verdict,
                confidence=out.confidence,
                evidence_handles=list(out.evidence_handles),
                critic_status='pending',
                critic_notes=[],
                suggested_action=out.suggested_action,
            )
        )
        for h in world.hypotheses:
            if h.id == hypothesis.id:
                h.status = out.verdict
                if out.evidence_handles:
                    h.evidence_handles = list(out.evidence_handles)
                break
        box['fid'] = fid

    session.world_store.update(_apply)
    return box.get('fid', '')

from __future__ import annotations

import hashlib
from collections.abc import Mapping
from typing import Any

from evo.artifact_flow.contract import STEP_ROOTS
from evo.artifact_runtime.utils import canonical_json
from evo.operations.eval import ANSWER_METRICS, answer_score_from_metrics

from .models import AutoObservation
from .ports import AutoAgentPorts

OBSERVED_ARTIFACTS = tuple(key.artifact_id for key in STEP_ROOTS.values())


class AutoObserver:
    def __init__(self, ports: AutoAgentPorts) -> None:
        self.ports = ports

    def observe(self, thread_id: str) -> AutoObservation:
        meta = self.ports.get_thread(thread_id)
        status = self.ports.flow_status(thread_id)
        artifacts = {artifact_id: self.ports.artifact(thread_id, artifact_id) for artifact_id in OBSERVED_ARTIFACTS}
        latest_refs = {
            artifact_id: str(row.get('ref') or '')
            for artifact_id, row in artifacts.items()
            if row is not None and row.get('ref')
        }
        facts = _facts_from_artifacts(artifacts)
        approval = self.ports.active_approval(thread_id)
        payload = {
            'thread_id': thread_id,
            'mode': str(meta.get('mode') or 'interactive'),
            'status': str(status.get('status') or 'idle'),
            'current_step': str(status.get('current_step') or ''),
            'completed_steps': tuple(str(item) for item in status.get('completed_steps') or ()),
            'stale_steps': tuple(str(item) for item in status.get('stale_steps') or ()),
            'pending_checkpoint': (
                status.get('pending_checkpoint')
                if isinstance(status.get('pending_checkpoint'), dict)
                else None
            ),
            'latest_refs': latest_refs,
            'facts': facts,
            'active_approval': None if approval is None else approval.model_dump(),
        }
        return AutoObservation(**payload, hash=hashlib.sha256(canonical_json(payload).encode('utf-8')).hexdigest())


def _facts_from_artifacts(artifacts: Mapping[str, dict[str, Any] | None]) -> dict[str, Any]:
    data = {
        artifact_id: row.get('data')
        for artifact_id, row in artifacts.items()
        if isinstance(row, Mapping) and isinstance(row.get('data'), Mapping)
    }
    eval_summary = data.get('eval.summary', {})
    analysis = data.get('analysis.summary', {})
    return {
        'execution_failures': _execution_failures(eval_summary),
        'bad_cases': _bad_cases(eval_summary),
        'suspicious_scores': _suspicious_scores(eval_summary),
        'repairable_cases': list(analysis.get('repairable_cases') or []) if isinstance(analysis, Mapping) else [],
    }


def _execution_failures(summary: Mapping[str, Any]) -> list[dict[str, str]]:
    failures = []
    for item in summary.get('execution_failures') or []:
        if isinstance(item, Mapping):
            case_id = str(item.get('case_id') or '').strip()
            if case_id:
                failures.append({'case_id': case_id, 'reason': str(item.get('reason') or '')})
    return failures


def _bad_cases(summary: Mapping[str, Any]) -> list[dict[str, str]]:
    cases = []
    for item in summary.get('bad_cases') or []:
        if isinstance(item, Mapping):
            case_id = str(item.get('case_id') or '').strip()
            if case_id:
                cases.append({
                    'case_id': case_id,
                    'failure_type': str(item.get('failure_type') or ''),
                    'quality_label': str(item.get('quality_label') or ''),
                    'reason': str(item.get('reason') or ''),
                })
    return cases


def _suspicious_scores(summary: Mapping[str, Any]) -> list[dict[str, Any]]:
    out = []
    rows = summary.get('rows') if isinstance(summary.get('rows'), list) else []
    for row in rows:
        if not isinstance(row, Mapping):
            continue
        case_id = str(row.get('case_id') or '').strip()
        if not case_id or row.get('answer_score') is None or any(row.get(key) is None for key in ANSWER_METRICS):
            continue
        try:
            answer_score = float(row['answer_score'])
            component_scores = {key: float(row[key]) for key in ANSWER_METRICS}
            if any(value < 0.0 or value > 1.0 for value in component_scores.values()):
                continue
            expected = answer_score_from_metrics(component_scores)
        except (TypeError, ValueError):
            continue
        if 0.0 <= answer_score <= 1.0 and abs(answer_score - expected) > 0.05:
            out.append({
                'case_id': case_id,
                'field': 'answer_score',
                'current': answer_score,
                'suggested': expected,
                'reason': 'answer_score differs from weighted component metrics',
            })
    return out

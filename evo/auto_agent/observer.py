from __future__ import annotations

import hashlib
from collections.abc import Mapping
from typing import Any

from evo.artifact_flow.contract import STEP_ROOTS
from evo.artifact_runtime.utils import canonical_json

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
    facts: dict[str, list[dict[str, Any]]] = {
        'artifact_anomalies': [],
        'intervention_suggestions': [],
    }
    for artifact_id, row in artifacts.items():
        if not isinstance(row, Mapping) or not isinstance(row.get('data'), Mapping):
            continue
        payload = row['data']
        source_ref = str(row.get('ref') or artifact_id)
        source = {'source_artifact': str(artifact_id), 'source_ref': source_ref}
        if str(payload.get('status') or '').lower() in {'failed', 'error'}:
            facts['artifact_anomalies'].append({
                **source,
                'kind': 'status',
                'reason': str(payload.get('reason') or payload.get('error') or payload.get('status') or ''),
            })
        for key in ('errors', 'execution_failures'):
            rows = payload.get(key)
            if isinstance(rows, list) and rows:
                facts['artifact_anomalies'].append({
                    **source,
                    'kind': key,
                    'reason': f'{key} present',
                })
        for item in payload.get('intervention_suggestions') or ():
            if not isinstance(item, Mapping):
                continue
            kind = str(item.get('kind') or '').strip()
            args = item.get('args')
            if kind and isinstance(args, Mapping):
                facts['intervention_suggestions'].append({
                    **source,
                    'kind': kind,
                    'args': dict(args),
                    'reason': str(item.get('reason') or ''),
                })
    return facts

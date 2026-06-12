from __future__ import annotations

import re
from dataclasses import dataclass
from typing import Any

from ..artifacts import ArtifactRef
from ..internal_ids import is_synthetic_operation, stage_group
from ..operations import OperationGraph
from ..store import EvoStore


@dataclass(frozen=True)
class IntentConversationContextBuilder:
    store: EvoStore
    graph: OperationGraph

    def build(self, run_id: str) -> dict[str, Any]:
        artifacts = self.store.artifact_graph(run_id)
        eval_dataset = _latest_payload(artifacts, 'eval_dataset')
        eval_report = _latest_payload(artifacts, 'eval_report')
        return {
            'run_id': run_id,
            'current_stage': self._current_stage(),
            'active_operations': self._active_operations(),
            'failed_operations': self._failed_operations(),
            'case_context': _case_context(eval_dataset, eval_report),
            'recent_user_messages': self._recent_user_messages(artifacts),
            'last_mentioned_case_ids': self._last_mentioned_case_ids(artifacts),
        }

    def _current_stage(self) -> str:
        for ref in reversed(self.graph.run_refs({'running', 'checkpointed'})):
            if is_synthetic_operation(str(ref)): continue
            run = self.graph.get_run(ref)
            return stage_group(run.spec.flow_tag) or stage_group(run.spec.stage_tag)
        failed = self._failed_operations()
        return str(failed[-1].get('stage') or '') if failed else ''

    def _active_operations(self) -> list[dict[str, str]]:
        out = []
        for ref in self.graph.run_refs({'pending', 'running', 'checkpointed'}):
            if is_synthetic_operation(str(ref)): continue
            run = self.graph.get_run(ref)
            out.append({'operation_run_id': str(ref), 'status': run.status,
                        'stage': stage_group(run.spec.flow_tag) or stage_group(run.spec.stage_tag)})
        return out[-20:]

    def _failed_operations(self) -> list[dict[str, str]]:
        out = []
        for ref in self.graph.run_refs():
            if is_synthetic_operation(str(ref)): continue
            run = self.graph.get_run(ref)
            if run.status == 'ended' and run.outcome == 'failed':
                out.append({'operation_run_id': str(ref),
                            'stage': stage_group(run.spec.flow_tag) or stage_group(run.spec.stage_tag)})
        return out[-20:]

    def _recent_user_messages(self, artifacts: Any) -> list[dict[str, str]]:
        messages = []
        for ref in _latest_refs(artifacts, 'user_message_')[-8:]:
            payload = artifacts.get(ref)
            messages.append({'message_id': str(payload.get('message_id') or ''),
                             'message': str(payload.get('message') or '')})
        return messages

    def _last_mentioned_case_ids(self, artifacts: Any) -> list[str]:
        for message in reversed(self._recent_user_messages(artifacts)):
            ids = _case_ids_from_text(message['message'])
            if ids: return ids
        return []


def _case_context(eval_dataset: dict[str, Any], eval_report: dict[str, Any]) -> dict[str, Any]:
    bad = [str(row.get('case_id')) for row in eval_report.get('bad_cases') or [] if row.get('case_id')]
    execution_failed = [
        str(row.get('case_id')) for row in eval_report.get('execution_failures') or [] if row.get('case_id')
    ]
    current = [str(item) for item in eval_dataset.get('case_ids') or []]
    return {
        'current_case_ids': current,
        'bad_case_ids': bad,
        'execution_failed_case_ids': execution_failed,
        'last_failed_case_ids': list(dict.fromkeys([*execution_failed, *bad])),
    }


def _latest_payload(artifacts: Any, artifact_id: str) -> dict[str, Any]:
    try:
        payload = artifacts.get(artifacts.latest_ref(artifact_id))
    except KeyError:
        return {}
    return payload if isinstance(payload, dict) else {}


def _latest_refs(artifacts: Any, prefix: str) -> list[ArtifactRef]:
    refs = []
    for manifest_path in sorted(artifacts.manifest_dir.glob(f'{prefix}*.json')):
        try:
            refs.append(artifacts.latest_ref(manifest_path.stem))
        except KeyError:
            pass
    return refs


def _case_ids_from_text(text: str) -> list[str]:
    ids = []
    for raw in re.findall(r'case[_-]?(\d{1,4})', text, flags=re.I):
        ids.append(f'case_{int(raw):04d}')
    for raw in re.findall(r'第\s*(\d{1,4})\s*(?:条|个)?', text):
        ids.append(f'case_{int(raw):04d}')
    return list(dict.fromkeys(ids))

from __future__ import annotations

import threading
import time
from dataclasses import asdict
from datetime import datetime, timezone
from typing import Any

from ..store import EvoStore
from .models import CallView, OperationView, PipelineStageView, PipelineView


class ProjectionBuilder:
    def __init__(self, store: EvoStore):
        self.store = store

    def build_pipeline_view(self, run_id: str) -> PipelineView:
        groups: dict[tuple[str, str], list[dict[str, Any]]] = {}
        for operation in _operations(self.store, run_id):
            flow = operation.get('flow_tag') or operation.get('flow') or 'default'
            stage = operation.get('stage_tag') or operation.get('stage') or 'default'
            groups.setdefault((flow, stage), []).append(operation)
        stages = [
            PipelineStageView(
                flow=flow, stage=stage, total=len(items),
                ended=sum(item.get('status') == 'ended' for item in items),
                running=sum(item.get('status') == 'running' for item in items),
                checkpointed=sum(item.get('status') == 'checkpointed' for item in items),
                pending=sum(item.get('status', 'pending') == 'pending' for item in items),
            )
            for (flow, stage), items in sorted(groups.items())
        ]
        return PipelineView(run_id, stages)

    def build_operation_view(self, run_id: str) -> OperationView:
        operations = _operations(self.store, run_id)
        active = [item for item in operations if item.get('status') in {'running', 'checkpointed'}]
        return OperationView(run_id=run_id, active_operations=active, operations=operations,
                             history=self.store.operation_history(run_id))

    def build_call_view(self, run_id: str, operation_run_id: str) -> CallView:
        calls = [_call_summary(asdict(call)) for call in self.store.read_calls(run_id, operation_run_id)]
        return CallView(run_id, operation_run_id, calls)

    def rebuild_all(self, run_id: str) -> dict[str, Any]:
        projection_dir = self.store.run_dir(run_id) / 'projections'
        projection_dir.mkdir(parents=True, exist_ok=True)
        current = rebuild_frontend_state(self.store, run_id, write=False)
        pipeline = asdict(self.build_pipeline_view(run_id))
        operations = asdict(self.build_operation_view(run_id))
        self.store.atomic_write_json(projection_dir / 'current.json', current)
        self.store.atomic_write_json(projection_dir / 'pipeline.json', pipeline)
        self.store.atomic_write_json(projection_dir / 'operations.json', operations)
        return current


_THROTTLE_INTERVAL_S = 2.0
_throttle_lock = threading.Lock()
_last_rebuild_at: dict[str, float] = {}


def rebuild_frontend_state_throttled(store: EvoStore, run_id: str) -> None:
    """Skip rebuilds that land within the throttle window; state-bearing writers rebuild immediately."""
    key = str(store.run_dir(run_id))
    now = time.monotonic()
    with _throttle_lock:
        if now - _last_rebuild_at.get(key, 0.0) < _THROTTLE_INTERVAL_S: return
        _last_rebuild_at[key] = now
    rebuild_frontend_state(store, run_id)


def rebuild_frontend_state(store: EvoStore, run_id: str, *, write: bool = True) -> dict[str, Any]:
    projection_dir = store.run_dir(run_id) / 'projections'
    projection_dir.mkdir(parents=True, exist_ok=True)
    run_path = store.run_dir(run_id) / 'run.json'
    run = store.read_json(run_path) if run_path.exists() else {'run_id': run_id, 'status': 'running'}
    operations = _operations(store, run_id)
    current = {
        'built_at': datetime.now(timezone.utc).isoformat(),
        'source_event_count': _event_line_count(store, run_id),
        'run': {**run, 'can_dispatch': _can_dispatch(run)},
        'operations': operations,
        'progress': {operation['operation_run_id']: operation['progress'] for operation in operations
                     if operation.get('operation_run_id') and operation.get('progress')},
        'blockers': run.get('blocked_operations', []),
        'latest_artifacts': _latest_artifacts(store, run_id),
    }
    if write:
        store.atomic_write_json(projection_dir / 'current.json', current)
    return current


def _event_line_count(store: EvoStore, run_id: str) -> int:
    path = store.run_dir(run_id) / 'events.jsonl'
    if not path.exists(): return 0
    with path.open('rb') as handle:
        return sum(1 for line in handle if line.strip())


def _operations(store: EvoStore, run_id: str) -> list[dict[str, Any]]:
    return [_operation_for_view(operation) for operation in store.list_operations(run_id)]


def _operation_for_view(operation: dict[str, Any]) -> dict[str, Any]:
    operation = dict(operation)
    progress = operation.get('progress')
    if not isinstance(progress, dict): return operation
    progress = dict(progress)
    status = operation.get('status', '')
    if status == 'ended': progress['status'] = operation.get('outcome') or 'ended'
    elif status and 'status' in progress: progress['status'] = status
    operation['progress'] = progress
    return operation


def _can_dispatch(run: dict[str, Any]) -> bool:
    return run.get('status') == 'running' and not any(
        run.get(key) for key in ('dispatch_block_reason', 'blocked_operations', 'root_blockers', 'impacted_blockers')
    )


def _latest_artifacts(store: EvoStore, run_id: str) -> dict[str, str]:
    manifest_dir = store.run_dir(run_id) / 'artifacts' / 'manifests'
    if not manifest_dir.exists(): return {}
    latest: dict[str, str] = {}
    for manifest_path in sorted(manifest_dir.glob('*.json')):
        manifest = store.read_json(manifest_path)
        artifact_id = manifest.get('artifact_id', manifest_path.stem)
        version = int(manifest.get('latest_version', 0))
        version_meta = next((item for item in manifest.get('versions', [])
                             if int(item.get('version', 0)) == version), {})
        if version and version_meta.get('role', 'operation_output') == 'operation_output':
            latest[artifact_id] = f'{artifact_id}@v{version}'
    return latest


def _call_summary(record: dict[str, Any]) -> dict[str, Any]:
    return {
        'sequence': int(record.get('sequence') or 0),
        'call_id': record.get('call_id', ''),
        'adapter_type': record.get('adapter_type', ''),
        'operation_run_id': record.get('operation_run_id', ''),
        'phase': record.get('phase', ''),
        'item_ref': record.get('item_ref', ''),
        'status': record.get('status', ''),
        'reused': bool(record.get('reused', False)),
        'reused_from_call_id': record.get('reused_from_call_id', ''),
        'reused_from_operation_run_id': record.get('reused_from_operation_run_id', ''),
        'request_ref': record.get('request_ref', ''),
        'response_ref': record.get('response_ref', ''),
        'record_ref': record.get('record_ref', ''),
        'error': record.get('error'),
    }

from __future__ import annotations

import uuid

from ..artifacts import ArtifactRef, SnapshotRef
from ..operations import OperationRunRef
from ..store import Event, EvoStore
from .models import CheckpointRef


class CheckpointManager:
    def __init__(self, store: EvoStore):
        self.store = store

    def create_checkpoint(
        self,
        run_id: str,
        operation_ref: OperationRunRef | None,
        summary: str,
        *,
        allowed_capabilities: list[str] | None = None,
        resume_operations: list[OperationRunRef] | None = None,
    ) -> CheckpointRef:
        checkpoint = CheckpointRef(f'ckpt_{uuid.uuid4().hex[:12]}')
        artifact_refs = self._active_artifact_refs(run_id)
        snapshot = self.store.artifact_graph(run_id).create_snapshot(artifact_refs)
        data = {
            'checkpoint_id': checkpoint.checkpoint_id,
            'summary': summary,
            'current_operation': str(operation_ref) if operation_ref else '',
            'snapshot_id': snapshot.snapshot_id,
            'artifact_refs': {key: str(ref) for key, ref in artifact_refs.items()},
            'allowed_capabilities': list(allowed_capabilities or []),
            'resume_operations': [str(ref) for ref in resume_operations or []],
        }
        self.store.write_checkpoint(run_id, checkpoint.checkpoint_id, data)
        self.store.append_event(Event('checkpoint.created', run_id, {'checkpoint_id': checkpoint.checkpoint_id}))
        return checkpoint

    def snapshot_for_checkpoint(self, run_id: str, checkpoint_ref: CheckpointRef) -> SnapshotRef:
        data = self._read_checkpoint(run_id, checkpoint_ref)
        return SnapshotRef(data['snapshot_id'])

    def allowed_capabilities(self, run_id: str, checkpoint_ref: CheckpointRef) -> list[str]:
        return list(self._read_checkpoint(run_id, checkpoint_ref).get('allowed_capabilities', []))

    def resume_operations(self, run_id: str, checkpoint_ref: CheckpointRef) -> list[OperationRunRef]:
        return [OperationRunRef(value) for value in self._read_checkpoint(run_id, checkpoint_ref).get('resume_operations', [])]

    def _active_artifact_refs(self, run_id: str) -> dict[str, ArtifactRef]:
        artifact_graph = self.store.artifact_graph(run_id)
        refs: dict[str, ArtifactRef] = {}
        for manifest_path in sorted(artifact_graph.manifest_dir.glob('*.json')):
            manifest = self.store.read_json(manifest_path)
            refs[manifest['artifact_id']] = ArtifactRef(manifest['artifact_id'], int(manifest['latest_version']))
        return refs

    def _read_checkpoint(self, run_id: str, checkpoint_ref: CheckpointRef) -> dict:
        return self.store.read_json(self.store.run_dir(run_id) / 'checkpoints' / f'{checkpoint_ref.checkpoint_id}.json')

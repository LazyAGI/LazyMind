from __future__ import annotations

import uuid
from dataclasses import dataclass, field
from typing import Any, Callable

from .. import validate_id
from ..artifacts import ArtifactDraft, ArtifactRef
from ..operations import OperationRunRef
from ..store import Event, EvoStore, StoreRunLifecycle
from .models import CheckpointRef, RESUME_FROM_SNAPSHOT, RESUME_WITH_INTERVENTIONS, ResumeInputPolicy


@dataclass(frozen=True)
class CheckpointState:
    checkpoint_id: str
    dispatch_block_reason: str
    checkpoint_kind: str = ''
    message: str = ''
    stage: str = ''
    next_stage: str = ''
    blocked_operations: tuple[str, ...] = ()
    next_operations: tuple[str, ...] = ()
    capability_id: str = ''
    message_id: str = ''
    next_op: str = ''
    detail: dict[str, Any] = field(default_factory=dict)

    @property
    def is_intent_confirmation(self) -> bool:
        return self.checkpoint_kind == 'intent_confirmation'

    @property
    def is_manual_cutover(self) -> bool:
        return self.checkpoint_kind == 'manual_cutover'

    def frontend_payload(self) -> dict[str, Any]:
        return {
            'checkpoint_id': self.checkpoint_id, 'stage': self.stage, 'next_stage': self.next_stage,
            'message': self.message or 'waiting for checkpoint', 'checkpoint_kind': self.checkpoint_kind,
            'blocked_operations': list(self.blocked_operations),
            'next_operations': list(self.next_operations or self.blocked_operations),
            'capability_id': self.capability_id,
            'next_op': {'op': self.next_op} if self.next_op else {},
            'detail': dict(self.detail),
        }


class CheckpointManager:
    def __init__(self, store: EvoStore):
        self.store = store

    def create_checkpoint(self, run_id: str, operation_ref: OperationRunRef | None, summary: str, *,
                          allowed_capabilities: list[str] | None = None,
                          next_operations: list[OperationRunRef] | None = None) -> CheckpointRef:
        checkpoint = CheckpointRef(f'ckpt_{uuid.uuid4().hex[:12]}')
        artifact_refs = self._active_artifact_refs(run_id)
        snapshot = self.store.artifact_graph(run_id).create_snapshot(artifact_refs)
        next_operation_ids = [str(ref) for ref in next_operations or []]
        data = {
            'checkpoint_id': checkpoint.checkpoint_id, 'summary': summary,
            'current_operation': str(operation_ref) if operation_ref else '', 'snapshot_id': snapshot.snapshot_id,
            'artifact_refs': {key: str(ref) for key, ref in artifact_refs.items()},
            'allowed_capabilities': list(allowed_capabilities or []), 'next_operations': next_operation_ids,
        }
        self.store.write_checkpoint(run_id, checkpoint.checkpoint_id, data)
        self.store.append_event(Event('checkpoint.created', run_id, {
            'checkpoint_id': checkpoint.checkpoint_id, 'next_operations': next_operation_ids}))
        return checkpoint

    def resume_operations(self, run_id: str, checkpoint_ref: CheckpointRef) -> list[OperationRunRef]:
        path = self.store.run_dir(run_id) / 'checkpoints' / f'{checkpoint_ref.checkpoint_id}.json'
        return [OperationRunRef(value) for value in self.store.read_json(path).get('next_operations') or []]

    def resume_operation_runs(
        self, run_id: str, operation_graph: Any, operation_refs: list[OperationRunRef], *, checkpoint_id: str,
        input_policy: ResumeInputPolicy, old_refs_for: Callable[[OperationRunRef], list[ArtifactRef]],
        resume_context: dict[str, Any] | None = None,
    ) -> ArtifactRef:
        if input_policy not in {RESUME_FROM_SNAPSHOT, RESUME_WITH_INTERVENTIONS}:
            raise ValueError(f'unsupported checkpoint resume input policy: {input_policy}')
        rebound: dict[str, list[dict[str, str]]] = {}
        for operation_ref in operation_refs:
            replacements = (self.adopted_replacements(run_id, old_refs_for(operation_ref))
                            if input_policy == RESUME_WITH_INTERVENTIONS else {})
            changes = operation_graph.rebind_input_refs(operation_ref, replacements)
            if changes: rebound[str(operation_ref)] = list(changes.values())
            operation_graph.reset_run(operation_ref)
        return self.record_resume(run_id, checkpoint_id, input_policy=input_policy, next_operations=operation_refs,
                                  rebound_input_refs=rebound, resume_context=resume_context)

    def record_wait(self, run_id: str, payload: dict[str, Any]) -> None:
        self.store.append_event(Event('checkpoint.wait', run_id, dict(payload)))

    def record_wait_and_block(self, run_id: str, payload: dict[str, Any], *, block_reason: str,
                              block_payload: dict[str, Any]) -> None:
        self.record_wait(run_id, payload)
        StoreRunLifecycle(self.store, run_id).block_dispatch(block_reason, **block_payload)

    def record_stage_wait(self, run_id: str, checkpoint_id: str, *, stage: str, next_stage: str, message: str,
                          checkpoint_kind: str, next_op: str, detail: dict[str, Any]) -> CheckpointState:
        payload = {
            'checkpoint_id': validate_id(checkpoint_id, 'checkpoint_id'),
            'stage': str(stage or ''), 'completed_stage': str(stage or ''), 'completed_flow': str(stage or ''),
            'next_stage': str(next_stage or ''), 'next_op': {'op': str(next_op or '')}, 'message': str(message or ''),
            'detail': dict(detail), 'checkpoint_kind': str(checkpoint_kind or 'stage_gate')}
        confirmation = payload['checkpoint_kind'] == 'intent_confirmation'
        self.record_wait_and_block(run_id, payload,
                                   block_reason='confirmation_required' if confirmation else 'checkpoint_wait',
                                   block_payload=_lifecycle_payload(payload))
        state = self.active_checkpoint(run_id)
        if state is None: raise RuntimeError(f'checkpoint {checkpoint_id} was not written to run lifecycle')
        return state

    def block_intent_confirmation(self, run_id: str, *, checkpoint_id: str, operation_refs: list[str],
                                  capability_id: str, message_id: str, as_child: bool) -> None:
        payload = _lifecycle_payload({
            'checkpoint_id': validate_id(checkpoint_id, 'checkpoint_id'),
            'checkpoint_kind': 'intent_confirmation', 'message': 'operation requires confirmation',
            'blocked_operations': [str(ref) for ref in operation_refs],
            'next_operations': [str(ref) for ref in operation_refs],
            'capability_id': str(capability_id or ''), 'message_id': str(message_id or ''),
        })
        lifecycle = StoreRunLifecycle(self.store, run_id)
        if as_child: lifecycle.push_child_dispatch('confirmation_required', **payload)
        else: lifecycle.block_dispatch('confirmation_required', **payload)

    def record_cancel(self, run_id: str, payload: dict[str, Any]) -> None:
        data = dict(payload)
        data['checkpoint_id'] = validate_id(str(data.get('checkpoint_id') or ''), 'checkpoint_id')
        self.store.append_event(Event('checkpoint.cancel', run_id, data))

    def cancel_active(self, run_id: str, **payload: Any) -> None:
        for checkpoint_id in self.active_checkpoint_ids(run_id):
            self.record_cancel(run_id, {**payload, 'checkpoint_id': checkpoint_id})

    def open_dispatch(self, run_id: str, **extra: Any) -> None:
        self._assert_active_checkpoint_closed(run_id, allow_cancel=True)
        StoreRunLifecycle(self.store, run_id).open_dispatch(checkpoint_close_verified=True, **extra)

    def restore_parent_dispatch(self, run_id: str, **extra: Any) -> bool:
        self._assert_current_checkpoint_closed(run_id, allow_cancel=True)
        return StoreRunLifecycle(self.store, run_id).restore_parent_dispatch(checkpoint_close_verified=True, **extra)

    def active_checkpoint(self, run_id: str) -> CheckpointState | None:
        path = self.store.run_dir(run_id) / 'run.json'
        if not path.exists(): return None
        return checkpoint_state_from_run(self.store.read_json(path))

    def frontend_checkpoint(self, run_id: str) -> dict[str, Any] | None:
        state = self.active_checkpoint(run_id)
        return None if state is None else state.frontend_payload()

    def active_checkpoint_ids(self, run_id: str) -> list[str]:
        path = self.store.run_dir(run_id) / 'run.json'
        if not path.exists(): return []
        return active_checkpoint_ids_from_run(self.store.read_json(path))

    def record_resume(
        self, run_id: str, checkpoint_id: str, *, input_policy: ResumeInputPolicy,
        next_operations: list[OperationRunRef], rebound_input_refs: dict[str, list[dict[str, str]]],
        resume_context: dict[str, Any] | None = None,
    ) -> ArtifactRef:
        if input_policy not in {RESUME_FROM_SNAPSHOT, RESUME_WITH_INTERVENTIONS}:
            raise ValueError(f'unsupported checkpoint resume input policy: {input_policy}')
        checkpoint_id = validate_id(checkpoint_id, 'checkpoint_id')
        context = _resume_context(resume_context)
        artifact_id = validate_id(f'checkpoint_resume_{checkpoint_id}', 'artifact_id')
        common = {'checkpoint_id': checkpoint_id, 'input_policy': input_policy,
                  'next_operations': [str(ref) for ref in next_operations],
                  'rebound_input_refs': rebound_input_refs, **context}
        resume_ref = self.store.artifact_graph(run_id).commit_artifact(ArtifactDraft(
            artifact_id=artifact_id, schema_name='CheckpointResume', payload={'id': artifact_id, **common},
            producer_operation_run_id='checkpoint.resume', role='audit',
            input_refs=[ArtifactRef.parse(item['new_ref'])
                        for changes in rebound_input_refs.values() for item in changes]))
        self.store.append_event(Event('checkpoint.continue', run_id, {**common, 'resume_ref': str(resume_ref)}))
        return resume_ref

    def _active_artifact_refs(self, run_id: str) -> dict[str, ArtifactRef]:
        refs: dict[str, ArtifactRef] = {}
        for manifest_path in sorted(self.store.artifact_graph(run_id).manifest_dir.glob('*.json')):
            manifest = self.store.read_json(manifest_path)
            refs[manifest['artifact_id']] = ArtifactRef(manifest['artifact_id'], int(manifest['latest_version']))
        return refs

    def checkpoint_artifact_refs(self, run_id: str, checkpoint_id: str) -> dict[str, ArtifactRef]:
        data = self.store.read_json(self.store.run_dir(run_id) / 'checkpoints' / f'{checkpoint_id}.json')
        return {artifact_id: ArtifactRef.parse(value)
                for artifact_id, value in (data.get('artifact_refs') or {}).items()}

    def rebind_stage_resume_inputs(self, run_id: str, checkpoint_id: str,
                                   operation_graph: Any) -> dict[str, list[dict[str, str]]]:
        replacements = self.adopted_replacements_since_checkpoint(run_id, checkpoint_id)
        if not replacements: return {}
        rebound: dict[str, list[dict[str, str]]] = {}
        for ref in operation_graph.run_refs():
            if operation_graph.get_run(ref).status == 'ended': continue
            changes = operation_graph.rebind_input_refs(ref, replacements)
            if changes: rebound[str(ref)] = list(changes.values())
        return rebound

    def adopted_replacements_since_checkpoint(self, run_id: str, checkpoint_id: str) -> dict[str, ArtifactRef]:
        return self.adopted_replacements(run_id, list(self.checkpoint_artifact_refs(run_id, checkpoint_id).values()))

    def adopted_replacements(self, run_id: str, old_refs: list[ArtifactRef]) -> dict[str, ArtifactRef]:
        graph = self.store.artifact_graph(run_id)
        replacements: dict[str, ArtifactRef] = {}
        for old_ref in _merge_refs(old_refs):
            try:
                new_ref = graph.latest_ref(old_ref.artifact_id)
            except KeyError:
                continue
            if new_ref.version <= old_ref.version or graph.version_metadata(new_ref).get('role') == 'audit': continue
            replacements[old_ref.artifact_id] = new_ref
        return replacements

    def _assert_active_checkpoint_closed(self, run_id: str, *, allow_cancel: bool) -> None:
        path = self.store.run_dir(run_id) / 'run.json'
        if not path.exists(): return
        for checkpoint_id in active_checkpoint_ids_from_run(self.store.read_json(path)):
            self._assert_checkpoint_closed(run_id, checkpoint_id, allow_cancel=allow_cancel)

    def _assert_current_checkpoint_closed(self, run_id: str, *, allow_cancel: bool) -> None:
        state = self.active_checkpoint(run_id)
        if state is not None: self._assert_checkpoint_closed(run_id, state.checkpoint_id, allow_cancel=allow_cancel)

    def _assert_checkpoint_closed(self, run_id: str, checkpoint_id: str, *, allow_cancel: bool) -> None:
        if self._checkpoint_closed(run_id, checkpoint_id, allow_cancel=allow_cancel): return
        suffix = ' or checkpoint.cancel' if allow_cancel else ''
        raise RuntimeError(
            f'checkpoint {checkpoint_id} cannot clear dispatch before CheckpointResume{suffix} is recorded')

    def _checkpoint_closed(self, run_id: str, checkpoint_id: str, *, allow_cancel: bool) -> bool:
        blocked_at = -1
        closed_at = -1
        for index, event in enumerate(self.store.read_events(run_id)):
            payload = event.payload or {}
            if event.event_type == 'run.dispatch_blocked' and (
                payload.get('checkpoint_id') == checkpoint_id
                or (checkpoint_id == 'operation_checkpointed'
                    and payload.get('dispatch_block_reason') == 'checkpointed')
            ):
                blocked_at = index
            elif event.event_type == 'checkpoint.continue' and payload.get('checkpoint_id') == checkpoint_id:
                closed_at = index
            elif (allow_cancel and event.event_type == 'checkpoint.cancel'
                    and payload.get('checkpoint_id') == checkpoint_id):
                closed_at = index
        return closed_at > blocked_at


def _lifecycle_payload(checkpoint: dict[str, Any]) -> dict[str, Any]:
    next_operations = [str(item) for item in checkpoint.get('next_operations')
                       or checkpoint.get('blocked_operations') or [] if str(item)]
    return {
        'checkpoint_id': str(checkpoint.get('checkpoint_id') or ''), 'stage': str(checkpoint.get('stage') or ''),
        'next_stage': str(checkpoint.get('next_stage') or ''),
        'checkpoint_kind': str(checkpoint.get('checkpoint_kind') or ''),
        'checkpoint_message': str(checkpoint.get('message') or checkpoint.get('checkpoint_message') or ''),
        'blocked_operations': next_operations, 'next_operations': next_operations,
        'capability_id': str(checkpoint.get('capability_id') or ''),
        'next_op': _next_op_value(checkpoint.get('next_op')),
        'detail': dict(checkpoint.get('detail') or {}) if isinstance(checkpoint.get('detail'), dict) else {},
        **({'message_id': str(checkpoint['message_id'])} if checkpoint.get('message_id') else {}),
    }


def checkpoint_state_from_run(run: dict[str, Any]) -> CheckpointState | None:
    checkpoint_id = str(run.get('checkpoint_id') or '')
    reason = str(run.get('dispatch_block_reason') or '')
    if not checkpoint_id and reason == 'checkpointed': checkpoint_id = 'operation_checkpointed'
    if not checkpoint_id or not reason: return None
    blocked = _string_list(run.get('blocked_operations'))
    return CheckpointState(
        checkpoint_id=checkpoint_id, dispatch_block_reason=reason,
        checkpoint_kind=str(run.get('checkpoint_kind') or ''),
        message=str(run.get('checkpoint_message') or run.get('message') or ''),
        stage=str(run.get('stage') or ''), next_stage=str(run.get('next_stage') or ''),
        blocked_operations=tuple(blocked),
        next_operations=tuple(_string_list(run.get('next_operations')) or blocked),
        capability_id=str(run.get('capability_id') or ''), message_id=str(run.get('message_id') or ''),
        next_op=_next_op_value(run.get('next_op')),
        detail=dict(run.get('detail') or {}) if isinstance(run.get('detail'), dict) else {})


def active_checkpoint_ids_from_run(run: dict[str, Any]) -> list[str]:
    ids: list[str] = []
    _append_checkpoint_id(ids, run)
    parent = run.get('parent_checkpoint')
    if isinstance(parent, dict): _append_checkpoint_id(ids, parent)
    return list(dict.fromkeys(ids))


def frontend_checkpoint_from_run(run: dict[str, Any]) -> dict[str, Any] | None:
    state = checkpoint_state_from_run(run)
    return None if state is None else state.frontend_payload()


def _append_checkpoint_id(ids: list[str], checkpoint: dict[str, Any]) -> None:
    checkpoint_id = str(checkpoint.get('checkpoint_id') or '')
    if not checkpoint_id and checkpoint.get('dispatch_block_reason') == 'checkpointed':
        checkpoint_id = 'operation_checkpointed'
    if checkpoint_id: ids.append(checkpoint_id)


def _string_list(value: Any) -> list[str]:
    return [str(item) for item in value] if isinstance(value, list) else []


def _next_op_value(value: Any) -> str:
    if isinstance(value, dict): return str(value.get('op') or '')
    return str(value or '')


def _resume_context(context: dict[str, Any] | None) -> dict[str, dict[str, Any]]:
    if context is None: return {}
    kind = str(context.get('kind') or '')
    allowed = {'intent_confirmation': {'kind', 'message_id'},
               'stage': {'kind', 'stage', 'next_stage', 'source', 'recovered'}}.get(kind)
    if allowed is None: raise ValueError(f'unsupported checkpoint resume context kind: {kind}')
    unexpected = sorted(set(context) - allowed)
    if unexpected: raise ValueError(f'checkpoint resume context has unsupported fields: {", ".join(unexpected)}')
    return {'resume_context': dict(context)}


def _merge_refs(refs: list[ArtifactRef]) -> list[ArtifactRef]:
    merged: dict[str, ArtifactRef] = {}
    for ref in refs:
        current = merged.get(ref.artifact_id)
        if current is None or ref.version > current.version: merged[ref.artifact_id] = ref
    return [merged[artifact_id] for artifact_id in sorted(merged)]

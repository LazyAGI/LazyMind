from __future__ import annotations

from typing import Any

from ..artifacts import ArtifactDraft, ArtifactRef
from ..operations import OperationGraph, OperationRunRef
from ..runtime import OperationRuntime
from ..store import Event, EvoStore, StoreRunLifecycle
from .manager import CheckpointManager
from .models import CheckpointRef

OBSERVE_ONLY_INTENT_KINDS = {'chat', 'query'}
INTERRUPTING_INTENT_KINDS = {'artifact_change', 'flow_control', 'config_change', 'confirmation'}


def message_requires_interrupt(intents: list[Any]) -> bool:
    kinds = {_intent_kind(intent) for intent in intents}
    if kinds and kinds <= OBSERVE_ONLY_INTENT_KINDS:
        return False
    return any(kind in INTERRUPTING_INTENT_KINDS for kind in kinds)


class InterruptManager:
    def __init__(
        self,
        *,
        store: EvoStore,
        operation_graph: OperationGraph,
        runtime: OperationRuntime,
        checkpoint_manager: CheckpointManager,
    ):
        self.store = store
        self.operation_graph = operation_graph
        self.runtime = runtime
        self.checkpoint_manager = checkpoint_manager
        self._interrupted_runs: set[str] = set()
        self._pending_message: dict[str, str] = {}

    def handle_message(self, run_id: str, message_id: str, message: str, intents: list[Any]) -> CheckpointRef | None:
        self.record_message(run_id, message_id, message)
        if not message_requires_interrupt(intents):
            return None
        return self.interrupt_run(run_id, message_id, record_message=False)

    def record_message(self, run_id: str, message_id: str, message: str) -> ArtifactRef:
        payload = {'message_id': message_id, 'message': message}
        self.store.append_event(Event('user_message.received', run_id, payload))
        return self.store.artifact_graph(run_id).commit_artifact(
            ArtifactDraft(
                artifact_id=f'user_message_{message_id}',
                schema_name='UserMessage',
                payload=payload,
                producer_operation_run_id='user',
                role='external_input',
            )
        )

    def interrupt_run(self, run_id: str, message_id: str, *, record_message: bool = True) -> CheckpointRef:
        self._interrupted_runs.add(run_id)
        self._pending_message[run_id] = message_id
        if record_message:
            self.store.append_event(Event('user_message.received', run_id, {'message_id': message_id}))
        active = self.settle_active_operations(run_id)
        checkpoint = self.checkpoint_manager.create_checkpoint(
            run_id,
            active[-1] if active else None,
            summary=f'interrupted by {message_id}',
            resume_operations=_unique_refs([*active, *self.operation_graph.run_refs({'checkpointed'})]),
        )
        StoreRunLifecycle(self.store, run_id).block_dispatch(
            'user_message',
            checkpoint_id=checkpoint.checkpoint_id,
            message_id=message_id,
        )
        return checkpoint

    def settle_active_operations(self, run_id: str) -> list[OperationRunRef]:
        active = self.operation_graph.run_refs({'running'})
        for operation_ref in active:
            self.runtime.request_interrupt(operation_ref)
            self.runtime.settle_running(operation_ref)
        return active

    def resume(self, run_id: str, checkpoint_ref: CheckpointRef) -> None:
        for operation_ref in self.checkpoint_manager.resume_operations(run_id, checkpoint_ref):
            self.operation_graph.reset_run(operation_ref)
        self._interrupted_runs.discard(run_id)
        self._pending_message.pop(run_id, None)
        StoreRunLifecycle(self.store, run_id).open_dispatch(checkpoint_id=checkpoint_ref.checkpoint_id)

    def can_dispatch(self, run_id: str) -> bool:
        if run_id in self._interrupted_runs:
            return False
        return StoreRunLifecycle(self.store, run_id).can_dispatch()


def _intent_kind(intent: Any) -> str:
    if isinstance(intent, dict):
        return str(intent.get('kind', ''))
    return str(getattr(intent, 'kind', ''))


def _unique_refs(refs: list[OperationRunRef]) -> list[OperationRunRef]:
    seen: set[OperationRunRef] = set()
    result: list[OperationRunRef] = []
    for ref in refs:
        if ref in seen:
            continue
        seen.add(ref)
        result.append(ref)
    return result

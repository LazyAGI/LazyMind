from __future__ import annotations

from concurrent.futures import FIRST_COMPLETED, ThreadPoolExecutor, wait
from dataclasses import asdict
from datetime import datetime, timezone
from threading import RLock
import traceback
from pathlib import Path
from typing import Callable, TYPE_CHECKING

from ..artifacts import ArtifactDraft, ArtifactGraph, ArtifactRef
from ..ids import validate_id
from ..operations import OperationGraph, OperationRunRef
from .calls import InMemoryCallRecorder
from .models import (
    CallRecorder,
    DispatchGate,
    OperationContext,
    OperationExecutor,
    OperationInterrupted,
    OperationOutput,
    OperationResult,
    ProgressReporter,
    RunLifecycle,
)
from .workspace import DraftWorkspace

if TYPE_CHECKING:
    from ..store.store import EvoStore


class OperationRuntime:
    def __init__(self, *, run_id: str, operation_graph: OperationGraph, artifact_graph: ArtifactGraph, executors: dict[str, OperationExecutor],
                 draft_root: str | Path, progress_reporter: ProgressReporter | None = None, call_recorder_factory: Callable[[str], CallRecorder] | None = None,
                 run_lifecycle: RunLifecycle | None = None, dispatch_gate: DispatchGate | None = None, max_dispatch: int | None = None,
                 max_workers: int = 1,
                 ):
        self.run_id = run_id
        self.operation_graph = operation_graph
        self.artifact_graph = artifact_graph
        self.executors = executors
        self.workspace = DraftWorkspace(draft_root)
        self.progress_reporter = progress_reporter
        self.call_recorder_factory = call_recorder_factory
        self.run_lifecycle = run_lifecycle
        self.dispatch_gate = dispatch_gate
        self.max_dispatch = max_dispatch
        self.max_workers = max(1, int(max_workers or 1))
        self._lock = RLock()
        self._interrupts: set[OperationRunRef] = set()
        self._active_operations: set[OperationRunRef] = set()
        self._cancel_callbacks: dict[OperationRunRef, list[Callable[[], None]]] = {}
        self._call_records: dict[OperationRunRef, CallRecorder] = {}
        self._in_memory_call_records = []

    def run(self, operation_ref: OperationRunRef) -> OperationResult:
        results = self.dispatch(operation_ref)
        return results[0] if results else self.settle(operation_ref)

    def dispatch(self, operation_ref: OperationRunRef | None = None) -> list[OperationResult]:
        if self.max_workers > 1:
            return self._dispatch_parallel(operation_ref)
        if not self._can_dispatch(operation_ref):
            return []
        state = self.operation_graph.schedule_state()
        if self.run_lifecycle is not None and not state.complete and (operation_ref is not None or state.ready or state.running):
            self.run_lifecycle.mark_running()
        results: list[OperationResult] = []
        if operation_ref is not None:
            if not self.operation_graph.can_run(operation_ref):
                self._settle_run_lifecycle()
                return results
            results.append(self._run_one(operation_ref))
            if self._dispatch_limit_reached(results):
                self._settle_run_lifecycle()
                return results
        while True:
            if not self._can_dispatch():
                self._settle_run_lifecycle()
                return results
            ready = self.operation_graph.ready_runs()
            if not ready:
                self._settle_run_lifecycle()
                return results
            results.append(self._run_one(ready[0]))
            if self._dispatch_limit_reached(results):
                self._settle_run_lifecycle()
                return results

    def _dispatch_parallel(self, operation_ref: OperationRunRef | None = None) -> list[OperationResult]:
        if not self._can_dispatch(operation_ref):
            return []
        state = self.operation_graph.schedule_state()
        if self.run_lifecycle is not None and not state.complete and (operation_ref is not None or state.ready or state.running):
            self.run_lifecycle.mark_running()
        results: list[OperationResult] = []
        futures = {}
        with ThreadPoolExecutor(max_workers=self.max_workers) as pool:
            if operation_ref is not None:
                self._submit_parallel(pool, futures, operation_ref, results)
            while True:
                while self._remaining_slots(results, futures) > 0 and self._can_dispatch():
                    ready = self.operation_graph.ready_runs()
                    if not ready:
                        break
                    self._submit_parallel(pool, futures, ready[0], results)
                if not futures:
                    self._settle_run_lifecycle()
                    return results
                done, _ = wait(futures, return_when=FIRST_COMPLETED)
                for future in done:
                    futures.pop(future, None)
                    operation_ref, output, exc = future.result()
                    results.append(self._finish_claimed(operation_ref, output, exc))
                if self._remaining_slots(results, futures) <= 0 and not futures:
                    self._settle_run_lifecycle()
                    return results

    def _submit_parallel(self, pool: ThreadPoolExecutor, futures: dict, operation_ref: OperationRunRef, results: list[OperationResult]) -> None:
        claim = self._claim_run(operation_ref)
        if isinstance(claim, OperationResult):
            results.append(claim)
            return
        if claim is not None:
            futures[pool.submit(self._execute_claimed, claim)] = operation_ref

    def _claim_run(self, operation_ref: OperationRunRef):
        with self._lock:
            if not self.operation_graph.can_run(operation_ref):
                return None
            run = self.operation_graph.get_run(operation_ref)
            if run.status == 'ended' and run.output_refs:
                return OperationResult(str(operation_ref), list(run.output_refs), 'ended')
            if run.status not in {'pending', 'running'}:
                return OperationResult(str(operation_ref), list(run.output_refs), run.status)
            if run.status == 'running':
                if operation_ref in self._active_operations:
                    return OperationResult(str(operation_ref), list(run.output_refs), 'running')
                if operation_ref in self._interrupts:
                    return self._checkpoint_interrupted(operation_ref)
                return OperationResult(str(operation_ref), list(run.output_refs), 'running')
            executor = self.executors[run.spec.operation_type]
            draft_dir = self.workspace.prepare(str(operation_ref))
            recorder = _LockedCallRecorder(self._call_recorder(str(operation_ref)), self._lock)
            self._call_records[operation_ref] = recorder
            input_refs = self.operation_graph.inputs_for(operation_ref)
            self.operation_graph.bind_inputs(operation_ref, input_refs)
            ctx = OperationContext(
                run_id=self.run_id,
                operation_run_id=str(operation_ref),
                input_refs=input_refs,
                draft_dir=draft_dir,
                artifact_graph=self.artifact_graph,
                call_recorder=recorder,
                params=dict(run.spec.params),
                progress_reporter=_LockedProgressReporter(self.progress_reporter, self._lock) if self.progress_reporter else None,
                interrupt_requested=lambda: self._interrupt_requested(operation_ref),
                cancel_requested=lambda: self.request_interrupt(operation_ref),
                cancel_callback_registrar=lambda callback: self._register_cancel_callback(operation_ref, callback),
            )
            self._active_operations.add(operation_ref)
            self.operation_graph.start_run(operation_ref)
            return operation_ref, executor, ctx

    def _execute_claimed(self, claim) -> tuple[OperationRunRef, OperationOutput | None, Exception | None]:
        operation_ref, executor, ctx = claim
        try:
            ctx.check_interrupt()
            output = executor.execute(ctx)
            ctx.check_interrupt()
            return operation_ref, output, None
        except Exception as exc:
            return operation_ref, None, exc

    def _finish_claimed(self, operation_ref: OperationRunRef, output: OperationOutput | None, exc: Exception | None) -> OperationResult:
        with self._lock:
            try:
                if isinstance(exc, OperationInterrupted):
                    return self._checkpoint_interrupted(operation_ref)
                if exc is not None:
                    self.workspace.discard(str(operation_ref))
                    self._clear_cancel_callbacks(operation_ref)
                    return self._commit_failure(operation_ref, exc)
                return self._commit_success(operation_ref, output or OperationOutput())
            finally:
                self._active_operations.discard(operation_ref)

    def _run_one(self, operation_ref: OperationRunRef) -> OperationResult:
        run = self.operation_graph.get_run(operation_ref)
        if run.status == 'ended' and run.output_refs:
            return OperationResult(str(operation_ref), list(run.output_refs), 'ended')
        if run.status not in {'pending', 'running'}:
            return OperationResult(str(operation_ref), list(run.output_refs), run.status)
        if run.status == 'running':
            if operation_ref in self._active_operations:
                return OperationResult(str(operation_ref), list(run.output_refs), 'running')
            if operation_ref in self._interrupts:
                return self._checkpoint_interrupted(operation_ref)
            return OperationResult(str(operation_ref), list(run.output_refs), 'running')
        executor = self.executors[run.spec.operation_type]
        draft_dir = self.workspace.prepare(str(operation_ref))
        recorder = self._call_recorder(str(operation_ref))
        self._call_records[operation_ref] = recorder
        input_refs = self.operation_graph.inputs_for(operation_ref)
        self.operation_graph.bind_inputs(operation_ref, input_refs)
        ctx = OperationContext(
            run_id=self.run_id,
            operation_run_id=str(operation_ref),
            input_refs=input_refs,
            draft_dir=draft_dir,
            artifact_graph=self.artifact_graph,
            call_recorder=recorder,
            params=dict(run.spec.params),
            progress_reporter=self.progress_reporter,
            interrupt_requested=lambda: operation_ref in self._interrupts,
            cancel_requested=lambda: self.request_interrupt(operation_ref),
            cancel_callback_registrar=lambda callback: self._register_cancel_callback(operation_ref, callback),
        )
        self._active_operations.add(operation_ref)
        try:
            if run.status == 'pending':
                self.operation_graph.start_run(operation_ref)
            try:
                ctx.check_interrupt()
                output = executor.execute(ctx)
                ctx.check_interrupt()
            except OperationInterrupted:
                return self._checkpoint_interrupted(operation_ref)
            except Exception as exc:
                self.workspace.discard(str(operation_ref))
                self._clear_cancel_callbacks(operation_ref)
                return self._commit_failure(operation_ref, exc)
            return self._commit_success(operation_ref, output)
        finally:
            self._active_operations.discard(operation_ref)

    def request_interrupt(self, operation_ref: OperationRunRef) -> None:
        with self._lock:
            self._interrupts.add(operation_ref)
            callbacks = self._cancel_callbacks.pop(operation_ref, [])
        for callback in callbacks:
            callback()

    def settle_running(self, operation_ref: OperationRunRef) -> OperationResult:
        if self.operation_graph.get_run(operation_ref).status != 'running':
            return self.settle(operation_ref)
        return self._run_one(operation_ref)

    def settle(self, operation_ref: OperationRunRef) -> OperationResult:
        run = self.operation_graph.get_run(operation_ref)
        return OperationResult(str(operation_ref), list(run.output_refs), run.status)

    def call_records(self, operation_ref: OperationRunRef) -> list:
        recorder = self._call_records.get(operation_ref)
        return [] if recorder is None else list(getattr(recorder, 'records', []))

    def _call_recorder(self, operation_run_id: str) -> CallRecorder:
        if self.call_recorder_factory is not None:
            return self.call_recorder_factory(operation_run_id)
        return InMemoryCallRecorder(operation_run_id, self._in_memory_call_records)

    def _can_dispatch(self, operation_ref: OperationRunRef | None = None) -> bool:
        if self.run_lifecycle is not None and not self.run_lifecycle.can_dispatch():
            return False
        return self.dispatch_gate is None or self.dispatch_gate.can_dispatch(self.run_id)

    def _dispatch_limit_reached(self, results: list[OperationResult]) -> bool:
        return self.max_dispatch is not None and len(results) >= self.max_dispatch

    def _remaining_slots(self, results: list[OperationResult], futures: dict) -> int:
        slots = self.max_workers - len(futures)
        if self.max_dispatch is not None:
            slots = min(slots, self.max_dispatch - len(results) - len(futures))
        return max(0, slots)

    def _commit_success(self, operation_ref: OperationRunRef, output: OperationOutput) -> OperationResult:
        if operation_ref in self._interrupts:
            return self._checkpoint_interrupted(operation_ref)
        input_refs = self.operation_graph.inputs_for(operation_ref)
        drafts = [_with_runtime_lineage(draft, str(operation_ref), input_refs) for draft in output.artifacts]
        store = self._commit_store()
        if store is None:
            output_refs = [self.artifact_graph.commit_artifact(draft) for draft in drafts]
        else:
            output_refs = self._commit_success_with_tx(store, operation_ref, drafts)
        self.operation_graph.end_run(operation_ref, output_refs)
        self.operation_graph.settle_retry_replacements([operation_ref])
        self.workspace.discard(str(operation_ref))
        self._clear_cancel_callbacks(operation_ref)
        result = OperationResult(str(operation_ref), output_refs, 'ended')
        return result

    def _commit_success_with_tx(self, store: EvoStore, operation_ref: OperationRunRef, drafts: list[ArtifactDraft]) -> list:
        tx_dir = self.workspace.prepare_tx(str(operation_ref))
        output_refs = _expected_refs(self.artifact_graph, drafts)
        artifacts = []
        for draft, ref in zip(drafts, output_refs):
            draft_ref = f'artifacts/{ref.artifact_id}_v{ref.version:04d}.json'
            store.atomic_write_json(
                tx_dir / draft_ref,
                {
                    'payload': draft.payload,
                    'fragments': [asdict(item) for item in draft.fragments],
                },
            )
            artifacts.append(
                {
                    'artifact_id': ref.artifact_id,
                    'artifact_ref': str(ref),
                    'draft_ref': draft_ref,
                    'input_refs': [str(item) for item in draft.input_refs],
                    'producer_operation_run_id': draft.producer_operation_run_id,
                    'role': draft.role,
                    'schema_name': draft.schema_name,
                }
            )
        operation_state = self._success_operation_state(store, operation_ref, output_refs)
        store.atomic_write_json(tx_dir / 'operation.json', operation_state)
        store.atomic_write_json(
            tx_dir / 'tx.json',
            {
                'tx_id': f"tx_{str(operation_ref).replace('.', '_').replace(':', '_').replace('#', '_').replace('-', '_')}_{self.operation_graph.get_run(operation_ref).attempt}",
                'run_id': self.run_id,
                'operation_run_id': str(operation_ref),
                'input_refs': [str(item) for item in self.operation_graph.inputs_for(operation_ref)],
                'output_refs': [str(item) for item in output_refs],
                'operation_state': operation_state,
                'artifacts': artifacts,
            },
        )
        return store.finalize_operation_commit(self.run_id, tx_dir)

    def _success_operation_state(self, store: EvoStore, operation_ref: OperationRunRef, output_refs: list) -> dict:
        try:
            operation = store.read_operation(self.run_id, str(operation_ref))
        except FileNotFoundError:
            operation = {'operation_run_id': str(operation_ref)}
        run = self.operation_graph.get_run(operation_ref)
        operation.update(
            {
                'operation_run_id': str(operation_ref),
                'operation_id': run.spec.operation_id,
                'operation_type': run.spec.operation_type,
                'status': 'ended',
                'attempt': run.attempt,
                'category': run.spec.category,
                'flow_tag': run.spec.flow_tag,
                'stage_tag': run.spec.stage_tag,
                'input_refs': [str(ref) for ref in run.input_refs],
                'output_refs': [str(ref) for ref in output_refs],
                'depends_on': [str(ref) for ref in run.depends_on],
                'parent': str(run.parent or ''),
                'source_message_id': run.source_message_id or '',
                'superseded_by': str(run.superseded_by or ''),
                'supersede_reason': run.supersede_reason,
                'outcome': 'success',
                'tags': dict(run.spec.tags),
                'params': dict(run.spec.params),
                'required_artifact_ids': list(run.spec.required_artifact_ids),
                'required_artifact_sets': [asdict(item) for item in run.spec.required_artifact_sets],
                'ended_at': _now(),
            }
        )
        return operation

    def _commit_store(self) -> EvoStore | None:
        root = self.artifact_graph.root
        if root.name == self.run_id and root.parent.name == 'runs':
            from ..store.store import EvoStore

            return EvoStore(root.parent.parent)
        return None

    def _commit_failure(self, operation_ref: OperationRunRef, exc: Exception) -> OperationResult:
        error_artifact_id = f"error_{str(operation_ref).replace('.', '_').replace(':', '_').replace('#', '_').replace('-', '_')}"
        validate_id(error_artifact_id, 'artifact_id')
        error = ArtifactDraft(
            artifact_id=error_artifact_id,
            schema_name='ErrorArtifact',
            payload={
                'operation_run_id': str(operation_ref),
                'error_type': exc.__class__.__name__,
                'message': str(exc),
                'traceback': traceback.format_exc(),
            },
            producer_operation_run_id=str(operation_ref),
            input_refs=self.operation_graph.inputs_for(operation_ref),
        )
        ref = self.artifact_graph.commit_artifact(error)
        self.operation_graph.end_run(operation_ref, [ref], outcome='failed')
        self._clear_cancel_callbacks(operation_ref)
        result = OperationResult(str(operation_ref), [ref], 'ended')
        return result

    def _register_cancel_callback(self, operation_ref: OperationRunRef, callback: Callable[[], None]) -> None:
        with self._lock:
            if operation_ref not in self._interrupts:
                self._cancel_callbacks.setdefault(operation_ref, []).append(callback)
                return
        callback()

    def _interrupt_requested(self, operation_ref: OperationRunRef) -> bool:
        with self._lock:
            return operation_ref in self._interrupts

    def _checkpoint_interrupted(self, operation_ref: OperationRunRef) -> OperationResult:
        self.workspace.discard(str(operation_ref))
        self._clear_cancel_callbacks(operation_ref)
        if self.operation_graph.get_run(operation_ref).status == 'running':
            self.operation_graph.checkpoint_run(operation_ref)
        self._interrupts.discard(operation_ref)
        return OperationResult(str(operation_ref), [], 'checkpointed')

    def _clear_cancel_callbacks(self, operation_ref: OperationRunRef) -> None:
        self._cancel_callbacks.pop(operation_ref, None)

    def _settle_run_lifecycle(self) -> None:
        if self.run_lifecycle is None:
            return
        state = self.operation_graph.schedule_state()
        if state.complete:
            self.run_lifecycle.mark_ended(outcome='success')
            return
        if state.checkpointed:
            self.run_lifecycle.block_dispatch(
                'checkpointed',
                blocked_operations=[str(ref) for ref in state.checkpointed],
            )
            return
        if state.blockers:
            root_blockers = [blocker for blocker in state.blockers if blocker.reason != 'dependency_not_satisfied']
            impacted_blockers = [blocker for blocker in state.blockers if blocker.reason == 'dependency_not_satisfied']
            self.run_lifecycle.block_dispatch(
                (root_blockers or state.blockers)[0].reason,
                blocked_operations=[asdict(blocker) for blocker in state.blockers],
                root_blockers=[asdict(blocker) for blocker in root_blockers],
                impacted_blockers=[asdict(blocker) for blocker in impacted_blockers],
            )


class _LockedCallRecorder:
    def __init__(self, recorder: CallRecorder, lock: RLock):
        self.recorder = recorder
        self.lock = lock

    @property
    def records(self):
        return getattr(self.recorder, 'records', [])

    def record(self, *args, **kwargs):
        with self.lock:
            return self.recorder.record(*args, **kwargs)

    def succeeded(self, *args, **kwargs):
        with self.lock:
            return self.recorder.succeeded(*args, **kwargs)


class _LockedProgressReporter:
    def __init__(self, reporter: ProgressReporter, lock: RLock):
        self.reporter = reporter
        self.lock = lock

    def report(self, *args, **kwargs) -> None:
        with self.lock:
            self.reporter.report(*args, **kwargs)


def _with_runtime_lineage(draft: ArtifactDraft, operation_run_id: str, input_refs: list) -> ArtifactDraft:
    return ArtifactDraft(
        artifact_id=draft.artifact_id,
        schema_name=draft.schema_name,
        payload=draft.payload,
        producer_operation_run_id=operation_run_id,
        input_refs=list(draft.input_refs or input_refs),
        fragments=draft.fragments,
        role=draft.role,
    )


def _expected_refs(artifact_graph: ArtifactGraph, drafts: list[ArtifactDraft]) -> list:
    next_versions: dict[str, int] = {}
    refs = []
    for draft in drafts:
        if draft.artifact_id not in next_versions:
            try:
                next_versions[draft.artifact_id] = artifact_graph.latest_ref(draft.artifact_id).version + 1
            except KeyError:
                next_versions[draft.artifact_id] = 1
        refs.append(ArtifactRef(draft.artifact_id, next_versions[draft.artifact_id]))
        next_versions[draft.artifact_id] += 1
    return refs


def _now() -> str:
    return datetime.now(timezone.utc).isoformat()

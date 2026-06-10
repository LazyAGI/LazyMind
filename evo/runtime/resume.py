from __future__ import annotations

from pathlib import Path
from typing import Callable, TYPE_CHECKING

from .models import CallRecorder, DispatchGate, OperationExecutor, ProgressReporter
from .runtime import OperationRuntime

if TYPE_CHECKING:
    from ..store.store import EvoStore


def resume_run_from_store(
    *, store: EvoStore, run_id: str, executors: dict[str, OperationExecutor], draft_root: str | Path | None = None,
    progress_reporter: ProgressReporter | None = None,
    call_recorder_factory: Callable[[str], CallRecorder] | None = None, dispatch_gate: DispatchGate | None = None,
    max_dispatch: int | None = None, auto_dispatch: bool = False,
) -> OperationRuntime:
    from ..store.run_lifecycle import StoreRunLifecycle

    store.recover_run(run_id)
    runtime = OperationRuntime(
        run_id=run_id, operation_graph=store.restore_operation_graph(run_id),
        artifact_graph=store.artifact_graph(run_id), executors=executors,
        draft_root=draft_root or store.run_dir(run_id) / 'tmp' / 'drafts', progress_reporter=progress_reporter,
        call_recorder_factory=call_recorder_factory, run_lifecycle=StoreRunLifecycle(store, run_id),
        dispatch_gate=dispatch_gate, max_dispatch=max_dispatch,
    )
    if auto_dispatch:
        runtime.dispatch()
    return runtime


def continue_run(runtime: OperationRuntime) -> list:
    if runtime.operation_graph.run_refs({'checkpointed'}):
        raise RuntimeError('checkpointed runs must resume through CheckpointManager with an explicit input_policy')
    return runtime.dispatch()

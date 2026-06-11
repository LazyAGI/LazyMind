from __future__ import annotations

from dataclasses import dataclass, field
from pathlib import Path
from typing import Any, Callable, Literal, Protocol

from ..artifacts import ArtifactDraft, ArtifactGraph, ArtifactRef


@dataclass(frozen=True)
class OperationOutput:
    artifacts: list[ArtifactDraft] = field(default_factory=list)


@dataclass(frozen=True)
class OperationResult:
    operation_run_id: str
    output_refs: list[ArtifactRef]
    status: str


CallStatus = Literal['succeeded', 'failed']


@dataclass(frozen=True)
class CallRecord:
    operation_run_id: str
    adapter_type: str
    request: Any
    response: Any = None
    call_id: str = ''
    phase: str = ''
    item_ref: str = ''
    status: CallStatus = 'succeeded'
    request_ref: str = ''
    response_ref: str = ''
    idempotency_key: str = ''
    idempotency_scope: str = 'operation'
    error: dict[str, Any] | None = None
    reused: bool = False
    reused_from_call_id: str = ''
    reused_from_operation_run_id: str = ''
    record_ref: str = ''
    sequence: int = 0


@dataclass(frozen=True)
class OperationProgress:
    phase: str
    message: str = ''
    status: str = 'running'
    current_item: str = ''
    done: int | None = None
    total: int | None = None
    detail: dict[str, Any] = field(default_factory=dict)

    def to_dict(self) -> dict[str, Any]:
        data = {'phase': self.phase, 'status': self.status, 'message': self.message,
                'current_item': self.current_item, 'detail': self.detail}
        if self.done is not None: data['done'] = self.done
        if self.total is not None: data['total'] = self.total
        return data


@dataclass
class OperationContext:
    run_id: str
    operation_run_id: str
    input_refs: list[ArtifactRef]
    draft_dir: Path
    artifact_graph: ArtifactGraph
    call_recorder: 'CallRecorder'
    params: dict[str, Any] = field(default_factory=dict)
    progress_reporter: 'ProgressReporter | None' = None
    interrupt_requested: Callable[[], bool] = lambda: False
    cancel_requested: Callable[[], None] = lambda: None
    cancel_callback_registrar: Callable[[Callable[[], None]], None] = lambda callback: None

    def check_interrupt(self) -> None:
        if self.interrupt_requested(): raise OperationInterrupted(self.operation_run_id)

    def register_cancel_callback(self, callback: Callable[[], None]) -> None:
        self.cancel_callback_registrar(callback)

    def report_progress(
        self, *, phase: str, message: str = '', status: str = 'running', current_item: str = '',
        done: int | None = None, total: int | None = None, detail: dict[str, Any] | None = None,
    ) -> None:
        if self.progress_reporter is None: return
        self.progress_reporter.report(
            self.operation_run_id,
            OperationProgress(phase=phase, message=message, status=status, current_item=current_item,
                              done=done, total=total, detail=detail or {}),
        )


class CallRecorder(Protocol):
    def record(
        self, adapter_type: str, request: Any, response: Any = None, *, phase: str = '', item_ref: str = '',
        status: CallStatus = 'succeeded', idempotency_key: str = '', idempotency_scope: str = 'operation',
        error: dict[str, Any] | None = None,
    ) -> CallRecord:
        ...

    def succeeded(self, idempotency_key: str, *, idempotency_scope: str = 'operation') -> CallRecord | None:
        ...


class ProgressReporter(Protocol):
    def report(self, operation_run_id: str, progress: OperationProgress) -> None:
        ...


class RunLifecycle(Protocol):
    def mark_running(self, **extra: Any) -> None:
        ...

    def block_dispatch(self, reason: str, **extra: Any) -> None:
        ...

    def open_dispatch(self, **extra: Any) -> None:
        ...

    def mark_ended(self, *, outcome: str = 'success', **extra: Any) -> None:
        ...

    def can_dispatch(self) -> bool:
        ...


class DispatchGate(Protocol):
    def can_dispatch(self, run_id: str) -> bool:
        ...


class OperationExecutor(Protocol):
    def execute(self, ctx: OperationContext) -> OperationOutput:
        ...


class OperationInterrupted(Exception):
    pass

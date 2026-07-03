from __future__ import annotations

import hashlib
import json
from collections.abc import Callable, Mapping
from dataclasses import dataclass
from typing import Literal, Protocol

from evo.artifact_runtime.evo.actions import (
    dispatch_evo_mutation,
    mutation_idempotency_key,
    mutation_receipt_outcome,
    mutation_request_fingerprint,
)
from evo.artifact_runtime.evo.flow import EvoFlowSpec
from evo.artifact_runtime.evo.progress import StepProgress, progress_view
from evo.artifact_runtime.evo.use_cases import EvoArtifactAccess
from evo.artifact_runtime.kernel.artifact import ArtifactRef
from evo.artifact_runtime.kernel.errors import IdempotencyConflictError

from .commands import (
    ApplyArtifactMutation,
    CancelFlow,
    ContinueFlow,
    FlowCommand,
    PauseFlow,
    ResumeFlow,
    RetryFlow,
)
from .gate import CommandReceipt
from .state import Checkpoint, CheckpointPolicy, FlowRunState


CommandStatus = Literal['ok', 'blocked', 'conflict', 'failed', 'stale', 'tick_limit']
_OUTCOME_STATUS: Mapping[object, CommandStatus] = {
    'cancelled': 'blocked',
    'conflict': 'conflict',
    'failed': 'failed',
    'paused': 'blocked',
    'stale': 'stale',
    'tick_limit': 'tick_limit',
}
DEFAULT_CHECKPOINT_POLICY = CheckpointPolicy()


class FlowGatePort(Protocol):
    def get(self, run_id: str) -> FlowRunState | None:
        ...

    def read_command(self, run_id: str, command_id: str, request_hash: str) -> CommandReceipt:
        ...

    def record_command(self, run_id: str, command_id: str, request_hash: str,
                       outcome: Mapping[str, object], *, next_state: FlowRunState | None = None,
                       expected_version: int | None = None) -> CommandReceipt:
        ...

    def apply_gate_command(self, run_id: str, command_id: str, request_hash: str, command_kind: str) -> CommandReceipt:
        ...


class FlowAdapterPort(EvoArtifactAccess, Protocol):
    def tick(self, run_id: str) -> object:
        ...


@dataclass(frozen=True)
class FlowCommandResult:
    run_id: str
    command_status: CommandStatus = 'ok'
    command_outcome: Mapping[str, object] | None = None
    error: str = ''


class FlowService:
    def __init__(self, gate: FlowGatePort, adapter_factory: Callable[[], FlowAdapterPort],
                 spec: EvoFlowSpec, checkpoint_policy: CheckpointPolicy = DEFAULT_CHECKPOINT_POLICY,
                 tick_limit: int = 100) -> None:
        if not isinstance(spec, EvoFlowSpec):
            raise TypeError('spec must be EvoFlowSpec')
        if not isinstance(checkpoint_policy, CheckpointPolicy):
            raise TypeError('checkpoint_policy must be CheckpointPolicy')
        if not isinstance(tick_limit, int) or isinstance(tick_limit, bool):
            raise TypeError('tick_limit must be int')
        if tick_limit < 1:
            raise ValueError('tick_limit must be >= 1')
        _validate_policy(spec, checkpoint_policy)
        self._gate = gate
        self._adapter_factory = adapter_factory
        self._spec = spec
        self._checkpoint_policy = checkpoint_policy
        self._tick_limit = tick_limit

    def handle(self, run_id: str, command: FlowCommand) -> FlowCommandResult:
        _require_text(run_id, 'run_id')
        match command:
            case ContinueFlow():
                return self._continue(run_id, command)
            case ApplyArtifactMutation():
                return self._mutation(run_id, command)
            case PauseFlow():
                return self._gate_command(run_id, command.command_id, 'pause')
            case ResumeFlow():
                return self._gate_command(run_id, command.command_id, 'resume')
            case CancelFlow():
                return self._gate_command(run_id, command.command_id, 'cancel')
            case RetryFlow():
                return self._gate_command(run_id, command.command_id, 'retry')
        raise TypeError(f'unsupported FlowCommand: {type(command).__name__}')

    def _gate_command(self, run_id: str, command_id: str, kind: str) -> FlowCommandResult:
        receipt = self._gate.apply_gate_command(run_id, command_id, _request_hash({'kind': kind}), kind)
        return self._from_receipt(run_id, receipt)

    def _mutation(self, run_id: str, command: ApplyArtifactMutation) -> FlowCommandResult:
        mutation_key = mutation_idempotency_key(command.mutation)
        if mutation_key != command.command_id:
            return self._result(
                run_id,
                'failed',
                {'status': 'failed'},
                error='mutation idempotency_key must match command_id',
            )

        try:
            fingerprint = mutation_request_fingerprint(command.mutation)
        except Exception as exc:
            return self._result(run_id, 'failed', {'status': 'failed'}, error=_short_error(exc))
        request_hash = _request_hash({'kind': 'ApplyArtifactMutation', 'mutation': fingerprint})
        replay = self._gate.read_command(run_id, command.command_id, request_hash)
        if replay.status != 'new':
            return self._from_receipt(run_id, replay)

        try:
            result = dispatch_evo_mutation(self._adapter_factory(), self._spec, run_id, command.mutation)
            outcome = dict(mutation_receipt_outcome(result))
            error = ''
        except IdempotencyConflictError as exc:
            return self._result(run_id, 'conflict', {'status': 'conflict'}, error=_short_error(exc))
        except Exception as exc:
            outcome = {'error': _short_error(exc), 'status': 'failed'}
            error = str(outcome['error'])

        receipt = self._gate.record_command(run_id, command.command_id, request_hash, outcome)
        return self._from_receipt(run_id, receipt, error=error)

    def _continue(self, run_id: str, command: ContinueFlow) -> FlowCommandResult:
        self._validate_until_step(command.until_step)
        request_hash = _request_hash({'kind': 'ContinueFlow', 'until_step': command.until_step})
        replay = self._gate.read_command(run_id, command.command_id, request_hash)
        if replay.status != 'new':
            return self._from_receipt(run_id, replay)
        state = replay.state
        if state.status != 'idle':
            return self._record(run_id, command, request_hash, {'status': state.status})

        released = dict(state.released_checkpoints)
        try:
            adapter = self._adapter_factory()
            for _ in range(self._tick_limit):
                interrupted = self._interrupted(run_id, state)
                if interrupted is not None:
                    return self._record(run_id, command, request_hash, interrupted)

                progress = progress_view(adapter, self._spec, run_id)
                released.update(_released_before(command.until_step, progress))
                interrupted = self._interrupted(run_id, state)
                if interrupted is not None:
                    return self._record(run_id, command, request_hash, interrupted)
                boundary = self._record_if_boundary(run_id, command, request_hash, state, progress, released)
                if boundary is not None:
                    return boundary

                tick = adapter.tick(run_id)
                if tick.status in {'failed', 'conflict'}:
                    error = _tick_error(tick)
                    return self._record(
                        run_id,
                        command,
                        request_hash,
                        {'error': error, 'status': tick.status},
                        FlowRunState(run_id, status='failed', released_checkpoints=released, last_error=error),
                        state.status_version,
                        error=error,
                    )

                progress = progress_view(adapter, self._spec, run_id)
                released.update(_released_before(command.until_step, progress))
                interrupted = self._interrupted(run_id, state)
                if interrupted is not None:
                    return self._record(run_id, command, request_hash, interrupted)
                boundary = self._record_if_boundary(run_id, command, request_hash, state, progress, released)
                if boundary is not None:
                    return boundary
                if tick.status == 'idle':
                    next_state = None
                    expected_version = None
                    if released != dict(state.released_checkpoints):
                        next_state = FlowRunState(run_id, released_checkpoints=released)
                        expected_version = state.status_version
                    return self._record(
                        run_id,
                        command,
                        request_hash,
                        {'status': 'idle'},
                        next_state,
                        expected_version,
                    )

            return self._result(
                run_id,
                'tick_limit',
                {'status': 'tick_limit'},
            )
        except Exception as exc:
            error = _short_error(exc)
            return self._record(
                run_id,
                command,
                request_hash,
                {'error': error, 'status': 'failed'},
                FlowRunState(run_id, status='failed', released_checkpoints=released, last_error=error),
                state.status_version,
                error=error,
            )

    def _record_if_boundary(self, run_id: str, command: ContinueFlow, request_hash: str,
                            state: FlowRunState, progress: tuple[StepProgress, ...],
                            released: Mapping[str, ArtifactRef]) -> FlowCommandResult | None:
        target = command.until_step
        if target == self._spec.steps[-1]:
            if _progress_by_step(progress)[target].completed:
                return self._record(
                    run_id,
                    command,
                    request_hash,
                    {'status': 'ok'},
                )
            return None

        checkpoint = _checkpoint(progress, self._checkpoint_policy, released, target)
        if checkpoint is None:
            return None
        return self._record(
            run_id,
            command,
            request_hash,
            {'checkpoint': _checkpoint_json(checkpoint), 'status': 'paused'},
            FlowRunState(run_id, status='paused', pending_checkpoint=checkpoint, released_checkpoints=released),
            state.status_version,
        )

    def _record(self, run_id: str, command: ContinueFlow, request_hash: str,
                outcome: Mapping[str, object], next_state: FlowRunState | None = None,
                expected_version: int | None = None, *, error: str = ''
                ) -> FlowCommandResult:
        receipt = self._gate.record_command(
            run_id,
            command.command_id,
            request_hash,
            outcome,
            next_state=next_state,
            expected_version=expected_version,
        )
        return self._from_receipt(run_id, receipt, error=error)

    def _interrupted(self, run_id: str, state: FlowRunState) -> dict[str, object] | None:
        current = self._gate.get(run_id)
        if current is None:
            return {'receipt_status': 'stale', 'status': 'idle'}
        if current.status != 'idle' or current.status_version != state.status_version:
            return {'receipt_status': 'stale', 'status': current.status}
        return None

    def _validate_until_step(self, step: str) -> None:
        if step and step not in self._spec.steps:
            raise ValueError(f'unknown until_step: {step}')

    def _from_receipt(self, run_id: str, receipt: CommandReceipt, *, error: str = '') -> FlowCommandResult:
        if receipt.status == 'conflict':
            return self._result(run_id, 'conflict', receipt.outcome, error=error)
        return self._result(run_id, _outcome_status(receipt.outcome or {'status': 'ok'}), receipt.outcome, error=error)

    def _result(self, run_id: str, command_status: CommandStatus,
                command_outcome: Mapping[str, object] | None = None, error: str = ''
                ) -> FlowCommandResult:
        return FlowCommandResult(run_id, command_status, command_outcome, error)


def _validate_policy(spec: EvoFlowSpec, policy: CheckpointPolicy) -> None:
    final_step = spec.steps[-1]
    for step in policy.pause_after_steps:
        if step not in spec.steps:
            raise ValueError(f'unknown checkpoint step: {step}')
        if step == final_step:
            raise ValueError('checkpoint policy must not include final step')


def _request_hash(value: Mapping[str, object]) -> str:
    return hashlib.sha256(json.dumps(value, sort_keys=True, separators=(',', ':'), allow_nan=False).encode()).hexdigest()


def _outcome_status(outcome: Mapping[str, object]) -> CommandStatus:
    if outcome.get('receipt_status') == 'stale':
        return 'stale'
    return _OUTCOME_STATUS.get(outcome.get('status'), 'ok')


def _released_before(target_step: str, progress: tuple[StepProgress, ...]) -> dict[str, ArtifactRef]:
    if not target_step:
        return {}
    released: dict[str, ArtifactRef] = {}
    for item in progress:
        if item.step == target_step:
            break
        if item.root_ref is not None:
            released[item.step] = item.root_ref
    return released


def _checkpoint(progress: tuple[StepProgress, ...], policy: CheckpointPolicy,
                released: Mapping[str, ArtifactRef], target_step: str
                ) -> Checkpoint | None:
    for item in progress:
        if target_step and item.step != target_step:
            continue
        if (
            target_step and item.step == target_step and item.root_ref is not None
            and released.get(item.step) != item.root_ref
        ):
            return Checkpoint(item.step, item.root, item.root_ref)
        if (
            item.step in policy.pause_after_steps and item.root_ref is not None
            and released.get(item.step) != item.root_ref
        ):
            return Checkpoint(item.step, item.root, item.root_ref)
        if target_step and item.step == target_step:
            return None
    return None


def _progress_by_step(progress: tuple[StepProgress, ...]) -> dict[str, StepProgress]:
    return {item.step: item for item in progress}


def _tick_error(tick: object) -> str:
    for op in getattr(tick, 'ops', ()):
        if op.error:
            return op.error
    return str(getattr(tick, 'status', 'failed'))


def _checkpoint_json(checkpoint: Checkpoint) -> dict[str, object]:
    return {
        'ref': [checkpoint.ref.key.artifact_id, checkpoint.ref.key.partition, checkpoint.ref.version],
        'root': [checkpoint.root.artifact_id, checkpoint.root.partition],
        'step': checkpoint.step,
    }


def _short_error(exc: Exception) -> str:
    return str(exc) or type(exc).__name__


def _require_text(value: str, name: str) -> None:
    if not isinstance(value, str):
        raise TypeError(f'{name} must be str')
    if not value.strip():
        raise ValueError(f'{name} must be non-empty')


__all__ = ['CommandStatus', 'FlowCommandResult', 'FlowService']

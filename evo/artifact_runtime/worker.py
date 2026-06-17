from __future__ import annotations

from collections.abc import Callable, Mapping
from dataclasses import dataclass
from threading import RLock
from typing import Any, Literal, Protocol

from .artifact import ArtifactKey, ArtifactPayload, ArtifactRef
from .controller import (
    AttemptClaim,
    AttemptExecutionResult,
    AttemptExecutor,
    AttemptResult,
    RunController,
    RunStatus,
)
from .errors import DAGGraphError
from .external import CancellationToken, ExternalCallGateway, ExternalCallResult, ExternalCallRunner
from .graph import DAGGraph
from .plan import PlanOp
from .store import ArtifactRecord

DispatchStatus = Literal['idle', 'dispatched']
RUN_TERMINAL = frozenset({'completed', 'failed', 'cancelled'})
_CANCEL_ERROR = {'error_type': 'cancel_requested', 'error_message': 'cancel_requested'}


class ArtifactReader(Protocol):
    def get(self, ref: ArtifactRef) -> ArtifactRecord | None:
        ...


class ExternalCallFacade:
    def __init__(self, gateway: ExternalCallGateway | None, claim: AttemptClaim, token: CancellationToken) -> None:
        self._gateway = gateway
        self._claim = claim
        self._token = token

    def call(
        self,
        *,
        call_id: str,
        payload: Mapping[str, Any],
        runner: ExternalCallRunner,
        idempotency_key: str | None = None,
        payload_fingerprint: str | None = None,
        metadata: Mapping[str, Any] | None = None,
    ) -> ExternalCallResult:
        if self._gateway is None:
            raise RuntimeError('external call gateway is not configured')
        return self._gateway.call(
            run_id=self._claim.run_id,
            attempt_id=self._claim.attempt_id,
            plan_version=self._claim.plan_version,
            op_id=self._claim.op_id,
            call_id=call_id,
            payload=payload,
            runner=runner,
            token=self._token,
            idempotency_key=idempotency_key,
            payload_fingerprint=payload_fingerprint,
            metadata=metadata,
        )


@dataclass(frozen=True)
class ExecutionContext:
    cancellation_token: CancellationToken
    external: ExternalCallFacade
    output_keys: tuple[ArtifactKey, ...] = ()
    model_config: Mapping[str, Any] | None = None

    def is_cancel_requested(self) -> bool:
        return self.cancellation_token.is_cancel_requested()

    def raise_if_cancelled(self) -> None:
        self.cancellation_token.raise_if_cancelled()

    @property
    def output_partition(self) -> str:
        partitions = {key.partition for key in self.output_keys if key.partition}
        return next(iter(partitions)) if len(partitions) == 1 else ''


@dataclass(frozen=True)
class WorkerDispatchResult:
    run_id: str
    claimed: int
    results: tuple[AttemptResult, ...]
    status: DispatchStatus
    run_status: RunStatus | None = None


class MaterializerExecutor(AttemptExecutor):
    def __init__(
        self,
        graph: DAGGraph,
        reader: ArtifactReader,
        *,
        external_gateway: ExternalCallGateway | None = None,
        cancellation_probe: Callable[[AttemptClaim], bool] | None = None,
        model_config: Mapping[str, Any] | None = None,
    ) -> None:
        self.graph = graph
        self.reader = reader
        self.external_gateway = external_gateway
        self._cancellation_probe = cancellation_probe
        self._model_config_lock = RLock()
        self._model_config = dict(model_config or {})

    def bind_controller(self, controller: RunController) -> None:
        self._cancellation_probe = lambda claim: controller.inspect_claim(claim).cancel_requested

    def set_model_config(self, model_config: Mapping[str, Any] | None) -> None:
        with self._model_config_lock:
            self._model_config = dict(model_config or {})

    def execute(self, claim: AttemptClaim, plan_op: PlanOp) -> AttemptExecutionResult:
        model_config = self._model_config_snapshot()
        try:
            _activate_lazyllm_model_config(claim, model_config)
            op_cls = self.graph.materializer_for_plan_op(plan_op)
            inputs = self._load_inputs(claim, plan_op)
            outputs = op_cls.execute(inputs, self._context_for(claim, plan_op, model_config))
            return _validate_outputs(plan_op, outputs)
        except DAGGraphError as error:
            return AttemptExecutionResult(False, error_type='materializer_lookup_failed', error_message=str(error))
        except _InputLoadError as error:
            return AttemptExecutionResult(False, error_type=error.error_type, error_message=error.message)
        except Exception as error:  # noqa: BLE001 - worker boundary converts op failures to attempt failures.
            return AttemptExecutionResult(False, error_type=type(error).__name__, error_message=str(error))

    def _load_inputs(self,
                     claim: AttemptClaim,
                     plan_op: PlanOp) -> dict[str,
                                              ArtifactPayload | dict[str,
                                                                     ArtifactPayload]]:
        inputs: dict[str, ArtifactPayload | dict[str, ArtifactPayload]] = {}
        for binding in plan_op.input_bindings:
            ref = claim.resolved_input_refs.get(binding.key)
            if ref is None:
                if binding.required:
                    raise _InputLoadError('missing_input_ref', f'missing input ref: {binding.key}')
                continue
            record = self.reader.get(ref)
            if record is None:
                raise _InputLoadError('missing_input_record', f'missing input record: {ref}')
            if binding.input_kind == 'partition_collection':
                collection = inputs.setdefault(binding.collection_name or binding.name, {})
                collection[binding.key.partition] = record.value
            else:
                inputs[binding.name] = record.value
        return inputs

    def _model_config_snapshot(self) -> dict[str, Any]:
        with self._model_config_lock:
            return dict(self._model_config)

    def _context_for(self, claim: AttemptClaim, plan_op: PlanOp, model_config: Mapping[str, Any]) -> ExecutionContext:
        token = CancellationToken(lambda: self._is_cancel_requested(claim))
        return ExecutionContext(
            token,
            ExternalCallFacade(self.external_gateway, claim, token),
            plan_op.output_keys,
            dict(model_config),
        )

    def _is_cancel_requested(self, claim: AttemptClaim) -> bool:
        if self._cancellation_probe is not None:
            return self._cancellation_probe(claim)
        return claim.cancel_requested


class PlanExecutionWorker:
    def __init__(self, controller: RunController, executor: AttemptExecutor) -> None:
        self.controller = controller
        self.executor = executor
        if isinstance(executor, MaterializerExecutor):
            executor.bind_controller(controller)

    def dispatch_once(self, run_id: str, *, limit: int = 1) -> WorkerDispatchResult:
        if limit < 1:
            raise ValueError('limit must be >= 1')
        claims = self.controller.claim_ready(run_id, limit=limit)
        results = dispatch_claims(self.controller, self.executor, claims)
        return WorkerDispatchResult(
            run_id,
            len(claims),
            results,
            'dispatched' if claims else 'idle',
            _run_status(self.controller, run_id),
        )

    def drain(self, run_id: str, *, max_rounds: int | None = None,
              limit_per_round: int = 1) -> tuple[WorkerDispatchResult, ...]:
        if limit_per_round < 1:
            raise ValueError('limit_per_round must be >= 1')
        if max_rounds is not None and max_rounds < 1:
            raise ValueError('max_rounds must be >= 1')

        rounds: list[WorkerDispatchResult] = []
        while max_rounds is None or len(rounds) < max_rounds:
            result = self.dispatch_once(run_id, limit=limit_per_round)
            rounds.append(result)
            if result.claimed == 0 or result.run_status in RUN_TERMINAL:
                break
        return tuple(rounds)


class _InputLoadError(Exception):
    def __init__(self, error_type: str, message: str) -> None:
        super().__init__(error_type, message)
        self.error_type = error_type


def _validate_outputs(plan_op: PlanOp, outputs: Any) -> AttemptExecutionResult:
    if not isinstance(outputs, Mapping):
        return AttemptExecutionResult(False, error_type='output_mismatch', error_message='outputs must be a mapping')
    expected = set(plan_op.output_names)
    actual = set(outputs)
    if actual != expected:
        return AttemptExecutionResult(
            False,
            error_type='output_mismatch',
            error_message='output keys do not match output names')
    if not all(isinstance(value, ArtifactPayload) for value in outputs.values()):
        return AttemptExecutionResult(
            False,
            error_type='output_mismatch',
            error_message='outputs must be ArtifactPayload values')
    return AttemptExecutionResult(True, dict(outputs))


def dispatch_claims(controller: RunController, executor: AttemptExecutor,
                    claims: list[AttemptClaim]) -> tuple[AttemptResult, ...]:
    results: list[AttemptResult] = []
    for claim in claims:
        inspection = controller.inspect_claim(claim)
        if inspection.status == 'stale':
            results.append(AttemptResult(claim.attempt_id, 'stale', reason='stale_claim'))
            continue
        if inspection.status == 'terminal':
            results.append(
                AttemptResult(
                    claim.attempt_id,
                    inspection.attempt_status or 'stale',
                    reason=inspection.reason))
            continue
        if inspection.cancel_requested:
            results.append(controller.fail_attempt(claim, _CANCEL_ERROR))
            continue
        try:
            execution = executor.execute(claim, claim.plan_op)
        except Exception as error:  # noqa: BLE001 - worker boundary must not leave claims hanging.
            execution = AttemptExecutionResult(False, error_type=type(error).__name__, error_message=str(error))
        results.append(controller.complete_attempt(claim, execution) if execution.ok else controller.fail_attempt(
            claim, {'error_type': execution.error_type, 'error_message': execution.error_message}))
    return tuple(results)


def _run_status(controller: RunController, run_id: str) -> RunStatus | None:
    state = controller.state(run_id)
    return state.run.status if state.run_exists else None


def _activate_lazyllm_model_config(claim: AttemptClaim, model_config: Mapping[str, Any]) -> None:
    _init_lazyllm_session(claim, required=bool(model_config))
    if model_config:
        from lazymind.model_config import inject_model_config

        inject_model_config(dict(model_config))


def _init_lazyllm_session(claim: AttemptClaim, *, required: bool) -> None:
    try:
        import lazyllm
    except ImportError:
        if required:
            raise
        return

    session_id = f'evo-materializer-{claim.run_id}-{claim.attempt_id}'
    globals_init = getattr(getattr(lazyllm, 'globals', None), '_init_sid', None)
    locals_init = getattr(getattr(lazyllm, 'locals', None), '_init_sid', None)
    if callable(globals_init):
        globals_init(sid=session_id)
    if callable(locals_init):
        locals_init(session_id)

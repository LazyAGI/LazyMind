from __future__ import annotations

from concurrent.futures import FIRST_COMPLETED, Future, ThreadPoolExecutor, wait
import hashlib
import json
import uuid
from collections.abc import Callable, Mapping
from dataclasses import dataclass
from types import MappingProxyType
from typing import Literal

from .artifact import ArtifactKey, ArtifactRef
from .errors import ArtifactStoreCorruptionError, IdempotencyConflictError, MaterializerContractError
from .graph import DAGGraph, NextOp
from .materializer import Materializer, MaterializerInput, MaterializerOutput, MaterializerContext
from .scheduler import ConcurrencyLimits, select_ready_op
from .store import SQLiteArtifactStore


TickInterruptionChecker = Callable[[], bool]
OpSelector = Callable[[NextOp], bool]
TickStatus = Literal['idle', 'ok', 'stopped', 'stale', 'failed', 'conflict']
OpStatus = Literal['ok', 'stale', 'failed', 'conflict']
_RunStatus = Literal['ok', 'stale', 'failed', 'conflict', 'busy', 'already_done']


@dataclass(frozen=True)
class OpResult:
    op_id: str
    status: OpStatus
    output_refs: tuple[ArtifactRef, ...] = ()
    error: str = ''


@dataclass(frozen=True)
class TickResult:
    status: TickStatus
    ops: tuple[OpResult, ...] = ()


@dataclass(frozen=True)
class _RunOpResult:
    op_id: str
    status: _RunStatus
    output_refs: tuple[ArtifactRef, ...] = ()
    error: str = ''


class ArtifactRuntime:
    def __init__(
        self,
        store: SQLiteArtifactStore,
        graph: DAGGraph,
        materializers: Mapping[str, Materializer],
        *,
        store_factory: Callable[[], SQLiteArtifactStore] | None = None,
        concurrency_limits: ConcurrencyLimits | None = None,
    ) -> None:
        self._store = store
        self._graph = graph
        self._materializers = dict(materializers)
        self._store_factory = store_factory or (lambda: type(store)(store.root))
        self._concurrency_limits = concurrency_limits or ConcurrencyLimits()
        self._runtime_id = uuid.uuid4().hex
        self._next_tick_id = 0

    def tick(
        self,
        run_id: str,
        *,
        should_interrupt: TickInterruptionChecker | None = None,
        op_selector: OpSelector | None = None,
    ) -> TickResult:
        tick_id = self._next_tick_id
        self._next_tick_id += 1
        if self._concurrency_limits.max_in_flight == 1:
            return self._tick_serial(
                run_id,
                tick_id,
                should_interrupt=should_interrupt,
                op_selector=op_selector,
            )
        return self._tick_concurrent(
            run_id,
            tick_id,
            should_interrupt=should_interrupt,
            op_selector=op_selector,
        )

    def _tick_serial(
        self,
        run_id: str,
        tick_id: int,
        *,
        should_interrupt: TickInterruptionChecker | None,
        op_selector: OpSelector | None,
    ) -> TickResult:
        results: list[OpResult] = []
        busy_ops: set[str] = set()
        while True:
            if should_interrupt is not None and should_interrupt():
                return TickResult('stopped', tuple(results))
            ready = tuple(
                op for op in self._graph.next_ops(self._store.effective_artifacts(run_id))
                if op.op_id not in busy_ops and (op_selector is None or op_selector(op))
            )
            op = select_ready_op(ready)
            if op is None:
                return TickResult('idle' if not results else 'ok', tuple(results))
            run_result = self._run_op(self._store, run_id, tick_id, op)
            if run_result.status == 'busy':
                busy_ops.add(op.op_id)
                continue
            busy_ops.clear()
            if run_result.status == 'already_done':
                continue
            op_result = OpResult(run_result.op_id, run_result.status, run_result.output_refs, run_result.error)
            results.append(op_result)
            if op_result.status != 'ok':
                return TickResult(op_result.status, tuple(results))

    def _tick_concurrent(
        self,
        run_id: str,
        tick_id: int,
        *,
        should_interrupt: TickInterruptionChecker | None,
        op_selector: OpSelector | None,
    ) -> TickResult:
        results: list[OpResult] = []
        stop_requested = False
        busy_ops: set[str] = set()
        terminal_status: TickStatus | None = None
        with ThreadPoolExecutor(max_workers=self._concurrency_limits.max_in_flight) as executor:
            in_flight: dict[Future[_RunOpResult], NextOp] = {}
            while True:
                if not stop_requested and should_interrupt is not None and should_interrupt():
                    stop_requested = True
                    for future in tuple(in_flight):
                        future.cancel()
                if not stop_requested and terminal_status is None:
                    ready = tuple(
                        op
                        for op in self._graph.next_ops(self._store.effective_artifacts(run_id))
                        if (
                            op.op_id not in busy_ops
                            and op.op_id not in {item.op_id for item in in_flight.values()}
                            and (op_selector is None or op_selector(op))
                        )
                    )
                    while len(in_flight) < self._concurrency_limits.max_in_flight:
                        candidates = tuple(candidate for candidate in ready if self._can_submit(candidate, in_flight))
                        op = select_ready_op(candidates)
                        if op is None:
                            break
                        future = executor.submit(self._run_op_in_worker, run_id, tick_id, op)
                        in_flight[future] = op
                        ready = tuple(candidate for candidate in ready if candidate.op_id != op.op_id)
                if not in_flight:
                    if terminal_status is not None:
                        return TickResult(terminal_status, tuple(results))
                    if stop_requested:
                        return TickResult('stopped', tuple(results))
                    return TickResult('idle' if not results else 'ok', tuple(results))

                done, _ = wait(tuple(in_flight), return_when=FIRST_COMPLETED)
                for future in done:
                    op = in_flight.pop(future)
                    if future.cancelled():
                        continue
                    run_result = future.result()
                    if run_result.status == 'busy':
                        busy_ops.add(op.op_id)
                        continue
                    busy_ops.clear()
                    if run_result.status == 'already_done':
                        continue
                    op_result = OpResult(run_result.op_id, run_result.status, run_result.output_refs, run_result.error)
                    results.append(op_result)
                    if op_result.status == 'ok':
                        continue
                    if terminal_status is None:
                        terminal_status = op_result.status
                        stop_requested = True
                        for pending in tuple(in_flight):
                            pending.cancel()

    def _can_submit(self, op: NextOp, in_flight: Mapping[Future[_RunOpResult], NextOp]) -> bool:
        limit = self._concurrency_limits.per_materializer.get(op.base_op_id, self._concurrency_limits.max_in_flight)
        return sum(1 for item in in_flight.values() if item.base_op_id == op.base_op_id) < limit

    def _run_op_in_worker(self, run_id: str, tick_id: int, op: NextOp) -> _RunOpResult:
        store = self._store_factory()
        try:
            return self._run_op(store, run_id, tick_id, op)
        finally:
            store.close()

    def _run_op(self, store: SQLiteArtifactStore, run_id: str, tick_id: int, op: NextOp) -> _RunOpResult:
        output_key_by_name = dict(op.output_key_by_name)
        execution_key = _execution_key(op.op_id, output_key_by_name)
        try:
            input_values = _input_values(store, run_id, op.input_refs)
        except ArtifactStoreCorruptionError as exc:
            return _RunOpResult(op.op_id, 'failed', error=str(exc))
        if input_values is None:
            return _RunOpResult(op.op_id, 'failed', error='input ref is missing')

        materializer = self._materializers.get(op.materializer_id)
        if materializer is None:
            return _RunOpResult(op.op_id, 'failed', error='materializer is missing')

        consumed_refs = _consumed_refs(op.input_refs)
        materialization_key = _materialization_key(op.op_id, output_key_by_name, consumed_refs)
        claim_token = uuid.uuid4().hex
        claim = store.claim_materialization(
            run_id,
            materialization_key,
            tuple(output_key_by_name.values()),
            consumed_refs,
            claim_token=claim_token,
        )
        if claim.status == 'stale':
            return _RunOpResult(op.op_id, 'stale')
        if claim.status == 'already_done':
            return _RunOpResult(op.op_id, 'already_done')
        if claim.status == 'busy':
            return _RunOpResult(op.op_id, 'busy')
        ctx = MaterializerContext(
            run_id,
            op.op_id,
            materialization_key,
            consumed_refs,
            output_key_by_name,
        )
        try:
            try:
                output_values = _materialize(materializer, ctx, input_values, output_key_by_name)
            except MaterializerContractError as exc:
                return _RunOpResult(op.op_id, 'failed', error=str(exc))
            except Exception as exc:
                return _RunOpResult(op.op_id, 'failed', error=str(exc))

            try:
                result = store.commit_outputs(
                    run_id,
                    op.op_id,
                    output_values,
                    consumed_refs,
                    idempotency_key=f'{self._runtime_id}:{tick_id}:{execution_key}',
                )
            except IdempotencyConflictError as exc:
                return _RunOpResult(op.op_id, 'conflict', error=str(exc))

            if result.status == 'stale':
                return _RunOpResult(op.op_id, 'stale')
            return _RunOpResult(op.op_id, 'ok', result.refs)
        finally:
            store.release_materialization(run_id, materialization_key, claim_token=claim_token)


def _input_values(
    store: SQLiteArtifactStore,
    run_id: str,
    input_refs: Mapping[str, ArtifactRef | tuple[ArtifactRef, ...]],
) -> MaterializerInput | None:
    values: dict[str, object | tuple[object, ...]] = {}
    for name, refs in input_refs.items():
        if isinstance(refs, tuple):
            records = tuple(store.get(run_id, ref) for ref in refs)
            if any(record is None for record in records):
                return None
            values[name] = tuple(record.value for record in records)
        else:
            record = store.get(run_id, refs)
            if record is None:
                return None
            values[name] = record.value
    return MappingProxyType(values)


def _materialize(
    materializer: Materializer,
    ctx: MaterializerContext,
    input_values: MaterializerInput,
    output_key_by_name: Mapping[str, ArtifactKey],
) -> dict[ArtifactKey, object]:
    return _output_values(output_key_by_name, materializer(ctx, input_values))


def _consumed_refs(input_refs: Mapping[str, ArtifactRef | tuple[ArtifactRef, ...]]) -> dict[ArtifactKey, ArtifactRef]:
    refs: dict[ArtifactKey, ArtifactRef] = {}
    for value in input_refs.values():
        items = value if isinstance(value, tuple) else (value,)
        refs.update((ref.key, ref) for ref in items)
    return refs


def _output_values(
    output_key_by_name: Mapping[str, ArtifactKey],
    outputs_by_name: MaterializerOutput,
) -> dict[ArtifactKey, object]:
    if set(outputs_by_name) != set(output_key_by_name):
        raise MaterializerContractError('materializer output names must match op outputs')
    return {output_key_by_name[name]: value for name, value in outputs_by_name.items()}
def _execution_key(op_id: str, output_key_by_name: Mapping[str, ArtifactKey]) -> str:
    output_ids = sorted([key.artifact_id, key.partition] for key in output_key_by_name.values())
    payload = json.dumps([op_id, output_ids], separators=(',', ':')).encode()
    return hashlib.sha256(payload).hexdigest()


def _materialization_key(
    op_id: str,
    output_key_by_name: Mapping[str, ArtifactKey],
    input_ref_by_key: Mapping[ArtifactKey, ArtifactRef],
) -> str:
    output_ids = sorted([key.artifact_id, key.partition] for key in output_key_by_name.values())
    input_ids = sorted(
        [ref.key.artifact_id, ref.key.partition, ref.version]
        for ref in input_ref_by_key.values()
    )
    payload = json.dumps([op_id, output_ids, input_ids], separators=(',', ':')).encode()
    return hashlib.sha256(payload).hexdigest()

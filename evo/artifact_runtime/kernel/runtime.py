from __future__ import annotations

import hashlib
import json
import uuid
from collections.abc import Mapping
from dataclasses import dataclass
from types import MappingProxyType
from typing import Literal

from .artifact import ArtifactKey, ArtifactRef
from .errors import ArtifactStoreCorruptionError, IdempotencyConflictError, MaterializerContractError
from .graph import DAGGraph, NextOp
from .materializer import Materializer, MaterializerInput, MaterializerOutput, MaterializerContext
from .store import SQLiteArtifactStore


TickStatus = Literal['idle', 'ok', 'stale', 'failed', 'conflict']
OpStatus = Literal['ok', 'stale', 'failed', 'conflict']


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


class ArtifactRuntime:
    def __init__(self, store: SQLiteArtifactStore, graph: DAGGraph, materializers: Mapping[str, Materializer]) -> None:
        self._store = store
        self._graph = graph
        self._materializers = dict(materializers)
        self._runtime_id = uuid.uuid4().hex
        self._next_tick_id = 0

    def tick(self, run_id: str) -> TickResult:
        tick_id = self._next_tick_id
        self._next_tick_id += 1
        ops = self._graph.next_ops(self._store.effective_artifacts(run_id))
        if not ops:
            return TickResult('idle')

        duplicate = _duplicate_output_key(ops)
        if duplicate is not None:
            return TickResult('failed', (OpResult('', 'failed', error=f'duplicate output key: {duplicate}'),))

        results: list[OpResult] = []
        for op in ops:
            op_result = self._run_op(run_id, tick_id, op)
            results.append(op_result)
            if op_result.status != 'ok':
                return TickResult(op_result.status, tuple(results))
        return TickResult('ok', tuple(results))

    def _run_op(self, run_id: str, tick_id: int, op: NextOp) -> OpResult:
        output_key_by_name = dict(op.output_key_by_name)
        execution_key = _execution_key(op.op_id, output_key_by_name)
        try:
            input_values = _input_values(self._store, run_id, op.input_refs)
        except ArtifactStoreCorruptionError as exc:
            return OpResult(op.op_id, 'failed', error=str(exc))
        if input_values is None:
            return OpResult(op.op_id, 'failed', error='input ref is missing')

        materializer = self._materializers.get(op.materializer_id)
        if materializer is None:
            return OpResult(op.op_id, 'failed', error='materializer is missing')

        consumed_refs = _consumed_refs(op.input_refs)
        materialization_key = _materialization_key(op.op_id, output_key_by_name, consumed_refs)
        claim_token = uuid.uuid4().hex
        claim = self._store.claim_materialization(
            run_id,
            materialization_key,
            tuple(output_key_by_name.values()),
            consumed_refs,
            claim_token=claim_token,
        )
        if claim.status == 'stale':
            return OpResult(op.op_id, 'stale')
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
                return OpResult(op.op_id, 'failed', error=str(exc))
            except Exception as exc:
                return OpResult(op.op_id, 'failed', error=str(exc))

            try:
                result = self._store.commit_outputs(
                    run_id,
                    op.op_id,
                    output_values,
                    consumed_refs,
                    idempotency_key=f'{self._runtime_id}:{tick_id}:{execution_key}',
                )
            except IdempotencyConflictError as exc:
                return OpResult(op.op_id, 'conflict', error=str(exc))

            if result.status == 'stale':
                return OpResult(op.op_id, 'stale')
            return OpResult(op.op_id, 'ok', result.refs)
        finally:
            self._store.release_materialization(run_id, materialization_key, claim_token=claim_token)


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


def _duplicate_output_key(ops: tuple[NextOp, ...]) -> ArtifactKey | None:
    seen: set[ArtifactKey] = set()
    for op in ops:
        for key in op.output_key_by_name.values():
            if key in seen:
                return key
            seen.add(key)
    return None


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

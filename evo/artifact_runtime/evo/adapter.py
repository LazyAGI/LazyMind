from __future__ import annotations

from collections.abc import Callable, Mapping, Sequence
import threading

from ..kernel import (ArtifactKey, ArtifactRef, ArtifactRecord, ArtifactRuntime, DAGGraph,
                      ConcurrencyLimits, FixedOp, Materializer, SQLiteArtifactStore, StoreResult,
                      TickInterruptionChecker, TickResult)


class EvoArtifactAdapter:
    def __init__(
        self,
        store: SQLiteArtifactStore,
        runtime: ArtifactRuntime,
    ) -> None:
        self._store = store
        self._runtime = runtime
        self._owner_thread_id = threading.get_ident()

    def commit_external(self, run_id: str, key: ArtifactKey, value: object, *,
                        idempotency_key: str, expected_ref: ArtifactRef | None = None,
                        metadata: Mapping[str, str] | None = None) -> StoreResult:
        self._require_owner_thread()
        return self._store.commit_external(
            run_id,
            key,
            value,
            idempotency_key=idempotency_key,
            expected_ref=expected_ref,
            metadata=metadata,
        )

    def invalidate(self, run_id: str, keys: Sequence[ArtifactKey] = (), refs: Sequence[ArtifactRef] = (),
                   *, idempotency_key: str) -> StoreResult:
        self._require_owner_thread()
        return self._store.invalidate(run_id, keys, refs, idempotency_key=idempotency_key)

    def delete_artifacts(self, run_id: str, keys: Sequence[ArtifactKey] = (),
                         refs: Sequence[ArtifactRef] = (), *, idempotency_key: str) -> tuple[ArtifactRef, ...]:
        self._require_owner_thread()
        return self._store.delete_artifacts(run_id, keys, refs, idempotency_key=idempotency_key)

    def delete_run(self, run_id: str) -> tuple[ArtifactRef, ...]:
        self._require_owner_thread()
        return self._store.delete_run(run_id)

    def effective_artifacts(self, run_id: str) -> dict[ArtifactKey, ArtifactRef]:
        self._require_owner_thread()
        return self._store.effective_artifacts(run_id)

    def get(self, run_id: str, ref: ArtifactRef) -> ArtifactRecord | None:
        self._require_owner_thread()
        return self._store.get(run_id, ref)

    def tick(self, run_id: str, *, should_interrupt: TickInterruptionChecker | None = None,
             op_selector: Callable[[object], bool] | None = None) -> TickResult:
        self._require_owner_thread()
        return self._runtime.tick(
            run_id,
            should_interrupt=should_interrupt,
            op_selector=op_selector,
        )

    def _require_owner_thread(self) -> None:
        if threading.get_ident() != self._owner_thread_id:
            raise RuntimeError('EvoArtifactAdapter must only be used from its owner thread')


def build_evo_artifact_adapter(store: SQLiteArtifactStore, ops: Sequence[type[FixedOp]],
                               materializers: Mapping[str, Materializer], *,
                               concurrency_limits: ConcurrencyLimits | None = None,
                               ) -> EvoArtifactAdapter:
    graph = DAGGraph()
    for op in ops:
        graph.register(op)
    graph.validate()
    return EvoArtifactAdapter(
        store,
        ArtifactRuntime(store, graph, materializers, concurrency_limits=concurrency_limits),
    )


__all__ = ['EvoArtifactAdapter', 'build_evo_artifact_adapter']

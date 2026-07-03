from __future__ import annotations

from collections.abc import Mapping, Sequence

from ..kernel import (ArtifactKey, ArtifactRef, ArtifactRecord, ArtifactRuntime, DAGGraph,
                      FixedOp, Materializer, SQLiteArtifactStore, StoreResult, TickResult)


class EvoArtifactAdapter:
    def __init__(self, store: SQLiteArtifactStore, runtime: ArtifactRuntime) -> None:
        self._store = store
        self._runtime = runtime

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

    def tick(self, run_id: str) -> TickResult:
        self._require_owner_thread()
        return self._runtime.tick(run_id)

    def _require_owner_thread(self) -> None:
        return None


def build_evo_artifact_adapter(store: SQLiteArtifactStore, ops: Sequence[type[FixedOp]],
                               materializers: Mapping[str, Materializer]
                               ) -> EvoArtifactAdapter:
    graph = DAGGraph()
    for op in ops:
        graph.register(op)
    graph.validate()
    return EvoArtifactAdapter(store, ArtifactRuntime(store, graph, materializers))


__all__ = ['EvoArtifactAdapter', 'build_evo_artifact_adapter']

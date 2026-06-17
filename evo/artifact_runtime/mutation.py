from __future__ import annotations

from collections.abc import Mapping
from dataclasses import dataclass, field
from threading import RLock
from types import MappingProxyType
from typing import Any, Literal, Protocol

from .artifact import ArtifactKey, ArtifactRef
from .store import ArtifactCommitOutcome
from .store import ArtifactStore
from .utils import validate_nonempty

MutationStatus = Literal['applied', 'failed']


@dataclass(frozen=True)
class ArtifactMutationRequest:
    command_id: str
    artifact: ArtifactKey
    value: Any
    expected_ref: ArtifactRef | None = None
    create_only: bool = False
    reason: str = 'artifact_mutation'
    metadata: Mapping[str, Any] = field(default_factory=lambda: MappingProxyType({}))

    def __post_init__(self) -> None:
        validate_nonempty(self.command_id, 'command_id')
        object.__setattr__(self, 'metadata', _freeze_mapping(self.metadata))


@dataclass(frozen=True)
class ArtifactMutationResult:
    status: MutationStatus
    artifact: ArtifactKey
    ref: ArtifactRef | None = None
    reason: str = ''


class MutationLog(Protocol):
    def get(self, command_id: str) -> ArtifactMutationResult | None:
        ...

    def record(self, command_id: str, result: ArtifactMutationResult) -> None:
        ...


class InMemoryMutationLog:
    def __init__(self) -> None:
        self._results: dict[str, ArtifactMutationResult] = {}

    def get(self, command_id: str) -> ArtifactMutationResult | None:
        return self._results.get(command_id)

    def record(self, command_id: str, result: ArtifactMutationResult) -> None:
        self._results[command_id] = result


class ArtifactMutationService:
    """Single-process mutation coordinator; durable idempotency belongs in FC-7."""

    def __init__(self, store: ArtifactStore, *, log: MutationLog | None = None) -> None:
        self.store = store
        self.log = log or InMemoryMutationLog()
        self._lock = RLock()

    def mutate(self, request: ArtifactMutationRequest) -> ArtifactMutationResult:
        # Coarse-grained FC-4 idempotency lock: protects get -> side effect -> record for this service instance only.
        with self._lock:
            if seen := self.log.get(request.command_id):
                return seen

            outcome = self.store.put_source_once(
                request.command_id,
                request.artifact,
                request.value,
                expected_ref=request.expected_ref,
                create_only=request.create_only,
                metadata=_mutation_metadata(request),
            )
            result = _mutation_result(request.artifact, outcome)
            self.log.record(request.command_id, result)
            return result


def _mutation_metadata(request: ArtifactMutationRequest) -> Mapping[str, Any]:
    metadata = dict(request.metadata)
    metadata.update(
        {
            'mutation_command_id': request.command_id,
            'mutation_reason': request.reason,
            'patch_source': str(metadata.get('patch_source') or 'intervention'),
        }
    )
    return MappingProxyType(metadata)


def _mutation_result(key: ArtifactKey, outcome: ArtifactCommitOutcome) -> ArtifactMutationResult:
    if outcome.status == 'committed':
        return ArtifactMutationResult('applied', key, dict(outcome.output_refs).get(key), outcome.reason)
    return ArtifactMutationResult('failed', key, reason=outcome.reason or outcome.status)


def _freeze_mapping(values: Mapping[Any, Any]) -> Mapping[Any, Any]:
    return MappingProxyType(dict(values))

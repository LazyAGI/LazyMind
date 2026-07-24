from __future__ import annotations

from importlib import import_module


__all__ = [
    'ArtifactEvent',
    'ArtifactInput',
    'ArtifactKernelError',
    'ArtifactKey',
    'ArtifactOutput',
    'ArtifactRecord',
    'ArtifactRef',
    'ArtifactRuntime',
    'ArtifactStoreCorruptionError',
    'ClaimResult',
    'CycleError',
    'DAGGraph',
    'DAGGraphError',
    'DuplicateArtifactWriterError',
    'DuplicateOpError',
    'FixedOp',
    'IdempotencyConflictError',
    'Materializer',
    'MaterializerContractError',
    'MaterializerContext',
    'MaterializerError',
    'NextOp',
    'OpResult',
    'SQLiteArtifactStore',
    'StaticPartitions',
    'StoreResult',
    'ConcurrencyLimits',
    'TickInterruptionChecker',
    'TickResult',
    'Unpartitioned',
    'all_to_unpartitioned',
    'same_partition',
    'unpartitioned_to_all',
]

_EXPORTS = {
    'ArtifactEvent': '.store',
    'ArtifactInput': '.artifact',
    'ArtifactKernelError': '.errors',
    'ArtifactKey': '.artifact',
    'ArtifactOutput': '.artifact',
    'ArtifactRecord': '.store',
    'ArtifactRef': '.artifact',
    'ArtifactRuntime': '.runtime',
    'ArtifactStoreCorruptionError': '.errors',
    'ClaimResult': '.store',
    'CycleError': '.errors',
    'DAGGraph': '.graph',
    'DAGGraphError': '.errors',
    'DuplicateArtifactWriterError': '.errors',
    'DuplicateOpError': '.errors',
    'FixedOp': '.ops',
    'IdempotencyConflictError': '.errors',
    'Materializer': '.materializer',
    'MaterializerContractError': '.errors',
    'MaterializerContext': '.materializer',
    'MaterializerError': '.errors',
    'NextOp': '.graph',
    'OpResult': '.runtime',
    'SQLiteArtifactStore': '.store',
    'StaticPartitions': '.partition',
    'StoreResult': '.store',
    'ConcurrencyLimits': '.scheduler',
    'TickInterruptionChecker': '.runtime',
    'TickResult': '.runtime',
    'Unpartitioned': '.partition',
    'all_to_unpartitioned': '.partition',
    'same_partition': '.partition',
    'unpartitioned_to_all': '.partition',
}


def __getattr__(name: str):
    if name not in _EXPORTS:
        raise AttributeError(f'module {__name__!r} has no attribute {name!r}')
    value = getattr(import_module(_EXPORTS[name], __name__), name)
    globals()[name] = value
    return value


def __dir__() -> list[str]:
    return sorted((*globals(), *__all__))

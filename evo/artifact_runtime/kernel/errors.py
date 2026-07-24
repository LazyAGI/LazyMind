class ArtifactKernelError(Exception):
    pass


class DAGGraphError(ArtifactKernelError, ValueError):
    pass


class CycleError(DAGGraphError):
    pass


class DuplicateArtifactWriterError(DAGGraphError):
    pass


class DuplicateOpError(DAGGraphError):
    pass


class IdempotencyConflictError(ArtifactKernelError, RuntimeError):
    pass


class ArtifactStoreCorruptionError(ArtifactKernelError, RuntimeError):
    pass


class MaterializerError(ArtifactKernelError, RuntimeError):
    pass


class MaterializerContractError(MaterializerError):
    pass

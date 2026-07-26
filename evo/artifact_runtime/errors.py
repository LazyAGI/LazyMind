class ArtifactRuntimeError(Exception):
    """Base error for the artifact runtime."""


class DefinitionError(ArtifactRuntimeError, ValueError):
    """Raised when artifact or operation declarations are invalid."""


class PlanningError(ArtifactRuntimeError, RuntimeError):
    """Raised when an artifact snapshot cannot be planned."""


class OperationExecutionError(ArtifactRuntimeError, RuntimeError):
    """Raised when an operation execution unit fails."""


__all__ = ['ArtifactRuntimeError', 'DefinitionError', 'OperationExecutionError', 'PlanningError']

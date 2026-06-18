class DAGGraphError(ValueError):
    pass


class DuplicateOpError(DAGGraphError):
    pass


class CycleError(DAGGraphError):
    pass


class DuplicateArtifactWriterError(DAGGraphError):
    pass


class MissingArtifactVersionError(DAGGraphError):
    pass


class UnknownTargetError(DAGGraphError):
    pass

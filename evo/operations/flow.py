from evo import artifacts as A
from evo.artifact_flow import FlowDefinition, FlowStage
from evo.artifact_runtime import ArtifactKey

from .operation import evo_operations


def evo_flow_definition() -> FlowDefinition:
    return FlowDefinition(
        evo_operations(),
        tuple(
            FlowStage(
                stage,
                ArtifactKey.scalar(A.ROOTS[stage]),
                None if stage not in A.APPROVALS else ArtifactKey.scalar(
                    A.APPROVALS[stage]
                ),
            )
            for stage in A.STEPS
        ),
    )


__all__ = ['evo_flow_definition']

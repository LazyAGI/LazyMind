from dataclasses import dataclass
from typing import TypeAlias

from evo.artifact_runtime.evo.actions import EvoMutation


@dataclass(frozen=True)
class ContinueFlow:
    command_id: str
    until_step: str = ''

    def __post_init__(self) -> None:
        _require_command_id(self.command_id)
        if not isinstance(self.until_step, str):
            raise TypeError('until_step must be str')


@dataclass(frozen=True)
class PauseFlow:
    command_id: str

    def __post_init__(self) -> None:
        _require_command_id(self.command_id)


@dataclass(frozen=True)
class ResumeFlow:
    command_id: str

    def __post_init__(self) -> None:
        _require_command_id(self.command_id)


@dataclass(frozen=True)
class CancelFlow:
    command_id: str

    def __post_init__(self) -> None:
        _require_command_id(self.command_id)


@dataclass(frozen=True)
class RetryFlow:
    command_id: str

    def __post_init__(self) -> None:
        _require_command_id(self.command_id)


@dataclass(frozen=True)
class ApplyArtifactMutation:
    command_id: str
    mutation: EvoMutation

    def __post_init__(self) -> None:
        _require_command_id(self.command_id)
        if not isinstance(self.mutation, EvoMutation):
            raise TypeError('mutation must be EvoMutation')


FlowCommand: TypeAlias = (
    ContinueFlow
    | PauseFlow
    | ResumeFlow
    | CancelFlow
    | RetryFlow
    | ApplyArtifactMutation
)


def _require_command_id(value: str) -> None:
    if not isinstance(value, str):
        raise TypeError('command_id must be str')
    if not value.strip():
        raise ValueError('command_id must be non-empty')


__all__ = [
    'ApplyArtifactMutation',
    'CancelFlow',
    'ContinueFlow',
    'FlowCommand',
    'PauseFlow',
    'ResumeFlow',
    'RetryFlow',
]

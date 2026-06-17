from __future__ import annotations

from collections.abc import Mapping
from typing import Any, ClassVar

from .artifact import ArtifactInput, ArtifactOutput, ArtifactPayload


class FixedOp:
    op_id: ClassVar[str] = ''
    inputs: ClassVar[Mapping[str, ArtifactInput]] = {}
    outputs: ClassVar[Mapping[str, ArtifactOutput]] = {}
    flow: ClassVar[str] = ''
    stage: ClassVar[str] = ''
    tags: ClassVar[Mapping[str, str]] = {}

    @classmethod
    def execute(cls, inputs: dict[str, ArtifactPayload], ctx: Any) -> dict[str, ArtifactPayload]:
        raise NotImplementedError(f'{cls.__name__}.execute is not implemented')

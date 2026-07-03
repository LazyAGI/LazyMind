from __future__ import annotations

from collections.abc import Mapping
from types import MappingProxyType
from typing import ClassVar

from .artifact import ArtifactInput, ArtifactOutput


class FixedOp:
    op_id: ClassVar[str] = ''
    inputs: ClassVar[Mapping[str, ArtifactInput]] = MappingProxyType({})
    outputs: ClassVar[Mapping[str, ArtifactOutput]] = MappingProxyType({})

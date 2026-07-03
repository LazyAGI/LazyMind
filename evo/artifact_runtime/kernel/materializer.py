from __future__ import annotations

from collections.abc import Callable, Mapping
from dataclasses import dataclass
from types import MappingProxyType

from .artifact import ArtifactKey, ArtifactRef


MaterializerInput = Mapping[str, object]
MaterializerOutput = Mapping[str, object]
Materializer = Callable[['MaterializerContext', MaterializerInput], MaterializerOutput]


@dataclass(frozen=True)
class MaterializerContext:
    run_id: str
    op_id: str
    materialization_key: str
    input_ref_by_key: Mapping[ArtifactKey, ArtifactRef]
    output_key_by_name: Mapping[str, ArtifactKey]

    def __post_init__(self) -> None:
        object.__setattr__(self, 'input_ref_by_key', MappingProxyType(dict(self.input_ref_by_key)))
        object.__setattr__(self, 'output_key_by_name', MappingProxyType(dict(self.output_key_by_name)))

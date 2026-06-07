from __future__ import annotations

from dataclasses import dataclass


@dataclass(frozen=True)
class CheckpointRef:
    checkpoint_id: str

    def __str__(self) -> str:
        return self.checkpoint_id

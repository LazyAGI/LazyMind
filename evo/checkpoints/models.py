from __future__ import annotations

from dataclasses import dataclass
from typing import Literal


RESUME_FROM_SNAPSHOT = 'resume_from_snapshot'
RESUME_WITH_INTERVENTIONS = 'resume_with_interventions'
ResumeInputPolicy = Literal['resume_from_snapshot', 'resume_with_interventions']


@dataclass(frozen=True)
class CheckpointRef:
    checkpoint_id: str

    def __str__(self) -> str:
        return self.checkpoint_id

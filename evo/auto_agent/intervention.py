from __future__ import annotations

from typing import Any, Literal

from pydantic import BaseModel, ConfigDict

AutoInterventionKind = Literal['rerun_case', 'patch_judge_score']


class AutoIntervention(BaseModel):
    model_config = ConfigDict(extra='forbid', strict=True)

    kind: AutoInterventionKind
    case_id: str
    field: str = ''
    value: Any = None
    source_ref: str = ''

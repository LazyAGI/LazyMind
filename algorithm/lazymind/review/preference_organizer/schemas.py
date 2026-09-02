from __future__ import annotations

from typing import List, Literal, Optional

from pydantic import BaseModel, ConfigDict, Field


class PreferenceOrganizerError(BaseModel):
    model_config = ConfigDict(extra='forbid')

    code: str
    message: str


class PreferenceStateData(BaseModel):
    model_config = ConfigDict(extra='forbid')

    stored_items: int
    full_projection_chars: int
    projected_items: int
    projected_chars: int
    projection_truncated: bool
    etag: str


class PreferenceOrganizerPassResult(BaseModel):
    model_config = ConfigDict(extra='forbid')

    pass_number: int
    plan_hash: str = ''
    before: PreferenceStateData
    after: PreferenceStateData
    changes: int = 0
    operation_count: int = 0
    outcome: str


class PreferenceOrganizerResultData(BaseModel):
    model_config = ConfigDict(extra='forbid')

    current_pass: int = 0
    passes_attempted: int = 0
    passes: List[PreferenceOrganizerPassResult] = Field(default_factory=list)
    total_changes: int = 0
    outcome: str
    reason: str = ''
    target_reached: bool = False
    stop_reason: str = ''


class PreferenceOrganizerResult(BaseModel):
    model_config = ConfigDict(extra='forbid')

    status: Literal['success', 'failed']
    task_id: str
    outcome: Literal[
        'organized',
        'organized_with_remaining',
        'no_safe_changes',
        'budget_exhausted',
        'stale_state',
        'partial',
        'failed',
    ]
    retryable: bool = False
    result: Optional[PreferenceOrganizerResultData] = None
    error: Optional[PreferenceOrganizerError] = None

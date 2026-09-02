from __future__ import annotations

from typing import Any, Dict, Optional

from fastapi import APIRouter
from fastapi.responses import JSONResponse
from lazyllm import LOG
from pydantic import BaseModel, ConfigDict, Field, model_validator

from lazymind.review.preference_organizer.schemas import PreferenceOrganizerResult

router = APIRouter()


class PreferenceOrganizerPayload(BaseModel):
    model_config = ConfigDict(extra='forbid')

    task_id: str
    user_id: str
    llm_config: Optional[Dict[str, Any]] = None
    target_items: int = Field(30, ge=1, le=100)
    min_items: int = Field(20, ge=1, le=100)
    hard_min_items: int = Field(15, ge=1, le=100)
    max_items: int = Field(40, ge=1, le=100)
    target_prompt_percent: int = Field(40, ge=1, le=100)
    max_changes: int = Field(50, ge=1, le=100)
    max_passes: int = Field(2, ge=1, le=2)
    max_rounds_per_pass: int = Field(60, ge=1, le=60)

    @model_validator(mode='after')
    def validate_payload(self) -> 'PreferenceOrganizerPayload':
        self.task_id = str(self.task_id or '').strip()
        self.user_id = str(self.user_id or '').strip()
        if not self.task_id.startswith('preference_organizer_'):
            raise ValueError("task_id must start with 'preference_organizer_'.")
        if not self.user_id:
            raise ValueError('user_id must be non-empty.')
        if not self.hard_min_items <= self.min_items <= self.target_items <= self.max_items:
            raise ValueError(
                'item limits must satisfy hard_min_items <= min_items <= '
                'target_items <= max_items.'
            )
        return self


@router.post(
    '/api/chat/preference_organize',
    summary='Organize the complete Preference index in at most two gated passes',
    response_model=PreferenceOrganizerResult,
    response_model_exclude_none=True,
)
async def preference_organize(payload: PreferenceOrganizerPayload):
    from lazymind.review.service.preference_organizer import organize_preferences

    try:
        result = organize_preferences(**payload.model_dump())
    except Exception as exc:
        LOG.exception(f'[PreferenceOrganizer] unexpected failure: {exc}')
        return JSONResponse(
            status_code=500,
            content={
                'status': 'failed',
                'task_id': payload.task_id,
                'outcome': 'failed',
                'retryable': False,
                'error': {
                    'code': 'internal_error',
                    'message': 'Preference Organizer failed unexpectedly.',
                },
            },
        )
    return result.model_dump(exclude_none=True)

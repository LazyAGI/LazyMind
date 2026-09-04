from __future__ import annotations

from typing import Any, Dict, Optional

from fastapi import APIRouter, HTTPException, Request
from fastapi.responses import JSONResponse
from lazyllm import LOG
from pydantic import BaseModel, ConfigDict, Field, model_validator

from lazymind.review.preference_organizer.schemas import PreferenceOrganizerResult

from lazymind.common.maintenance import execute

router = APIRouter()


class PreferenceOrganizerPayload(BaseModel):
    model_config = ConfigDict(extra='forbid')

    run_id: str = Field(..., min_length=1, pattern=r'\S')
    task_id: str
    user_id: str
    llm_config: Optional[Dict[str, Any]] = None
    force_analysis: bool = False

    @model_validator(mode='after')
    def validate_payload(self) -> 'PreferenceOrganizerPayload':
        self.task_id = str(self.task_id or '').strip()
        self.user_id = str(self.user_id or '').strip()
        if not self.task_id.startswith('preference_organizer_'):
            raise ValueError("task_id must start with 'preference_organizer_'.")
        if not self.user_id:
            raise ValueError('user_id must be non-empty.')
        return self


@router.post(
    '/api/chat/preference_organize',
    summary='Organize the complete Preference index in at most two gated passes',
    response_model=PreferenceOrganizerResult,
    response_model_exclude_none=True,
)
async def preference_organize(payload: PreferenceOrganizerPayload, request: Request = None):
    from lazymind.review.service.preference_organizer import organize_preferences

    try:
        result = await execute(request, organize_preferences, timeout=1800, **payload.model_dump())
    except HTTPException:
        raise
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

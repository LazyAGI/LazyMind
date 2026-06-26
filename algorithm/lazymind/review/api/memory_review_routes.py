from __future__ import annotations

from typing import Any, Dict, List, Optional

from fastapi import APIRouter
from fastapi.responses import JSONResponse
from lazyllm import LOG
from pydantic import BaseModel, ConfigDict, Field, model_validator

from lazymind.review.service.memory_review import MemoryReviewResult, review_memory

router = APIRouter()


class MemoryReviewPayload(BaseModel):
    model_config = ConfigDict(extra='forbid')

    user_id: str = Field(..., description='Backend user ID being reviewed')
    history: List[Dict[str, Any]] = Field(
        default_factory=list,
        description='Chat history passed by backend for review',
    )
    memory: str = Field(
        default='',
        description='Current full agent memory text to edit',
    )
    user: str = Field(
        default='',
        description='Current full user profile text to edit',
    )
    llm_config: Optional[Dict[str, Any]] = Field(
        None,
        description=(
            'Optional per-request model configuration loaded by core for the current user. '
            'When omitted, the active runtime_models configuration is used.'
        ),
    )

    @model_validator(mode='after')
    def validate_payload(self) -> 'MemoryReviewPayload':
        self.user_id = str(self.user_id).strip()
        if not self.user_id:
            raise ValueError("'user_id' must be non-empty.")
        if not any(
            message.get('role') == 'user'
            and str(message.get('content', '')).strip()
            for message in self.history
        ):
            raise ValueError("'history' must contain at least one user message.")
        return self


@router.post(
    '/api/chat/memory_review',
    summary='Review backend-provided history for memory or user_preference edits',
    response_model=MemoryReviewResult,
)
async def memory_review(payload: MemoryReviewPayload):
    try:
        result = review_memory(
            user_id=payload.user_id,
            history=payload.history,
            memory=payload.memory,
            user=payload.user,
            llm_config=payload.llm_config,
        )
    except Exception as exc:
        LOG.exception(f'[MemoryReview] memory review failed: {exc}')
        return JSONResponse(status_code=500, content={'status': 'failed'})
    return result.model_dump()

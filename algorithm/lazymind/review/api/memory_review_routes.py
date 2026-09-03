from __future__ import annotations

from typing import Any, Dict, List, Literal, Optional

from fastapi import APIRouter, HTTPException, Request
from fastapi.responses import JSONResponse
from lazyllm import LOG
from pydantic import BaseModel, ConfigDict, Field, field_validator, model_validator

from lazymind.common.maintenance import execute

router = APIRouter()


class MemoryReviewPayload(BaseModel):
    model_config = ConfigDict(extra='forbid')

    run_id: str = Field(..., min_length=1, pattern=r'\S')
    task_id: str = Field(..., description='Core resource update task ID for this review run')
    user_id: str = Field(..., description='Backend user ID being reviewed')
    conversation_id: str = Field(..., description='Source conversation ID being reviewed')
    history: List[Dict[str, Any]] = Field(
        default_factory=list,
        description='Chat history passed by backend for review',
    )
    conversation_last_active_at_ms: Optional[int] = Field(
        None,
        description='Optional conversation last-active Unix timestamp in milliseconds',
    )
    llm_config: Optional[Dict[str, Any]] = Field(
        None,
        description=(
            'Optional per-request model configuration loaded by core for the current user. '
            'When omitted, the active runtime_models configuration is used.'
        ),
    )

    @field_validator('conversation_last_active_at_ms', mode='before')
    @classmethod
    def normalize_conversation_last_active_at_ms(cls, value):
        if isinstance(value, bool) or (value is not None and not isinstance(value, int)):
            return None
        return value

    @model_validator(mode='before')
    @classmethod
    def preserve_missing_conversation_as_business_error(cls, data):
        if isinstance(data, dict) and ('conversation_id' not in data or data.get('conversation_id') is None):
            data = dict(data)
            data['conversation_id'] = ''
        return data

    @model_validator(mode='after')
    def validate_payload(self) -> 'MemoryReviewPayload':
        self.task_id = str(self.task_id).strip()
        if not self.task_id:
            raise ValueError("'task_id' must be non-empty.")
        if not self.task_id.startswith('memory_review_'):
            raise ValueError("'task_id' must start with 'memory_review_'.")
        self.user_id = str(self.user_id).strip()
        if not self.user_id:
            raise ValueError("'user_id' must be non-empty.")
        self.conversation_id = str(self.conversation_id).strip()
        if not any(
            message.get('role') == 'user' and str(message.get('content', '')).strip() for message in self.history
        ):
            raise ValueError("'history' must contain at least one user message.")
        return self


class MemoryReviewError(BaseModel):
    model_config = ConfigDict(extra='forbid')

    code: str
    message: str


class MemoryReviewResult(BaseModel):
    model_config = ConfigDict(extra='forbid')

    status: Literal['success', 'failed']
    task_id: str
    outcome: Literal['saved', 'no_changes', 'partial', 'failed']
    retryable: bool = False
    error: Optional[MemoryReviewError] = None


@router.post(
    '/api/chat/memory_review',
    summary='Review backend-provided history for persistent memory updates',
    response_model=MemoryReviewResult,
    response_model_exclude_none=True,
)
async def memory_review(payload: MemoryReviewPayload, request: Request = None):
    if not payload.conversation_id:
        return MemoryReviewResult(
            status='failed',
            task_id=payload.task_id,
            outcome='failed',
            retryable=False,
            error={
                'code': 'missing_context',
                'message': 'conversation_id is required.',
            },
        ).model_dump(exclude_none=True)

    from lazymind.review.service.memory_review import review_memory

    try:
        result = await execute(
            request,
            review_memory,
            timeout=600,
            run_id=payload.run_id,
            task_id=payload.task_id,
            user_id=payload.user_id,
            conversation_id=payload.conversation_id,
            history=payload.history,
            llm_config=payload.llm_config,
            conversation_last_active_at_ms=payload.conversation_last_active_at_ms,
        )
    except HTTPException:
        raise
    except Exception as exc:
        LOG.exception(f'[MemoryReview] memory review failed: {exc}')
        return JSONResponse(
            status_code=500,
            content={
                'status': 'failed',
                'task_id': payload.task_id,
                'outcome': 'failed',
                'retryable': False,
                'error': {
                    'code': 'internal_error',
                    'message': 'Memory Review failed unexpectedly.',
                },
            },
        )
    return result.model_dump(exclude_none=True)

from __future__ import annotations

from typing import List, Optional

from fastapi import APIRouter, HTTPException
from pydantic import BaseModel, ConfigDict, Field
from pydantic import model_validator

from lazymind.review.service.memory_generate import (
    BadRequestError,
    MemoryType,
    UnprocessableContentError,
    generate_memory_content,
)

router = APIRouter()


class SuggestionPayload(BaseModel):
    model_config = ConfigDict(extra='forbid')

    title: str = Field(..., description='Suggestion title')
    content: str = Field(..., description='Natural language modification suggestion')
    reason: Optional[str] = Field(default=None, description='Reason for the suggestion')
    outdated: Optional[bool] = Field(default=None, description='Whether the suggestion is outdated')


class GeneratePayload(BaseModel):
    model_config = ConfigDict(extra='forbid')

    content: str = Field(..., description='Current full text of the target content')
    suggestions: Optional[List[SuggestionPayload]] = Field(
        default=None,
        description='List of suggestions to merge',
    )
    user_instruct: Optional[str] = Field(default=None, description='Natural language instruction directly from the user')  # noqa: E501

    @model_validator(mode='after')
    def validate_generation_inputs(self) -> 'GeneratePayload':
        has_suggestions = bool(self.suggestions)
        has_user_instruct = bool(self.user_instruct and self.user_instruct.strip())
        if not has_suggestions and not has_user_instruct:
            raise ValueError("At least one of 'suggestions' or 'user_instruct' must be provided.")
        return self


def _handle_generate(memory_type: MemoryType, payload: GeneratePayload):
    try:
        generated = generate_memory_content(
            memory_type=memory_type,
            content=payload.content,
            suggestions=[s.model_dump() for s in payload.suggestions] if payload.suggestions else None,
            user_instruct=payload.user_instruct,
        )
        return {'content': generated}
    except BadRequestError as exc:
        raise HTTPException(status_code=400, detail=str(exc)) from exc
    except UnprocessableContentError as exc:
        raise HTTPException(status_code=422, detail=str(exc)) from exc
    except Exception as exc:
        raise HTTPException(status_code=500, detail=f'generate failed: {exc}') from exc


@router.post('/api/chat/skill/generate', summary='Generate new skill content')
async def generate_skill(payload: GeneratePayload):
    return _handle_generate('skill', payload)


@router.post('/api/chat/memory/generate', summary='Generate new memory content')
async def generate_memory(payload: GeneratePayload):
    return _handle_generate('memory', payload)


@router.post('/api/chat/user_preference/generate', summary='Generate new user_preference content')
async def generate_user_preference(payload: GeneratePayload):
    return _handle_generate('user_preference', payload)

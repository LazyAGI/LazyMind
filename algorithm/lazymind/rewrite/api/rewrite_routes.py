from __future__ import annotations

from typing import Any, Dict
from uuid import uuid4

from fastapi import APIRouter, HTTPException
import lazyllm
from pydantic import BaseModel, ConfigDict, Field
from pydantic import model_validator

from lazymind.model_config import inject_model_config
from lazymind.rewrite import (
    BadRequestError,
    RewriteTaskType,
    UnprocessableContentError,
    rewrite_content,
    rewrite_editable_selection,
)

router = APIRouter()


class RewritePayload(BaseModel):
    model_config = ConfigDict(extra='forbid')

    task_type: RewriteTaskType = Field(..., description='Rewrite task type')
    content: str = Field(..., description='Current full text of the target content')
    user_instruct: str = Field(..., description='Natural language instruction directly from the user')
    llm_config: Dict[str, Any] = Field(
        ...,
        description='Per-request model configuration loaded by core for the current user',
    )
    full_content: str | None = None
    selection_start: int | None = None
    selection_end: int | None = None

    @model_validator(mode='after')
    def validate_inputs(self) -> 'RewritePayload':
        has_user_instruct = bool(self.user_instruct and self.user_instruct.strip())
        if not has_user_instruct:
            raise ValueError("'user_instruct' must be a non-empty string.")
        return self


def _init_session(task_type: RewriteTaskType, model_config: Dict[str, Any]) -> None:
    session_id = f'{task_type}_rewrite_{uuid4().hex}'
    lazyllm.globals._init_sid(sid=session_id)
    lazyllm.locals._init_sid(sid=session_id)
    inject_model_config(model_config)


@router.post('/api/chat/rewrite', summary='Rewrite a skill draft or polish a prompt with an LLM')
async def rewrite(payload: RewritePayload):
    try:
        _init_session(payload.task_type, payload.llm_config)
        if payload.task_type == 'polish' and payload.full_content is not None:
            start = payload.selection_start
            end = payload.selection_end
            if start is None or end is None or not (0 <= start < end <= len(payload.full_content)):
                raise BadRequestError('valid selection_start and selection_end are required')
            if payload.full_content[start:end] != payload.content:
                raise BadRequestError('selection offsets do not match content')
            return rewrite_editable_selection(
                payload.full_content, start, end, payload.user_instruct,
            )
        generated = rewrite_content(
            task_type=payload.task_type,
            content=payload.content,
            user_instruct=payload.user_instruct,
        )
        return {'content': generated}
    except BadRequestError as exc:
        raise HTTPException(status_code=400, detail=str(exc)) from exc
    except UnprocessableContentError as exc:
        raise HTTPException(status_code=422, detail=str(exc)) from exc
    except Exception as exc:
        raise HTTPException(status_code=500, detail=f'rewrite failed: {exc}') from exc

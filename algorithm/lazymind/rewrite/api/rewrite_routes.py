from __future__ import annotations

from typing import Any, Dict
from uuid import uuid4

from fastapi import APIRouter, HTTPException
import lazyllm
from pydantic import BaseModel, ConfigDict, Field
from pydantic import model_validator

from lazymind.model_config import inject_model_config
from lazymind.rewrite.selection import rewrite_ranges, validate_ranges
from lazymind.rewrite import (
    BadRequestError,
    RewriteTaskType,
    UnprocessableContentError,
    rewrite_content,
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
    selection_ranges: list[Dict[str, Any]] | None = None

    @model_validator(mode='after')
    def validate_inputs(self) -> 'RewritePayload':
        has_user_instruct = bool(self.user_instruct and self.user_instruct.strip())
        if not has_user_instruct:
            raise ValueError("'user_instruct' must be a non-empty string.")
        if self.selection_ranges is not None or self.full_content is not None:
            if self.task_type != 'polish' or self.full_content is None or self.selection_ranges is None:
                raise ValueError('selection requires polish, full_content and selection_ranges')
            if 'llm' in self.llm_config and not isinstance(self.llm_config['llm'], dict):
                raise ValueError('llm_config.llm must be a model configuration object')
            validate_ranges(self.full_content, self.selection_ranges)
        return self


def _init_session(task_type: RewriteTaskType, model_config: Dict[str, Any]) -> None:
    session_id = f'{task_type}_rewrite_{uuid4().hex}'
    lazyllm.globals._init_sid(sid=session_id)
    lazyllm.locals._init_sid(sid=session_id)
    inject_model_config(model_config)


@router.post('/api/chat/rewrite', summary='Rewrite a skill draft or polish a prompt with an LLM')
def rewrite(payload: RewritePayload):
    try:
        _init_session(payload.task_type, payload.llm_config)
        if payload.task_type == 'polish' and payload.full_content is not None:
            return rewrite_ranges(payload.full_content, payload.selection_ranges, payload.user_instruct)
        generated = rewrite_content(
            task_type=payload.task_type,
            content=payload.content,
            user_instruct=payload.user_instruct,
        )
        return {'content': generated}
    except BadRequestError as exc:
        raise HTTPException(status_code=400, detail=str(exc)) from exc
    except UnprocessableContentError as exc:
        status = 502 if payload.full_content is not None else 422
        raise HTTPException(status_code=status, detail=str(exc)) from exc
    except Exception as exc:
        raise HTTPException(status_code=502, detail='rewrite model call failed') from exc

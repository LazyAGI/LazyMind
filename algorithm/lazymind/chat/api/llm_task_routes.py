from __future__ import annotations

import asyncio
import logging

from fastapi import APIRouter, HTTPException

from lazymind.chat.service.llm_task import LLMTaskRequest, LLMTaskResult, run_llm_task


router = APIRouter()
_logger = logging.getLogger(__name__)


@router.post(
    '/api/chat/llm-task:run',
    response_model=LLMTaskResult,
    summary='Run a non-streaming platform LLM or Agent task',
)
async def llm_task_run(request: LLMTaskRequest) -> LLMTaskResult:
    result = await asyncio.to_thread(run_llm_task, request)
    if result.status == 'failed':
        _logger.warning(
            'llm_task_failed task_type=%s task_id=%s error=%s',
            request.task_type,
            result.task_id,
            result.error,
        )
        raise HTTPException(status_code=502, detail=result.error or 'llm task failed')
    return result

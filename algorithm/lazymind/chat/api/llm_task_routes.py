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
    structured_failures = {'conversation.describe_opening', 'conversation.organize_step'}
    if result.status == 'failed' and request.task_type not in structured_failures:
        _logger.warning(
            'llm_task_failed task_type=%s task_id=%s error=%s',
            request.task_type,
            result.task_id,
            result.error,
        )
        raise HTTPException(status_code=502, detail=result.error or 'llm task failed')
    return result


@router.post('/api/chat/organizer-executions/{execution_id}:stream')
async def organizer_stream(execution_id: str, request: LLMTaskRequest):
    from lazymind.chat.service.organizer_stream import stream_execution
    return await stream_execution(execution_id, request)


@router.post('/api/chat/organizer-executions/{execution_id}:cancel')
async def organizer_cancel(execution_id: str):
    from lazymind.chat.service.organizer_stream import cancel_execution
    return await cancel_execution(execution_id)

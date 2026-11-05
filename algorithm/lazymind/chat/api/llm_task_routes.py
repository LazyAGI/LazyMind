from __future__ import annotations

import asyncio
import logging

from fastapi import APIRouter, HTTPException

from lazymind.structured_tasks import is_structured_task
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
        if is_structured_task(request.task_type):
            _logger.warning('structured_task_failed task_id=%s error_code=%s', result.task_id, result.error_code)
            raise HTTPException(status_code=502, detail={
                'error_code': result.error_code or 'model_call_failed',
                'retryable': result.retryable,
                'diagnostic_id': result.diagnostic_id,
                'usage': result.usage,
            })
        _logger.warning(
            'llm_task_failed task_type=%s task_id=%s error=%s',
            request.task_type,
            result.task_id,
            result.error,
        )
        if request.task_type == 'workflow.repair':
            # Preserve structured failure classification without returning provider
            # bodies, credentials, or the model's generated response text.
            raise HTTPException(status_code=502, detail={
                'error_code': result.error_code or 'invalid_model_output',
                'retryable': result.retryable,
                'diagnostic_id': result.diagnostic_id,
            })
        raise HTTPException(status_code=502, detail=result.error or 'llm task failed')
    return result

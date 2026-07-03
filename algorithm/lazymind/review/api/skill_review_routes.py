from __future__ import annotations

import asyncio

from fastapi import APIRouter
from fastapi.responses import JSONResponse
from lazyllm import LOG
from lazyllm import ThreadPoolExecutor

from lazymind.review.skill_review.config import DEFAULT_BACKGROUND_WORKERS
from lazymind.review.skill_review.schemas import SkillReviewRequest
from lazymind.review.service.skill_review import run_skill_review

router = APIRouter()
background_executor = ThreadPoolExecutor(max_workers=DEFAULT_BACKGROUND_WORKERS)


@router.on_event('shutdown')
def shutdown_background_executor() -> None:
    background_executor.shutdown(wait=False, cancel_futures=True)


@router.post('/api/chat/skill_review', summary='Run skill review for chat histories in a time range')
async def skill_review(payload: SkillReviewRequest):
    loop = asyncio.get_running_loop()
    try:
        future = loop.run_in_executor(
            background_executor,
            run_skill_review,
            payload,
        )
    except Exception as exc:
        LOG.exception(f'[SkillReview] failed to submit skill review task: {exc}')
        return JSONResponse(
            status_code=500,
            content={
                'code': 500,
                'msg': f'skill review submit failed: {exc}',
                'data': {'requestid': payload.requestid},
            },
        )

    LOG.info(f'[SkillReview] skill review accepted: {payload.requestid} for user {payload.user_id}')
    future.add_done_callback(lambda item: _log_skill_review_result(payload, item))
    return JSONResponse(
        status_code=200,
        content={
            'code': 0,
            'msg': 'skill review accepted',
            'data': {'status': 'running', 'requestid': payload.requestid},
        },
    )


def _log_skill_review_result(payload: SkillReviewRequest, future: asyncio.Future) -> None:
    try:
        result = future.result()
        LOG.info(f'[SkillReview] skill review completed: {payload.requestid} for user {payload.user_id}')
        LOG.info(f'[SkillReview] skill review result: {result.model_dump()}')
    except Exception as exc:
        LOG.exception(f'[SkillReview] skill review failed in background: {exc}')

from __future__ import annotations

import asyncio

from fastapi import APIRouter
from fastapi.responses import JSONResponse
from lazyllm import LOG
from lazyllm import ThreadPoolExecutor

from lazymind.review.skill_review.config import DEFAULT_BACKGROUND_WORKERS
from lazymind.review.skill_review.schemas import SkillReviewRequest
from lazymind.review.service.skill_review import (
    build_skill_review_taskid,
    record_skill_review_failed,
    record_skill_review_pending,
    run_skill_review,
)

router = APIRouter()
background_executor = ThreadPoolExecutor(max_workers=DEFAULT_BACKGROUND_WORKERS)


@router.on_event('shutdown')
def shutdown_background_executor() -> None:
    background_executor.shutdown(wait=False, cancel_futures=True)


@router.post('/api/chat/skill_review', summary='Run skill review for chat histories in a time range')
async def skill_review(payload: SkillReviewRequest):
    loop = asyncio.get_running_loop()
    taskid = build_skill_review_taskid(payload.requestid)
    try:
        record_skill_review_pending(payload, taskid)
    except Exception as exc:
        LOG.exception(f'[SkillReview] failed to create pending skill review task: {exc}')
        return JSONResponse(
            status_code=500,
            content={
                'code': 500,
                'msg': f'skill review pending record failed: {exc}',
                'data': {'requestid': payload.requestid, 'taskid': taskid},
            },
        )

    try:
        future = loop.run_in_executor(
            background_executor,
            run_skill_review,
            payload,
            taskid,
        )
    except Exception as exc:
        LOG.exception(f'[SkillReview] failed to submit skill review task: {exc}')
        try:
            record_skill_review_failed(payload, f'skill review submit failed: {exc}', taskid)
        except Exception as insert_exc:
            LOG.exception(f'[SkillReview] failed to mark submit failure: {insert_exc}')
        return JSONResponse(
            status_code=500,
            content={
                'code': 500,
                'msg': f'skill review submit failed: {exc}',
                'data': {'requestid': payload.requestid, 'taskid': taskid},
            },
        )

    LOG.info(f'[SkillReview] skill review accepted: {payload.requestid} task={taskid} for user {payload.user_id}')
    future.add_done_callback(lambda item: _log_skill_review_result(payload, taskid, item))
    return JSONResponse(
        status_code=200,
        content={
            'code': 0,
            'msg': 'skill review accepted',
            'data': {'status': 'pending', 'requestid': payload.requestid, 'taskid': taskid},
        },
    )


def _log_skill_review_result(payload: SkillReviewRequest, taskid: str, future: asyncio.Future) -> None:
    try:
        result = future.result()
        LOG.info(f'[SkillReview] skill review completed: {payload.requestid} task={taskid} for user {payload.user_id}')
        LOG.info(f'[SkillReview] skill review result: {result.model_dump()}')
    except Exception as exc:
        LOG.exception(f'[SkillReview] skill review failed in background task={taskid}: {exc}')

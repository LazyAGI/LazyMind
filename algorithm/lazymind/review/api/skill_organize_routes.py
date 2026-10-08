from __future__ import annotations

import asyncio
from functools import partial

from fastapi import APIRouter
from fastapi.responses import JSONResponse
from lazyllm import LOG
from lazyllm import ThreadPoolExecutor
from pydantic import BaseModel

from lazymind.review.skill_organize.config import DEFAULT_BACKGROUND_WORKERS
from lazymind.review.skill_organize.schemas import SkillOrganizeRequest
from lazymind.review.service.skill_organize import (
    arm_skill_organize_cancel,
    cancel_skill_organize,
    disarm_skill_organize_cancel,
)

router = APIRouter()
background_executor = ThreadPoolExecutor(max_workers=DEFAULT_BACKGROUND_WORKERS)


@router.on_event('shutdown')
def shutdown_background_executor() -> None:
    background_executor.shutdown(wait=False, cancel_futures=True)


@router.post('/api/chat/skill_organize', summary='Organize existing skills and write an organize review record')
async def skill_organize(payload: SkillOrganizeRequest):
    from lazymind.review.service.skill_organize import (
        build_skill_organize_taskid,
        record_skill_organize_failed,
        record_skill_organize_pending,
        run_skill_organize,
    )

    loop = asyncio.get_running_loop()
    taskid = build_skill_organize_taskid(payload.requestid)
    try:
        inserted = record_skill_organize_pending(payload, taskid)
        if inserted == 0:
            return JSONResponse(status_code=409, content={
                'code': 409, 'msg': 'skill organize was cancelled',
                'data': {'status': 'cancelled', 'requestid': payload.requestid, 'taskid': taskid},
            })
        cancel_event = arm_skill_organize_cancel(payload.requestid, user_id=payload.user_id)
    except Exception as exc:
        LOG.exception(f'[SkillOrganize] failed to create pending skill organize task: {exc}')
        return JSONResponse(
            status_code=500,
            content={
                'code': 500,
                'msg': f'skill organize pending record failed: {exc}',
                'data': {'requestid': payload.requestid, 'taskid': taskid},
            },
        )

    try:
        future = loop.run_in_executor(
            background_executor,
            partial(run_skill_organize, payload, taskid, cancel_event=cancel_event),
        )
    except Exception as exc:
        LOG.exception(f'[SkillOrganize] failed to submit skill organize task: {exc}')
        disarm_skill_organize_cancel(payload.requestid, cancel_event, user_id=payload.user_id)
        try:
            record_skill_organize_failed(payload, taskid, f'skill organize submit failed: {exc}')
        except Exception as insert_exc:
            LOG.exception(f'[SkillOrganize] failed to mark submit failure task={taskid}: {insert_exc}')
        return JSONResponse(
            status_code=500,
            content={
                'code': 500,
                'msg': f'skill organize submit failed: {exc}',
                'data': {'requestid': payload.requestid, 'taskid': taskid},
            },
        )

    LOG.info(
        f'[SkillOrganize] skill organize accepted: {payload.requestid} '
        f'task={taskid} for user {payload.user_id} '
        f'model_config_roles={sorted((payload.model_configs or {}).keys())}'
    )
    future.add_done_callback(lambda item: _log_skill_organize_result(payload, taskid, item))
    return JSONResponse(
        status_code=200,
        content={
            'code': 0,
            'msg': 'skill organize accepted',
            'data': {'status': 'pending', 'requestid': payload.requestid, 'taskid': taskid},
        },
    )


class SkillOrganizeCancelBody(BaseModel):
    requestid: str
    user_id: str = ''


@router.post('/api/chat/skill_organize:cancel', summary='Cancel a running skill organize job')
async def skill_organize_cancel(payload: SkillOrganizeCancelBody):
    cancelled = cancel_skill_organize(payload.requestid, user_id=payload.user_id)
    return JSONResponse(
        status_code=200,
        content={
            'code': 0,
            'msg': 'skill organize cancel requested' if cancelled else 'skill organize was not running',
            'data': {'cancelled': cancelled, 'requestid': payload.requestid},
        },
    )


def _log_skill_organize_result(payload: SkillOrganizeRequest, taskid: str, future: asyncio.Future) -> None:
    try:
        result = future.result()
        LOG.info(f'[SkillOrganize] skill organize completed: {payload.requestid} \
            task={taskid} for user {payload.user_id}')
        LOG.info(f'[SkillOrganize] skill organize result: {result.model_dump()}')
    except Exception as exc:
        LOG.exception(f'[SkillOrganize] skill organize failed in background task={taskid}: {exc}')

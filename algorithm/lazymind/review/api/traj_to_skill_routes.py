from __future__ import annotations

import asyncio

from fastapi import APIRouter
from fastapi.responses import JSONResponse
from lazyllm import LOG
from lazyllm import ThreadPoolExecutor

from lazymind.review.traj_to_skill.config import DEFAULT_BACKGROUND_WORKERS
from lazymind.review.traj_to_skill.schemas import TrajToSkillRequest
router = APIRouter()
background_executor = ThreadPoolExecutor(max_workers=DEFAULT_BACKGROUND_WORKERS)


@router.on_event('shutdown')
def shutdown_background_executor() -> None:
    background_executor.shutdown(wait=False, cancel_futures=True)


@router.post('/api/chat/traj_to_skill', summary='Mine selected conversation trajs into skills')
async def traj_to_skill(payload: TrajToSkillRequest):
    from lazymind.review.service.traj_to_skill import (
        build_traj_to_skill_taskid,
        record_traj_to_skill_failed,
        record_traj_to_skill_pending,
        run_traj_to_skill,
    )

    loop = asyncio.get_running_loop()
    taskid = build_traj_to_skill_taskid(payload.requestid)
    try:
        record_traj_to_skill_pending(payload, taskid)
    except Exception as exc:
        LOG.exception(f'[TrajToSkill] failed to create pending traj_to_skill task: {exc}')
        return JSONResponse(
            status_code=500,
            content={
                'code': 500,
                'msg': f'traj_to_skill pending record failed: {exc}',
                'data': {'requestid': payload.requestid, 'taskid': taskid},
            },
        )

    try:
        future = loop.run_in_executor(
            background_executor,
            run_traj_to_skill,
            payload,
            taskid,
        )
    except Exception as exc:
        LOG.exception(f'[TrajToSkill] failed to submit traj_to_skill task: {exc}')
        try:
            record_traj_to_skill_failed(payload, f'traj_to_skill submit failed: {exc}', taskid)
        except Exception as insert_exc:
            LOG.exception(f'[TrajToSkill] failed to mark submit failure: {insert_exc}')
        return JSONResponse(
            status_code=500,
            content={
                'code': 500,
                'msg': f'traj_to_skill submit failed: {exc}',
                'data': {'requestid': payload.requestid, 'taskid': taskid},
            },
        )

    LOG.info(f'[TrajToSkill] accepted: {payload.requestid} task={taskid} for user {payload.user_id}')
    future.add_done_callback(lambda item: _log_traj_to_skill_result(payload, taskid, item))
    return JSONResponse(
        status_code=200,
        content={
            'code': 0,
            'msg': 'traj_to_skill accepted',
            'data': {'status': 'pending', 'requestid': payload.requestid, 'taskid': taskid},
        },
    )


def _log_traj_to_skill_result(payload: TrajToSkillRequest, taskid: str, future: asyncio.Future) -> None:
    try:
        result = future.result()
        LOG.info(f'[TrajToSkill] completed: {payload.requestid} task={taskid} for user {payload.user_id}')
        LOG.info(f'[TrajToSkill] result: {result.model_dump()}')
    except Exception as exc:
        LOG.exception(f'[TrajToSkill] failed in background task={taskid}: {exc}')

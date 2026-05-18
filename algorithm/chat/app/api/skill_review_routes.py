from __future__ import annotations

import asyncio

from fastapi import APIRouter
from fastapi.responses import JSONResponse

from chat.components.skill_review.schemas import SkillReviewRequest
from chat.pipelines.skill_review import run_skill_review

router = APIRouter()


@router.post('/api/chat/skill-review', summary='Run skill review from a session database')
async def skill_review(payload: SkillReviewRequest):
    try:
        result = await asyncio.to_thread(run_skill_review, payload)
        return {'code': 0, 'msg': 'ok', 'data': result.model_dump()}
    except Exception as exc:
        return JSONResponse(
            status_code=500,
            content={'code': 500, 'msg': f'skill review failed: {exc}', 'data': None},
        )

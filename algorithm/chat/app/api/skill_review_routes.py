from __future__ import annotations

import asyncio

from fastapi import APIRouter
from fastapi.responses import JSONResponse
from lazyllm import AutoModel, LOG

from chat.components.skill_review.schemas import SkillReviewRequest
from chat.pipelines.skill_review import run_skill_review
from chat.utils.load_config import get_config_path

router = APIRouter()


@router.post('/api/chat/skill-review', summary='Run skill review for chat histories in a time range')
async def skill_review(payload: SkillReviewRequest):
    try:
        llm, emb = _build_and_check_models()
    except Exception as exc:
        return JSONResponse(
            status_code=400,
            content={
                'code': 400,
                'msg': f'skill review model unavailable: {exc}',
                'data': None,
            },
        )

    asyncio.create_task(_run_skill_review_background(payload, llm, emb))
    return JSONResponse(
        status_code=200,
        content={
            'code': 0,
            'msg': 'skill review accepted',
            'data': {'status': 'running'},
        },
    )


def _build_and_check_models():
    llm = AutoModel(model='llm', config=get_config_path())
    emb = AutoModel(model='embed_main', config=get_config_path())
    llm('你好')
    if emb('你好') is None:
        raise RuntimeError('embedding model returned empty response')
    return llm, emb


async def _run_skill_review_background(payload: SkillReviewRequest, llm, emb) -> None:
    try:
        result = await asyncio.to_thread(run_skill_review, payload, llm, emb)
        LOG.info(f'skill review completed: {result.model_dump()}')
    except Exception as exc:
        LOG.error(f'skill review failed in background: {exc}')
        try:
            import traceback
            traceback.print_exc()
        except Exception:
            pass

from __future__ import annotations

import asyncio
from concurrent.futures import ThreadPoolExecutor
from functools import partial

from fastapi import APIRouter
from fastapi.responses import JSONResponse
from lazyllm import AutoModel, LOG

from lazymind.review.skill_review.config import DEFAULT_BACKGROUND_WORKERS, DEFAULT_LLM_CALL_TIMEOUT_SECONDS
from lazymind.review.skill_review.schemas import SkillReviewRequest
from lazymind.review.service.skill_review import run_skill_review
from lazymind.model_config import get_config_path

router = APIRouter()
background_tasks: set[asyncio.Task] = set()
background_executor = ThreadPoolExecutor(max_workers=DEFAULT_BACKGROUND_WORKERS)


@router.on_event('shutdown')
def shutdown_background_executor() -> None:
    background_executor.shutdown(wait=False, cancel_futures=True)


@router.post('/api/chat/skill-review', summary='Run skill review for chat histories in a time range')
async def skill_review(payload: SkillReviewRequest):
    try:
        llm, emb = await asyncio.wait_for(
            asyncio.to_thread(_build_and_check_models),
            timeout=DEFAULT_LLM_CALL_TIMEOUT_SECONDS,
        )
    except Exception as exc:
        return JSONResponse(
            status_code=400,
            content={
                'code': 400,
                'msg': f'skill review model unavailable: {exc}',
                'data': None,
            },
        )

    task = asyncio.create_task(_run_skill_review_background(payload, llm, emb))
    background_tasks.add(task)
    task.add_done_callback(background_tasks.discard)
    return JSONResponse(
        status_code=200,
        content={
            'code': 0,
            'msg': 'skill review accepted',
            'data': {'status': 'running', 'requestid': payload.requestid},
        },
    )


def _build_and_check_models():
    llm = AutoModel(model='llm', config=get_config_path())
    emb = AutoModel(model='embed_main', config=get_config_path())
    if llm('hello world') is None:
        raise RuntimeError('llm returned empty response')
    if emb('hello world') is None:
        raise RuntimeError('embedding model returned empty response')
    return llm, emb


async def _run_skill_review_background(payload: SkillReviewRequest, llm, emb) -> None:
    try:
        loop = asyncio.get_running_loop()
        result = await loop.run_in_executor(
            background_executor,
            partial(run_skill_review, payload, llm, emb, payload.artifact_dir),
        )
        LOG.info(f'[SkillReview] skill review completed: {result.model_dump()}')
    except Exception as exc:
        LOG.exception(f'[SkillReview] skill review failed in background: {exc}')

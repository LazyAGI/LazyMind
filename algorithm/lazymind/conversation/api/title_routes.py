"""Title generation endpoints."""
import asyncio

from fastapi import APIRouter

from ..conversation_title import generate_title, generate_titles
from ..schemas import BatchTitleRequest, ConversationResult, TitleRequest

router = APIRouter(prefix='/api/conversation')


@router.post('/title:generate', response_model=ConversationResult)
async def title_generate(request: TitleRequest):
    return await asyncio.to_thread(generate_title, request)


@router.post('/titles:generate', response_model=ConversationResult)
async def titles_generate(request: BatchTitleRequest):
    return await asyncio.to_thread(generate_titles, request)

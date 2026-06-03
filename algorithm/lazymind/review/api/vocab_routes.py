"""vocab_routes: Vocabulary-related API (multi-user support).

The backend calls this endpoint after a user modifies synonym groups; the algorithm
service reloads the vocabulary for the corresponding user from the database and
rebuilds the AC automaton.

POST /api/vocab/reload
    Body: {"user_id": "user_001"}
    Response: {"status": "ok", "vocab_size": <int>, "user_id": "<str>"}

POST /api/vocab/extract
    Body: {"user_id": "user_001"}
    Response: 204 No Content
"""
from __future__ import annotations

from fastapi import APIRouter, HTTPException, Response, status
from lazyllm import LOG
from pydantic import BaseModel

from lazymind.review.service.registry import get_vocab_manager

router = APIRouter()


class VocabUserRequest(BaseModel):
    user_id: str = ''


@router.post('/api/vocab/reload', summary='Hot-reload vocabulary for the specified user')
async def reload_vocab(body: VocabUserRequest | None = None):
    """Reload vocabulary from the database for the specified user and rebuild the AC automaton.

    - **user_id**: User ID.
    """
    body = body or VocabUserRequest()
    user_id = body.user_id.strip()
    LOG.info(f'[VocabRoutes] reload requested user_id={user_id!r}')
    try:
        manager = get_vocab_manager(user_id)
        count = manager.reload()
    except Exception as exc:
        LOG.error(f'[VocabRoutes] reload failed user_id={user_id!r} error={exc}')
        raise HTTPException(status_code=status.HTTP_503_SERVICE_UNAVAILABLE, detail='vocab reload failed') from exc
    return {'status': 'ok', 'vocab_size': count, 'user_id': user_id}


@router.post('/api/vocab/extract', summary='Trigger vocabulary evolution extraction',
             status_code=status.HTTP_204_NO_CONTENT)
async def extract_vocab(
    body: VocabUserRequest | None = None,
):
    """Deprecated no-op; vocabulary evolution is driven by the review-agent vocab tool.

    - **user_id**: Optional user ID for logging.
    """
    body = body or VocabUserRequest()
    LOG.info(f'[VocabRoutes] extract deprecated and ignored user_id={body.user_id.strip() or "<all-users>"!r}')
    return Response(status_code=status.HTTP_204_NO_CONTENT)

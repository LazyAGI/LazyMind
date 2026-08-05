from __future__ import annotations

import hmac
import os
from typing import List, Optional

from fastapi import APIRouter, Header, HTTPException
from pydantic import BaseModel, Field

from lazymind.config import config as _cfg
from lazymind.chat.service import knowledge_search_service

router = APIRouter()
INTERNAL_TOKEN_HEADER = 'X-LazyMind-Internal-Token'


class KnowledgeSearchRequest(BaseModel):
    query: str = Field(default='')
    kb_ids: List[str] = Field(default_factory=list)
    top_k: int = Field(default=10)


class KnowledgeSearchHitResponse(BaseModel):
    kb_id: str
    doc_id: str
    chunk_id: str
    text: str
    score: float
    title: str = ''
    source_url: str = ''


class KnowledgeSearchResponse(BaseModel):
    hits: List[KnowledgeSearchHitResponse]


def expected_internal_token() -> str:
    return str(
        _cfg['core_internal_token']
        or os.getenv('LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN')
        or os.getenv('AUTH_SERVICE_INTERNAL_TOKEN')
        or ''
    ).strip()


def require_internal_token(provided: Optional[str]) -> str:
    expected = expected_internal_token()
    if not expected:
        raise HTTPException(
            status_code=503,
            detail={'code': 'BACKEND_UNAVAILABLE', 'message': 'internal token is not configured'},
        )
    provided = (provided or '').strip()
    if not provided or not hmac.compare_digest(provided, expected):
        raise HTTPException(status_code=401, detail={'code': 'UNAUTHORIZED', 'message': 'unauthorized'})
    return expected


@router.post('/internal/knowledge:search', response_model=KnowledgeSearchResponse)
async def search_knowledge(
    request: KnowledgeSearchRequest,
    x_lazymind_internal_token: Optional[str] = Header(default=None, alias=INTERNAL_TOKEN_HEADER),
):
    require_internal_token(x_lazymind_internal_token)
    try:
        hits = knowledge_search_service.search(
            query=request.query,
            kb_ids=request.kb_ids,
            top_k=request.top_k,
        )
    except knowledge_search_service.KnowledgeSearchError as exc:
        status = 400 if exc.code == 'INVALID_ARGUMENT' else 503
        raise HTTPException(status_code=status, detail={'code': exc.code, 'message': str(exc)}) from exc
    return KnowledgeSearchResponse(
        hits=[
            KnowledgeSearchHitResponse(
                kb_id=hit.kb_id,
                doc_id=hit.doc_id,
                chunk_id=hit.chunk_id,
                text=hit.text,
                score=hit.score,
                title=hit.title,
                source_url=hit.source_url,
            )
            for hit in hits
        ]
    )

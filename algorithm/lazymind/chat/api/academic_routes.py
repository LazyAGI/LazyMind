"""Internal PDF parsing and one-pass scholarly metadata extraction."""
from __future__ import annotations

import asyncio
import hashlib
import os
import email.utils
from datetime import datetime, timedelta, timezone
from pathlib import Path
from typing import Annotated, Optional

from fastapi import APIRouter, Header, HTTPException
from pydantic import BaseModel, Field

from .knowledge_search_routes import INTERNAL_TOKEN_HEADER, require_internal_token

router = APIRouter()


class PDFRequest(BaseModel):
    file_ref: str = Field(min_length=1, max_length=256)
    content_hash: str = Field(pattern=r'^[a-f0-9]{64}$')


class MetadataRequest(BaseModel):
    text: str = Field(min_length=1, max_length=32000)
    llm_config: dict = Field(default_factory=dict)


@router.post('/internal/academic/pdf:parse')
async def parse_pdf(payload: PDFRequest, token: Annotated[Optional[str], Header(alias=INTERNAL_TOKEN_HEADER)] = None):
    require_internal_token(token)
    root = Path(os.getenv('LAZYMIND_SHARED_UPLOAD_DIR', '/var/lib/lazymind/uploads')).resolve() / 'academic'
    path = (root / payload.file_ref).resolve()
    if not path.is_relative_to(root) or not path.is_file() or path.suffix != '.pdf':
        raise HTTPException(422, 'invalid academic file reference')

    def run():
        import lazyllm
        from lazyllm.common.globals import init_session
        init_session()
        try:
            from lazymind.chat.engine.tools.file_resources.ingest import parse_pdf_pages
            if hashlib.sha256(path.read_bytes()).hexdigest() != payload.content_hash:
                raise ValueError('PDF version changed')
            grouped: dict[int, list[str]] = {}
            unknown = []
            for page, text in parse_pdf_pages(str(path)):
                if page > 0:
                    grouped.setdefault(page, []).append(text)
                else:
                    unknown.append({'page': 0, 'text': text})
            pages = [{'page': page, 'text': '\n\n'.join(texts)} for page, texts in sorted(grouped.items())]
            pages.extend(unknown)
            if not pages:
                raise ValueError('PDF yielded no text')
            return {'pages': pages, 'parser_version': 'pdf-reader-v2'}
        finally:
            lazyllm.globals.clear()
    try:
        return await asyncio.to_thread(run)
    except ValueError as exc:
        raise HTTPException(422, str(exc)) from None


@router.post('/internal/academic/metadata:extract')
async def extract_metadata(
    payload: MetadataRequest, token: Annotated[Optional[str], Header(alias=INTERNAL_TOKEN_HEADER)] = None,
):
    require_internal_token(token)

    def run():
        from lazymind.chat.service.llm_task import LLMTaskInput, LLMTaskRequest, run_llm_task
        instruction = (
            'Classify the supplied document itself (ignore cited works). Return ONLY JSON: '
            '{"kind":"paper|other|unknown","title":"","authors":[],"abstract":"",'
            '"year":0,"venue":"","doi":"","arxiv_id":""}. '
            'Use unknown if uncertain. Copy the original abstract, never invent metadata. '
            'DOI/arxiv must identify this document, not a reference.')
        result = run_llm_task(LLMTaskRequest(
            task_type='academic.metadata', instruction=instruction,
            input=LLMTaskInput(text=payload.text), llm_config=payload.llm_config,
            response_format={'type': 'json_object'},
            options={'max_retries': 1, 'model_retry_attempts': 0, 'timeout_seconds': 90},
        ))
        if result.status != 'succeeded' or result.output.get('kind') not in {'paper', 'other', 'unknown'}:
            raise ValueError('invalid classification')
        return result.output
    try:
        return await asyncio.to_thread(run)
    except (ValueError, TypeError):
        raise HTTPException(422, 'metadata extraction returned invalid JSON') from None


class PreparationRequest(BaseModel):
    expansion_id: str = Field(min_length=1, max_length=36)
    user_id: str = Field(min_length=1, max_length=255)
    revision: int = Field(ge=1)
    input: dict
    preparation: dict
    llm_config: dict
    depth: dict


@router.post('/internal/academic/preparation:run')
async def prepare_expansion(payload: PreparationRequest,
                            token: Annotated[Optional[str], Header(alias=INTERNAL_TOKEN_HEADER)] = None):
    require_internal_token(token)
    from lazymind.chat.service.academic_preparation import run_preparation
    return await asyncio.to_thread(run_preparation, payload.model_dump())


class AcademicSearchRequest(BaseModel):
    source: str = Field(pattern=r'^(sciverse|web)$')
    query: str = Field(min_length=1, max_length=2000)
    limit: int = Field(ge=1, le=20)
    page: int = Field(default=1, ge=1)
    cursor: str = Field(default='', max_length=4096)
    tool_config: dict = Field(default_factory=dict)


@router.post('/internal/academic/search')
async def academic_search(payload: AcademicSearchRequest,
                          token: Annotated[Optional[str], Header(alias=INTERNAL_TOKEN_HEADER)] = None):
    require_internal_token(token)
    from lazymind.chat.service.academic_preparation import search_registry
    try:
        return await asyncio.to_thread(search_registry, payload.model_dump())
    except ValueError as exc:
        if str(exc) == 'missing_configuration':
            raise HTTPException(422, 'missing_configuration') from None
        raise HTTPException(502, 'academic source request failed') from None
    except Exception as exc:
        from lazyllm.common import AllKeysExhaustedError, KeyAuthError
        if isinstance(exc, (KeyAuthError, AllKeysExhaustedError)):
            raise HTTPException(401, {'code': 'authentication_failed'}) from None
        status = getattr(getattr(exc, 'response', None), 'status_code', None)
        if status in (401, 402, 403, 429):
            code = {401: 'authentication_failed', 403: 'authentication_failed',
                    402: 'quota_exhausted', 429: 'rate_limited'}[status]
            detail = {'code': code}
            response = exc.response
            if status == 429:
                try:
                    upstream = response.json()
                except ValueError:
                    upstream = {}
                if isinstance(upstream, dict) and upstream.get('code') in {
                    'quota_exhausted', 'insufficient_quota', 'not_enough_credits',
                }:
                    detail['code'] = 'quota_exhausted'
            retry = response.headers.get('Retry-After', '')
            if retry.isdigit() and int(retry) > 0:
                detail['retry_at'] = (datetime.now(timezone.utc) + timedelta(seconds=int(retry))).isoformat()
            elif retry:
                try:
                    detail['retry_at'] = email.utils.parsedate_to_datetime(retry).isoformat()
                except (TypeError, ValueError):
                    pass
            raise HTTPException(status, detail) from None
        raise HTTPException(502, 'academic source request failed') from None

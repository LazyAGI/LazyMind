from typing import Annotated, Optional

from fastapi import APIRouter, Header, HTTPException
from pydantic import BaseModel
from starlette.concurrency import run_in_threadpool

from lazymind.chat.api.knowledge_search_routes import INTERNAL_TOKEN_HEADER, require_internal_token
from lazymind.config import config
from lazymind.common.base_ingestion import ARTIFACT_SUFFIX, prepare_document

router = APIRouter()


class PrepareRequest(BaseModel):
    source_path: str


@router.post('/internal/documents:prepare')
async def prepare(
    request: PrepareRequest,
    token: Annotated[Optional[str], Header(alias=INTERNAL_TOKEN_HEADER)] = None,
):
    require_internal_token(token)
    try:
        data = await run_in_threadpool(prepare_document, request.source_path, config['shared_upload_dir'])
    except (ValueError, OSError) as exc:
        raise HTTPException(422, detail=str(exc)) from None
    return {'artifact_ref': request.source_path + ARTIFACT_SUFFIX,
            'source_sha256': data['source_sha256'],
            'parser_version': data['parser_version'], 'chunker_version': data['chunker_version']}

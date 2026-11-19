from typing import Annotated, Optional
import logging

from fastapi import APIRouter, Header, HTTPException
from pydantic import BaseModel, Field
from starlette.concurrency import run_in_threadpool

from lazymind.chat.api.knowledge_search_routes import INTERNAL_TOKEN_HEADER, require_internal_token
from lazymind.config import config
from lazymind.common.base_ingestion import ARTIFACT_SUFFIX, DocumentContentError, prepare_document

from lazymind.parsing.service.embedding_reset import IndexBusy

router = APIRouter()


class PrepareRequest(BaseModel):
    source_path: str
    embedding_generation: int | None = None


@router.post('/internal/documents:prepare')
async def prepare(
    request: PrepareRequest,
    token: Annotated[Optional[str], Header(alias=INTERNAL_TOKEN_HEADER)] = None,
):
    require_internal_token(token)
    try:
        data = await run_in_threadpool(_prepare_guarded, request)
    except IndexBusy:
        raise HTTPException(409, detail={'code': 'INDEX_BUSY'}) from None
    except DocumentContentError as exc:
        raise HTTPException(422, detail={'code': exc.code, 'message': str(exc)}) from None
    except OSError:
        raise HTTPException(503, detail={'code': 'READER_UNAVAILABLE'}) from None
    except ValueError:
        raise HTTPException(422, detail={'code': 'DOCUMENT_READ_FAILED'}) from None
    return {'artifact_ref': request.source_path + ARTIFACT_SUFFIX,
            'source_sha256': data['source_sha256'],
            'parser_version': data['parser_version'], 'chunker_version': data['chunker_version']}


class ClearChunksRequest(BaseModel):
    generation: int = Field(ge=1)
    empty_only: bool = False


@router.post('/internal/knowledge:clear-chunks')
async def clear_knowledge_chunks(
    request: ClearChunksRequest,
    token: Annotated[Optional[str], Header(alias=INTERNAL_TOKEN_HEADER)] = None,
):
    require_internal_token(token)
    from lazymind.parsing.service.embedding_reset import clear_chunks, IndexBusy
    try:
        return await run_in_threadpool(clear_chunks, request.generation, request.empty_only)
    except IndexBusy:
        raise HTTPException(409, detail={'code': 'INDEX_BUSY'}) from None
    except Exception:
        logging.getLogger(__name__).exception('Text-vector cleanup failed')
        raise HTTPException(503, detail={'code': 'INDEX_CLEANUP_FAILED'}) from None


def _prepare_guarded(request):
    # This only writes a retained parse artifact, never vectors.
    return prepare_document(request.source_path, config['shared_upload_dir'])

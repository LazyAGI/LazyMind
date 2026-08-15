from __future__ import annotations

from dataclasses import dataclass
from typing import Any, Dict, List, Optional
from urllib.parse import urlparse

import lazyllm
from lazyllm.common.globals import init_session

from lazymind.chat.engine.tools.algo import search_kb
from lazymind.chat.engine.tools.kb import _ensure_kb_search_runtime, _serialize_kb_result
from lazymind.model_config import inject_model_config

_DEFAULT_RETRIEVER_TOPK = 20
_DEFAULT_RERANK_TOPK = 20
_DEFAULT_IMAGE_TOPK = 0


class KnowledgeSearchError(Exception):
    def __init__(self, code: str, message: str, cause: Optional[BaseException] = None):
        super().__init__(message)
        self.code = code
        self.cause = cause


@dataclass(frozen=True)
class KnowledgeSearchHit:
    kb_id: str
    doc_id: str
    chunk_id: str
    text: str
    score: float
    title: str = ''
    source_url: str = ''


def search(user_id: str, query: str, kb_ids: List[str], top_k: int,
           llm_config: Optional[Dict[str, Any]] = None) -> List[KnowledgeSearchHit]:
    user_id = (user_id or '').strip()
    query = (query or '').strip()
    kb_ids = [str(kb_id).strip() for kb_id in kb_ids or [] if str(kb_id).strip()]
    if not user_id:
        raise KnowledgeSearchError('INVALID_ARGUMENT', 'user_id is required')
    if not query:
        raise KnowledgeSearchError('INVALID_ARGUMENT', 'query is required')
    if not kb_ids:
        raise KnowledgeSearchError('INVALID_ARGUMENT', 'kb_ids is required')
    try:
        top_k = int(top_k)
    except (TypeError, ValueError):
        top_k = 10
    if top_k <= 0:
        top_k = 10
    if top_k > 50:
        top_k = 50

    init_session()
    try:
        inject_model_config(llm_config)
        retrievers, reranker, image_retriever = _ensure_kb_search_runtime()
        raw = search_kb(
            {
                'query': query,
                'filters': {'kb_id': kb_ids},
                'user_id': user_id,
                'llm_config': llm_config or {},
            },
            retrievers=retrievers,
            reranker=reranker,
            image_retriever=image_retriever,
            retriever_topk=max(_DEFAULT_RETRIEVER_TOPK, top_k),
            rerank_topk=max(_DEFAULT_RERANK_TOPK, top_k),
            k_max=top_k,
            image_topk=_DEFAULT_IMAGE_TOPK,
        )
        return _hits_from_serialized(_serialize_kb_result(raw), kb_ids, top_k)
    except KnowledgeSearchError:
        raise
    except Exception as exc:
        raise KnowledgeSearchError('BACKEND_UNAVAILABLE', 'knowledge search backend unavailable', exc) from exc
    finally:
        lazyllm.globals.clear()


def _hits_from_serialized(serialized: Any, allowed_kb_ids: List[str], limit: int) -> List[KnowledgeSearchHit]:
    items = serialized.get('items') if isinstance(serialized, dict) else serialized
    if not isinstance(items, list):
        return []

    allowed = set(allowed_kb_ids)
    hits: List[KnowledgeSearchHit] = []
    seen = set()
    for item in items:
        if len(hits) >= limit:
            break
        if not isinstance(item, dict):
            continue
        if _is_image_only(item):
            continue
        kb_id = _string(item.get('kb_id'))
        if kb_id not in allowed:
            continue
        text = _string(item.get('text'))
        if not text:
            continue
        doc_id = _string(item.get('doc_id') or item.get('document_id') or item.get('docid'))
        chunk_id = _string(item.get('chunk_id') or item.get('uid') or item.get('segment_id'))
        key = (kb_id, doc_id, chunk_id, text)
        if key in seen:
            continue
        seen.add(key)
        hits.append(KnowledgeSearchHit(
            kb_id=kb_id,
            doc_id=doc_id,
            chunk_id=chunk_id,
            text=text,
            score=_float(item.get('score')),
            title=_string(item.get('title') or item.get('file_name')),
            source_url=_safe_source_url(item.get('source_url')),
        ))
    return hits


def _is_image_only(item: dict) -> bool:
    group = _string(item.get('group')).lower()
    if group == 'image':
        return True
    if item.get('image_markdown') or item.get('local_path'):
        return True
    metadata = item.get('metadata')
    if isinstance(metadata, dict):
        node_type = _string(metadata.get('type') or metadata.get('node_type')).lower()
        if node_type == 'image' or metadata.get('images'):
            return True
    return False


def _safe_source_url(value: Any) -> str:
    url = _string(value)
    if not url:
        return ''
    parsed = urlparse(url)
    if parsed.scheme in ('http', 'https') and parsed.netloc:
        return url
    if parsed.scheme or url.startswith('/') or 'local_path' in url.lower() or 'stored_path' in url.lower():
        return ''
    return ''


def _string(value: Any) -> str:
    return str(value).strip() if value is not None else ''


def _float(value: Any) -> float:
    try:
        return float(value)
    except (TypeError, ValueError):
        return 0.0

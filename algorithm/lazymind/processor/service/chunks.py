"""Text-only chunk previews for the LazyMind document processor."""
from copy import copy
from types import MethodType

from fastapi import HTTPException
from lazyllm.tools.rag.store import HybridStore


def list_doc_chunks_data(processor, algo_id, kb_id, doc_id, group, offset=0, limit=20):
    algorithm = processor._get_algo(algo_id)
    if algorithm is None:
        raise HTTPException(status_code=404, detail=f'Invalid algo_id {algo_id}')
    store = processor._get_or_init_store(algorithm, init=True)
    if store is None:
        raise HTTPException(status_code=503, detail='Store not initialized for algo')
    if group not in (algorithm.get('node_groups') or {}) or not store.is_group_active(group):
        raise HTTPException(status_code=400, detail=f'Invalid group {group}')

    backend = store.seg_impl
    if isinstance(backend, HybridStore):
        # Keep LazyLLM pagination and null-node filtering without reading vectors.
        # A separate view avoids changing the shared store during concurrent reads.
        store = copy(store)
        store._impl = backend.segment_store
    offset, limit = max(offset, 0), max(limit, 1)
    segments, total = store.get_segments(
        doc_ids={doc_id}, kb_id=kb_id, group=group, offset=offset, limit=limit,
        return_total=True, sort_by_number=True,
    )
    return {
        'items': [processor._format_chunk_item(segment) for segment in segments],
        'total': total,
        'offset': offset,
        'page_size': limit,
    }


def install_chunk_preview(processor):
    processor._raw_impl._list_doc_chunks_data = MethodType(list_doc_chunks_data, processor._raw_impl)

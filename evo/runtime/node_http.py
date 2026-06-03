from __future__ import annotations
import logging
from functools import lru_cache
from typing import Any
import requests
from algorithm.config import config
from evo.domain.node import NodeInfo
from evo.runtime.config import (
    EVO_CHUNK_BASE_URL,
    EVO_KB_BASE_URL,
    EVO_NODE_HTTP_DIRECT,
    EVO_NODE_HTTP_MAX_PAGES,
    EVO_NODE_HTTP_TIMEOUT_S,
)

_log = logging.getLogger('evo.runtime.node_http')


def http_get_node(node_id: str, *, kb_ids: tuple[str, ...] | None = None) -> NodeInfo | None:
    nid = str(node_id or '').strip()
    if not _looks_like_node_id(nid):
        return None
    return _cached_get_node(nid, _base_url(), tuple(kb_ids or _candidate_kb_ids()))


def _looks_like_node_id(value: str) -> bool:
    if not value or any((ch.isspace() for ch in value)):
        return False
    if value.startswith(('doc_', 'chunk_', 'node_', 'seg_', 'segment_', 'uid_')):
        return True
    return len(value) >= 24 and all((ch.isalnum() or ch in '_-' for ch in value))


@lru_cache(maxsize=4096)
def _cached_get_node(node_id: str, base: str, kb_ids: tuple[str, ...]) -> NodeInfo | None:
    if not base:
        return None
    if EVO_NODE_HTTP_DIRECT:
        direct = _try_direct_node(base, node_id)
        if direct:
            return direct
    for kb_id in kb_ids:
        doc = _find_doc(base, kb_id, node_id)
        if doc:
            return doc
        chunk = _find_chunk(base, kb_id, node_id)
        if chunk:
            return chunk
    return None


def _session() -> requests.Session:
    s = requests.Session()
    s.trust_env = False
    return s


def _base_url() -> str:
    return (EVO_CHUNK_BASE_URL or EVO_KB_BASE_URL or config['document_server_url'] or '').rstrip('/')


def _candidate_kb_ids() -> list[str]:
    return []


def _try_direct_node(base: str, node_id: str) -> NodeInfo | None:
    for path, params in (
        ('/v1/nodes', {'node_id': node_id}),
        ('/v1/chunks', {'chunk_id': node_id}),
        ('/v1/chunks', {'uid': node_id}),
    ):
        try:
            r = _session().get(f'{base}{path}', params=params, timeout=EVO_NODE_HTTP_TIMEOUT_S)
            if not r.ok:
                continue
            items = _items(r.json())
            for item in items:
                node = _node_from_chunk(item)
                if node and node.get('id') == node_id:
                    return node
        except Exception as exc:
            _log.debug('direct node lookup failed id=%s path=%s: %s', node_id, path, exc)
    return None


def _find_doc(base: str, kb_id: str, node_id: str) -> NodeInfo | None:
    for page in range(1, EVO_NODE_HTTP_MAX_PAGES + 1):
        try:
            r = _session().get(
                f'{base}/v1/docs',
                params={'kb_id': kb_id, 'algo_id': _algo_id(), 'page': page, 'page_size': 100},
                timeout=EVO_NODE_HTTP_TIMEOUT_S,
            )
            if not r.ok:
                return None
            items = _items(r.json())
            if not items:
                return None
            for item in items:
                doc = item.get('doc') if isinstance(item, dict) else item
                if not isinstance(doc, dict):
                    continue
                doc_id = str(doc.get('doc_id') or doc.get('id') or '')
                if doc_id == node_id:
                    return NodeInfo(
                        id=node_id,
                        docid=doc_id,
                        kb_id=kb_id,
                        file_name=str(doc.get('filename') or doc.get('file_name') or doc.get('name') or ''),
                        text=str(doc.get('content') or doc.get('text') or ''),
                    )
        except Exception as exc:
            _log.debug('doc lookup failed id=%s kb=%s: %s', node_id, kb_id, exc)
            return None
    return None


def _find_chunk(base: str, kb_id: str, node_id: str) -> NodeInfo | None:
    for doc in _iter_docs(base, kb_id):
        doc_id = doc.get('doc_id') or doc.get('id')
        if not doc_id:
            continue
        for group in ('block', 'line'):
            for page in range(1, EVO_NODE_HTTP_MAX_PAGES + 1):
                try:
                    r = _session().get(
                        f'{base}/v1/chunks',
                        params={
                            'kb_id': kb_id,
                            'doc_id': doc_id,
                            'group': group,
                            'algo_id': _algo_id(),
                            'page': page,
                            'page_size': 100,
                        },
                        timeout=EVO_NODE_HTTP_TIMEOUT_S,
                    )
                    if not r.ok:
                        break
                    items = _items(r.json())
                    if not items:
                        break
                    for item in items:
                        node = _node_from_chunk(item, fallback_doc=doc, kb_id=kb_id, group=group)
                        if node and node.get('id') == node_id:
                            return node
                except Exception as exc:
                    _log.debug('chunk lookup failed id=%s kb=%s doc=%s: %s', node_id, kb_id, doc_id, exc)
                    break
    return None


def _iter_docs(base: str, kb_id: str) -> list[dict[str, Any]]:
    out: list[dict[str, Any]] = []
    for page in range(1, EVO_NODE_HTTP_MAX_PAGES + 1):
        try:
            r = _session().get(
                f'{base}/v1/docs',
                params={'kb_id': kb_id, 'algo_id': _algo_id(), 'page': page, 'page_size': 100},
                timeout=EVO_NODE_HTTP_TIMEOUT_S,
            )
            if not r.ok:
                break
            items = _items(r.json())
            if not items:
                break
            for item in items:
                doc = item.get('doc') if isinstance(item, dict) else item
                if isinstance(doc, dict):
                    out.append(doc)
        except Exception as exc:
            _log.debug('doc scan failed kb=%s: %s', kb_id, exc)
            break
    return out


def _node_from_chunk(
    item: Any, *, fallback_doc: dict[str, Any] | None = None, kb_id: str | None = None, group: str | None = None
) -> NodeInfo | None:
    if not isinstance(item, dict):
        return None
    meta = item.get('metadata') if isinstance(item.get('metadata'), dict) else {}
    node_id = item.get('uid') or item.get('id') or item.get('chunk_id') or item.get('node_id')
    if not node_id:
        return None
    doc_id = item.get('doc_id') or item.get('docid') or item.get('document_id') or (fallback_doc or {}).get('doc_id')
    file_name = (
        meta.get('file_name')
        or meta.get('filename')
        or item.get('file_name')
        or item.get('filename')
        or (fallback_doc or {}).get('filename')
    )
    return NodeInfo(
        id=str(node_id),
        docid=str(doc_id) if doc_id else '',
        kb_id=str(item.get('kb_id') or kb_id or ''),
        file_name=str(file_name or ''),
        text=str(item.get('content') or item.get('text') or ''),
        group=str(item.get('group') or group or ''),
        page=_int_or_none(item.get('page') or meta.get('page')),
        index=_int_or_none(item.get('index') or meta.get('index')),
        number=_int_or_none(item.get('number') or meta.get('number')),
        bbox=item.get('bbox') or meta.get('bbox') or [],
    )


def _items(data: Any) -> list[Any]:
    if not isinstance(data, dict):
        return []
    payload = data.get('data') if isinstance(data.get('data'), dict) else data
    items = payload.get('items') if isinstance(payload, dict) else None
    if isinstance(items, list):
        return items
    return [payload] if isinstance(payload, dict) else []


def _int_or_none(v: Any) -> int | None:
    return int(v) if isinstance(v, (int, float)) else None


def _algo_id() -> str:
    return config['algo_dataset_name'] or config['default_algo_id']

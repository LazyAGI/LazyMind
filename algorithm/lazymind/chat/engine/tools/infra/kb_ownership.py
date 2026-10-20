"""Core is the authority for current document ownership, including stale indexes."""
from __future__ import annotations

from typing import Any

from .core_api_client import post_core_api


def _identity(node: Any) -> tuple[str, str]:
    if isinstance(node, dict):
        metadata = node.get('global_metadata') or node.get('metadata') or {}
        direct = node
    else:
        metadata = getattr(node, 'global_metadata', {}) or {}
        direct = {}
    if not isinstance(metadata, dict):
        metadata = {}
    kb = direct.get('kb_id') or direct.get('dataset_id') or metadata.get('kb_id')
    doc = (direct.get('doc_id') or direct.get('docid') or direct.get('document_id')
           or metadata.get('docid') or metadata.get('doc_id') or metadata.get('document_id'))
    return str(kb or '').strip(), str(doc or '').strip()


def filter_active_nodes(nodes: list, *, user_id: str | None = None) -> list:
    """Authorize exact KB/document pairs before exposing text, images or references.

    Never cache the result across tool calls: deletion/move and ACL revocation
    must take effect while an agent run is still active. Missing identities are
    not evidence of ownership. Transport/backfill failures propagate closed.
    """
    nodes = list(nodes or [])
    identities = [_identity(node) for node in nodes]
    pairs = list(dict.fromkeys(pair for pair in identities if all(pair)))
    allowed = set()
    for offset in range(0, len(pairs), 500):
        payload = {'items': [{'knowledge_id': kb, 'document_id': doc}
                             for kb, doc in pairs[offset:offset + 500]]}
        response = post_core_api('/documents:filterActive', payload, user_id=user_id)
        body = response.get('response') if isinstance(response, dict) else None
        if not isinstance(body, dict) or not isinstance(body.get('items'), list):
            raise RuntimeError('Core document ownership response is invalid')
        for item in body['items']:
            if isinstance(item, dict):
                allowed.add((str(item.get('knowledge_id') or ''), str(item.get('document_id') or '')))
    return [node for node, identity in zip(nodes, identities) if identity in allowed]

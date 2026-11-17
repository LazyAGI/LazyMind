from __future__ import annotations

import json
from typing import Any, Dict, List, Literal, Optional

from lazyllm.tools import fc_register


def has_active_document(context: Dict[str, Any]) -> bool:
    document = context.get('document_context') or {}
    return isinstance(document, dict) and all(
        isinstance(document.get(key), str) and document[key].strip()
        for key in ('dataset_id', 'document_id')
    )


def _adapt_kb_search_input(tool_input: Any) -> Any:
    """Decode only JSON-encoded kb_ids arrays before tool schema validation."""
    if not isinstance(tool_input, dict) or not isinstance(tool_input.get('kb_ids'), str):
        return tool_input
    try:
        kb_ids = json.loads(tool_input['kb_ids'])
    except (TypeError, json.JSONDecodeError):
        return tool_input
    if not isinstance(kb_ids, list) or not all(isinstance(item, str) for item in kb_ids):
        return tool_input
    adapted = dict(tool_input)
    adapted['kb_ids'] = kb_ids
    return adapted


def _toolkit(kb_scope=None):
    from lazymind.chat.runtime_loader import ensure_rag_runtime
    toolkit_class = ensure_rag_runtime().KBToolkit
    return toolkit_class(kb_scope=kb_scope) if kb_scope is not None else toolkit_class()


class KBToolkit:
    """Knowledge-base tools. “资料库” and “知识库” refer to the same resource."""

    __public_apis__ = [
        'read_document', 'kb_search',
        'kb_get_parent_node', 'kb_get_window_nodes', 'kb_keyword_search',
    ]
    __tool_input_adapters__ = {
        'kb_search': _adapt_kb_search_input,
    }

    def __init__(self, kb_scope: Optional[List[str]] = None, *, legacy_management: bool = False):
        self._kb_scope = tuple(kb_scope) if kb_scope is not None else None
        self._legacy_management = legacy_management

    @property
    def __tool_public_apis__(self):
        from lazymind.common.optional_components import component_enabled

        if not component_enabled('rag'):
            return ['read_document']
        if self._legacy_management:
            # Only the Host's pinned Workflow resolver selects this compatibility surface.
            return [
                'list_knowledge_bases', 'list_knowledge_base_documents',
                'aggregate_knowledge_base_documents', *self.__public_apis__,
            ]
        return self.__public_apis__

    def _toolkit(self):
        return _toolkit(self._kb_scope) if self._kb_scope is not None else _toolkit()

    def __lazy_source__(self) -> bool:
        import lazyllm
        agentic_config = lazyllm.globals.get('agentic_config') or {}
        return not (bool((agentic_config.get('filters') or {}).get('kb_id')) or has_active_document(agentic_config))

    @fc_register(host_file='NONE')
    def list_knowledge_bases(
        self,
        keyword: str = '',
        tags: Optional[List[str]] = None,
        page_size: int = 20,
    ) -> Dict[str, Any]:
        """List knowledge bases the current user can read."""
        return self._toolkit().list_knowledge_bases(keyword, tags, page_size)

    @fc_register(host_file='NONE')
    def list_knowledge_base_documents(
        self,
        knowledge_base_ids: List[str],
        keyword: str = '',
        page_size: int = 20,
    ) -> Dict[str, Any]:
        """List readable documents in the selected knowledge bases."""
        return self._toolkit().list_knowledge_base_documents(knowledge_base_ids, keyword, page_size)

    @fc_register(host_file='NONE')
    def aggregate_knowledge_base_documents(
        self,
        knowledge_base_ids: Optional[List[str]] = None,
        file_types: Optional[List[str]] = None,
        document_stages: Optional[List[str]] = None,
        data_source_types: Optional[List[str]] = None,
        creators: Optional[List[str]] = None,
        tags: Optional[List[str]] = None,
        group_by: Optional[List[str]] = None,
    ) -> Dict[str, Any]:
        """Aggregate readable document counts, optionally grouped by metadata."""
        return self._toolkit().aggregate_knowledge_base_documents(
            knowledge_base_ids, file_types, document_stages, data_source_types,
            creators, tags, group_by,
        )

    @fc_register(host_file='NONE')
    def kb_search(
        self,
        query: str,
        retriever_topk: Optional[int] = None,
        rerank_topk: Optional[int] = None,
        k_max: Optional[int] = None,
        image_topk: Optional[int] = None,
        filters: Optional[Dict[str, Any]] = None,
        kb_ids: Optional[List[str]] = None,
    ) -> Any:
        """Search selected knowledge bases semantically and return cited evidence.

        ``kb_ids`` must be a JSON array such as ``["ds_example"]``, not a
        string containing the array.
        """
        return self._toolkit().kb_search(
            query, retriever_topk, rerank_topk, k_max, image_topk, filters, kb_ids,
        )

    @fc_register(host_file='NONE')
    def read_document(self, knowledge_base_id: str, document_id: str) -> Dict[str, Any]:
        """Read a document without requiring an embedding model."""
        from urllib.parse import quote
        from lazyllm.tools.agent import ToolExecutionError
        from .infra.core_api_client import get_core_api

        kb_id = str(knowledge_base_id or '').strip()
        doc_id = str(document_id or '').strip()
        if not kb_id or not doc_id:
            raise ToolExecutionError('knowledge_base_id and document_id are required')
        if self._kb_scope is not None and kb_id not in self._kb_scope:
            raise ToolExecutionError('Knowledge base is outside the inherited knowledge-base scope.')
        # Core authorizes the current user and owns parsing. Importing the RAG
        # toolkit here would prevent stored/parsed libraries from being read
        # when the optional retrieval component is not installed.
        return get_core_api(
            f'/datasets/{quote(kb_id, safe="")}/documents/{quote(doc_id, safe="")}:read'
        )

    @fc_register(host_file='NONE')
    def kb_get_parent_node(self, node_id: str) -> Dict[str, Any]:
        """Get the parent node of a document node returned by search."""
        return self._toolkit().kb_get_parent_node(node_id)

    @fc_register(host_file='NONE')
    def kb_get_window_nodes(
        self,
        node_id: str,
        before: int = 5,
        after: int = 5,
    ) -> Dict[str, Any]:
        """Get neighboring document nodes around a search result."""
        return self._toolkit().kb_get_window_nodes(node_id, before, after)

    @fc_register(host_file='NONE')
    def kb_keyword_search(
        self,
        keyword: str,
        target: str,
        target_type: Literal['file_name', 'docid'] = 'file_name',
        group: str = 'block',
        phrase: bool = True,
        size: int = 10,
        sort_by: str = 'score',
        kb_ids: Optional[List[str]] = None,
    ) -> Dict[str, Any]:
        """Search for an exact term or phrase within a specific document."""
        return self._toolkit().kb_keyword_search(
            keyword, target, target_type, group, phrase, size, sort_by, kb_ids,
        )


@fc_register(host_file='NONE')
def kb_tmp_search(
    semantic_query: Optional[str] = None,
    grep_patterns: Optional[List[str]] = None,
    top_k: int = 10,
) -> Any:
    """Locate passages in this conversation's uploaded documents.

    Use for user-uploaded PDFs, Word/PPT, and prose text (txt/md). After hits,
    call read_file_resource on the returned target and line. Do not use for knowledge
    bases, url_fetch web PDFs, workspace drafts, desktop folders, or source
    code — use kb_* tools or grep for those.

    At least one of semantic_query or grep_patterns is required. Refine
    grep_patterns across later calls; parsed text is reused. Each grep pattern
    is a literal substring or regular expression (same rules as grep).

    Args:
        semantic_query: Optional natural-language question over uploaded files.
        grep_patterns: Optional list of search strings (regex or literal).
        top_k: Maximum hits to return (default 10, max 30).
    """
    from lazymind.chat.engine.tools.temporary_files import kb_tmp_search as search_uploads
    return search_uploads(
        semantic_query, grep_patterns, top_k,
    )

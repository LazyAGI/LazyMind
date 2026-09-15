from __future__ import annotations

from typing import Any, Dict, List, Literal, Optional


def _toolkit(kb_scope=None):
    from lazymind.chat.runtime_loader import ensure_rag_runtime
    toolkit_class = ensure_rag_runtime().KBToolkit
    return toolkit_class(kb_scope=kb_scope) if kb_scope is not None else toolkit_class()


class KBToolkit:
    """Knowledge-base discovery, inspection, search, and navigation tools."""

    __public_apis__ = [
        'list_knowledge_bases', 'list_knowledge_base_documents',
        'aggregate_knowledge_base_documents', 'kb_search',
        'kb_get_parent_node', 'kb_get_window_nodes', 'kb_keyword_search',
    ]
    __tool_auto_activate__ = [r'知识库|(?<!\w)knowledge[\s_-]+bases?(?!\w)']

    def __init__(self, kb_scope: Optional[List[str]] = None):
        self._kb_scope = tuple(kb_scope) if kb_scope is not None else None

    def _toolkit(self):
        return _toolkit(self._kb_scope) if self._kb_scope is not None else _toolkit()

    def __lazy_source__(self) -> bool:
        import lazyllm
        agentic_config = lazyllm.globals.get('agentic_config') or {}
        return not bool((agentic_config.get('filters') or {}).get('kb_id'))

    def list_knowledge_bases(
        self,
        keyword: str = '',
        tags: Optional[List[str]] = None,
        page_size: int = 20,
    ) -> Dict[str, Any]:
        """List knowledge bases the current user can read."""
        return self._toolkit().list_knowledge_bases(keyword, tags, page_size)

    def list_knowledge_base_documents(
        self,
        knowledge_base_ids: List[str],
        keyword: str = '',
        page_size: int = 20,
    ) -> Dict[str, Any]:
        """List readable documents in the selected knowledge bases."""
        return self._toolkit().list_knowledge_base_documents(knowledge_base_ids, keyword, page_size)

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
        """Search selected knowledge bases semantically and return cited evidence."""
        return self._toolkit().kb_search(
            query, retriever_topk, rerank_topk, k_max, image_topk, filters, kb_ids,
        )

    def kb_get_parent_node(self, node_id: str) -> Dict[str, Any]:
        """Get the parent node of a document node returned by search."""
        return self._toolkit().kb_get_parent_node(node_id)

    def kb_get_window_nodes(
        self,
        node_id: str,
        before: int = 5,
        after: int = 5,
    ) -> Dict[str, Any]:
        """Get neighboring document nodes around a search result."""
        return self._toolkit().kb_get_window_nodes(node_id, before, after)

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


def kb_tmp_search(
    semantic_query: Optional[str] = None,
    grep_patterns: Optional[List[str]] = None,
    top_k: int = 10,
) -> Any:
    """Locate passages in this conversation's uploaded documents.

    Use for user-uploaded PDFs, Word/PPT, and prose text (txt/md). After hits,
    call read_file on the returned target and line. Do not use for knowledge
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
    from lazymind.chat.runtime_loader import ensure_rag_runtime
    return ensure_rag_runtime().kb_tmp_search(
        semantic_query, grep_patterns, top_k,
    )

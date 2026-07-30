from __future__ import annotations

from typing import Any, Dict, List, Literal, Optional


def _toolkit():
    from lazymind.chat.runtime_loader import ensure_rag_runtime
    return ensure_rag_runtime().KBToolkit()


class KBToolkit:
    """Knowledge-base discovery, inspection, search, and navigation tools."""

    __public_apis__ = [
        'list_knowledge_bases', 'list_knowledge_base_documents',
        'aggregate_knowledge_base_documents', 'kb_search',
        'kb_get_parent_node', 'kb_get_window_nodes', 'kb_keyword_search',
    ]
    __tool_auto_activate__ = [r'知识库|(?<!\w)knowledge[\s_-]+bases?(?!\w)']

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
        return _toolkit().list_knowledge_bases(keyword, tags, page_size)

    def list_knowledge_base_documents(
        self,
        knowledge_base_ids: List[str],
        keyword: str = '',
        page_size: int = 20,
    ) -> Dict[str, Any]:
        """List readable documents in the selected knowledge bases."""
        return _toolkit().list_knowledge_base_documents(knowledge_base_ids, keyword, page_size)

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
        return _toolkit().aggregate_knowledge_base_documents(
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
        return _toolkit().kb_search(
            query, retriever_topk, rerank_topk, k_max, image_topk, filters, kb_ids,
        )

    def kb_get_parent_node(self, node_id: str) -> Dict[str, Any]:
        """Get the parent node of a document node returned by search."""
        return _toolkit().kb_get_parent_node(node_id)

    def kb_get_window_nodes(
        self,
        node_id: str,
        before: int = 5,
        after: int = 5,
    ) -> Dict[str, Any]:
        """Get neighboring document nodes around a search result."""
        return _toolkit().kb_get_window_nodes(node_id, before, after)

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
        return _toolkit().kb_keyword_search(
            keyword, target, target_type, group, phrase, size, sort_by, kb_ids,
        )


def kb_tmp_search(
    query: str,
    retriever_topk: Optional[int] = None,
    rerank_topk: Optional[int] = None,
    k_max: Optional[int] = None,
    files: Optional[List[str]] = None,
) -> Any:
    """Search attached temporary uploaded files with the temporary document retriever."""
    from lazymind.chat.runtime_loader import ensure_rag_runtime
    return ensure_rag_runtime().kb_tmp_search(
        query, retriever_topk, rerank_topk, k_max, files,
    )

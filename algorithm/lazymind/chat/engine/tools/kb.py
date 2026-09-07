from typing import Any, Dict, List, Literal, Optional, Sequence

import os
from concurrent.futures import TimeoutError as FuturesTimeoutError
from pathlib import Path
from urllib.parse import quote

import lazyllm
from lazyllm import AutoModel, LOG
from lazyllm.common.threading import ThreadPoolExecutor
from lazyllm.tools.agent import ToolExecutionError
from lazyllm.tools.rag import Reranker, Retriever

from lazymind.chat.engine.tools.infra import (
    get_core_api,
    get_vocab_manager,
    post_core_api,
    resolve_index,
)
from lazymind.chat.engine.tools._utils import (
    iter_lookup_ids,
    parse_json_dict,
    truncate_text,
)
from lazymind.chat.engine.tools.algo import DOCUMENT, search_kb
from lazymind.chat.service.utils import (
    basename_from_path,
    local_path_from_static_file_url,
    static_file_url_from_any,
)
from lazymind.config import EMBED_IMAGE, EMBED_MAIN, config as _cfg
from lazymind.model_config import get_dynamic_role_slot_map

_MAX_TEXT_LEN = 1200
_MAX_RESULT_ITEMS = 50
_DEFAULT_RETRIEVER_TOPK = 20
_DEFAULT_RERANK_TOPK = 20
_DEFAULT_K_MAX = 10
_DEFAULT_IMAGE_TOPK = 3
_ACCESSIBLE_KB_IDS_CACHE_KEY = '_accessible_kb_ids'
_RERANKER_MODULE = 'ModuleReranker'
_RERANKER_MODEL = 'reranker'
_KB_RETRIEVER_CONFIGS = [
    {'group_name': 'line', 'embed_keys': [EMBED_MAIN], 'target': 'block'},
    {'group_name': 'block', 'embed_keys': [EMBED_MAIN]},
]
_KB_IMAGE_RETRIEVER_CONFIG = {
    'group_name': 'image',
    'embed_keys': [EMBED_IMAGE],
}
_kb_retrievers = None
_kb_reranker = None
_kb_image_retriever = None

_TMP_WHITELIST_SUFFIXES = frozenset({
    '.pdf', '.doc', '.docx', '.pptx', '.txt', '.md', '.markdown', '.lmd',
})
_TMP_OFFICE_SUFFIXES = frozenset({'.doc', '.docx', '.pptx'})
_TMP_PARSE_TIMEOUT_SECONDS = 60
_TMP_DEFAULT_TOP_K = 10
_TMP_MAX_TOP_K = 30
_TMP_DEFAULT_RETRIEVER_TOPK = 20
_TMP_DEFAULT_RERANK_TOPK = 20
_TMP_DEFAULT_K_MAX = 10
_TMP_HINT = (
    'After a hit, call read_file(target, offset=max(1, line-20), limit=80) '
    'for surrounding context. Read footers decide EOF, not document headings. '
    'Do not use this tool for knowledge bases, fetched web PDFs, workspace drafts, '
    'or source code; use kb_* tools or grep instead.'
)


def _is_reranker_enabled() -> bool:
    role_slots = get_dynamic_role_slot_map()
    if 'reranker' not in role_slots:
        return True

    try:
        cfg = lazyllm.globals.config['dynamic_model_configs']
    except Exception:
        cfg = None
    role_cfg = cfg.get('reranker') if isinstance(cfg, dict) else None
    return isinstance(role_cfg, dict) and bool(role_cfg.get(role_slots['reranker']))


def _build_reranker() -> Optional[Reranker]:
    return (
        Reranker(_RERANKER_MODULE, model=AutoModel(model=_RERANKER_MODEL))
        if _is_reranker_enabled()
        else None
    )


def _ensure_kb_search_runtime() -> tuple[List[Retriever], Optional[Reranker], Retriever]:
    global _kb_retrievers, _kb_reranker, _kb_image_retriever
    if _kb_retrievers is not None and _kb_image_retriever is not None:
        return _kb_retrievers, _kb_reranker, _kb_image_retriever

    _kb_retrievers = [Retriever(DOCUMENT, **cfg) for cfg in _KB_RETRIEVER_CONFIGS]
    _kb_reranker = _build_reranker()
    _kb_image_retriever = Retriever(DOCUMENT, **_KB_IMAGE_RETRIEVER_CONFIG)
    return _kb_retrievers, _kb_reranker, _kb_image_retriever


def _serialize_doc_node_like(node: Any) -> Dict[str, Any]:
    metadata = getattr(node, 'metadata', {}) or {}
    if not isinstance(metadata, dict):
        metadata = {}
    global_md = getattr(node, 'global_metadata', {}) or {}
    if not isinstance(global_md, dict):
        global_md = {}
    compact_metadata = {
        k: metadata[k]
        for k in (
            'type',
            'node_type',
            'index',
            'file_name',
            'source',
            'source_path',
            'normalized_source_path',
            'store_num',
            'lazyllm_store_num',
            'page',
            'bbox',
            'images',
        )
        if k in metadata
    }
    group = getattr(node, 'group', None) or getattr(node, '_group', None)
    text = getattr(node, 'text', '') or ''
    raw_text = text.strip() if isinstance(text, str) else ''
    local_path = raw_text
    if raw_text.startswith('/static-files/'):
        resolved = local_path_from_static_file_url(raw_text)
        if resolved:
            local_path = resolved
    is_image = group == 'image' or (
        local_path.startswith('/var/lib/lazymind/uploads/')
        and local_path.lower().endswith(('.jpg', '.jpeg', '.png', '.gif', '.webp', '.bmp'))
    )
    image_markdown = None
    # Prefer durable normalized JPEG over volatile OCR cache (.image_cache).
    render_path = (
        metadata.get('normalized_source_path')
        or metadata.get('source_path')
        or ''
    )
    if isinstance(render_path, str):
        render_path = render_path.strip()
    else:
        render_path = ''
    if render_path:
        signed = static_file_url_from_any(render_path)
        text = signed
        compact_metadata = dict(compact_metadata)
        compact_metadata['image_url'] = signed
        compact_metadata['local_path'] = render_path
        doc_file_name = global_md.get('file_name') or compact_metadata.get('file_name')
        file_label = doc_file_name or basename_from_path(signed)
        image_markdown = f'![{file_label}]({signed})'
    elif is_image and local_path:
        signed = static_file_url_from_any(local_path)
        if signed:
            text = signed
            compact_metadata = dict(compact_metadata)
            compact_metadata['image_url'] = signed
            compact_metadata['local_path'] = local_path
            file_label = (
                compact_metadata.get('file_name')
                or global_md.get('file_name')
                or basename_from_path(signed)
            )
            image_markdown = f'![{file_label}]({signed})'
    else:
        local_path = ''

    doc_file_name = (
        global_md.get('file_name') or compact_metadata.get('file_name')
        if group == 'image'
        else compact_metadata.get('file_name') or global_md.get('file_name')
    )
    serialized = {
        'uid': getattr(node, 'uid', None) or getattr(node, '_uid', None),
        'number': getattr(node, 'number', metadata.get('index')),
        'group': group,
        'parent': getattr(node, '_parent', None),
        'score': getattr(node, 'relevance_score', None),
        'text': truncate_text(text, _MAX_TEXT_LEN),
        'docid': global_md.get('docid'),
        'kb_id': global_md.get('kb_id'),
        'file_name': doc_file_name,
        'metadata': compact_metadata,
        'global_metadata': global_md,
    }
    if image_markdown:
        serialized['image_markdown'] = image_markdown
        serialized['local_path'] = render_path or local_path
    return serialized


def _store_dict_to_result(d: Dict[str, Any]) -> Dict[str, Any]:
    meta = d.get('meta', {})
    if isinstance(meta, str):
        meta = parse_json_dict(meta)
    global_meta = d.get('global_meta', {})
    if isinstance(global_meta, str):
        global_meta = parse_json_dict(global_meta)
    return {
        'uid': d.get('uid'),
        'number': d.get('number'),
        'group': d.get('group'),
        'parent': d.get('parent'),
        'score': d.get('score'),
        'text': truncate_text(d.get('content', '') or '', _MAX_TEXT_LEN),
        'docid': d.get('doc_id') or global_meta.get('docid'),
        'kb_id': d.get('kb_id') or global_meta.get('kb_id'),
        'file_name': global_meta.get('file_name'),
        'metadata': meta,
        'global_metadata': global_meta,
        'highlights': d.get('highlights', []),
    }


def _serialize_kb_result(result: Any) -> Any:
    if isinstance(result, (str, int, float, bool)) or result is None:
        return result
    if isinstance(result, dict):
        result = dict(result)
        if isinstance(result.get('items'), list):
            serialized = _serialize_kb_result(result['items'])
            if isinstance(serialized, dict):
                result['items'] = serialized.get('items', result['items'])
                result.setdefault('total', serialized.get('total'))
        return result
    if isinstance(result, tuple):
        result = list(result)
    if isinstance(result, list):
        serialized_items = []
        for item in result[:_MAX_RESULT_ITEMS]:
            if isinstance(item, (str, int, float, bool)) or item is None:
                serialized_items.append(item)
                continue
            if isinstance(item, dict):
                serialized_items.append(item)
                continue
            if getattr(item, 'uid', None) is not None or getattr(item, 'text', None) is not None:
                serialized_items.append(_serialize_doc_node_like(item))
                continue
            serialized_items.append(truncate_text(item, 400))
        return {
            'total': len(result),
            'items': serialized_items,
        }
    return truncate_text(result, 400)


def _string_list(value: Any) -> List[str]:
    if value is None:
        return []
    if isinstance(value, str):
        return [item.strip() for item in value.split(',') if item.strip()]
    if isinstance(value, (list, tuple, set)):
        return [str(item).strip() for item in value if str(item).strip()]
    return [str(value).strip()] if str(value).strip() else []


def _bounded_page_size(value: int, default: int = 20) -> int:
    try:
        page_size = int(value)
    except (TypeError, ValueError):
        page_size = default
    if page_size <= 0:
        return default
    return min(page_size, 100)


class KBToolkit:
    """Knowledge-base discovery, inspection, search, and navigation tools.

    Use this Toolkit when the user selects or @mentions a knowledge base, or
    explicitly asks to discover, inspect, or search knowledge bases. If only the
    gateway is visible and you decide this Toolkit is relevant, activate the
    gateway before calling its methods. Do not activate it for unrelated requests.

    Use list_knowledge_bases to discover a knowledge base, then inspect its
    documents or aggregates. Use kb_search for open-ended semantic questions,
    kb_keyword_search for an exact phrase in a known document, and the parent
    or window tools only to expand context around an existing search hit.
    Search methods require either explicit kb_ids or a knowledge-base selection
    in the current request. Retrieved evidence carries citation markers that
    must be preserved verbatim in the final answer.
    """

    __public_apis__ = [
        'list_knowledge_bases', 'list_knowledge_base_documents',
        'aggregate_knowledge_base_documents', 'kb_search',
        'kb_get_parent_node', 'kb_get_window_nodes', 'kb_keyword_search',
    ]
    __tool_auto_activate__ = [
        r'知识库|(?<!\w)knowledge[\s_-]+bases?(?!\w)',
    ]

    def __init__(self, kb_scope: Optional[List[str]] = None):
        self._kb_scope = tuple(_string_list(kb_scope)) if kb_scope is not None else None

    def _check_node_scope(self, nodes: list) -> None:
        if self._kb_scope is not None:
            for node in nodes:
                metadata = getattr(node, 'global_metadata', {}) or {}
                if metadata.get('kb_id') not in self._kb_scope:
                    raise ToolExecutionError('Node is outside the inherited knowledge-base scope.')

    def __lazy_source__(self) -> bool:
        """Stay lazy only while the request has no explicit knowledge-base scope."""
        agentic_config = lazyllm.globals.get('agentic_config') or {}
        return not bool((agentic_config.get('filters') or {}).get('kb_id'))

    def list_knowledge_bases(
        self,
        keyword: str = '',
        tags: Optional[List[str]] = None,
        page_size: int = 20,
    ) -> Dict[str, Any]:
        """List knowledge bases the current user can read."""
        if self._kb_scope is not None:
            datasets = [get_core_api(f'/datasets/{quote(kb_id, safe="")}') for kb_id in self._kb_scope]
            tag_values = set(_string_list(tags))
            datasets = [item for item in datasets
                        if (not keyword or keyword.lower() in str(item.get('display_name') or '').lower())
                        and tag_values.issubset(item.get('tags') or [])]
            return {'datasets': datasets[:_bounded_page_size(page_size)], 'total_size': len(datasets)}
        params: Dict[str, Any] = {'page_size': _bounded_page_size(page_size)}
        if keyword:
            params['keyword'] = keyword
        tag_values = _string_list(tags)
        if tag_values:
            params['tags'] = ','.join(tag_values)
        return get_core_api('/datasets', params=params)

    def list_knowledge_base_documents(
        self,
        knowledge_base_ids: List[str],
        keyword: str = '',
        page_size: int = 20,
    ) -> Dict[str, Any]:
        """List readable documents in the selected knowledge bases."""
        payload: Dict[str, Any] = {
            'dataset_ids': (
                self._kb_ids(knowledge_base_ids) if self._kb_scope is not None else _string_list(knowledge_base_ids)
            ),
            'page_size': _bounded_page_size(page_size),
        }
        if keyword:
            payload['keyword'] = keyword
        return post_core_api('/documents:listByDatasets', payload)['response']

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
        """Aggregate readable document counts, optionally grouped by metadata fields."""
        payload = {
            'dataset_ids': (
                self._kb_ids(knowledge_base_ids) if self._kb_scope is not None else _string_list(knowledge_base_ids)
            ),
            'file_types': _string_list(file_types),
            'document_stages': _string_list(document_stages),
            'data_source_types': _string_list(data_source_types),
            'creators': _string_list(creators),
            'tags': _string_list(tags),
            'group_by': _string_list(group_by),
        }
        return post_core_api('/system-query/documents:aggregate', payload)

    @staticmethod
    def _accessible_kb_ids() -> set[str]:
        """Return the complete readable KB id set, cached for this agent run."""
        config = lazyllm.globals.get('agentic_config') or {}
        cached = config.get(_ACCESSIBLE_KB_IDS_CACHE_KEY)
        if isinstance(cached, (list, tuple, set)):
            return {str(item).strip() for item in cached if str(item).strip()}

        accessible: set[str] = set()
        page_token = ''
        seen_page_tokens: set[str] = set()
        while True:
            params: Dict[str, Any] = {'page_size': 100}
            if page_token:
                params['page_token'] = page_token
            response = get_core_api('/datasets', params=params)
            for item in response.get('datasets') or []:
                if not isinstance(item, dict):
                    continue
                dataset_id = str(item.get('dataset_id') or '').strip()
                if dataset_id:
                    accessible.add(dataset_id)

            next_page_token = str(response.get('next_page_token') or '').strip()
            if not next_page_token:
                break
            if next_page_token in seen_page_tokens:
                raise RuntimeError('knowledge-base catalog returned a repeated page token')
            seen_page_tokens.add(next_page_token)
            page_token = next_page_token

        config[_ACCESSIBLE_KB_IDS_CACHE_KEY] = sorted(accessible)
        lazyllm.globals['agentic_config'] = config
        return accessible

    def _kb_ids(self, explicit: Optional[List[str]] = None) -> List[str]:
        config = lazyllm.globals.get('agentic_config') or {}
        selected = explicit if explicit else (
            list(self._kb_scope) if self._kb_scope is not None else (config.get('filters') or {}).get('kb_id')
        )
        ids = [str(item).strip() for item in iter_lookup_ids(selected, field_name='kb_ids') if item]
        if self._kb_scope is not None and any(kb_id not in self._kb_scope for kb_id in ids):
            raise ToolExecutionError('Knowledge base is outside the inherited knowledge-base scope.')
        if not ids:
            raise ToolExecutionError(
                'kb_ids is required when no knowledge base is selected in the request'
            )
        if explicit:
            accessible = KBToolkit._accessible_kb_ids()
            if any(kb_id not in accessible for kb_id in ids):
                raise ToolExecutionError(
                    'One or more requested knowledge bases are unavailable.'
                )
        return ids

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
        """Search the knowledge base and return text and image retrieval results.

        Use this semantic search method for open-ended knowledge-base questions.
        Search with the user's core question, and treat the returned text and
        image retrieval results as the primary evidence before answering.

        IMPORTANT: Each call handles exactly ONE search intent. If the user asks
        about multiple unrelated keywords or topics, you MUST call this tool
        separately for each keyword/topic — do NOT combine unrelated terms into
        one query with spaces, commas, or list-like text.

        For example, if the user asks "What is the difference between Redis and
        Kafka?", call this tool twice: once with query="Redis" and once with
        query="Kafka", rather than a single call with query="Redis Kafka".

        Args:
            query: A SINGLE natural language query for retrieval. Do NOT put
                multiple unrelated keywords in this field.
            retriever_topk: Candidate count used by each retriever route before
                fusion. Defaults to 20.
            rerank_topk: Number of nodes the reranker keeps before adaptive-k
                trimming. Defaults to 20.
            k_max: Hard upper bound on the adaptive-k stage. Defaults to 10.
            image_topk: Top-k for the image retrieval branch. Defaults to 3.
            filters: Metadata filters for retrieval, e.g.
                {'file_name': 'report.pdf'}.
            kb_ids: Knowledge-base IDs. Overrides the knowledge bases selected
                in the current request.
        """
        agentic_config = lazyllm.globals['agentic_config']
        selected_ids = self._kb_ids(kb_ids)
        retrievers, reranker, image_retriever = _ensure_kb_search_runtime()
        effective_filters = dict(filters or agentic_config.get('filters') or {})
        effective_filters['kb_id'] = selected_ids
        payload = {
            'query': query.strip(),
            'filters': effective_filters,
            'user_id': agentic_config.get('user_id', ''),
        }

        result = search_kb(
            payload,
            retrievers=retrievers,
            reranker=reranker,
            image_retriever=image_retriever,
            retriever_topk=retriever_topk or _DEFAULT_RETRIEVER_TOPK,
            rerank_topk=rerank_topk or _DEFAULT_RERANK_TOPK,
            k_max=k_max or _DEFAULT_K_MAX,
            image_topk=image_topk or _DEFAULT_IMAGE_TOPK,
        )
        serialized = _serialize_kb_result(result)
        return serialized

    def kb_get_parent_node(self, node_id: str) -> Dict[str, Any]:
        """Get the parent node of a target document node.

        Retrieves the parent node (e.g., section heading or enclosing
        paragraph) for a given chunk node. This provides the section-level
        context needed to fully understand the chunk's content.

        Args:
            node_id: Target document node uid.

        Returns:
            The matched parent node, if the current node has a parent and the
            parent can be found.
        """
        doc = DOCUMENT
        current_nodes = doc.get_nodes(uids=[node_id])
        current_nodes = current_nodes if isinstance(current_nodes, list) else []
        self._check_node_scope(current_nodes)
        if current_nodes:
            current_node = current_nodes[0]
            current = _serialize_doc_node_like(current_node)
            parent_id = current.get('parent')
            if parent_id:
                global_metadata = getattr(current_node, 'global_metadata', {}) or {}
                kb_id = global_metadata.get('kb_id') if isinstance(global_metadata, dict) else None
                parent_nodes = doc.get_nodes(uids=[parent_id], kb_id=kb_id)
                parent_nodes = parent_nodes if isinstance(parent_nodes, list) else []
                self._check_node_scope(parent_nodes)
                parent = _serialize_doc_node_like(parent_nodes[0]) if parent_nodes else None
            else:
                parent = None
            result = {
                'node_id': node_id,
                'current_node': current,
                'parent_id': parent_id,
                'total': 1 if parent else 0,
                'items': [parent] if parent else [],
            }
            return result

        result = {
            'node_id': node_id,
            'current_node': None,
            'parent_id': None,
            'total': 0,
            'items': [],
        }
        return result

    def kb_get_window_nodes(
        self,
        node_id: str,
        before: int = 5,
        after: int = 5,
    ) -> Dict[str, Any]:
        """Get neighboring nodes around a target node.

        The target node supplies its own knowledge-base, document, group, and
        position metadata, so callers only need the node id returned by search.

        Args:
            node_id: Target document node uid returned by a search method.
            before: Maximum number of preceding nodes. Defaults to 5.
            after: Maximum number of following nodes. Defaults to 5.

        Returns:
            A compact dict with node numbers and contents only.
        """
        before = int(before)
        after = int(after)
        if before < 0 or after < 0:
            raise ToolExecutionError('before and after must be non-negative')
        if before + after + 1 > _MAX_RESULT_ITEMS:
            raise ToolExecutionError(f'window cannot exceed {_MAX_RESULT_ITEMS} nodes')
        doc = DOCUMENT
        seed_nodes = doc.get_nodes(uids=[node_id])
        seed_nodes = seed_nodes if isinstance(seed_nodes, list) else []
        self._check_node_scope(seed_nodes)
        if seed_nodes:
            nodes = doc.get_window_nodes(seed_nodes[0], span=(-before, after), merge=False)
            nodes = nodes if isinstance(nodes, list) else []
            self._check_node_scope(nodes)
            result = {
                'total': len(nodes),
                'items': [_serialize_doc_node_like(n) for n in nodes],
            }
            return result

        result = {
            'total': 0,
            'items': [],
        }
        return result

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
        """Search for exact keyword or phrase matches within a specific document.

        Use when the user names a document file -- pass it as ``target`` with
        ``target_type='file_name'``.
        Performs full-text keyword matching inside one target document,
        useful for finding all occurrences of a term or checking whether a
        document mentions something specific.

        You must provide ``target`` to identify the document. By default
        ``target`` is treated as a file name. Use ``target_type='docid'`` only
        when a document id is already known.

        Use this method only when the user names a specific document and asks for
        an exact term or phrase inside that document. For open-ended semantic
        questions, use kb_search instead.

        Args:
            keyword: Keyword or phrase to search in ``content``.
            target: Target file name or document id.
            target_type: How to interpret ``target``; either ``file_name`` or
                ``docid``. Defaults to ``file_name``.
            group: Search granularity, either ``block`` or ``line``.
            phrase: Use ``match_phrase`` when true, otherwise ``match``.
            size: Maximum number of hits.
            sort_by: score for relevance first, or number for document
                order.

        Returns:
            Matching nodes with content snippets.
        """
        index_name = resolve_index(group)
        size = max(1, min(int(size), _MAX_RESULT_ITEMS))
        doc = DOCUMENT
        docid = target if target_type == 'docid' else ''
        file_name = target if target_type == 'file_name' else None
        if not keyword:
            raise ToolExecutionError('keyword is required')
        if not (target and str(target).strip()):
            raise ToolExecutionError('target is required')
        LOG.info(f'[kb_keyword_search] store={_cfg["segment_store_type"]!r} keyword={keyword!r} docid={docid!r} '
                 f'file_name={file_name!r} group={group!r} phrase={phrase} sort_by={sort_by!r} size={size}')

        for kb_id in self._kb_ids(kb_ids):
            LOG.info(f'[kb_keyword_search] trying kb_id={kb_id!r}')
            nodes = doc.keyword_search(
                group=group, keyword=keyword, doc_id=docid,
                kb_id=kb_id, phrase=phrase, sort_by=sort_by, size=size,
                file_name=file_name,
            )
            LOG.info(f'[kb_keyword_search] doc.keyword_search returned {len(nodes)} nodes')
            if not nodes:
                continue
            result = {
                'index': index_name,
                'group': group,
                'docid': docid,
                'file_name': file_name,
                'keyword': keyword,
                'total': len(nodes),
                'items': [_store_dict_to_result(n) for n in nodes],
            }
            return result

        return {
            'index': index_name, 'group': group, 'docid': docid,
            'file_name': file_name, 'keyword': keyword, 'total': 0, 'items': [],
        }


def _tmp_suffix(path: str) -> str:
    return Path(str(path or '').split('?', 1)[0]).suffix.lower()


def _tmp_clamp_top_k(top_k: Optional[int]) -> int:
    try:
        value = int(top_k if top_k is not None else _TMP_DEFAULT_TOP_K)
    except (TypeError, ValueError):
        value = _TMP_DEFAULT_TOP_K
    if value <= 0:
        return 1
    return min(value, _TMP_MAX_TOP_K)


def _tmp_pattern_list(grep_patterns: Any) -> List[str]:
    if grep_patterns is None:
        return []
    if isinstance(grep_patterns, str):
        item = grep_patterns.strip()
        return [item] if item else []
    if isinstance(grep_patterns, Sequence) and not isinstance(grep_patterns, (bytes, bytearray)):
        return [str(item).strip() for item in grep_patterns if str(item).strip()]
    item = str(grep_patterns).strip()
    return [item] if item else []


def _tmp_run_with_timeout(fn, timeout: float):
    with ThreadPoolExecutor(max_workers=1) as pool:
        future = pool.submit(fn)
        try:
            return future.result(timeout=timeout)
        except FuturesTimeoutError as exc:
            raise TimeoutError(
                f'parse exceeded {_TMP_PARSE_TIMEOUT_SECONDS}s'
            ) from exc


def _tmp_agentic_config() -> dict:
    cfg = lazyllm.globals.get('agentic_config') or {}
    return cfg if isinstance(cfg, dict) else {}


def _tmp_collect_uploads() -> tuple[list[dict], list[dict]]:
    from lazymind.chat.engine.tools.local_file.resolver import (
        _dedupe_turn,
        materialize_local_path,
    )

    cfg = _tmp_agentic_config()
    history = cfg.get('history_files_per_turn') or {}
    if not isinstance(history, dict):
        history = {}
    current = [str(path) for path in (cfg.get('files') or []) if str(path).strip()]
    skipped: list[dict] = []
    items: list[dict] = []
    seen: set[str] = set()

    def consider(display: str, raw: str, turn: Optional[int]) -> None:
        if str(raw).lower().startswith(('http://', 'https://')):
            skipped.append({'target': display, 'reason': 'remote_url'})
            return
        source = materialize_local_path(raw)
        if not source or not os.path.isfile(source):
            skipped.append({'target': display, 'reason': 'missing'})
            return
        real = os.path.realpath(source)
        if real in seen:
            return
        seen.add(real)
        suffix = _tmp_suffix(source)
        if suffix not in _TMP_WHITELIST_SUFFIXES:
            skipped.append({'target': display, 'reason': 'not_in_whitelist'})
            return
        items.append({
            'target': display,
            'source_path': real,
            'suffix': suffix,
            'turn': turn,
        })

    seqs = sorted((int(key) for key in history if str(key).isdigit()), reverse=True)
    for seq in seqs:
        paths = [str(path) for path in (history.get(str(seq)) or []) if str(path).strip()]
        for display, path in _dedupe_turn(paths):
            consider(display, path, seq)
    for display, path in _dedupe_turn(current):
        consider(display, path, None)
    return items, skipped


def _tmp_prepare_doc(item: dict) -> dict:
    from lazymind.chat.engine.tools.local_file.store import workspace_for_request

    suffix = item['suffix']
    source = item['source_path']
    if suffix == '.pdf':
        from lazymind.chat.engine.tools.local_file.ingest import ingest_pdf_file
        from lazymind.chat.engine.tools.local_file.store import FileResourceStore

        store = FileResourceStore(workspace_for_request())

        def ingest():
            return ingest_pdf_file(
                source,
                source='upload',
                display_name=item['target'],
                turn_seq=item.get('turn'),
                store=store,
            )

        manifest = _tmp_run_with_timeout(ingest, _TMP_PARSE_TIMEOUT_SECONDS)
        if manifest.get('parse_status') != 'ready':
            raise ValueError(manifest.get('parse_error') or 'pdf parse failed')
        parsed = str(manifest.get('parsed_path') or '')
        if not os.path.isfile(parsed):
            raise FileNotFoundError(parsed)
        return {
            **item,
            'text_path': parsed,
            'file_id': manifest.get('file_id'),
            'kind': 'pdf',
            'parse': 'ready',
        }
    if suffix in _TMP_OFFICE_SUFFIXES:
        from lazymind.chat.engine.tools.local_file.resolver import _materialize_document_text

        workspace = workspace_for_request()

        def parse_office():
            return _materialize_document_text(source, workspace)

        parsed = _tmp_run_with_timeout(parse_office, _TMP_PARSE_TIMEOUT_SECONDS)
        return {
            **item,
            'text_path': parsed,
            'file_id': None,
            'kind': 'office',
            'parse': 'ready',
        }
    return {
        **item,
        'text_path': source,
        'file_id': None,
        'kind': 'text',
        'parse': 'ready',
    }


def _tmp_chunk_line(lines: List[str], chunk: str) -> tuple[int, bool]:
    text = str(chunk or '').strip()
    if not text:
        return 1, True
    first = next((part.strip() for part in text.splitlines() if part.strip()), '')
    needle = first[:160] if first else text[:160]
    if needle:
        for index, line in enumerate(lines, start=1):
            if needle in line:
                return index, False
    compact = ' '.join(text.split())[:120]
    if compact:
        for index, line in enumerate(lines, start=1):
            if compact[:40] and compact[:40] in ' '.join(line.split()):
                return index, False
    for index, line in enumerate(lines, start=1):
        if line.startswith('<!-- page:'):
            return index, True
    return 1, True


def _tmp_grep_hits(docs: List[dict], patterns: List[str]) -> List[dict]:
    from lazymind.chat.engine.tools.local_file.window import grep_lines, load_text_lines

    hits: list[dict] = []
    for pattern in patterns:
        for doc in docs:
            try:
                lines = load_text_lines(doc['text_path'])
            except (OSError, ValueError):
                continue
            found = grep_lines(lines, pattern, max_results=50)
            for item in found.get('matches') or []:
                hits.append({
                    'target': doc['target'],
                    'file_id': doc.get('file_id'),
                    'line': item.get('line'),
                    'snippet': item.get('text'),
                    'channels': ['grep'],
                    'patterns': [pattern],
                })
    return hits


def _tmp_semantic_hits(docs: List[dict], query: str, user_id: str) -> tuple[List[dict], str]:
    from lazymind.chat.engine.tools.algo.search_temp import embed_available, retrieve_temp_nodes
    from lazymind.chat.engine.tools.local_file.window import load_text_lines

    expanded = get_vocab_manager(user_id)(query)
    files = [doc['text_path'] for doc in docs]
    channel = 'vector' if embed_available() else 'bm25_chinese'
    by_path = {doc['text_path']: doc for doc in docs}
    nodes = retrieve_temp_nodes(
        files,
        expanded,
        retriever_topk=_TMP_DEFAULT_RETRIEVER_TOPK,
        rerank_topk=_TMP_DEFAULT_RERANK_TOPK,
        k_max=_TMP_DEFAULT_K_MAX,
    )
    hits: list[dict] = []
    for node in nodes or []:
        meta = getattr(node, 'global_metadata', None) or {}
        if not isinstance(meta, dict):
            meta = {}
        local_meta = getattr(node, 'metadata', None) or {}
        if not isinstance(local_meta, dict):
            local_meta = {}
        path = (
            str(meta.get('file_path') or meta.get('source_path') or '')
            or str(local_meta.get('file_path') or local_meta.get('source_path') or '')
        )
        doc = by_path.get(os.path.realpath(path) if path else '')
        if doc is None and path:
            for candidate in docs:
                if os.path.basename(candidate['text_path']) == os.path.basename(path):
                    doc = candidate
                    break
        if doc is None and len(docs) == 1:
            doc = docs[0]
        if doc is None:
            continue
        try:
            lines = load_text_lines(doc['text_path'])
        except (OSError, ValueError):
            continue
        line, estimated = _tmp_chunk_line(lines, getattr(node, 'text', '') or '')
        snippet = str(getattr(node, 'text', '') or '').strip().replace('\n', ' ')
        if len(snippet) > 240:
            snippet = snippet[:237] + '...'
        hits.append({
            'target': doc['target'],
            'file_id': doc.get('file_id'),
            'line': line,
            'snippet': snippet,
            'channels': [channel],
            'line_estimate': estimated,
        })
    return hits, channel


def _tmp_merge_hits(grep_hits: List[dict], semantic_hits: List[dict], top_k: int) -> List[dict]:
    merged: list[dict] = []
    seen: dict[tuple, dict] = {}

    def add(hit: dict, prefer_grep: bool) -> None:
        key = (hit.get('target'), int(hit.get('line') or 0))
        existing = seen.get(key)
        if existing is None:
            seen[key] = hit
            merged.append(hit)
            return
        channels = list(dict.fromkeys(
            list(existing.get('channels') or []) + list(hit.get('channels') or [])
        ))
        existing['channels'] = channels
        patterns = list(dict.fromkeys(
            list(existing.get('patterns') or []) + list(hit.get('patterns') or [])
        ))
        if patterns:
            existing['patterns'] = patterns
        if prefer_grep and 'grep' in (hit.get('channels') or []):
            if hit.get('snippet'):
                existing['snippet'] = hit['snippet']

    for hit in grep_hits:
        add(hit, prefer_grep=True)
    for hit in semantic_hits:
        add(hit, prefer_grep=False)
    return merged[:top_k]


def kb_tmp_search(
    semantic_query: Optional[str] = None,
    grep_patterns: Optional[List[str]] = None,
    top_k: int = 10,
) -> Dict[str, Any]:
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
    query = str(semantic_query or '').strip() or None
    patterns = _tmp_pattern_list(grep_patterns)
    if not query and not patterns:
        raise ToolExecutionError('at least one of semantic_query or grep_patterns is required')
    top_k = _tmp_clamp_top_k(top_k)
    uploads, skipped = _tmp_collect_uploads()
    docs: list[dict] = []
    for item in uploads:
        try:
            docs.append(_tmp_prepare_doc(item))
        except TimeoutError:
            skipped.append({'target': item['target'], 'reason': 'parse_timeout'})
            LOG.warning(f'[kb_tmp_search] parse timeout target={item["target"]}')
        except Exception as exc:
            reason = str(exc)[:200]
            skipped.append({'target': item['target'], 'reason': reason})
            LOG.warning(f'[kb_tmp_search] skip target={item["target"]} reason={reason}')

    grep_hits = _tmp_grep_hits(docs, patterns) if patterns else []
    semantic_hits: list[dict] = []
    semantic_channel = None
    if query and docs:
        user_id = str(_tmp_agentic_config().get('user_id') or '')
        try:
            semantic_hits, semantic_channel = _tmp_semantic_hits(docs, query, user_id)
        except Exception as exc:
            skipped.append({'target': '*', 'reason': f'semantic_degraded:{exc}'[:200]})
            semantic_channel = 'degraded'

    hits = _tmp_merge_hits(grep_hits, semantic_hits, top_k)
    LOG.info(
        f'[kb_tmp_search] corpus={len(docs)} skipped={len(skipped)} '
        f'total={len(hits)} semantic={semantic_channel} '
        f'skipped_reasons={[item.get("reason") for item in skipped]}'
    )
    return {
        'semantic_query': query,
        'grep_patterns': patterns,
        'corpus': [
            {
                'target': doc['target'],
                'file_id': doc.get('file_id'),
                'kind': doc.get('kind'),
                'parse': doc.get('parse'),
            }
            for doc in docs
        ],
        'skipped': skipped,
        'channels': {
            'grep': bool(patterns),
            'semantic': semantic_channel,
        },
        'total': len(hits),
        'hits': hits,
        'hint': _TMP_HINT,
        'footer': (
            'No matches.'
            if not hits
            else f'Showing {len(hits)} locating hits. Call read_file for surrounding context.'
        ),
    }

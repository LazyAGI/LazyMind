from typing import Any, Dict, List, Literal, Optional

import lazyllm
from lazyllm import AutoModel, LOG
from lazyllm.tools.rag import Reranker, Retriever, TempDocRetriever
from lazyllm.tools.rag.doc_impl import NodeGroupType

from lazymind.chat.engine.tools.infra import handle_tool_errors, tool_success
from lazymind.chat.engine.tools._utils import (
    iter_lookup_ids,
    parse_json_dict,
    parse_number_range,
    truncate_text,
)
from lazymind.chat.engine.tools.algo import DOCUMENT, search_kb, search_temp_files
from lazymind.chat.engine.tools.infra import (
    resolve_index,
)
from lazymind.parsing.engine.transform import GeneralParser
from lazymind.chat.service.utils import (
    annotate_citations,
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
_TEMP_NODE_GROUP_NAME = 'block'
_TEMP_NODE_GROUP_DISPLAY_NAME = 'paragraph slice'
_TEMP_NODE_GROUP_MAX_LENGTH = 2048
_TEMP_NODE_GROUP_SPLIT_BY = '\n'

_kb_retrievers = None
_kb_reranker = None
_kb_image_retriever = None
_tmp_retriever = None
_tmp_reranker = None


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
    if _kb_retrievers is None:
        _kb_retrievers = [
            Retriever(DOCUMENT, **cfg)
            for cfg in _KB_RETRIEVER_CONFIGS
        ]
        _kb_reranker = _build_reranker()
        _kb_image_retriever = Retriever(DOCUMENT, **_KB_IMAGE_RETRIEVER_CONFIG)
    return _kb_retrievers, _kb_reranker, _kb_image_retriever


def _ensure_temp_search_runtime() -> tuple[TempDocRetriever, Optional[Reranker]]:
    global _tmp_retriever, _tmp_reranker
    if _tmp_retriever is None:
        _tmp_retriever = TempDocRetriever(embed=AutoModel(model=EMBED_MAIN))
        _tmp_retriever.create_node_group(
            name=_TEMP_NODE_GROUP_NAME,
            display_name=_TEMP_NODE_GROUP_DISPLAY_NAME,
            group_type=NodeGroupType.CHUNK,
            transform=GeneralParser(
                max_length=_TEMP_NODE_GROUP_MAX_LENGTH,
                split_by=_TEMP_NODE_GROUP_SPLIT_BY,
            ),
        )
        _tmp_retriever.add_subretriever(_TEMP_NODE_GROUP_NAME)
        _tmp_reranker = _build_reranker()
    return _tmp_retriever, _tmp_reranker


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
    if is_image and local_path:
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

    serialized = {
        'uid': getattr(node, 'uid', None) or getattr(node, '_uid', None),
        'number': getattr(node, 'number', metadata.get('index')),
        'group': group,
        'parent': getattr(node, '_parent', None),
        'score': getattr(node, 'relevance_score', None),
        'text': truncate_text(text, _MAX_TEXT_LEN),
        'docid': global_md.get('docid'),
        'kb_id': global_md.get('kb_id'),
        'file_name': compact_metadata.get('file_name') or global_md.get('file_name'),
        'metadata': compact_metadata,
        'global_metadata': global_md,
    }
    if image_markdown:
        serialized['image_markdown'] = image_markdown
        serialized['local_path'] = local_path
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


def _get_citation_state() -> dict:
    agentic_config = lazyllm.globals.get('agentic_config') or {}
    state = agentic_config.get('citation_state')
    return state if isinstance(state, dict) else {}


def _annotate_result_citations(result: Any) -> Any:
    config = _get_citation_state()
    if not config:
        return result
    annotate_citations(result, config)
    return result


class KBToolGroup:
    """Knowledge base search and navigation tools.

    This tool group has the highest retrieval priority. If this tool group is
    visible, use it before Wikipedia, web search, academic search, URL fetching,
    or answering from the model's own knowledge for every factual, definition,
    explanation, or retrieval-style question. Do not skip it because the topic
    looks general, familiar, popular, or likely available on the web. Use other
    retrieval sources only after this knowledge-base search returns no useful
    evidence.
    """
    __public_apis__ = ['kb_search', 'kb_get_parent_node', 'kb_get_window_nodes', 'kb_keyword_search']

    def __key_source__(self) -> Any:
        agentic_config = lazyllm.globals.get('agentic_config') or {}
        return (agentic_config.get('filters') or {}).get('kb_id')

    @handle_tool_errors
    def kb_search(
        self,
        query: str,
        retriever_topk: Optional[int] = None,
        rerank_topk: Optional[int] = None,
        k_max: Optional[int] = None,
        image_topk: Optional[int] = None,
        filters: Optional[Dict[str, Any]] = None,
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
        """
        agentic_config = lazyllm.globals['agentic_config']
        retrievers, reranker, image_retriever = _ensure_kb_search_runtime()

        payload = {
            'query': query.strip(),
            'filters': filters or agentic_config.get('filters') or {},
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
        _annotate_result_citations(serialized)
        return tool_success(
            'kb_search',
            serialized,
        )

    @handle_tool_errors
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
        config = lazyllm.globals['agentic_config']
        doc = DOCUMENT

        for kb_id in iter_lookup_ids(
            (config.get('filters') or {}).get('kb_id'),
            field_name='agentic_config.filters.kb_id',
        ):
            current_nodes = doc.get_nodes(uids=[node_id], kb_id=kb_id)
            current_nodes = current_nodes if isinstance(current_nodes, list) else []
            if not current_nodes:
                continue

            current = _serialize_doc_node_like(current_nodes[0])
            parent_id = current.get('parent')
            if not parent_id:
                result = {
                    'node_id': node_id,
                    'current_node': current,
                    'parent_id': None,
                    'total': 0,
                    'items': [],
                }
                _annotate_result_citations(result)
                return tool_success('kb_get_parent_node', result)

            parent_nodes = doc.get_nodes(uids=[parent_id], kb_id=kb_id)
            parent_nodes = parent_nodes if isinstance(parent_nodes, list) else []
            parent = _serialize_doc_node_like(parent_nodes[0]) if parent_nodes else None
            result = {
                'node_id': node_id,
                'current_node': current,
                'parent_id': parent_id,
                'total': 1 if parent else 0,
                'items': [parent] if parent else [],
            }
            _annotate_result_citations(result)
            return tool_success('kb_get_parent_node', result)

        result = {
            'node_id': node_id,
            'current_node': None,
            'parent_id': None,
            'total': 0,
            'items': [],
        }
        _annotate_result_citations(result)
        return tool_success('kb_get_parent_node', result)

    @handle_tool_errors
    def kb_get_window_nodes(
        self,
        docid: str,
        number: Any,
        group: str = 'block',
    ) -> Dict[str, Any]:
        """Get nodes by number range from a target document.

        Retrieves one or more neighboring nodes around a specific position
        within a known document. This provides surrounding context for a
        node whose docid and number are already known.

        Args:
            docid: Target document id.
            number: Node number or inclusive number range. Pass an int for one
                node, or [start, end] / "start,end" for all nodes in that
                range.
            group: Node group, either block or line.

        Returns:
            A compact dict with node numbers and contents only.
        """
        start, end = parse_number_range(number)

        numbers = set(range(start, end + 1))
        if len(numbers) > _MAX_RESULT_ITEMS:
            raise ValueError(f'number range cannot exceed {_MAX_RESULT_ITEMS} nodes')

        config = lazyllm.globals['agentic_config']
        doc = DOCUMENT

        for kb_id in iter_lookup_ids(
            (config.get('filters') or {}).get('kb_id'),
            field_name='agentic_config.filters.kb_id',
        ):
            nodes = doc.get_nodes(
                doc_ids=[docid],
                group=group,
                kb_id=kb_id,
                offset=max(start - 1, 0),
                limit=len(numbers),
                sort_by_number=True,
            )
            nodes = nodes if isinstance(nodes, list) else []
            nodes = [n for n in nodes if getattr(n, 'number', None) in numbers]
            if not nodes:
                continue
            nodes.sort(key=lambda n: (getattr(n, 'number', 0) or 0, getattr(n, 'uid', '') or ''))
            result = {
                'total': len(nodes),
                'items': [_serialize_doc_node_like(n) for n in nodes],
            }
            _annotate_result_citations(result)
            return tool_success('kb_get_window_nodes', result)

        result = {
            'total': 0,
            'items': [],
        }
        _annotate_result_citations(result)
        return tool_success('kb_get_window_nodes', result)

    @handle_tool_errors
    def kb_keyword_search(
        self,
        keyword: str,
        target: str,
        target_type: Literal['file_name', 'docid'] = 'file_name',
        group: str = 'block',
        phrase: bool = True,
        size: int = 10,
        sort_by: str = 'score',
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
        config = lazyllm.globals['agentic_config']
        index_name = resolve_index(group)
        size = max(1, min(int(size), _MAX_RESULT_ITEMS))
        doc = DOCUMENT
        docid = target if target_type == 'docid' else ''
        file_name = target if target_type == 'file_name' else None
        LOG.info(f'[kb_keyword_search] store={_cfg["segment_store_type"]!r} keyword={keyword!r} docid={docid!r} '
                 f'file_name={file_name!r} group={group!r} phrase={phrase} sort_by={sort_by!r} size={size}')

        for kb_id in iter_lookup_ids(
            (config.get('filters') or {}).get('kb_id'),
            field_name='agentic_config.filters.kb_id',
        ):
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
            _annotate_result_citations(result)
            return tool_success('kb_keyword_search', result)

        return tool_success('kb_keyword_search', {
            'index': index_name, 'group': group, 'docid': docid,
            'file_name': file_name, 'keyword': keyword, 'total': 0, 'items': [],
        })


@handle_tool_errors
def kb_tmp_search(
    query: str,
    retriever_topk: Optional[int] = None,
    rerank_topk: Optional[int] = None,
    k_max: Optional[int] = None,
    files: Optional[List[str]] = None,
) -> Any:
    """Search attached temporary uploaded files with the temporary document retriever.

    Use this tool before answering questions that depend on attached temporary
    uploaded files that require text or document retrieval, such as PDFs, text
    files, office documents, and data files. Scope retrieval to the current
    uploaded files by default, or pass explicit temporary file IDs in ``files``
    when needed.

    Each call handles exactly one search intent. If the user asks about
    multiple unrelated keywords or topics, call this tool separately for each
    keyword/topic. Do not combine unrelated terms into one query with spaces,
    commas, or list-like text.

    Args:
        query: A single natural language query for retrieval.
        retriever_topk: Candidate count used by the temporary retriever before
            reranking. Defaults to 20.
        rerank_topk: Number of nodes the reranker keeps before adaptive-k
            trimming. Defaults to 20.
        k_max: Hard upper bound on the adaptive-k stage. Defaults to 10.
        files: Optional list of temporary file IDs. Defaults to the current
            request's agentic_config.files.
    """
    agentic_config = lazyllm.globals['agentic_config']
    tmp_retriever, reranker = _ensure_temp_search_runtime()
    payload = {
        'query': query.strip(),
        'filters': {},
        'files': files,
        'user_id': agentic_config.get('user_id', ''),
    }
    result = search_temp_files(
        payload,
        tmp_retriever=tmp_retriever,
        reranker=reranker,
        retriever_topk=retriever_topk or _DEFAULT_RETRIEVER_TOPK,
        rerank_topk=rerank_topk or _DEFAULT_RERANK_TOPK,
        k_max=k_max or _DEFAULT_K_MAX,
    )
    serialized = _serialize_kb_result(result)
    _annotate_result_citations(serialized)
    return tool_success(
        'kb_tmp_search',
        serialized,
    )

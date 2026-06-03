from typing import Any, Dict, List, Optional

import lazyllm
from lazyllm import AutoModel
from lazyllm.tools.rag import Reranker, Retriever, TempDocRetriever

from lazymind.chat.engine.tools.infra import handle_tool_errors, tool_success
from lazymind.chat.engine.tools._utils import (
    iter_lookup_ids,
    parse_json_dict,
    parse_number_range,
    truncate_text,
)
from lazymind.chat.engine.tools.algo import search_kb
from lazymind.chat.engine.tools.infra import (
    opensearch_search,
    resolve_index,
    term_filter,
)
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
_DEFAULT_KB_URL = _cfg['agentic_kb_url']
_DEFAULT_KB_NAME = _cfg['agentic_kb_name']
_DEFAULT_KB_DOCUMENT = lazyllm.Document(url=f'{_DEFAULT_KB_URL}/_call', name=_DEFAULT_KB_NAME)


def build_default_retriever_configs() -> List[dict]:
    return [
        {'group_name': 'line', 'embed_keys': [EMBED_MAIN], 'target': 'block'},
        {'group_name': 'block', 'embed_keys': [EMBED_MAIN]},
    ]


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


def _source_to_result(hit: Dict[str, Any]) -> Dict[str, Any]:
    src = hit.get('_source') or {}
    meta = parse_json_dict(src.get('meta'))
    global_meta = parse_json_dict(src.get('global_meta'))
    return {
        'uid': src.get('uid') or hit.get('_id'),
        'number': src.get('number'),
        'group': src.get('group'),
        'parent': src.get('parent'),
        'docid': src.get('doc_id') or global_meta.get('docid'),
        'kb_id': src.get('kb_id') or global_meta.get('kb_id'),
        'score': hit.get('_score'),
        'text': truncate_text(src.get('content'), _MAX_TEXT_LEN),
        'metadata': meta,
        'global_metadata': global_meta,
        'highlight': hit.get('highlight', {}).get('content', []),
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
    __public_apis__ = ['kb_search', 'kb_get_parent_node', 'kb_get_window_nodes', 'kb_keyword_search']
    _document = None
    _retrievers = None
    _reranker = None
    _image_retriever = None

    def __key_source__(self) -> Any:
        agentic_config = lazyllm.globals.get('agentic_config') or {}
        return (agentic_config.get('filters') or {}).get('kb_id')

    def _ensure_search_runtime(self) -> None:
        cls = type(self)
        if cls._document is not None:
            return
        cls._document = _DEFAULT_KB_DOCUMENT
        cls._retrievers = [
            Retriever(cls._document, **cfg)
            for cfg in build_default_retriever_configs()
        ]
        cls._reranker = (
            Reranker('ModuleReranker', model=AutoModel(model='reranker'))
            if _is_reranker_enabled()
            else None
        )
        cls._image_retriever = Retriever(
            cls._document,
            group_name='image',
            embed_keys=[EMBED_IMAGE],
        )

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

        Text retrieval and image retrieval run simultaneously. The final result
        is the concatenation of text nodes and image nodes.

        Args:
            query: Natural language query text used for retrieval.
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
        self._ensure_search_runtime()

        payload = {
            'query': query,
            'filters': filters or agentic_config.get('filters') or {},
            'files': [],
            'user_id': agentic_config.get('user_id', ''),
        }

        result = search_kb(
            payload,
            document=type(self)._document,
            retrievers=type(self)._retrievers,
            tmp_retriever=None,
            reranker=type(self)._reranker,
            image_retriever=type(self)._image_retriever,
            retriever_topk=retriever_topk or 20,
            rerank_topk=rerank_topk or 20,
            k_max=k_max or 10,
            image_topk=image_topk or 3,
        )
        serialized = _serialize_kb_result(result)
        _annotate_result_citations(serialized)
        return tool_success(
            'kb_search',
            serialized,
        )

    @handle_tool_errors
    def kb_get_parent_node(self, node_id: str) -> Dict[str, Any]:
        """Get the parent node of a target node by document node uid.

        Args:
            node_id: Target document node ``uid``.

        Returns:
            The matched parent node, if the current node has a parent and the
            parent can be found.
        """
        if not node_id:
            raise ValueError('node_id is required')

        config = lazyllm.globals['agentic_config']
        doc = _DEFAULT_KB_DOCUMENT

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
        """Get nodes by number in a target document using LazyLLM Document.

        Args:
            docid: Target document id.
            number: Node number or inclusive number range. Pass an int for one
                node, or ``[start, end]`` / ``"start,end"`` for all nodes in that
                range.
            group: Node group, either ``block`` or ``line``.

        Returns:
            A compact dict with node numbers and contents only.
        """
        if not docid:
            raise ValueError('docid is required')
        if number is None:
            raise ValueError('number is required')

        start, end = parse_number_range(number)

        numbers = set(range(start, end + 1))
        if len(numbers) > _MAX_RESULT_ITEMS:
            raise ValueError(f'number range cannot exceed {_MAX_RESULT_ITEMS} nodes')

        config = lazyllm.globals['agentic_config']
        doc = _DEFAULT_KB_DOCUMENT

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
        docid: str,
        group: str = 'block',
        phrase: bool = True,
        size: int = 10,
        sort_by: str = 'score',
    ) -> Dict[str, Any]:
        """Search a keyword inside one target document in OpenSearch.

        Args:
            keyword: Keyword or phrase to search in ``content``.
            docid: Target document id.
            group: Search granularity, either ``block`` or ``line``.
            phrase: Use ``match_phrase`` when true, otherwise ``match``.
            size: Maximum number of hits.
            sort_by: ``score`` for relevance first, or ``number`` for document
                order.

        Returns:
            Matching nodes with content snippets and OpenSearch highlights.
        """
        if not keyword:
            raise ValueError('keyword is required')
        if not docid:
            raise ValueError('docid is required')

        config = lazyllm.globals['agentic_config']
        size = max(1, min(int(size), _MAX_RESULT_ITEMS))
        text_query = {'match_phrase' if phrase else 'match': {'content': keyword}}
        sort = [{'number': {'order': 'asc'}}] if sort_by == 'number' else [
            {'_score': {'order': 'desc'}},
            {'number': {'order': 'asc'}},
        ]
        index_name = resolve_index(group)
        for kb_id in iter_lookup_ids(
            (config.get('filters') or {}).get('kb_id'),
            field_name='agentic_config.filters.kb_id',
        ):
            filters = [term_filter('doc_id', docid)]
            if kb_id:
                filters.insert(0, term_filter('kb_id', kb_id))
            body = {
                'size': size,
                '_source': [
                    'uid', 'doc_id', 'kb_id', 'group', 'content', 'meta',
                    'global_meta', 'type', 'number', 'parent',
                ],
                'query': {
                    'bool': {
                        'filter': filters,
                        'must': [text_query],
                    }
                },
                'sort': sort,
                'highlight': {
                    'fields': {
                        'content': {
                            'fragment_size': 180,
                            'number_of_fragments': 3,
                        }
                    }
                },
            }
            hits = opensearch_search(index_name, body).get('hits', {}).get('hits', [])
            if not hits:
                continue
            result = {
                'index': index_name,
                'group': group,
                'docid': docid,
                'keyword': keyword,
                'total': len(hits),
                'items': [_source_to_result(hit) for hit in hits],
            }
            _annotate_result_citations(result)
            return tool_success('kb_keyword_search', result)

        result = {
            'index': index_name,
            'group': group,
            'docid': docid,
            'keyword': keyword,
            'total': 0,
            'items': [],
        }
        _annotate_result_citations(result)
        return tool_success('kb_keyword_search', result)


class TempKBToolGroup:
    __public_apis__ = ['kb_tmp_search']
    _document = None
    _tmp_retriever = None
    _reranker = None

    def __key_source__(self) -> Any:
        agentic_config = lazyllm.globals.get('agentic_config') or {}
        return agentic_config.get('files')

    def _ensure_search_runtime(self) -> None:
        cls = type(self)
        if cls._tmp_retriever is not None:
            return
        cls._document = _DEFAULT_KB_DOCUMENT
        cls._tmp_retriever = TempDocRetriever(embed=AutoModel(model=EMBED_MAIN))
        cls._tmp_retriever.add_subretriever('block')
        cls._reranker = (
            Reranker('ModuleReranker', model=AutoModel(model='reranker'))
            if _is_reranker_enabled()
            else None
        )

    @handle_tool_errors
    def kb_tmp_search(
        self,
        query: str,
        retriever_topk: Optional[int] = None,
        rerank_topk: Optional[int] = None,
        k_max: Optional[int] = None,
        files: Optional[List[str]] = None,
    ) -> Any:
        """Search temporary uploaded files with the temporary document retriever.

        Args:
            query: Natural language query text used for retrieval.
            retriever_topk: Candidate count used by the temporary retriever.
                Defaults to 20.
            rerank_topk: Number of nodes the reranker keeps before adaptive-k
                trimming. Defaults to 20.
            k_max: Hard upper bound on the adaptive-k stage. Defaults to 10.
            files: Optional list of temporary file IDs. Defaults to the current
                request's ``agentic_config.files``.
        """
        agentic_config = lazyllm.globals['agentic_config']
        self._ensure_search_runtime()
        payload = {
            'query': query,
            'filters': {},
            'files': files,
            'user_id': agentic_config.get('user_id', ''),
        }
        result = search_kb(
            payload,
            document=type(self)._document,
            retrievers=[],
            tmp_retriever=type(self)._tmp_retriever,
            reranker=type(self)._reranker,
            image_retriever=None,
            retriever_topk=retriever_topk or 20,
            rerank_topk=rerank_topk or 20,
            k_max=k_max or 10,
        )
        serialized = _serialize_kb_result(result)
        _annotate_result_citations(serialized)
        return tool_success(
            'kb_tmp_search',
            serialized,
        )

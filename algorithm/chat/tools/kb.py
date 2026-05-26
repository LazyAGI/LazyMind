import json
from typing import Any, Dict, List, Optional

import lazyllm
import requests

from lazyllm import fc_register

from chat.tools._common import handle_tool_errors, tool_success
from chat.tools._utils import parse_json_dict, truncate_text
from chat.pipelines.get_ppl_search import get_ppl_search
from chat.utils.static_file_url import (
    basename_from_path,
    local_path_from_static_file_url,
    static_file_url_from_any,
)
from config import config as _cfg

_MAX_TEXT_LEN = 1200
_MAX_RESULT_ITEMS = 50
_DEFAULT_KB_URL = _cfg['agentic_kb_url']
_DEFAULT_KB_NAME = _cfg['agentic_kb_name']
_DEFAULT_ES_URL = _cfg['opensearch_uri'] or 'https://opensearch:9200'
_DEFAULT_ES_USER = _cfg['opensearch_user']
_DEFAULT_ES_PASSWORD = _cfg['opensearch_password']
_CITATION_REFS_KEY = '_citation_sources'
_CITATION_KEY_MAP_KEY = '_citation_key_map'
_CITATION_NEXT_KEY = '_citation_next_index'
_IMAGE_URL_REGISTRY_KEY = '_image_url_registry'
_CITATION_DOC_KEY_MAP_KEY = '_citation_doc_key_map'
_CITATION_NEXT_DOC_KEY = '_citation_next_doc_index'
_CITATION_DOC_CHUNK_NEXT_KEY = '_citation_next_chunk_index_map'


def _safe_getattr(obj: Any, key: str, default: Any = None) -> Any:
    try:
        return getattr(obj, key)
    except Exception:
        return default


def _parse_number_range(number: Any) -> tuple[int, int]:
    if isinstance(number, str):
        raw = number.strip()
        try:
            number = json.loads(raw)
        except (TypeError, ValueError):
            if ',' in raw:
                number = [part.strip() for part in raw.split(',', 1)]
            elif '-' in raw:
                number = [part.strip() for part in raw.split('-', 1)]
            else:
                number = raw

    if isinstance(number, (list, tuple)):
        if len(number) != 2:
            raise ValueError('number range must be [start, end]')
        start, end = int(number[0]), int(number[1])
    else:
        start = end = int(number)
    if start > end:
        start, end = end, start
    return start, end


def _serialize_doc_node_like(node: Any) -> Dict[str, Any]:
    metadata = _safe_getattr(node, 'metadata', {}) or {}
    if not isinstance(metadata, dict):
        metadata = {}
    global_md = _safe_getattr(node, 'global_metadata', {}) or {}
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
    group = _safe_getattr(node, 'group', None) or _safe_getattr(node, '_group', None)
    text = _safe_getattr(node, 'text', '') or ''
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
        'uid': _safe_getattr(node, 'uid', None) or _safe_getattr(node, '_uid', None),
        'number': _safe_getattr(node, 'number', metadata.get('index')),
        'group': group,
        'parent': _safe_getattr(node, '_parent', None),
        'score': _safe_getattr(node, 'relevance_score', None),
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
        _register_image_url(lazyllm.globals['agentic_config'], text)
    return serialized


def _register_image_url(config: Dict[str, Any], path_or_url: str) -> None:
    signed = static_file_url_from_any(path_or_url)
    if not signed:
        return
    registry = config.setdefault(_IMAGE_URL_REGISTRY_KEY, {})
    registry[signed] = signed
    base = basename_from_path(signed)
    if base:
        registry[base] = signed


def _normalize_es_url(url: Optional[str]) -> str:
    return (url or _DEFAULT_ES_URL).rstrip('/')


def _iter_lookup_kb_ids(kb_id: Any) -> List[Optional[str]]:
    if kb_id is None:
        return [None]
    if isinstance(kb_id, str):
        return [kb_id]
    if isinstance(kb_id, list):
        return kb_id or [None]
    raise TypeError(f'agentic_config.kb_id must be None, str, or list[str], got {type(kb_id).__name__}')


def _build_agentic_document(config: Dict[str, Any]) -> Any:
    return lazyllm.tools.rag.Document(
        url=_DEFAULT_KB_URL,
        name=_resolve_algo_name(config.get('algo_id')),
    )


def _resolve_algo_name(algo_id: Any) -> str:
    """Return the algo name bound to this dataset.

    After the node-group refactor the collection name no longer includes the
    algo name, but lazyllm.Document still needs 'name' (= algo_id) to connect
    to the correct algorithm instance. We read 'algo_id' from agentic_config
    and otherwise fall back to the configured default kb name.
    """
    normalized_algo_id = str(algo_id or '').strip()
    if normalized_algo_id:
        return normalized_algo_id
    return str(_DEFAULT_KB_NAME or '').strip()


def _resolve_index(group: str) -> str:
    # Post node-group refactor: collection name is col_{group}; kb_id is used as
    # a document-level filter inside OpenSearch so multi-KB isolation is preserved.
    group = (group or 'block').strip()
    if group not in ('block', 'line'):
        raise ValueError("group must be either 'block' or 'line'")
    return f'col_{group}'


def _term_filter(field: str, value: Any) -> Dict[str, Any]:
    return {
        'bool': {
            'should': [
                {'term': {field: value}},
                {'term': {f'{field}.keyword': value}},
            ],
            'minimum_should_match': 1,
        }
    }


def _opensearch_search(index: str, body: Dict[str, Any]) -> Dict[str, Any]:
    with requests.sessions.Session() as session:
        session.trust_env = False
        resp = session.post(
            f'{_normalize_es_url(_DEFAULT_ES_URL)}/{index}/_search',
            auth=(_DEFAULT_ES_USER, _DEFAULT_ES_PASSWORD),
            json=body,
            verify=False,
            timeout=30,
        )
    resp.raise_for_status()
    return resp.json()


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


def _citation_key(item: Dict[str, Any]) -> Optional[str]:
    uid = item.get('uid') or item.get('segement_id')
    if uid:
        return f'uid:{uid}'
    docid = item.get('docid') or item.get('document_id')
    group = item.get('group') or item.get('group_name')
    number = item.get('number') or item.get('segment_number')
    if docid and group and number is not None:
        return f'node:{docid}:{group}:{number}'
    text = item.get('text') or item.get('content')
    if docid and text:
        return f'text:{docid}:{str(text)[:80]}'
    return None


def _document_citation_key(item: Dict[str, Any]) -> Optional[str]:
    metadata = item.get('metadata') if isinstance(item.get('metadata'), dict) else {}
    global_md = item.get('global_metadata') if isinstance(item.get('global_metadata'), dict) else {}
    docid = item.get('docid') or item.get('document_id') or global_md.get('docid')
    if not docid:
        return None
    dataset_id = item.get('kb_id') or item.get('dataset_id') or global_md.get('kb_id') or metadata.get('kb_id') or ''
    return f'doc:{dataset_id}:{docid}'


def _split_citation_index(index: Any) -> tuple[int | None, int | None]:
    if isinstance(index, str) and '.' in index:
        doc_index, chunk_index = index.split('.', 1)
        if doc_index.isdigit() and chunk_index.isdigit():
            return int(doc_index), int(chunk_index)
    if isinstance(index, int) and index > 0:
        return index, None
    if isinstance(index, str) and index.isdigit():
        return int(index), None
    return None, None


def _file_name_from_item(item: Dict[str, Any]) -> str:
    metadata = item.get('metadata') if isinstance(item.get('metadata'), dict) else {}
    global_md = item.get('global_metadata') if isinstance(item.get('global_metadata'), dict) else {}
    return (
        item.get('file_name')
        or global_md.get('file_name')
        or metadata.get('file_name')
        or metadata.get('source')
        or 'title_example'
    )


def _source_node_from_item(index: Any, item: Dict[str, Any]) -> Dict[str, Any]:
    metadata = item.get('metadata') if isinstance(item.get('metadata'), dict) else {}
    global_md = item.get('global_metadata') if isinstance(item.get('global_metadata'), dict) else {}
    content = item.get('text') if item.get('text') is not None else item.get('content', '')
    document_index, chunk_index = _split_citation_index(index)
    source = {
        'file_id': '',
        'file_name': _file_name_from_item(item),
        'document_id': item.get('docid') or item.get('document_id') or global_md.get('docid', ''),
        'segement_id': item.get('uid') or item.get('segement_id') or '',
        'dataset_id': item.get('kb_id') or item.get('dataset_id') or global_md.get('kb_id', ''),
        'index': index,
        'display_index': document_index or index,
        'document_index': document_index or index,
        'chunk_index': chunk_index,
        'content': content or '',
        'group_name': item.get('group') or item.get('group_name') or '',
        'segment_number': (
            metadata.get('store_num')
            or metadata.get('lazyllm_store_num')
            or item.get('number')
            or item.get('segment_number')
            or -1
        ),
        'page': metadata.get('page', -1),
        'bbox': metadata.get('bbox', []),
        'metadata': metadata,
    }
    image_url = metadata.get('image_url') or item.get('image_url')
    if isinstance(image_url, str) and image_url.strip():
        source['image_url'] = image_url.strip()
    image_markdown = item.get('image_markdown')
    if isinstance(image_markdown, str) and image_markdown.strip():
        source['image_markdown'] = image_markdown.strip()
    return source


def _register_citation_item(item: Dict[str, Any]) -> Dict[str, Any]:
    text = item.get('text') if item.get('text') is not None else item.get('content')
    if not text:
        return item

    config = lazyllm.globals['agentic_config']
    refs = config.setdefault(_CITATION_REFS_KEY, {})
    key_map = config.setdefault(_CITATION_KEY_MAP_KEY, {})
    doc_key_map = config.setdefault(_CITATION_DOC_KEY_MAP_KEY, {})
    doc_chunk_next_map = config.setdefault(_CITATION_DOC_CHUNK_NEXT_KEY, {})
    key = _citation_key(item)
    if not key:
        return item

    index = key_map.get(key)
    if index is None:
        doc_key = _document_citation_key(item)
        if not doc_key:
            return item
        document_index = doc_key_map.get(doc_key)
        if document_index is None:
            document_index = int(config.get(_CITATION_NEXT_DOC_KEY) or 1)
            config[_CITATION_NEXT_DOC_KEY] = document_index + 1
            doc_key_map[doc_key] = document_index
        chunk_index = int(doc_chunk_next_map.get(doc_key) or 1)
        doc_chunk_next_map[doc_key] = chunk_index + 1
        index = f'{document_index}.{chunk_index}'
        key_map[key] = index
        refs[index] = _source_node_from_item(index, item)
        signed = static_file_url_from_any(str(text))
        if signed:
            _register_image_url(config, signed)

    item['citation_index'] = index
    item['ref'] = f'[[{index}]]'
    return item


def _annotate_citations(result: Any) -> Any:
    if isinstance(result, dict):
        if any(k in result for k in ('text', 'content', 'uid', 'docid', 'document_id')):
            _register_citation_item(result)
        if isinstance(result.get('items'), list):
            result['items'] = [
                _annotate_citations(item) if isinstance(item, dict) else item
                for item in result['items']
            ]
        if isinstance(result.get('current_node'), dict):
            result['current_node'] = _annotate_citations(result['current_node'])
        return result
    if isinstance(result, list):
        return [
            _annotate_citations(item) if isinstance(item, dict) else item
            for item in result
        ]
    return result


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
            if _safe_getattr(item, 'uid', None) is not None or _safe_getattr(item, 'text', None) is not None:
                serialized_items.append(_serialize_doc_node_like(item))
                continue
            serialized_items.append(truncate_text(item, 400))
        return {
            'total': len(result),
            'items': serialized_items,
        }
    return truncate_text(result, 400)


@fc_register('tool', execute_in_sandbox=False)
@handle_tool_errors
def kb_search(
    query: str,
    retriever_configs: Optional[List[Dict[str, Any]]] = None,
    topk: Optional[int] = None,
    k_max: Optional[int] = None,
    filters: Optional[Dict[str, Any]] = None,
    files: Optional[List[str]] = None,
) -> Any:
    """Search the knowledge base or uploaded temporary documents and return retrieval results.

    The pipeline automatically selects one of two retrieval branches based on
    whether `files` is non-empty:

    Branch A — Temporary-file retrieval (when `files` is provided):
        Runs TempDocRetriever over the specified uploaded file IDs. Use this
        branch when the user's question is about files they uploaded in the
        current session rather than the persistent knowledge base.

    Branch B — Knowledge-base retrieval (when `files` is empty or omitted):
        Runs multi-route KB retrieval (dense + sparse, multiple granularities),
        followed by RRF fusion, reranking, adaptive-k selection, and context
        expansion. Use this branch for questions about the knowledge base.

    Both branches share the same reranker, adaptive-k, and context-expansion
    stages, so `topk` and `k_max` apply to both.

    Args:
        query: Natural language query text used for retrieval.
        retriever_configs: Multi-route retriever configuration list. Only
            relevant for Branch B (KB retrieval). If None, falls back to
            `retrieval.retriever_configs` from runtime config.
            Each item is a dict with the following fields:
            - group_name (str, required): retrieval granularity, either
              'line' (sentence-level) or 'block' (paragraph-level).
            - embed_keys (List[str], required): embedding model keys for this
              route. Must match keys declared under `embeddings` in the runtime
              config (e.g. ['embed_1'] for dense, ['embed_2'] for sparse).
            - topk (int, optional): number of candidate nodes fetched by this
              route before fusion. Defaults to 20.
            - target (str, optional): cross-granularity target group applied
              after retrieval, e.g. 'block' when group_name is 'line' to
              promote line hits to their parent blocks.
            Extra keyword arguments accepted by `lazyllm.Retriever` can also
            be included in each dict.
        topk: Final reranker top-k; limits the number of nodes returned after
            reranking. Defaults to 20.
        k_max: Hard upper bound on the adaptive-k stage, which dynamically
            trims results to fit within a token budget. Defaults to 10.
        filters: Metadata filters applied to KB retrievers (Branch B only).
            E.g. {'file_name': 'report.pdf'} restricts retrieval to a single
            file. Ignored when `files` is provided (Branch A).
        files: List of temporary file IDs (uploaded by the user in the current
            session). When non-empty, the pipeline switches to Branch A
            (TempDocRetriever). Defaults to the session's uploaded file list
            from `agentic_config['files']`; pass an explicit list to
            override, or pass [] to force Branch B even when temp files exist.
            Attached images are read from `agentic_config['image_files']` and
            passed through so the search pipeline can rewrite the query.

    Returns:
        Retrieval results returned by `get_ppl_search(...)(payload)`.
    """
    agentic_config = lazyllm.globals['agentic_config']

    if files is None:
        files = agentic_config.get('files') or []

    payload = {
        'query': query,
        'filters': filters or {},
        'files': files,
        'image_files': agentic_config.get('image_files') or [],
        'user_id': agentic_config.get('user_id', ''),
    }
    resolved_kb_id = agentic_config.get('kb_id')
    if resolved_kb_id is not None:
        payload['filters']['kb_id'] = resolved_kb_id
    search_ppl = get_ppl_search(
        url=f'{_DEFAULT_KB_URL},{_DEFAULT_KB_NAME}',
        retriever_configs=retriever_configs,
        topk=topk or 20,
        k_max=k_max or 10,
    )
    return tool_success('kb_search', _annotate_citations(_serialize_kb_result(search_ppl(payload))))


@fc_register('tool', execute_in_sandbox=False)
@handle_tool_errors
def kb_get_parent_node(node_id: str) -> Dict[str, Any]:
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
    doc = _build_agentic_document(config)

    for kb_id in _iter_lookup_kb_ids(config.get('kb_id')):
        current_nodes = doc.get_nodes(uids=[node_id], kb_id=kb_id)
        current_nodes = current_nodes if isinstance(current_nodes, list) else []
        if not current_nodes:
            continue

        current = _serialize_doc_node_like(current_nodes[0])
        parent_id = current.get('parent')
        if not parent_id:
            return tool_success('kb_get_parent_node', _annotate_citations({
                'node_id': node_id,
                'current_node': current,
                'parent_id': None,
                'total': 0,
                'items': [],
            }))

        parent_nodes = doc.get_nodes(uids=[parent_id], kb_id=kb_id)
        parent_nodes = parent_nodes if isinstance(parent_nodes, list) else []
        parent = _serialize_doc_node_like(parent_nodes[0]) if parent_nodes else None
        return tool_success('kb_get_parent_node', _annotate_citations({
            'node_id': node_id,
            'current_node': current,
            'parent_id': parent_id,
            'total': 1 if parent else 0,
            'items': [parent] if parent else [],
        }))

    return tool_success('kb_get_parent_node', {
        'node_id': node_id,
        'current_node': None,
        'parent_id': None,
        'total': 0,
        'items': [],
    })


@fc_register('tool', execute_in_sandbox=False)
@handle_tool_errors
def kb_get_window_nodes(
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

    start, end = _parse_number_range(number)

    numbers = set(range(start, end + 1))
    if len(numbers) > _MAX_RESULT_ITEMS:
        raise ValueError(f'number range cannot exceed {_MAX_RESULT_ITEMS} nodes')

    config = lazyllm.globals['agentic_config']
    doc = _build_agentic_document(config)

    for kb_id in _iter_lookup_kb_ids(config.get('kb_id')):
        nodes = doc.get_nodes(
            doc_ids=[docid],
            group=group,
            kb_id=kb_id,
            offset=max(start - 1, 0),
            limit=len(numbers),
            sort_by_number=True,
        )
        nodes = nodes if isinstance(nodes, list) else []
        nodes = [n for n in nodes if _safe_getattr(n, 'number', None) in numbers]
        if not nodes:
            continue
        nodes.sort(key=lambda n: (_safe_getattr(n, 'number', 0) or 0, _safe_getattr(n, 'uid', '') or ''))
        return tool_success('kb_get_window_nodes', _annotate_citations({
            'total': len(nodes),
            'items': [_serialize_doc_node_like(n) for n in nodes],
        }))

    return tool_success('kb_get_window_nodes', _annotate_citations({
        'total': 0,
        'items': [],
    }))


@fc_register('tool', execute_in_sandbox=False)
@handle_tool_errors
def kb_keyword_search(
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
    index_name = _resolve_index(group)
    for kb_id in _iter_lookup_kb_ids(config.get('kb_id')):
        filters = [_term_filter('doc_id', docid)]
        if kb_id:
            filters.insert(0, _term_filter('kb_id', kb_id))
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
        hits = _opensearch_search(index_name, body).get('hits', {}).get('hits', [])
        if not hits:
            continue
        return tool_success('kb_keyword_search', _annotate_citations({
            'index': index_name,
            'group': group,
            'docid': docid,
            'keyword': keyword,
            'total': len(hits),
            'items': [_source_to_result(hit) for hit in hits],
        }))

    return tool_success('kb_keyword_search', _annotate_citations({
        'index': index_name,
        'group': group,
        'docid': docid,
        'keyword': keyword,
        'total': 0,
        'items': [],
    }))

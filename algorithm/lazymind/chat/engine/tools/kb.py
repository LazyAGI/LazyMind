from typing import Any, Dict, List, Optional

import lazyllm

from lazymind import model_config
from lazymind.chat.engine.tools.infra import handle_tool_errors, tool_success
from lazymind.chat.engine.tools._utils import (
    iter_lookup_ids,
    parse_json_dict,
    parse_number_range,
    safe_getattr,
    truncate_text,
)
from lazymind.chat.engine.tools.algo import ppl_search
from lazymind.chat.engine.tools.infra import kb_document_provider, kb_reranker_factory, kb_retriever_factory
from lazymind.chat.engine.tools.infra import (
    get_image_retriever,
    opensearch_search,
    resolve_index,
    term_filter,
)
from lazymind.chat.service.utils import (
    annotate_citations,
    basename_from_path,
    local_path_from_static_file_url,
    register_image_url,
    static_file_url_from_any,
)
_MAX_TEXT_LEN = 1200
_MAX_RESULT_ITEMS = 50
_DEFAULT_KB_DOCUMENT = kb_document_provider.get_default_document()


def _serialize_doc_node_like(node: Any) -> Dict[str, Any]:
    metadata = safe_getattr(node, 'metadata', {}) or {}
    if not isinstance(metadata, dict):
        metadata = {}
    global_md = safe_getattr(node, 'global_metadata', {}) or {}
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
    group = safe_getattr(node, 'group', None) or safe_getattr(node, '_group', None)
    text = safe_getattr(node, 'text', '') or ''
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
        'uid': safe_getattr(node, 'uid', None) or safe_getattr(node, '_uid', None),
        'number': safe_getattr(node, 'number', metadata.get('index')),
        'group': group,
        'parent': safe_getattr(node, '_parent', None),
        'score': safe_getattr(node, 'relevance_score', None),
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
        register_image_url(lazyllm.globals['agentic_config'], text)
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
            if safe_getattr(item, 'uid', None) is not None or safe_getattr(item, 'text', None) is not None:
                serialized_items.append(_serialize_doc_node_like(item))
                continue
            serialized_items.append(truncate_text(item, 400))
        return {
            'total': len(result),
            'items': serialized_items,
        }
    return truncate_text(result, 400)


class KBToolGroup:
    __public_apis__ = ['search', 'get_parent_node', 'get_window_nodes', 'keyword_search']

    def __init__(self, document: Any = None) -> None:
        self._document = document or _DEFAULT_KB_DOCUMENT
        self._retrievers = kb_retriever_factory.get_kb_retrievers(self._document)
        self._tmp_retriever = kb_retriever_factory.get_tmp_retriever()
        self._reranker = kb_reranker_factory.get_reranker()
        self._image_embed_key = model_config.get_image_embed_key()
        self._image_retriever = (
            get_image_retriever(self._document, self._image_embed_key, 3)
            if self._image_embed_key else None
        )

    @handle_tool_errors
    def search(
        self,
        query: str,
        retriever_topk: Optional[int] = None,
        rerank_topk: Optional[int] = None,
        k_max: Optional[int] = None,
        image_topk: Optional[int] = None,
        filters: Optional[Dict[str, Any]] = None,
        files: Optional[List[str]] = None,
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
            files: Optional list of temporary file IDs to search instead of the
                persistent knowledge base.
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

        resolved_image_topk = image_topk or 3
        if resolved_image_topk != 3 and self._image_embed_key:
            image_retriever = get_image_retriever(
                self._document, self._image_embed_key, resolved_image_topk
            )
        else:
            image_retriever = self._image_retriever

        result = ppl_search(
            payload,
            document=self._document,
            retrievers=self._retrievers,
            tmp_retriever=self._tmp_retriever,
            reranker=self._reranker,
            image_retriever=image_retriever,
            retriever_topk=retriever_topk or 20,
            rerank_topk=rerank_topk or 20,
            k_max=k_max or 10,
        )
        return tool_success(
            'kb_search',
            annotate_citations(_serialize_kb_result(result), lazyllm.globals['agentic_config']),
        )

    @handle_tool_errors
    def get_parent_node(self, node_id: str) -> Dict[str, Any]:
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
        doc = kb_document_provider.build_agentic_document(config)

        for kb_id in iter_lookup_ids(config.get('kb_id'), field_name='agentic_config.kb_id'):
            current_nodes = doc.get_nodes(uids=[node_id], kb_id=kb_id)
            current_nodes = current_nodes if isinstance(current_nodes, list) else []
            if not current_nodes:
                continue

            current = _serialize_doc_node_like(current_nodes[0])
            parent_id = current.get('parent')
            if not parent_id:
                return tool_success('kb_get_parent_node', annotate_citations({
                    'node_id': node_id,
                    'current_node': current,
                    'parent_id': None,
                    'total': 0,
                    'items': [],
                }, lazyllm.globals['agentic_config']))

            parent_nodes = doc.get_nodes(uids=[parent_id], kb_id=kb_id)
            parent_nodes = parent_nodes if isinstance(parent_nodes, list) else []
            parent = _serialize_doc_node_like(parent_nodes[0]) if parent_nodes else None
            return tool_success('kb_get_parent_node', annotate_citations({
                'node_id': node_id,
                'current_node': current,
                'parent_id': parent_id,
                'total': 1 if parent else 0,
                'items': [parent] if parent else [],
            }, lazyllm.globals['agentic_config']))

        return tool_success('kb_get_parent_node', {
            'node_id': node_id,
            'current_node': None,
            'parent_id': None,
            'total': 0,
            'items': [],
        })

    @handle_tool_errors
    def get_window_nodes(
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
        doc = kb_document_provider.build_agentic_document(config)

        for kb_id in iter_lookup_ids(config.get('kb_id'), field_name='agentic_config.kb_id'):
            nodes = doc.get_nodes(
                doc_ids=[docid],
                group=group,
                kb_id=kb_id,
                offset=max(start - 1, 0),
                limit=len(numbers),
                sort_by_number=True,
            )
            nodes = nodes if isinstance(nodes, list) else []
            nodes = [n for n in nodes if safe_getattr(n, 'number', None) in numbers]
            if not nodes:
                continue
            nodes.sort(key=lambda n: (safe_getattr(n, 'number', 0) or 0, safe_getattr(n, 'uid', '') or ''))
            return tool_success('kb_get_window_nodes', annotate_citations({
                'total': len(nodes),
                'items': [_serialize_doc_node_like(n) for n in nodes],
            }, lazyllm.globals['agentic_config']))

        return tool_success('kb_get_window_nodes', annotate_citations({
            'total': 0,
            'items': [],
        }, lazyllm.globals['agentic_config']))

    @handle_tool_errors
    def keyword_search(
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
        for kb_id in iter_lookup_ids(config.get('kb_id'), field_name='agentic_config.kb_id'):
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
            return tool_success('kb_keyword_search', annotate_citations({
                'index': index_name,
                'group': group,
                'docid': docid,
                'keyword': keyword,
                'total': len(hits),
                'items': [_source_to_result(hit) for hit in hits],
            }, lazyllm.globals['agentic_config']))

        return tool_success('kb_keyword_search', annotate_citations({
            'index': index_name,
            'group': group,
            'docid': docid,
            'keyword': keyword,
            'total': 0,
            'items': [],
        }, lazyllm.globals['agentic_config']))

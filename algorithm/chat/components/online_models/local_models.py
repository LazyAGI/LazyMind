from typing import Any, Dict, List, Optional, Tuple, Union
import re
import requests

from lazyllm import LOG
from lazyllm.tools.rag.doc_node import DocNode, MetadataMode
from lazyllm.module.llms.onlinemodule.base import LazyLLMOnlineEmbedModuleBase, LazyLLMOnlineRerankModuleBase


class BgeM3Embed(LazyLLMOnlineEmbedModuleBase):
    NO_PROXY = True

    def __init__(self, embed_url: str = '', embed_model_name: str = 'custom', api_key: str = None,
                 skip_auth: bool = True, batch_size: int = 16, **kw):
        super().__init__(embed_url=embed_url, api_key='' if skip_auth else (api_key or ''),
                         embed_model_name=embed_model_name,
                         skip_auth=skip_auth, batch_size=batch_size, **kw)

    def _set_embed_url(self):
        pass

    def _encapsulated_data(self, input: Union[List, str], **kwargs):
        model = kwargs.get('model', self._embed_model_name)
        extras = {k: v for k, v in kwargs.items() if k not in ('model',)}
        if isinstance(input, str):
            json_data: Dict = {'inputs': input}
            if model:
                json_data['model'] = model
            json_data.update(extras)
            return json_data
        text_batch = [input[i: i + self._batch_size] for i in range(0, len(input), self._batch_size)]
        out = []
        for texts in text_batch:
            item: Dict = {'inputs': texts}
            if model:
                item['model'] = model
            item.update(extras)
            out.append(item)
        return out

    def _parse_response(self, response: Union[Dict, List], input: Union[List, str]
                        ) -> Union[List[float], List[List[float]], Dict]:
        if isinstance(response, dict):
            if 'data' in response:
                return super()._parse_response(response, input)
            return response
        if isinstance(response, list):
            if not response:
                raise RuntimeError('empty embedding response')
            if isinstance(input, str):
                first = response[0]
                return response if isinstance(first, float) else first
            return response
        raise RuntimeError(f'unexpected embedding response type: {type(response)!r}')


class Qwen3Rerank(LazyLLMOnlineRerankModuleBase):
    _PROMPT_PREFIX = (
        '<|im_start|>system\n'
        'Judge whether the Document meets the requirements based on the Query and the Instruct provided. '
        'Note that the answer can only be "yes" or "no".'
        '<|im_end|>\n<|im_start|>user\n'
    )
    _PROMPT_SUFFIX = '<|im_end|>\n<|im_start|>assistant\n<think>\n\n</think>\n\n'

    _QUERY_TEMPLATE = '{prefix}<Instruct>: {instruction}\n<Query>: {query}\n'
    _DOCUMENT_TEMPLATE = '<Document>: {doc}{suffix}'
    _LOCAL_ONLY_PAYLOAD_KEYS = frozenset((
        'query', 'documents', 'nodes', 'template',
        'top_n', 'top_k', 'topk', 'timeout', 'request_timeout',
    ))

    _LOCAL_TRUNCATE_MAX_CHARS = 16384
    _DEFAULT_TASK_DESCRIPTION = 'Given a web search query, retrieve relevant passages that answer the query'

    def __init__(
        self,
        embed_model_name: str = 'Qwen3Reranker',
        embed_url: Optional[str] = None,
        api_key: str = 'api_key',
        skip_auth: bool = True,
        batch_size: int = 64,
        truncate_text: bool = True,
        output_format: Optional[str] = None,
        join: Union[bool, str] = False,
        task_description: Optional[str] = None,
        request_timeout: Optional[float] = None,
        timeout: Optional[float] = None,
        **kwargs: Any,
    ) -> None:
        super().__init__(
            embed_url=embed_url,
            api_key='' if skip_auth else (api_key or ''),
            embed_model_name=embed_model_name,
            skip_auth=skip_auth,
        )
        if not embed_url:
            raise ValueError('`url` is required, pass the remote reranking service address.')

        self._url = embed_url
        self._batch_size = max(1, int(batch_size))
        self._truncate_text = bool(truncate_text)
        self._timeout = request_timeout if request_timeout is not None else timeout

        self._headers: Dict[str, str] = self._build_headers()
        self._session = requests.Session()
        self._task_description = task_description or self._DEFAULT_TASK_DESCRIPTION

    def _build_headers(self) -> Dict[str, str]:
        return {
            'Content-Type': 'application/json',
            'Authorization': f'Bearer {self._api_key}',
        }

    def _extract_top_k(self, total: int, **kwargs: Any) -> int:
        top_k = kwargs.get('top_n', kwargs.get('top_k', kwargs.get('topk', total)))
        try:
            top_k = int(top_k)
        except Exception:
            top_k = total
        return max(0, min(top_k, total))

    def _score_texts(self, query: str, texts: List[str], **kwargs: Any) -> List[float]:
        if not texts:
            return []

        all_scores: List[float] = [0.0] * len(texts)
        for start in range(0, len(texts), self._batch_size):
            batch_texts = texts[start:start + self._batch_size]
            payload = self._encapsulated_data(query, documents=batch_texts, **kwargs)

            try:
                resp = self._session.post(
                    self._url, json=payload, headers=self._headers, timeout=self._timeout
                )
                resp.raise_for_status()
                parsed = self._parse_response(resp.json())
            except requests.RequestException as exc:
                LOG.error('HTTP request for reranking failed (this batch will be scored as 0): %s', exc)
                parsed = []

            for idx, score in parsed:
                if 0 <= idx < len(batch_texts):
                    all_scores[start + idx] = score

        return all_scores

    def _get_format_content(self, nodes: List[DocNode], **kwargs: Any) -> List[str]:
        template: Optional[str] = dict(kwargs).pop('template', None)
        if not template:
            return [n.get_text(metadata_mode=MetadataMode.EMBED) for n in nodes]

        placeholders = re.findall(r'{(\w+)}', template)

        formatted: List[str] = []
        for node in nodes:
            values = {
                key: (
                    node.text
                    if key == 'text'
                    else node.metadata.get(key, '') or node.global_metadata.get(key, '')
                )
                for key in placeholders
            }
            try:
                formatted.append(template.format(**values))
            except Exception as exc:
                LOG.warning('Template formatting failed; fallback to raw text: %s', exc)
                formatted.append(node.get_text(metadata_mode=MetadataMode.EMBED))
        return formatted

    def _build_instruct(self, task_description: str, query: str) -> str:
        return self._QUERY_TEMPLATE.format(
            prefix=self._PROMPT_PREFIX, instruction=task_description, query=query
        )

    def _build_documents(self, texts: List[str]) -> List[str]:
        docs: List[str] = []

        def _truncate_if_needed(s: str) -> str:
            if not self._truncate_text:
                return s
            if len(s) <= self._LOCAL_TRUNCATE_MAX_CHARS:
                return s
            return s[: self._LOCAL_TRUNCATE_MAX_CHARS]

        for t in texts:
            t_norm = _truncate_if_needed(t or '')
            docs.append(self._DOCUMENT_TEMPLATE.format(doc=t_norm, suffix=self._PROMPT_SUFFIX))
        return docs

    def _encapsulated_data(self, query: str, **kwargs: Any) -> Dict[str, Any]:
        documents = kwargs.pop('documents', [])
        payload: Dict[str, Any] = {
            'query': self._build_instruct(self._task_description, query),
            'documents': self._build_documents(documents),
        }
        for k, v in kwargs.items():
            if k not in self._LOCAL_ONLY_PAYLOAD_KEYS:
                payload[k] = v
        return payload

    def _warn_on_empty_query(self, query: str) -> str:
        normalized_query = '' if query is None else str(query)
        if not normalized_query:
            LOG.warning('Qwen3Rerank received an empty query. Check caller input and reranker binding.')
        return normalized_query

    def _parse_response(self, response: Any, input=None) -> List[Tuple[int, float]]:
        """Return [(index, relevance_score), ...], compatible with ModuleReranker protocol."""
        if not isinstance(response, dict) or 'results' not in response:
            LOG.warning("response missing 'results' field: %r", response)
            return []

        results = response.get('results', [])
        try:
            return [(item['index'], float(item['relevance_score'])) for item in results]
        except Exception as exc:
            LOG.error('Failed to parse response: %s; response=%r', exc, response)
            return []

    def _rerank_nodes(self, nodes: List[DocNode], query: str, **kwargs: Any) -> List[DocNode]:
        if not nodes:
            return []

        query = self._warn_on_empty_query(query)
        texts = self._get_format_content(nodes, **kwargs)
        top_k = self._extract_top_k(len(texts), **kwargs)
        all_scores = self._score_texts(query, texts, **kwargs)
        scored_nodes: List[DocNode] = [nodes[i].with_score(all_scores[i]) for i in range(len(nodes))]
        scored_nodes.sort(key=lambda n: n.relevance_score, reverse=True)
        results = scored_nodes[:top_k]
        LOG.debug(f'Rerank use `{self._embed_model_name}` and get nodes: {results}')
        return results

    def _rerank_documents(
        self, query: str, documents: List[str], **kwargs: Any
    ) -> List[Tuple[int, float]]:
        if not documents:
            return []

        query = self._warn_on_empty_query(query)
        texts = [doc if isinstance(doc, str) else str(doc or '') for doc in documents]
        top_k = self._extract_top_k(len(texts), **kwargs)
        all_scores = self._score_texts(query, texts, **kwargs)
        scored_indices = [(index, all_scores[index]) for index in range(len(texts))]
        scored_indices.sort(key=lambda item: item[1], reverse=True)
        results = scored_indices[:top_k]
        LOG.debug(f'Rerank use `{self._embed_model_name}` and get indices: {results}')
        return results

    def forward(self, *args: Any, **kwargs: Any) -> Union[List[DocNode], List[Tuple[int, float]]]:
        if not args:
            if 'nodes' in kwargs:
                nodes = kwargs.pop('nodes')
                query = kwargs.pop('query', '')
                return self._rerank_nodes(nodes, query, **kwargs)
            if 'documents' in kwargs:
                documents = kwargs.pop('documents')
                query = kwargs.pop('query', '')
                return self._rerank_documents(query, documents, **kwargs)
            raise TypeError('forward() missing required arguments')

        if len(args) == 1:
            first = args[0]
            if 'nodes' in kwargs:
                nodes = kwargs.pop('nodes')
                return self._rerank_nodes(nodes, first, **kwargs)
            if 'documents' in kwargs:
                documents = kwargs.pop('documents')
                return self._rerank_documents(first, documents, **kwargs)

        if len(args) == 2:
            first, second = args
            if isinstance(first, list) and first and isinstance(first[0], DocNode):
                return self._rerank_nodes(first, second, **kwargs)
            return self._rerank_documents(first, second, **kwargs)

        raise TypeError(f'Unsupported forward arguments: {args!r}')

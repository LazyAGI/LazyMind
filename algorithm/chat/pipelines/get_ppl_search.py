from typing import List, Any, NamedTuple, Optional
import lazyllm
from lazyllm import AutoModel, Retriever, pipeline, parallel, bind, ifs, Document
from lazyllm.tools.rag import Reranker
from lazyllm.tools.rag.rank_fusion.reciprocal_rank_fusion import RRFFusion
from lazyllm.tools.rag import TempDocRetriever

import chat.components.online_models.local_models  # noqa: F401 — registers BgeM3Embed / Qwen3Rerank into lazyllm.online

from chat.config import DEFAULT_TMP_BLOCK_TOPK
from chat.components.process import AdaptiveKComponent, ContextExpansionComponent
from chat.utils.load_config import (
    get_dynamic_role_slot_map,
    get_image_embed_key,
    get_text_embed_keys,
)
from vocab.vocab_manager import get_vocab_manager
from config import config as _cfg

EMBED_MAIN = 'embed_main'


def _build_default_retriever_configs(topk: int = 20) -> List[dict]:
    embed_keys = get_text_embed_keys() or [EMBED_MAIN]
    return [
        {'group_name': 'line', 'embed_keys': embed_keys, 'topk': topk, 'target': 'block'},
        {'group_name': 'block', 'embed_keys': embed_keys, 'topk': topk},
    ]


class SearchRetrievalParts(NamedTuple):
    kb_retrievers: List[Retriever]
    tmp_retriever_pipeline: object
    image_retriever: Optional[Retriever]


def get_remote_document(url: str) -> Document:
    url = url.split(',')
    if len(url) == 1:
        url, name = url[0], '__default__'
    else:
        url, name = url[0], url[1]
    return Document(url=f'{url}/_call', name=name)


def get_retriever(
    url: str,
    retriever_configs: List[dict] = None,
    *,
    tmp_block_topk: int = DEFAULT_TMP_BLOCK_TOPK,
) -> SearchRetrievalParts:
    retriever_configs = retriever_configs or _build_default_retriever_configs()
    document = get_remote_document(url)
    kb_retrievers = [Retriever(document, **cfg) for cfg in retriever_configs]

    image_retriever: Optional[Retriever] = None
    image_embed_key = get_image_embed_key()
    if image_embed_key:
        image_retriever = Retriever(
            document,
            group_name='image',
            embed_keys=[image_embed_key],
            topk=int(_cfg['image_topk']),
        )

    ref_docs_retriever = TempDocRetriever(embed=AutoModel(model=EMBED_MAIN))
    ref_docs_retriever.add_subretriever('block', topk=tmp_block_topk)
    with pipeline() as tmp_ppl:
        tmp_ppl.parse_input = lambda input, **kwargs: kwargs.get('files', [])
        tmp_ppl.tmp_retriever = ref_docs_retriever | bind(query=tmp_ppl.input)

    return SearchRetrievalParts(
        kb_retrievers=kb_retrievers,
        tmp_retriever_pipeline=tmp_ppl,
        image_retriever=image_retriever,
    )


def parse_query(query_params: dict) -> str:
    return get_vocab_manager(query_params['user_id'])(query_params['query'])


def has_files(x: dict) -> bool:
    return bool(x.get('files'))


def merge_rank_results(*args):
    return tuple(rank_list for rank_list in args if rank_list)


def merge_text_image_nodes(text_nodes, image_nodes):
    return list(text_nodes or []) + list(image_nodes or [])


def _adaptive_get_token_len(n: Any) -> int:
    txt = getattr(n, 'text', '') or ''
    return max(1, len(txt) // 4)


def _rerank(nodes, query: str, topk: int):
    role_slots = get_dynamic_role_slot_map()
    cfg = lazyllm.globals.config['dynamic_model_configs']
    role_cfg = cfg.get('reranker') if isinstance(cfg, dict) else None

    if 'reranker' not in role_slots or (isinstance(role_cfg, dict) and role_cfg.get(role_slots['reranker'])):
        return Reranker(
            'ModuleReranker', model=AutoModel(model='reranker'), topk=topk,
        )(nodes, query=query)

    for node in nodes or []:
        if getattr(node, 'relevance_score', None) is None:
            node.relevance_score = getattr(node, 'score', None) or getattr(node, 'similarity_score', None) or 0.0
    return nodes


def _build_text_branch(retrievers, tmp_retriever, document, topk: int, k_max: int):
    with pipeline() as text_branch:
        text_branch.parse_input = parse_query
        text_branch.divert = ifs(
            has_files | bind(x=text_branch.input),
            tpath=tmp_retriever | bind(files=text_branch.input['files']),
            fpath=parallel(
                *[(retriever | bind(filters=text_branch.input['filters']))
                  for retriever in retrievers]
            ),
        )
        text_branch.merge_results = merge_rank_results
        text_branch.join = RRFFusion(top_k=50)
        text_branch.reranker = _rerank | bind(
            query=text_branch.input['query'], topk=topk,
        )
        text_branch.adaptive_k = AdaptiveKComponent(
            bias=2, k_max=k_max, gap_tau=0.2,
            get_token_len=_adaptive_get_token_len,
            max_tokens=2048,
        )
        text_branch.ctx_expand = ContextExpansionComponent(
            document=document,
            token_budget=1500,
            score_decay=0.97,
            max_seeds=1,
        )
    return text_branch


def _build_image_branch(image_retriever):
    with pipeline() as image_branch:
        image_branch.parse_input = lambda x: x['query']
        image_branch.body = ifs(
            has_files | bind(x=image_branch.input),
            tpath=lambda *_: [],
            fpath=image_retriever | bind(filters=image_branch.input['filters']),
        )
    return image_branch


def get_ppl_search(url: str, retriever_configs: List[dict] = None, topk=20, k_max=10):
    retrieval = get_retriever(url, retriever_configs)
    retrievers = retrieval.kb_retrievers
    tmp_retriever = retrieval.tmp_retriever_pipeline
    image_retriever = retrieval.image_retriever
    document = get_remote_document(url)

    with lazyllm.save_pipeline_result():
        text_branch = _build_text_branch(retrievers, tmp_retriever, document, topk, k_max)

        if image_retriever is None:
            with pipeline() as text_search_ppl:
                text_search_ppl.search = text_branch
            return text_search_ppl

        image_branch = _build_image_branch(image_retriever)

        with pipeline() as search_ppl:
            search_ppl.par = parallel(text_branch, image_branch)
            search_ppl.merge = merge_text_image_nodes

    return search_ppl

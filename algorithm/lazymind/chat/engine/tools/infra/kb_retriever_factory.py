from typing import Dict, List, Tuple

from lazyllm import AutoModel, Document
from lazyllm.tools.rag import Retriever, TempDocRetriever

from lazymind.model_config import get_config_path, get_text_embed_keys

EMBED_MAIN = 'embed_main'
_KB_RETRIEVER_CACHE: Dict[int, List[Retriever]] = {}
_IMAGE_RETRIEVER_CACHE: Dict[Tuple[int, str, int], Retriever] = {}
_TMP_RETRIEVER_CACHE: Dict[str, TempDocRetriever] = {}


def build_default_retriever_configs() -> List[dict]:
    embed_keys = get_text_embed_keys() or [EMBED_MAIN]
    return [
        {'group_name': 'line', 'embed_keys': embed_keys, 'target': 'block'},
        {'group_name': 'block', 'embed_keys': embed_keys},
    ]


def get_kb_retrievers(document: Document) -> List[Retriever]:
    cache_key = id(document)
    retrievers = _KB_RETRIEVER_CACHE.get(cache_key)
    if retrievers is None:
        retrievers = [Retriever(document, **cfg) for cfg in build_default_retriever_configs()]
        _KB_RETRIEVER_CACHE[cache_key] = retrievers
    return retrievers


def get_image_retriever(document: Document, image_embed_key: str, image_topk: int) -> Retriever:
    cache_key = (id(document), image_embed_key, image_topk)
    image_retriever = _IMAGE_RETRIEVER_CACHE.get(cache_key)
    if image_retriever is None:
        image_retriever = Retriever(
            document,
            group_name='image',
            embed_keys=[image_embed_key],
            topk=image_topk,
        )
        _IMAGE_RETRIEVER_CACHE[cache_key] = image_retriever
    return image_retriever


def get_tmp_retriever() -> TempDocRetriever:
    cache_key = get_config_path()
    tmp_retriever = _TMP_RETRIEVER_CACHE.get(cache_key)
    if tmp_retriever is None:
        tmp_retriever = TempDocRetriever(embed=AutoModel(model=EMBED_MAIN, config=cache_key))
        tmp_retriever.add_subretriever('block')
        _TMP_RETRIEVER_CACHE[cache_key] = tmp_retriever
    return tmp_retriever

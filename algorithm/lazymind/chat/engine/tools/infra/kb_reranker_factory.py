from typing import Dict, Optional

from lazyllm import AutoModel
from lazyllm.tools.rag import Reranker

from lazymind.model_config import get_enabled_role_config_path

_RERANKER_CACHE: Dict[str, Optional[Reranker]] = {}


def build_reranker() -> Optional[Reranker]:
    config_path = get_enabled_role_config_path('reranker')
    if config_path is None:
        return None
    return Reranker(
        'ModuleReranker',
        model=AutoModel(model='reranker', config=config_path),
    )


def get_reranker() -> Optional[Reranker]:
    config_path = get_enabled_role_config_path('reranker')
    cache_key = config_path or '__disabled__'
    if cache_key not in _RERANKER_CACHE:
        _RERANKER_CACHE[cache_key] = build_reranker() if config_path is not None else None
    return _RERANKER_CACHE[cache_key]

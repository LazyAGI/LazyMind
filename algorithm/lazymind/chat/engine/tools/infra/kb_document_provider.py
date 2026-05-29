from typing import Any, Dict

import lazyllm
from lazyllm import Document

from lazymind.config import config as _cfg

_DEFAULT_KB_URL = _cfg['agentic_kb_url']
_DEFAULT_KB_NAME = _cfg['agentic_kb_name']


def get_remote_document(base_url: str, name: str = '__default__') -> Document:
    return Document(url=f'{base_url}/_call', name=name)


def get_default_document() -> Document:
    return get_remote_document(_DEFAULT_KB_URL, _DEFAULT_KB_NAME)


def _resolve_algo_name(algo_id: Any) -> str:
    normalized_algo_id = str(algo_id or '').strip()
    if normalized_algo_id:
        return normalized_algo_id
    return str(_DEFAULT_KB_NAME or '').strip()


def build_agentic_document(config: Dict[str, Any]) -> Any:
    return lazyllm.tools.rag.Document(
        url=_DEFAULT_KB_URL,
        name=_resolve_algo_name(config.get('algo_id')),
    )

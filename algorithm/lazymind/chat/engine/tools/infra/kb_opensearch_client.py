from typing import Any, Dict, Optional

import requests

from lazymind.config import config as _cfg

_DEFAULT_ES_URL = _cfg['opensearch_uri'] or 'https://opensearch:9200'
_DEFAULT_ES_USER = _cfg['opensearch_user']
_DEFAULT_ES_PASSWORD = _cfg['opensearch_password'] or 'LazyRAG_OpenSearch123!'


def _normalize_es_url(url: Optional[str]) -> str:
    return (url or _DEFAULT_ES_URL).rstrip('/')


def resolve_index(group: str) -> str:
    group = (group or 'block').strip()
    if group not in ('block', 'line'):
        raise ValueError("group must be either 'block' or 'line'")
    return f'col_{group}'


def term_filter(field: str, value: Any) -> Dict[str, Any]:
    return {
        'bool': {
            'should': [
                {'term': {field: value}},
                {'term': {f'{field}.keyword': value}},
            ],
            'minimum_should_match': 1,
        }
    }


def opensearch_search(index: str, body: Dict[str, Any], es_conf: Optional[Dict[str, Any]] = None) -> Dict[str, Any]:
    es_conf = es_conf or {}
    es_url = _normalize_es_url(str(es_conf.get('es_url') or _DEFAULT_ES_URL))
    es_user = es_conf.get('es_user', _DEFAULT_ES_USER)
    es_password = es_conf.get('es_password') or _DEFAULT_ES_PASSWORD
    with requests.sessions.Session() as session:
        session.trust_env = False
        resp = session.post(
            f'{es_url}/{index}/_search',
            auth=(es_user, es_password),
            json=body,
            verify=False,
            timeout=30,
        )
    resp.raise_for_status()
    return resp.json()

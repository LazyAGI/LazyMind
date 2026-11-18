from __future__ import annotations

from typing import Any, Dict, List, Optional

import lazyllm
from lazyllm.common import ApiKeyHeaderStrategy
from lazyllm.tools.agent.toolsManager import fc_register
from lazyllm.tools.tools.search.base import SearchBase, _make_result

# tool_config.go writes this sentinel when Keenable is selected without an API key.
_PUBLIC_TOKEN = 'public'


class KeenableSearch(SearchBase):
    """Open-web search through Keenable. An API key is optional.

    Without a key, or when the stored token is the ``public`` sentinel, this
    uses ``POST /v1/search/public``. A real key switches to ``POST /v1/search``.
    The tool is inactive until the user selects Keenable in External Services,
    so it does not replace Google, Bing, Bocha or Tavily by default.
    """

    def __init__(
        self,
        api_key: Optional[str] = None,
        base_url: str = 'https://api.keenable.ai',
        timeout: int = 30,
        source_name: str = 'keenable',
    ):
        super().__init__(
            source_name=source_name,
            api_key=api_key or '',
            auth_strategy=ApiKeyHeaderStrategy('X-API-Key'),
            dynamic_auth=(api_key is None),
            skip_auth=True,
        )
        self._base_url = base_url.rstrip('/')
        self._timeout = timeout
        # SearchBase(skip_auth=True) would otherwise look always-on. Only treat
        # the provider as active when the user has selected it.
        self.__key_source__ = self._is_configured

    def _is_configured(self) -> bool:
        mapping = lazyllm.globals.config['dynamic_tool_auth'] or {}
        if not isinstance(mapping, dict):
            return False
        return self.source_name in mapping

    def _token(self) -> str:
        token = ''
        getter = getattr(self, '_get_token', None)
        if callable(getter):
            token = getter() or ''
        token = str(token).strip()
        if token.lower() == _PUBLIC_TOKEN:
            return ''
        return token

    @fc_register(host_file='NONE')
    def search(self, query: str, max_results: int = 10) -> List[Dict[str, Any]]:
        max_results = min(50, max_results)
        token = self._token()
        path = '/v1/search' if token else '/v1/search/public'
        resp = self._request(
            'POST',
            f'{self._base_url}{path}',
            json={'query': query, 'max_results': max_results},
            headers={'X-Keenable-Title': 'LazyMind'},
            timeout=self._timeout,
        )
        data = resp.json()
        out: List[Dict[str, Any]] = []
        for it in data.get('results') or []:
            url = it.get('url') or ''
            if not url:
                continue
            out.append(_make_result(
                title=it.get('title') or '',
                url=url,
                snippet=it.get('snippet') or '',
                source=self.source_name,
            ))
        return out[:max_results]

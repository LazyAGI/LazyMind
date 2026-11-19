from __future__ import annotations

import lazyllm
from lazyllm.tools.tools.search import KeenableSearch as _LazyLLMKeenableSearch

# tool_config.go writes this sentinel when Keenable is selected without an API key.
_PUBLIC_TOKEN = 'public'


class KeenableSearch(_LazyLLMKeenableSearch):
    """LazyLLM's KeenableSearch with LazyMind's per-request selection.

    The request and result parsing come from LazyLLM. This subclass only:
    - keeps the tool inactive until the user selects Keenable in External
      Services, so it does not replace Google, Bing, Bocha or Tavily by default;
    - reads the user's key from ``dynamic_tool_auth`` per request, and treats the
      ``public`` sentinel as no key (free ``/v1/search/public`` endpoint, no
      ``X-API-Key`` header).
    A key passed to the constructor is used as-is, like upstream.
    """

    def __init__(self, api_key: str | None = None, **kwargs):
        super().__init__(api_key=api_key, **kwargs)
        self._use_request_key = api_key is None

    def __key_source__(self) -> bool:
        # Defined on the class so InstanceToolGroup, which reads
        # ``type(instance).__key_source__``, sees the same check as the tool list.
        return self.source_name in self._request_auth()

    @staticmethod
    def _request_auth() -> dict:
        mapping = lazyllm.globals.config['dynamic_tool_auth'] or {}
        return mapping if isinstance(mapping, dict) else {}

    def _get_token(self) -> str:
        if self._use_request_key:
            token = self._request_auth().get(self.source_name) or ''
        else:
            token = super()._get_token() or ''
        token = str(token).strip()
        if token.lower() == _PUBLIC_TOKEN:
            return ''
        return token

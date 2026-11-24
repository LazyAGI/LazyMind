from __future__ import annotations

import json
import re
import threading
import time
from email.utils import parsedate_to_datetime

import lazyllm
from lazyllm.tools.agent.toolsManager import fc_register
from lazyllm.tools.tools.search import KeenableSearch as _LazyLLMKeenableSearch
from lazyllm.tools.tools.search import WikipediaSearch

# Core supplies this sentinel for the default or explicitly selected keyless service.
_PUBLIC_TOKEN = 'public'
SEARCH_SETTINGS_URL = '/settings?section=knowledge&tool=web-search'
_PUBLIC_LOCK = threading.Lock()
_PUBLIC_RETRY_AT = 0.0


def request_search_configuration(reason: str) -> None:
    """Use the existing capability card and workflow retry checkpoint for required sources."""
    from lazyllm.tools.agent import ToolExecutionError
    missing = {
        'id': 'web_search', 'label': '网络搜索', 'available': False,
        'settings_url': SEARCH_SETTINGS_URL,
        'reason': str(reason)[:500],
    }
    raise ToolExecutionError('MEDIA_CAPABILITY_DEPENDENCY_MISSING ' + json.dumps({
        'status': 'blocked', 'workflow': 'ppt_search', 'required': ['web_search'],
        'checks': [missing], 'missing': [missing],
        'message': '必需素材搜索暂不可用。请配置搜索服务后返回原任务继续，已收集的素材会保留。',
    }, ensure_ascii=False, separators=(',', ':')))


def search_result_items(value) -> list[dict]:
    """Unwrap degraded searches for callers which only consume result URLs."""
    return value.get('items', []) if isinstance(value, dict) else (value or [])


def _failure_reason(exc: Exception) -> str:
    status = getattr(getattr(exc, 'response', None), 'status_code', None)
    # CredentialMixin deliberately redacts authentication responses.
    from lazyllm.common import KeyAuthError
    if isinstance(exc, KeyAuthError) or status in (401, 403):
        return 'authentication_failed'
    if status == 402:
        return 'quota_exhausted'
    if status == 429:
        return 'rate_limited'
    return 'temporarily_unavailable'


def _retry_seconds(response) -> float:
    value = response.headers.get('Retry-After', '')
    try:
        return max(1.0, float(value))
    except (TypeError, ValueError):
        try:
            return max(1.0, parsedate_to_datetime(value).timestamp() - time.time())
        except (TypeError, ValueError, OverflowError):
            return 60.0


class KeenableSearch(_LazyLLMKeenableSearch):
    """Search with the user's Keenable key or the built-in keyless service.

    Other configured search providers take priority over this default. On failure,
    Wikipedia supplies explicitly labelled encyclopedic background only; degraded
    results include configuration guidance and must not be treated as current facts.
    """

    def __init__(self, api_key: str | None = None, **kwargs):
        kwargs.setdefault('timeout', 15)
        super().__init__(api_key=api_key, **kwargs)
        self._use_request_key = api_key is None

    def __key_source__(self) -> bool:
        # Defined on the class so InstanceToolGroup, which reads
        # ``type(instance).__key_source__``, sees the same check as the tool list.
        auth = self._request_auth()
        return (not self._use_request_key or self.source_name in auth
                or not any(auth.get(name) for name in ('google', 'bing', 'bocha', 'tavily')))

    @staticmethod
    def _request_auth() -> dict:
        mapping = lazyllm.globals.config['dynamic_tool_auth'] or {}
        return mapping if isinstance(mapping, dict) else {}

    def _get_token(self) -> str:
        if self._use_request_key:
            token = self._request_auth().get(self.source_name) or ''
            if isinstance(token, (list, tuple)):
                token = next((key for key in token if key), '')
        else:
            token = super()._get_token() or ''
        token = str(token).strip()
        if token.lower() == _PUBLIC_TOKEN:
            return ''
        return token

    def _http_execute(self, method: str, url: str, **kwargs):
        kwargs['headers'] = {**(kwargs.get('headers') or {}), 'X-Keenable-Title': 'LazyMind'}
        return super()._http_execute(method, url, **kwargs)

    @fc_register(host_file='NONE')
    def search(self, query: str, max_results: int = 10):
        """Search the web, returning labelled background and setup guidance on failure.

        Args:
            query (str): One search intent.
            max_results (int): Maximum number of results, from 1 to 50.
        """
        global _PUBLIC_RETRY_AT
        if not isinstance(query, str) or not query.strip():
            raise ValueError('query must be a nonempty string')
        if type(max_results) is not int or max_results < 1:
            raise ValueError('max_results must be a positive integer')
        max_results = min(50, max_results)
        public = not self._get_token()
        with _PUBLIC_LOCK:
            cooling_down = public and time.monotonic() < _PUBLIC_RETRY_AT
        reason = 'rate_limited'
        if not cooling_down:
            try:
                results = super().search(query, max_results=max_results)
                if results or not public:
                    return results
                reason = 'no_results'
            except Exception as exc:
                reason = _failure_reason(exc)
                if public and reason == 'rate_limited':
                    with _PUBLIC_LOCK:
                        _PUBLIC_RETRY_AT = max(
                            _PUBLIC_RETRY_AT, time.monotonic() + _retry_seconds(exc.response),
                        )
        # Do not retry in a tight loop or wait for an hourly public quota to reset.
        # Never include exception text: it can contain credentials or response bodies.
        try:
            language = 'zh' if re.search(r'[\u4e00-\u9fff]', query) else 'en'
            results = WikipediaSearch(base_url=f'https://{language}.wikipedia.org', timeout=8).search(
                query, limit=max_results,
            )
        except Exception:
            results = []
        return {
            'status': 'partial' if results else 'unavailable',
            'items': results,
            'provider': 'keenable',
            'reason': reason,
            'fallback': 'wikipedia',
            'limitations': '百科兜底仅提供背景知识，不能验证新闻、最新数据或真实图片；缺失素材须在产出中标注。',
            'settings_url': SEARCH_SETTINGS_URL,
            'message': (
                ('默认搜索没有返回结果。' if reason == 'no_results' else '搜索服务暂不可用或额度受限。')
                + '可在设置中配置自有搜索服务，然后在原任务继续素材收集。'
            ),
        }

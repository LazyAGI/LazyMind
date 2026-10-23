"""Read-only supplier checks; actual model inference belongs to model_probe."""
import asyncio
import json
from urllib.parse import urlsplit

import httpx

from lazymind.provider_request import apply_request_advanced

from .model_probe import ProbeInput
from .model_probe.runner import _error_code, _headers, _valid_url

CHECK_TIMEOUT_SECONDS = 20
MAX_CHECK_RESPONSE_BYTES = 2 * 1024 * 1024


def _models_url(source: str, url: str) -> str:
    root = url.rstrip('/')
    path = urlsplit(url).path.rstrip('/')
    if path.endswith('/models'):
        return root
    if path.endswith('/chat/completions'):
        root = root[:-len('/chat/completions')]
        path = urlsplit(root).path
    if source == 'qwen' and not path.endswith('/compatible-mode/v1'):
        root += '/compatible-mode/v1'
    elif source in {'kimi', 'claude', 'anthropic'} and not path.endswith('/v1'):
        root += '/v1'
    elif source == 'ppio' and not path.endswith(('/openai', '/v1')):
        root += '/openai'
    elif source in {'openai', 'vllm', 'lmdeploy'} and not path:
        root += '/v1'
    return root + '/models'


async def check_provider_connection(*, source: str | None, url: str | None, api_key: str | None = None,
                                    skip_auth: bool = False, model: str | None = None,
                                    models_url: str | None = None,
                                    request_advanced: dict | None = None,
                                    client: httpx.AsyncClient | None = None) -> dict:
    """Never choose a default model or issue inference while saving/checking keys."""
    del model  # Kept for compatibility with older internal callers.
    failure = {'success': False, 'message': 'provider connection check failed', 'reason': 'invalid_request'}
    if not source or not url or not _valid_url(url):
        return failure
    source = source.strip().lower()
    target = models_url or _models_url(source, url)
    original, resolved = urlsplit(url), urlsplit(target)
    if not _valid_url(target) or (resolved.netloc, resolved.scheme) != (original.netloc, original.scheme):
        return failure
    request = ProbeInput(model='', model_type='', source=source, url=url, api_key=api_key or '', skip_auth=skip_auth)

    async def execute(active_client):
        auth = 'anthropic' if source in {'claude', 'anthropic'} else 'bearer'
        options = apply_request_advanced(request_advanced, method='GET', url=target, headers=_headers(request, auth))
        async with active_client.stream('GET', options['url'], headers=options['headers'], follow_redirects=False,
                                        timeout=httpx.Timeout(15, connect=5)) as response:
            if response.headers.get('content-encoding', 'identity').lower() not in {'', 'identity'}:
                return failure | {'reason': 'invalid_response'}
            content = bytearray()
            async for chunk in response.aiter_bytes():
                if len(content) + len(chunk) > MAX_CHECK_RESPONSE_BYTES:
                    return failure | {'reason': 'invalid_response'}
                content.extend(chunk)
            try:
                payload = json.loads(content)
            except ValueError:
                payload = None
            reason = _error_code(response.status_code, payload)
            if response.status_code == 404:
                reason = 'model_list_unavailable'
            if reason:
                return failure | {'reason': reason}
            rows = payload
            if isinstance(payload, dict):
                rows = payload.get('data', payload.get('models'))
            if not isinstance(rows, list):
                return failure | {'reason': 'invalid_response'}
            return {'success': True, 'message': 'model connection is available'}

    try:
        async with asyncio.timeout(CHECK_TIMEOUT_SECONDS):
            if client is not None:
                return await execute(client)
            async with httpx.AsyncClient() as owned:
                return await execute(owned)
    except (TimeoutError, httpx.TimeoutException):
        return failure | {'reason': 'timeout'}
    except httpx.RequestError:
        return failure | {'reason': 'connection_error'}
    except (ValueError, TypeError):
        return failure | {'reason': 'invalid_request'}

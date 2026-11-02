"""Validated, request-scoped supplier transport options.

The compatibility hooks live here so the pinned LazyLLM checkout stays intact.
Only a dynamic model call activates the transport policy; cached suppliers and
unrelated HTTP clients never retain private headers, query values or body data.
"""
from contextvars import ContextVar
from copy import copy, deepcopy
from functools import wraps
from http import HTTPStatus
from io import BytesIO
import json
import posixpath
import re
import threading
from urllib.parse import parse_qsl, unquote, urlencode, urlsplit, urlunsplit

MAX_ADVANCED_BYTES = 6 * 1024
_PROTECTED_HEADERS = frozenset({
    'authorization', 'proxy-authorization', 'x-api-key', 'host', 'content-type', 'content-length',
    'accept-encoding', 'anthropic-version', 'connection', 'transfer-encoding', 'te', 'trailer', 'upgrade',
})
_PROTECTED_BODY = frozenset({
    'model', 'messages', 'input', 'prompt', 'query', 'documents', 'stream', 'file', 'files',
    'api_key', 'apikey', 'authorization', 'encoding_format', 'dimensions',
})
_ACTIVE_POLICY = ContextVar('lazymind_provider_request_policy', default=None)
_HOOK_LOCK = threading.Lock()
_INSTALLED = False


class ProviderRequestConfigurationError(ValueError):
    """A safe, explicit unsupported/invalid configuration diagnostic."""


class ProviderRequestFailure(RuntimeError):
    def __init__(self, code='provider_request_failed', http_status=None):
        self.code = code
        self.http_status = http_status
        super().__init__(code, http_status)

    def __str__(self):
        status = f' (HTTP {self.http_status})' if self.http_status else ''
        return f'provider request failed: {self.code}{status}'


def validate_runtime_request_transport(source, kind, model):
    """Reject known unhooked SDKs before constructing or invoking a supplier."""
    from lazymind.chat.model_probe.runtime_support import runtime_source, supports_runtime_type
    source = runtime_source(str(source or ''))
    kind = {'chat': 'llm', 'embedding': 'embed', 'cross_modal_embed': 'multimodal_embedding'}.get(kind, kind)
    if (not supports_runtime_type(source, kind)
            or source == 'glm' and kind in {'stt', 'text2image'}
            or source == 'qwen' and kind == 'tts' and str(model or '').lower().startswith(('cosyvoice', 'sambert'))):
        raise ProviderRequestConfigurationError('advanced request configuration is unsupported by this model transport')


def _sanitize_failed_response(response, policy):
    """Replace errors before adapters can log provider-echoed private data."""
    status = response.status_code
    if 200 <= status < 300:
        return response
    # Inspect only a small body already buffered by the HTTP client. Reading
    # response.content/json here would consume an unbounded streamed error.
    payload = {}
    cached = response._content
    if isinstance(cached, (bytes, bytearray)) and len(cached) <= 16 * 1024:
        try:
            decoded = json.loads(cached)
            if isinstance(decoded, dict):
                payload = decoded
        except (ValueError, TypeError, RecursionError):
            pass
    from lazymind.chat.model_probe.runner import _error_code
    generic_code = _error_code(status, payload) or 'upstream_error'
    profile = policy.get('response_profile')
    if profile is not None:
        from lazyllm.module.llms.onlinemodule.base.model_outcome import ModelFailureCode, ModelFailureOrigin
        provider_code, provider_type = profile.extract_error(payload) or (None, None)
        code = profile.classify(
            origin=ModelFailureOrigin.HTTP, provider_error_code=provider_code, provider_error_type=provider_type,
            provider_http_status=status, provider_response=payload,
        ).value
        mapped = (str(provider_code or '').lower() in profile.code_map
                  or str(provider_type or '').lower() in profile.type_map)
        if not mapped and generic_code == 'quota_exceeded':
            code = ModelFailureCode.QUOTA_EXHAUSTED.value
    else:
        code = generic_code
    policy['failure'] = {'code': code, 'http_status': status}
    if response.raw is not None:
        response.close()
    # LazyLLM configures the raw stream after the request returns. Keep a
    # harmless, empty stream there so it cannot reach the provider's body.
    response.raw = BytesIO()
    response._content = json.dumps({'error': {'code': code, 'message': 'provider request failed'}}).encode()
    response._content_consumed = True
    response.encoding = 'utf-8'
    response.headers.clear()
    response.headers['Content-Type'] = 'application/json'
    response.history = []
    response.cookies.clear()
    response._next = None
    try:
        response.reason = HTTPStatus(status).phrase
    except ValueError:
        response.reason = 'Provider response'
    if response.request is not None:
        safe_request = copy(response.request)
        parsed = urlsplit(safe_request.url)
        safe_request.url = urlunsplit(parsed._replace(query='', fragment=''))
        safe_request.headers = {'Content-Type': 'application/json'}
        safe_request.body = None
        response.request = safe_request
        response.url = safe_request.url
    return response


def _configuration_error_in_chain(error):
    seen = set()
    while error is not None and id(error) not in seen:
        seen.add(id(error))
        if isinstance(error, ProviderRequestConfigurationError):
            return error
        error = error.__cause__ or error.__context__
    return None


def validate_request_advanced(config):
    """Return an isolated JSON object; errors never interpolate private values."""
    if config is None:
        return {}
    if not isinstance(config, dict) or set(config) - {'headers', 'query', 'extra_body'}:
        raise ProviderRequestConfigurationError('invalid advanced request configuration')
    try:
        encoded = json.dumps(config, ensure_ascii=False, allow_nan=False, separators=(',', ':')).encode()
    except (ValueError, TypeError, OverflowError, RecursionError):
        raise ProviderRequestConfigurationError('invalid advanced request configuration') from None
    if len(encoded) > MAX_ADVANCED_BYTES:
        raise ProviderRequestConfigurationError('advanced request configuration exceeds size limit')
    result = json.loads(encoded)
    for field, maximum in (('headers', 32), ('query', 32), ('extra_body', 64)):
        value = result.get(field, {})
        if value is None:
            value = {}
            result[field] = value
        if not isinstance(value, dict) or len(value) > maximum:
            raise ProviderRequestConfigurationError('advanced request fields must be JSON objects')
    seen = set()
    for key, value in result.get('headers', {}).items():
        lower = key.lower()
        if (not re.fullmatch(r"[!#$%&'*+\-.^_`|~0-9A-Za-z]{1,128}", key)
                or lower in seen or not isinstance(value, str) or len(value.encode()) > 2048
                or any(ord(c) > 126 or ord(c) < 32 and c != '\t' for c in value)):
            raise ProviderRequestConfigurationError('invalid custom request header')
        if lower in _PROTECTED_HEADERS:
            raise ProviderRequestConfigurationError(
                'authentication, protocol and transport headers are managed by LazyMind',
            )
        seen.add(lower)
    for key, value in result.get('query', {}).items():
        if (not key or key != key.strip() or len(key.encode()) > 128 or not isinstance(value, str)
                or len(value.encode()) > 2048 or any(c in key + value for c in '\r\n\x00')):
            raise ProviderRequestConfigurationError('invalid custom query parameter')
    for key in result.get('extra_body', {}):
        if not key or len(key.encode()) > 128 or key.lower() in _PROTECTED_BODY:
            raise ProviderRequestConfigurationError(
                'model, input, output format and streaming fields are managed by LazyMind',
            )

    def check_depth(value, depth=0):
        if depth > 10:
            raise ProviderRequestConfigurationError('advanced request body exceeds nesting limit')
        for child in value.values() if isinstance(value, dict) else value if isinstance(value, list) else ():
            check_depth(child, depth + 1)
    for value in result.get('extra_body', {}).values():
        check_depth(value)
    return result


def apply_request_advanced(config, *, method, url, headers=None, json_body=None, data=None, files=None,
                           features=None, source=''):
    """Apply options at the final HTTP boundary; adapter-owned values win.

    GET catalog/check requests have no body. A configured extra body on a
    multipart inference raises a safe error before sending any HTTP request.
    """
    config = validate_request_advanced(config)
    result = {'url': str(url), 'headers': dict(headers or {})}
    result['headers'].update(config.get('headers', {}))
    parsed = urlsplit(str(url))
    query = parse_qsl(parsed.query, keep_blank_values=True)
    existing = {key for key, _ in query}
    query.extend((key, value) for key, value in config.get('query', {}).items() if key not in existing)
    if query:
        result['url'] = urlunsplit(parsed._replace(query=urlencode(query)))
    extra = config.get('extra_body', {})
    feature_disables = isinstance(features, dict) and any(
        features.get(key) is False for key in ('tool_calling', 'reasoning')
    )
    if str(method).upper() not in {'GET', 'HEAD', 'OPTIONS'} and (extra or feature_disables):
        content_type = next((value for key, value in result['headers'].items() if key.lower() == 'content-type'), '')
        if (json_body is None and files is None and isinstance(data, (str, bytes))
                and content_type.partition(';')[0].strip().lower() == 'application/json'):
            try:
                json_body = json.loads(data)
                data = None
                result['data'] = None
            except (ValueError, TypeError):
                raise ProviderRequestConfigurationError('invalid JSON provider request') from None
        if files is not None or data is not None or not isinstance(json_body, dict):
            raise ProviderRequestConfigurationError('extra JSON body is unsupported for this request transport')
        # A generated field always keeps its value. Extra fields are defaults,
        # so provider-specific mandatory objects are not overwritten either.
        result['json'] = {**deepcopy(extra), **json_body}
    elif json_body is not None:
        result['json'] = json_body
    if feature_disables and 'json' in result:
        from lazymind.model_features import apply_model_features
        result['json'] = apply_model_features(result['json'], features, source)
    return result


def configure_request_advanced(model_config):
    """Replace this LazyLLM session's private, per-role transport settings."""
    import lazyllm
    policies = {}
    for role, entry in (model_config or {}).items():
        if not isinstance(entry, dict):
            continue
        advanced = validate_request_advanced(entry.get('request_advanced'))
        kind = str(entry.get('type') or 'llm').lower()
        features = entry.get('features') if kind in {'llm', 'vlm', 'chat'} else None
        features = features if isinstance(features, dict) else {}
        feature_disables = any(features.get(key) is False for key in ('tool_calling', 'reasoning'))
        private = any(advanced.get(key) for key in ('headers', 'query', 'extra_body'))
        if not feature_disables and not private:
            continue
        base = str(entry.get('base_url') or '').strip()
        parsed = urlsplit(base)
        if private and (parsed.scheme not in {'http', 'https'} or not parsed.hostname
                        or parsed.query or parsed.fragment or parsed.username or parsed.password):
            raise ProviderRequestConfigurationError('advanced request configuration requires a valid supplier API root')
        policies[role] = {'base': base, 'advanced': advanced, 'source': str(entry.get('source') or '').lower(),
                          'model': str(entry.get('model') or '').lower(), 'features': deepcopy(features),
                          'private': private, 'kind': kind}
    lazyllm.globals['lazymind_request_advanced'] = policies
    _install_runtime_hooks()


def _within_supplier(url, base):
    target, root = urlsplit(str(url)), urlsplit(base)
    # Match the path the HTTP server will route, including encoded dot/segment
    # separators, so a media URL cannot escape into a sibling proxy tenant.
    target_path = posixpath.normpath(unquote(target.path))
    prefix = posixpath.normpath(unquote(root.path)).rstrip('/')
    if '\\' in target_path or '\\' in prefix:
        return False
    if prefix == '.':
        prefix = ''
    return ((target.scheme.lower(), target.netloc.lower()) == (root.scheme.lower(), root.netloc.lower())
            and (not prefix or target_path == prefix or target_path.startswith(prefix + '/')))


def _install_runtime_hooks():
    global _INSTALLED
    with _HOOK_LOCK:
        if _INSTALLED:
            return
        import lazyllm
        import requests
        from lazyllm.module.llms.onlinemodule.dynamic_router import _DynamicSourceRouterMixin
        original_forward = _DynamicSourceRouterMixin.forward
        original_request = requests.Session.request

        @wraps(original_request)
        def request(session, method, url, **kwargs):
            policy = _ACTIVE_POLICY.get()
            if policy is not None and (not policy['private'] or _within_supplier(url, policy['base'])):
                applied = apply_request_advanced(
                    policy['advanced'], method=method, url=url, headers=kwargs.get('headers'),
                    json_body=kwargs.get('json'), data=kwargs.get('data'), files=kwargs.get('files'),
                    features=policy['features'], source=policy['source'],
                )
                url = applied.pop('url')
                kwargs.update(applied)
                # Extra credentials must never follow a provider redirect.
                kwargs['allow_redirects'] = False
                if str(method).upper() not in {'GET', 'HEAD', 'OPTIONS'}:
                    policy['calls'] += 1
            try:
                response = original_request(session, method, url, **kwargs)
                return _sanitize_failed_response(response, policy) if policy is not None else response
            except requests.RequestException as exc:
                if policy is not None:
                    raise type(exc)('provider request failed') from None
                raise

        @wraps(original_forward)
        def forward(module, *args, **kwargs):
            policies = lazyllm.globals.get('lazymind_request_advanced', {}) or {}
            identities = [*(lazyllm.globals.current_stack() or []), *module.identities, 'default']
            policy = next((policies[key] for key in identities if key in policies), None)
            if policy is None:
                return original_forward(module, *args, **kwargs)
            validate_runtime_request_transport(policy['source'], policy['kind'], policy['model'])
            merged = module._merge_dynamic_forward_kwargs(kwargs)
            target = merged.get('url') or merged.get('base_url') or policy['base']
            if policy['private'] and not _within_supplier(target, policy['base']):
                raise ProviderRequestConfigurationError(
                    'advanced request configuration does not match the supplier API root',
                )
            active = {**policy, 'calls': 0, 'failure': None}
            token = _ACTIVE_POLICY.set(active)
            try:
                supplier = copy(module._get_supplier())
                profile = getattr(supplier, 'RESPONSE_PROFILE', None)
                active['response_profile'] = profile
                if profile is not None:
                    from lazyllm.module.llms.onlinemodule.base.model_outcome import ModelFailureCode
                    # Re-classification by the adapter must preserve the safe
                    # category, without retaining raw provider codes/messages.
                    supplier.RESPONSE_PROFILE = profile.extend(code_map={code.value: code for code in ModelFailureCode})
                # The pinned embedding adapter's worker pool does not copy context.
                # A per-call clone keeps this setting out of the shared cache.
                if hasattr(supplier, '_num_worker'):
                    supplier._num_worker = 1
                output = supplier.forward(*args, **merged)
                if active['calls'] == 0:
                    raise ProviderRequestConfigurationError(
                        'advanced request configuration is unsupported by this model transport',
                    )
                return output
            except Exception as exc:
                configuration_error = _configuration_error_in_chain(exc)
                if configuration_error is not None:
                    raise ProviderRequestConfigurationError(str(configuration_error)) from None
                from lazyllm.module.llms.onlinemodule.base.model_outcome import ModelCallError
                if isinstance(exc, ModelCallError):
                    # Preserve structured categories/status used by the chat UI.
                    raise ModelCallError('provider request failed', exc.terminal) from None
                raise ProviderRequestFailure(**(active['failure'] or {})) from None
            finally:
                _ACTIVE_POLICY.reset(token)

        requests.Session.request = request
        _DynamicSourceRouterMixin.forward = forward
        _INSTALLED = True

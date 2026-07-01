from __future__ import annotations

import json
import re
import time
import urllib.error
import urllib.parse
import urllib.request
from collections.abc import Mapping
from dataclasses import dataclass
from hashlib import sha256
from typing import Any, NamedTuple

from evo.artifact_runtime import ExternalCallRequest, ExternalCallResult
from evo.llm import LazyLLMClient

METRICS = (
    'answer_correctness',
    'answer_relevance',
    'completeness',
    'format_compliance',
    'answer_score',
    'chunk_recall',
    'chunk_precision',
    'doc_recall',
    'doc_precision',
    'retrieval_score',
)

DISABLED_CHAT_TOOLS = (
    'temp_kb',
    'wikipedia',
    'web_search',
    'academic_search',
    'url_fetch',
    'multimodal',
    'vocab_learn',
    'memory_editor',
    'skill_editor',
    'feishu',
)


class ServiceResult(NamedTuple):
    status: str
    value: Mapping[str, Any] | None = None
    error_type: str = ''
    error_message: str = ''


@dataclass(frozen=True)
class OperationServices:
    ctx: Any

    @property
    def llm_config(self) -> Mapping[str, Any]:
        value = getattr(self.ctx, 'llm_config', None)
        return value if isinstance(value, Mapping) else {}

    def raise_if_cancelled(self) -> None:
        if hasattr(self.ctx, 'raise_if_cancelled'):
            self.ctx.raise_if_cancelled()

    def external_call(
        self,
        *,
        call_id: str,
        payload: Mapping[str, Any],
        runner: Any,
        idempotency_key: str,
        payload_fingerprint: str,
        metadata: Mapping[str, Any],
    ) -> ExternalCallResult:
        external = getattr(self.ctx, 'external', None)
        if external is None:
            raise RuntimeError('external call gateway is not configured')
        return external.call(
            call_id=call_id,
            payload=payload,
            runner=runner,
            idempotency_key=idempotency_key,
            payload_fingerprint=payload_fingerprint,
            metadata=metadata,
        )

    def answer_case(self, case: Mapping[str, Any], target_config: Mapping[str, Any]) -> Mapping[str, Any]:
        case_id = text(case.get('id'))
        question = text(case.get('question'))
        target_url = text(target_config.get('target_chat_url'))
        dataset_id = text(target_config.get('dataset_id') or target_config.get('kb_id')
                          or target_config.get('dataset_name'))
        algorithm_id = text(target_config.get('algorithm_id'))
        request_payload = {
            'query': _rag_answer_query(question),
            'history': [],
            'session_id': _chat_session_id(case_id, dataset_id, algorithm_id, question),
            'trace': bool(target_config.get('require_trace', True)),
            'dataset': dataset_id,
            'filters': {'kb_id': [dataset_id]} if dataset_id else {},
            'reasoning': False,
            'disabled_tools': list(DISABLED_CHAT_TOOLS),
        }
        if algorithm_id:
            request_payload['algorithm_id'] = algorithm_id
        if not target_url:
            result = ServiceResult('failed_permanent', error_type='missing_target_chat_url',
                                   error_message='target_chat_url is empty')
        else:
            result = self.chat(
                target_url=target_url,
                case_id=case_id,
                payload={**request_payload, 'llm_config': self.llm_config or None},
            )
        value = dict(result.value or {})
        routed_algorithm_id = text(value.get('routed_algorithm_id'))
        value['question'] = question
        value['trace_id'] = request_payload['session_id'] if request_payload['trace'] else ''
        value['target'] = {
            'target_chat_url': target_url,
            'dataset_id': dataset_id,
            'require_trace': request_payload['trace'],
            **({'algorithm_id': algorithm_id} if algorithm_id else {}),
            **({'routed_algorithm_id': routed_algorithm_id} if routed_algorithm_id else {}),
        }
        return {
            'status': result.status,
            'value': value,
            'error_type': result.error_type,
            'error_message': result.error_message,
        }

    def chat(self, *, target_url: str, case_id: str, payload: Mapping[str, Any]) -> ServiceResult:
        fingerprint = stable_text({
            'target_chat_url': target_url,
            'payload': request_identity(payload),
            'llm_config': llm_config_identity(self.llm_config),
        })
        result = self.external_call(
            call_id=f'rag_answer:{case_id}',
            payload={'target_chat_url': target_url, 'payload': payload},
            runner=HttpChatRunner(),
            idempotency_key=f'{case_id}:rag:{fingerprint}',
            payload_fingerprint=fingerprint,
            metadata={'kind': 'rag_answer', 'case_id': case_id},
        )
        return service_result(result)

    def register_candidate_algorithm(
        self,
        *,
        router_admin_url: str,
        algorithm_id: str,
        body: Mapping[str, Any],
        timeout_s: int,
    ) -> ServiceResult:
        payload = {'router_admin_url': router_admin_url, 'algorithm_id': algorithm_id, 'body': body}
        result = self.external_call(
            call_id=f'candidate_service:register:{algorithm_id}',
            payload=payload,
            runner=RouterCandidateRegisterRunner(timeout_s=timeout_s),
            idempotency_key=f'candidate-service:{algorithm_id}:{stable_text(request_identity(body))}',
            payload_fingerprint=stable_text(payload),
            metadata={'kind': 'candidate_service', 'algorithm_id': algorithm_id},
        )
        return service_result(result)

    def llm_complete(self, prompt: str) -> str:
        fingerprint = stable_text({'prompt': prompt, 'llm_config': llm_config_identity(self.llm_config)})
        fingerprint_key = sha256(fingerprint.encode('utf-8')).hexdigest()
        result = self.external_call(
            call_id=f'llm_complete:{sha256(prompt.encode("utf-8")).hexdigest()[:16]}',
            payload={'prompt': prompt, 'llm_config_identity': llm_config_identity(self.llm_config)},
            runner=LlmCompleteRunner(dict(self.llm_config)),
            idempotency_key=f'llm-complete:{fingerprint_key}',
            payload_fingerprint=fingerprint,
            metadata={'kind': 'llm_complete'},
        )
        if result.status != 'completed':
            raise RuntimeError(f'{result.error_type or result.status}: {result.error_message}')
        return text(result.value)


@dataclass(frozen=True)
class HttpChatRunner:
    timeout_s: float = 20.0
    max_attempts: int = 6
    backoff_s: float = 1.0

    def invoke(self, request: ExternalCallRequest, token: Any) -> ExternalCallResult:
        target_url = text(request.payload.get('target_chat_url'))
        body = json.dumps(request.payload.get('payload') or {}, ensure_ascii=False).encode('utf-8')
        last_error: BaseException | None = None
        for attempt in range(1, max(1, self.max_attempts) + 1):
            try:
                token.raise_if_cancelled()
                req = urllib.request.Request(
                    target_url,
                    data=body,
                    method='POST',
                    headers={'content-type': 'application/json'},
                )
                with urllib.request.urlopen(req, timeout=self.timeout_s) as response:
                    raw = response.read().decode('utf-8', 'replace')
                    routed_algorithm = response.headers.get('X-Algorithm-Id') or ''
                    routed_instance = response.headers.get('X-Instance-Host') or ''
                parsed = parse_chat_response(raw)
                if routed_algorithm:
                    parsed['routed_algorithm_id'] = routed_algorithm
                if routed_instance:
                    parsed['routed_instance_host'] = routed_instance
                return ExternalCallResult('completed', parsed, metadata={'target_url': target_url, 'attempt': attempt})
            except urllib.error.HTTPError as exc:
                last_error = exc
                if exc.code not in {429, 502, 503, 504} or attempt >= self.max_attempts:
                    break
            except (urllib.error.URLError, OSError) as exc:
                last_error = exc
                if attempt >= self.max_attempts:
                    break
            token.raise_if_cancelled()
            time.sleep(min(self.backoff_s * (2 ** (attempt - 1)), 8.0))
        exc = last_error or RuntimeError('chat call failed')
        if isinstance(exc, urllib.error.HTTPError) and exc.code == 429:
            return ExternalCallResult('rate_limited', error_type=type(exc).__name__, error_message=str(exc))
        if isinstance(exc, TimeoutError):
            return ExternalCallResult('timeout', error_type=type(exc).__name__, error_message=str(exc))
        return ExternalCallResult('failed_transient', error_type=type(exc).__name__, error_message=str(exc))


@dataclass(frozen=True)
class RouterCandidateRegisterRunner:
    timeout_s: float = 180.0

    def invoke(self, request: ExternalCallRequest, token: Any) -> ExternalCallResult:
        router_admin_url = text(request.payload.get('router_admin_url')).rstrip('/')
        algorithm_id = text(request.payload.get('algorithm_id'))
        body = request.payload.get('body') if isinstance(request.payload.get('body'), Mapping) else {}
        try:
            existing = _router_get(router_admin_url, algorithm_id, token, timeout_s=10)
            if existing:
                _ensure_existing_candidate_matches(existing, body)
            registered = existing if existing.get('status') == 'active' and existing.get('instances') else None
            if registered is None:
                registered = _router_post_register(router_admin_url, body, token, timeout_s=self.timeout_s)
            ready = _wait_router_algorithm_ready(router_admin_url, algorithm_id, token, timeout_s=self.timeout_s)
            ports = ready.get('ports') or registered.get('ports') or [
                item.get('port') for item in as_list(ready.get('instances')) if isinstance(item, Mapping)
            ]
            return ExternalCallResult('completed', {
                'algorithm_id': algorithm_id,
                'ports': [port for port in ports if port],
                'registration': registered,
                'ready': ready,
            })
        except urllib.error.HTTPError as exc:
            return ExternalCallResult(
                'failed_permanent',
                error_type='HTTPError',
                error_message=f'{exc.code}: {exc.reason}',
            )
        except TimeoutError as exc:
            return ExternalCallResult('timeout', error_type='TimeoutError', error_message=str(exc))
        except (urllib.error.URLError, OSError, ValueError, RuntimeError) as exc:
            return ExternalCallResult('failed_transient', error_type=type(exc).__name__, error_message=str(exc))


@dataclass(frozen=True)
class LlmCompleteRunner:
    llm_config: Mapping[str, Any]

    def invoke(self, request: ExternalCallRequest, token: Any) -> ExternalCallResult:
        token.raise_if_cancelled()
        prompt = text(request.payload.get('prompt'))
        try:
            value = text(LazyLLMClient(llm_config=dict(self.llm_config))(prompt, stream=False))
        except Exception as exc:  # noqa: BLE001 - external call boundary records model failures.
            return ExternalCallResult('failed_transient', error_type=type(exc).__name__, error_message=str(exc))
        token.raise_if_cancelled()
        return ExternalCallResult('completed', value)


def service_result(result: ExternalCallResult) -> ServiceResult:
    value = result.value if isinstance(result.value, Mapping) else None
    return ServiceResult(result.status, value, result.error_type, result.error_message)


def _chat_session_id(case_id: str, dataset_id: str, algorithm_id: str = '', question: str = '') -> str:
    return sha256(f'evo:{dataset_id}:{algorithm_id}:{case_id}:{question}'.encode('utf-8')).hexdigest()[:32]


def _rag_answer_query(question: str) -> str:
    return (
        'You are answering an evaluation case. First call `get_KBToolGroup_methods` to activate the '
        'knowledge-base tool group. Then call the returned knowledge-base search method to retrieve '
        'evidence for the user question. Finally answer concisely from the retrieved evidence.\n\n'
        f'User question: {question}'
    )


def parse_chat_response(raw: str) -> dict[str, Any]:
    raw = raw.strip()
    if not raw:
        return {}
    try:
        body = json.loads(raw)
    except json.JSONDecodeError:
        body = None
    if isinstance(body, Mapping):
        parsed = _chat_payload_from_events([body])
        if not parsed.get('kb_errors'):
            parsed['kb_errors'] = _extract_tool_errors_from_text(raw)
        _merge_tool_sources(parsed, raw)
        return parsed
    if isinstance(body, list):
        parsed = _chat_payload_from_events([item for item in body if isinstance(item, Mapping)])
        if not parsed.get('kb_errors'):
            parsed['kb_errors'] = _extract_tool_errors_from_text(raw)
        _merge_tool_sources(parsed, raw)
        return parsed

    events, text_fragments = [], []
    for line in raw.splitlines():
        line_text = line.removeprefix('data:').strip() if line.startswith('data:') else line.strip()
        if not line_text or line_text == '[DONE]' or line_text.startswith(('event:', 'id:')):
            continue
        try:
            data = json.loads(line_text)
        except json.JSONDecodeError:
            text_fragments.append(line_text)
            continue
        if isinstance(data, Mapping):
            events.append(data)
        elif isinstance(data, list):
            events.extend(item for item in data if isinstance(item, Mapping))
    parsed = _chat_payload_from_events(events)
    if not parsed.get('answer') and text_fragments:
        parsed['answer'] = _clean_answer(''.join(text_fragments))
    if not parsed.get('kb_errors'):
        parsed['kb_errors'] = _extract_tool_errors_from_text(raw)
    _merge_tool_sources(parsed, raw)
    return parsed


def as_list(value: Any) -> list[Any]:
    if value is None:
        return []
    if isinstance(value, list):
        return value
    if isinstance(value, tuple | set):
        return list(value)
    return [value]


def avg(values: Any) -> float:
    rows = list(values)
    return round(sum(rows) / len(rows), 4) if rows else 0.0


def chunks(items: list[Any], size: int) -> list[list[Any]]:
    return [items[index:index + size] for index in range(0, len(items), size)]


def clip(value: Any, limit: int) -> str:
    value_text = text(value)
    return value_text if len(value_text) <= limit else value_text[: max(0, limit - 15)] + '\n...[truncated]'


def first_text(item: Mapping[str, Any], *keys: str) -> str:
    return next((text(item.get(key)) for key in keys if text(item.get(key))), '')


def int_between(value: Any, default: int, low: int, high: int) -> int:
    try:
        number = int(value)
    except (TypeError, ValueError):
        number = default
    return min(high, max(low, number))


def json_safe(value: Any) -> Any:
    if isinstance(value, Mapping):
        return {str(key): json_safe(item) for key, item in value.items()}
    if isinstance(value, (list, tuple, set)):
        return [json_safe(item) for item in value]
    return value if value is None or isinstance(value, (str, int, float, bool)) else str(value)


def norm(value: Any) -> str:
    return re.sub(r'\s+', '', text(value).lower())


def request_identity(value: Any) -> Any:
    if isinstance(value, Mapping):
        return {key: request_identity(item) for key, item in value.items() if key != 'llm_config'}
    if isinstance(value, (list, tuple)):
        return [request_identity(item) for item in value]
    return value


def stable_text(value: Any) -> str:
    return json.dumps(value, ensure_ascii=False, sort_keys=True, default=str)


def text(value: Any) -> str:
    return '' if value is None else str(value).strip()


def unique_texts(items: Any) -> list[str]:
    return list(dict.fromkeys(item for item in (text(value) for value in as_list(items)) if item))


def llm_config_identity(value: Any) -> dict[str, dict[str, Any]]:
    if not isinstance(value, Mapping):
        return {}
    safe_fields = ('source', 'model', 'base_url', 'url', 'type', 'skip_auth')
    return {
        role: {field: config[field] for field in safe_fields if field in config and config[field] not in (None, '')}
        for role, config in sorted((text(role), item) for role, item in value.items())
        if isinstance(config, Mapping)
    }


def _router_get(router_admin_url: str, algorithm_id: str, token: Any, *, timeout_s: float) -> dict[str, Any]:
    try:
        return _request_json(
            'GET',
            f'{router_admin_url}/inner/algorithm/{urllib.parse.quote(algorithm_id)}',
            token,
            timeout_s=timeout_s,
        )
    except urllib.error.HTTPError as exc:
        if exc.code == 404:
            return {}
        raise


def _router_post_register(
    router_admin_url: str,
    body: Mapping[str, Any],
    token: Any,
    *,
    timeout_s: float,
) -> dict[str, Any]:
    return _request_json('POST', f'{router_admin_url}/inner/algorithm/register', token, body=body, timeout_s=timeout_s)


def _wait_router_algorithm_ready(
    router_admin_url: str,
    algorithm_id: str,
    token: Any,
    *,
    timeout_s: float,
) -> dict[str, Any]:
    deadline = time.time() + timeout_s
    last: dict[str, Any] = {}
    while time.time() < deadline:
        token.raise_if_cancelled()
        last = _router_get(router_admin_url, algorithm_id, token, timeout_s=10)
        instances = [item for item in as_list(last.get('instances')) if isinstance(item, Mapping)]
        healthy = [item for item in instances if item.get('status') == 'healthy']
        if last.get('status') == 'active' and healthy:
            return {
                'status': 'ready',
                'algorithm_id': algorithm_id,
                'instances': healthy,
                'ports': [item.get('port') for item in healthy if item.get('port')],
            }
        time.sleep(1)
    raise TimeoutError(f'candidate algorithm did not become healthy: {algorithm_id}; last={last}')


def _request_json(
    method: str,
    url: str,
    token: Any,
    *,
    body: Mapping[str, Any] | None = None,
    timeout_s: float = 30.0,
) -> dict[str, Any]:
    token.raise_if_cancelled()
    data = None if body is None else stable_text(body).encode('utf-8')
    request = urllib.request.Request(url, data=data, method=method, headers={'content-type': 'application/json'})
    with urllib.request.urlopen(request, timeout=timeout_s) as response:
        raw = response.read().decode('utf-8', 'replace')
    value = json.loads(raw) if raw else {}
    if not isinstance(value, Mapping):
        raise RuntimeError(f'{method} {url} returned non-object JSON')
    return dict(value)


def _ensure_existing_candidate_matches(existing: Mapping[str, Any], body: Mapping[str, Any]) -> None:
    expected_path = text(body.get('code_path'))
    actual_path = text(existing.get('code_path'))
    if expected_path and actual_path and expected_path != actual_path:
        raise RuntimeError(f'candidate algorithm_id already points to different code_path: {actual_path}')
    expected_config = body.get('config') if isinstance(body.get('config'), Mapping) else {}
    actual_config = existing.get('config') if isinstance(existing.get('config'), Mapping) else {}
    for key in ('LAZYMIND_ALGO_ID', 'LAZYMIND_ENABLE_ROUTER', 'LAZYMIND_ROUTER_CHILD_PROXIED_ONLY'):
        if text(expected_config.get(key)) != text(actual_config.get(key)):
            raise RuntimeError(f'candidate algorithm_id already has different config for {key}')


def _chat_payload_from_events(events: list[Mapping[str, Any]]) -> dict[str, Any]:
    answer, sources, contexts, doc_ids, chunk_ids, trace_id, kb_errors = [], [], [], [], [], '', []
    for event in events:
        piece = _unwrap_chat_event(event)
        piece_sources = [item for item in as_list(piece.get('sources')) if isinstance(item, Mapping)]
        piece_contexts = as_list(piece.get('contexts'))
        piece_context_sources = [item for item in piece_contexts if isinstance(item, Mapping)]
        piece_text = _chat_text(piece)
        tool_sources = _tool_sources_from_text(piece_text)
        answer.append(piece_text)
        sources.extend([*piece_sources, *tool_sources])
        contexts.extend([
            *(_source_text(item) for item in piece_contexts),
            *(_source_text(item) for item in tool_sources),
        ])
        doc_ids.extend(as_list(piece.get('doc_ids') or piece.get('document_ids')))
        chunk_ids.extend(as_list(piece.get('chunk_ids') or piece.get('segment_ids') or piece.get('segement_ids')))
        source_doc_ids, source_chunk_ids = _source_ids([*piece_sources, *piece_context_sources, *tool_sources])
        doc_ids.extend(source_doc_ids)
        chunk_ids.extend(source_chunk_ids)
        kb_errors.extend(_tool_errors(piece))
        kb_errors.extend(_extract_tool_errors_from_text(piece_text))
        trace_id = trace_id or text(piece.get('trace_id') or piece.get('traceId'))
    return {
        'answer': _clean_answer(''.join(answer)),
        'sources': _unique_sources(sources),
        'contexts': list(dict.fromkeys(item for item in contexts if item)),
        'doc_ids': list(dict.fromkeys(text(item) for item in doc_ids if text(item))),
        'chunk_ids': list(dict.fromkeys(text(item) for item in chunk_ids if text(item))),
        'trace_id': trace_id,
        'kb_errors': list(dict.fromkeys(err for err in kb_errors if err)),
    }


def _unwrap_chat_event(data: Mapping[str, Any]) -> Mapping[str, Any]:
    current: Any = data
    for key in ('data', 'result', 'output', 'message'):
        if isinstance(current, Mapping) and isinstance(current.get(key), Mapping):
            current = current[key]
    return current if isinstance(current, Mapping) else {}


def _chat_text(data: Mapping[str, Any]) -> str:
    for key in ('answer', 'delta', 'text', 'content', 'response'):
        value = data.get(key)
        if isinstance(value, str) and value:
            return value
    message = data.get('message')
    if isinstance(message, str):
        return message
    if isinstance(message, Mapping):
        return _chat_text(message)
    return ''


def _tool_errors(data: Mapping[str, Any]) -> list[str]:
    errors: list[str] = []
    for key in ('tool_error', 'tool_errors', 'error', 'errors'):
        value = data.get(key)
        if isinstance(value, str):
            errors.append(value)
        elif isinstance(value, Mapping):
            errors.extend(_tool_errors(value))
        elif isinstance(value, list):
            for item in value:
                if isinstance(item, str):
                    errors.append(item)
                elif isinstance(item, Mapping):
                    errors.extend(_tool_errors(item))
    return errors


def _extract_tool_errors_from_text(raw: str) -> list[str]:
    errors: list[str] = []
    for raw_item in re.findall(r'<tool_result>(.*?)</tool_result>', raw, flags=re.S):
        try:
            payload = json.loads(raw_item)
        except json.JSONDecodeError:
            continue
        if not isinstance(payload, Mapping):
            continue
        result = payload.get('result')
        if isinstance(result, Mapping):
            if result.get('success') is False:
                errors.append(text(result.get('reason') or result.get('error') or 'kb_search failed'))
            nested = result.get('result')
            if isinstance(nested, Mapping) and nested.get('success') is False:
                errors.append(text(nested.get('reason') or nested.get('error') or 'kb_search failed'))
        elif isinstance(result, str) and _looks_like_tool_error(result):
            errors.append(result)
    return errors


def _looks_like_tool_error(value: str) -> bool:
    lowered = value.lower()
    return (
        'tool [' in lowered
        or 'error' in lowered
        or 'failed' in lowered
        or 'exception' in lowered
        or 'traceback' in lowered
        or '工具未激活' in value
        or '失败' in value
    )


def _clean_answer(value: str) -> str:
    cleaned = re.sub(r'<(?P<tag>tp|trp|tool_call|tool_result)(?:\s[^>]*)?>.*?</(?P=tag)>', '', value, flags=re.S)
    cleaned = _strip_tool_status_text(cleaned)
    cleaned = re.sub(r'\n{3,}', '\n\n', cleaned)
    return cleaned.strip()


def _strip_tool_status_text(value: str) -> str:
    patterns = (
        (
            r'(?im)^\s*I will (?:first )?(?:activate|call|use|search|now search|look|retrieve|query)\b'
            r'.*(?:knowledge base|KBToolGroup|kb_search|tool group).*$'
        ),
        r'(?im)^\s*I will now search\b.*$',
        (
            r"(?im)^\s*I(?:'ll| am going to) (?:first )?(?:activate|call|use|search|look|retrieve|query)\b"
            r'.*(?:knowledge base|KBToolGroup|kb_search|tool group).*$'
        ),
    )
    for pattern in patterns:
        value = re.sub(pattern, '', value)
    return value


def _tool_sources_from_text(raw: str) -> list[Mapping[str, Any]]:
    sources: list[Mapping[str, Any]] = []
    for raw_item in re.findall(r'<tool_result>(.*?)</tool_result>', raw, flags=re.S):
        try:
            payload = json.loads(raw_item)
        except json.JSONDecodeError:
            continue
        result = payload.get('result') if isinstance(payload, Mapping) else None
        nested = result.get('result') if isinstance(result, Mapping) else None
        for key in ('items', 'sources', 'contexts'):
            value = nested.get(key) if isinstance(nested, Mapping) else None
            if isinstance(value, list):
                sources.extend(item for item in value if isinstance(item, Mapping))
    return sources


def _merge_tool_sources(parsed: dict[str, Any], raw: str) -> None:
    sources = _tool_sources_from_text(raw)
    if not sources:
        return
    parsed['sources'] = _unique_sources([*parsed.get('sources', []), *sources])
    doc_ids, chunk_ids = _source_ids(sources)
    parsed['doc_ids'] = list(dict.fromkeys([*parsed.get('doc_ids', []), *doc_ids]))
    parsed['chunk_ids'] = list(dict.fromkeys([*parsed.get('chunk_ids', []), *chunk_ids]))
    parsed['contexts'] = list(dict.fromkeys([
        *(_source_text(item) for item in as_list(parsed.get('contexts'))),
        *(_source_text(item) for item in sources),
    ]))


def _source_ids(items: Any) -> tuple[list[str], list[str]]:
    doc_ids, chunk_ids = [], []
    for item in as_list(items):
        if isinstance(item, Mapping):
            doc = first_text(item, 'doc_id', 'document_id', 'file_id', 'docid')
            chunk = first_text(item, 'chunk_id', 'segment_id', 'segement_id', 'node_id', 'uid', 'source_unit_ref')
            if doc:
                doc_ids.append(doc)
            if chunk:
                chunk_ids.append(chunk)
    return list(dict.fromkeys(doc_ids)), list(dict.fromkeys(chunk_ids))


def _source_text(item: Any) -> str:
    if isinstance(item, Mapping):
        return text(item.get('context') or item.get('content') or item.get('text'))
    return text(item)


def _unique_sources(items: Any) -> list[Mapping[str, Any]]:
    unique: dict[str, Mapping[str, Any]] = {}
    for item in as_list(items):
        if not isinstance(item, Mapping):
            continue
        key = first_text(item, 'uid', 'chunk_id', 'segment_id', 'segement_id',
                         'node_id', 'doc_id', 'document_id', 'file_id', 'docid', 'ref') or stable_text(item)
        unique.setdefault(key, item)
    return list(unique.values())

import json
import os
import re
import threading
import time
import urllib.parse
import urllib.request
from dataclasses import asdict, is_dataclass
from typing import Any
from uuid import uuid4

from ...artifacts import ArtifactDraft, ArtifactRef
from ..analysis.utils import typed_payload
from ..dataset.utils import progress, validate_case_id
from ... import validate_id
from ...runtime import AdapterCall, AdapterCallError, OperationContext, OperationOutput

KB_CHAT_TOOLS = ['kb']
NON_KB_CHAT_TOOLS = [
    'temp_kb', 'calculator', 'wikipedia', 'web_search', 'academic_search', 'url_fetch',
    'multimodal', 'vocab_learn', 'memory_editor', 'skill_editor', 'feishu',
]
SOURCE_KEY_FIELDS = ('index', 'segement_id', 'segment_id', 'docid', 'document_id', 'uid', 'ref')
TOOL_FRAME_RE = re.compile(r'<(?P<tag>tp|trp|tool_call|tool_result)(?:\s[^>]*)?>.*?</(?P=tag)>', re.S)


class RagAnswerOperation:
    def __init__(self, model_config: dict[str, Any] | None = None,
                 user_context: dict[str, str] | None = None):
        self.model_config = model_config or {}
        self.user_context = user_context or {}

    def execute(self, ctx: OperationContext) -> OperationOutput:
        dataset_ref = ArtifactRef.parse(str(ctx.params.get('eval_dataset_ref') or ''))
        case_id = validate_case_id(str(ctx.params.get('case_id') or ''))
        raw = str(ctx.params.get('candidate_service_ref') or '').strip()
        service_ref = ArtifactRef.parse(raw) if raw else None
        target_url = str(ctx.params.get('target_chat_url') or '').strip()
        dataset_name = str(ctx.params.get('dataset_name') or '').strip()
        if service_ref:
            service = typed_payload(ctx, service_ref, 'CandidateServiceRun')
            if (service.get('healthcheck') or {}).get('status') != 'passed':
                raise ValueError(f'candidate service is not healthy: {service_ref}')
            target_url = str(service.get('service_url') or '').strip()
            dataset_name = str(service.get('dataset_name') or dataset_name).strip()
        if not target_url or not dataset_name or 'require_trace' not in ctx.params:
            raise ValueError('target_chat_url, dataset_name and require_trace are required')
        target_url = _eval_chat_stream_url(target_url)
        case_ref = _case_ref(typed_payload(ctx, dataset_ref, 'EvalDataset'), case_id)
        case = typed_payload(ctx, case_ref, 'DatasetCase')
        if str(case.get('id') or '') != case_id:
            raise ValueError(f'{case_ref} payload id mismatch: {case.get("id")} != {case_id}')
        question, require_trace = str(case.get('question') or '').strip(), ctx.params.get('require_trace')
        if not question or not isinstance(require_trace, bool):
            raise ValueError('case question and boolean require_trace are required')
        session_id = uuid4().hex
        chat_payload = {
            'query': question, 'history': [], 'trace': require_trace, 'session_id': session_id,
            'dataset': dataset_name, 'filters': {'kb_id': [dataset_name]}, 'reasoning': False,
            'available_tools': KB_CHAT_TOOLS, 'disabled_tools': NON_KB_CHAT_TOOLS, 'use_memory': False,
        }
        progress(ctx, 'rag_answer', 'running', 'calling LazyMind chat', current_item=case_id)
        call_id = ''
        try:
            def call(req):
                return _call_chat(
                    ctx, req['target_chat_url'],
                    {**req['payload'], 'llm_config': self.model_config or None},
                )
            result = AdapterCall('rag.lazymind.chat', call).run(
                ctx, {'target_chat_url': target_url, 'payload': chat_payload},
                phase='rag_answer', item_ref=case_id)
            response, call_id = result.response, result.record.call_id
            if require_trace and not response.get('trace_id'): raise ValueError('target chat did not return trace_id')
            if not str(response.get('answer') or '').strip(): raise ValueError('target chat returned empty answer')
        except AdapterCallError as exc:
            response, call_id = self._failed(chat_payload, exc.record.error or {}, exc.record.call_id)
        except ValueError as exc:
            response, call_id = self._failed(chat_payload, {'type': exc.__class__.__name__, 'message': str(exc)},
                                             call_id)
        answer = {'case_id': case_id, 'eval_dataset_ref': str(dataset_ref), 'case_ref': str(case_ref),
                  'session_id': session_id, 'question': question, 'answer': str(response.get('answer') or ''),
                  'status': 'failed' if response.get('chat_error') else 'ok', 'chat_error': response.get('chat_error'),
                  'contexts': response.get('contexts') or [], 'doc_ids': response.get('doc_ids') or [],
                  'chunk_ids': response.get('chunk_ids') or [], 'trace_id': str(response.get('trace_id') or ''),
                  'evidence_status': 'no_evidence',
                  'kb_errors': response.get('kb_errors') or [], 'trace_label': f'{ctx.operation_run_id}:{case_id}',
                  'target': {'target_chat_url': target_url, 'dataset_name': dataset_name,
                             'filters': _target_filters(chat_payload), 'require_trace': require_trace},
                  'source_message_id': str(ctx.params.get('source_message_id') or '')}
        trace: Any = {}
        if str(answer.get('trace_id') or ''):
            try:
                from lazyllm.tracing.consume import get_single_trace
                trace = get_single_trace(str(answer['trace_id']))
            except Exception:
                trace = {}
            trace = asdict(trace) if is_dataclass(trace) else trace if isinstance(trace, dict) else {}
        if trace and not (answer['contexts'] or answer['doc_ids'] or answer['chunk_ids']):
            trace_sources = _trace_sources(trace)
            answer['contexts'] = _pluck(trace_sources, ('context', 'content', 'text'))
            answer['doc_ids'] = _pluck(trace_sources, ('doc_id', 'document_id', 'file_id', 'docid'))
            answer['chunk_ids'] = _pluck(trace_sources, ('chunk_id', 'segment_id', 'segement_id', 'node_id', 'uid'))
        if not trace:
            sources = [{'text': text, 'doc_id': doc_id, 'chunk_id': chunk_id} for text, doc_id, chunk_id in
                       zip(answer.get('contexts') or [], answer.get('doc_ids') or [], answer.get('chunk_ids') or [])]
            raw_data = {'input': {'question': answer.get('question'), 'target': answer.get('target')},
                        'output': {'answer': answer.get('answer'), 'sources': sources,
                                   'kb_errors': response.get('kb_errors') or []}}
            trace = {'trace_id': answer.get('trace_id'),
                     'execution_tree': {'step_id': 'chat', 'node_id': 'chat', 'name': 'run_chat_pipeline',
                                        'node_type': 'callable', 'status': 'ok', 'raw_data': raw_data, 'children': []}}
        evidence = answer.get('contexts') or answer.get('doc_ids') or answer.get('chunk_ids')
        answer['evidence_status'] = 'found' if evidence else 'no_evidence'
        progress(ctx, 'rag_answer', 'success', 'rag answer generated', current_item=case_id,
                 detail={'call_id': call_id, 'trace_id': answer['trace_id'], 'chat_error': response.get('chat_error')})
        refs = [dataset_ref, case_ref] + ([service_ref] if service_ref else [])
        output_id = validate_id(str(ctx.params.get('output_id') or f'rag_answer_{case_id}'), 'output_id')
        drafts = [ArtifactDraft(output_id, 'RagAnswer', answer, ctx.operation_run_id, input_refs=refs)]
        if trace: drafts.append(
            ArtifactDraft(f"trace_{answer['trace_id']}", 'Trace', trace, ctx.operation_run_id, input_refs=refs))
        return OperationOutput(drafts)

    def _failed(self, payload, error, call_id) -> tuple[dict[str, Any], str]:
        error_type, message = str(error.get('type') or 'ChatError'), str(error.get('message') or 'chat call failed')
        return {'answer': f'RAG call failed: {error_type}: {message}', 'contexts': [], 'doc_ids': [], 'chunk_ids': [],
                'trace_id': str(payload.get('session_id') or ''), 'kb_errors': [f'{error_type}: {message}'],
                'chat_error': {'type': error_type, 'message': message, 'call_id': call_id}}, call_id


def _case_ref(dataset: dict[str, Any], case_id: str) -> ArtifactRef:
    case_ids, case_refs = list(dataset.get('case_ids') or []), list(dataset.get('case_refs') or [])
    if len(case_ids) != len(case_refs) or case_id not in case_ids:
        raise ValueError(f'case_id not found in EvalDataset: {case_id}')
    return ArtifactRef.parse(str(case_refs[case_ids.index(case_id)]))


def _is_core_chat_url(url: str) -> bool:
    return urllib.parse.urlparse(url).path in {'/conversations:chat', '/api/core/conversations:chat'}


def _eval_chat_stream_url(target_url: str) -> str:
    if _is_core_chat_url(target_url):
        target_url = os.getenv('LAZYMIND_EVO_CHAT_STREAM_URL') or 'http://chat:8046/api/chat/stream'
    if target_url.endswith('/api/chat'):
        target_url = target_url.rstrip('/') + '/stream'
    if not target_url.endswith('/api/chat/stream'):
        raise ValueError('target_chat_url must be a core chat or fixed /api/chat/stream endpoint')
    return target_url


def _target_filters(payload: dict[str, Any]) -> dict[str, Any]:
    filters = payload.get('filters')
    if isinstance(filters, dict): return filters
    config = ((payload.get('conversation') or {}).get('search_config') or {})
    return {'kb_id': [item['id'] for item in config.get('dataset_list') or []
                      if isinstance(item, dict) and item.get('id')]}


def _headers(user_context: dict[str, str] | None = None) -> dict[str, str]:
    out = {'Content-Type': 'application/json', 'Accept': 'text/event-stream'}
    for source, target in {'authorization': 'Authorization', 'x-user-id': 'X-User-Id',
                           'x-user-name': 'X-User-Name', 'x-request-id': 'X-Request-Id'}.items():
        if user_context and user_context.get(source):
            out[target] = user_context[source]
    return out


def _call_core_chat(ctx: OperationContext, target_url: str, payload: dict[str, Any],
                    user_context: dict[str, str] | None = None, timeout_s: float = 300) -> dict:
    encoded = json.dumps({k: v for k, v in payload.items() if v is not None}, ensure_ascii=False).encode('utf-8')
    req = urllib.request.Request(target_url, data=encoded, method='POST', headers=_headers(user_context))
    text, sources, cancelled, holder = [], [], threading.Event(), {}

    def cancel() -> None:
        cancelled.set()
        if holder.get('response') is not None: holder['response'].close()

    ctx.register_cancel_callback(cancel)
    with urllib.request.build_opener(urllib.request.ProxyHandler({})).open(req, timeout=timeout_s) as response:
        holder['response'] = response
        deadline = time.time() + timeout_s
        progress(ctx, 'rag_answer', 'running', 'reading LazyMind core chat stream')
        for raw_line in response:
            if cancelled.is_set(): raise RuntimeError('core chat call cancelled')
            if time.time() > deadline: raise TimeoutError(f'core chat stream exceeded {timeout_s}s')
            line = raw_line.decode('utf-8', errors='replace').strip()
            if line.startswith('data:'): line = line[5:].strip()
            if line == '[DONE]': break
            body = json.loads(line) if line else {}
            if not isinstance(body, dict): continue
            result = body.get('result') if isinstance(body.get('result'), dict) else {}
            finish = str(result.get('finish_reason') or '')
            if finish == 'FINISH_REASON_UNKNOWN':
                raise RuntimeError(result or body)
            if isinstance(result.get('delta'), str): text.append(result['delta'])
            if isinstance(result.get('sources'), list): sources = result['sources']
    return _chat_response(''.join(text), sources, str(payload.get('session_id') or ''))


def _call_chat(ctx: OperationContext, target_url: str, payload: dict[str, Any], timeout_s: float = 300) -> dict:
    encoded = json.dumps({k: v for k, v in payload.items() if v is not None}, ensure_ascii=False).encode('utf-8')
    req = urllib.request.Request(target_url, data=encoded, method='POST',
                                 headers=_headers())
    text, sources, trace_id, cancelled, holder = [], [], '', threading.Event(), {}

    def cancel() -> None:
        cancelled.set()
        if holder.get('response') is not None: holder['response'].close()

    ctx.register_cancel_callback(cancel)
    with urllib.request.build_opener(urllib.request.ProxyHandler({})).open(req, timeout=timeout_s) as response:
        holder['response'] = response
        deadline = time.time() + timeout_s
        progress(ctx, 'rag_answer', 'running', 'reading LazyMind chat stream')
        for raw_line in response:
            if cancelled.is_set(): raise RuntimeError('chat call cancelled')
            if time.time() > deadline: raise TimeoutError(f'chat stream exceeded {timeout_s}s')
            line = raw_line.decode('utf-8', errors='replace').strip()
            if line.startswith('data:'): line = line[5:].strip()
            if line == '[DONE]': break
            body = json.loads(line) if line else {}
            if not isinstance(body, dict): continue
            data = body.get('data') if isinstance(body.get('data'), dict) else {}
            if body.get('code') not in (None, 0, 200) or data.get('status') == 'FAILED':
                raise RuntimeError(body.get('msg') or data or body)
            if isinstance(data.get('text'), str): text.append(data['text'])
            if isinstance(data.get('sources'), list): sources.extend(data['sources'])
            if isinstance(data.get('trace_id'), str): trace_id = data['trace_id']
    return _chat_response(''.join(text), sources, trace_id or str(payload.get('session_id') or ''))


def _chat_response(raw_answer: str, sources: list[Any], trace_id: str) -> dict:
    tool_sources, kb_errors = [], []
    for raw in re.findall(r'<tool_result>(.*?)</tool_result>', raw_answer, flags=re.S):
        try:
            result = json.loads(raw).get('result')
        except json.JSONDecodeError: continue
        if isinstance(result, dict) and result.get('success') is False:
            kb_errors.append(str(result.get('reason') or result.get('error') or 'kb_search failed'))
        res = result.get('result') if isinstance(result, dict) else None
        items = res.get('items') if isinstance(res, dict) else None
        tool_sources.extend(item for item in items or [] if isinstance(item, dict))
    unique, seen = [], set()
    for item in sources or tool_sources:
        if not isinstance(item, dict): continue
        key = str(next((item.get(name) for name in SOURCE_KEY_FIELDS if item.get(name)), id(item)))
        if key not in seen:
            seen.add(key)
            unique.append(item)
    # Tool frames carry full KB dumps (megabytes); evidence is already mined into sources/kb_errors
    # and the consumer trace, so the stored answer keeps only user-visible text.
    answer = re.sub(r'\n{3,}', '\n\n', TOOL_FRAME_RE.sub('', raw_answer)).strip()
    return {'answer': answer, 'contexts': _pluck(unique, ('context', 'content', 'text')),
            'doc_ids': _pluck(unique, ('doc_id', 'document_id', 'file_id', 'docid')),
            'chunk_ids': _pluck(unique, ('chunk_id', 'segment_id', 'segement_id', 'node_id', 'uid')),
            'trace_id': trace_id, 'kb_errors': kb_errors}


def _trace_sources(trace: dict[str, Any]) -> list[dict[str, Any]]:
    out: list[dict[str, Any]] = []

    def walk(value: Any) -> None:
        if isinstance(value, dict):
            items = value.get('items')
            if isinstance(items, list):
                for item in items:
                    if isinstance(item, dict):
                        walk(item)
            source = _source_item(value)
            if source:
                out.append(source)
            for item in value.values():
                walk(item)
        elif isinstance(value, list):
            for item in value:
                walk(item)

    walk(trace)
    unique, seen = [], set()
    for item in out:
        key = str(next((item.get(name) for name in SOURCE_KEY_FIELDS if item.get(name)), id(item)))
        if key not in seen:
            seen.add(key)
            unique.append(item)
    return unique


def _source_item(value: dict[str, Any]) -> dict[str, Any]:
    meta = value.get('global_metadata') if isinstance(value.get('global_metadata'), dict) else {}
    doc_id = value.get('doc_id') or value.get('docid') or value.get('document_id') \
        or meta.get('doc_id') or meta.get('docid') or meta.get('document_id') or meta.get('core_document_id')
    text = value.get('context') or value.get('content') or value.get('text')
    if not doc_id and not (text and (value.get('ref') or value.get('citation_index'))):
        return {}
    return {
        **value,
        'doc_id': doc_id or '',
        'chunk_id': value.get('chunk_id') or value.get('segment_id') or value.get('segement_id')
        or value.get('uid') or value.get('ref') or '',
        'text': text or '',
    }


def _pluck(items: Any, keys: tuple[str, ...]) -> list[Any]:
    out = []
    for item in items if isinstance(items, list) else []:
        value = next((item[key] for key in keys if isinstance(item, dict) and item.get(key) is not None), None)
        if value is not None: out.append(value)
    return out

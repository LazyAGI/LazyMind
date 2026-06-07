from __future__ import annotations

import json
import re
import threading
import time
import urllib.request
from dataclasses import asdict, is_dataclass
from typing import Any
from uuid import uuid4

from ...artifacts import ArtifactDraft, ArtifactRef
from ..dataset.utils import validate_case_id
from ...runtime import AdapterCall, AdapterCallError, OperationContext, OperationOutput

KB_CHAT_TOOLS = ['kb']
SOURCE_KEY_FIELDS = ('index', 'segement_id', 'document_id', 'uid')


class RagAnswerOperation:
    def __init__(self, model_config: dict[str, Any] | None = None):
        self.model_config = model_config or {}

    def execute(self, ctx: OperationContext) -> OperationOutput:
        dataset_ref = ArtifactRef.parse(str(ctx.params.get('eval_dataset_ref') or ''))
        case_id, service_ref = validate_case_id(str(ctx.params.get('case_id') or '')), _service_ref(ctx)
        target_url, dataset_name = _target(ctx, service_ref)
        dataset = _typed(ctx, dataset_ref, 'EvalDataset')
        case_ref = _case_ref(dataset, case_id)
        case = _typed(ctx, case_ref, 'DatasetCase')
        if str(case.get('id') or '') != case_id:
            raise ValueError(f'{case_ref} payload id mismatch: {case.get("id")} != {case_id}')
        question, require_trace = str(case.get('question') or '').strip(), ctx.params.get('require_trace')
        if not question or not isinstance(require_trace, bool):
            raise ValueError('case question and boolean require_trace are required')
        session_id = uuid4().hex
        chat_payload = _payload(question, session_id, dataset_name, require_trace)
        ctx.report_progress(phase='rag_answer', status='running', message='calling LazyMind chat',
                            current_item=case_id)
        call_id = ''
        try:
            result = AdapterCall('rag.lazymind.chat', lambda req: _call_chat(
                ctx, req['target_chat_url'], {**req['payload'], 'llm_config': self.model_config or None},
            )).run(ctx, {'target_chat_url': target_url, 'payload': chat_payload}, phase='rag_answer', item_ref=case_id)
            response, call_id = result.response, result.record.call_id
            _validate_response(response, require_trace)
        except AdapterCallError as exc:
            response, call_id = _failed_response(chat_payload, exc.record.error or {}, exc.record.call_id)
        except ValueError as exc:
            response, call_id = _failed_response(
                chat_payload, {'type': exc.__class__.__name__, 'message': str(exc)}, call_id
            )
        answer = {
            'case_id': case_id, 'eval_dataset_ref': str(dataset_ref), 'case_ref': str(case_ref),
            'session_id': session_id, 'question': question, 'answer': str(response.get('answer') or ''),
            'contexts': response.get('contexts') or [], 'doc_ids': response.get('doc_ids') or [],
            'chunk_ids': response.get('chunk_ids') or [], 'trace_id': str(response.get('trace_id') or ''),
            'evidence_status': 'found' if _has_evidence(response) else 'no_evidence',
            'kb_errors': response.get('kb_errors') or [],
            'trace_label': f'{ctx.operation_run_id}:{case_id}',
            'target': {'target_chat_url': target_url, 'dataset_name': dataset_name,
                       'filters': chat_payload['filters'], 'require_trace': require_trace},
            'source_message_id': str(ctx.params.get('source_message_id') or ''),
        }
        trace = _trace_payload(answer, response)
        ctx.report_progress(phase='rag_answer', status='success', message='rag answer generated',
                            current_item=case_id,
                            detail={'call_id': call_id, 'trace_id': answer['trace_id'],
                                    'chat_error': response.get('chat_error')})
        refs = [dataset_ref, case_ref] + ([service_ref] if service_ref else [])
        drafts = [ArtifactDraft(f'rag_answer_{case_id}', 'RagAnswer', answer, ctx.operation_run_id, input_refs=refs)]
        if trace:
            drafts.append(
                ArtifactDraft(f"trace_{answer['trace_id']}", 'Trace', trace, ctx.operation_run_id, input_refs=refs)
            )
        return OperationOutput(drafts)


def _typed(ctx: OperationContext, ref: ArtifactRef, schema: str) -> dict[str, Any]:
    if ctx.artifact_graph.schema_name(ref) != schema:
        raise ValueError(f'artifact is not {schema}: {ref}')
    return ctx.artifact_graph.get(ref)


def _case_ref(dataset: dict[str, Any], case_id: str) -> ArtifactRef:
    case_ids, case_refs = list(dataset.get('case_ids') or []), list(dataset.get('case_refs') or [])
    if len(case_ids) != len(case_refs) or case_id not in case_ids:
        raise ValueError(f'case_id not found in EvalDataset: {case_id}')
    return ArtifactRef.parse(str(case_refs[case_ids.index(case_id)]))


def _service_ref(ctx: OperationContext) -> ArtifactRef | None:
    raw = str(ctx.params.get('candidate_service_ref') or '').strip()
    if not raw:
        return None
    ref = ArtifactRef.parse(raw)
    _typed(ctx, ref, 'CandidateServiceRun')
    return ref


def _target(ctx: OperationContext, service_ref: ArtifactRef | None) -> tuple[str, str]:
    target_url, dataset_name = str(ctx.params.get('target_chat_url') or '').strip(), str(
        ctx.params.get('dataset_name') or ''
    ).strip()
    if service_ref:
        service = ctx.artifact_graph.get(service_ref)
        if (service.get('healthcheck') or {}).get('status') != 'passed':
            raise ValueError(f'candidate service is not healthy: {service_ref}')
        target_url = str(service.get('service_url') or '').strip()
        dataset_name = str(service.get('dataset_name') or dataset_name).strip()
    if not target_url or not dataset_name or 'require_trace' not in ctx.params:
        raise ValueError('target_chat_url, dataset_name and require_trace are required')
    if not target_url.endswith('/api/chat/stream'):
        raise ValueError('target_chat_url must be the fixed /api/chat/stream endpoint')
    return target_url, dataset_name


def _payload(question: str, session_id: str, dataset_name: str, require_trace: bool) -> dict[str, Any]:
    return {'query': question, 'history': [], 'trace': require_trace, 'session_id': session_id,
            'dataset': dataset_name, 'filters': {'kb_id': [dataset_name]}, 'reasoning': False,
            'available_tools': KB_CHAT_TOOLS}


def _call_chat(
    ctx: OperationContext, target_url: str, payload: dict[str, Any], timeout_s: float = 180
) -> dict[str, Any]:
    req = urllib.request.Request(
        target_url,
        data=json.dumps({k: v for k, v in payload.items() if v is not None}, ensure_ascii=False).encode('utf-8'),
        headers={'Content-Type': 'application/json', 'Accept': 'text/event-stream'},
        method='POST',
    )
    text, sources, trace_id, cancelled, holder = [], [], '', threading.Event(), {}

    def cancel() -> None:
        cancelled.set()
        if holder.get('response') is not None:
            holder['response'].close()

    ctx.register_cancel_callback(cancel)
    with urllib.request.build_opener(urllib.request.ProxyHandler({})).open(req, timeout=timeout_s) as response:
        holder['response'] = response
        deadline = time.time() + timeout_s
        ctx.report_progress(phase='rag_answer', status='running', message='reading LazyMind chat stream')
        for raw_line in response:
            if cancelled.is_set():
                raise RuntimeError('chat call cancelled')
            if time.time() > deadline:
                raise TimeoutError(f'chat stream exceeded {timeout_s}s')
            body = _stream(raw_line.decode('utf-8', errors='replace').strip())
            if body is None:
                break
            if not isinstance(body, dict):
                continue
            data = body.get('data') if isinstance(body.get('data'), dict) else {}
            if body.get('code') not in (None, 0, 200) or data.get('status') == 'FAILED':
                raise RuntimeError(body.get('msg') or data or body)
            text.extend([data['text']] if isinstance(data.get('text'), str) else [])
            sources.extend(data['sources'] if isinstance(data.get('sources'), list) else [])
            trace_id = data['trace_id'] if isinstance(data.get('trace_id'), str) else trace_id
    answer = ''.join(text)
    tool_sources, kb_errors = _tool_evidence(answer)
    sources = _unique_sources(sources or tool_sources)
    return {'answer': answer, 'contexts': _pluck(sources, ('context', 'content', 'text')),
            'doc_ids': _pluck(sources, ('doc_id', 'document_id', 'file_id', 'docid')),
            'chunk_ids': _pluck(sources, ('chunk_id', 'segment_id', 'segement_id', 'node_id', 'uid')),
            'trace_id': trace_id or str(payload.get('session_id') or ''), 'kb_errors': kb_errors}


def _stream(line: str) -> Any:
    if not line:
        return {}
    line = line[5:].strip() if line.startswith('data:') else line
    return None if line == '[DONE]' else json.loads(line)


def _pluck(items: Any, keys: tuple[str, ...]) -> list[Any]:
    out = []
    for item in items if isinstance(items, list) else []:
        value = next((item[key] for key in keys if isinstance(item, dict) and item.get(key) is not None), None)
        out.extend([value] if value is not None else [])
    return out


def _unique_sources(items: list[Any]) -> list[Any]:
    out, seen = [], set()
    for item in items:
        if not isinstance(item, dict):
            continue
        key = next((item.get(name) for name in SOURCE_KEY_FIELDS if item.get(name)), id(item))
        key = str(key)
        if key not in seen:
            seen.add(key)
            out.append(item)
    return out


def _tool_evidence(text: str) -> tuple[list[Any], list[str]]:
    sources, errors = [], []
    for raw in re.findall(r'<tool_result>(.*?)</tool_result>', text, flags=re.S):
        try:
            result = json.loads(raw).get('result')
        except json.JSONDecodeError:
            continue
        errors.extend([str(result.get('reason') or result.get('error') or 'kb_search failed')]
                      if isinstance(result, dict) and result.get('success') is False else [])
        payload = result.get('result') if isinstance(result, dict) else None
        items = payload.get('items') if isinstance(payload, dict) else None
        sources.extend(item for item in items or [] if isinstance(item, dict))
    return sources, errors


def _validate_response(response: dict[str, Any], require_trace: bool) -> None:
    if require_trace and not response.get('trace_id'):
        raise ValueError('target chat did not return trace_id')
    if not str(response.get('answer') or '').strip():
        raise ValueError('target chat returned empty answer')


def _failed_response(payload: dict[str, Any], error: dict[str, Any], call_id: str) -> tuple[dict[str, Any], str]:
    error_type = str(error.get('type') or 'ChatError')
    message = str(error.get('message') or 'chat call failed')
    reason = f'{error_type}: {message}'
    return {
        'answer': f'RAG call failed: {reason}',
        'contexts': [],
        'doc_ids': [],
        'chunk_ids': [],
        'trace_id': str(payload.get('session_id') or ''),
        'kb_errors': [reason],
        'chat_error': {'type': error_type, 'message': message, 'call_id': call_id},
    }, call_id


def _has_evidence(response: dict[str, Any]) -> bool:
    return bool(response.get('contexts') or response.get('doc_ids') or response.get('chunk_ids'))


def _trace_payload(answer: dict[str, Any], response: dict[str, Any]) -> dict[str, Any]:
    trace = _consumer_trace(str(answer.get('trace_id') or ''))
    if trace:
        return trace
    return {
        'trace_id': answer.get('trace_id'),
        'execution_tree': {
            'step_id': 'chat',
            'node_id': 'chat',
            'name': 'run_chat_pipeline',
            'node_type': 'callable',
            'status': 'ok',
            'raw_data': {
                'input': {'question': answer.get('question'), 'target': answer.get('target')},
                'output': {
                    'answer': answer.get('answer'),
                    'sources': [
                        {'text': text, 'doc_id': doc_id, 'chunk_id': chunk_id}
                        for text, doc_id, chunk_id in zip(
                            answer.get('contexts') or [],
                            answer.get('doc_ids') or [],
                            answer.get('chunk_ids') or [],
                        )
                    ],
                    'kb_errors': response.get('kb_errors') or [],
                },
            },
            'children': [],
        },
    }


def _consumer_trace(trace_id: str) -> dict[str, Any]:
    if not trace_id:
        return {}
    try:
        from lazyllm.tracing.consume import get_single_trace
    except Exception:
        return {}
    try:
        trace = get_single_trace(trace_id)
    except Exception:
        return {}
    return asdict(trace) if is_dataclass(trace) else trace if isinstance(trace, dict) else {}

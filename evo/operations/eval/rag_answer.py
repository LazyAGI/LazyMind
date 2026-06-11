import json
import re
import threading
import time
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
SOURCE_KEY_FIELDS = ('index', 'segement_id', 'document_id', 'uid')
TOOL_FRAME_RE = re.compile(r'<(?P<tag>tp|trp|tool_call|tool_result)(?:\s[^>]*)?>.*?</(?P=tag)>', re.S)


class RagAnswerOperation:
    def __init__(self, model_config: dict[str, Any] | None = None):
        self.model_config = model_config or {}

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
        if not target_url.endswith('/api/chat/stream'):
            raise ValueError('target_chat_url must be the fixed /api/chat/stream endpoint')
        case_ref = _case_ref(typed_payload(ctx, dataset_ref, 'EvalDataset'), case_id)
        case = typed_payload(ctx, case_ref, 'DatasetCase')
        if str(case.get('id') or '') != case_id:
            raise ValueError(f'{case_ref} payload id mismatch: {case.get("id")} != {case_id}')
        question, require_trace = str(case.get('question') or '').strip(), ctx.params.get('require_trace')
        if not question or not isinstance(require_trace, bool):
            raise ValueError('case question and boolean require_trace are required')
        session_id = uuid4().hex
        chat_payload = {'query': question, 'history': [], 'trace': require_trace, 'session_id': session_id,
                        'dataset': dataset_name, 'filters': {'kb_id': [dataset_name]}, 'reasoning': False,
                        'available_tools': KB_CHAT_TOOLS}
        progress(ctx, 'rag_answer', 'running', 'calling LazyMind chat', current_item=case_id)
        call_id = ''
        try:
            result = AdapterCall('rag.lazymind.chat', lambda req: _call_chat(
                ctx, req['target_chat_url'], {**req['payload'], 'llm_config': self.model_config or None},
            )).run(ctx, {'target_chat_url': target_url, 'payload': chat_payload}, phase='rag_answer', item_ref=case_id)
            response, call_id = result.response, result.record.call_id
            if require_trace and not response.get('trace_id'): raise ValueError('target chat did not return trace_id')
            if not str(response.get('answer') or '').strip(): raise ValueError('target chat returned empty answer')
        except AdapterCallError as exc:
            response, call_id = self._failed(chat_payload, exc.record.error or {}, exc.record.call_id)
        except ValueError as exc:
            response, call_id = self._failed(chat_payload, {'type': exc.__class__.__name__, 'message': str(exc)},
                                             call_id)
        evidence = response.get('contexts') or response.get('doc_ids') or response.get('chunk_ids')
        answer = {'case_id': case_id, 'eval_dataset_ref': str(dataset_ref), 'case_ref': str(case_ref),
                  'session_id': session_id, 'question': question, 'answer': str(response.get('answer') or ''),
                  'status': 'failed' if response.get('chat_error') else 'ok', 'chat_error': response.get('chat_error'),
                  'contexts': response.get('contexts') or [], 'doc_ids': response.get('doc_ids') or [],
                  'chunk_ids': response.get('chunk_ids') or [], 'trace_id': str(response.get('trace_id') or ''),
                  'evidence_status': 'found' if evidence else 'no_evidence',
                  'kb_errors': response.get('kb_errors') or [], 'trace_label': f'{ctx.operation_run_id}:{case_id}',
                  'target': {'target_chat_url': target_url, 'dataset_name': dataset_name,
                             'filters': chat_payload['filters'], 'require_trace': require_trace},
                  'source_message_id': str(ctx.params.get('source_message_id') or '')}
        trace: Any = {}
        if str(answer.get('trace_id') or ''):
            try:
                from lazyllm.tracing.consume import get_single_trace
                trace = get_single_trace(str(answer['trace_id']))
            except Exception:
                trace = {}
            trace = asdict(trace) if is_dataclass(trace) else trace if isinstance(trace, dict) else {}
        if not trace:
            sources = [{'text': text, 'doc_id': doc_id, 'chunk_id': chunk_id} for text, doc_id, chunk_id in
                       zip(answer.get('contexts') or [], answer.get('doc_ids') or [], answer.get('chunk_ids') or [])]
            raw_data = {'input': {'question': answer.get('question'), 'target': answer.get('target')},
                        'output': {'answer': answer.get('answer'), 'sources': sources,
                                   'kb_errors': response.get('kb_errors') or []}}
            trace = {'trace_id': answer.get('trace_id'),
                     'execution_tree': {'step_id': 'chat', 'node_id': 'chat', 'name': 'run_chat_pipeline',
                                        'node_type': 'callable', 'status': 'ok', 'raw_data': raw_data, 'children': []}}
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


def _call_chat(ctx: OperationContext, target_url: str, payload: dict[str, Any], timeout_s: float = 300) -> dict:
    encoded = json.dumps({k: v for k, v in payload.items() if v is not None}, ensure_ascii=False).encode('utf-8')
    req = urllib.request.Request(target_url, data=encoded, method='POST',
                                 headers={'Content-Type': 'application/json', 'Accept': 'text/event-stream'})
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
    raw_answer = ''.join(text)
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
            'trace_id': trace_id or str(payload.get('session_id') or ''), 'kb_errors': kb_errors}


def _pluck(items: Any, keys: tuple[str, ...]) -> list[Any]:
    out = []
    for item in items if isinstance(items, list) else []:
        value = next((item[key] for key in keys if isinstance(item, dict) and item.get(key) is not None), None)
        if value is not None: out.append(value)
    return out

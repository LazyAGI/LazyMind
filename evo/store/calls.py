from __future__ import annotations

import json
from dataclasses import asdict, replace
from typing import Any
from uuid import uuid4

from ..artifacts import ArtifactDraft, ArtifactRef
from ..runtime import CallRecord
from ..runtime.models import CallStatus
from .models import Event
from .store import EvoStore

AUDITED_ADAPTER_TYPES = {
    'llm.intent_parser', 'llm.generate_dataset_case', 'llm.judge_answer', 'llm.fine_classify_case',
    'llm.repair_judge', 'rag.lazymind.chat', 'rag.candidate.chat',
}
AUDITED_ADAPTER_PREFIXES = ('llm.prepare_dataset_case.',)
AUDIT_PAYLOAD_MAX_BYTES = 64 * 1024
AUDIT_RETENTION_DAYS = 30
AUDIT_SAMPLING = 'forced_for_audited_adapters'
SENSITIVE_KEY_PARTS = ('api_key', 'apikey', 'authorization', 'cookie', 'password', 'secret', 'token')


class StoreCallRecorder:
    def __init__(self, store: EvoStore, run_id: str, operation_run_id: str):
        self.store = store
        self.run_id = run_id
        self.operation_run_id = operation_run_id
        self.records: list[CallRecord] = []

    def record(self, adapter_type: str, request: Any, response: Any = None, *, phase: str = '', item_ref: str = '',
               status: CallStatus = 'succeeded', idempotency_key: str = '', idempotency_scope: str = 'operation',
               error: dict[str, Any] | None = None) -> CallRecord:
        if idempotency_key:
            existing = self.succeeded(idempotency_key, idempotency_scope=idempotency_scope)
            if existing is not None: return self._reuse(existing, phase, item_ref)
        call_id = uuid4().hex
        request_ref = self._payload_ref(adapter_type, call_id, 'request', request, [])
        response_ref = (
            self._payload_ref(adapter_type, call_id, 'response', response, self._payload_input_refs(request_ref))
            if response is not None else ''
        )
        record = CallRecord(
            operation_run_id=self.operation_run_id, adapter_type=adapter_type, request=request, response=response,
            call_id=call_id, phase=phase, item_ref=item_ref, status=status, request_ref=request_ref,
            response_ref=response_ref, idempotency_key=idempotency_key, idempotency_scope=idempotency_scope,
            error=error,
        )
        return self._persist(record)

    def succeeded(self, idempotency_key: str, *, idempotency_scope: str = 'operation') -> CallRecord | None:
        for record in self.records:
            if _matches(record, idempotency_key, idempotency_scope): return record
        stored = (self.store.read_calls(self.run_id) if idempotency_scope == 'run'
                  else self.store.read_calls(self.run_id, self.operation_run_id))
        for record in stored:
            if _matches(record, idempotency_key, idempotency_scope): return record
        return None

    def _reuse(self, existing: CallRecord, phase: str, item_ref: str) -> CallRecord:
        if existing.operation_run_id == self.operation_run_id: return existing
        record = replace(
            existing, operation_run_id=self.operation_run_id, call_id=uuid4().hex, phase=phase or existing.phase,
            item_ref=item_ref or existing.item_ref, reused=True, reused_from_call_id=existing.call_id,
            reused_from_operation_run_id=existing.operation_run_id,
        )
        return self._persist(record)

    def _payload_ref(self, adapter_type: str, call_id: str, kind: str, payload: Any,
                     refs: list[ArtifactRef]) -> str:
        safe_payload, audit = _audit_payload(payload)
        return str(self.store.artifact_graph(self.run_id).commit_artifact(ArtifactDraft(
            f'call_{call_id}_{kind}', 'CallPayload',
            {'call_id': call_id, 'kind': kind, 'payload': safe_payload, 'audit': audit},
            self.operation_run_id, input_refs=refs, role='audit')))

    def _payload_input_refs(self, ref: str) -> list[ArtifactRef]:
        return [ArtifactRef.parse(ref)]

    def _record_ref(self, record: CallRecord) -> str:
        payload = asdict(replace(record, record_ref=f'call_record_{record.call_id}@v1'))
        payload.pop('request', None)
        payload.pop('response', None)
        return str(self.store.artifact_graph(self.run_id).commit_artifact(ArtifactDraft(
            f'call_record_{record.call_id}', 'CallRecord', payload, self.operation_run_id, role='audit',
            input_refs=[ArtifactRef.parse(ref) for ref in (record.request_ref, record.response_ref) if '@v' in ref])))

    def _persist(self, record: CallRecord) -> CallRecord:
        record = self.store.append_call(self.run_id, self.operation_run_id, record, self._record_ref)
        self.records.append(record)
        _append_event(self.store, self.run_id, record)
        _update_operation(self.store, self.run_id, self.operation_run_id, record)
        return record


class CompactStoreCallRecorder(StoreCallRecorder):
    def _payload_ref(self, adapter_type: str, call_id: str, kind: str, payload: Any,
                     refs: list[ArtifactRef]) -> str:
        if _audited_adapter_type(adapter_type):
            return super()._payload_ref(adapter_type, call_id, kind, payload, refs)
        return self._ref(call_id, kind)

    def _payload_input_refs(self, ref: str) -> list[ArtifactRef]:
        return super()._payload_input_refs(ref) if ref and '@v' in ref else []

    def _record_ref(self, record: CallRecord) -> str:
        if _audited_adapter_type(record.adapter_type):
            return super()._record_ref(record)
        return self._ref(record.call_id)

    def _ref(self, call_id: str, kind: str = 'record') -> str:
        log = self.store.relative_to_run(self.run_id, self.store.call_log_path(self.run_id, self.operation_run_id))
        return f'{log}#{kind}:{call_id}'


def _append_event(store: EvoStore, run_id: str, record: CallRecord) -> None:
    store.append_event(Event('adapter.call.reused' if record.reused else 'adapter.call', run_id, {
        'operation_run_id': record.operation_run_id, 'call_id': record.call_id,
        'adapter_type': record.adapter_type, 'phase': record.phase, 'item_ref': record.item_ref,
        'status': record.status, 'record_ref': record.record_ref, 'call_sequence': record.sequence,
        'reused': record.reused, 'reused_from_call_id': record.reused_from_call_id,
        'reused_from_operation_run_id': record.reused_from_operation_run_id}))


def _update_operation(store: EvoStore, run_id: str, operation_run_id: str, record: CallRecord) -> None:
    try:
        operation = store.read_operation(run_id, operation_run_id)
    except FileNotFoundError:
        operation = {'operation_run_id': operation_run_id, 'status': 'running'}
    summary = operation.setdefault('call_summary', {'total': 0, 'by_adapter': {}})
    summary.pop('by_tool', None)
    summary['total'] = int(summary.get('total', 0)) + 1
    if record.reused: summary['reused'] = int(summary.get('reused', 0)) + 1
    summary['last_call_id'] = record.call_id
    summary['last_record_ref'] = record.record_ref
    by_adapter = summary.setdefault('by_adapter', {})
    by_adapter[record.adapter_type] = int(by_adapter.get(record.adapter_type, 0)) + 1
    operation['call_log_ref'] = store.relative_to_run(run_id, store.call_log_path(run_id, operation_run_id))
    store.write_operation(run_id, operation_run_id, operation)
    from ..projections import rebuild_frontend_state_throttled

    rebuild_frontend_state_throttled(store, run_id)


def _audited_adapter_type(adapter_type: str) -> bool:
    return adapter_type in AUDITED_ADAPTER_TYPES or adapter_type.startswith(AUDITED_ADAPTER_PREFIXES)


def _audit_payload(payload: Any) -> tuple[Any, dict[str, Any]]:
    redacted = _redact(payload)
    encoded = json.dumps(redacted, ensure_ascii=False, sort_keys=True, default=str)
    payload_bytes = len(encoded.encode('utf-8'))
    audit = {'max_payload_bytes': AUDIT_PAYLOAD_MAX_BYTES, 'payload_bytes': payload_bytes,
             'retention_days': AUDIT_RETENTION_DAYS, 'sampling': AUDIT_SAMPLING,
             'truncated': payload_bytes > AUDIT_PAYLOAD_MAX_BYTES}
    if payload_bytes <= AUDIT_PAYLOAD_MAX_BYTES: return redacted, audit
    preview = encoded.encode('utf-8')[:AUDIT_PAYLOAD_MAX_BYTES].decode('utf-8', errors='ignore')
    return {'preview_json': preview}, audit


def _redact(value: Any) -> Any:
    if isinstance(value, dict):
        return {str(key): '[REDACTED]' if _sensitive_key(str(key)) else _redact(item) for key, item in value.items()}
    if isinstance(value, (list, tuple)): return [_redact(item) for item in value]
    if isinstance(value, set): return [_redact(item) for item in sorted(value, key=str)]
    return value


def _sensitive_key(key: str) -> bool:
    lower = key.lower().replace('-', '_')
    return any(part in lower for part in SENSITIVE_KEY_PARTS)


def _matches(record: CallRecord, idempotency_key: str, idempotency_scope: str) -> bool:
    return (record.idempotency_key == idempotency_key and record.idempotency_scope == idempotency_scope
            and record.status == 'succeeded')

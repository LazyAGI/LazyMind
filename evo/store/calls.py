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
            if existing is not None:
                return self._reuse(existing, phase, item_ref)
        call_id = uuid4().hex
        request_ref = self._payload_ref(call_id, 'request', request, [])
        response_ref = (
            self._payload_ref(call_id, 'response', response, self._payload_input_refs(request_ref))
            if response is not None
            else ''
        )
        record = CallRecord(
            operation_run_id=self.operation_run_id,
            adapter_type=adapter_type,
            request=request,
            response=response,
            call_id=call_id,
            phase=phase,
            item_ref=item_ref,
            status=status,
            request_ref=request_ref,
            response_ref=response_ref,
            idempotency_key=idempotency_key,
            idempotency_scope=idempotency_scope,
            error=error,
        )
        return self._persist(replace(record, record_ref=self._record_ref(record)))

    def succeeded(self, idempotency_key: str, *, idempotency_scope: str = 'operation') -> CallRecord | None:
        for record in self.records:
            if _matches(record, idempotency_key, idempotency_scope):
                return record
        for record in self._call_records(idempotency_scope):
            if _matches(record, idempotency_key, idempotency_scope):
                return record
        return None

    def _reuse(self, existing: CallRecord, phase: str, item_ref: str) -> CallRecord:
        if existing.operation_run_id == self.operation_run_id:
            return existing
        record = replace(
            existing,
            operation_run_id=self.operation_run_id,
            call_id=uuid4().hex,
            phase=phase or existing.phase,
            item_ref=item_ref or existing.item_ref,
            reused=True,
            reused_from_call_id=existing.call_id,
            reused_from_operation_run_id=existing.operation_run_id,
        )
        return self._persist(replace(record, record_ref=self._record_ref(record)))

    def _call_records(self, idempotency_scope: str) -> list[CallRecord]:
        path = self.store.call_log_path(self.run_id, self.operation_run_id)
        if not path.exists():
            return []
        records = [CallRecord(**json.loads(line)) for line in path.read_text(encoding='utf-8').splitlines()
                   if line.strip()]
        if idempotency_scope == 'run':
            return records
        return [record for record in records if record.operation_run_id == self.operation_run_id]

    def _commit_payload(self, call_id: str, kind: str, payload: Any, refs: list[ArtifactRef]) -> ArtifactRef:
        return self.store.artifact_graph(self.run_id).commit_artifact(ArtifactDraft(
            f'call_{call_id}_{kind}',
            'CallPayload',
            {'call_id': call_id, 'kind': kind, 'payload': payload},
            self.operation_run_id,
            input_refs=refs,
            role='audit',
        ))

    def _payload_ref(self, call_id: str, kind: str, payload: Any, refs: list[ArtifactRef]) -> str:
        return str(self._commit_payload(call_id, kind, payload, refs))

    def _payload_input_refs(self, ref: str) -> list[ArtifactRef]:
        return [ArtifactRef.parse(ref)]

    def _record_ref(self, record: CallRecord) -> str:
        payload = replace(record, record_ref=_call_record_ref(record.call_id))
        return str(self.store.artifact_graph(self.run_id).commit_artifact(ArtifactDraft(
            f'call_record_{record.call_id}',
            'CallRecord',
            _record_payload(payload),
            self.operation_run_id,
            input_refs=[ArtifactRef.parse(ref) for ref in (record.request_ref, record.response_ref) if ref],
            role='audit',
        )))

    def _persist(self, record: CallRecord) -> CallRecord:
        self.records.append(record)
        _append_call(self.store, self.run_id, self.operation_run_id, record)
        _append_event(self.store, self.run_id, record)
        _update_operation(self.store, self.run_id, self.operation_run_id, record)
        return record


class CompactStoreCallRecorder(StoreCallRecorder):
    def _payload_ref(self, call_id: str, kind: str, payload: Any, refs: list[ArtifactRef]) -> str:
        return self._ref(call_id, kind)

    def _payload_input_refs(self, ref: str) -> list[ArtifactRef]:
        return []

    def _record_ref(self, record: CallRecord) -> str:
        return self._ref(record.call_id)

    def _ref(self, call_id: str, kind: str = 'record') -> str:
        log = self.store.relative_to_run(self.run_id, self.store.call_log_path(self.run_id, self.operation_run_id))
        return f'{log}#{kind}:{call_id}'


def _append_call(store: EvoStore, run_id: str, operation_run_id: str, record: CallRecord) -> None:
    path = store.call_log_path(run_id, operation_run_id)
    path.parent.mkdir(parents=True, exist_ok=True)
    with path.open('a', encoding='utf-8') as handle:
        handle.write(json.dumps(asdict(record), ensure_ascii=False, sort_keys=True) + '\n')


def _append_event(store: EvoStore, run_id: str, record: CallRecord) -> None:
    store.append_event(Event(
        'adapter.call.reused' if record.reused else 'adapter.call',
        run_id,
        {
            'operation_run_id': record.operation_run_id,
            'call_id': record.call_id,
            'adapter_type': record.adapter_type,
            'phase': record.phase,
            'item_ref': record.item_ref,
            'status': record.status,
            'record_ref': record.record_ref,
            'reused': record.reused,
            'reused_from_call_id': record.reused_from_call_id,
            'reused_from_operation_run_id': record.reused_from_operation_run_id,
        },
    ))


def _update_operation(store: EvoStore, run_id: str, operation_run_id: str, record: CallRecord) -> None:
    try:
        operation = store.read_operation(run_id, operation_run_id)
    except FileNotFoundError:
        operation = {'operation_run_id': operation_run_id, 'status': 'running'}
    summary = operation.setdefault('call_summary', {'total': 0, 'by_adapter': {}})
    summary.pop('by_tool', None)
    summary['total'] = int(summary.get('total', 0)) + 1
    if record.reused:
        summary['reused'] = int(summary.get('reused', 0)) + 1
    summary['last_call_id'] = record.call_id
    summary['last_record_ref'] = record.record_ref
    by_adapter = summary.setdefault('by_adapter', {})
    by_adapter[record.adapter_type] = int(by_adapter.get(record.adapter_type, 0)) + 1
    operation['call_log_ref'] = store.relative_to_run(run_id, store.call_log_path(run_id, operation_run_id))
    store.write_operation(run_id, operation_run_id, operation)
    _rebuild_frontend_state(store, run_id)


def _record_payload(record: CallRecord) -> dict:
    data = asdict(record)
    data.pop('request', None)
    data.pop('response', None)
    return data


def _call_record_ref(call_id: str) -> str:
    return f'call_record_{call_id}@v1'


def _matches(record: CallRecord, idempotency_key: str, idempotency_scope: str) -> bool:
    return (
        record.idempotency_key == idempotency_key
        and record.idempotency_scope == idempotency_scope
        and record.status == 'succeeded'
    )


def _rebuild_frontend_state(store: EvoStore, run_id: str) -> None:
    from ..projections import rebuild_frontend_state

    rebuild_frontend_state(store, run_id)

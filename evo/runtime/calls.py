from __future__ import annotations

from dataclasses import replace
from typing import Any
from uuid import uuid4

from .models import CallRecord, CallStatus


class InMemoryCallRecorder:
    def __init__(self, operation_run_id: str, all_records: list[CallRecord] | None = None):
        self.operation_run_id = operation_run_id
        self.records: list[CallRecord] = []
        self._all_records = all_records if all_records is not None else self.records

    def record(self, adapter_type: str, request: Any, response: Any = None, *, phase: str = '', item_ref: str = '',
               status: CallStatus = 'succeeded', idempotency_key: str = '', idempotency_scope: str = 'operation',
               error: dict[str, Any] | None = None) -> CallRecord:
        if idempotency_key:
            existing = self.succeeded(idempotency_key, idempotency_scope=idempotency_scope)
            if existing is not None: return self._reuse(existing, phase=phase, item_ref=item_ref)
        return self._append(CallRecord(
            operation_run_id=self.operation_run_id, adapter_type=adapter_type, request=request, response=response,
            call_id=uuid4().hex, phase=phase, item_ref=item_ref, status=status, idempotency_key=idempotency_key,
            idempotency_scope=idempotency_scope, error=error,
        ))

    def succeeded(self, idempotency_key: str, *, idempotency_scope: str = 'operation') -> CallRecord | None:
        records = self._all_records if idempotency_scope == 'run' else self.records
        for record in records:
            if (record.idempotency_key == idempotency_key and record.idempotency_scope == idempotency_scope
                    and record.status == 'succeeded'):
                return record
        return None

    def _reuse(self, existing: CallRecord, *, phase: str, item_ref: str) -> CallRecord:
        if existing.operation_run_id == self.operation_run_id: return existing
        return self._append(replace(
            existing, operation_run_id=self.operation_run_id, call_id=uuid4().hex, phase=phase or existing.phase,
            item_ref=item_ref or existing.item_ref, reused=True, reused_from_call_id=existing.call_id,
            reused_from_operation_run_id=existing.operation_run_id, record_ref='', sequence=0,
        ))

    def _append(self, record: CallRecord) -> CallRecord:
        self.records.append(record)
        if self._all_records is not self.records:
            self._all_records.append(record)
        return record

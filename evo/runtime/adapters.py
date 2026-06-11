from __future__ import annotations

import hashlib
import json
from dataclasses import dataclass
from typing import Any, Callable

from .models import CallRecord, OperationContext, OperationInterrupted


@dataclass(frozen=True)
class AdapterResult:
    record: CallRecord
    response: Any
    reused: bool = False


class AdapterCallError(Exception):
    def __init__(self, record: CallRecord):
        super().__init__(record.error['message'] if record.error else 'adapter call failed')
        self.record = record


class AdapterCall:
    def __init__(self, adapter_type: str, handler: Callable[[dict], Any]):
        self.adapter_type = adapter_type
        self.handler = handler

    def run(self, ctx: OperationContext, request: dict, *, phase: str = '', item_ref: str = '',
            idempotency_key: str = '', idempotency_scope: str = 'operation') -> AdapterResult:
        ctx.check_interrupt()
        key = idempotency_key or _idempotency_key(self.adapter_type, request)
        kwargs = dict(phase=phase, item_ref=item_ref, idempotency_key=key, idempotency_scope=idempotency_scope)
        existing = ctx.call_recorder.succeeded(key, idempotency_scope=idempotency_scope)
        if existing is not None:
            ctx.check_interrupt()
            record = ctx.call_recorder.record(self.adapter_type, request, existing.response,
                                              status='succeeded', **kwargs)
            return AdapterResult(record, record.response, reused=True)
        try:
            response = self.handler(request)
        except Exception as exc:
            if ctx.interrupt_requested(): raise OperationInterrupted(ctx.operation_run_id) from exc
            record = ctx.call_recorder.record(self.adapter_type, request, None, status='failed',
                                              error={'type': exc.__class__.__name__, 'message': str(exc)}, **kwargs)
            raise AdapterCallError(record) from exc
        ctx.check_interrupt()
        record = ctx.call_recorder.record(self.adapter_type, request, response, status='succeeded', **kwargs)
        return AdapterResult(record, response)


def _idempotency_key(adapter_type: str, request: dict) -> str:
    payload = json.dumps({'adapter_type': adapter_type, 'request': request}, ensure_ascii=False, sort_keys=True,
                         default=str)
    return hashlib.sha256(payload.encode('utf-8')).hexdigest()

from __future__ import annotations

from dataclasses import asdict
from datetime import datetime, timezone

from ..operations import OperationRunChange
from .models import Event
from .store import EvoStore


class StoreOperationRunObserver:
    def __init__(self, store: EvoStore, run_id: str):
        self.store = store
        self.run_id = run_id

    def on_operation_run_change(self, change: OperationRunChange) -> None:
        operation_run_id = change.after.operation_run_id
        event_type = f'operation.{change.kind}'
        event_payload = {'before': asdict(change.before) if change.before else None,
                         'after': asdict(change.after), 'reason': change.reason}
        self.store.append_event(Event(event_type, self.run_id, event_payload))
        operation = self._read_operation(operation_run_id)
        operation.update(asdict(change.after))
        operation[f'{change.kind}_at'] = _now()
        if change.reason: operation['last_change_reason'] = change.reason
        self.store.write_operation(self.run_id, operation_run_id, operation)
        from ..projections import rebuild_frontend_state

        rebuild_frontend_state(self.store, self.run_id)

    def _read_operation(self, operation_run_id: str) -> dict:
        try:
            return self.store.read_operation(self.run_id, operation_run_id)
        except FileNotFoundError:
            return {'operation_run_id': operation_run_id}


def _now() -> str:
    return datetime.now(timezone.utc).isoformat()

from __future__ import annotations

from ..runtime import OperationProgress
from .models import Event
from .store import EvoStore


class StoreProgressReporter:
    def __init__(self, store: EvoStore, run_id: str):
        self.store = store
        self.run_id = run_id

    def report(self, operation_run_id: str, progress: OperationProgress) -> None:
        payload = {'operation_run_id': operation_run_id, **progress.to_dict()}
        self.store.append_event(Event('operation.progress', self.run_id, payload))
        try:
            operation = self.store.read_operation(self.run_id, operation_run_id)
        except FileNotFoundError:
            operation = {'operation_run_id': operation_run_id, 'status': 'running'}
        operation['progress'] = progress.to_dict()
        self.store.write_operation(self.run_id, operation_run_id, operation)
        from ..projections import rebuild_frontend_state_throttled

        rebuild_frontend_state_throttled(self.store, self.run_id)

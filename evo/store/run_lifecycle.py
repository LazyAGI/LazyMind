from __future__ import annotations

from datetime import datetime, timezone
from typing import Any, TYPE_CHECKING

from .models import Event

if TYPE_CHECKING:
    from .store import EvoStore


class StoreRunLifecycle:
    def __init__(self, store: EvoStore, run_id: str):
        self.store = store
        self.run_id = run_id

    def mark_running(self, **extra: Any) -> None:
        self._transition('running', 'run.started', extra, clear_dispatch_block=True)

    def block_dispatch(self, reason: str, **extra: Any) -> None:
        self._transition('running', 'run.dispatch_blocked', {'dispatch_block_reason': reason, **extra})

    def open_dispatch(self, **extra: Any) -> None:
        path = self.store.run_dir(self.run_id) / 'run.json'
        data = self.store.read_json(path) if path.exists() else {}
        final_extra = dict(extra)
        if data.get('checkpoint_id'):
            final_extra.setdefault('last_checkpoint_id', data['checkpoint_id'])
        if data.get('message_id'):
            final_extra.setdefault('last_message_id', data['message_id'])
        self._transition('running', 'run.dispatch_opened', final_extra, clear_dispatch_block=True)

    def mark_ended(self, *, outcome: str = 'success', **extra: Any) -> None:
        path = self.store.run_dir(self.run_id) / 'run.json'
        data = self.store.read_json(path) if path.exists() else {}
        if data.get('status') == 'ended' and data.get('outcome') == outcome:
            return
        final_extra = {'outcome': outcome, 'ended_at': _now(), **extra}
        if data.get('checkpoint_id'):
            final_extra.setdefault('last_checkpoint_id', data['checkpoint_id'])
        if data.get('message_id'):
            final_extra.setdefault('last_message_id', data['message_id'])
        self._transition('ended', 'run.ended', final_extra)

    def can_dispatch(self) -> bool:
        path = self.store.run_dir(self.run_id) / 'run.json'
        if not path.exists():
            return True
        data = self.store.read_json(path)
        return data.get('status') in {'running', 'ended'} and not any(
            data.get(key)
            for key in ('dispatch_block_reason', 'blocked_operations', 'root_blockers', 'impacted_blockers')
        )

    def _transition(self, status: str, event_type: str, extra: dict[str, Any], *, clear_dispatch_block: bool = False) -> None:
        path = self.store.run_dir(self.run_id) / 'run.json'
        data = self.store.read_json(path) if path.exists() else {'run_id': self.run_id}
        data.update({'status': status, **extra})
        if status == 'running':
            data.setdefault('started_at', _now())
            data.pop('outcome', None)
            data.pop('ended_at', None)
        if status == 'running' and clear_dispatch_block:
            data.pop('dispatch_block_reason', None)
            data.pop('blocked_operations', None)
            data.pop('root_blockers', None)
            data.pop('impacted_blockers', None)
            data.pop('checkpoint_id', None)
            data.pop('message_id', None)
        if status == 'ended':
            data.pop('dispatch_block_reason', None)
            data.pop('blocked_operations', None)
            data.pop('root_blockers', None)
            data.pop('impacted_blockers', None)
            data.pop('checkpoint_id', None)
            data.pop('message_id', None)
        if data.get('status') == status and all(data.get(key) == value for key, value in extra.items()):
            previous = self.store.read_json(path) if path.exists() else {}
            if previous == data:
                return
        self.store.atomic_write_json(path, data)
        self.store.append_event(Event(event_type, self.run_id, {'status': status, **extra}))
        _rebuild_frontend_state(self.store, self.run_id)


def _now() -> str:
    return datetime.now(timezone.utc).isoformat()


def _rebuild_frontend_state(store: EvoStore, run_id: str) -> None:
    from ..projections import rebuild_frontend_state

    rebuild_frontend_state(store, run_id)

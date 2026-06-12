from __future__ import annotations

from dataclasses import asdict
from datetime import datetime, timezone
from typing import Any, TYPE_CHECKING

from .models import Event

if TYPE_CHECKING:
    from .store import EvoStore


_DISPATCH_BLOCK_FIELDS = ('dispatch_block_reason', 'blocked_operations', 'root_blockers', 'impacted_blockers')
_CHECKPOINT_FIELDS = (*_DISPATCH_BLOCK_FIELDS, 'checkpoint_id', 'message_id', 'next_operations', 'checkpoint_kind',
                      'checkpoint_message', 'stage', 'next_stage', 'next_op', 'detail', 'capability_id',
                      'parent_checkpoint')


class StoreRunLifecycle:
    def __init__(self, store: EvoStore, run_id: str):
        self.store = store
        self.run_id = run_id

    def _run_data(self, default: dict[str, Any] | None = None) -> dict[str, Any]:
        path = self.store.run_dir(self.run_id) / 'run.json'
        return self.store.read_json(path) if path.exists() else dict(default or {})

    def mark_running(self, **extra: Any) -> None:
        self._transition('running', 'run.started', extra, clear_dispatch_block=True)

    def block_dispatch(self, reason: str, **extra: Any) -> None:
        self._transition('running', 'run.dispatch_blocked', {'dispatch_block_reason': reason, **extra})

    def push_child_dispatch(self, reason: str, **extra: Any) -> None:
        data = self._run_data()
        parent = _checkpoint_payload(data)
        if parent is None: raise RuntimeError('child checkpoint requires an active parent checkpoint')
        incoming = _incoming_checkpoint_identity(reason, extra)
        if incoming is None or incoming == self._checkpoint_identity(data):
            raise RuntimeError('child checkpoint requires a distinct checkpoint_id')
        self._transition('running', 'run.dispatch_blocked',
                         {'dispatch_block_reason': reason, **extra, 'parent_checkpoint': parent})

    def restore_parent_dispatch(self, *, checkpoint_close_verified: bool = False, **extra: Any) -> bool:
        data = self._run_data()
        parent = data.get('parent_checkpoint')
        if not isinstance(parent, dict) or not parent.get('checkpoint_id'): return False
        if not checkpoint_close_verified:
            current = self._checkpoint_identity(data)
            checkpoint_id = current[0] if current else 'active checkpoint'
            raise RuntimeError(f'checkpoint {checkpoint_id} cannot restore parent without checkpoint manager approval')
        self._transition('running', 'run.dispatch_blocked', {**parent, **extra})
        return True

    def open_dispatch(self, *, checkpoint_close_verified: bool = False, **extra: Any) -> None:
        data = self._run_data()
        if self._checkpoint_identity(data) is not None and not checkpoint_close_verified:
            checkpoint_id = self._checkpoint_identity(data)[0]
            raise RuntimeError(f'checkpoint {checkpoint_id} cannot clear dispatch without checkpoint manager approval')
        final_extra = _with_last_checkpoint(data, dict(extra))
        self._transition('running', 'run.dispatch_opened', final_extra, clear_dispatch_block=True)

    def mark_ended(self, *, outcome: str = 'success', **extra: Any) -> None:
        data = self._run_data()
        if data.get('status') == 'ended' and data.get('outcome') == outcome: return
        final_extra = _with_last_checkpoint(data, {'outcome': outcome, 'ended_at': _now(), **extra})
        self._transition('ended', 'run.ended', final_extra)

    def mark_paused(self, **extra: Any) -> None:
        self._transition('paused', 'run.paused', {'paused_at': _now(), **extra})

    def mark_cancelled(self, **extra: Any) -> None:
        self._transition('cancelled', 'run.cancelled', {'cancelled_at': _now(), **extra})

    def mark_failed(self, **extra: Any) -> None:
        self._transition('failed', 'run.failed', {'failed_at': _now(), **extra})

    def can_dispatch(self) -> bool:
        path = self.store.run_dir(self.run_id) / 'run.json'
        if not path.exists(): return True
        data = self.store.read_json(path)
        return data.get('status') in {'running', 'ended'} and not any(data.get(key) for key in _DISPATCH_BLOCK_FIELDS)

    def _transition(self, status: str, event_type: str, extra: dict[str, Any], *,
                    clear_dispatch_block: bool = False) -> None:
        path = self.store.run_dir(self.run_id) / 'run.json'
        data = self._run_data({'run_id': self.run_id})
        if event_type == 'run.dispatch_blocked': _drop_fields(data, _CHECKPOINT_FIELDS)
        data.update({'status': status, **extra})
        if status == 'running':
            data.setdefault('started_at', _now())
            _drop_fields(data, ('outcome', 'ended_at', 'cancelled_at', 'paused_at', 'failed_at',
                                'error_type', 'message'))
        if status == 'paused':
            _drop_fields(data, ('outcome', 'ended_at', 'cancelled_at', 'failed_at', 'error_type', 'message'))
        if (status == 'running' and clear_dispatch_block) or status in {'ended', 'cancelled', 'failed'}:
            _drop_fields(data, _CHECKPOINT_FIELDS)
        if data.get('status') == status and all(data.get(key) == value for key, value in extra.items()):
            previous = self.store.read_json(path) if path.exists() else {}
            if previous == data: return
        self.store.atomic_write_json(path, data)
        self.store.append_event(Event(event_type, self.run_id, {'status': status, **extra}))
        _rebuild_frontend_state(self.store, self.run_id)

    def _checkpoint_identity(self, data: dict[str, Any]) -> tuple[str, str] | None:
        return _checkpoint_identity_from_payload(data)


def settle_lifecycle(lifecycle: Any, state: Any, *, mark_running_when_idle: bool = False) -> None:
    '''Translate an OperationGraph schedule state into the persisted run lifecycle.'''
    if state.complete:
        lifecycle.mark_ended(outcome='success')
        return
    if state.ready or state.running:
        lifecycle.mark_running()
        return
    if state.checkpointed:
        lifecycle.block_dispatch('checkpointed', blocked_operations=[str(ref) for ref in state.checkpointed])
        return
    if state.blockers:
        root_blockers = [blocker for blocker in state.blockers if blocker.reason != 'dependency_not_satisfied']
        impacted_blockers = [blocker for blocker in state.blockers if blocker.reason == 'dependency_not_satisfied']
        lifecycle.block_dispatch(
            (root_blockers or state.blockers)[0].reason,
            blocked_operations=[asdict(blocker) for blocker in state.blockers],
            root_blockers=[asdict(blocker) for blocker in root_blockers],
            impacted_blockers=[asdict(blocker) for blocker in impacted_blockers],
        )
        return
    if mark_running_when_idle:
        lifecycle.mark_running()


def _now() -> str:
    return datetime.now(timezone.utc).isoformat()


def _drop_fields(data: dict[str, Any], fields: tuple[str, ...]) -> None:
    for field in fields:
        data.pop(field, None)


def _with_last_checkpoint(data: dict[str, Any], extra: dict[str, Any]) -> dict[str, Any]:
    if data.get('checkpoint_id'): extra.setdefault('last_checkpoint_id', data['checkpoint_id'])
    if data.get('message_id'): extra.setdefault('last_message_id', data['message_id'])
    return extra


def _incoming_checkpoint_identity(reason: str, extra: dict[str, Any]) -> tuple[str, str] | None:
    checkpoint_id = str(extra.get('checkpoint_id') or '')
    if not checkpoint_id and reason == 'checkpointed': checkpoint_id = 'operation_checkpointed'
    return (checkpoint_id, reason) if checkpoint_id else None


def _checkpoint_identity_from_payload(data: dict[str, Any]) -> tuple[str, str] | None:
    reason = str(data.get('dispatch_block_reason') or '')
    checkpoint_id = str(data.get('checkpoint_id') or '')
    if not checkpoint_id and reason == 'checkpointed': checkpoint_id = 'operation_checkpointed'
    return (checkpoint_id, reason) if checkpoint_id and reason else None


def _checkpoint_payload(data: dict[str, Any]) -> dict[str, Any] | None:
    if not data.get('checkpoint_id') and data.get('dispatch_block_reason') != 'checkpointed': return None
    payload = {key: data[key] for key in _CHECKPOINT_FIELDS if key in data and key != 'parent_checkpoint'}
    if not payload.get('checkpoint_id') and payload.get('dispatch_block_reason') == 'checkpointed':
        payload['checkpoint_id'] = 'operation_checkpointed'
    return payload if payload.get('checkpoint_id') and payload.get('dispatch_block_reason') else None


def _rebuild_frontend_state(store: EvoStore, run_id: str) -> None:
    from ..projections import rebuild_frontend_state

    rebuild_frontend_state(store, run_id)

from __future__ import annotations

import os
import threading
import uuid
from pathlib import Path
from threading import RLock
from typing import Any

from .executor import AutoActionExecutor
from .models import AutoAgentConfig
from .observer import AutoObserver
from .policy import AutoPolicy
from .ports import AutoAgentPorts
from .store import AutoAgentLease, AutoAgentLeaseError, AutoAgentStateError, AutoAgentStore


class AutoAgentRunner:
    def __init__(self, base_dir: str | Path, ports: AutoAgentPorts) -> None:
        self.store = AutoAgentStore(base_dir)
        self.observer = AutoObserver(ports)
        self.policy = AutoPolicy()
        self.executor = AutoActionExecutor(ports, self.store)
        self._lock = RLock()
        self._owner_prefix = f'evo-auto-agent:{os.getpid()}:{uuid.uuid4().hex}'
        self._step_locks: dict[str, RLock] = {}
        self._threads: dict[str, threading.Thread] = {}
        self._stops: dict[str, threading.Event] = {}

    def start(self, thread_id: str, config_payload: dict[str, Any] | None = None) -> dict[str, Any]:
        config = _config(config_payload)
        owner_id = f'{self._owner_prefix}:loop:{thread_id}:{uuid.uuid4().hex}'
        try:
            lease = self.store.claim_lease(thread_id, owner_id=owner_id)
        except AutoAgentLeaseError as exc:
            return self.status(thread_id) | {'started': False, 'skipped': True, 'reason': str(exc)}
        started = False
        stop: threading.Event | None = None
        thread: threading.Thread | None = None
        state = None
        try:
            state = self.store.mark_running(thread_id, config, lease=lease)
            with self._lock:
                old_stop = self._stops.get(thread_id)
                if old_stop is not None:
                    old_stop.set()
                stop = threading.Event()
                thread = threading.Thread(
                    target=self._run_loop,
                    args=(thread_id, stop, lease),
                    name=f'evo-auto-agent-{thread_id}',
                    daemon=True,
                )
                self._stops[thread_id] = stop
                self._threads[thread_id] = thread
                thread.start()
                started = True
        except Exception as exc:  # noqa: BLE001 - lease must not leak during start setup.
            if not started:
                with self._lock:
                    if self._stops.get(thread_id) is stop:
                        self._stops.pop(thread_id, None)
                    if self._threads.get(thread_id) is thread:
                        self._threads.pop(thread_id, None)
                try:
                    if state is not None:
                        self.store.save(state.model_copy(update={
                            'running': False,
                            'stop_reason': f'start_error:{type(exc).__name__}:{exc}',
                        }), lease=lease)
                finally:
                    self.store.release_lease(lease)
            return {'thread_id': thread_id, 'started': False, 'running': False, 'error': str(exc)}
        return self.status(thread_id) | {'started': True, 'running': state.running}

    def stop(self, thread_id: str, reason: str = 'stopped_by_request') -> dict[str, Any]:
        try:
            state = self.store.mark_stopped(thread_id, reason)
        except AutoAgentStateError as exc:
            with self._lock:
                if stop := self._stops.get(thread_id):
                    stop.set()
            return {'thread_id': thread_id, 'stopped': False, 'running': False, 'error': str(exc)}
        with self._lock:
            if stop := self._stops.get(thread_id):
                stop.set()
        return self.status(thread_id) | {'stopped': True, 'running': state.running}

    def step(self, thread_id: str, config_payload: dict[str, Any] | None = None) -> dict[str, Any]:
        owner_id = f'{self._owner_prefix}:step:{thread_id}:{uuid.uuid4().hex}'
        try:
            with self.store.lease(thread_id, owner_id=owner_id) as lease:
                stop = threading.Event()
                heartbeat_stop = self._start_heartbeat(lease, stop)
                try:
                    return self._step_locked(thread_id, config_payload, lease=lease)
                finally:
                    stop.set()
                    heartbeat_stop.set()
        except AutoAgentLeaseError as exc:
            return self.status(thread_id) | {'skipped': True, 'reason': str(exc)}

    def _step_locked(
        self,
        thread_id: str,
        config_payload: dict[str, Any] | None = None,
        *,
        lease: AutoAgentLease,
    ) -> dict[str, Any]:
        with self._lock:
            lock = self._step_locks.setdefault(thread_id, RLock())
        with lock:
            state = self.store.load(thread_id, config=_config(config_payload))
            self.store.assert_lease(lease)
            observation = self.observer.observe(thread_id)
            decision = self.policy.decide(observation, state)
            updated = self.executor.execute(thread_id, decision, state, lease=lease)
            return {
                'thread_id': thread_id,
                'running': updated.running,
                'observation': observation.model_dump(mode='json'),
                'decision': decision.model_dump(mode='json'),
                'state': _state_payload(updated),
            }

    def status(self, thread_id: str) -> dict[str, Any]:
        try:
            state = self.store.load(thread_id)
            state_payload = _state_payload(state)
            running = state.running
        except AutoAgentStateError as exc:
            state_payload = {}
            running = False
            state_error = str(exc)
        else:
            state_error = ''
        with self._lock:
            thread = self._threads.get(thread_id)
            alive = bool(thread and thread.is_alive())
        lease = self.store.lease_status(thread_id)
        return {
            'thread_id': thread_id,
            'running': running,
            'alive': alive or bool(lease.get('active')),
            'local_alive': alive,
            'lease': lease,
            'state': state_payload,
            'state_error': state_error,
        }

    def _run_loop(self, thread_id: str, stop: threading.Event, lease: AutoAgentLease) -> None:
        try:
            heartbeat_stop = self._start_heartbeat(lease, stop)
            try:
                while not stop.is_set():
                    with self._lock:
                        if self._stops.get(thread_id) is not stop:
                            return
                    state = self.store.load(thread_id)
                    if not state.running:
                        return
                    try:
                        self._step_locked(thread_id, state.config.model_dump(mode='json'), lease=lease)
                    except AutoAgentStateError:
                        return
                    except Exception as exc:  # noqa: BLE001 - persisted state should expose loop failures.
                        latest = self.store.load(thread_id)
                        self.store.save(latest.model_copy(update={
                            'running': False,
                            'stop_reason': f'loop_error:{type(exc).__name__}:{exc}',
                        }), lease=lease)
                        return
                    state = self.store.load(thread_id)
                    if not state.running:
                        return
                    stop.wait(state.config.tick_interval_s)
            finally:
                heartbeat_stop.set()
                self.store.release_lease(lease)
        except (AutoAgentLeaseError, AutoAgentStateError):
            return

    def _start_heartbeat(self, lease: AutoAgentLease, stop: threading.Event) -> threading.Event:
        heartbeat_stop = threading.Event()
        interval = max(1.0, self.store.lease_seconds / 3.0)

        def beat() -> None:
            while not heartbeat_stop.wait(interval):
                if stop.is_set():
                    return
                try:
                    self.store.heartbeat(lease)
                except AutoAgentLeaseError:
                    stop.set()
                    return

        threading.Thread(
            target=beat,
            name=f'evo-auto-agent-heartbeat-{lease.thread_id}',
            daemon=True,
        ).start()
        return heartbeat_stop


def _config(payload: dict[str, Any] | None) -> AutoAgentConfig:
    raw: dict[str, Any] = {}
    if isinstance(payload, dict):
        if isinstance(payload.get('auto_agent'), dict):
            raw = dict(payload['auto_agent'])
        elif any(key in AutoAgentConfig.model_fields for key in payload):
            raw = dict(payload)
    return AutoAgentConfig.model_validate(raw or {})


def _state_payload(state) -> dict[str, Any]:
    data = state.model_dump(mode='json')
    data['records'] = data['records'][-20:]
    return data

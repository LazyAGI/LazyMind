from __future__ import annotations

import time
import uuid
from typing import Any

from fastapi import HTTPException

from ..checkpoints import CheckpointState
from ..checkpoints.models import RESUME_FROM_SNAPSHOT, RESUME_WITH_INTERVENTIONS
from ..store import StoreRunLifecycle
from .flow import EvoFlowService


class ContinuationPolicyResolver:
    @staticmethod
    def resolve(payload: dict[str, Any] | None = None, checkpoint: CheckpointState | None = None) -> str:
        del checkpoint
        payload = payload or {}
        value = str(payload.get('input_policy') or '').strip()
        if not value and payload.get('restart_from_snapshot'): value = RESUME_FROM_SNAPSHOT
        if not value: value = RESUME_WITH_INTERVENTIONS
        if value not in {RESUME_FROM_SNAPSHOT, RESUME_WITH_INTERVENTIONS}:
            expected = f'{RESUME_WITH_INTERVENTIONS} or {RESUME_FROM_SNAPSHOT}'
            raise HTTPException(400, f'bad input_policy {value!r}; expected {expected}')
        return value


class InterventionCoordinator:
    def __init__(self, hub: Any, run_id: str):
        self.hub = hub
        self.run_id = run_id

    def pause(self, thread_id: str) -> dict:
        service = self.hub._service(thread_id)
        StoreRunLifecycle(service.store, self.run_id).mark_paused(thread_id=thread_id)
        self.hub._update_meta(thread_id, status='paused', updated_at=time.time())
        for ref in service.graph.run_refs({'running'}):
            service.runtime.request_interrupt(ref)
        task = self.hub._tasks.get(thread_id)
        if task and task.is_alive():
            task.join(timeout=5)
        if task and task.is_alive():
            StoreRunLifecycle(service.store, self.run_id).mark_running(thread_id=thread_id,
                                                                       reason='pause_timeout')
            self.hub._update_meta(thread_id, status='running', updated_at=time.time())
            return {'status': 'running', 'thread_id': thread_id, 'paused': False, 'block_reason': 'flow_busy'}
        if not (task and task.is_alive()):
            self.hub._checkpoint_orphaned_running_operations(service)
        return {'status': 'paused', 'thread_id': thread_id, 'paused': True}

    def cancel(self, thread_id: str) -> dict:
        service = self.hub._service(thread_id)
        for ref in service.graph.run_refs({'running'}):
            service.runtime.request_interrupt(ref)
        self.hub._queued_messages.pop(thread_id, None)
        service.checkpoints.cancel_active(self.run_id, thread_id=thread_id)
        StoreRunLifecycle(service.store, self.run_id).mark_cancelled(thread_id=thread_id)
        self.hub._update_meta(thread_id, status='cancelled', pending_checkpoint=None, updated_at=time.time())
        event = self.hub._checkpoint_events.get(thread_id)
        if event: event.set()
        return {'status': 'cancelled', 'thread_id': thread_id}

    def retry(self, thread_id: str, payload: dict[str, Any] | None = None) -> dict:
        return self.continue_thread(thread_id, payload)

    def continue_thread(self, thread_id: str, payload: dict[str, Any] | None = None) -> dict:
        payload = payload or {}
        self.hub._meta(thread_id)
        if self.hub._task_alive(thread_id):
            return {'status': 'running', 'thread_id': thread_id, 'resumed': False, 'block_reason': 'flow_busy'}
        if not self.hub._has_run(thread_id) and thread_id not in self.hub._services:
            raise HTTPException(409, 'thread has no flow to continue')
        service: EvoFlowService = self.hub._service(thread_id)
        checkpoint = self.hub._stage_checkpoint(thread_id)
        if checkpoint and checkpoint.is_intent_confirmation and not payload.get('confirm_intent'):
            return {'status': 'waiting_checkpoint', 'thread_id': thread_id, 'resumed': False,
                    'block_reason': 'intent_confirmation_required'}
        if checkpoint and checkpoint.is_manual_cutover and not payload.get('confirm_cutover'):
            return {'status': 'waiting_checkpoint', 'thread_id': thread_id, 'resumed': False,
                    'block_reason': 'manual_cutover_confirmation_required'}

        policy = ContinuationPolicyResolver.resolve(payload, checkpoint)
        if checkpoint and checkpoint.is_intent_confirmation:
            result = self.hub._execute_intent_confirmation(
                thread_id, service, checkpoint, str(payload.get('message_id') or f'continue_{uuid.uuid4().hex[:8]}'),
                input_policy=policy,
            )
            return {'status': self.hub.flow_status(thread_id)['status'], 'thread_id': thread_id,
                    'resumed': bool(result.raw.get('parent_resumed', False)),
                    'intent_applied': bool(result.raw.get('intent_applied', False)),
                    'action': result.action}
        if checkpoint and checkpoint.is_manual_cutover:
            result = self.hub._confirm_manual_cutover(
                thread_id, service, checkpoint, str(payload.get('message_id') or f'continue_{uuid.uuid4().hex[:8]}'),
                policy,
            )
            return {'status': self.hub.flow_status(thread_id)['status'], 'thread_id': thread_id, 'resumed': True,
                    'action': result.action, 'input_policy': policy}
        if checkpoint:
            resumed = self.hub._resume_stage_checkpoint(thread_id, service, checkpoint, 'continue', policy)
            return {'status': self.hub.flow_status(thread_id)['status'], 'thread_id': thread_id, 'resumed': resumed,
                    'input_policy': policy, 'next_stage': checkpoint.next_stage}

        if service.graph.run_refs({'checkpointed'}):
            service.resume_checkpointed(input_policy=policy, dispatch=False)
            self.hub._update_meta(thread_id, status='running', pending_checkpoint=None, updated_at=time.time())
            self.hub._start_flow_task_locked(thread_id, self.hub._resume_start_stage(thread_id))
            return {'status': 'running', 'thread_id': thread_id, 'resumed': True, 'input_policy': policy}

        if str(self.hub._meta(thread_id).get('status') or '') == 'paused':
            self.hub._update_meta(thread_id, status='running', pending_checkpoint=None, updated_at=time.time())
            self.hub._start_flow_task_locked(thread_id, self.hub._resume_start_stage(thread_id))
            return {'status': 'running', 'thread_id': thread_id, 'resumed': True}

        raise HTTPException(409, 'thread has no checkpoint or paused work to continue')

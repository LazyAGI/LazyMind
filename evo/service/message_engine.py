from __future__ import annotations

import time
import uuid
from typing import Any

from fastapi import HTTPException

from ..operations import OperationRunRef
from .flow import EvoFlowService, FlowMessageResult


class MessageExecutionEngine:
    def __init__(self, hub: Any):
        self.hub = hub

    def handle_message(self, thread_id: str, payload: dict[str, Any]) -> dict:
        content = str(payload.get('content') or payload.get('message') or '').strip()
        if not content: raise HTTPException(400, 'message content required')
        message_id = str(payload.get('message_id') or f'msg_{thread_id}_{uuid.uuid4().hex[:8]}')
        self.hub._append_message(thread_id, 'user', content)
        task_alive = self.hub._task_alive(thread_id)
        checkpoint = self.hub._stage_checkpoint(thread_id)
        if checkpoint:
            service = self.hub._service(thread_id)
            return self.hub._checkpoint_messages.handle(thread_id, service, checkpoint, message_id, content, payload)
        if task_alive:
            return self._handle_running_message(thread_id, message_id, content, payload)
        return self._handle_idle_message(thread_id, message_id, content, payload)

    def _handle_running_message(self, thread_id: str, message_id: str, content: str,
                                payload: dict[str, Any]) -> dict:
        service = self.hub._service(thread_id)
        result = self.hub._preview_message(thread_id, service, message_id, content, payload)
        if result.action in {'read_run_status_query', 'explain_run_failure_query'}:
            result = service.send_message(message_id, content, allowed_capabilities=payload.get('allowed_capabilities'),
                                          dispatch=False, max_dispatch=int(payload.get('max_dispatch') or 1))
            outputs = service.run_checkpoint_query([OperationRunRef(ref) for ref in result.operation_refs])
            result = FlowMessageResult(message_id, result.raw, result.action, result.operation_refs, outputs,
                                       result.skipped, result.requires_confirmation,
                                       result.confirmation_checkpoint_id)
            return self.hub._message_response(thread_id, message_id,
                                              self.hub._result_reply(thread_id, service, result, content), result)
        if self.hub._pause_running_for_message(thread_id, service):
            self.hub._update_meta(thread_id, status='running', updated_at=time.time())
            result = service.send_message(message_id, content, allowed_capabilities=payload.get('allowed_capabilities'),
                                          dispatch=bool(payload.get('dispatch', True)),
                                          max_dispatch=int(payload.get('max_dispatch') or 1))
            if self.hub._should_start_resumed_dispatch(result):
                self.hub._start_resumed_dispatch(thread_id)
            return self.hub._message_response(thread_id, message_id,
                                              self.hub._result_reply(thread_id, service, result, content), result)
        self.hub._queued_messages.setdefault(thread_id, []).append({
            'message_id': message_id, 'content': content,
            'allowed_capabilities': payload.get('allowed_capabilities'),
            'dispatch': bool(payload.get('dispatch', True)),
            'max_dispatch': int(payload.get('max_dispatch') or 1), 'action': result.action,
        })
        queued = FlowMessageResult(message_id, result.raw, result.action, result.operation_refs, [], skipped=True)
        return self.hub._message_response(
            thread_id, message_id, self.hub._result_reply(thread_id, service, queued, content), queued,
            requires_confirmation=False, confirmation_checkpoint_id='',
            result_payload=_queued_preview_result_dict(result),
        )

    def _handle_idle_message(self, thread_id: str, message_id: str, content: str,
                             payload: dict[str, Any]) -> dict:
        dispatch = bool(payload.get('dispatch', True))
        had_run = self.hub._has_run(thread_id)
        service: EvoFlowService = self.hub._service(thread_id)
        if not had_run: service.plan_full_flow()
        checkpoint = self.hub._stage_checkpoint(thread_id)
        if checkpoint:
            return self.hub._checkpoint_messages.handle(thread_id, service, checkpoint, message_id, content, payload)
        resume_stage = self.hub._stalled_resume_stage(thread_id)
        result = self.hub._preview_message(thread_id, service, message_id, content, payload) if not dispatch else (
            service.send_message(message_id, content, allowed_capabilities=payload.get('allowed_capabilities'),
                                 dispatch=True, max_dispatch=int(payload.get('max_dispatch') or 1))
        )
        if result.action == 'resume_checkpointed' and resume_stage:
            self.hub._start_resume_stage(thread_id, service, resume_stage, 'message')
            reply = self.hub._result_reply(thread_id, service, result, content)
        else:
            if result.action == 'resume_checkpointed': self.hub._start_resumed_dispatch(thread_id)
            reply = self.hub._result_reply(thread_id, service, result, content)
            if self.hub._should_start_resumed_dispatch(result): self.hub._start_resumed_dispatch(thread_id)
        return self.hub._message_response(thread_id, message_id, reply, result)


def _queued_preview_result_dict(result: FlowMessageResult) -> dict[str, Any]:
    payload = result.raw | {'action': result.action, 'queued': True, 'executed': False}
    return {'message_id': result.message_id, 'raw': payload, 'action': result.action,
            'operation_refs': result.operation_refs, 'results': [], 'skipped': True,
            'requires_confirmation': False, 'confirmation_checkpoint_id': ''}

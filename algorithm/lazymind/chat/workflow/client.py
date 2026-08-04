"""Typed client for the Workflow Tool facade.

All algorithm/chat Workflow HTTP traffic belongs here.  The legacy transport is
kept behind an explicit rollback flag and records every fallback invocation.
"""
from __future__ import annotations

import logging
import os
import time
import uuid
from dataclasses import asdict, dataclass, field
from typing import Any, Dict, List, Optional

import httpx

CONTRACT_VERSION = 'workflow.v1'
LOG = logging.getLogger(__name__)
workflow_legacy_client_hits = 0
WorkflowTransportTimeout = httpx.TimeoutException


def workflow_http_get(url: str, **kwargs: Any) -> Any:
    """Compatibility transport boundary for old read endpoints."""
    record_legacy_client_call('GET ' + url.rsplit('/', 1)[-1])
    transport = kwargs.pop('transport', httpx)
    return transport.get(url, **kwargs)


def workflow_http_post(url: str, **kwargs: Any) -> Any:
    """Compatibility transport boundary for old command endpoints."""
    record_legacy_client_call('POST ' + url.rsplit('/', 1)[-1])
    transport = kwargs.pop('transport', httpx)
    return transport.post(url, **kwargs)


@dataclass(frozen=True)
class StepCommand:
    step_id: str
    task_id: str = ''
    objective: str = ''
    user_input: str = ''
    runtime_instruction: str = ''
    partial_indices: Dict[str, List[int]] = field(default_factory=dict)


@dataclass(frozen=True)
class AdvanceRequest:
    session_id: str
    expected_state_version: int
    steps: List[StepCommand]
    handoff: bool = False
    command_id: str = field(default_factory=lambda: str(uuid.uuid4()))


@dataclass(frozen=True)
class WorkflowResponse:
    result: Dict[str, Any]
    request_id: str = ''


class WorkflowClientError(RuntimeError):
    def __init__(self, code: str, message: str, retryable: bool = False,
                 status_code: int = 0, details: Optional[Dict[str, Any]] = None):
        super().__init__(code, message, retryable, status_code, details)
        self.code = code
        self.message = message
        self.retryable = retryable
        self.status_code = status_code
        self.details = details or {}

    def __str__(self) -> str:
        return self.message


class WorkflowClient:
    """Small synchronous facade client with bounded safe retries."""

    def __init__(self, base_url: str, user_id: str = '', *, timeout: float = 15.0,
                 read_retries: int = 2, transport: Any = httpx):
        self.base_url = base_url.rstrip('/')
        self.user_id = user_id
        self.timeout = timeout
        self.read_retries = read_retries
        self.transport = transport

    def _headers(self, command_id: str = '') -> Dict[str, str]:
        headers = {'Workflow-Contract-Version': CONTRACT_VERSION}
        if self.user_id:
            headers['X-User-Id'] = self.user_id
        if command_id:
            headers['Idempotency-Key'] = command_id
        return headers

    @staticmethod
    def _decode(response: Any) -> WorkflowResponse:
        try:
            body = response.json()
        except Exception as exc:
            raise WorkflowClientError('INVALID_RESPONSE', str(exc),
                                      status_code=response.status_code) from exc
        if response.status_code >= 400 or (isinstance(body, dict) and body.get('ok') is False):
            error = body.get('error', {}) if isinstance(body, dict) else {}
            raise WorkflowClientError(
                str(error.get('code') or 'WORKFLOW_REQUEST_FAILED'),
                str(error.get('message') or f'Workflow request failed ({response.status_code})'),
                retryable=bool(error.get('retryable')),
                status_code=response.status_code,
                details=error.get('details') if isinstance(error.get('details'), dict) else {},
            )
        result = body.get('result', body.get('data', body)) if isinstance(body, dict) else {}
        return WorkflowResponse(result=result if isinstance(result, dict) else {'value': result},
                                request_id=str(body.get('request_id') or '') if isinstance(body, dict) else '')

    def _read(self, path: str) -> WorkflowResponse:
        for attempt in range(self.read_retries + 1):
            try:
                return self._decode(self.transport.get(
                    self.base_url + path, headers=self._headers(), timeout=self.timeout,
                ))
            except WorkflowClientError as exc:
                if not exc.retryable or attempt >= self.read_retries:
                    raise
            except (httpx.TimeoutException, httpx.TransportError) as exc:
                if attempt >= self.read_retries:
                    raise WorkflowClientError('WORKFLOW_TIMEOUT', str(exc), retryable=True) from exc
            time.sleep(0.05 * (2 ** attempt))
        raise AssertionError('unreachable')

    def get_state(self, session_id: str) -> Dict[str, Any]:
        return self._read(f'/workflow-sessions/{session_id}/projection').result

    def advance(self, request: AdvanceRequest) -> WorkflowResponse:
        # Keep the established public tool spelling across Host and Runtime.
        tool = 'advance_step_and_hand_off' if request.handoff else 'advance_step'
        payload = {
            'contract_version': CONTRACT_VERSION,
            'command_id': request.command_id,
            'tool': tool,
            'session_id': request.session_id,
            'expected_state_version': request.expected_state_version,
            'steps': [asdict(step) for step in request.steps],
        }
        path = f'/workflow-sessions/{request.session_id}:' + (
            'advance-step-and-hand-off' if request.handoff else 'advance-step'
        )
        try:
            response = self.transport.post(
                self.base_url + path, json=payload,
                headers=self._headers(request.command_id), timeout=self.timeout,
            )
        except (httpx.TimeoutException, httpx.TransportError) as exc:
            # Commands are never blindly retried. The command id is safe for explicit reconciliation.
            raise WorkflowClientError('TRANSITION_RESULT_UNKNOWN', str(exc), retryable=True) from exc
        return self._decode(response)

    def prepare_start(self, workflow_id: str, session_id: str, command_id: str,
                      start_fields: Dict[str, Any]) -> WorkflowResponse:
        payload = {
            **start_fields, 'workflow_id': workflow_id,
            'preparation_id': command_id, 'idempotency_key': command_id,
        }
        prepared = self._decode(self.transport.post(
            self.base_url + '/workflow-preparations', json=payload,
            headers=self._headers(command_id), timeout=self.timeout,
        )).result
        preparation_id = str(prepared.get('id') or prepared.get('preparation_id') or command_id)
        return self._decode(self.transport.post(
            f'{self.base_url}/workflow-preparations/{preparation_id}:consume',
            json={'session_id': session_id}, headers=self._headers(command_id), timeout=self.timeout,
        ))


def use_legacy_client() -> bool:
    return os.getenv('LAZYMIND_WORKFLOW_CLIENT_V1', 'true').lower() not in {'1', 'true', 'yes', 'on'}


def record_legacy_client_call(caller: str) -> None:
    global workflow_legacy_client_hits
    workflow_legacy_client_hits += 1
    LOG.warning('workflow_legacy_client_call caller=%s count=%d deletion_gate=zero_calls',
                caller, workflow_legacy_client_hits)


_FORBIDDEN_ATTEMPT_KEYS = {
    'llm_config', 'model_config', 'tool_config', 'api_key', 'token',
    'history_files_per_turn', 'local_path', 'temporary_url', 'host_prompt',
}


def sanitize_attempt_context(context: Dict[str, Any]) -> Dict[str, Any]:
    """Fail closed when Host-private values are about to cross the Runtime boundary."""
    def clean(value: Any, key: str = '') -> Any:
        if key.lower() in _FORBIDDEN_ATTEMPT_KEYS:
            raise WorkflowClientError('ATTEMPT_CONTEXT_PRIVATE_DATA', f'forbidden field: {key}')
        if isinstance(value, dict):
            return {name: clean(item, name) for name, item in value.items()}
        if isinstance(value, list):
            return [clean(item, key) for item in value]
        if isinstance(value, str) and (value.startswith('/') or value.startswith('file://')):
            raise WorkflowClientError('ATTEMPT_CONTEXT_PRIVATE_DATA', f'private path in field: {key}')
        return value

    return clean(context)

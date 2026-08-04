"""Host-neutral client and local LazyMind endpoint discovery for Workflow v1."""
from __future__ import annotations

import json
import os
import platform
import time
import uuid
from dataclasses import asdict, dataclass, field
from pathlib import Path
from typing import Any, Dict, List, Optional
from urllib.parse import quote, urlencode, urlsplit

import httpx

CONTRACT_VERSION = 'workflow.v1'


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


@dataclass(frozen=True)
class ConnectionInfo:
    base_url: str
    source: str
    runtime_root: str = ''


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


def _default_runtime_roots() -> list[Path]:
    explicit = os.getenv('LAZYMIND_RUNTIME_ROOT', '').strip()
    roots = [Path(explicit)] if explicit else []
    system = platform.system().lower()
    if system == 'darwin':
        roots.append(Path.home() / 'Library' / 'Application Support' / 'LazyMind')
    elif system == 'windows':
        local = os.getenv('LOCALAPPDATA', '').strip()
        if local:
            roots.append(Path(local) / 'LazyMind')
    else:
        data = os.getenv('XDG_DATA_HOME', '').strip()
        roots.append(Path(data) / 'LazyMind' if data else Path.home() / '.local/share/LazyMind')
    return list(dict.fromkeys(roots))


def _endpoint_from_file(path: Path) -> str:
    try:
        body = json.loads(path.read_text(encoding='utf-8'))
    except (OSError, ValueError):
        return ''
    host = body.get('host') or body.get('Host') or {}
    endpoint = str(
        host.get('coreBaseUrl') or host.get('coreBaseURL') or host.get('core_base_url')
        or host.get('CoreBaseURL') or ''
    ).rstrip('/')
    if endpoint and urlsplit(endpoint).path in {'', '/'}:
        endpoint += '/api/core'
    return endpoint


def discover_connection() -> ConnectionInfo:
    """Resolve Core without assuming a fixed local port."""
    for name in ('LAZYMIND_WORKFLOW_BASE_URL', 'LAZYMIND_ENDPOINT_HOST_CORE_BASE_URL',
                 'LAZYMIND_CORE_API_URL', 'LAZYMIND_CORE_SERVICE_URL'):
        value = os.getenv(name, '').strip().rstrip('/')
        if value:
            return ConnectionInfo(value, f'env:{name}')
    for root in _default_runtime_roots():
        endpoint = _endpoint_from_file(root / 'generated' / 'service-endpoints.json')
        if endpoint:
            return ConnectionInfo(endpoint, 'runtime-service-endpoints', str(root))
    raise WorkflowClientError(
        'LAZYMIND_NOT_FOUND',
        'LazyMind Core was not discovered; start LazyMind or set LAZYMIND_WORKFLOW_BASE_URL.',
    )


class WorkflowClient:
    """Shared HTTP implementation used by LazyMind and MCP adapters."""

    def __init__(self, base_url: str = '', user_id: str = '', *, token: str = '',
                 timeout: float = 15.0, read_retries: int = 2, transport: Any = httpx):
        connection = ConnectionInfo(base_url.rstrip('/'), 'argument') if base_url else discover_connection()
        self.connection = connection
        self.base_url = connection.base_url
        self.user_id = user_id or os.getenv('LAZYMIND_WORKFLOW_USER_ID', '').strip()
        self.token = token or os.getenv('LAZYMIND_WORKFLOW_TOKEN', '').strip()
        self.timeout = timeout
        self.read_retries = read_retries
        self.transport = transport

    def _headers(self, command_id: str = '') -> Dict[str, str]:
        headers = {'Workflow-Contract-Version': CONTRACT_VERSION}
        if self.user_id:
            headers['X-User-Id'] = self.user_id
        if self.token:
            headers['Authorization'] = f'Bearer {self.token}'
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
                retryable=bool(error.get('retryable')), status_code=response.status_code,
                details=error.get('details') if isinstance(error.get('details'), dict) else {},
            )
        result = body.get('result', body.get('data', body)) if isinstance(body, dict) else {}
        return WorkflowResponse(
            result=result if isinstance(result, dict) else {'value': result},
            request_id=str(body.get('request_id') or '') if isinstance(body, dict) else '',
        )

    def _read(self, path: str) -> WorkflowResponse:
        for attempt in range(self.read_retries + 1):
            try:
                response = self.transport.get(
                    self.base_url + path, headers=self._headers(), timeout=self.timeout,
                )
                return self._decode(response)
            except WorkflowClientError as exc:
                if not exc.retryable or attempt >= self.read_retries:
                    raise
            except (httpx.TimeoutException, httpx.TransportError) as exc:
                if attempt >= self.read_retries:
                    raise WorkflowClientError('WORKFLOW_TIMEOUT', str(exc), retryable=True) from exc
            time.sleep(0.05 * (2 ** attempt))
        raise AssertionError('unreachable')

    def connection_status(self) -> Dict[str, Any]:
        workflows = self.list_workflows().result
        return {'connected': True, 'base_url': self.base_url,
                'source': self.connection.source, 'contract_version': CONTRACT_VERSION,
                'discovery_response': workflows}

    def list_workflows(self) -> WorkflowResponse:
        return self._read('/workflows')

    def get_workflow(self, workflow_id: str) -> WorkflowResponse:
        return self._read(f'/workflows/{workflow_id}')

    def get_state(self, session_id: str) -> Dict[str, Any]:
        return self._read(f'/workflow-sessions/{session_id}/projection').result

    def get_ready_steps(self, session_id: str) -> Dict[str, Any]:
        state = self.get_state(session_id)
        ready = state.get('ready_steps', state.get('ready', []))
        return {'session_id': session_id, 'state_version': state.get('state_version'),
                'ready_steps': ready, 'projection': state}

    def prepare_workflow(self, workflow_id: str, *, input_bindings: Optional[Dict[str, Any]] = None,
                         command_id: str = '', fields: Optional[Dict[str, Any]] = None) -> WorkflowResponse:
        command_id = command_id or str(uuid.uuid4())
        payload = {**(fields or {}), 'workflow_id': workflow_id, 'preparation_id': command_id,
                   'idempotency_key': command_id, 'input_bindings': input_bindings or {}}
        return self._decode(self.transport.post(
            self.base_url + '/workflow-preparations', json=payload,
            headers=self._headers(command_id), timeout=self.timeout,
        ))

    def start_workflow(self, preparation_id: str, session_id: str,
                       *, command_id: str = '') -> WorkflowResponse:
        command_id = command_id or preparation_id
        return self._decode(self.transport.post(
            f'{self.base_url}/workflow-preparations/{preparation_id}:consume',
            json={'session_id': session_id}, headers=self._headers(command_id), timeout=self.timeout,
        ))

    def advance(self, request: AdvanceRequest) -> WorkflowResponse:
        tool = 'advance_step_and_hand_off' if request.handoff else 'advance_step'
        payload = {'contract_version': CONTRACT_VERSION, 'command_id': request.command_id,
                   'tool': tool, 'session_id': request.session_id,
                   'expected_state_version': request.expected_state_version,
                   'steps': [asdict(step) for step in request.steps]}
        path = f'/workflow-sessions/{request.session_id}:' + (
            'advance-step-and-hand-off' if request.handoff else 'advance-step')
        try:
            response = self.transport.post(
                self.base_url + path, json=payload, headers=self._headers(request.command_id),
                timeout=self.timeout,
            )
        except (httpx.TimeoutException, httpx.TransportError) as exc:
            raise WorkflowClientError('TRANSITION_RESULT_UNKNOWN', str(exc), retryable=True) from exc
        return self._decode(response)

    def prepare_start(self, workflow_id: str, session_id: str, command_id: str,
                      start_fields: Dict[str, Any]) -> WorkflowResponse:
        prepared = self.prepare_workflow(
            workflow_id, input_bindings=start_fields.get('input_bindings'), command_id=command_id,
            fields=start_fields,
        ).result
        preparation_id = str(prepared.get('id') or prepared.get('preparation_id') or command_id)
        return self.start_workflow(preparation_id, session_id, command_id=command_id)

    def get_skill_conversion_context(self, skill_id: str,
                                     revision_id: str = '') -> WorkflowResponse:
        query = {'skill_id': skill_id}
        if revision_id:
            query['revision_id'] = revision_id
        return self._read('/workflow-authoring/v1/skill-context?' + urlencode(query))

    def create_workflow_draft(self, name: str, skill_id: str, revision_id: str,
                              tree_hash: str, files: Dict[str, str]) -> WorkflowResponse:
        return self._decode(self.transport.post(
            self.base_url + '/workflow-authoring/v1/drafts',
            json={'name': name, 'skill_id': skill_id, 'revision_id': revision_id,
                  'tree_hash': tree_hash, 'files': files},
            headers=self._headers(), timeout=self.timeout,
        ))

    def update_workflow_draft_file(self, draft_id: str, path: str, content: str,
                                   expected_version: int) -> WorkflowResponse:
        return self._decode(self.transport.put(
            f'{self.base_url}/workflow-authoring/v1/drafts/{quote(draft_id, safe="")}/files',
            json={'path': path, 'content': content, 'expected_version': expected_version},
            headers=self._headers(), timeout=self.timeout,
        ))

    def validate_workflow_draft(self, draft_id: str) -> WorkflowResponse:
        return self._decode(self.transport.post(
            f'{self.base_url}/workflow-drafts/{quote(draft_id, safe="")}:validate',
            json={}, headers=self._headers(), timeout=self.timeout,
        ))

    def get_workflow_diagnostics(self, draft_id: str) -> WorkflowResponse:
        return self._read(
            f'/workflow-authoring/v1/drafts/{quote(draft_id, safe="")}/diagnostics')

    def publish_workflow(self, draft_id: str) -> WorkflowResponse:
        return self._decode(self.transport.post(
            f'{self.base_url}/workflow-authoring/v1/drafts/{quote(draft_id, safe="")}:publish',
            json={}, headers=self._headers(), timeout=self.timeout,
        ))

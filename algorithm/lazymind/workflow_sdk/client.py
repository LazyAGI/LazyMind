"""Host-neutral client and local LazyMind endpoint discovery for Workflow v1."""
from __future__ import annotations

import base64
import json
import os
import platform
import time
import uuid
from dataclasses import asdict, dataclass, field
from pathlib import Path
from typing import Any, Callable, Dict, Iterator, List, Optional
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
    retry_origin: str = 'automatic'
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
                 host: str = '', timeout: float = 15.0, read_retries: int = 2,
                 execution_timeout: float = 7200.0, transport: Any = httpx,
                 trace_context: Optional[Callable[[], Any]] = None):
        connection = ConnectionInfo(base_url.rstrip('/'), 'argument') if base_url else discover_connection()
        self.connection = connection
        self.base_url = connection.base_url
        self.user_id = user_id or os.getenv('LAZYMIND_WORKFLOW_USER_ID', '').strip()
        self.token = token or os.getenv('LAZYMIND_WORKFLOW_TOKEN', '').strip()
        self.host = host or os.getenv('LAZYMIND_WORKFLOW_HOST', '').strip() or 'lazymind'
        self.timeout = timeout
        self.execution_timeout = execution_timeout
        self.read_retries = read_retries
        self.transport = transport
        self.trace_context = trace_context

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
        return self._read('/workflow-runtime/v1/workflows')

    def get_workflow(self, workflow_id: str, revision_id: str = '') -> WorkflowResponse:
        query = ('?' + urlencode({'revision_id': revision_id})) if revision_id else ''
        return self._read(
            f'/workflow-runtime/v1/workflows/{quote(workflow_id, safe="")}{query}'
        )

    def get_state(self, session_id: str) -> Dict[str, Any]:
        return self._read(f'/workflow-sessions/{session_id}/projection').result

    def get_ready_steps(self, session_id: str) -> Dict[str, Any]:
        state = self.get_state(session_id)
        projection = state.get('projection') if isinstance(state.get('projection'), dict) else state
        nodes = projection.get('nodes') if isinstance(projection.get('nodes'), dict) else {}

        def step_ids(value: Any) -> List[str]:
            values = value if isinstance(value, list) else []
            return [
                step_id for item in values
                if (step_id := str(
                    item.get('step_id') if isinstance(item, dict) else item or '',
                ).strip())
            ]

        def step_details(step_ids: List[str]) -> List[Dict[str, Any]]:
            details: List[Dict[str, Any]] = []
            for step_id in step_ids:
                node = nodes.get(step_id) if isinstance(nodes.get(step_id), dict) else {}
                mode = str(node.get('mode') or '').strip()
                requires_approval = (
                    bool(node.get('requires_approval')) if 'requires_approval' in node
                    else mode == 'human'
                )
                details.append({
                    'step_id': step_id,
                    'mode': mode,
                    'requires_approval': requires_approval,
                    'default_approval': 'required' if requires_approval else 'not_required',
                    'approval_timing': (
                        'after_step_execution' if requires_approval else 'none'
                    ),
                    'execution_tool': (
                        'advance_step_and_hand_off' if requires_approval else 'advance_step'
                    ),
                })
            return details

        ready = step_ids(projection.get('ready_steps', projection.get('ready', [])))
        ready_details = step_details(ready)
        return {
            'session_id': session_id, 'state_version': state.get('state_version'),
            'ready_steps': ready,
            'ready_step_details': ready_details,
            'approval_by_step': {
                item['step_id']: item['default_approval'] for item in ready_details
            },
            'retryable_steps': step_ids(projection.get(
                'retryable_steps', projection.get('retryable', []))),
            'rewindable_steps': step_ids(projection.get(
                'rewindable_steps', projection.get('rewindable', []))),
            'projection': state,
        }

    def iter_events(self, session_id: str, after_event_id: int = 0) -> Iterator[Dict[str, Any]]:
        """Yield the durable SSE stream, reconnecting with Last-Event-ID for replay."""
        last_id = max(0, after_event_id)
        while True:
            headers = self._headers()
            if last_id:
                headers['Last-Event-ID'] = str(last_id)
            try:
                with self.transport.stream(
                    'GET', f'{self.base_url}/workflow-sessions/{quote(session_id, safe="")}/events',
                    headers=headers, timeout=None,
                ) as response:
                    if response.status_code >= 400:
                        self._decode(response)
                    event: Dict[str, Any] = {}
                    for line in response.iter_lines():
                        if not line:
                            if 'data' in event:
                                if 'id' in event:
                                    last_id = int(event['id'])
                                yield event
                            event = {}
                        elif line.startswith('id:'):
                            event['id'] = line[3:].strip()
                        elif line.startswith('event:'):
                            event['event'] = line[6:].strip()
                        elif line.startswith('data:'):
                            event['data'] = json.loads(line[5:].strip())
            except (httpx.TimeoutException, httpx.TransportError):
                time.sleep(0.2)

    def prepare_workflow(self, workflow_id: str, *, input_bindings: Optional[Dict[str, Any]] = None,
                         command_id: str = '', fields: Optional[Dict[str, Any]] = None) -> WorkflowResponse:
        command_id = command_id or str(uuid.uuid4())
        payload = {**(fields or {}), 'workflow_id': workflow_id, 'preparation_id': command_id,
                   'idempotency_key': command_id, 'input_bindings': input_bindings or {},
                   'origin_host': self.host, 'controller_host': self.host}
        return self._decode(self.transport.post(
            self.base_url + '/workflow-preparations', json=payload,
            headers=self._headers(command_id), timeout=self.timeout,
        ))

    def import_input_resource(self, name: str, mime_type: str, content: bytes) -> WorkflowResponse:
        import hashlib
        digest = 'sha256:' + hashlib.sha256(content).hexdigest()
        return self._decode(self.transport.post(
            self.base_url + '/workflow-input-resources',
            json={'contract_version': CONTRACT_VERSION, 'name': name, 'mime_type': mime_type,
                  'size': len(content), 'content_hash': digest,
                  'content_base64': base64.b64encode(content).decode('ascii')},
            headers=self._headers(), timeout=self.timeout,
        ))

    def read_input_resource(self, resource_id: str) -> Dict[str, Any]:
        result = self._read(
            f'/workflow-input-resources/{quote(resource_id, safe="")}').result
        encoded = str(result.get('content_base64') or '')
        result['content'] = base64.b64decode(encoded) if encoded else b''
        return result

    def list_workflow_inputs(self, session_id: str) -> WorkflowResponse:
        return self._read(
            f'/workflow-sessions/{quote(session_id, safe="")}/input-bindings')

    def bind_workflow_input(self, session_id: str, material_id: str, resource: Dict[str, Any],
                            command_id: str = '') -> WorkflowResponse:
        command_id = command_id or str(uuid.uuid4())
        return self._decode(self.transport.post(
            f'{self.base_url}/workflow-sessions/{quote(session_id, safe="")}/input-bindings',
            json={'material_id': material_id, 'resource_type': 'input_resource',
                  'resource_id': resource['resource_id'], 'resource_revision': resource['revision'],
                  'content_hash': resource['content_hash'], 'command_id': command_id},
            headers=self._headers(command_id), timeout=self.timeout,
        ))

    def stop_workflow(self, session_id: str, command_id: str = '') -> WorkflowResponse:
        return self._lifecycle(session_id, 'stop', command_id)

    def resume_workflow(self, session_id: str, command_id: str = '') -> WorkflowResponse:
        return self._lifecycle(session_id, 'resume', command_id)

    def _lifecycle(self, session_id: str, action: str, command_id: str) -> WorkflowResponse:
        command_id = command_id or str(uuid.uuid4())
        return self._decode(self.transport.post(
            f'{self.base_url}/workflow-sessions/{quote(session_id, safe="")}:{action}',
            json={'command_id': command_id}, headers=self._headers(command_id), timeout=self.timeout,
        ))

    def get_command(self, command_id: str) -> WorkflowResponse:
        return self._read(f'/workflow-commands/{quote(command_id, safe="")}')

    def list_artifacts(self, session_id: str) -> WorkflowResponse:
        return self._read(f'/workflow-sessions/{quote(session_id, safe="")}/artifacts')

    def get_slot_order(self, session_id: str, slot_id: str) -> WorkflowResponse:
        """Read the durable visual-order to list-index mapping for one slot."""
        return self._read(
            f'/workflow-sessions/{quote(session_id, safe="")}/slots/'
            f'{quote(slot_id, safe="")}/order'
        )

    def read_artifact(self, artifact_id: str) -> WorkflowResponse:
        return self._read(f'/workflow-artifacts/{quote(artifact_id, safe="")}')

    def patch_artifact(self, artifact_id: str, base_revision: int, value: Any,
                       content_type: str = 'json', caption: str = '',
                       command_id: str = '') -> WorkflowResponse:
        command_id = command_id or str(uuid.uuid4())
        payload: Dict[str, Any] = {
            'base_revision': base_revision, 'value': value,
            'content_type': content_type, 'command_id': command_id,
        }
        if caption:
            payload['caption'] = caption
        return self._decode(self.transport.patch(
            f'{self.base_url}/workflow-artifacts/{quote(artifact_id, safe="")}', json=payload,
            headers=self._headers(command_id), timeout=self.timeout,
        ))

    def delete_artifact(self, artifact_id: str, base_revision: int,
                        command_id: str = '') -> WorkflowResponse:
        """Create an immutable deletion tombstone for the selected revision."""
        command_id = command_id or str(uuid.uuid4())
        return self._decode(self.transport.delete(
            f'{self.base_url}/workflow-artifacts/{quote(artifact_id, safe="")}',
            json={'base_revision': base_revision, 'command_id': command_id},
            headers=self._headers(command_id), timeout=self.timeout,
        ))

    def start_workflow(self, preparation_id: str, session_id: str = '',
                       *, command_id: str = '') -> WorkflowResponse:
        command_id = command_id or preparation_id
        return self._decode(self.transport.post(
            f'{self.base_url}/workflow-preparations/{preparation_id}:consume',
            json={'session_id': session_id} if session_id else {},
            headers=self._headers(command_id), timeout=self.timeout,
        ))

    def advance(self, request: AdvanceRequest) -> WorkflowResponse:
        tool = 'advance_step_and_hand_off' if request.handoff else 'advance_step'
        payload = {'contract_version': CONTRACT_VERSION, 'command_id': request.command_id,
                   'tool': tool, 'session_id': request.session_id,
                   'expected_state_version': request.expected_state_version,
                   'retry_origin': request.retry_origin,
                   'steps': [asdict(step) for step in request.steps]}
        if self.trace_context is not None:
            context = self.trace_context()
            if context.trace_id and context.parent_span_id:
                payload['trace_id'] = context.trace_id
                payload['parent_span_id'] = context.parent_span_id
        path = f'/workflow-sessions/{request.session_id}:' + (
            'advance-step-and-hand-off' if request.handoff else 'advance-step')
        try:
            response = self.transport.post(
                self.base_url + path, json=payload, headers=self._headers(request.command_id),
                timeout=self.execution_timeout,
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
        started = self.start_workflow(preparation_id, session_id, command_id=command_id)
        target = str(start_fields.get('target_step_id') or '')
        # Compatibility starts may already execute their first step. The public
        # lifecycle creates a Session first and advances only through the same
        # common transition tool used for every later step.
        if not target or started.result.get('accepted') or started.result.get('task_id'):
            return started
        return self.advance(AdvanceRequest(
            session_id=session_id,
            expected_state_version=int(started.result.get('state_version') or 1),
            steps=[StepCommand(
                step_id=target, task_id=str(start_fields.get('task_id') or ''),
                objective=str(start_fields.get('objective') or ''),
                user_input=str(start_fields.get('user_input') or ''),
            )],
            handoff=bool(start_fields.get('hand_off')),
            command_id=str(uuid.uuid4()),
        ))

    def get_skill_conversion_context(self, skill_id: str,
                                     revision_id: str = '') -> WorkflowResponse:
        query = {'skill_id': skill_id}
        if revision_id:
            query['revision_id'] = revision_id
        return self._read('/workflow-authoring/v1/skill-context?' + urlencode(query))

    def list_skills(self) -> WorkflowResponse:
        return self._read('/skills')

    def create_workflow_draft(self, name: str, skill_id: str = '', revision_id: str = '',
                              tree_hash: str = '', files: Optional[Dict[str, str]] = None,
                              source_type: str = '') -> WorkflowResponse:
        source_type = source_type or ('skill' if skill_id else 'blank')
        return self._decode(self.transport.post(
            self.base_url + '/workflow-authoring/v1/drafts',
            json={'name': name, 'skill_id': skill_id, 'revision_id': revision_id,
                  'tree_hash': tree_hash, 'files': files or {}, 'source_type': source_type},
            headers=self._headers(), timeout=self.timeout,
        ))

    def list_workflow_drafts(self) -> WorkflowResponse:
        return self._read('/workflow-drafts')

    def get_workflow_draft(self, draft_id: str) -> WorkflowResponse:
        return self._read(f'/workflow-drafts/{quote(draft_id, safe="")}')

    def delete_workflow_draft(self, draft_id: str) -> WorkflowResponse:
        return self._decode(self.transport.delete(
            f'{self.base_url}/workflow-drafts/{quote(draft_id, safe="")}',
            headers=self._headers(), timeout=self.timeout,
        ))

    def list_workflow_versions(self, workflow_ref: str) -> WorkflowResponse:
        return self._read(f'/published-workflows/{quote(workflow_ref, safe="")}/versions')

    def archive_workflow(self, workflow_ref: str) -> WorkflowResponse:
        return self._decode(self.transport.post(
            f'{self.base_url}/published-workflows/{quote(workflow_ref, safe="")}:archive',
            json={}, headers=self._headers(), timeout=self.timeout,
        ))

    def restore_workflow(self, workflow_ref: str) -> WorkflowResponse:
        return self._decode(self.transport.post(
            f'{self.base_url}/published-workflows/{quote(workflow_ref, safe="")}:restore',
            json={}, headers=self._headers(), timeout=self.timeout,
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

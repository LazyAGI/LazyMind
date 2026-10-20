"""Run-bound policy and credentials for the built-in knowledge management MCP."""
from __future__ import annotations

import asyncio
import copy
import json
import os
from functools import wraps
import time
from contextlib import contextmanager
from contextvars import ContextVar
from types import MappingProxyType

from lazyllm.tools import get_tool_runtime_metadata
from lazyllm.tools.agent import HostFile, ToolExecutionError
from lazyllm.tools.agent.base import _write_agent_data
from lazyllm.tools.agent.tool_runtime import _set_tool_runtime_metadata

from lazymind.chat.engine.agent_runtime.workspace_authorization import digest, response_data
from lazymind.chat.engine.tools.infra.core_api_client import CoreAPIError, post_core_api
from lazymind.chat.engine.tools.workspace_context import thaw


BUILTIN_ID = 'lazymind-knowledge'
MANAGEMENT_TOOLS = (
    'knowledge.list', 'knowledge.document.list', 'knowledge.document.aggregate',
    'knowledge.create', 'knowledge.folder.create', 'knowledge.resource.update_metadata',
    'knowledge.resource.move', 'knowledge.resource.delete', 'knowledge.document.upload',
)
_REQUEST = ContextVar('knowledge_management_request', default=None)


class _CoreManagementError(ToolExecutionError):
    def __init__(self, body, status_code):
        error = body.get('error') if isinstance(body, dict) else None
        if isinstance(error, dict) and isinstance(error.get('code'), str):
            details = {key: copy.deepcopy(error[key]) for key in ('code', 'message', 'details') if key in error}
        else:
            details = {'code': 'KNOWLEDGE_REQUEST_FAILED', 'message': 'Knowledge management request failed',
                       'details': {'status_code': status_code}}
        self.envelope = {'content': [], 'structuredContent': details, 'isError': True}
        super().__init__(json.dumps(self.envelope, ensure_ascii=False))


def _resolve_upload_files(arguments):
    from lazymind.chat.engine.tools.host_file_resolution import FileResolution
    resolution = FileResolution()
    arguments = copy.deepcopy(arguments)
    for item in arguments.get('files') or []:
        item['path'] = resolution.local(item.get('path'))
    return resolution.finish(arguments)


def requires_confirmation(wire_name, arguments):
    if wire_name == 'knowledge.resource.delete':
        return True
    if wire_name != 'knowledge.resource.move':
        return False
    destination = (arguments.get('destination') or {}).get('knowledge_base_id')
    sources = arguments.get('sources') or []
    # Malformed move requests must not be classified as harmless by omission.
    return not destination or not sources or any(
        source.get('knowledge_base_id') != destination for source in sources)


class KnowledgeManagementRuntime:
    """Capture trusted execution once; Core still checks live account and lease ownership."""

    def __init__(self, execution, core_api_url, *, allowed_tools=None, cancel_check=None):
        execution = copy.deepcopy(execution)
        if not execution.get('user_id') or not execution.get('conversation_id') or not (
            (execution.get('history_id') and execution.get('run_id')) or
            (execution.get('task_id') and execution.get('generation'))
        ):
            raise ToolExecutionError('Knowledge management requires an active execution identity')
        if any(execution.get(key) for key in ('attempt_id', 'workflow_id', 'step_id')) and not execution.get('lease_token'):
            raise ToolExecutionError('Knowledge management requires an active workflow lease')
        self.execution = MappingProxyType(execution)
        self.core_api_url = core_api_url.rstrip('/')
        self.url = self.core_api_url + '/internal/mcp/knowledge-management'
        self.allowed_tools = tuple(name for name in MANAGEMENT_TOOLS
                                   if allowed_tools is None or name in allowed_tools)
        self.cancel_check = cancel_check
        self._tools = {}
        self._uncertain_calls = set()

    def _post(self, operation, payload):
        try:
            return response_data(post_core_api('internal/knowledge-management:' + operation,
                                               payload, user_id=self.execution['user_id']))
        except CoreAPIError as error:
            raise _CoreManagementError(error.message, error.status_code) from None

    def server_config(self, config):
        requested = config.get('allowed_tools')
        allowed = [name for name in self.allowed_tools if requested is None or name in requested]
        config = {key: value for key, value in config.items() if key not in ('oauth', 'auth_type', 'auth_recovery')}
        return {**config, 'id': BUILTIN_ID, 'name': BUILTIN_ID, 'url': self.url,
                'transport': 'streamable-http', 'allowed_tools': allowed, 'result_mode': 'structured',
                'headers': None, 'auth_provider': self.headers,
                'request_metadata_provider': self.request_metadata,
                '_knowledge_runtime': self}

    async def headers(self):
        try:
            result = await asyncio.to_thread(self._post, 'credential', {'execution': dict(self.execution)})
        except _CoreManagementError as error:
            current = _REQUEST.get()
            if current and current[0] is self:
                current[6]['error'] = error.envelope
            raise
        token = result.get('token')
        if not isinstance(token, str) or not token:
            raise ToolExecutionError('Knowledge management credentials unavailable')
        return {'Authorization': 'Bearer ' + token}

    def bind_tools(self, tools):
        bound = []
        for tool in tools:
            name = getattr(tool, '__mcp_tool_name__', '')
            if name not in self.allowed_tools:
                raise ToolExecutionError('Knowledge management tool is outside the allowed scope')
            metadata = get_tool_runtime_metadata(tool)
            if metadata.tool_source != 'mcp' or metadata.tool_origin != BUILTIN_ID:
                raise ToolExecutionError('Knowledge management tool provenance is invalid')
            self._tools[metadata.tool_identity] = name
            if name == 'knowledge.document.upload':
                _set_tool_runtime_metadata(tool, {'host_file': _resolve_upload_files})
            else:
                _set_tool_runtime_metadata(tool, {'host_file': HostFile.NONE})
            bound.append(self._dispatch_tool(tool))
        return bound

    def _dispatch_tool(self, tool):
        @wraps(tool)
        def dispatch(**arguments):
            current = _REQUEST.get()
            if not current or current[0] is not self or current[4] != digest(arguments):
                raise ToolExecutionError('Knowledge management has no matching trusted dispatch')
            result = tool(**copy.deepcopy(current[5]))
            error = current[6].get('error')
            return json.dumps(error, ensure_ascii=False) if error is not None else result
        return dispatch

    async def request_metadata(self, wire_name, arguments):
        current = _REQUEST.get()
        if not current or current[0] is not self or current[1] != wire_name or current[2] != digest(arguments):
            raise ToolExecutionError('Knowledge management call has no matching trusted dispatch')
        return dict(current[3])

    @contextmanager
    def execution_context(self, prepared):
        if prepared.tool_origin != BUILTIN_ID:
            yield
            return
        wire_name = self._tools.get(prepared.tool_identity)
        if (prepared.tool_source != 'mcp' or wire_name not in self.allowed_tools or not prepared.call_id):
            raise ToolExecutionError('Knowledge management dispatch provenance is invalid')
        arguments = thaw(prepared.validated_arguments)
        original_digest = digest(arguments)
        if (wire_name, original_digest) in self._uncertain_calls:
            raise ToolExecutionError('Previous knowledge mutation outcome is uncertain; reconcile its receipt before retrying')
        arguments_digest = digest(arguments)
        payload = {'execution': dict(self.execution), 'wire_name': wire_name,
                   'arguments': arguments, 'call_id': prepared.call_id}
        self._check_cancel()
        prepared_state = self._post('prepare', payload)
        needs_confirmation = requires_confirmation(wire_name, arguments)
        if (prepared_state.get('arguments_digest') != arguments_digest or not prepared_state.get('operation_key')
                or not prepared_state.get('snapshot')
                or prepared_state.get('requires_confirmation') is not needs_confirmation):
            raise ToolExecutionError('Knowledge management preflight mismatch')
        status = prepared_state.get('status')
        confirmation_id = prepared_state.get('confirmation_id')
        if needs_confirmation and not confirmation_id:
            raise ToolExecutionError('Knowledge management confirmation unavailable')
        if status == 'pending':
            if not needs_confirmation:
                raise ToolExecutionError('Unexpected knowledge management confirmation')
            preview = prepared_state.get('preview') or {}
            _write_agent_data('ask_pending', ask_id=confirmation_id,
                              title=preview.get('title') or 'Confirm knowledge management operation',
                              description=preview.get('description') or '',
                              knowledge_management_preview=copy.deepcopy(preview),
                              questions=[{'text': preview.get('title') or 'Proceed with this operation?',
                                          'type': 'boolean', 'choices': ['__ask_user_yes__', '__ask_user_no__']}],
                              knowledge_management={'confirmation_id': confirmation_id,
                                                    'arguments_digest': arguments_digest,
                                                    'snapshot': prepared_state['snapshot']})
            deadline = time.monotonic() + 300
            while status == 'pending' and time.monotonic() < deadline:
                self._check_cancel()
                response = self._post('status', {'execution': dict(self.execution),
                                                 'confirmation_id': confirmation_id})
                if (response.get('snapshot') != prepared_state['snapshot']
                        or response.get('arguments_digest') != arguments_digest):
                    raise ToolExecutionError('Knowledge management confirmation snapshot changed')
                status = response.get('status')
                if status == 'pending':
                    time.sleep(.2)
        if status != 'allowed':
            raise ToolExecutionError('Knowledge management confirmation ' + str(status or 'unavailable'))
        self._check_cancel()
        claim = self._post('claim', {**payload, 'operation_key': prepared_state['operation_key'],
                                   'arguments_digest': arguments_digest, 'snapshot': prepared_state['snapshot'],
                                   'confirmation_id': confirmation_id or ''})
        metadata = claim.get('metadata')
        if (claim.get('status') != 'allowed' or not isinstance(metadata, dict)
                or metadata.get('operation_key') != prepared_state['operation_key']):
            raise ToolExecutionError('Knowledge management claim unavailable')
        token = _REQUEST.set((self, wire_name, arguments_digest, copy.deepcopy(metadata), original_digest, arguments, {}))
        try:
            yield
        finally:
            _REQUEST.reset(token)

    def record_result(self, prepared, result):
        wire_name = self._tools.get(prepared.tool_identity)
        if prepared.tool_origin != BUILTIN_ID or wire_name is None or not isinstance(result, dict):
            return result
        value = result.get('value')
        try:
            envelope = json.loads(value) if isinstance(value, str) else value
        except (TypeError, ValueError):
            return result
        if not isinstance(envelope, dict):
            return result
        if envelope.get('isError') is not True:
            operation = envelope.get('structuredContent', envelope)
            if isinstance(operation, dict) and operation.get('action') == wire_name and isinstance(operation.get('items'), list):
                items = [{key: copy.deepcopy(item[key]) for key in (
                    'resource_ref', 'name', 'resource_status', 'processing_status', 'cleanup_status', 'error'
                ) if key in item} for item in operation['items'] if isinstance(item, dict)]
                _write_agent_data('knowledge_operation_result', call_id=prepared.call_id,
                                  action=wire_name, status=operation.get('status'), items=items)
            return result
        details = envelope.get('structuredContent') or {}
        if (isinstance(details, dict) and details.get('code') == 'MCP_CALL_FAILED'
                and wire_name not in MANAGEMENT_TOOLS[:3]):
            self._uncertain_calls.add((wire_name, digest(thaw(prepared.validated_arguments))))
        return {**result, 'ok': False}

    def _check_cancel(self):
        if self.cancel_check is not None:
            self.cancel_check(None)


def build_knowledge_runtime(agentic_config, *, cancel_check=None):
    """Only Core's system entry enables this provider; callbacks never come from a model."""
    from lazymind.config import config
    server = next((item for item in agentic_config.get('system_mcp_config') or []
                   if item.get('id') == BUILTIN_ID), None)
    if server is None:
        return None
    execution = {**(agentic_config.get('_workspace_execution') or {}),
                 'user_id': agentic_config.get('user_id'),
                 'conversation_id': agentic_config.get('conversation_id')}
    try:
        return KnowledgeManagementRuntime(execution, str(config['core_api_url']),
                                          allowed_tools=server.get('allowed_tools'), cancel_check=cancel_check)
    except ToolExecutionError:
        return None


def bind_knowledge_server(server, runtime):
    """Canonicalize duplicate connections to Core so aliases cannot bypass policy."""
    from lazymind.config import config
    base = str(config['core_api_url']).rstrip('/')
    known_urls = {base + '/internal/mcp/knowledge-management', base + '/mcp/capabilities/v1'}
    if server.get('id') != BUILTIN_ID and str(server.get('url') or '').rstrip('/') not in known_urls:
        return server
    if runtime is None:
        return {**server, 'allowed_tools': []}
    return runtime.server_config(server)

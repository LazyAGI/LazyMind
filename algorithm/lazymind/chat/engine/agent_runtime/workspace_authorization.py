"""Request-local Core approval barrier and one-use local execution lifecycle."""
from __future__ import annotations

import copy
from contextlib import contextmanager
import hashlib
import json
import os
import time
import uuid
from dataclasses import dataclass
from types import MappingProxyType

from lazyllm.tools.agent import ToolExecutionError

from lazymind.chat.engine.tools.infra.core_api_client import get_core_api, post_core_api
from lazymind.chat.engine.tools.approved_local_io import LocalPath
from lazymind.chat.engine.tools.local_fs import LocalFileToolkit
from .cancellation import UserCancelledError
from lazymind.chat.engine.tools.host_access_guard import HostAccessGuard, host_access_scope
from lazymind.chat.engine.tools.workspace_context import (
    thaw, local_access_scope, workspace_permission_scope,
)


def digest(arguments):
    return hashlib.sha256(json.dumps(arguments, sort_keys=True, ensure_ascii=False,
                                     separators=(',', ':')).encode()).hexdigest()


def response_data(response):
    body = response.get('response', response)
    return body.get('data', body) if isinstance(body, dict) else {}


@dataclass
class AuthorizedCall:
    prepared: object
    toolkit: object
    method: str
    path: LocalPath
    payload: object
    status: dict
    operation_id: str
    result: object = None

    def execute(self, method, arguments):
        if method != self.method:
            raise ToolExecutionError('workspace authorization unavailable')
        self.result = self.path.execute(method, arguments)
        return self.result


class WorkspaceAuthorization:
    def __init__(self, permission, cancel_check=None):
        self.permission = permission
        config = thaw(permission.config)
        binding = LocalFileToolkit._workspace_binding_from_config(config) or {}
        parent = config.get('parent_agentic_config') or {}
        identity = config.get('_workspace_execution') or {}
        context = {key: str(config.get(key) or '') for key in ('user_id', 'conversation_id')}
        context['workspace_id'] = str(binding.get('workspace_id') or '')
        identity = {key: identity[key] for key in (
            'history_id', 'run_id', 'task_id', 'generation', 'attempt_id', 'lease_token',
        ) if key in identity}
        roots = [path for cfg in (config, parent) for source in cfg.get('local_fs_sources', [])
                 if isinstance(source, dict) and source.get('source_id') == 'local-workspace:' + context['workspace_id']
                 for path in source.get('paths', []) if isinstance(path, str) and os.path.isabs(path)]
        if (not all(context.values()) or not roots or len(set(roots)) != 1 or not (
            (identity.get('history_id') and identity.get('run_id'))
            or (identity.get('task_id') and identity.get('generation'))
        )):
            raise ToolExecutionError('workspace authorization unavailable')
        self.root = os.path.realpath(roots[0])
        self.context = MappingProxyType({**context, **identity})
        self.cancel_check = cancel_check
        self.base = f"internal/conversations/{context['conversation_id']}/workspace-operations"
        self.calls = {}
        self.operations = []
        self.by_call = {}
        self.guards = {}
        self.execution_states = {}
        self.previous = {}
        self.invocation_id = f'{int(time.time() * 1000)}/{uuid.uuid4().hex}'

    def check_cancel(self):
        if self.cancel_check is not None:
            self.cancel_check(None)

    def _post(self, suffix, payload):
        return response_data(post_core_api(self.base + suffix, payload, user_id=self.context['user_id']))

    def prepare(self, prepared, versions):
        self.check_cancel()
        toolkit, method = None, prepared.tool_name.removeprefix('LocalFileToolkit_')
        args = thaw(prepared.validated_arguments)
        target = args.get('filepath', args.get('path'))
        local_path = LocalPath(target, self.cancel_check)
        try:
            payload = {
                **self.context, 'execution_mode': 'local',
                'call_id': f'{self.invocation_id}:{prepared.index}:{digest(prepared.call_id)}',
                'tool_name': prepared.tool_name,
                'operation': 'replace' if method == 'string_replace' else method,
                'path': local_path.path, 'parent_identity': local_path.parent_identity,
                'target_identity': local_path.target_identity, 'arguments_digest': digest(args),
            }
            fields = {'content': 'content', 'new_string': 'content', 'old_string': 'old_content',
                      'expected_replacements': 'expected_replacements', 'pattern': 'pattern', 'glob': 'glob',
                      'max_entries': 'limit', 'max_results': 'limit', 'start_line': 'offset', 'max_lines': 'max_lines'}
            payload.update({dest: args[key] for key, dest in fields.items() if key in args})
            if local_path.path in self.previous:
                payload['depends_on'] = self.previous[local_path.path]
            version = args.get('expected_version') or (
                versions.get(local_path.path, '') if 'depends_on' not in payload else ''
            )
            if version:
                payload['expected_version'] = version
            status = {}
            operation_id = self._operation_id(payload)
            call = AuthorizedCall(prepared, toolkit, method, local_path, MappingProxyType(payload), status, operation_id)
            self.calls[prepared.index] = call
            self.operations.append(call)
            self.by_call[prepared.index] = [call]
            self.previous[local_path.path] = operation_id
        except BaseException:
            local_path.close()
            raise

    @staticmethod
    def _operation_id(payload):
        values = [str(payload.get(key) or '') for key in (
            'user_id', 'conversation_id', 'run_id', 'history_id', 'task_id', 'generation',
            'attempt_id', 'lease_token', 'call_id',
        )]
        if payload.get('execution_mode') == 'host_access':
            values += ['host_access', payload['host_intent_id']]
        # Go encoding/json escapes HTML characters in identity fields.
        encoded = json.dumps(values, ensure_ascii=False, separators=(',', ':'))
        for char, escaped in (('&', r'\u0026'), ('<', r'\u003c'), ('>', r'\u003e'),
                              ('\u2028', r'\u2028'), ('\u2029', r'\u2029')):
            encoded = encoded.replace(char, escaped)
        return hashlib.sha256(encoded.encode()).hexdigest()

    def prepare_host(self, prepared):
        self.guards[prepared.index] = HostAccessGuard(prepared.host_files)
        entries = []
        for offset, intent in enumerate(prepared.host_files):
            payload = {
                **self.context, 'execution_mode': 'host_access',
                'call_id': f'{self.invocation_id}:{prepared.index}:{digest(prepared.call_id)}',
                'host_intent_id': str(offset), 'tool_name': prepared.tool_name,
                'operation': intent.operation, 'path': intent.path,
                'arguments_digest': digest(thaw(prepared.validated_arguments)),
            }
            entry = AuthorizedCall(prepared, None, '', None, MappingProxyType(payload), {},
                                   self._operation_id(payload))
            entries.append(entry)
            self.operations.append(entry)
        self.by_call[prepared.index] = entries

    def submit(self):
        if not self.operations:
            return
        if len(self.operations) > 16:
            raise ToolExecutionError('workspace authorization unavailable')
        result = self._post(':prepare-batch', {'calls': [dict(call.payload) for call in self.operations]})
        statuses = result.get('operations')
        if not isinstance(statuses, list) or len(statuses) != len(self.operations):
            raise ToolExecutionError('workspace authorization unavailable')
        for call, status in zip(self.operations, statuses):
            if not isinstance(status, dict) or status.get('operation_id') != call.operation_id:
                raise ToolExecutionError('workspace authorization unavailable')
            call.status = status
            if call.path is not None:
                call.path.permission_mode = status.get('permission_mode', 'always_ask')

    def wait(self):
        """No tool executes here; prepare every item before polling any decision."""
        while True:
            self.check_cancel()
            pending = []
            for call in self.operations:
                status = call.status
                if status.get('status') not in {'allowed', 'pending', 'preparing', 'rejected', 'expired', 'failed'}:
                    raise ToolExecutionError('workspace authorization unavailable')
                if status.get('status') in {'allowed', 'pending', 'preparing'}:
                    deadline = status.get('expires_at', 0)
                    if not deadline or time.time() * 1000 >= deadline:
                        call.status = {'status': 'expired', 'decision': 'denied'}
                        continue
                if status.get('status') in {'pending', 'preparing'}:
                    pending.append(call)
            if not pending:
                return {index for index, calls in self.by_call.items()
                        if all(call.status.get('status') == 'allowed' and call.status.get('decision') == 'allowed'
                               for call in calls)}
            time.sleep(0.2)
            for call in pending:
                self.check_cancel()
                params = {key: value for key, value in self.context.items()
                          if key in {'run_id', 'history_id', 'task_id', 'generation', 'attempt_id'}}
                call.status = response_data(get_core_api(
                    self.base + '/' + call.operation_id, params, user_id=self.context['user_id'],
                ))

    @contextmanager
    def execution_context(self, prepared, versions):
        entries = self.by_call.get(prepared.index, [])
        claimed_entries = []
        local = self.calls.get(prepared.index)
        self.execution_states[prepared.index] = False
        try:
            self.check_cancel()
            guard = self.guards.get(prepared.index)
            if guard is not None:
                guard.validate()
            for call in entries:
                claimed = self._post('/' + call.operation_id + ':claim', dict(call.payload))
                if claimed.get('execute_allowed') is not True or claimed.get('status') != 'executing':
                    raise ToolExecutionError('workspace authorization denied')
                claimed_entries.append(call)
                if call.path is not None:
                    call.path.target_identity = str(claimed.get('target_identity') or '')
                    call.path.expected_version = str(claimed.get('version') or '')
            with workspace_permission_scope(self.permission), local_access_scope(local), host_access_scope(guard):
                if local is None:
                    self.execution_states[prepared.index] = True
                yield
            self.execution_states[prepared.index] = True
        except BaseException as error:
            if local is not None:
                self.execution_states[prepared.index] = local.path.started
            touched = local.path.touched if local else self.execution_states[prepared.index]
            for call in claimed_entries:
                try:
                    self._post('/' + call.operation_id + ':complete', {
                        **call.payload, 'status': 'uncertain' if touched else 'failed',
                        'reason': 'operation_uncertain' if touched else 'path_invalid',
                    })
                except Exception:
                    if not isinstance(error, UserCancelledError):
                        raise ToolExecutionError('operation_uncertain') from None
            if isinstance(error, (UserCancelledError, ToolExecutionError)):
                raise
            raise ToolExecutionError('workspace authorization unavailable') from None
        else:
            for call in claimed_entries:
                fields = {}
                if call.path is not None:
                    fields = {'version': (call.result or {}).get('version', ''),
                              'result_identity': call.path.result_identity()}
                try:
                    result = self._post('/' + call.operation_id + ':complete', {
                        **call.payload, 'status': 'completed', **fields,
                    })
                    if result.get('status') != 'completed':
                        raise ToolExecutionError('operation_uncertain')
                except Exception:
                    raise ToolExecutionError('operation_uncertain') from None
                if call.path is not None:
                    if call.method == 'delete':
                        versions.pop(call.path.path, None)
                    elif fields.get('version'):
                        versions[call.path.path] = fields['version']

    def close(self):
        for guard in self.guards.values():
            guard.close()
        for call in self.operations:
            if call.path is not None:
                call.path.close()

"""Request-local Core approval barrier and one-use local execution lifecycle."""
from __future__ import annotations

from contextlib import contextmanager
import hashlib
import json
import time
import uuid
from dataclasses import dataclass
from types import MappingProxyType

from lazyllm.tools.agent import HostFileAccess, ToolExecutionError

from lazymind.chat.engine.tools.infra.core_api_client import get_core_api, post_core_api
from .cancellation import UserCancelledError
from lazymind.chat.engine.tools.host_access_guard import HostAccessGuard, host_access_scope
from lazymind.chat.engine.tools.workspace_context import thaw, workspace_permission_scope


def digest(arguments):
    return hashlib.sha256(json.dumps(arguments, sort_keys=True, ensure_ascii=False,
                                     separators=(',', ':')).encode()).hexdigest()


def response_data(response):
    body = response.get('response', response)
    return body.get('data', body) if isinstance(body, dict) else {}


@dataclass
class AuthorizedCall:
    prepared: object
    payload: object
    status: dict
    operation_id: str


class WorkspaceAuthorization:
    def __init__(self, permission, cancel_check=None, run_grants=None):
        self.permission = permission
        self.run_grants = run_grants if run_grants is not None else set()
        context = {
            'user_id': permission.user_id,
            'conversation_id': permission.conversation_id,
            'workspace_id': permission.workspace_id,
        }
        identity = dict(permission.execution)
        self.context = MappingProxyType({**context, **identity})
        self.cancel_check = cancel_check
        self.base = f"internal/conversations/{context['conversation_id']}/workspace-operations"
        self.operations = []
        self.by_call = {}
        self.guards = {}
        self.execution_states = {}
        self.invocation_id = f'{int(time.time() * 1000)}/{uuid.uuid4().hex}'

    def check_cancel(self):
        if self.cancel_check is not None:
            self.cancel_check(None)

    def _post(self, suffix, payload):
        return response_data(post_core_api(self.base + suffix, payload, user_id=self.context['user_id']))

    @staticmethod
    def _operation_id(payload):
        values = [str(payload.get(key) or '') for key in (
            'user_id', 'conversation_id', 'run_id', 'history_id', 'task_id', 'generation',
            'attempt_id', 'lease_token', 'call_id',
        )]
        values += ['host_access', payload['host_intent_id']]
        # Go encoding/json escapes HTML characters in identity fields.
        encoded = json.dumps(values, ensure_ascii=False, separators=(',', ':'))
        for char, escaped in (('&', r'\u0026'), ('<', r'\u003c'), ('>', r'\u003e'),
                              ('\u2028', r'\u2028'), ('\u2029', r'\u2029')):
            encoded = encoded.replace(char, escaped)
        return hashlib.sha256(encoded.encode()).hexdigest()

    def prepare_guard(self, prepared):
        self.guards[prepared.index] = HostAccessGuard(prepared.host_files)

    def prepare_host(self, prepared):
        identity = self.permission.execution
        if not self.permission.active or not self.permission.user_id or not self.permission.conversation_id or not (
            (identity.get('history_id') and identity.get('run_id'))
            or (identity.get('task_id') and identity.get('generation'))
        ):
            raise ToolExecutionError('workspace authorization unavailable')
        self.prepare_guard(prepared)
        entries = []
        for offset, intent in enumerate(prepared.host_files or (None,)):
            payload = {
                **self.context, 'execution_mode': 'host_access',
                'call_id': f'{self.invocation_id}:{prepared.index}:{digest(prepared.call_id)}',
                'host_intent_id': str(offset), 'tool_name': prepared.tool_name,
                'operation': intent.operation if intent is not None else 'shell',
                'path': intent.path if intent is not None else '',
                'arguments_digest': digest(thaw(prepared.validated_arguments)),
            }
            if intent is None and prepared.host_file_access is HostFileAccess.UNDECLARED:
                payload.update(operation='tool', capability='tool', tool_identity=prepared.tool_identity,
                               tool_origin=prepared.tool_origin)
            elif intent is None:
                command = str(prepared.validated_arguments.get('cmd', ''))
                encoded = command.encode('utf-8')
                preview = command if len(encoded) <= 4096 else encoded[:4093].decode('utf-8', 'ignore') + '…'
                payload.update(capability='shell', command=preview)
            entry = AuthorizedCall(prepared, MappingProxyType(payload), {},
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
                for call in self.operations:
                    if call.status.get('shell_granted') and call.status.get('decision') == 'allowed':
                        self.run_grants.add('shell')
                    if call.status.get('tool_granted') and call.status.get('decision') == 'allowed':
                        self.run_grants.add(call.status['tool_granted'])
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

    def manages(self, index):
        return index in self.guards or index in self.by_call

    @contextmanager
    def execution_context(self, prepared):
        entries = self.by_call.get(prepared.index, [])
        claimed_entries = []
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
            if guard is not None:
                guard.validate()
            with workspace_permission_scope(self.permission), host_access_scope(guard):
                self.execution_states[prepared.index] = True
                yield
            self.execution_states[prepared.index] = True
        except BaseException as error:
            touched = self.execution_states[prepared.index]
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
            for pending_guard in self.guards.values():
                pending_guard.record_changes(prepared.host_files)
            for call in claimed_entries:
                try:
                    result = self._post('/' + call.operation_id + ':complete', {
                        **call.payload, 'status': 'completed',
                    })
                    if result.get('status') != 'completed':
                        raise ToolExecutionError('operation_uncertain')
                except Exception:
                    raise ToolExecutionError('operation_uncertain') from None

    def close(self):
        for guard in self.guards.values():
            guard.close()

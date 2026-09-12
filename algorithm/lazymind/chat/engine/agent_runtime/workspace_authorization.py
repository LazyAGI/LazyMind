"""Request-local Core approval barrier and one-use local execution lifecycle."""
from __future__ import annotations

import copy
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


class WorkspaceAuthorization:
    def __init__(self, config, cancel_check=None):
        config = copy.deepcopy(config)
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
        self.previous = {}
        self.invocation_id = f'{int(time.time() * 1000)}/{uuid.uuid4().hex}'

    def check_cancel(self):
        if self.cancel_check is not None:
            self.cancel_check(None)

    def normalize(self, tools, admissions):
        """Normalize paths before ToolManager prepares its immutable invocations."""
        normalized = copy.deepcopy(tools)
        entries = normalized if isinstance(normalized, list) else [normalized]
        for item in entries:
            if not isinstance(item, dict):
                continue
            function = item.get('function')
            if not isinstance(function, dict):
                continue
            admission = admissions.get(function.get('name'))
            if not admission or not isinstance(admission[0], LocalFileToolkit):
                continue
            arguments = function.get('arguments', {})
            encoded = isinstance(arguments, str)
            if encoded:
                try:
                    arguments = json.loads(arguments)
                except (ValueError, TypeError):
                    continue
            if not isinstance(arguments, dict):
                continue
            for name in ('expected_replacements', 'start_line', 'max_lines', 'max_results', 'max_entries'):
                if name in arguments and (type(arguments[name]) is not int or arguments[name] < 0):
                    raise ToolExecutionError('invalid_selection')
            if 'expected_replacements' in arguments and not 1 <= arguments['expected_replacements'] <= 100:
                raise ToolExecutionError('invalid_selection')
            key = 'path' if admission[1] in {'ls', 'glob', 'grep', 'mkdir', 'info'} else 'filepath'
            value = arguments.get(key)
            if key == 'path' and admission[1] != 'mkdir' and value in (None, ''):
                value = self.root
            if isinstance(value, str) and value:
                arguments[key] = os.path.realpath(value if os.path.isabs(value) else os.path.join(self.root, value))
            function['arguments'] = json.dumps(arguments) if encoded else arguments
        return normalized

    def _post(self, suffix, payload):
        return response_data(post_core_api(self.base + suffix, payload, user_id=self.context['user_id']))

    def prepare(self, prepared, admission, versions):
        self.check_cancel()
        toolkit, method = admission[0], admission[1]
        args = dict(prepared.validated_arguments)
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
            status = self._post(':prepare', payload)
            operation_id = str(status.get('operation_id') or '')
            if len(operation_id) != 64 or any(char not in '0123456789abcdef' for char in operation_id):
                raise ToolExecutionError('workspace authorization unavailable')
            local_path.permission_mode = status.get('permission_mode', 'always_ask')
            call = AuthorizedCall(prepared, toolkit, method, local_path, MappingProxyType(payload), status, operation_id)
            self.calls[prepared.index] = call
            self.previous[local_path.path] = operation_id
        except BaseException:
            local_path.close()
            raise

    def wait(self):
        """No tool executes here; prepare every item before polling any decision."""
        while True:
            self.check_cancel()
            pending = []
            for call in self.calls.values():
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
                return {index for index, call in self.calls.items()
                        if call.status.get('status') == 'allowed' and call.status.get('decision') == 'allowed'}
            time.sleep(0.2)
            for call in pending:
                self.check_cancel()
                params = {key: value for key, value in self.context.items()
                          if key in {'run_id', 'history_id', 'task_id', 'generation', 'attempt_id'}}
                call.status = response_data(get_core_api(
                    self.base + '/' + call.operation_id, params, user_id=self.context['user_id'],
                ))

    def execute(self, index, versions):
        call = self.calls[index]
        payload = dict(call.payload)
        local_path = call.path
        try:
            self.check_cancel()
            claimed = self._post('/' + call.operation_id + ':claim', payload)
            if claimed.get('execute_allowed') is not True or claimed.get('status') != 'executing':
                raise ToolExecutionError('workspace authorization denied')
            local_path.target_identity = str(claimed.get('target_identity') or '')
            local_path.expected_version = str(claimed.get('version') or '')
            # The registered instance remains unchanged. Only this invocation
            # receives the descriptor-backed IO context, with no HTTP in the tool.
            try:
                result = local_path.execute(call.method, call.prepared.validated_arguments)
            except BaseException as error:
                reason = str(error) if isinstance(error, ToolExecutionError) else 'path_invalid'
                if reason not in {'binding_conflict', 'path_invalid', 'unsupported_file', 'execution_inactive'}:
                    reason = 'path_invalid'
                try:
                    self._post('/' + call.operation_id + ':complete', {
                        **payload, 'status': 'uncertain' if local_path.touched else 'failed',
                        'reason': 'operation_uncertain' if local_path.touched else reason,
                    })
                except Exception:
                    raise ToolExecutionError('operation_uncertain') from None
                if isinstance(error, UserCancelledError):
                    raise
                raise ToolExecutionError('operation_uncertain' if local_path.touched else reason) from None
            try:
                completed = self._post('/' + call.operation_id + ':complete', {
                    **payload, 'status': 'completed', 'version': result.get('version', ''),
                    'result_identity': local_path.result_identity(),
                })
                if completed.get('status') != 'completed':
                    raise ToolExecutionError('operation_uncertain')
            except Exception:
                raise ToolExecutionError('operation_uncertain') from None
            if call.method == 'delete':
                versions.pop(local_path.path, None)
            elif result.get('version'):
                versions[local_path.path] = result['version']
            return result
        except UserCancelledError:
            raise
        except Exception as error:
            public = (error if isinstance(error, ToolExecutionError)
                      else ToolExecutionError('workspace authorization unavailable'))
            public.workspace_execution_started = local_path.started
            raise public from None

    def close(self):
        for call in self.calls.values():
            call.path.close()

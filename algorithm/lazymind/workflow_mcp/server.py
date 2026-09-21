"""Dependency-light stdio MCP server backed by the shared Workflow SDK."""
from __future__ import annotations

import json
import os
import sys
import argparse
import copy
from typing import Any, Callable, Dict

from jsonschema import Draft202012Validator

from lazymind.workflow_sdk import AdvanceRequest, StepCommand, WorkflowClient, WorkflowClientError

PROTOCOL_VERSION = '2025-06-18'
CANONICAL_EXTERNAL_AGENT_TYPES = [
    'codex', 'trae-work', 'workbuddy', 'cursor', 'raccoon-work', 'deepseek-harness',
]


def _normalize_external_agent_type(value: str) -> str:
    normalized = str(value or '').strip().lower().replace(' ', '-').replace('_', '-')
    if normalized in ('trae', 'traework'):
        return 'trae-work'
    if normalized in ('deepseek', 'deepseek-harness'):
        return 'deepseek-harness'
    if normalized in ('raccoon', 'raccoon-work', 'xiaohuanxiong', 'xiaohuan-xiong', '小浣熊'):
        return 'raccoon-work'
    return normalized


def _object(properties: Dict[str, Any], required: list[str] | None = None) -> Dict[str, Any]:
    return {'type': 'object', 'properties': properties, 'required': required or [],
            'additionalProperties': False}


TOOL_SCHEMAS = {
    'workflow_connection_status': _object({}),
    'list_workflows': _object({}),
    'get_workflow': _object({
        'workflow_id': {'type': 'string'},
    }, ['workflow_id']),
    'list_workflow_inputs': _object({}),
    'get_workflow_state': _object({}),
    'get_ready_steps': _object({}),
    'list_artifacts': _object({}),
    'read_artifact': _object({'artifact_ref': {'type': 'string'}}, ['artifact_ref']),
    'patch_artifact': _object({
        'artifact_ref': {'type': 'string'}, 'value': {}, 'caption': {'type': 'string'},
    }, ['artifact_ref', 'value']),
    'advance_step': _object({
        'steps': {'type': 'array', 'minItems': 1, 'items': _object({
            'step_id': {'type': 'string'},
        }, ['step_id'])},
    }, ['steps']),
    'get_skill_conversion_context': _object({
        'skill_id': {'type': 'string'},
    }, ['skill_id']),
    'preflight_skill_workflow_conversion': _object({
        'skill_id': {'type': 'string'},
    }, ['skill_id']),
    'list_skills': _object({}),
    'create_workflow_draft': _object({
        'name': {'type': 'string'},
        'skill_id': {'type': 'string'},
        'files': {'type': 'object', 'additionalProperties': {'type': 'string'}},
    }, ['name', 'files']),
    'list_workflow_drafts': _object({}),
    'select_workflow_draft': _object({'draft_id': {'type': 'string'}}, ['draft_id']),
    'get_workflow_draft': _object({}),
    'list_workflow_versions': _object({'workflow_ref': {'type': 'string'}}, ['workflow_ref']),
    'update_workflow_draft_file': _object({
        'path': {'type': 'string'}, 'content': {'type': 'string'},
    }, ['path', 'content']),
    'validate_workflow_draft': _object({}),
    'get_workflow_diagnostics': _object({}),
    'publish_workflow': _object({}),
    'start_skill_workflow_task': _object({
        'agent_type': {'type': 'string', 'enum': CANONICAL_EXTERNAL_AGENT_TYPES},
        'skill': _object({
            'name': {'type': 'string', 'minLength': 1},
            'url': {'type': 'string', 'minLength': 1},
            'zip_path': {
                'type': 'string', 'minLength': 1,
                'description': (
                    'Path to a ZIP readable by this MCP process. '
                    'The adapter uploads its bytes without placing base64 in the conversation.'
                ),
            },
            'zip_base64': {'type': 'string', 'minLength': 1},
            'zip_sha256': {'type': 'string'},
        }, ['name']),
        'task_description': {'type': 'string', 'minLength': 1},
        'external_conversation_id': {'type': 'string'},
        'external_thread_id': {'type': 'string'},
        'input_bindings': {'type': 'object', 'additionalProperties': {}},
        'input_files': {'type': 'array', 'items': _object({
            'material_id': {'type': 'string'},
            'name': {'type': 'string'},
            'mime_type': {'type': 'string'},
            'content_base64': {'type': 'string'},
            'content_hash': {'type': 'string'},
        }, ['material_id', 'name', 'mime_type', 'content_base64'])},
        'config': _object({'reuse_workflow': {'type': 'boolean', 'default': True}}),
        'idempotency_key': {'type': 'string'},
    }, ['agent_type', 'skill', 'task_description']),
    'get_skill_workflow_task': _object({
        'task_id': {'type': 'string'},
    }, ['task_id']),
    'get_skill_workflow_result': _object({
        'task_id': {'type': 'string'},
    }, ['task_id']),
}

TOOL_SCHEMAS['start_skill_workflow_task']['properties']['skill']['oneOf'] = [
    {'required': [field]} for field in ('url', 'zip_path', 'zip_base64')
]

TOOL_DESCRIPTIONS = {
    'workflow_connection_status': 'Discover LazyMind Core and verify Workflow API connectivity.',
    'list_workflows': 'List enabled Workflows visible to the current LazyMind user.',
    'get_workflow': 'Read one Workflow definition and pinned revision metadata.',
    'list_workflow_inputs': 'List the immutable input bindings for a Workflow Session.',
    'get_workflow_state': 'Read the authoritative Workflow projection and state_version.',
    'get_ready_steps': 'Read only the current Ready frontier from the authoritative projection.',
    'list_artifacts': 'List selected immutable Artifact revisions for a Workflow Session.',
    'read_artifact': 'Read one authorized Artifact revision and its lineage metadata.',
    'patch_artifact': 'Create an Agent-authored immutable revision from a selected Artifact.',
    'advance_step': 'Synchronously request one or more Ready targets; Runtime resolves execute/retry/rewind.',
    'get_skill_conversion_context': 'Read a complete, immutable Skill revision snapshot; never invokes a model.',
    'preflight_skill_workflow_conversion': 'Check whether a Skill snapshot is ready for deterministic conversion.',
    'list_skills': 'List Skills visible to the current user for deterministic Workflow conversion.',
    'create_workflow_draft': 'Store Agent-authored Workflow package files against a pinned Skill snapshot.',
    'list_workflow_drafts': 'List Workflow drafts owned by the current user.',
    'select_workflow_draft': 'Select one exact draft for context-bound authoring operations.',
    'get_workflow_draft': 'Read one owned Workflow draft and its current package content.',
    'list_workflow_versions': 'List immutable published revisions for one Workflow.',
    'update_workflow_draft_file': 'Deterministically update one draft file with optimistic version checking.',
    'validate_workflow_draft': 'Compile the draft with the deterministic Workflow graph validator.',
    'get_workflow_diagnostics': 'Read deterministic package, graph, tool, and script diagnostics.',
    'publish_workflow': 'Publish only a draft that passes deterministic publish diagnostics.',
    'start_skill_workflow_task': (
        'Submit skill.name and exactly one source: URL, local zip_path, or zip_base64. '
        'LazyMind installs, converts and executes in the background. '
        'Reuse the same idempotency_key and arguments when retrying a submission.'
    ),
    'get_skill_workflow_task': (
        'Read task stage and next_action. Follow poll_after_seconds while active; '
        'on open_lazymind show the link and stop polling. Polling does not drive execution.'
    ),
    'get_skill_workflow_result': (
        'Read task outcome and, when result_ready, summary and output artifacts. '
        'A running or blocked task returns its status and next_action without raising an error.'
    ),
}


class WorkflowMCPServer:
    _EXTERNAL_TOOLS = {'workflow_connection_status', 'start_skill_workflow_task',
                       'get_skill_workflow_task', 'get_skill_workflow_result'}
    _SESSION_TOOLS = {
        'list_workflow_inputs', 'get_workflow_state', 'get_ready_steps',
        'list_artifacts', 'read_artifact', 'patch_artifact', 'advance_step',
    }

    def __init__(self, client_factory: Callable[[], WorkflowClient] = WorkflowClient,
                 session_id: str = '', draft_id: str = '', *, mode: str = 'full', agent_type: str = ''):
        self.client_factory = client_factory
        self._client: WorkflowClient | None = None
        self.session_id = session_id or os.getenv('LAZYMIND_WORKFLOW_SESSION_ID', '').strip()
        self.draft_id = draft_id or os.getenv('LAZYMIND_WORKFLOW_DRAFT_ID', '').strip()
        if mode not in ('full', 'external'):
            raise ValueError('mode must be full or external')
        self.mode = mode
        self.agent_type = _normalize_external_agent_type(
            agent_type or os.getenv('LAZYMIND_EXTERNAL_AGENT_TYPE', '').strip())
        if self.agent_type and self.agent_type not in CANONICAL_EXTERNAL_AGENT_TYPES:
            raise ValueError('Unsupported configured external agent_type')

    def _schema(self, name: str) -> Dict[str, Any]:
        schema = copy.deepcopy(TOOL_SCHEMAS[name])
        if name == 'start_skill_workflow_task' and self.agent_type:
            schema['required'].remove('agent_type')
            schema['properties']['agent_type'] = {'type': 'string', 'const': self.agent_type}
        return schema

    def list_tools(self) -> list[Dict[str, Any]]:
        return [
            {'name': name, 'description': TOOL_DESCRIPTIONS[name], 'inputSchema': self._schema(name)}
            for name in TOOL_SCHEMAS
            if (self.mode != 'external' or name in self._EXTERNAL_TOOLS)
            and (self.session_id or name not in self._SESSION_TOOLS)
        ]

    def call_tool(self, name: str, arguments: Dict[str, Any]) -> Dict[str, Any]:
        if name not in TOOL_SCHEMAS:
            raise WorkflowClientError('UNKNOWN_TOOL', f'Unknown Workflow tool: {name}')
        if self.mode == 'external' and name not in self._EXTERNAL_TOOLS:
            raise WorkflowClientError('TOOL_NOT_AVAILABLE', 'This tool is not available in external mode.')
        if (name == 'start_skill_workflow_task' and isinstance(arguments, dict)
                and isinstance(arguments.get('agent_type'), str)):
            arguments = {**arguments, 'agent_type': _normalize_external_agent_type(arguments['agent_type'])}
        error = next(Draft202012Validator(self._schema(name)).iter_errors(arguments), None)
        if error is not None:
            path = '.'.join(str(part) for part in error.absolute_path) or 'arguments'
            message = f'{path}: {error.message}'
            if error.validator == 'oneOf':
                message = f'{path}: provide exactly one of url, zip_path or zip_base64'
            raise WorkflowClientError('INVALID_REQUEST', message)
        if name in self._SESSION_TOOLS and not self.session_id:
            raise WorkflowClientError(
                'WORKFLOW_SESSION_CONTEXT_REQUIRED',
                'The deterministic MCP Host must bind a Workflow Session before exposing this tool.',
            )
        # Bind endpoint and identity once so status, submission and polling use
        # the same instance even if environment or selected runtime data changes.
        if self._client is None:
            self._client = self.client_factory()
        client = self._client
        if name == 'workflow_connection_status':
            result = client.external_connection_status() if self.mode == 'external' else client.connection_status()
        elif name == 'list_workflows':
            result = client.list_workflows().result
        elif name == 'get_workflow':
            result = client.get_workflow(
                arguments['workflow_id'], arguments.get('revision_id', '')).result
        elif name == 'list_workflow_inputs':
            result = client.list_workflow_inputs(self.session_id).result
        elif name == 'get_workflow_state':
            result = client.get_state(self.session_id)
        elif name == 'get_ready_steps':
            result = client.get_ready_steps(self.session_id)
        elif name == 'list_artifacts':
            result = client.list_artifacts(self.session_id).result
        elif name == 'read_artifact':
            artifact = self._artifact(client, arguments['artifact_ref'])
            result = client.read_artifact(str(artifact.get('artifact_id') or artifact['id'])).result
        elif name == 'patch_artifact':
            artifact = self._artifact(client, arguments['artifact_ref'])
            artifact_id = str(artifact.get('artifact_id') or artifact['id'])
            current = client.read_artifact(artifact_id).result
            result = client.patch_artifact(
                artifact_id, int(current['revision']), arguments['value'],
                str(current.get('content_type') or 'json'), arguments.get('caption', '')).result
        elif name == 'advance_step':
            frontier = client.get_ready_steps(self.session_id)
            allowed = set(frontier.get('ready_steps') or [])
            allowed.update(frontier.get('retryable_steps') or [])
            allowed.update(frontier.get('rewindable_steps') or [])
            steps = [StepCommand(**step) for step in arguments['steps']]
            if any(step.step_id not in allowed for step in steps):
                raise WorkflowClientError(
                    'WORKFLOW_TARGET_NOT_PROJECTED',
                    'Every step must be returned by the latest Runtime projection.',
                )
            result = client.advance(AdvanceRequest(
                session_id=self.session_id,
                expected_state_version=int(frontier.get('state_version') or 0), steps=steps,
            )).result
        elif name == 'get_skill_conversion_context':
            result = client.get_skill_conversion_context(
                arguments['skill_id']).result
        elif name == 'preflight_skill_workflow_conversion':
            result = client.preflight_skill_workflow_conversion(
                arguments['skill_id']).result
        elif name == 'list_skills':
            result = client.list_skills().result
        elif name == 'create_workflow_draft':
            skill_id = arguments.get('skill_id', '')
            context = client.get_skill_conversion_context(skill_id).result if skill_id else {}
            snapshot = context.get('snapshot') or {}
            draft_args = [arguments['name'], skill_id,
                          snapshot.get('revision_id', ''), snapshot.get('tree_hash', ''),
                          arguments['files']]
            draft_args.append('skill' if skill_id else 'blank')
            result = client.create_workflow_draft(*draft_args).result
            draft = result.get('draft') if isinstance(result.get('draft'), dict) else result
            self.draft_id = str(draft.get('draft_id') or draft.get('id') or '')
        elif name == 'list_workflow_drafts':
            result = client.list_workflow_drafts().result
        elif name == 'select_workflow_draft':
            result = client.get_workflow_draft(arguments['draft_id']).result
            self.draft_id = arguments['draft_id']
        elif name == 'get_workflow_draft':
            result = client.get_workflow_draft(self._require_draft()).result
        elif name == 'list_workflow_versions':
            result = client.list_workflow_versions(arguments['workflow_ref']).result
        elif name == 'update_workflow_draft_file':
            draft_id = self._require_draft()
            current = client.get_workflow_draft(draft_id).result
            result = client.update_workflow_draft_file(
                draft_id, arguments['path'], arguments['content'],
                int(current['version'])).result
        elif name == 'validate_workflow_draft':
            result = client.validate_workflow_draft(self._require_draft()).result
        elif name == 'get_workflow_diagnostics':
            result = client.get_workflow_diagnostics(self._require_draft()).result
        elif name == 'publish_workflow':
            result = client.publish_workflow(self._require_draft()).result
        elif name == 'start_skill_workflow_task':
            result = client.start_skill_workflow_task(
                arguments.get('agent_type') or self.agent_type,
                arguments['skill'], arguments['task_description'],
                external_conversation_id=arguments.get('external_conversation_id', ''),
                external_thread_id=arguments.get('external_thread_id', ''),
                input_bindings=arguments.get('input_bindings') or {},
                input_files=arguments.get('input_files') or [],
                config=arguments.get('config') or {},
                idempotency_key=arguments.get('idempotency_key', ''),
            ).result
        elif name == 'get_skill_workflow_task':
            result = client.get_skill_workflow_task(arguments['task_id']).result
        else:
            result = client.get_skill_workflow_result(arguments['task_id']).result
        return {'content': [{'type': 'text', 'text': json.dumps(result, ensure_ascii=False)}],
                'structuredContent': result, 'isError': False}

    def _artifact(self, client: WorkflowClient, ref: str) -> Dict[str, Any]:
        values = client.list_artifacts(self.session_id).result.get('artifacts') or []
        matches = []
        for item in values:
            handles = {str(item.get('artifact_id') or item.get('id') or ''),
                       str(item.get('slot') or '')}
            if item.get('list_index') is not None:
                handles.add(f'{item.get("slot")}[{item.get("list_index")}]')
            if ref in handles:
                matches.append(item)
        if len(matches) != 1:
            raise WorkflowClientError(
                'ARTIFACT_NOT_SELECTED',
                'artifact_ref must uniquely identify a selected Session artifact.',
            )
        return matches[0]

    def _require_draft(self) -> str:
        if not self.draft_id:
            raise WorkflowClientError(
                'WORKFLOW_DRAFT_CONTEXT_REQUIRED',
                'Create or select one draft before using this authoring tool.',
            )
        return self.draft_id

    def handle(self, request: Dict[str, Any]) -> Dict[str, Any] | None:
        method = request.get('method')
        request_id = request.get('id')
        if request_id is None:
            return None
        try:
            if method == 'initialize':
                result = {'protocolVersion': PROTOCOL_VERSION,
                          'capabilities': {'tools': {'listChanged': False}},
                          'serverInfo': {'name': 'lazymind-workflow', 'version': 'workflow.v1'}}
            elif method == 'ping':
                result = {}
            elif method == 'tools/list':
                result = {'tools': self.list_tools()}
            elif method == 'tools/call':
                params = request.get('params') or {}
                result = self.call_tool(str(params.get('name') or ''), params.get('arguments') or {})
            else:
                return {'jsonrpc': '2.0', 'id': request_id,
                        'error': {'code': -32601, 'message': f'Method not found: {method}'}}
            return {'jsonrpc': '2.0', 'id': request_id, 'result': result}
        except WorkflowClientError as exc:
            result = {'code': exc.code, 'message': exc.message, 'retryable': exc.retryable,
                      'status_code': exc.status_code, 'details': exc.details}
            return {'jsonrpc': '2.0', 'id': request_id, 'result': {
                'content': [{'type': 'text', 'text': json.dumps(result, ensure_ascii=False)}],
                'structuredContent': {'error': result}, 'isError': True,
            }}
        except (KeyError, TypeError, ValueError) as exc:
            return {'jsonrpc': '2.0', 'id': request_id,
                    'error': {'code': -32602, 'message': f'Invalid tool arguments: {exc}'}}


def main() -> None:
    parser = argparse.ArgumentParser(description='LazyMind Workflow MCP adapter')
    parser.add_argument('--mode', choices=['external', 'full'], default='external')
    parser.add_argument('--agent-type', default='')
    args = parser.parse_args()
    server = WorkflowMCPServer(mode=args.mode, agent_type=args.agent_type)
    for line in sys.stdin:
        try:
            request = json.loads(line)
            response = server.handle(request)
            if response is not None:
                sys.stdout.write(json.dumps(response, ensure_ascii=False) + '\n')
                sys.stdout.flush()
        except (ValueError, TypeError) as exc:
            sys.stdout.write(json.dumps({
                'jsonrpc': '2.0', 'id': None,
                'error': {'code': -32700, 'message': f'Parse error: {exc}'},
            }) + '\n')
            sys.stdout.flush()


if __name__ == '__main__':
    main()

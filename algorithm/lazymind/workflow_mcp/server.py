"""Dependency-light stdio MCP server backed by the shared Workflow SDK."""
from __future__ import annotations

import json
import sys
import uuid
from typing import Any, Callable, Dict

from lazymind.workflow_sdk import AdvanceRequest, StepCommand, WorkflowClient, WorkflowClientError

PROTOCOL_VERSION = '2025-06-18'


def _object(properties: Dict[str, Any], required: list[str] | None = None) -> Dict[str, Any]:
    return {'type': 'object', 'properties': properties, 'required': required or [],
            'additionalProperties': False}


TOOL_SCHEMAS = {
    'workflow_connection_status': _object({}),
    'list_workflows': _object({}),
    'get_workflow': _object({'workflow_id': {'type': 'string'}}, ['workflow_id']),
    'prepare_workflow': _object({
        'workflow_id': {'type': 'string'},
        'input_bindings': {'type': 'object'},
        'command_id': {'type': 'string'},
    }, ['workflow_id']),
    'start_workflow': _object({
        'preparation_id': {'type': 'string'}, 'session_id': {'type': 'string'},
        'command_id': {'type': 'string'},
    }, ['preparation_id', 'session_id']),
    'get_workflow_state': _object({'session_id': {'type': 'string'}}, ['session_id']),
    'get_ready_steps': _object({'session_id': {'type': 'string'}}, ['session_id']),
    'advance_step': _object({
        'session_id': {'type': 'string'},
        'expected_state_version': {'type': 'integer', 'minimum': 0},
        'steps': {'type': 'array', 'minItems': 1, 'items': _object({
            'step_id': {'type': 'string'}, 'task_id': {'type': 'string'},
            'objective': {'type': 'string'}, 'user_input': {'type': 'string'},
            'runtime_instruction': {'type': 'string'}, 'partial_indices': {'type': 'object'},
        }, ['step_id'])},
        'command_id': {'type': 'string'},
    }, ['session_id', 'expected_state_version', 'steps']),
    'get_skill_conversion_context': _object({
        'skill_id': {'type': 'string'}, 'revision_id': {'type': 'string'},
    }, ['skill_id']),
    'create_workflow_draft': _object({
        'name': {'type': 'string'}, 'skill_id': {'type': 'string'},
        'revision_id': {'type': 'string'}, 'tree_hash': {'type': 'string'},
        'files': {'type': 'object', 'additionalProperties': {'type': 'string'}},
    }, ['name', 'skill_id', 'revision_id', 'tree_hash', 'files']),
    'update_workflow_draft_file': _object({
        'draft_id': {'type': 'string'}, 'path': {'type': 'string'},
        'content': {'type': 'string'}, 'expected_version': {'type': 'integer', 'minimum': 1},
    }, ['draft_id', 'path', 'content', 'expected_version']),
    'validate_workflow_draft': _object({'draft_id': {'type': 'string'}}, ['draft_id']),
    'get_workflow_diagnostics': _object({'draft_id': {'type': 'string'}}, ['draft_id']),
    'publish_workflow': _object({'draft_id': {'type': 'string'}}, ['draft_id']),
}

TOOL_DESCRIPTIONS = {
    'workflow_connection_status': 'Discover LazyMind Core and verify Workflow API connectivity.',
    'list_workflows': 'List enabled Workflows visible to the current LazyMind user.',
    'get_workflow': 'Read one Workflow definition and pinned revision metadata.',
    'prepare_workflow': 'Validate a Workflow and inputs without creating a Session.',
    'start_workflow': 'Consume a ready preparation and create a Workflow Session.',
    'get_workflow_state': 'Read the authoritative Workflow projection and state_version.',
    'get_ready_steps': 'Read only the current Ready frontier from the authoritative projection.',
    'advance_step': 'Synchronously request one or more Ready targets; Runtime resolves execute/retry/rewind.',
    'get_skill_conversion_context': 'Read a complete, immutable Skill revision snapshot; never invokes a model.',
    'create_workflow_draft': 'Store Agent-authored Workflow package files against a pinned Skill snapshot.',
    'update_workflow_draft_file': 'Deterministically update one draft file with optimistic version checking.',
    'validate_workflow_draft': 'Compile the draft with the deterministic Workflow graph validator.',
    'get_workflow_diagnostics': 'Read deterministic package, graph, tool, and script diagnostics.',
    'publish_workflow': 'Publish only a draft that passes deterministic publish diagnostics.',
}


class WorkflowMCPServer:
    def __init__(self, client_factory: Callable[[], WorkflowClient] = WorkflowClient):
        self.client_factory = client_factory

    def list_tools(self) -> list[Dict[str, Any]]:
        return [{'name': name, 'description': TOOL_DESCRIPTIONS[name], 'inputSchema': schema}
                for name, schema in TOOL_SCHEMAS.items()]

    def call_tool(self, name: str, arguments: Dict[str, Any]) -> Dict[str, Any]:
        if name not in TOOL_SCHEMAS:
            raise WorkflowClientError('UNKNOWN_TOOL', f'Unknown Workflow tool: {name}')
        client = self.client_factory()
        if name == 'workflow_connection_status':
            result = client.connection_status()
        elif name == 'list_workflows':
            result = client.list_workflows().result
        elif name == 'get_workflow':
            result = client.get_workflow(arguments['workflow_id']).result
        elif name == 'prepare_workflow':
            result = client.prepare_workflow(
                arguments['workflow_id'], input_bindings=arguments.get('input_bindings'),
                command_id=arguments.get('command_id', ''),
            ).result
        elif name == 'start_workflow':
            result = client.start_workflow(
                arguments['preparation_id'], arguments['session_id'],
                command_id=arguments.get('command_id', ''),
            ).result
        elif name == 'get_workflow_state':
            result = client.get_state(arguments['session_id'])
        elif name == 'get_ready_steps':
            result = client.get_ready_steps(arguments['session_id'])
        elif name == 'advance_step':
            steps = [StepCommand(**step) for step in arguments['steps']]
            result = client.advance(AdvanceRequest(
                session_id=arguments['session_id'],
                expected_state_version=arguments['expected_state_version'], steps=steps,
                command_id=arguments.get('command_id') or str(uuid.uuid4()),
            )).result
        elif name == 'get_skill_conversion_context':
            result = client.get_skill_conversion_context(
                arguments['skill_id'], arguments.get('revision_id', '')).result
        elif name == 'create_workflow_draft':
            result = client.create_workflow_draft(
                arguments['name'], arguments['skill_id'], arguments['revision_id'],
                arguments['tree_hash'], arguments['files']).result
        elif name == 'update_workflow_draft_file':
            result = client.update_workflow_draft_file(
                arguments['draft_id'], arguments['path'], arguments['content'],
                arguments['expected_version']).result
        elif name == 'validate_workflow_draft':
            result = client.validate_workflow_draft(arguments['draft_id']).result
        elif name == 'get_workflow_diagnostics':
            result = client.get_workflow_diagnostics(arguments['draft_id']).result
        else:
            result = client.publish_workflow(arguments['draft_id']).result
        return {'content': [{'type': 'text', 'text': json.dumps(result, ensure_ascii=False)}],
                'structuredContent': result, 'isError': False}

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
    server = WorkflowMCPServer()
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

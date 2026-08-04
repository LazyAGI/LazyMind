"""Dependency-light stdio MCP server backed by the shared Workflow SDK."""
from __future__ import annotations

import base64
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
    'get_workflow': _object({
        'workflow_id': {'type': 'string'}, 'revision_id': {'type': 'string'},
    }, ['workflow_id']),
    'prepare_workflow': _object({
        'workflow_id': {'type': 'string'},
        'input_bindings': {'type': 'object'},
        'command_id': {'type': 'string'},
    }, ['workflow_id']),
    'import_input_resource': _object({
        'name': {'type': 'string'}, 'mime_type': {'type': 'string'},
        'content_base64': {'type': 'string'},
    }, ['name', 'mime_type', 'content_base64']),
    'read_input_resource': _object({'resource_id': {'type': 'string'}}, ['resource_id']),
    'list_workflow_inputs': _object({'session_id': {'type': 'string'}}, ['session_id']),
    'bind_workflow_input': _object({
        'session_id': {'type': 'string'}, 'material_id': {'type': 'string'},
        'resource': {'type': 'object'}, 'command_id': {'type': 'string'},
    }, ['session_id', 'material_id', 'resource']),
    'start_workflow': _object({
        'preparation_id': {'type': 'string'}, 'session_id': {'type': 'string'},
        'command_id': {'type': 'string'},
    }, ['preparation_id', 'session_id']),
    'get_workflow_state': _object({'session_id': {'type': 'string'}}, ['session_id']),
    'get_ready_steps': _object({'session_id': {'type': 'string'}}, ['session_id']),
    'stop_workflow': _object({
        'session_id': {'type': 'string'}, 'command_id': {'type': 'string'},
    }, ['session_id']),
    'resume_workflow': _object({
        'session_id': {'type': 'string'}, 'command_id': {'type': 'string'},
    }, ['session_id']),
    'get_workflow_command': _object({'command_id': {'type': 'string'}}, ['command_id']),
    'list_artifacts': _object({'session_id': {'type': 'string'}}, ['session_id']),
    'read_artifact': _object({'artifact_id': {'type': 'string'}}, ['artifact_id']),
    'patch_artifact': _object({
        'artifact_id': {'type': 'string'}, 'base_revision': {'type': 'integer', 'minimum': 1},
        'value': {}, 'content_type': {'type': 'string'}, 'caption': {'type': 'string'},
        'command_id': {'type': 'string'},
    }, ['artifact_id', 'base_revision', 'value']),
    'delete_artifact': _object({
        'artifact_id': {'type': 'string'}, 'base_revision': {'type': 'integer', 'minimum': 1},
        'command_id': {'type': 'string'},
    }, ['artifact_id', 'base_revision']),
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
    'list_skills': _object({}),
    'create_workflow_draft': _object({
        'name': {'type': 'string'}, 'source_type': {'type': 'string', 'enum': ['blank', 'skill', 'import']},
        'skill_id': {'type': 'string'},
        'revision_id': {'type': 'string'}, 'tree_hash': {'type': 'string'},
        'files': {'type': 'object', 'additionalProperties': {'type': 'string'}},
    }, ['name', 'files']),
    'list_workflow_drafts': _object({}),
    'get_workflow_draft': _object({'draft_id': {'type': 'string'}}, ['draft_id']),
    'delete_workflow_draft': _object({'draft_id': {'type': 'string'}}, ['draft_id']),
    'list_workflow_versions': _object({'workflow_ref': {'type': 'string'}}, ['workflow_ref']),
    'archive_workflow': _object({'workflow_ref': {'type': 'string'}}, ['workflow_ref']),
    'restore_workflow': _object({'workflow_ref': {'type': 'string'}}, ['workflow_ref']),
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
    'import_input_resource': 'Import immutable Host content into the shared Workflow resource store.',
    'read_input_resource': 'Read an authorized immutable Workflow input resource.',
    'list_workflow_inputs': 'List the immutable input bindings for a Workflow Session.',
    'bind_workflow_input': 'Bind an imported resource revision to a Session material.',
    'start_workflow': 'Consume a ready preparation and create a Workflow Session.',
    'get_workflow_state': 'Read the authoritative Workflow projection and state_version.',
    'get_ready_steps': 'Read only the current Ready frontier from the authoritative projection.',
    'stop_workflow': 'Stop a Workflow and interrupt active Attempts while preserving outputs.',
    'resume_workflow': 'Resume a stopped Workflow from persisted Runtime state.',
    'get_workflow_command': 'Reconcile the result of an idempotent Workflow command.',
    'list_artifacts': 'List selected immutable Artifact revisions for a Workflow Session.',
    'read_artifact': 'Read one authorized Artifact revision and its lineage metadata.',
    'patch_artifact': 'Create an Agent-authored immutable revision from a selected Artifact.',
    'delete_artifact': 'Create an immutable deletion tombstone revision without erasing history.',
    'advance_step': 'Synchronously request one or more Ready targets; Runtime resolves execute/retry/rewind.',
    'get_skill_conversion_context': 'Read a complete, immutable Skill revision snapshot; never invokes a model.',
    'list_skills': 'List Skills visible to the current user for deterministic Workflow conversion.',
    'create_workflow_draft': 'Store Agent-authored Workflow package files against a pinned Skill snapshot.',
    'list_workflow_drafts': 'List Workflow drafts owned by the current user.',
    'get_workflow_draft': 'Read one owned Workflow draft and its current package content.',
    'delete_workflow_draft': 'Delete one unpublished Workflow draft.',
    'list_workflow_versions': 'List immutable published revisions for one Workflow.',
    'archive_workflow': 'Archive a published Workflow while retaining immutable history.',
    'restore_workflow': 'Restore an archived Workflow without changing its immutable revisions.',
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
            result = client.get_workflow(
                arguments['workflow_id'], arguments.get('revision_id', '')).result
        elif name == 'prepare_workflow':
            result = client.prepare_workflow(
                arguments['workflow_id'], input_bindings=arguments.get('input_bindings'),
                command_id=arguments.get('command_id', ''),
            ).result
        elif name == 'import_input_resource':
            result = client.import_input_resource(
                arguments['name'], arguments['mime_type'],
                base64.b64decode(arguments['content_base64'])).result
        elif name == 'read_input_resource':
            value = client.read_input_resource(arguments['resource_id'])
            content = value.pop('content', b'')
            value['content_base64'] = base64.b64encode(content).decode('ascii')
            result = value
        elif name == 'list_workflow_inputs':
            result = client.list_workflow_inputs(arguments['session_id']).result
        elif name == 'bind_workflow_input':
            result = client.bind_workflow_input(
                arguments['session_id'], arguments['material_id'], arguments['resource'],
                arguments.get('command_id', '')).result
        elif name == 'start_workflow':
            result = client.start_workflow(
                arguments['preparation_id'], arguments['session_id'],
                command_id=arguments.get('command_id', ''),
            ).result
        elif name == 'get_workflow_state':
            result = client.get_state(arguments['session_id'])
        elif name == 'get_ready_steps':
            result = client.get_ready_steps(arguments['session_id'])
        elif name == 'stop_workflow':
            result = client.stop_workflow(
                arguments['session_id'], arguments.get('command_id', '')).result
        elif name == 'resume_workflow':
            result = client.resume_workflow(
                arguments['session_id'], arguments.get('command_id', '')).result
        elif name == 'get_workflow_command':
            result = client.get_command(arguments['command_id']).result
        elif name == 'list_artifacts':
            result = client.list_artifacts(arguments['session_id']).result
        elif name == 'read_artifact':
            result = client.read_artifact(arguments['artifact_id']).result
        elif name == 'patch_artifact':
            result = client.patch_artifact(
                arguments['artifact_id'], arguments['base_revision'], arguments['value'],
                arguments.get('content_type', 'json'), arguments.get('caption', ''),
                arguments.get('command_id', '')).result
        elif name == 'delete_artifact':
            result = client.delete_artifact(
                arguments['artifact_id'], arguments['base_revision'],
                arguments.get('command_id', '')).result
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
        elif name == 'list_skills':
            result = client.list_skills().result
        elif name == 'create_workflow_draft':
            draft_args = [arguments['name'], arguments.get('skill_id', ''),
                          arguments.get('revision_id', ''), arguments.get('tree_hash', ''),
                          arguments['files']]
            if arguments.get('source_type'):
                draft_args.append(arguments['source_type'])
            result = client.create_workflow_draft(*draft_args).result
        elif name == 'list_workflow_drafts':
            result = client.list_workflow_drafts().result
        elif name == 'get_workflow_draft':
            result = client.get_workflow_draft(arguments['draft_id']).result
        elif name == 'delete_workflow_draft':
            result = client.delete_workflow_draft(arguments['draft_id']).result
        elif name == 'list_workflow_versions':
            result = client.list_workflow_versions(arguments['workflow_ref']).result
        elif name == 'archive_workflow':
            result = client.archive_workflow(arguments['workflow_ref']).result
        elif name == 'restore_workflow':
            result = client.restore_workflow(arguments['workflow_ref']).result
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

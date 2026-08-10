"""Dependency-light stdio MCP server backed by the shared Workflow SDK."""
from __future__ import annotations

import json
import os
import sys
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
}

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
}


class WorkflowMCPServer:
    _SESSION_TOOLS = {
        'list_workflow_inputs', 'get_workflow_state', 'get_ready_steps',
        'list_artifacts', 'read_artifact', 'patch_artifact', 'advance_step',
    }

    def __init__(self, client_factory: Callable[[], WorkflowClient] = WorkflowClient,
                 session_id: str = '', draft_id: str = ''):
        self.client_factory = client_factory
        self.session_id = session_id or os.getenv('LAZYMIND_WORKFLOW_SESSION_ID', '').strip()
        self.draft_id = draft_id or os.getenv('LAZYMIND_WORKFLOW_DRAFT_ID', '').strip()

    def list_tools(self) -> list[Dict[str, Any]]:
        return [
            {'name': name, 'description': TOOL_DESCRIPTIONS[name], 'inputSchema': schema}
            for name, schema in TOOL_SCHEMAS.items()
            if self.session_id or name not in self._SESSION_TOOLS
        ]

    def call_tool(self, name: str, arguments: Dict[str, Any]) -> Dict[str, Any]:
        if name not in TOOL_SCHEMAS:
            raise WorkflowClientError('UNKNOWN_TOOL', f'Unknown Workflow tool: {name}')
        if name in self._SESSION_TOOLS and not self.session_id:
            raise WorkflowClientError(
                'WORKFLOW_SESSION_CONTEXT_REQUIRED',
                'The deterministic MCP Host must bind a Workflow Session before exposing this tool.',
            )
        client = self.client_factory()
        if name == 'workflow_connection_status':
            result = client.connection_status()
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
        elif name == 'list_skills':
            result = client.list_skills().result
        elif name == 'create_workflow_draft':
            skill_id = arguments.get('skill_id', '')
            context = client.get_skill_conversion_context(skill_id).result if skill_id else {}
            draft_args = [arguments['name'], skill_id,
                          context.get('revision_id', ''), context.get('tree_hash', ''),
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
        else:
            result = client.publish_workflow(self._require_draft()).result
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

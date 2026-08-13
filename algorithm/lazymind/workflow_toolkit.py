"""Host-neutral callable tools for the public Workflow runtime.

Every function is deterministic infrastructure. Only ``advance_step`` may cause
the runtime Supervisor to launch an explicit Workflow SubAgent after acceptance.
"""
from __future__ import annotations

import base64
import logging
import os
import types
import uuid
from dataclasses import dataclass
from pathlib import Path
from typing import Any, Callable, Dict, List, Optional

from pydantic import BaseModel, ConfigDict, Field

from lazymind.workflow_sdk import (
    AdvanceRequest, StepCommand, WorkflowClient, WorkflowClientError,
)

LOG = logging.getLogger(__name__)


def load_workflow_package_tools(
    package: Dict[str, Any], names: List[str], workflow_id: str, revision_id: str,
) -> Dict[str, Any]:
    """Load named callables from an already validated Workflow package."""
    files = package.get('files') if isinstance(package.get('files'), dict) else {}
    remaining = set(names)
    resolved: Dict[str, Any] = {}
    for path in sorted(files):
        if not remaining:
            break
        if not path.startswith('scripts/') or not path.endswith('.py'):
            continue
        encoded = files[path]
        source = base64.b64decode(encoded) if isinstance(encoded, str) else bytes(encoded)
        module = types.ModuleType(
            f'_lazymind_workflow_{revision_id.replace("-", "_")}_{len(resolved)}'
        )
        module.__file__ = f'{workflow_id}@{revision_id}/{path}'
        exec(compile(source.decode('utf-8'), module.__file__, 'exec'), module.__dict__)
        for name in tuple(remaining):
            candidate = module.__dict__.get(name)
            if callable(candidate):
                if not str(getattr(candidate, '__doc__', '') or '').strip():
                    candidate.__doc__ = f'Execute the published Workflow tool {name}.'
                resolved[name] = candidate
                remaining.remove(name)
    if remaining:
        LOG.warning('Workflow revision %s does not provide tools %s', revision_id, sorted(remaining))
    return resolved


class StepCommandInput(BaseModel):
    """One Ready Workflow target; command metadata belongs to advance_step."""

    model_config = ConfigDict(extra='forbid')

    step_id: str = Field(description='Exact Ready step_id returned by get_ready_steps.')
    task_id: str = Field(default='', description='Optional caller-generated task id.')
    objective: str = Field(default='', description='Objective for this step execution.')
    user_input: str = Field(default='', description='User input forwarded to the step.')
    runtime_instruction: str = Field(default='', description='Attempt-only execution instruction.')
    partial_indices: Dict[str, List[int]] = Field(
        default_factory=dict, description='Optional slot-to-list-index retry selector.')


class InputResourceRef(BaseModel):
    """Immutable resource reference returned by import_input_resource."""

    model_config = ConfigDict(extra='forbid')

    resource_id: str
    revision: int
    content_hash: str


WORKFLOW_SKILL_NAME = 'workflow-agent-kit'


@dataclass(frozen=True)
class AgentWorkflowToolProjection:
    """Host-neutral Workflow capability projection for model-driven Agents.

    Lifecycle mutation remains available through the public API for UI, human,
    and deterministic Host controllers. This projection only controls which
    capabilities are safe to expose to a model for the current Session state.
    """

    session_id: str = ''
    session_status: str = ''

    def expose(self, tools: List[Callable[..., Any]]) -> List[Callable[..., Any]]:
        has_session = bool(self.session_id.strip())
        status = self.session_status.strip().lower()
        exposed: List[Callable[..., Any]] = []
        for tool in tools:
            name = str(getattr(tool, '__name__', ''))
            # Session creation is atomic in Agent Hosts; direct start and model-
            # initiated stop are controller capabilities, never model tools.
            if name in {'prepare_workflow', 'start_workflow', 'stop_workflow'}:
                continue
            if has_session and _is_workflow_trigger(name):
                continue
            if name == 'resume_workflow' and (not has_session or status != 'stopped'):
                continue
            exposed.append(tool)
        return exposed


def _is_workflow_trigger(name: str) -> bool:
    return name.startswith('trigger_') and name.endswith('_workflow')


def workflow_skills_dir() -> str:
    """Return the shared skill root used by every in-process Host."""
    configured = os.getenv('LAZYMIND_WORKFLOW_SKILLS_DIR', '').strip()
    if configured:
        return configured
    if (Path('/skills') / WORKFLOW_SKILL_NAME / 'SKILL.md').is_file():
        return '/skills'
    root = Path(__file__).resolve().parents[2] / 'skills'
    return str(root)


class HostWorkflowToolkit:
    """Expose the complete public Workflow SDK as Agent-callable functions."""

    def __init__(self, client_factory: Callable[[], WorkflowClient],
                 allowed_workflow_ids: Optional[List[str]] = None,
                 origin_ref: str = ''):
        self._client_factory = client_factory
        self._origin_ref = origin_ref.strip()
        self._allowed_workflow_ids = frozenset(
            value.strip() for value in (allowed_workflow_ids or []) if value.strip()
        )

    def _client(self) -> WorkflowClient:
        return self._client_factory()

    def _require_allowed(self, workflow_id: str) -> None:
        if self._allowed_workflow_ids and workflow_id not in self._allowed_workflow_ids:
            raise WorkflowClientError(
                'WORKFLOW_NOT_SELECTED',
                f"Workflow '{workflow_id}' is not selected for this turn.",
                details={'allowed_workflow_ids': sorted(self._allowed_workflow_ids)},
            )

    def workflow_connection_status(self) -> Dict[str, Any]:
        """Verify public Workflow API discovery and connectivity."""
        return self._client().connection_status()

    def list_workflows(self) -> Dict[str, Any]:
        """List enabled Workflows visible to the current user."""
        result = self._client().list_workflows().result
        if not self._allowed_workflow_ids:
            return result
        workflows = result.get('workflows')
        if not isinstance(workflows, list):
            return result
        return {
            **result,
            'workflows': [
                item for item in workflows
                if isinstance(item, dict)
                and str(item.get('workflow_id') or '') in self._allowed_workflow_ids
            ],
        }

    def get_workflow(self, workflow_id: str, revision_id: str = '') -> Dict[str, Any]:
        """Read one public Workflow package and its pinned revision."""
        self._require_allowed(workflow_id)
        return self._client().get_workflow(workflow_id, revision_id).result

    def prepare_workflow(self, workflow_id: str, input_bindings: Optional[Dict[str, Any]] = None,
                         command_id: str = '', request_context: str = '') -> Dict[str, Any]:
        """Prepare a Workflow; in LazyMind create its Session and return Ready steps."""
        self._require_allowed(workflow_id)
        client = self._client()
        prepared = client.prepare_workflow(
            workflow_id, input_bindings=input_bindings, command_id=command_id,
            fields={
                **({'origin_ref': self._origin_ref} if self._origin_ref else {}),
                **({'request_context': request_context} if request_context else {}),
            } or None).result
        if not self._origin_ref or prepared.get('status') != 'ready':
            return prepared
        preparation_id = str(prepared.get('id') or prepared.get('preparation_id') or '')
        if not preparation_id:
            raise WorkflowClientError(
                'INVALID_PREPARATION_RESPONSE',
                'ready preparation did not return preparation_id.',
            )
        started = client.start_workflow(preparation_id, '', command_id='').result
        session_id = str(started.get('session_id') or started.get('workflow_session_id') or '')
        if not session_id:
            raise WorkflowClientError(
                'INVALID_START_RESPONSE',
                'started Workflow did not return session_id.',
            )
        ready = client.get_ready_steps(session_id)
        return {
            **prepared,
            **started,
            'preparation_id': preparation_id,
            'session_id': session_id,
            'state_version': ready.get('state_version', started.get('state_version')),
            'ready_steps': ready.get('ready_steps') or [],
            'ready_step_details': ready.get('ready_step_details') or [],
            'approval_by_step': ready.get('approval_by_step') or {},
            'next_action': {
                'tool': 'advance_step',
                'instruction': (
                    'Call advance_step with exact returned ready step ids. The Host injects '
                    'session, state version, and command identity; do not provide them.'
                ),
            },
        }

    def start_workflow(self, preparation_id: str, session_id: str = '',
                       command_id: str = '') -> Dict[str, Any]:
        """Start a prepared Workflow; omit session_id so the server generates it."""
        return self._client().start_workflow(
            preparation_id, '' if self._origin_ref else session_id,
            command_id=command_id).result

    def get_workflow_state(self, session_id: str) -> Dict[str, Any]:
        """Read the authoritative public projection and state_version."""
        return self._client().get_state(session_id)

    def get_ready_steps(self, session_id: str) -> Dict[str, Any]:
        """Read the current Ready frontier; never infer readiness locally."""
        return self._client().get_ready_steps(session_id)

    def advance_step(self, session_id: str, expected_state_version: int,
                     steps: List[StepCommandInput], command_id: str = '',
                     retry_origin: str = 'automatic') -> Dict[str, Any]:
        """Submit Ready targets; command_id is top-level and never belongs inside steps."""
        commands = [StepCommand(**item.model_dump()) if isinstance(item, StepCommandInput)
                    else StepCommand(**item) for item in steps]
        resolved_command_id = command_id or str(uuid.uuid4())
        result = self._client().advance(AdvanceRequest(
            session_id=session_id, expected_state_version=expected_state_version,
            steps=commands,
            command_id=resolved_command_id,
            retry_origin=retry_origin,
        )).result
        statuses = result.get('attempt_statuses') if isinstance(result, dict) else None
        if isinstance(statuses, dict):
            failed = {
                str(task_id): str(status) for task_id, status in statuses.items()
                if str(status) in {'failed', 'cancelled', 'interrupted'}
            }
            if failed:
                projection = result.get('projection') if isinstance(result.get('projection'), dict) else {}
                retryable = result.get('retryable_steps') or projection.get('retryable') or []
                return {
                    **result,
                    'status': 'failed',
                    'outcome': 'step_failed',
                    'failed_attempts': failed,
                    'retryable_steps': retryable,
                    'next_action': {
                        'decision_owner': 'ChatAgent',
                        'instruction': (
                            'Do not advance a downstream step. Decide whether to retry only an '
                            'exact retryable_steps ID. If automatic_retry_remaining is zero, do '
                            'not retry autonomously; explicitly tell the user that manual retry '
                            'is still available. If the retryable list is empty, report the failure '
                            'to the user.'
                        ),
                    },
                    'command_id': resolved_command_id,
                }
        projection = result.get('projection') if isinstance(result.get('projection'), dict) else {}
        ready = result.get('ready_steps') or projection.get('ready') or []
        nodes = projection.get('nodes') if isinstance(projection.get('nodes'), dict) else {}
        ready_details = []
        for step_id in ready:
            node = nodes.get(step_id) if isinstance(nodes.get(step_id), dict) else {}
            mode = str(node.get('mode') or '').strip()
            requires_approval = (
                bool(node.get('requires_approval')) if 'requires_approval' in node
                else mode == 'human'
            )
            ready_details.append({
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
        completed = bool(projection.get('completed'))
        return {
            **result,
            'status': 'completed' if completed else 'active',
            'outcome': 'workflow_completed' if completed else 'step_succeeded',
            'ready_steps': ready,
            'ready_step_details': ready_details,
            'approval_by_step': {
                item['step_id']: item['default_approval'] for item in ready_details
            },
            'next_action': {
                'tool': None if completed else 'advance_step',
                'instruction': (
                    'Workflow is complete; summarize the final result to the user.'
                    if completed else
                    'Continue in this same ChatAgent turn by selecting exact IDs from the '
                    'returned ready_steps. Stop only for a terminal state, required user input, '
                    'explicit user boundary, or a failed step decision. If the next Ready '
                    'Workflow step requires human approval, execute it with advance_step_and_hand_off; '
                    'approval happens after that step runs, for its result, so do not ask whether '
                    'to execute the step.'
                ),
            },
            'command_id': resolved_command_id,
        }

    def stop_workflow(self, session_id: str, command_id: str = '') -> Dict[str, Any]:
        """Explicitly pause one Session; preserve state and never prepare a replacement."""
        resolved_command_id = command_id or str(uuid.uuid4())
        result = self._client().stop_workflow(session_id, resolved_command_id).result
        return {**result, 'command_id': resolved_command_id}

    def resume_workflow(self, session_id: str, command_id: str = '') -> Dict[str, Any]:
        """Resume the same stopped Session; refresh projection before advancing."""
        resolved_command_id = command_id or str(uuid.uuid4())
        result = self._client().resume_workflow(session_id, resolved_command_id).result
        return {**result, 'command_id': resolved_command_id}

    def get_workflow_command(self, command_id: str) -> Dict[str, Any]:
        """Reconcile an uncertain transition using its previously returned command_id."""
        return self._client().get_command(command_id).result

    def import_input_resource(self, name: str, mime_type: str,
                              content_base64: str) -> Dict[str, Any]:
        """Store immutable attachment bytes as a public Workflow Input Resource."""
        return self._client().import_input_resource(
            name, mime_type, base64.b64decode(content_base64)).result

    def read_input_resource(self, resource_id: str) -> Dict[str, Any]:
        """Read an authorized immutable Input Resource, returning base64 content."""
        value = self._client().read_input_resource(resource_id)
        content = value.pop('content', b'')
        value['content_base64'] = base64.b64encode(content).decode('ascii')
        return value

    def list_workflow_inputs(self, session_id: str) -> Dict[str, Any]:
        """List durable Input Resource bindings for a Workflow Session."""
        return self._client().list_workflow_inputs(session_id).result

    def bind_workflow_input(self, session_id: str, material_id: str,
                            resource: InputResourceRef, command_id: str = '') -> Dict[str, Any]:
        """Bind an exact immutable resource revision to a Session material."""
        resolved_command_id = command_id or str(uuid.uuid4())
        value = resource.model_dump() if isinstance(resource, InputResourceRef) else resource
        result = self._client().bind_workflow_input(
            session_id, material_id, value, resolved_command_id).result
        return {**result, 'command_id': resolved_command_id}

    def list_artifacts(self, session_id: str) -> Dict[str, Any]:
        """List selected output Artifact revisions, including deletion tombstones."""
        return self._client().list_artifacts(session_id).result

    def read_artifact(self, artifact_id: str) -> Dict[str, Any]:
        """Read one authorized immutable Artifact revision and lineage."""
        return self._client().read_artifact(artifact_id).result

    def patch_artifact(self, artifact_id: str, base_revision: int, value: Any,
                       content_type: str = 'json', caption: str = '',
                       command_id: str = '') -> Dict[str, Any]:
        """Create a new selected Artifact revision without overwriting history."""
        resolved_command_id = command_id or str(uuid.uuid4())
        result = self._client().patch_artifact(
            artifact_id, base_revision, value, content_type, caption, resolved_command_id).result
        return {**result, 'command_id': resolved_command_id}

    def delete_artifact(self, artifact_id: str, base_revision: int,
                        command_id: str = '') -> Dict[str, Any]:
        """Create a selected deletion tombstone revision; never erase history."""
        resolved_command_id = command_id or str(uuid.uuid4())
        result = self._client().delete_artifact(
            artifact_id, base_revision, resolved_command_id).result
        return {**result, 'command_id': resolved_command_id}

    def list_skills(self) -> Dict[str, Any]:
        """List Skills visible for deterministic Skill-to-Workflow conversion."""
        return self._client().list_skills().result

    def get_skill_conversion_context(self, skill_id: str,
                                     revision_id: str = '') -> Dict[str, Any]:
        """Read the complete immutable Skill snapshot; never summarize with a tool model."""
        return self._client().get_skill_conversion_context(skill_id, revision_id).result

    def create_workflow_draft(self, name: str, files: Dict[str, str], skill_id: str = '',
                              revision_id: str = '', tree_hash: str = '',
                              source_type: str = '') -> Dict[str, Any]:
        """Store exact Agent-authored Workflow package files as a draft."""
        return self._client().create_workflow_draft(
            name, skill_id, revision_id, tree_hash, files, source_type).result

    def list_workflow_drafts(self) -> Dict[str, Any]:
        """List Workflow drafts owned by the current user."""
        return self._client().list_workflow_drafts().result

    def get_workflow_draft(self, draft_id: str) -> Dict[str, Any]:
        """Read one Workflow draft and its exact package files."""
        return self._client().get_workflow_draft(draft_id).result

    def delete_workflow_draft(self, draft_id: str) -> Dict[str, Any]:
        """Delete an unpublished Workflow draft; published revisions are unaffected."""
        return self._client().delete_workflow_draft(draft_id).result

    def update_workflow_draft_file(self, draft_id: str, path: str, content: str,
                                   expected_version: int) -> Dict[str, Any]:
        """Store one exact Agent-authored file with optimistic version checking."""
        return self._client().update_workflow_draft_file(
            draft_id, path, content, expected_version).result

    def validate_workflow_draft(self, draft_id: str) -> Dict[str, Any]:
        """Run the deterministic Workflow compiler; never repair with an internal model."""
        return self._client().validate_workflow_draft(draft_id).result

    def get_workflow_diagnostics(self, draft_id: str) -> Dict[str, Any]:
        """Read deterministic graph, package, capability, and script diagnostics."""
        return self._client().get_workflow_diagnostics(draft_id).result

    def publish_workflow(self, draft_id: str) -> Dict[str, Any]:
        """Publish a draft only after deterministic diagnostics are valid."""
        return self._client().publish_workflow(draft_id).result

    def list_workflow_versions(self, workflow_ref: str) -> Dict[str, Any]:
        """List immutable published revisions for one Workflow."""
        return self._client().list_workflow_versions(workflow_ref).result

    def archive_workflow(self, workflow_ref: str) -> Dict[str, Any]:
        """Archive a published Workflow while preserving immutable history."""
        return self._client().archive_workflow(workflow_ref).result

    def restore_workflow(self, workflow_ref: str) -> Dict[str, Any]:
        """Restore an archived Workflow without changing its revisions."""
        return self._client().restore_workflow(workflow_ref).result

    def tools(self) -> List[Callable[..., Any]]:
        """Return the complete common tool set in stable lifecycle order."""
        tools = [
            self.workflow_connection_status, self.list_workflows, self.get_workflow,
            self.prepare_workflow, self.start_workflow,
            self.get_workflow_state, self.get_ready_steps, self.advance_step,
            self.stop_workflow, self.resume_workflow, self.get_workflow_command,
            self.import_input_resource, self.read_input_resource,
            self.list_workflow_inputs, self.bind_workflow_input,
            self.list_artifacts, self.read_artifact, self.patch_artifact,
            self.delete_artifact, self.list_skills, self.get_skill_conversion_context,
            self.create_workflow_draft, self.list_workflow_drafts,
            self.get_workflow_draft, self.delete_workflow_draft,
            self.update_workflow_draft_file,
            self.validate_workflow_draft, self.get_workflow_diagnostics,
            self.publish_workflow, self.list_workflow_versions,
            self.archive_workflow, self.restore_workflow,
        ]
        if self._origin_ref:
            tools.remove(self.start_workflow)
        return tools

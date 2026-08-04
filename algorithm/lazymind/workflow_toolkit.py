"""Host-neutral callable tools for the public Workflow runtime.

Every function is deterministic infrastructure. Only ``advance_step`` may cause
the runtime Supervisor to launch an explicit Workflow SubAgent after acceptance.
"""
from __future__ import annotations

import base64
import os
import uuid
from pathlib import Path
from typing import Any, Callable, Dict, List, Optional

from lazymind.workflow_sdk import AdvanceRequest, StepCommand, WorkflowClient


WORKFLOW_SKILL_NAME = 'workflow-agent-kit'


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

    def __init__(self, client_factory: Callable[[], WorkflowClient]):
        self._client_factory = client_factory

    def _client(self) -> WorkflowClient:
        return self._client_factory()

    def workflow_connection_status(self) -> Dict[str, Any]:
        """Verify public Workflow API discovery and connectivity."""
        return self._client().connection_status()

    def list_workflows(self) -> Dict[str, Any]:
        """List enabled Workflows visible to the current user."""
        return self._client().list_workflows().result

    def get_workflow(self, workflow_id: str, revision_id: str = '') -> Dict[str, Any]:
        """Read one public Workflow package and its pinned revision."""
        return self._client().get_workflow(workflow_id, revision_id).result

    def prepare_workflow(self, workflow_id: str, input_bindings: Optional[Dict[str, Any]] = None,
                         command_id: str = '') -> Dict[str, Any]:
        """Validate a Workflow and durable inputs without creating a Session."""
        return self._client().prepare_workflow(
            workflow_id, input_bindings=input_bindings, command_id=command_id).result

    def start_workflow(self, preparation_id: str, session_id: str,
                       command_id: str = '') -> Dict[str, Any]:
        """Consume a ready preparation and create its public Workflow Session."""
        return self._client().start_workflow(
            preparation_id, session_id, command_id=command_id).result

    def get_workflow_state(self, session_id: str) -> Dict[str, Any]:
        """Read the authoritative public projection and state_version."""
        return self._client().get_state(session_id)

    def get_ready_steps(self, session_id: str) -> Dict[str, Any]:
        """Read the current Ready frontier; never infer readiness locally."""
        return self._client().get_ready_steps(session_id)

    def advance_step(self, session_id: str, expected_state_version: int,
                     steps: List[Dict[str, Any]], command_id: str = '') -> Dict[str, Any]:
        """Submit Ready targets; Runtime deterministically resolves execute/retry/rewind."""
        return self._client().advance(AdvanceRequest(
            session_id=session_id, expected_state_version=expected_state_version,
            steps=[StepCommand(**item) for item in steps],
            command_id=command_id or str(uuid.uuid4()),
        )).result

    def stop_workflow(self, session_id: str, command_id: str = '') -> Dict[str, Any]:
        """Stop a Workflow while preserving durable state and Artifact history."""
        return self._client().stop_workflow(session_id, command_id).result

    def resume_workflow(self, session_id: str, command_id: str = '') -> Dict[str, Any]:
        """Resume a stopped Workflow from its persisted public projection."""
        return self._client().resume_workflow(session_id, command_id).result

    def get_workflow_command(self, command_id: str) -> Dict[str, Any]:
        """Reconcile the durable result of an idempotent Workflow command."""
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
                            resource: Dict[str, Any], command_id: str = '') -> Dict[str, Any]:
        """Bind an exact immutable resource revision to a Session material."""
        return self._client().bind_workflow_input(
            session_id, material_id, resource, command_id).result

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
        return self._client().patch_artifact(
            artifact_id, base_revision, value, content_type, caption, command_id).result

    def delete_artifact(self, artifact_id: str, base_revision: int,
                        command_id: str = '') -> Dict[str, Any]:
        """Create a selected deletion tombstone revision; never erase history."""
        return self._client().delete_artifact(artifact_id, base_revision, command_id).result

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
        return [
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

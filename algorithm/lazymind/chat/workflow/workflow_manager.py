"""LazyMind Chat adapter for the public Workflow runtime.

This module deliberately owns no Workflow definition loading, graph policy,
transition state, input binding, or Artifact persistence.  It only turns the
public Workflow SDK into ChatAgent tools and applies LazyMind's handoff rule.
"""
from __future__ import annotations

import json
import logging
import uuid
from dataclasses import asdict, dataclass
from typing import Any, Dict, List, Optional

import httpx
import lazyllm

from lazymind.chat.engine.tools.intent_writer import enable_workflow_intent_scopes
from lazymind.workflow_sdk import AdvanceRequest, StepCommand, WorkflowClient, WorkflowClientError
from lazymind.workflow_toolkit import HostWorkflowToolkit

LOG = logging.getLogger(__name__)


@dataclass
class WorkflowAgentContribution:
    tools: List[Any]
    stop_tools: List[str]
    agentic_config_patch: Dict[str, Any]
    runtime_context: str


def _agentic_config() -> Dict[str, Any]:
    return lazyllm.globals.get('agentic_config', {}) or {}


def _client() -> WorkflowClient:
    from lazymind.config import config
    cfg = _agentic_config()
    return WorkflowClient(
        str(config['core_api_url']).rstrip('/'),
        str(cfg.get('user_id') or ''),
        host='lazymind',
        transport=httpx,
    )


def _result_text(value: Any) -> str:
    if hasattr(value, 'result'):
        value = value.result
    return json.dumps(value, ensure_ascii=False, default=str)


def _workflow_definition(workflow_id: str, revision_id: str = '') -> Dict[str, Any]:
    try:
        return _client().get_workflow(workflow_id, revision_id).result
    except WorkflowClientError:
        LOG.exception('public Workflow definition read failed id=%s', workflow_id)
        return {}


def _step_ids(workflow_id: str, revision_id: str = '') -> List[str]:
    package = _workflow_definition(workflow_id, revision_id)
    graph = package.get('compiled_graph') if isinstance(package.get('compiled_graph'), dict) else {}
    nodes = graph.get('nodes') if isinstance(graph.get('nodes'), dict) else {}
    return list(nodes)


def _state(session_id: str) -> Dict[str, Any]:
    try:
        return _client().get_state(session_id)
    except WorkflowClientError as exc:
        return {'error': {'code': exc.code, 'message': exc.message}}


def _handoff_tool(session_id: str) -> Any:
    def advance_step_and_hand_off(
        step_id: str,
        objective: str = '',
        user_input: str = '',
        runtime_instruction: str = '',
    ) -> str:
        """Advance one Ready Workflow step through the public runtime."""
        state = _client().get_state(session_id)
        version = int(state.get('state_version') or 0)
        response = _client().advance(AdvanceRequest(
            session_id=session_id,
            expected_state_version=version,
            steps=[StepCommand(
                step_id=step_id,
                objective=objective,
                user_input=user_input,
                runtime_instruction=runtime_instruction,
            )],
            handoff=True,
            command_id=str(uuid.uuid4()),
        ))
        return _result_text(response)

    advance_step_and_hand_off.__doc__ = (
        'Submit a Ready target and end this LazyMind turn only after durable '
        'Supervisor ownership is acknowledged.'
    )
    return advance_step_and_hand_off


def _attachment_import_tool() -> Any:
    def import_workflow_attachment(path: str) -> Dict[str, Any]:
        """Import a selected LazyMind attachment into the public resource store."""
        from lazymind.chat.workflow.file_adapter import LazyMindHostFileAdapter
        from lazymind.config import config
        cfg = _agentic_config()
        value = LazyMindHostFileAdapter(
            str(config['core_api_url']).rstrip('/'), str(cfg.get('user_id') or ''),
            transport=httpx,
        ).import_attachment(path)
        return asdict(value)

    return import_workflow_attachment


def resolve_workflow_injection(
    workflow_context: Optional[Dict[str, Any]],
    conversation_id: str = '',
    workflow_catalog: Optional[List[Dict[str, Any]]] = None,
    disabled_builtin_workflows: Optional[List[str]] = None,
    allowed_workflow_refs: Optional[List[str]] = None,
) -> WorkflowAgentContribution:
    """Map public Workflow APIs to LazyMind Chat tools; no Runtime decisions live here."""
    del conversation_id
    cfg = _agentic_config()
    if not cfg.get('enable_workflow', True):
        return WorkflowAgentContribution([], [], {}, '')

    context = workflow_context if isinstance(workflow_context, dict) else {}
    session_id = str(context.get('session_id') or '')
    workflow_id = str(context.get('workflow_id') or context.get('workflow_ref') or '')
    revision_id = str(context.get('revision_id') or '')
    mode = str(context.get('workflow_mode') or 'dynamic')
    patch: Dict[str, Any] = {'workflow_mode': mode}

    tools = [*HostWorkflowToolkit(_client).tools(), _attachment_import_tool()]
    if session_id:
        patch.update({
            'workflow_id': workflow_id,
            'workflow_session_id': session_id,
            'workflow_step': context.get('current_step') or '',
            'workflow_ref': context.get('workflow_ref') or '',
            'revision_id': revision_id,
        })
        tools.append(_handoff_tool(session_id))
        runtime_context = (
            '## Workflow Runtime [AUTHORITATIVE]\n'
            + json.dumps(_state(session_id), ensure_ascii=False, default=str)
        )
        return WorkflowAgentContribution(
            tools, ['advance_step_and_hand_off'],
            patch, runtime_context,
        )

    del workflow_catalog, disabled_builtin_workflows, allowed_workflow_refs
    return WorkflowAgentContribution(
        tools, [], patch, '',
    )


def update_intentwriter(tool: Any, workflow_context: Optional[Dict[str, Any]]) -> Any:
    """Add LazyMind intent scopes using step ids read from the public package."""
    context = workflow_context if isinstance(workflow_context, dict) else {}
    session_id = str(context.get('session_id') or '')
    workflow_id = str(context.get('workflow_id') or context.get('workflow_ref') or '')
    if not session_id or not workflow_id:
        return tool
    return enable_workflow_intent_scopes(
        tool,
        session_id=session_id,
        workflow_id=workflow_id,
        valid_step_ids=_step_ids(workflow_id, str(context.get('revision_id') or '')),
    )


def _build_chat_agent_task_context(conversation_id: str) -> str:
    """Generic LazyMind task presentation; unrelated to Workflow state authority."""
    if not conversation_id.strip():
        return ''
    from lazymind.chat.engine.subagent.db import TaskQueryDB
    return TaskQueryDB().build_chat_agent_task_context(conversation_id.strip())


async def guard_workflow_agent_stream(initial_stream: Any, **_: Any):
    """LazyMind handoff is enforced by declaring its tool as a stop tool."""
    async for item in initial_stream:
        yield item

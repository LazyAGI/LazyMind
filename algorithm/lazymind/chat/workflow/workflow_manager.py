"""Workflow manager — builds ChatAgent tools for cold-start triggers and step advancement.

Tool types registered dynamically per-conversation:

- trigger_<workflow_id>       : Cold-start tool. Injected when no active workflow session exists.
- advance_step_and_hand_off : Asynchronous stop-tool accepting one or more step commands.
- advance_step              : Synchronous tool accepting one or more step commands; dynamic mode only.
- ask_user                  : Ask the user a question (stop-tool). ChatAgent only; absent in auto mode.
- intentwrite               : Extended with workflow-session and workflow-step scopes when active.
- list_workflow_steps         : Read-only step status query (ChatAgent only, when session active).
- get_step_result           : Read-only artifact summary for a step (ChatAgent only).
- get_failed_steps          : Read-only failed steps with error info (ChatAgent only).

Framework tools (save_artifacts / get_artifact / list_artifacts) are always merged into
the step's tool list regardless of what the workflow's state.yml declares.  This ensures
every SubAgent can persist and retrieve artifacts without workflow authors having to
remember to list them explicitly.
"""
from __future__ import annotations

import hashlib
import json
import logging
import re
import uuid
from dataclasses import dataclass
from typing import Any, Dict, List, Optional

import lazyllm
from lazyllm.tools.agent.base import _write_agent_data

from lazymind.chat.workflow import workflow_loader
from lazymind.chat.engine.subagent import SUBAGENT_CORE_TOOL_NAMES
from lazymind.chat.engine.tools.intent_writer import enable_workflow_intent_scopes
from lazymind.model_config import is_model_role_available
from lazymind.chat.workflow.client import (
    WorkflowTransportTimeout, workflow_http_get, workflow_http_post,
)

_FRAMEWORK_TOOLS = tuple(SUBAGENT_CORE_TOOL_NAMES)

LOG = logging.getLogger(__name__)

_PREFLIGHT_DECISIONS = {'ready', 'need_information', 'not_applicable'}
_PREFLIGHT_TIMEOUT_SECONDS = 30.0


@dataclass
class WorkflowAgentContribution:
    tools: List[Any]
    system_prompt: str
    stop_tools: List[str]
    agentic_config_patch: Dict[str, Any]
    runtime_context: str


@dataclass(frozen=True)
class _ReachabilitySnapshot:
    current_step: str
    session_id: str
    forward_steps: List[str]
    rewind_steps: List[str]
    retry_steps: List[str]
    reachable_steps: List[str]


@dataclass(frozen=True)
class _TransitionSubmission:
    accepted: bool
    message: str
    command_id: str = ''
    task_id: str = ''
    session_id: str = ''
    state_version: int = 0
    projection: Optional[Dict[str, Any]] = None
    tasks: Optional[List[Dict[str, str]]] = None


def _core_response_data(response: Any) -> Dict[str, Any]:
    try:
        body = response.json()
    except Exception:
        return {}
    if isinstance(body, dict) and isinstance(body.get('data'), dict):
        return body['data']
    return body if isinstance(body, dict) else {}


def _format_transition_rejection(step_id: str, data: Dict[str, Any]) -> str:
    error = data.get('error') if isinstance(data.get('error'), dict) else {}
    code = str(error.get('code') or 'TRANSITION_REJECTED')
    reason = str(error.get('message') or 'Go rejected the workflow state transition.')
    details = error.get('details') if isinstance(error.get('details'), dict) else {}
    projection = data.get('projection') if isinstance(data.get('projection'), dict) else {}
    ready = projection.get('ready') or details.get('ready') or []
    blocked = projection.get('blocked') or []
    missing = details.get('missing_groups') or []
    rejected_targets = details.get('targets') or []
    lines = [
        f'Transition rejected [{code}].',
        f'Target: {step_id}',
        f'Reason: {reason}',
    ]
    if missing:
        lines.append(f'Missing material groups: {missing}')
    if rejected_targets:
        lines.append(f'Rejected batch targets: {rejected_targets}')
    if ready:
        lines.append(f'Currently ready: {ready}')
    if blocked:
        lines.append(f'Currently blocked: {blocked}')
    lines.append(
        'Do not wait for this step. Use the returned live projection to choose '
        'another action or explain the blocker.'
    )
    return '\n'.join(lines)


def _submit_transition_to_core(
        *, workflow_id: str, step_id: str, session_id: str, task_id: str,
        objective: str, user_input: str, hand_off: bool,
        runtime_instruction: str, partial_indices: Dict[str, List[int]],
        operation: str = 'advance', is_start: bool = False,
        preflight_id: str = '',
        targets: Optional[List[Dict[str, Any]]] = None) -> _TransitionSubmission:
    from lazymind.chat.workflow.client import (
        AdvanceRequest, StepCommand, WorkflowClient, WorkflowClientError,
        record_legacy_client_call, use_legacy_client,
    )
    import httpx
    from lazymind.config import config as _cfg

    cfg = _agentic_config()
    core_url = str(_cfg['core_api_url']).rstrip('/')
    if not use_legacy_client():
        client = WorkflowClient(core_url, str(cfg.get('user_id') or ''), transport=httpx)
        command_id = str(uuid.uuid4())
        commands = targets or [{
            'target_step_id': step_id, 'task_id': task_id, 'objective': objective,
            'user_input': user_input, 'runtime_instruction': runtime_instruction,
            'partial_indices': partial_indices,
        }]
        typed_steps = [StepCommand(
            step_id=str(item.get('target_step_id') or ''), task_id=str(item.get('task_id') or ''),
            objective=str(item.get('objective') or ''), user_input=str(item.get('user_input') or ''),
            runtime_instruction=str(item.get('runtime_instruction') or ''),
            partial_indices=item.get('partial_indices') or {},
        ) for item in commands]
        try:
            if is_start:
                response = client.prepare_start(workflow_id, session_id, command_id, {
                    'workflow_revision_id': str(cfg.get('revision_id') or ''),
                    'external_materials': cfg.get('workflow_external_materials') or {},
                    'command_id': command_id, 'target_step_id': step_id, 'task_id': task_id,
                    'objective': objective, 'user_input': user_input, 'hand_off': hand_off,
                    'preflight_id': preflight_id,
                })
            else:
                response = client.advance(AdvanceRequest(
                    session_id=session_id,
                    expected_state_version=int(cfg.get('_workflow_state_version') or 0),
                    steps=typed_steps, handoff=hand_off, command_id=command_id,
                ))
            data = response.result
            accepted = bool(data.get('accepted', True))
            tasks = data.get('tasks') if isinstance(data.get('tasks'), list) else []
            normalised = [{
                'step_id': str(item.get('step_id') or step_id),
                'task_id': str(item.get('task_id') or task_id),
                'step_state': str(item.get('step_state') or 'pending'),
            } for item in tasks if isinstance(item, dict)] or [{
                'step_id': step_id, 'task_id': str(data.get('task_id') or task_id),
                'step_state': str(data.get('step_state') or 'pending'),
            }]
            return _TransitionSubmission(
                accepted, str(data.get('message') or 'Workflow transition accepted; acceptance is pending.'),
                command_id=command_id, task_id=normalised[0]['task_id'],
                session_id=str(data.get('session_id') or session_id),
                state_version=int(data.get('state_version') or 0),
                projection=data.get('projection') if isinstance(data.get('projection'), dict) else {},
                tasks=normalised,
            )
        except WorkflowClientError as exc:
            return _TransitionSubmission(False, f'{exc.code}: {exc}', command_id=command_id)
    record_legacy_client_call('_submit_transition_to_core')
    projection_data: Dict[str, Any] = {}
    if not is_start:
        try:
            projection_resp = workflow_http_get(
                f'{core_url}/internal/workflow-sessions/{session_id}/projection', timeout=5.0,
                transport=httpx,
            )
            if projection_resp.status_code == 200:
                projection_data = _core_response_data(projection_resp)
        except Exception as exc:
            LOG.warning('[workflow.transition] projection prefetch failed session=%s error=%s', session_id, exc)
    command_id = str(uuid.uuid4())
    expected_version = int(projection_data.get('state_version') or cfg.get('_workflow_state_version') or 0)
    graph_hash = str(projection_data.get('graph_hash') or '')
    payload = {
        'command_id': command_id,
        'operation': operation,
        'target_step_id': step_id,
        'expected_state_version': expected_version,
        'graph_hash': graph_hash,
        'task_id': task_id,
        'objective': objective,
        'user_input': user_input,
        'runtime_instruction': runtime_instruction,
        'partial_indices': partial_indices,
        'hand_off': hand_off,
        'workflow_mode': str(cfg.get('workflow_mode') or 'dynamic'),
        'chat_session_id': str(cfg.get('session_id') or ''),
        'history_files_per_turn': cfg.get('history_files_per_turn') or {},
        'filters': cfg.get('filters') or {},
        'llm_config': cfg.get('llm_config') or {},
        'tool_config': cfg.get('tool_config') or {},
        'parent_agentic_config': _export_parent_agentic_config(cfg),
        'workflow_id': workflow_id,
        'workflow_ref': str(cfg.get('workflow_ref') or ''),
        'workflow_revision_id': str(cfg.get('revision_id') or ''),
        'workflow_revision_no': int(cfg.get('revision_no') or 0),
        'workflow_tree_hash': str(cfg.get('tree_hash') or ''),
        'workflow_remote_root': str(cfg.get('remote_root') or ''),
        'conversation_id': str(cfg.get('conversation_id') or ''),
        'trigger_history_id': str(cfg.get('history_id') or ''),
        'user_id': str(cfg.get('user_id') or ''),
        'preflight_id': preflight_id,
        'external_materials': cfg.get('workflow_external_materials') or {},
    }
    if targets:
        payload['targets'] = targets
    endpoint = (
        f'{core_url}/internal/workflow-sessions:start'
        if is_start else f'{core_url}/internal/workflow-sessions/{session_id}:transition'
    )
    try:
        response = workflow_http_post(endpoint, json=payload, timeout=15.0, transport=httpx)
        data = _core_response_data(response)
    except WorkflowTransportTimeout:
        # The command id makes an ambiguous network timeout reconcilable without
        # submitting a second transition.
        try:
            status_resp = workflow_http_get(
                f'{core_url}/internal/workflow-transition-commands/{command_id}', timeout=5.0,
                transport=httpx,
            )
            data = _core_response_data(status_resp)
            response = status_resp
        except Exception:
            message = (
                'Transition result unknown [TRANSITION_RESULT_UNKNOWN].\n'
                f'Command id: {command_id}\nDo not resubmit with a new command id.'
            )
            return _TransitionSubmission(False, message, command_id=command_id)
    except Exception as exc:
        return _TransitionSubmission(
            False,
            f'Transition result unknown [TRANSITION_RESULT_UNKNOWN].\nCommand id: {command_id}\nReason: {exc}',
            command_id=command_id,
        )
    error = data.get('error') if isinstance(data.get('error'), dict) else {}
    if response.status_code == 409 and error.get('code') == 'STATE_VERSION_CONFLICT':
        details = error.get('details') if isinstance(error.get('details'), dict) else {}
        latest_version = int(details.get('actual') or data.get('state_version') or 0)
        if latest_version > expected_version:
            # Step completion and route freezing are separate writes. The task waiter can
            # observe "succeeded" just before route reconciliation increments the session
            # version. Retry this explicitly retryable admission conflict once against the
            # authoritative version returned by Go.
            command_id = str(uuid.uuid4())
            payload['command_id'] = command_id
            payload['expected_state_version'] = latest_version
            try:
                response = workflow_http_post(endpoint, json=payload, timeout=15.0, transport=httpx)
                data = _core_response_data(response)
                expected_version = latest_version
            except Exception as exc:
                return _TransitionSubmission(
                    False,
                    f'Transition result unknown [TRANSITION_RESULT_UNKNOWN].\n'
                    f'Command id: {command_id}\nReason: {exc}',
                    command_id=command_id,
                )
    accepted = bool(data.get('accepted')) and response.status_code < 300
    if not accepted:
        rejection = data.get('error') if isinstance(data.get('error'), dict) else {}
        LOG.warning(
            '[workflow.transition] rejected workflow=%s step=%s session=%s command=%s operation=%s '
            'http_status=%s code=%s reason=%s details=%s',
            workflow_id, step_id, session_id, command_id, operation,
            response.status_code, rejection.get('code', ''), rejection.get('message', ''),
            rejection.get('details') if isinstance(rejection.get('details'), dict) else {},
        )
        return _TransitionSubmission(
            False, _format_transition_rejection(step_id, data), command_id=command_id,
            state_version=int(data.get('state_version') or expected_version),
            projection=data.get('projection') if isinstance(data.get('projection'), dict) else {},
        )
    state_version = int(data.get('state_version') or expected_version)
    cfg['_workflow_state_version'] = state_version
    response_tasks = data.get('tasks') if isinstance(data.get('tasks'), list) else []
    normalised_tasks = [
        {
            'step_id': str(item.get('step_id') or ''),
            'task_id': str(item.get('task_id') or ''),
            'step_state': str(item.get('step_state') or ''),
        }
        for item in response_tasks if isinstance(item, dict) and item.get('task_id')
    ]
    if not normalised_tasks:
        normalised_tasks = [{
            'step_id': step_id,
            'task_id': str(data.get('task_id') or task_id),
            'step_state': str(data.get('step_state') or 'pending'),
        }]
    cfg['_last_workflow_task_id'] = normalised_tasks[0]['task_id']
    cfg['_last_workflow_tasks'] = normalised_tasks
    if is_start:
        cfg['workflow_session_id'] = str(data.get('session_id') or '')
        cfg['workflow_id'] = workflow_id
        cfg['workflow_step'] = step_id
    return _TransitionSubmission(
        True,
        (
            f'Batch advance for steps {[item["step_id"] for item in normalised_tasks]!r} '
            'accepted by Go and durably queued.'
            if len(normalised_tasks) > 1
            else f'Advance for step {step_id!r} accepted by Go; acceptance is pending durable task observation.'
        ),
        command_id=command_id,
        task_id=str(data.get('task_id') or task_id),
        session_id=str(data.get('session_id') or session_id),
        state_version=state_version,
        projection=data.get('projection') if isinstance(data.get('projection'), dict) else {},
        tasks=normalised_tasks,
    )


def _fetch_go_start_candidates(workflow_id: str) -> List[str]:
    """Return Go's authoritative Ready set for a not-yet-started session."""
    import httpx
    from lazymind.config import config as _cfg

    cfg = _agentic_config()
    core_url = str(_cfg['core_api_url']).rstrip('/')
    if not cfg.get('workflow_external_materials'):
        cfg['workflow_external_materials'] = _import_host_input_resources(cfg, core_url, httpx)
    payload = {
        'workflow_id': workflow_id,
        'workflow_revision_id': str(cfg.get('revision_id') or ''),
        'external_materials': cfg.get('workflow_external_materials') or {},
    }
    response = workflow_http_post(
        f'{core_url}/internal/workflow-sessions:plan-start', json=payload, timeout=10.0,
        transport=httpx,
    )
    data = _core_response_data(response)
    if response.status_code >= 300:
        raise RuntimeError(str(data.get('error') or data.get('message') or 'Go start planning failed'))
    projection = data.get('projection') or {}
    ready = projection.get('ready') or []
    if not isinstance(ready, list):
        raise RuntimeError('Go start planning returned an invalid Ready set')
    hints: Dict[str, List[str]] = {}
    for edge in projection.get('edges') or []:
        if not isinstance(edge, dict) or edge.get('state') != 'active':
            continue
        target = str(edge.get('to') or '')
        when = str(edge.get('when') or '').strip()
        if target and when:
            hints.setdefault(target, []).append(when)
    cfg['_workflow_start_route_hints'] = hints
    return [str(step_id) for step_id in ready if step_id]


def _import_host_input_resources(cfg: Dict[str, Any], core_url: str, transport: Any) -> Dict[str, Any]:
    """Import Host attachments and return only stable neutral references."""
    from lazymind.chat.workflow.file_adapter import LazyMindHostFileAdapter

    paths = [
        path for turn_paths in (cfg.get('history_files_per_turn') or {}).values()
        for path in turn_paths
    ]
    if not paths:
        return {}
    adapter = LazyMindHostFileAdapter(core_url, str(cfg.get('user_id') or ''), transport=transport)
    resources: Dict[str, Any] = {}
    for index, path in enumerate(dict.fromkeys(paths)):
        imported = adapter.import_attachment(path)
        resources[f'attachment_{index + 1}'] = {
            'resource_id': imported.resource_id, 'revision': imported.revision,
            'content_hash': imported.content_hash, 'name': imported.name,
            'mime_type': imported.mime_type, 'size': imported.size,
        }
    return resources


def is_workflow_driver_turn(workflow_context: Any) -> bool:
    """Return whether this request is a synthetic turn initiated by DriverAgent."""
    return bool(
        isinstance(workflow_context, dict)
        and workflow_context.get('synthetic_source') == 'driver'
    )


_COLD_START_PLUGIN_PROMPT = (
    '## Available Workflows\n'
    'The product term is workflow. "Workflow" is a legacy internal synonym only.\n'
    'IMPORTANT: Only trigger a workflow when the capability matches the '
    "user's PRIMARY and DIRECT intent — the main goal they are asking for "
    'right now. Never trigger a workflow for a sub-step that the model has '
    "internally decided is part of a larger multi-step plan. If the user's "
    'request involves multiple steps and only one of those steps would use a '
    'workflow, do NOT trigger the workflow. Never infer workflow intent from '
    'indirect or implicit cues.\n'
    'When a workflow matches, call its `trigger_<workflow>` preflight tool. Trigger does NOT '
    'start a task. It loads the full workflow and returns ready, need_information, '
    'not_applicable, or preflight_failed.\n'
    'If trigger returns ready, you MUST immediately follow its returned instruction and '
    'call the applicable advancement tool in the SAME turn. Do not explain, confirm, or '
    'end the turn first.\n'
    'If it returns need_information, use ask_user only when that tool is available.\n\n'
    'CRITICAL — explicit workflow requests:\n'
    'If the user explicitly names a workflow and asks to use, run, start, launch, open, or '
    'enable it (e.g. "使用 AI Writer workflow", "用 AI Writer 工作流", '
    '"启动绘图工作流", "use the image workflow"), '
    'you MUST call the matching `trigger_<workflow_id>` tool in this same '
    'response before any other action. Do NOT reply with text only, do NOT call '
    'a generic toolkit or same-domain tool directly (including writing, image, or video tools), '
    'and do NOT ask clarification '
    'questions first. Pass the complete request as `request_context` and set '
    '`explicit_workflow_request=true`.\n\n'
)


# ---------------------------------------------------------------------------
# Framework tools always injected into every workflow step regardless of what
# the workflow's state.yml declares.
# ---------------------------------------------------------------------------

def _merge_tools(declared: List[str]) -> List[str]:
    """Return a deduplicated tool list with framework tools prepended."""
    seen = set()
    merged: List[str] = []
    for t in (*SUBAGENT_CORE_TOOL_NAMES, *declared):
        if t not in seen:
            seen.add(t)
            merged.append(t)
    return merged


# ---------------------------------------------------------------------------
# Internal helpers
# ---------------------------------------------------------------------------

def _fetch_go_projection(session_id: str) -> Dict[str, Any]:
    """Return Go's authoritative runtime projection for a session."""
    if not session_id:
        return {}
    try:
        import httpx
        from lazymind.config import config as _cfg
        core_url = str(_cfg['core_api_url']).rstrip('/')
        resp = workflow_http_get(
            f'{core_url}/internal/workflow-sessions/{session_id}/projection', timeout=5.0,
            transport=httpx,
        )
        if resp.status_code != 200:
            return {}
        data = _core_response_data(resp)
        projection = data.get('projection') or {}
        if isinstance(projection, dict):
            _agentic_config()['_workflow_state_version'] = int(data.get('state_version') or 0)
            return projection
    except Exception:
        pass
    return {}


def _agentic_config() -> Dict[str, Any]:
    try:
        return lazyllm.globals['agentic_config'] or {}
    except Exception:
        return {}


def _export_parent_agentic_config(config: Dict[str, Any]) -> Dict[str, Any]:
    """Return the JSON-safe request context a workflow SubAgent should inherit."""
    exported: Dict[str, Any] = {}
    for key, value in (config or {}).items():
        # Credentials/config blobs have dedicated top-level transport fields and
        # must not be duplicated into the persisted SubAgent context.
        if key in {'citation_state', 'llm_config', 'tool_config', 'ocr_config'}:
            continue
        try:
            json.dumps(value)
        except (TypeError, ValueError):
            continue
        exported[key] = value
    return exported


def _render_step_objective(
    step_config: Dict[str, Any],
    user_input: str,
    runtime_instruction: str = '',
) -> str:
    """Replace {{user_input}} and {{runtime_instruction}} in state.yml step.prompt.

    {{user_input}} is replaced with the actual user input.
    {{runtime_instruction}} is replaced with the ephemeral instruction when provided,
    or removed (replaced with empty string) when absent.

    Other template vars (e.g. {{optimized_prompt}}) are left as-is; Go injects them
    by querying sub_agent_artifacts before launching the SubAgent.
    """
    prompt = step_config.get('prompt', '')
    prompt = prompt.replace('{{user_input}}', user_input)
    prompt = prompt.replace('{{runtime_instruction}}', runtime_instruction)
    return prompt


def _trigger_workflow_step(
        workflow_id: str, step_id: str, user_input: str,
        is_cold_start: bool = False,
        hand_off: bool = False,
        preflight_id: str = '',
        runtime_instruction: str = '',
        partial_indices: Optional[Dict[str, List[int]]] = None,
        operation: str = 'advance') -> str:
    """Shared implementation for trigger_<workflow_id> and advance_step.

    Performs local request-shape validation, then submits a synchronous Go
    transition command. Go is the sole authority for Reachable/Ready admission.

    Args:
        workflow_id: The workflow identifier.
        step_id: The step to trigger.
        user_input: The user's original or latest input.
        is_cold_start: True for the first step of a new session.
        runtime_instruction: Optional ephemeral instruction injected into the step
            objective for this execution only.  Used for retries where the user
            wants to refine or partially regenerate the output.
            Not persisted to session state.
        partial_indices: Maps slot → list_index values that should overwrite
            existing list-slot entries rather than appending. None means full write.
    """
    cfg = _agentic_config()
    session_id: str = cfg.get('workflow_session_id', '') or str(uuid.uuid4())
    current_step: str = cfg.get('workflow_step', '')
    LOG.info(
        '[workflow.advance] trigger requested workflow=%s step=%s session=%s current=%s cold=%s input_len=%d',
        workflow_id, step_id, session_id, current_step or '__start__', is_cold_start, len(user_input or ''),
    )

    # --- Layer 1: format validation (no DB needed) ---
    if not user_input or not user_input.strip():
        # Fall back to the current conversation query so the SubAgent always
        # receives meaningful context even when the LLM omits user_input.
        user_input = cfg.get('query', '').strip()
    if not user_input:
        raise ValueError('user_input must not be empty.')

    if workflow_loader.get_workflow(workflow_id) is None:
        raise ValueError(f'workflow {workflow_id!r} not found.')

    # Step existence and prompt rendering remain local metadata concerns. Never
    # reject a transition from the Python graph: Go evaluates the compiled graph
    # and returns a structured rejection with the authoritative projection.
    step_config = workflow_loader.get_step_config(workflow_id, step_id)
    if not step_config:
        raise ValueError(f'step {step_id!r} is not defined in workflow {workflow_id!r}.')
    # --- Submit transition command ---
    task_id = str(uuid.uuid4())
    LOG.info(
        '[workflow.advance] submitting command workflow=%s step=%s session=%s task=%s cold=%s',
        workflow_id, step_id, session_id, task_id, is_cold_start,
    )

    # Inject focused_tab (UI context hint) into the objective.
    # focused_sort_order is NOT injected — it is the UI scroll position,
    # not the user's intended operation target. The SubAgent reads the
    # runtime_instruction directly and decides which sort_order to pass
    # to save_artifacts based on the user's stated intent.
    focused_tab = cfg.get('focused_tab')
    enriched_instruction = runtime_instruction or ''
    if focused_tab:
        sep = ' ' if enriched_instruction else ''
        enriched_instruction = enriched_instruction + sep + f'User is currently viewing tab: {focused_tab}.'

    objective = _render_step_objective(step_config, user_input, enriched_instruction)
    # Preserve the local runtime event consumed by existing agent hosts while Go
    # remains authoritative for transition admission.
    _write_agent_data(
        'workflow_step_started',
        workflow_id=workflow_id,
        step_id=step_id,
        task_id=task_id,
        objective=objective,
        tools=_merge_tools(list(step_config.get('tools', []) or [])),
        params={
            'workflow_id': workflow_id,
            'step_id': step_id,
            'user_input': user_input,
            'is_cold_start': is_cold_start,
            'hand_off': hand_off,
            'preflight_id': preflight_id,
        },
    )
    submission = _submit_transition_to_core(
        workflow_id=workflow_id,
        step_id=step_id,
        session_id=session_id,
        task_id=task_id,
        objective=objective,
        user_input=user_input,
        hand_off=hand_off,
        runtime_instruction=runtime_instruction,
        partial_indices=partial_indices or {},
        operation=operation,
        is_start=is_cold_start,
        preflight_id=preflight_id,
    )
    cfg['_last_workflow_transition_accepted'] = submission.accepted
    if submission.accepted:
        cfg['_last_workflow_task_id'] = submission.task_id
    LOG.info(
        '[workflow.transition] core result workflow=%s step=%s session=%s command=%s accepted=%s',
        workflow_id, step_id, submission.session_id or session_id,
        submission.command_id, submission.accepted,
    )
    return submission.message


def _trigger_workflow_steps(
        workflow_id: str,
        steps: List[Dict[str, Any]],
        *,
        hand_off: bool = False) -> _TransitionSubmission:
    """Atomically submit multiple currently-Ready steps to Go.

    Go validates every target against one projection and either persists every
    attempt or rejects the whole command. Previously attempted targets deliberately
    remain on the single-step path.
    """
    if not isinstance(steps, list) or len(steps) < 2:
        raise ValueError('steps must contain at least two step commands; use advance_step for one target.')
    if workflow_loader.get_workflow(workflow_id) is None:
        raise ValueError(f'workflow {workflow_id!r} not found.')

    cfg = _agentic_config()
    session_id = str(cfg.get('workflow_session_id') or '')
    if not session_id:
        raise ValueError('batch advancement requires an active workflow session.')
    focused_tab = cfg.get('focused_tab')
    targets: List[Dict[str, Any]] = []
    seen: set[str] = set()
    for raw in steps:
        if not isinstance(raw, dict):
            raise ValueError('every batch item must be an object.')
        step_id = str(raw.get('step_id') or '').strip()
        if not step_id or step_id == '__end__':
            raise ValueError('every batch item requires a non-__end__ step_id.')
        if step_id in seen:
            raise ValueError(f'duplicate batch step_id: {step_id!r}.')
        seen.add(step_id)
        step_config = workflow_loader.get_step_config(workflow_id, step_id)
        if not step_config:
            raise ValueError(f'step {step_id!r} is not defined in workflow {workflow_id!r}.')
        user_input = str(raw.get('user_input') or cfg.get('query') or '').strip()
        if not user_input:
            raise ValueError(f'user_input must not be empty for step {step_id!r}.')
        runtime_instruction = str(raw.get('runtime_instruction') or '')
        enriched_instruction = runtime_instruction
        if focused_tab:
            enriched_instruction += (' ' if enriched_instruction else '') + (
                f'User is currently viewing tab: {focused_tab}.'
            )
        partial_indices = raw.get('partial_indices') or {}
        if not isinstance(partial_indices, dict):
            raise ValueError(f'partial_indices for step {step_id!r} must be an object.')
        targets.append({
            'target_step_id': step_id,
            'task_id': str(uuid.uuid4()),
            'objective': _render_step_objective(step_config, user_input, enriched_instruction),
            'user_input': user_input,
            'runtime_instruction': runtime_instruction,
            'partial_indices': partial_indices,
        })

    submission = _submit_transition_to_core(
        workflow_id=workflow_id,
        step_id=', '.join(target['target_step_id'] for target in targets),
        session_id=session_id,
        task_id=targets[0]['task_id'],
        objective='',
        user_input='',
        hand_off=hand_off,
        runtime_instruction='',
        partial_indices={},
        operation='execute_batch',
        targets=targets,
    )
    cfg['_last_workflow_transition_accepted'] = submission.accepted
    if submission.accepted:
        cfg['_last_workflow_task_id'] = submission.task_id
        cfg['_last_workflow_tasks'] = submission.tasks or []
    return submission


def _build_step_choices_doc(
    forward_steps: List[str],
    rewind_steps: List[str],
    step_labels: Dict[str, str],
    workflow_id: str = '',
    current_step: str = '',
    include_default_approval: bool = True,
) -> str:
    """Return a formatted string listing available step choices for the LLM.

    Forward and previously attempted candidates come exclusively from Go's projection.
    """
    lines = [
        '## Available steps at this moment (authoritative — state machine computed)',
        '--------------------------------------------------------------------------',
        'These are the ONLY valid values for step_id right now.',
        'Do NOT infer step names from scenario descriptions or chat history.',
    ]
    if forward_steps:
        lines.append('Forward Ready steps reported by Go:')
        for s in forward_steps:
            label = step_labels.get(s, '')
            label_suffix = f'  ({label})' if label else ''
            approval_note = ''
            if include_default_approval:
                approval = (
                    'required'
                    if workflow_loader.get_step_mode(workflow_id, s) == 'human'
                    else 'not required'
                )
                approval_note = f'  [default approval: {approval}]'
            lines.append(f'  - {s}{label_suffix}{approval_note}')
    rerun_steps: List[str] = []
    if current_step and current_step not in {'__start__', '__end__'}:
        rerun_steps.append(current_step)
    rerun_steps.extend(step for step in rewind_steps if step not in rerun_steps)
    if rerun_steps:
        lines.append('Previously attempted steps that may be run again (including previously completed steps):')
        for s in rerun_steps:
            label = step_labels.get(s, '')
            suffix = f'  ({label})' if label else ''
            lines.append(f'  - {s}{suffix}  <- select this ID to run it again')
    lines.append('')
    lines.append('Pass one of the above IDs as step_id. Any other value will be rejected.')
    return '\n'.join(lines)


def _build_step_name_index(workflow_id: str) -> str:
    """Return a compact id-to-name index without graph or step details."""
    spec = workflow_loader.get_workflow(workflow_id)
    if not spec:
        return ''

    labels: Dict[str, str] = {}
    ordered_ids: List[str] = []
    for config in spec.yaml.get('steps', []) or []:
        if not isinstance(config, dict):
            continue
        step_id = str(config.get('id') or '').strip()
        if not step_id:
            continue
        ordered_ids.append(step_id)
        label = str(config.get('label') or config.get('name') or '').strip()
        if label:
            labels[step_id] = label
    for step_id, config in spec._steps.items():
        if step_id not in ordered_ids:
            ordered_ids.append(step_id)
        label = str(config.get('label') or config.get('name') or '').strip()
        if label:
            labels[step_id] = label

    entries = [
        f'{step_id}({labels[step_id]})' if labels.get(step_id) else step_id
        for step_id in ordered_ids
        if step_id not in {'__start__', '__end__'}
    ]
    if not entries:
        return ''
    return (
        '## Workflow Step Name Index [AUTHORITATIVE]\n'
        'Use this compact id/name list only to match a user-named target boundary. '
        'It does not imply reachability or execution order.\n'
        + ', '.join(entries)
    )


def _extract_json_object(raw: str) -> Dict[str, Any]:
    """Extract the first JSON object from an LLM response."""
    text = str(raw or '').strip()
    text = re.sub(r'^```(?:json)?\s*|\s*```$', '', text, flags=re.IGNORECASE)
    start = text.find('{')
    if start < 0:
        raise ValueError('preflight model returned no JSON object')
    value, _ = json.JSONDecoder().raw_decode(text, start)
    if not isinstance(value, dict):
        raise ValueError('preflight model result must be a JSON object')
    return value


def _evaluate_workflow_preflight(
    *,
    workflow_id: str,
    workflow_name: str,
    description: str,
    when_to_use: str,
    scenario: str,
    request_context: str,
    previous: Optional[Dict[str, Any]],
    first_steps: List[str],
    workflow_mode: str,
    explicit_workflow_request: bool = False,
) -> Dict[str, Any]:
    """Run the side-effect-free LLM suitability check for a cold workflow start."""
    if not is_model_role_available('llm'):
        raise RuntimeError('the llm model role is not available for workflow preflight')
    previous_json = json.dumps(previous or {}, ensure_ascii=False)
    prompt = f'''You are a workflow launch preflight evaluator. Return exactly one JSON object and no prose.

Workflow id: {workflow_id}
Workflow name: {workflow_name}
Description: {description}
When to use: {when_to_use}
Valid first steps: {json.dumps(first_steps, ensure_ascii=False)}

Full scenario:
---
{scenario}
---

Persisted preflight from earlier clarification turns:
{previous_json}

Current consolidated request context:
{request_context}

Explicit workflow request: {json.dumps(bool(explicit_workflow_request))}

If Explicit workflow request is true, the user has authoritatively selected this workflow.
You MUST NOT return not_applicable. Return ready when safe defaults are available, or
need_information only when information is genuinely required before the first step can run.

Classify the request as exactly one of:
- ready: applicable and all truly required information is available or has an explicit safe default.
- need_information: applicable but required information is missing.
- not_applicable: this workflow should not be launched for the request.

For ready, choose one valid first_step_id. Do not decide how execution continues after launch;
the caller applies the current execution policy.

Required schema:
{{
  "decision": "ready|need_information|not_applicable",
  "reason": "short explanation",
  "missing_information": [{{"key":"...","question":"..."}}],
  "normalized_request": "complete request preserving the original intent and all collected answers",
  "first_step_id": "one valid first step or empty"
}}'''
    llm = lazyllm.AutoModel(model='llm')

    def _call_with_one_repair() -> Dict[str, Any]:
        raw = llm(
            prompt,
            response_format={'type': 'json_object'},
            stream_output=False,
            timeout=_PREFLIGHT_TIMEOUT_SECONDS,
        )
        try:
            return _normalise_preflight_result(
                _extract_json_object(str(raw or '')),
                first_steps=first_steps,
                fallback_request=request_context,
                require_hand_off=False,
            )
        except Exception as first_error:
            repair_prompt = (
                prompt
                + '\n\nYour previous response was invalid JSON: '
                + str(first_error)
                + '\nReturn the required JSON object now. Do not add prose. Previous response:\n'
                + str(raw or '')[:4000]
            )
            repaired = llm(
                repair_prompt,
                response_format={'type': 'json_object'},
                stream_output=False,
                timeout=_PREFLIGHT_TIMEOUT_SECONDS,
            )
            return _normalise_preflight_result(
                _extract_json_object(str(repaired or '')),
                first_steps=first_steps,
                fallback_request=request_context,
                require_hand_off=False,
            )

    executor = lazyllm.ThreadPoolExecutor(max_workers=1)
    future = executor.submit(_call_with_one_repair)
    try:
        raw = future.result(timeout=_PREFLIGHT_TIMEOUT_SECONDS)
    finally:
        executor.shutdown(wait=False, cancel_futures=True)
    return raw


def _normalise_preflight_result(
    result: Dict[str, Any],
    *,
    first_steps: List[str],
    fallback_request: str,
    require_hand_off: bool = True,
) -> Dict[str, Any]:
    decision = str(result.get('decision') or '').strip().lower()
    if decision not in _PREFLIGHT_DECISIONS:
        raise ValueError(f'invalid preflight decision: {decision!r}')
    missing = result.get('missing_information') or []
    if not isinstance(missing, list):
        raise ValueError('missing_information must be a list')
    normalised = str(result.get('normalized_request') or fallback_request).strip()
    if not normalised:
        raise ValueError('normalized_request must not be empty')
    first_step = str(result.get('first_step_id') or '').strip()
    hand_off = result.get('hand_off')
    if decision == 'ready':
        if not first_step and len(first_steps) == 1:
            first_step = first_steps[0]
        if first_step not in first_steps:
            raise ValueError(f'preflight selected invalid first step {first_step!r}')
        if require_hand_off and not isinstance(hand_off, bool):
            raise ValueError('ready preflight must select a boolean hand_off value')
    if not require_hand_off:
        hand_off = True
    return {
        'decision': decision,
        'reason': str(result.get('reason') or '').strip(),
        'missing_information': missing,
        'normalized_request': normalised,
        'first_step_id': first_step,
        'hand_off': hand_off if isinstance(hand_off, bool) else True,
    }


def _emit_preflight_snapshot(snapshot: Optional[Dict[str, Any]]) -> None:
    _write_agent_data(
        'workflow_preflight_updated',
        clear=snapshot is None,
        snapshot=snapshot or {},
    )


def build_cold_start_tools(
    workflow_catalog: Optional[List[Dict[str, Any]]] = None,
    disabled_builtin_workflows: Optional[List[str]] = None,
    allowed_workflow_refs: Optional[List[str]] = None,
) -> List[Any]:
    """Build one side-effect-free preflight trigger per loaded workflow."""
    tools = []
    disabled = set(disabled_builtin_workflows or [])
    allowed = set(allowed_workflow_refs or [])
    candidates = [
        (spec, None)
        for spec in (workflow_loader._registry or {}).values()
        if (
            not spec.workflow_id.startswith('user_')
            and spec.workflow_id not in disabled
            and (not allowed or f'builtin:{spec.workflow_id}' in allowed)
        )
    ]
    candidates.extend(
        (None, entry) for entry in (workflow_catalog or [])
        if not allowed or str(entry.get('workflow_ref') or '') in allowed
    )
    for spec, catalog_entry in candidates:
        if catalog_entry is not None:
            pid = str(catalog_entry.get('workflow_id') or 'workflow')
            name = str(catalog_entry.get('name') or pid)
            desc = str(catalog_entry.get('description') or f'Trigger the {name} workflow.')
            when_to_use = str(catalog_entry.get('when_to_use') or '').strip()
            first_steps: List[str] = []
            workflow_ref = str(catalog_entry.get('workflow_ref', pid)).encode()
            ref_digest = hashlib.sha256(workflow_ref).hexdigest()[:8]
            public_tool_name = f'trigger_{pid.replace("-", "_")}_{ref_digest}'
        else:
            assert spec is not None
            pid = spec.workflow_id
            name = spec.yaml.get('name', pid)
            desc = spec.yaml.get('description', f'Trigger the {name} workflow.')
            when_to_use = spec.yaml.get('when_to_use', '').strip()
            # Entry candidates are resolved by Go when the trigger runs. Keeping
            # them out of the static tool definition prevents stale local graph
            # semantics from being presented as runtime Ready state.
            first_steps = []
            public_tool_name = f'trigger_{pid.replace("-", "_")}'

        def _make_trigger(
            workflow_id: str,
            workflow_name: str,
            first: List[str],
            workflow_desc: str,
            workflow_when_to_use: str,
            entry=None,
            tool_name='',
        ):

            def _trigger(request_context: str, explicit_workflow_request: bool) -> str:
                request_context = str(request_context or '').strip()
                explicit_workflow_request = bool(explicit_workflow_request)
                if not request_context:
                    return json.dumps({
                        'status': 'preflight_failed',
                        'outcome': 'preflight_failed',
                        'reason': 'request_context must not be empty',
                        'error': 'request_context must not be empty',
                    }, ensure_ascii=False)
                resolved_workflow_id = workflow_id
                resolved_first = first
                runtime_meta: Dict[str, Any] = {}
                if entry is not None:
                    resolved_workflow_id, _runtime_spec = workflow_loader.resolve_remote_workflow(entry)
                    runtime_meta = {
                        key: entry.get(key)
                        for key in ('workflow_ref', 'revision_id', 'revision_no', 'tree_hash', 'remote_root')
                    }
                resolved_spec = workflow_loader.get_workflow(resolved_workflow_id)
                if resolved_spec is None:
                    return json.dumps({
                        'status': 'preflight_failed',
                        'outcome': 'preflight_failed',
                        'reason': f'workflow {resolved_workflow_id!r} is not loaded',
                        'error': f'workflow {resolved_workflow_id!r} is not loaded',
                    }, ensure_ascii=False)
                cfg = _agentic_config()
                cfg.update(runtime_meta)
                try:
                    resolved_first = _fetch_go_start_candidates(resolved_workflow_id)
                except Exception as exc:
                    return json.dumps({
                        'status': 'preflight_failed',
                        'outcome': 'preflight_failed',
                        'reason': f'Go could not plan the workflow start: {exc}',
                        'error': str(exc),
                    }, ensure_ascii=False)
                if not resolved_first:
                    return json.dumps({
                        'status': 'preflight_failed',
                        'outcome': 'preflight_failed',
                        'reason': 'Go reports no Ready entry step for the current materials',
                        'error': 'no Ready entry step',
                    }, ensure_ascii=False)
                cfg.pop('prepared_workflow', None)
                if cfg.get('workflow_session_id'):
                    return json.dumps({
                        'status': 'preflight_failed',
                        'outcome': 'preflight_failed',
                        'reason': 'an active workflow session already exists',
                        'error': 'an active workflow session already exists',
                    }, ensure_ascii=False)
                previous = cfg.get('workflow_preflight_context')
                if not isinstance(previous, dict) or previous.get('workflow_id') != resolved_workflow_id:
                    previous = None
                # Once the user explicitly selects a workflow, retain that choice
                # across clarification turns whose text may no longer repeat its name.
                explicit_workflow_request = bool(
                    explicit_workflow_request
                    or (previous or {}).get('explicit_workflow_request')
                )
                workflow_mode = str(cfg.get('workflow_mode') or 'dynamic')
                try:
                    start_hints = cfg.pop('_workflow_start_route_hints', {})
                    preflight_scenario = resolved_spec.scenario_md
                    if start_hints:
                        hint_lines = ['Start route candidates (natural-language ChatAgent decision):']
                        for step_id in resolved_first:
                            hints = start_hints.get(step_id) or []
                            hint_lines.append(f'- {step_id}: {" OR ".join(hints) if hints else "always applicable"}')
                        preflight_scenario = preflight_scenario + '\n\n' + '\n'.join(hint_lines)
                    raw_result = _evaluate_workflow_preflight(
                        workflow_id=resolved_workflow_id,
                        workflow_name=workflow_name,
                        description=workflow_desc,
                        when_to_use=workflow_when_to_use,
                        scenario=preflight_scenario,
                        request_context=request_context,
                        previous=previous,
                        first_steps=resolved_first,
                        workflow_mode=workflow_mode,
                        explicit_workflow_request=explicit_workflow_request,
                    )
                    result = _normalise_preflight_result(
                        raw_result,
                        first_steps=resolved_first,
                        fallback_request=request_context,
                        require_hand_off=False,
                    )
                    # Explicit user selection outranks the model's suitability
                    # heuristic.  A preflight may still request genuinely required
                    # information, but it may not veto the selected workflow.  Treat a
                    # contradictory not_applicable result as ready so the launch
                    # invariant below can deterministically start the first step.
                    if explicit_workflow_request and result['decision'] == 'not_applicable':
                        LOG.warning(
                            '[workflow.preflight] overriding not_applicable for explicit request workflow=%s',
                            resolved_workflow_id,
                        )
                        result.update({
                            'decision': 'ready',
                            'reason': 'The user explicitly requested this workflow.',
                            'missing_information': [],
                            'first_step_id': resolved_first[0],
                        })
                except Exception as exc:
                    LOG.warning('[workflow.preflight] failed workflow=%s error=%s', resolved_workflow_id, exc)
                    failure_snapshot = {
                        **(previous or {}),
                        'preflight_id': str((previous or {}).get('preflight_id') or uuid.uuid4()),
                        'workflow_id': resolved_workflow_id,
                        'workflow_name': workflow_name,
                        'status': 'failed',
                        'original_intent': str(
                            (previous or {}).get('original_intent') or request_context
                        ).strip(),
                        'normalized_request': str(
                            (previous or {}).get('normalized_request') or request_context
                        ).strip(),
                        'missing_information': (previous or {}).get('missing_information') or [],
                        'explicit_workflow_request': explicit_workflow_request,
                        **runtime_meta,
                    }
                    cfg['workflow_preflight_context'] = failure_snapshot
                    _emit_preflight_snapshot(failure_snapshot)
                    return json.dumps({
                        'status': 'preflight_failed',
                        'outcome': 'preflight_failed',
                        'reason': str(exc),
                        'error': str(exc),
                    }, ensure_ascii=False)

                original_intent = str((previous or {}).get('original_intent') or request_context).strip()
                confirmation_answers = list((previous or {}).get('confirmation_answers') or [])
                if previous and request_context not in confirmation_answers:
                    confirmation_answers.append(request_context)
                if result['decision'] == 'not_applicable':
                    cfg.pop('prepared_workflow', None)
                    cfg.pop('workflow_preflight_context', None)
                    _emit_preflight_snapshot(None)
                    return json.dumps({
                        'status': 'not_applicable',
                        'outcome': 'not_applicable',
                        'reason': result['reason'],
                    }, ensure_ascii=False)

                snapshot: Dict[str, Any] = {
                    'preflight_id': str((previous or {}).get('preflight_id') or uuid.uuid4()),
                    'workflow_id': resolved_workflow_id,
                    'workflow_name': workflow_name,
                    'status': 'collecting' if result['decision'] == 'need_information' else 'ready',
                    'original_intent': original_intent,
                    'confirmation_answers': confirmation_answers,
                    'normalized_request': result['normalized_request'],
                    'missing_information': result['missing_information'],
                    'explicit_workflow_request': explicit_workflow_request,
                    **runtime_meta,
                }
                cfg['workflow_preflight_context'] = snapshot
                _emit_preflight_snapshot(snapshot)
                if result['decision'] == 'need_information':
                    cfg.pop('prepared_workflow', None)
                    return json.dumps({
                        'status': 'need_information',
                        'outcome': 'need_information',
                        'reason': result['reason'],
                        'missing_information': result['missing_information'],
                    }, ensure_ascii=False)

                static_advancement = workflow_mode == 'auto'
                first_step_default_approval = (
                    'required'
                    if workflow_loader.get_step_mode(
                        resolved_workflow_id, result['first_step_id']
                    ) == 'human'
                    else 'not_required'
                )
                inline_auto_step = (
                    workflow_mode == 'dynamic'
                    and first_step_default_approval == 'not_required'
                )
                launch_plan: Dict[str, Any] = {
                    'first_step_id': result['first_step_id'],
                    'normalized_request': result['normalized_request'],
                }
                if static_advancement:
                    launch_plan.update({
                        'hand_off': True,
                        'advance_tool': 'advance_step_and_hand_off',
                    })
                elif inline_auto_step:
                    launch_plan.update({
                        'hand_off': False,
                        'advance_tool': 'advance_step',
                    })
                step_name_index = _build_step_name_index(resolved_workflow_id)
                prepared = {
                    **snapshot,
                    'must_advance': True,
                    'advance_committed': False,
                    'requires_hand_off_choice': not (
                        static_advancement or inline_auto_step
                    ),
                    'fallback_hand_off': not inline_auto_step,
                    'step_name_index': step_name_index,
                    'launch_plan': launch_plan,
                    'scenario': resolved_spec.scenario_md,
                }
                cfg['prepared_workflow'] = prepared
                cfg.update(runtime_meta)
                visible_launch_plan = dict(launch_plan)
                if static_advancement or inline_auto_step:
                    visible_launch_plan.pop('hand_off', None)
                    instruction = (
                        'You MUST now call the advancement tool named by launch_plan in this '
                        'same turn. Do not answer with prose first.'
                    )
                else:
                    instruction = (
                        'Infer whether the user explicitly requested multiple workflow steps. '
                        'If yes, choose `advance_step`; otherwise choose the default '
                        '`advance_step_and_hand_off`. Do not answer with prose first.'
                    )
                return json.dumps({
                    'status': 'ready',
                    'outcome': 'ready',
                    'reason': result['reason'],
                    'must_advance': True,
                    'preflight_id': snapshot['preflight_id'],
                    'launch_plan': visible_launch_plan,
                    'step_name_index': step_name_index,
                    'first_step_default_approval': first_step_default_approval,
                    'instruction': instruction,
                }, ensure_ascii=False)

            # Set __name__ so the framework guard and logging use the public tool name.
            _trigger.__name__ = tool_name
            if workflow_when_to_use:
                tool_desc = f'{workflow_when_to_use.rstrip(".")}.  ({workflow_desc.rstrip(".")})'
            else:
                tool_desc = workflow_desc
            _trigger.__doc__ = (
                f'{tool_desc}\n\n'
                'Args:\n'
                '    request_context (str): The complete user goal. When clarification has\n'
                '        occurred, consolidate the original request and all answers.\n\n'
                '    explicit_workflow_request (bool): Always supply this flag. Set true when the user\n'
                '        explicitly names and asks to use, run, start, launch, open, or enable this\n'
                '        workflow. Explicit selection cannot\n'
                '        be rejected as not_applicable.\n\n'
                'Returns:\n'
                '    A structured preflight result. This tool never starts the workflow.\n'
                '    When status is ready, immediately call an advance tool in the same turn.'
            )
            return _trigger

        tools.append(_make_trigger(pid, name, first_steps, desc, when_to_use, catalog_entry, public_tool_name))
    return tools


def _commit_prepared_workflow(
    step_id: str,
    *,
    hand_off: bool,
    wait_for_result: bool = True,
) -> str:
    """Consume a ready preflight and emit the first cold-start task."""
    cfg = _agentic_config()
    prepared = cfg.get('prepared_workflow')
    if not isinstance(prepared, dict) or not prepared.get('must_advance'):
        raise ValueError('No ready workflow preflight. Call the matching trigger tool first.')
    if prepared.get('advance_committed'):
        raise ValueError('The prepared workflow has already been advanced.')
    plan = prepared.get('launch_plan') or {}
    expected_step = str(plan.get('first_step_id') or '')
    if step_id != expected_step:
        raise ValueError(f'First step must be {expected_step!r}, got {step_id!r}.')
    expected_hand_off = bool(plan.get('hand_off', True))
    if isinstance(plan.get('hand_off'), bool) and hand_off != expected_hand_off:
        expected_tool = 'advance_step_and_hand_off' if expected_hand_off else 'advance_step'
        raise ValueError(f'Launch plan requires {expected_tool}.')
    workflow_id = str(prepared.get('workflow_id') or '')
    normalised_request = str(plan.get('normalized_request') or '').strip()
    preflight_id = str(prepared.get('preflight_id') or '')
    result = _trigger_workflow_step(
        workflow_id,
        step_id,
        normalised_request,
        is_cold_start=True,
        hand_off=hand_off,
        preflight_id=preflight_id,
    )
    if not cfg.get('_last_workflow_transition_accepted', False):
        if hand_off:
            raise RuntimeError(result)
        return result
    prepared['advance_committed'] = True
    cfg['prepared_workflow'] = prepared
    if hand_off or not wait_for_result:
        return result

    task_id = str(cfg.get('_last_workflow_task_id') or '')
    if not task_id:
        raise RuntimeError('Cold-start task id was not recorded.')
    session_id = str(cfg.get('workflow_session_id') or '')
    if not session_id:
        raise RuntimeError('Go accepted cold start without a workflow session id.')
    cfg.update({
        'workflow_id': workflow_id,
        'workflow_session_id': session_id,
        'workflow_step': step_id,
    })
    summary = _wait_for_go_task(step_id, result)
    spec = workflow_loader.get_workflow(workflow_id)
    if spec is None:
        raise RuntimeError(f'Workflow {workflow_id!r} disappeared after launch was prepared.')
    labels = {
        sid: scfg.get('label', '')
        for sid, scfg in (spec._steps or {}).items()
        if scfg.get('label')
    }
    return _append_step_transition_hint(
        summary,
        workflow_id=workflow_id,
        current_step=step_id,
        rewind_steps=[],
        step_labels=labels,
    ) + '\n\n---\nWorkflow scenario:\n' + str(prepared.get('scenario') or '')


def build_cold_advance_tools(workflow_mode: str = 'dynamic') -> List[Any]:
    """Build only the cold-start advance tools allowed by the current policy."""

    def advance_step(step_id: str) -> str:
        """Start the prepared workflow and wait for its first step to finish.

        Use after a ready trigger when current request policy calls for synchronous continuation.

        Args:
            step_id: The launch_plan.first_step_id returned by trigger.

        Returns:
            The first step result and live next-step guidance.
        """
        cfg = _agentic_config()
        if cfg.get('workflow_session_id') and cfg.get('workflow_id'):
            prepared = cfg.get('prepared_workflow') or {}
            plan = prepared.get('launch_plan') or {}
            return build_advance_step_tool(
                str(cfg['workflow_id']), str(cfg.get('workflow_step') or '')
            )(
                steps=[{
                    'step_id': step_id,
                    'user_input': str(plan.get('normalized_request') or cfg.get('query') or ''),
                }],
            )
        return _commit_prepared_workflow(step_id, hand_off=False)

    def advance_step_and_hand_off(step_id: str) -> str:
        """Start the prepared workflow and hand control off immediately.

        Use after a ready trigger when current request policy calls for an asynchronous boundary.

        Args:
            step_id: The launch_plan.first_step_id returned by trigger.

        Returns:
            Confirmation that the first workflow step was queued.
        """
        cfg = _agentic_config()
        if cfg.get('workflow_session_id') and cfg.get('workflow_id'):
            prepared = cfg.get('prepared_workflow') or {}
            plan = prepared.get('launch_plan') or {}
            return build_advance_step_and_hand_off_tool(
                str(cfg['workflow_id']), str(cfg.get('workflow_step') or '')
            )(
                steps=[{
                    'step_id': step_id,
                    'user_input': str(plan.get('normalized_request') or cfg.get('query') or ''),
                }],
            )
        return _commit_prepared_workflow(step_id, hand_off=True)

    if workflow_mode == 'auto':
        return [advance_step_and_hand_off]
    return [advance_step_and_hand_off, advance_step]


def commit_prepared_workflow_fallback() -> str:
    """Deterministically emit the launch plan after the ChatAgent skipped advance twice."""
    prepared = _agentic_config().get('prepared_workflow') or {}
    plan = prepared.get('launch_plan') or {}
    planned_hand_off = plan.get('hand_off')
    hand_off = (
        planned_hand_off
        if isinstance(planned_hand_off, bool)
        else bool(prepared.get('fallback_hand_off', True))
    )
    return _commit_prepared_workflow(
        str(plan.get('first_step_id') or ''),
        hand_off=hand_off,
        wait_for_result=False,
    )


def _should_suppress_prepared_workflow_text(event: Any) -> bool:
    """Return whether prose must be held until a ready launch plan is committed."""
    prepared = _agentic_config().get('prepared_workflow')
    return bool(
        isinstance(event, dict)
        and event.get('tag') == 'text'
        and isinstance(prepared, dict)
        and prepared.get('must_advance')
        and not prepared.get('advance_committed')
    )


async def _enforce_prepared_workflow_advance(
    *,
    all_tools: List[Any],
    query: str,
    runtime_prompt: str,
    agent: Any,
    runtime_config: Any,
    fs: Any,
    stop_tools: List[str],
    history: Optional[List[Any]],
):
    """Yield retry/fallback output when a ready trigger was not followed by advance.

    ChatService owns generic agent streaming. This helper owns the workflow-specific
    invariant: one forced ReAct retry, followed by deterministic launch-plan commit.
    """
    prepared = _agentic_config().get('prepared_workflow')
    if not (
        isinstance(prepared, dict)
        and prepared.get('must_advance')
        and not prepared.get('advance_committed')
    ):
        return

    from lazymind.chat.engine.agent_runtime import (
        AgentExecutor, AgentRole, AgentRunPlan, PromptBuilder,
    )
    from lazymind.chat.service.component.status_retry import _new_react_agent

    launch_plan = dict(prepared.get('launch_plan') or {})
    requires_hand_off_choice = bool(prepared.get('requires_hand_off_choice', True))
    visible_launch_plan = dict(launch_plan)
    if not requires_hand_off_choice:
        visible_launch_plan.pop('hand_off', None)
    LOG.warning(
        '[workflow.advance] mandatory retry plan=%s',
        json.dumps(launch_plan, ensure_ascii=False),
    )
    retry_agent = _new_react_agent(
        all_tools=all_tools,
        query=query,
        runtime_prompt=runtime_prompt,
        agent=agent,
        config=runtime_config,
        fs=fs,
        stop_tools=stop_tools,
    )
    if requires_hand_off_choice:
        correction = (
            '## Mandatory workflow launch correction\n'
            'The workflow trigger already returned ready. Do not answer, explain, confirm, '
            'or ask another question. Immediately start first_step_id. Infer whether the user '
            'explicitly requested multiple workflow steps. Use `advance_step` only if they did; '
            'otherwise use the default `advance_step_and_hand_off`. Launch plan:\n'
            + json.dumps(visible_launch_plan, ensure_ascii=False)
            + '\n'
            + str(prepared.get('step_name_index') or '')
        )
    else:
        correction = (
            '## Mandatory workflow launch correction\n'
            'The workflow trigger already returned ready. Do not answer, explain, '
            'confirm, or ask another question. Immediately execute this launch plan '
            'using the advancement tool named by this plan exactly as specified:\n'
            + json.dumps(visible_launch_plan, ensure_ascii=False)
        )
    retry_prompt = (
        PromptBuilder.for_role(AgentRole.CHAT)
        .runtime(
            'workflow_launch_correction', 'Mandatory Workflow Launch Correction', correction,
            'workflow.runtime',
            authoritative=True,
            content_kind='instruction',
        )
        .input(query, source='user')
        .build()
    )
    retry_plan = AgentRunPlan(
        role=AgentRole.CHAT,
        prompt=retry_prompt,
        history=history or [],
    )
    async for kind, payload in AgentExecutor().stream_agent(retry_agent, retry_plan):
        if kind == 'event' and _should_suppress_prepared_workflow_text(payload):
            continue
        yield kind, payload

    prepared = _agentic_config().get('prepared_workflow')
    if not (
        isinstance(prepared, dict)
        and prepared.get('must_advance')
        and not prepared.get('advance_committed')
    ):
        return

    LOG.error('[workflow.advance] deterministic prepared-plan fallback')
    try:
        final_result = commit_prepared_workflow_fallback()
        # The fallback runs outside StreamCallHelper, so expose its task event
        # through the same generic event path consumed by ChatService.
        for raw_event in lazyllm.FileSystemQueue().dequeue():
            yield 'event', json.loads(raw_event)
        yield 'final', final_result
    except Exception as exc:
        LOG.exception('[workflow.advance] deterministic fallback failed')
        yield 'final', f'PLUGIN_START_FAILED: {exc}'


async def guard_workflow_agent_stream(
    initial_stream: Any,
    *,
    all_tools: List[Any],
    query: str,
    runtime_prompt: str,
    agent: Any,
    runtime_config: Any,
    fs: Any,
    stop_tools: List[str],
    history: Optional[List[Any]],
):
    """Wrap the normal ChatAgent stream with the workflow launch invariant."""
    async for kind, payload in initial_stream:
        if kind == 'event' and _should_suppress_prepared_workflow_text(payload):
            continue
        yield kind, payload

    async for item in _enforce_prepared_workflow_advance(
        all_tools=all_tools,
        query=query,
        runtime_prompt=runtime_prompt,
        agent=agent,
        runtime_config=runtime_config,
        fs=fs,
        stop_tools=stop_tools,
        history=history,
    ):
        yield item


def _live_reachability_snapshot(
    workflow_id: str,
    fallback_current_step: str,
    rewind_steps: Optional[List[str]] = None,
) -> _ReachabilitySnapshot:
    """Read Ready/Past from Go without a local graph fallback."""
    cfg = _agentic_config()
    # The caller's step is the scope being documented/executed. Agent globals
    # may still describe the previous tool invocation in the same process.
    current_step = fallback_current_step or cfg.get('workflow_step', '')
    session_id = cfg.get('workflow_session_id', '')
    forward_steps: List[str] = []
    rewind = list(rewind_steps or [])
    projection: Dict[str, Any] = {}
    if session_id:
        projection = _fetch_go_projection(session_id)
        forward_steps = list(projection.get('ready') or [])
        rewind = list(projection.get('past') or [])
    # A projection without node state is not authoritative (notably during
    # startup and in compatibility fixtures); retain the local graph fallback.
    projection_nodes = projection.get('nodes') if isinstance(projection.get('nodes'), dict) else {}
    if not projection or current_step not in projection_nodes or not forward_steps:
        spec = workflow_loader.get_workflow(workflow_id)
        transitions = spec.state.get('transitions', {}) if spec else {}
        forward_steps = [
            str(edge.get('to')) for edge in transitions.get(current_step, [])
            if isinstance(edge, dict) and edge.get('to') not in {current_step, '__end__'}
        ]
    nodes = projection_nodes
    current_execution = (
        str(nodes.get(current_step, {}).get('execution') or '')
        if isinstance(nodes.get(current_step), dict) else ''
    )
    retry = [current_step] if current_execution in {'failed', 'interrupted'} else []
    reachable = list(dict.fromkeys(forward_steps + retry + rewind))
    return _ReachabilitySnapshot(
        current_step=current_step,
        session_id=session_id,
        forward_steps=forward_steps,
        rewind_steps=rewind,
        retry_steps=retry,
        reachable_steps=reachable,
    )


def build_advance_step_and_hand_off_tool(
    workflow_id: str,
    current_step: str,
    rewind_steps: Optional[List[str]] = None,
    step_labels: Optional[Dict[str, str]] = None,
    include_approval_guidance: bool = True,
) -> Any:
    """Build the asynchronous advancement stop-tool for one or more steps."""
    snapshot = _live_reachability_snapshot(workflow_id, current_step, rewind_steps)
    forward = snapshot.forward_steps
    rewind = snapshot.rewind_steps
    labels = step_labels or {}

    choices_doc = _build_step_choices_doc(
        forward,
        rewind,
        labels,
        workflow_id=workflow_id,
        current_step=current_step if current_step in snapshot.retry_steps else '',
        include_default_approval=include_approval_guidance,
    )

    def advance_step_and_hand_off(steps: List[Dict[str, Any]]) -> str:
        """Start one or more Ready steps and end the current ReAct turn."""
        if not isinstance(steps, list) or not steps:
            raise ValueError('steps must contain at least one step command.')
        if len(steps) > 1:
            submission = _trigger_workflow_steps(workflow_id, steps, hand_off=True)
            if not submission.accepted:
                raise RuntimeError(submission.message)
            return submission.message
        command = steps[0]
        if not isinstance(command, dict):
            raise ValueError('each steps item must be an object.')
        step_id = str(command.get('step_id') or '')
        if step_id == '__end__':
            raise ValueError('Manual __end__ transitions are disabled; Go computes session completion.')
        result = _trigger_workflow_step(
            workflow_id, step_id, str(command.get('user_input') or ''),
            is_cold_start=False,
            hand_off=True,
            runtime_instruction=str(command.get('runtime_instruction') or ''),
            partial_indices=command.get('partial_indices') or {},
            operation='advance',
        )
        if not _agentic_config().get('_last_workflow_transition_accepted', False):
            raise RuntimeError(result)
        _set_local_workflow_step(step_id)
        return result

    selection_guidance = (
        'Use the current request policy and each step\'s default approval to decide when '
        'this asynchronous boundary is required.\n'
        if include_approval_guidance
        else 'Use this tool to start the selected next step.\n'
    )
    advance_step_and_hand_off.__doc__ = (
        'Start one or more Ready workflow steps asynchronously and end the current turn.\n\n'
        + selection_guidance
        + 'Pass one command for one step. Pass multiple commands only for independent Ready\n'
        'steps that should be submitted atomically. Never batch dependent or previously\n'
        'attempted steps. Terminal steps are also hand-off boundaries.\n\n'
        + choices_doc + '\n\n'
        'Args:\n'
        '    steps: One or more objects containing step_id and user_input; each may also\n'
        '        contain runtime_instruction and partial_indices.'
    )
    return advance_step_and_hand_off


def build_advance_step_tool(
    workflow_id: str,
    current_step: str,
    rewind_steps: Optional[List[str]] = None,
    step_labels: Optional[Dict[str, str]] = None,
) -> Any:
    """Build the synchronous advancement tool for one or more steps."""
    snapshot = _live_reachability_snapshot(workflow_id, current_step, rewind_steps)
    forward = snapshot.forward_steps
    rewind = snapshot.rewind_steps
    labels = step_labels or {}

    choices_doc = _build_step_choices_doc(
        forward, rewind, labels, workflow_id=workflow_id,
        current_step=current_step if current_step in snapshot.retry_steps else '',
    )

    def advance_step(steps: List[Dict[str, Any]]) -> str:
        """Start one or more Ready steps and wait for their results."""
        if not isinstance(steps, list) or not steps:
            raise ValueError('steps must contain at least one step command.')
        if len(steps) > 1:
            submission = _trigger_workflow_steps(workflow_id, steps, hand_off=False)
            if not submission.accepted:
                return submission.message
            summaries = []
            cfg = _agentic_config()
            for task in submission.tasks or []:
                step_id, task_id = str(task.get('step_id') or ''), str(task.get('task_id') or '')
                if step_id and task_id:
                    cfg['_last_workflow_task_id'] = task_id
                    summaries.append(f'## {step_id}\n{_wait_for_go_task(step_id, submission.message)}')
            cfg['_last_workflow_tasks'] = submission.tasks or []
            return (
                submission.message if not summaries else '\n\n'.join(summaries)
            ) + _append_step_transition_hint(
                '', workflow_id=workflow_id, current_step='', rewind_steps=rewind_steps or [],
                step_labels=labels,
            )
        command = steps[0]
        if not isinstance(command, dict):
            raise ValueError('each steps item must be an object.')
        step_id = str(command.get('step_id') or '')
        if step_id == '__end__':
            raise ValueError('Manual __end__ transitions are disabled; Go computes session completion.')
        result = _trigger_workflow_step(
            workflow_id, step_id, str(command.get('user_input') or ''),
            is_cold_start=False,
            runtime_instruction=str(command.get('runtime_instruction') or ''),
            partial_indices=command.get('partial_indices') or {},
            operation='advance',
        )
        if not _agentic_config().get('_last_workflow_transition_accepted', False):
            return result
        task_id = str(_agentic_config().get('_last_workflow_task_id') or '')
        # Keep only a conversational focus hint. It is not a runtime state fact;
        # parallel Current/Ready sets always come from Go's projection.
        _set_local_workflow_step(step_id)
        LOG.info(
            '[workflow.advance] local current_step updated workflow=%s step=%s session=%s task=%s',
            workflow_id, step_id, _agentic_config().get('workflow_session_id', ''), task_id,
        )
        LOG.info(
            '[workflow.advance] polling Go task workflow=%s step=%s session=%s task=%s',
            workflow_id, step_id, _agentic_config().get('workflow_session_id', ''), task_id,
        )
        summary = _wait_for_go_task(step_id, result)
        LOG.info(
            '[workflow.advance] advance_step completed workflow=%s step=%s session=%s task=%s summary_len=%d',
            workflow_id, step_id, _agentic_config().get('workflow_session_id', ''), task_id, len(summary or ''),
        )
        return _append_step_transition_hint(
            summary,
            workflow_id=workflow_id,
            current_step=step_id,
            rewind_steps=rewind_steps or [],
            step_labels=labels,
        )

    advance_step.__doc__ = (
        'Start one or more Ready workflow steps synchronously and return their results.\n\n'
        'Use when the selected step has default approval `not_required`, or when the user\n'
        'explicitly requests multiple workflow steps, for example\n'
        '"帮我执行 N 步", "连续执行到 X", "一次性执行完", "run N steps",\n'
        '"continue through X", or "run all steps". A complete-deliverable request alone\n'
        'does not authorize this synchronous tool.\n'
        'In continuous mode with an explicit target boundary, use `advance_step` only\n'
        'for prerequisite steps before that boundary, then execute the boundary step\n'
        'with `advance_step_and_hand_off` and stop. If the user did not set a boundary,\n'
        'run prerequisite remaining steps with this tool, then execute the terminal step\n'
        'with `advance_step_and_hand_off` and stop.\n\n'
        'For every other request, use `advance_step_and_hand_off` instead.\n\n'
        + choices_doc + '\n\n'
        'Pass one command for one step, or multiple independent Ready step commands for one\n'
        'atomic batch. Each command contains step_id and user_input and may contain\n'
        'runtime_instruction and partial_indices.'
    )
    return advance_step


def _append_step_transition_hint(
    summary: str,
    workflow_id: str,
    current_step: str,
    rewind_steps: List[str],
    step_labels: Dict[str, str],
) -> str:
    """Append live transition guidance to advance_step's tool result."""
    snapshot = _live_reachability_snapshot(workflow_id, current_step, rewind_steps)
    # Only expose retry option when Go's projection confirms the step is retryable
    # (i.e. its last execution was failed or interrupted). A just-succeeded step
    # must NOT appear as a Retry candidate — the LLM should advance forward instead.
    retryable_step = current_step if current_step in snapshot.retry_steps else ''
    choices_doc = _build_step_choices_doc(
        snapshot.forward_steps,
        snapshot.rewind_steps,
        step_labels,
        workflow_id=workflow_id,
        current_step=retryable_step,
    )
    return (
        f'{summary}\n\n'
        '---\n'
        'Workflow state after this step:\n'
        f'- Current step: {current_step}\n'
        '- The next advance_step call in this same turn must follow this live state:\n\n'
        f'{choices_doc}\n\n'
        'Continuous-mode boundary reminder:\n'
        '- If the latest user request says to run only up to a specific milestone/step '
        '(for example "执行到 X", "到 X 为止", "until X", "up to X"), match X against '
        'the available step ids, labels, and transition descriptions. Execute that '
        'target boundary step with `advance_step_and_hand_off`, then stop. Do not '
        'advance to downstream steps or submit a completion command after the boundary hand-off.'
    )


def _set_local_workflow_step(step_id: str) -> None:
    """Update the ChatAgent display focus after Go accepts a transition."""
    try:
        lazyllm.globals['agentic_config']['workflow_step'] = step_id
    except Exception as exc:
        LOG.warning('[workflow.advance] failed to update local workflow_step step=%s error=%s', step_id, exc)


def _wait_for_go_task(step_id: str, trigger_result: str, timeout: float = 600.0) -> str:
    """Poll Go's persisted task status after transition acceptance."""
    import time
    try:
        import httpx
        from lazymind.config import config as _cfg
        cfg = _agentic_config()
        session_id = cfg.get('workflow_session_id', '')
        task_id = str(cfg.get('_last_workflow_task_id') or '')
        if not task_id:
            return trigger_result
        core_url = str(_cfg['core_api_url']).rstrip('/')
        deadline = time.monotonic() + timeout
        LOG.info(
            '[workflow.advance] polling Go task step=%s session=%s task=%s timeout=%.0fs',
            step_id, session_id, task_id, timeout,
        )
        while time.monotonic() < deadline:
            response = workflow_http_get(
                f'{core_url}/internal/subagent/tasks/{task_id}', timeout=5.0, transport=httpx,
            )
            if response.status_code == 200:
                data = _core_response_data(response)
                status = str(data.get('status') or '')
                if status in {'succeeded', 'failed', 'interrupted', 'canceled'}:
                    summary = str(data.get('summary') or '')
                    return summary or f"Step '{step_id}' finished with status {status}."
            time.sleep(2.0)
        return f"Step '{step_id}' was accepted and is still running after {timeout:.0f}s. Task id: {task_id}."
    except Exception as exc:
        LOG.warning('[workflow.advance] Go task polling failed step=%s error=%s', step_id, exc)
        return trigger_result


def update_intentwriter(tool: Any, workflow_context: Optional[Dict[str, Any]]) -> Any:
    """Extend a conversation IntentWriter with active-workflow scopes.

    ChatService owns the base tool. Workflow internals stay here: ChatService does
    not inspect step ids, DAG state, or workflow lifecycle.
    """
    if not isinstance(workflow_context, dict):
        return tool
    session_id = str(workflow_context.get('session_id') or '').strip()
    workflow_id = str(workflow_context.get('workflow_id') or '').strip()
    spec = workflow_loader.get_workflow(workflow_id) if session_id and workflow_id else None
    if not spec:
        return tool
    return enable_workflow_intent_scopes(
        tool,
        session_id=session_id,
        workflow_id=workflow_id,
        valid_step_ids=list(spec._steps.keys()),
    )


# ---------------------------------------------------------------------------
# Schedule management tools (ChatAgent only, always available)
# ---------------------------------------------------------------------------
# Read-only query tools (ChatAgent only, active session required)
# ---------------------------------------------------------------------------

def build_query_tools() -> List[Any]:
    """Build read-only workflow state query tools for ChatAgent."""

    def list_workflow_steps(session_id: Optional[str] = None) -> str:
        """List all steps and their current status in the active workflow session.

        Use this when the user asks "where are we in the pipeline" or
        "which steps are done / failed".  Read-only — does not trigger execution.
        """
        cfg = _agentic_config()
        sid = session_id or cfg.get('workflow_session_id', '')
        if not sid:
            return 'No active workflow session.'
        try:
            import httpx
            from lazymind.config import config as _cfg
            core_url = str(_cfg['core_api_url']).rstrip('/')
            resp = workflow_http_get(
                f'{core_url}/workflow-sessions/{sid}', timeout=5.0, transport=httpx,
            )
            if resp.status_code != 200:
                return f'Could not fetch session {sid}.'
            steps = resp.json().get('data', {}).get('session', {}).get('steps', [])
            if not steps:
                return 'No steps recorded yet.'
            # Steps arrive ordered by created_at ASC (from ListSteps).
            # Split into contiguous "runs": a new run starts whenever step_id changes.
            # Within each run, if the last record is 'succeeded', collapse earlier
            # non-succeeded records and show only that final success.
            # Otherwise show every record so ChatAgent sees the full failure history.
            # Example: [1,2,3, 2(fail),2(int),2(succ), 3,4] → [1,2,3, 2,3,4]
            runs: list = []   # list of lists, each inner list is one contiguous run
            for s in steps:
                if runs and runs[-1][-1].get('step_id') == s.get('step_id'):
                    runs[-1].append(s)
                else:
                    runs.append([s])
            lines = ['## Workflow session steps']
            for run in runs:
                latest = run[-1]
                if latest.get('status') == 'succeeded':
                    lines.append(
                        f'- {latest.get("step_id")}: succeeded'
                        f' (attempt {latest.get("attempt", 1)})'
                    )
                else:
                    for s in run:
                        lines.append(
                            f'- {s.get("step_id")}: {s.get("status")}'
                            f' (attempt {s.get("attempt", 1)})'
                        )
            return '\n'.join(lines)
        except Exception as exc:
            return f'Error querying steps: {exc}'

    def get_step_result(step_id: str) -> str:
        """Return the artifact summary for a specific step.

        Use when the user asks "what did step X produce" or "show me the result of Y".
        Read-only.
        """
        cfg = _agentic_config()
        session_id = cfg.get('workflow_session_id', '')
        if not session_id:
            return 'No active workflow session.'
        try:
            from lazymind.chat.engine.subagent.db import TaskQueryDB
            artifacts = TaskQueryDB().get_step_artifacts(session_id, step_id)
            if not artifacts:
                return f'No artifacts found for step {step_id!r}.'
            lines = [f'## Artifacts for step {step_id!r}']
            for key, val in artifacts.items():
                lines.append(f'- {key}: {val}')
            return '\n'.join(lines)
        except Exception as exc:
            return f'Error fetching step result: {exc}'

    def get_failed_steps() -> str:
        """Return all failed steps with their error messages.

        Use when the user asks "which steps failed" or "what went wrong".
        Read-only.
        """
        cfg = _agentic_config()
        session_id = cfg.get('workflow_session_id', '')
        if not session_id:
            return 'No active workflow session.'
        try:
            import httpx
            from lazymind.config import config as _cfg
            core_url = str(_cfg['core_api_url']).rstrip('/')
            resp = workflow_http_get(
                f'{core_url}/workflow-sessions/{session_id}', timeout=5.0, transport=httpx,
            )
            if resp.status_code != 200:
                return 'Could not fetch session.'
            steps = resp.json().get('data', {}).get('session', {}).get('steps', [])
            failed = [s for s in steps if s.get('status') == 'failed']
            if not failed:
                return 'No failed steps in this session.'
            lines = ['## Failed steps']
            for s in failed:
                err = s.get('message', 'unknown error')
                lines.append(f'- {s.get("step_id")} (attempt {s.get("attempt", 1)}): {err}')
            return '\n'.join(lines)
        except Exception as exc:
            return f'Error fetching failed steps: {exc}'

    return [list_workflow_steps, get_step_result, get_failed_steps]


# ---------------------------------------------------------------------------
# High-level helper consumed by chat_service
# ---------------------------------------------------------------------------


def _build_session_artifact_section(session_id: str) -> str:
    """Build the artifact-context block prepended to the current user-turn.

    Returned text is injected before the user's query (not into the system prompt)
    so the LLM sees up-to-date session state without the snapshot polluting history.
    """
    if not session_id:
        return ''
    from lazymind.chat.engine.subagent.db import TaskQueryDB
    lines = TaskQueryDB().format_workflow_session_artifacts(session_id)
    if not lines:
        return ''
    # Replace the generic header with a workflow-specific one that warns against re-running steps.
    lines[0] = (
        '## Current session artifacts [AUTHORITATIVE — queried at request time]\n'
        '> Any artifact list mentioned in the conversation history is OUTDATED and must be ignored.\n'
        '> The list below is the ONLY source of truth for what is currently available.'
    )
    return '\n'.join(lines)


def _build_chat_agent_task_context(conversation_id: str) -> str:
    """Build the ## Tasks block injected before the current user-turn query.

    Returned text is prepended to the current user-turn (not the system prompt)
    so the LLM always sees a live snapshot and treats earlier history as outdated.
    """
    conv_id = conversation_id.strip()
    if not conv_id:
        return ''
    from lazymind.chat.engine.subagent.db import TaskQueryDB
    return TaskQueryDB().build_chat_agent_task_context(conv_id)


def _build_preflight_context_section(preflight: Any) -> str:
    """Render the durable clarification snapshot as authoritative turn context."""
    if not isinstance(preflight, dict) or not preflight:
        return ''
    visible = {
        key: preflight.get(key)
        for key in (
            'preflight_id', 'workflow_id', 'workflow_name', 'status', 'original_intent',
            'confirmation_answers', 'normalized_request', 'missing_information',
        )
        if preflight.get(key) not in (None, '', [], {})
    }
    if not visible:
        return ''
    return (
        '## Workflow Preflight Context [AUTHORITATIVE]\n'
        'This durable snapshot survives history compaction. Preserve original_intent, '
        'merge new answers into normalized_request, and pass the consolidated result to '
        'trigger_<workflow>(request_context).\n'
        + json.dumps(visible, ensure_ascii=False, indent=2)
    )


def _build_cold_execution_policy(workflow_mode: str) -> str:
    """Return shared policy plus the Host-only preflight/launch invariant."""
    return (
        _build_mode_guidance(workflow_mode)
        + '\n\n## LazyMind Workflow Launch Binding\n'
        'A ready preflight is not a Session. Start exactly `first_step_id` using the '
        'canonical advancement tool selected by the shared policy and available Host '
        'profile. The launch guard may force this call, but cannot choose a Runtime '
        'operation or alter projection/lineage.'
    )


def resolve_workflow_injection(
    workflow_context: Optional[Dict[str, Any]],
    conversation_id: str = '',
    workflow_catalog: Optional[List[Dict[str, Any]]] = None,
    disabled_builtin_workflows: Optional[List[str]] = None,
    allowed_workflow_refs: Optional[List[str]] = None,
) -> WorkflowAgentContribution:
    """Resolve workflow tools, system prompt, stop-tools and agentic_config patches.

    Called once per request from handle_chat.  Encapsulates all workflow-context
    branching so chat_service stays free of workflow-internal details.

    Note: schedule tools and SubAgent task context are intentionally NOT injected
    here — they are handled independently in chat_service.py so that schedule
    availability and task context visibility are not affected by enable_workflow.

    Returns a structured contribution containing tools, stable workflow policy,
    stop tools, runtime config patches, and request-local runtime context.
    """
    workflow_tools: List[Any] = []
    workflow_system_prompt: str = ''
    workflow_stop_tools: List[str] = []
    agentic_config_patch: Dict[str, Any] = {}
    workflow_artifact_context: str = ''

    # Honour enable_workflow=false: skip all workflow tooling and fall back to pure QA.
    cfg = _agentic_config()
    if not cfg.get('enable_workflow', True):
        return WorkflowAgentContribution(
            workflow_tools, workflow_system_prompt, workflow_stop_tools,
            agentic_config_patch, workflow_artifact_context,
        )

    if not workflow_loader._registry:
        # No workflows registered — return empty; task context is injected by chat_service.
        return WorkflowAgentContribution(
            workflow_tools, workflow_system_prompt, workflow_stop_tools,
            agentic_config_patch, workflow_artifact_context,
        )

    # Resolve workflow_mode from workflow_context (injected by Go).
    workflow_mode = 'dynamic'
    if workflow_context and isinstance(workflow_context, dict):
        pm = workflow_context.get('workflow_mode', '')
        if pm in ('auto', 'dynamic'):
            workflow_mode = pm
    agentic_config_patch['workflow_mode'] = workflow_mode
    if workflow_context and isinstance(workflow_context, dict):
        preflight_context = workflow_context.get('workflow_preflight')
        if isinstance(preflight_context, dict):
            agentic_config_patch['workflow_preflight_context'] = preflight_context

    if workflow_context and isinstance(workflow_context, dict):
        p_session_id = workflow_context.get('session_id', '')
        p_workflow_id = workflow_context.get('workflow_id', '')
        p_current_step = workflow_context.get('current_step', '')

        if p_session_id and p_workflow_id:
            if workflow_context.get('workflow_ref') and not workflow_loader.get_workflow(p_workflow_id):
                _, restored_spec = workflow_loader.resolve_remote_workflow({
                    **workflow_context,
                    'workflow_id': p_workflow_id,
                })
                workflow_loader._registry[p_workflow_id] = restored_spec
            agentic_config_patch.update({
                'workflow_id': p_workflow_id,
                'workflow_session_id': p_session_id,
                'workflow_step': p_current_step,
                'workflow_mode': workflow_mode,
                'workflow_ref': workflow_context.get('workflow_ref'),
                'revision_id': workflow_context.get('revision_id'),
                'revision_no': workflow_context.get('revision_no'),
                'tree_hash': workflow_context.get('tree_hash'),
                'remote_root': workflow_context.get('remote_root'),
                'focused_tab': workflow_context.get('focused_tab'),
                'focused_sort_order': workflow_context.get('focused_sort_order'),
            })
            projection = _fetch_go_projection(p_session_id)
            projected_current = list(projection.get('current') or [])
            if p_current_step not in projected_current:
                p_current_step = projected_current[0] if projected_current else ''
                agentic_config_patch['workflow_step'] = p_current_step
            rewind_steps = list(projection.get('past') or [])

            step_labels: Dict[str, str] = {}
            spec = workflow_loader.get_workflow(p_workflow_id)
            if spec:
                for sid, scfg in spec._steps.items():
                    lbl = scfg.get('label', '')
                    if lbl:
                        step_labels[sid] = lbl

            # Compare the default shared authority with the bounded legacy rollback
            # decision. The trace is observational and never invokes a tool.
            try:
                from lazymind.chat.workflow_policy_shadow import observe
                shadow_projection = dict(projection)
                shadow_projection['ready_steps'] = list(projection.get('ready') or ())
                shadow_projection['attempted_steps'] = list(projection.get('past') or ())
                shadow_projection['failed_steps'] = [
                    str(item.get('step_id') or item.get('id') or '')
                    for item in (projection.get('failed') or ())
                    if isinstance(item, dict)
                ]
                shadow_projection['approval_by_step'] = {
                    step_id: (
                        'required'
                        if workflow_loader.get_step_mode(p_workflow_id, step_id) == 'human'
                        else 'not_required'
                    )
                    for step_id in shadow_projection['ready_steps']
                }
                observe(shadow_projection, {
                    'profile': 'lazymind',
                    # The Host adapter and shared policy use the established
                    # public hand_off spelling.
                    'advance_tools': ['advance_step', 'advance_step_and_hand_off'],
                    'parallel_ready_steps': True,
                    'handoff': True,
                }, cfg, source='active_session_prompt')
            except Exception:
                LOG.exception('workflow policy shadow evaluation failed')

            # Build workflow tools according to workflow_mode.
            # Canonical handoff is always registered (stop-tool).
            # advance_step (sync) is only registered in dynamic mode.
            workflow_tools = [
                build_advance_step_and_hand_off_tool(
                    p_workflow_id, p_current_step,
                    rewind_steps=rewind_steps,
                    step_labels=step_labels,
                    include_approval_guidance=workflow_mode != 'auto',
                ),
            ]
            workflow_stop_tools = ['advance_step_and_hand_off']

            if workflow_mode == 'dynamic':
                workflow_tools.extend([
                    build_advance_step_tool(
                        p_workflow_id, p_current_step,
                        rewind_steps=rewind_steps,
                        step_labels=step_labels,
                    ),
                ])

            # Read-only query tools (active session required).
            workflow_tools.extend(build_query_tools())

            # save_workflow_artifact lets ChatAgent write an artifact directly.
            from lazymind.chat.engine.tools.subagent_chat_tools import save_workflow_artifact
            workflow_tools.append(save_workflow_artifact)

            workflow_system_prompt = workflow_loader.get_scenario(p_workflow_id)
            workflow_artifact_context = _build_session_artifact_section(p_session_id)

            # All step names stay compact and graph-free. Detailed conditions,
            # routing and approval metadata remain limited to live reachable steps.
            step_name_index = _build_step_name_index(p_workflow_id)
            if step_name_index:
                workflow_artifact_context = (
                    workflow_artifact_context + '\n\n' + step_name_index
                ).strip()

            # Inject intent/constraints into the artifact context (user-turn injection).
            intent_section = _build_intent_section(p_session_id, step_id=p_current_step)
            if intent_section:
                workflow_artifact_context = (workflow_artifact_context + '\n\n' + intent_section).strip()

            # Inject authoritative step execution status (user-turn injection).
            step_status_section = _build_step_status_section(
                p_workflow_id, p_session_id, p_current_step,
                rewind_steps, step_labels=step_labels,
            )
            if step_status_section:
                workflow_artifact_context = (workflow_artifact_context + '\n\n' + step_status_section).strip()

            # Inject the current execution policy into this request only. Keeping
            # it in workflow_artifact_context (rather than the system prompt/history)
            # makes configuration changes take effect on the next chat turn.
            mode_guidance = _build_mode_guidance(workflow_mode)
            if mode_guidance:
                workflow_artifact_context = (
                    workflow_artifact_context + '\n\n' + mode_guidance
                ).strip()
        else:
            # Cold start: no active session yet
            triggers = build_cold_start_tools(
                workflow_catalog, disabled_builtin_workflows, allowed_workflow_refs,
            )
            workflow_tools = triggers + build_cold_advance_tools(workflow_mode)
            workflow_stop_tools = ['advance_step_and_hand_off']
            workflow_artifact_context = _build_preflight_context_section(
                agentic_config_patch.get('workflow_preflight_context')
            )
            cold_policy = _build_cold_execution_policy(workflow_mode)
            workflow_artifact_context = (
                workflow_artifact_context + '\n\n' + cold_policy
            ).strip()
            if triggers:
                scenarios = [
                    workflow_loader.get_workflow_intro(spec.workflow_id)
                    for spec in (workflow_loader._registry or {}).values()
                    if (
                        spec.workflow_id not in set(disabled_builtin_workflows or [])
                        and (not allowed_workflow_refs or f'builtin:{spec.workflow_id}' in set(allowed_workflow_refs))
                        and not spec.workflow_id.startswith('user_')
                    )
                ]
                scenarios.extend(
                    _catalog_intro(entry) for entry in (workflow_catalog or [])
                    if not allowed_workflow_refs or str(entry.get('workflow_ref') or '') in set(allowed_workflow_refs)
                )
                workflow_system_prompt = (
                    _COLD_START_PLUGIN_PROMPT
                ) + '\n\n---\n\n'.join(s for s in scenarios if s)
    else:
        # No workflow_context provided: still inject cold-start triggers
        triggers = build_cold_start_tools(
            workflow_catalog, disabled_builtin_workflows, allowed_workflow_refs,
        )
        workflow_tools = triggers + build_cold_advance_tools(workflow_mode)
        workflow_stop_tools = ['advance_step_and_hand_off']
        workflow_artifact_context = _build_cold_execution_policy(workflow_mode)
        if triggers:
            scenarios = [
                workflow_loader.get_workflow_intro(spec.workflow_id)
                for spec in (workflow_loader._registry or {}).values()
                if (
                    spec.workflow_id not in set(disabled_builtin_workflows or [])
                    and (not allowed_workflow_refs or f'builtin:{spec.workflow_id}' in set(allowed_workflow_refs))
                    and not spec.workflow_id.startswith('user_')
                )
            ]
            scenarios.extend(
                _catalog_intro(entry) for entry in (workflow_catalog or [])
                if not allowed_workflow_refs or str(entry.get('workflow_ref') or '') in set(allowed_workflow_refs)
            )
            workflow_system_prompt = (
                _COLD_START_PLUGIN_PROMPT
            ) + '\n\n---\n\n'.join(s for s in scenarios if s)

    return WorkflowAgentContribution(
        workflow_tools, workflow_system_prompt, workflow_stop_tools,
        agentic_config_patch, workflow_artifact_context,
    )


def _catalog_intro(entry: Dict[str, Any]) -> str:
    workflow_id = entry.get('workflow_id') or entry.get('workflow_ref') or 'workflow'
    workflow_name = entry.get('name') or workflow_id
    lines = [f'## Workflow: {workflow_name} (id: {workflow_id})']
    if entry.get('description'):
        lines.append(str(entry['description']))
    if entry.get('when_to_use'):
        lines.append(f'When to use: {entry["when_to_use"]}')
    return '\n'.join(lines)


# ---------------------------------------------------------------------------
# Intent / constraint helpers
# ---------------------------------------------------------------------------

def _build_intent_section(session_id: str, step_id: Optional[str] = None) -> str:
    """Serialize workflow-session intent for ChatAgent prompt injection.

    Step intent is execution detail and is injected only into its SubAgent.
    """
    if not session_id:
        return ''
    try:
        from lazymind.chat.engine.subagent.db import TaskQueryDB
        db = TaskQueryDB()
        session_intent = db.get_session_intent(session_id) if hasattr(db, 'get_session_intent') else None
        if not session_intent:
            return ''

        lines = ['## User Intent & Constraints']
        lines.append('These constraints were recorded from the user and MUST be respected when advancing steps.')
        if session_intent:
            lines.append(f'Global: {session_intent}')
        return '\n'.join(lines)
    except Exception:
        return ''


def _build_step_status_section(
    workflow_id: str,
    session_id: str,
    current_step: str,
    rewind_steps: List[str],
    step_labels: Optional[Dict[str, str]] = None,
) -> str:
    # Build an authoritative snapshot of the pipeline execution state for this turn.
    # Injected into the user-turn prefix (not the system prompt) so it always reflects
    # the live DB state and overrides any stale information in chat history.
    if not session_id or not workflow_id:
        return ''
    try:
        labels = step_labels or {}

        def _label(sid: str) -> str:
            lbl = labels.get(sid, '')
            return f'{sid} ({lbl})' if lbl else sid

        projection = _fetch_go_projection(session_id)
        succeeded = list(projection.get('past') or [])
        ready = list(projection.get('ready') or [])
        route_hints: Dict[str, List[str]] = {}
        for edge in projection.get('edges') or []:
            if not isinstance(edge, dict) or edge.get('state') != 'active':
                continue
            target = str(edge.get('to') or '')
            when = str(edge.get('when') or '').strip()
            if target and when:
                route_hints.setdefault(target, []).append(when)

        lines = ['## Workflow Step Status [AUTHORITATIVE — queried at request time]']
        lines.append('> Any step-status information in the conversation history is OUTDATED. Use only this section.')

        if current_step:
            lines.append(f'\nCurrent workflow step state: **{_label(current_step)}**')
            lines.append(
                'This is the step the session is currently positioned at; it is not automatically '
                'the next action target. If the user clearly wants to proceed and does not modify '
                'the existing intent, choose from "Next forward steps" below.'
            )
        else:
            lines.append('\nCurrent step: pipeline not yet started')

        if succeeded:
            lines.append('Effective succeeded steps: ' + ', '.join(_label(s) for s in succeeded))
        else:
            lines.append('Succeeded steps: none yet')

        if rewind_steps:
            lines.append('Previously completed steps that can be run again: '
                         + ', '.join(_label(s) for s in rewind_steps))

        if ready:
            ready_labels = []
            for step in ready:
                hints = route_hints.get(step) or []
                suffix = f' [when: {" OR ".join(hints)}]' if hints else ''
                ready_labels.append(_label(step) + suffix)
            lines.append('Ready steps reported by Go (valid targets now): '
                         + ', '.join(ready_labels))
            if len(ready) > 1:
                lines.append(
                    'Decision hint: this is a parallel frontier. Evaluate every natural-language '
                    '`when` hint against the current '
                    'user intent. A hinted step is Reachable, not automatically selected. Batch only '
                    'the independent Ready steps that are simultaneously applicable in one plural '
                    'advancement tool call; for N-select-1 '
                    'alternatives, advance only the selected step.'
                )

        return '\n'.join(lines)
    except Exception:
        return ''


def _build_legacy_mode_guidance(workflow_mode: str) -> str:
    """Return the request-local execution policy selected by application code."""
    if workflow_mode == 'auto':
        return (
            '## Current Workflow Execution Policy [AUTHORITATIVE]\n\n'
            'Only asynchronous advancement tools are available. Use '
            '`advance_step_and_hand_off` with one command for one Ready step, or multiple '
            'commands for independent Ready steps that should start atomically. The tool ends '
            'the current turn only after Go accepts the full command.\n'
            'After the step completes, the backend controller evaluates the result and '
            'starts the next decision turn. Do not wait for synchronous step results or ask '
            'the user questions during execution.'
        )

    global_rules = (
        '## Current Workflow Execution Policy [AUTHORITATIVE]\n\n'
        'DEFAULT: call `advance_step_and_hand_off` unless a rule below explicitly selects synchronous execution.\n\n'
        'Reachable-step entries include `[default approval: ...]`; apply that value per step.\n\n'
        '## Step decision rules (READ BEFORE EVERY ACTION)\n\n'
        '### Rule 0 — Intent capture from latest user query (highest priority)\n'
        'At the beginning of each workflow turn, compare the latest user query with the inherited intent.\n'
        'If it contains explicit constraints/emphasis or a named execution boundary (e.g.\n'
        '"必须/不要/只能/执行到 X/做到 X/完成 X 后确认/until X"),\n'
        'you MUST call `intentwrite` with the minimal intent delta FIRST,\n'
        'before any step-advance tool call. Summarize 1-2 key constraints in concise Chinese.\n'
        'If the latest query has no explicit new constraints, do NOT call intentwrite.\n\n'
        'ALSO: if the "User Intent & Constraints" section is empty (no session intent recorded yet)\n'
        'AND the conversation history contains a persistent execution preference such as\n'
        '"一次性", "不要中断", "执行到 X", "完成 X 后确认", "每步确认", "每一步审批",\n'
        '"无需审批", "一次性写完", "run all steps", "approve every step",\n'
        '"do it all at once", or similar phrases,\n'
        'call `intentwrite(scope="workflow_session", operations=[...])`\n'
        'to persist the constraint before advancing any step.\n\n'
        '### Rule 1 — Intent-change detection\n'
        'Before advancing any step, check whether the user is rejecting or changing\n'
        'the outcome of a step that has ALREADY SUCCEEDED. Signals include:\n'
        '  - Direct negation: "我不喜欢…", "换成…", "不要…", "重新…", "I don\'t like…"\n'
        '  - Implicit correction: user describes a different style/subject/content\n'
        '    than what the current artifacts reflect.\n'
        'If intent has changed, identify the EARLIEST step whose output is now\n'
        'invalidated and select that step again using `advance_step_and_hand_off` with\n'
        '`step_id=<affected_step>`. The backend clears affected artifacts and determines\n'
        'the lifecycle operation automatically. Do NOT continue to the next forward step.\n\n'
        '### Rule 2 — DAG frontier and atomic batching\n'
        'The authoritative Ready list is the only forward execution frontier. Never infer\n'
        'serial order from `current_step`, conversation history, or visual position.\n'
        'If exactly one Ready step should start now, use the single-step tool. If two or more\n'
        'independent Ready steps should start now, issue ONE batch call containing all of them,\n'
        'with a separate user_input/runtime_instruction for every step. Do not issue repeated\n'
        'single-step calls for the same frontier. Never include a Blocked node or a downstream\n'
        'node that needs another batch item\'s future output. Running an attempted step again remains single-step.\n\n'
        '### Rule 3 — Step approval\n'
        'If the selected Ready step has `[default approval: not_required]`, call `advance_step`\n'
        'and continue from its result without asking the user for confirmation.\n'
        'Otherwise, call `advance_step_and_hand_off` and stop after starting the selected Ready step(s).\n'
        'For steps whose default approval is required, use `advance_step` only when the latest\n'
        'query or persisted session intent explicitly asks\n'
        'to execute multiple workflow steps, for example "帮我执行 N 步", "连续执行到 X",\n'
        '"一次性执行完", "run N steps", "continue through X", or "run all steps".\n'
        'A request for a complete deliverable or a named workflow is NOT a request for continuous\n'
        'execution by itself; the not_required approval exception above still applies.\n'
        'When several independent Ready steps form the same frontier, pass them in one call to the\n'
        'chosen tool; the number of parallel Ready nodes does not itself imply continuous execution.\n\n'
        'If the user clearly asks to proceed with the existing workflow and\n'
        'does not add new requirements, corrections, or dissatisfaction signals:\n'
        '  - If continuous mode is NOT active: use `advance_step_and_hand_off` and stop.\n'
        '  - If continuous mode IS active (Rule 4) and the user set a target boundary:\n'
        '    use `advance_step` for prerequisite steps before that boundary, then use\n'
        '    `advance_step_and_hand_off` for the boundary step and stop.\n'
        '  - If continuous mode IS active with no target boundary: use `advance_step`\n'
        '    for prerequisite remaining steps, then use `advance_step_and_hand_off`\n'
        '    for the terminal/final-deliverable step and stop.\n'
        'Select targets only from "Ready steps reported by Go" in the status block. Multiple\n'
        'listed targets are valid parallel choices, not an implicit N-select-1 choice. Unless\n'
        'the user explicitly limits the work to a subset, start all Ready steps that advance\n'
        'the requested workflow in one plural-tool call.\n'
        'Do NOT reply only with prose such as "正在生成..." without calling a tool.\n'
        'Do NOT pass the current workflow step state unless it is explicitly listed\n'
        'as a valid forward or previously-attempted target.\n'
    )
    common = (
        '\n\n## Workflow execution guidance\n\n'
        'Tools for step advancement:\n'
        '- `advance_step_and_hand_off`: Start a step asynchronously and end the current turn.\n'
    )
    common += (
        (
            'An asynchronous boundary returns the next decision to the user.\n'
            '- `advance_step`: Queue one step and WAIT for its result.\n'
            'Pass multiple step commands to `advance_step` to atomically queue independent Ready '
            'steps and WAIT for all results. Use this in continuous/uninterrupted mode (see Rule 4 below). '
            'Use `advance_step` for prerequisite steps before a requested boundary, then '
            '`advance_step_and_hand_off` for the boundary step.\n'
            'Unless explicit multi-step intent is present, always use `advance_step_and_hand_off`.\n\n'
            '### Rule 4 — Continuous / uninterrupted execution mode (MUST check before every action)\n'
            'Activate continuous mode when ANY of the following is true:\n'
            '  a) The "User Intent & Constraints" section contains phrases such as:\n'
            '     "一次性完成", "一次性写完", "不要中断", "不要打断", "中间不要停",\n'
            '     "run all steps", "do it all at once", "no interruptions", "without stopping".\n'
            '  b) The current user query contains any of the above phrases.\n'
            'Before executing continuous mode, determine whether the latest user query sets\n'
            'an explicit target boundary with phrases like "执行到 X", "做到 X", "到 X 为止",\n'
            '"生成到 X", "until X", or "up to X". Match X against the full compact\n'
            '"Workflow Step Name Index". Use detailed conditions, routing, and default approval\n'
            'only from the currently reachable steps shown by the step tools/status. The full\n'
            'name index does not imply reachability or execution order.\n'
            'A target boundary has higher priority than generic uninterrupted phrases. For\n'
            'example, "一次性执行到 X，中间不要问我" means run only through the\n'
            'matched boundary step X, then stop after queuing X.\n'
            'In continuous mode:\n'
            '  1. If an explicit target boundary exists, use singular or plural waiting tools\n'
            '     for each Ready frontier before the boundary; batch every multi-step frontier.\n'
            '  2. Execute the target boundary step with `advance_step_and_hand_off`, then stop.\n'
            '     Do NOT wait for the boundary step with `advance_step`.\n'
            '  3. Do NOT call downstream steps and do NOT call `__end__` after a non-`__end__`\n'
            '     boundary hand-off.\n'
            '  4. If there is no explicit target boundary and the user requested the whole\n'
            '     pipeline/final deliverable, run prerequisite Ready frontiers with the\n'
            '     appropriate singular/plural waiting tool,\n'
            '     execute the terminal step with `advance_step_and_hand_off`, then stop.\n'
            '  5. NEVER call `advance_step_and_hand_off` for intermediate steps — '
            '     it hands off control and breaks the continuous run.\n'
            '  6. If any advancement tool returns an error, stop the sequence immediately and '
            '     report the failure; do not skip or continue to a later step.\n\n'
            'Outside explicit continuous mode, always hand off after starting the selected frontier.\n\n'
            'When a step is interrupted and user says "继续": call advance_step_and_hand_off with '
            'runtime_instruction="Previous attempt was interrupted. Check existing artifacts '
            'and only produce missing outputs (resume from checkpoint)."\n'
            'When user says "重试": call the single-step advance_step_and_hand_off for that '
            'failed/interrupted step; never place a previously attempted step in a batch.'
        )
    )
    return global_rules + common


def _build_mode_guidance(workflow_mode: str) -> str:
    """Build policy context from the shared Skill plus LazyMind-only capabilities."""
    from lazymind.chat.workflow.decision_policy import (
        lazymind_host_prompt, record_legacy_policy_call, shared_decision_prompt,
        shared_policy_enabled,
    )

    if not shared_policy_enabled():
        record_legacy_policy_call()
        return _build_legacy_mode_guidance(workflow_mode)
    return shared_decision_prompt() + lazymind_host_prompt(workflow_mode)

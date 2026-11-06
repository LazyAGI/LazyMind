"""LazyMind Chat adapter for the public Workflow runtime.

This module deliberately owns no Workflow definition loading, graph policy,
transition state, input binding, or Artifact persistence.  It only turns the
public Workflow SDK into ChatAgent tools and applies LazyMind's handoff rule.
"""
from __future__ import annotations

import asyncio
import hashlib
import json
import logging
import re
from dataclasses import dataclass
from typing import Any, Callable, Dict, List, Optional, Union

import httpx
import lazyllm
from lazyllm.tools import fc_register
from lazyllm.tools.agent.tool_runtime import HostFileAccess, _get_tool_runtime_metadata

from lazymind.chat.workflow.completed_actions import load_completed_actions
from lazymind.chat.engine.tools.intent_writer import enable_workflow_intent_scopes
from lazymind.chat.engine.tools.artifact_output import artifact_output
from lazymind.workflow_sdk import AdvanceRequest, StepCommand, WorkflowClient, WorkflowClientError
from lazymind.workflow_sdk.input_bindings import normalize_input_reference
from lazymind.workflow_toolkit import (
    AgentWorkflowToolProjection, HostWorkflowToolkit, StepCommandInput,
    workflow_package_input_types,
)

LOG = logging.getLogger(__name__)


async def resolve_workflow_injection_async(*args, **kwargs):
    """Keep synchronous SDK I/O off the Chat ASGI event loop.

    Core artifact projections may call back into Chat document inspection.
    Blocking this loop while waiting for Core deadlocks those callbacks.
    to_thread copies ContextVars, including LazyLLM's user/session scope.
    """
    return await asyncio.to_thread(resolve_workflow_injection, *args, **kwargs)


_SERIAL_WORKFLOW_EXECUTION_POLICY = (
    'Workflow SubAgent execution is strictly serial. Never issue more than one Workflow '
    'execution tool call in the same assistant tool-call batch, and never call Workflow '
    'execution tools in parallel. Pass exactly one step_id in each '
    'advance_step(step_ids=[...]) call even when Runtime returns multiple ready_steps. '
    'Wait for that call to return a terminal result, then use its refreshed frontier before '
    'starting the next Workflow SubAgent. '
)

_USER_BOUNDARY_HANDOFF_POLICY = (
    'When the user explicitly asks to stop after a particular step or to show its result before '
    'continuing, execute that last requested target with advance_step_and_hand_off, including '
    'retryable and rewindable targets. This explicit boundary takes precedence over default '
    'approval settings. For earlier steps of a requested multi-step path, use advance_step and '
    'continue serially; do not hand off early. The Host publishes the result and owns the checkpoint. '
    'Do not reread successfully published artifacts merely to confirm or summarize completion. '
    'If the user explicitly requests analysis or explanation of the result in Chat, use advance_step '
    'and provide that requested response instead of handing off. '
)


@dataclass
class WorkflowAgentContribution:
    tools: List[Any]
    stop_tools: List[str]
    agentic_config_patch: Dict[str, Any]
    runtime_context: str
    runtime_policy: Optional[Dict[str, Any]] = None
    # Trusted host extensions may explicitly adopt a server-created successor.
    # This callback is never exposed as a model tool.
    bind_successor: Optional[Callable[[Dict[str, Any]], None]] = None
    pending_ask: Optional[Dict[str, Any]] = None

    @property
    def required_tool_names(self) -> tuple[str, ...]:
        """Keep execution capabilities preloaded, including temporarily hidden ones."""
        def names(tools):
            for tool in tools:
                if isinstance(tool, dict):
                    if not tool.get('lazy', True):
                        yield from names(tool.get('tools', []))
                else:
                    yield tool.__name__
        return tuple(names(self.tools))


@dataclass(frozen=True)
class WorkflowDiscoveryContext:
    activations: List[Dict[str, Any]]
    prompt: str


def _agentic_config() -> Dict[str, Any]:
    return lazyllm.globals.get('agentic_config', {}) or {}


def _step_request_input(bound_input: Optional[str], session_id: str) -> tuple[str, str]:
    from lazymind.chat.service.component.history import is_workflow_rewind_action

    cfg = _agentic_config()
    current_input = str(
        bound_input or cfg.get('workflow_current_query') or cfg.get('query') or ''
    ).strip()
    if is_workflow_rewind_action(current_input, {'session_id': session_id}):
        # Let Core restore session intent for every step in this rerun turn,
        # including downstream execute operations, not only rewind/retry.
        return '', f'Recovery request for this rerun only: {current_input}'
    return current_input, ''


def _client() -> WorkflowClient:
    from lazymind.config import config
    cfg = _agentic_config()
    return WorkflowClient(
        str(config['core_api_url']).rstrip('/'),
        str(cfg.get('user_id') or ''),
        host='lazymind',
        transport=httpx,
        trace_context=lazyllm.get_trace_context,
        enable_tool_retrieval=bool(cfg.get('enable_tool_retrieval')),
        trigger_history_id=str((cfg.get('_workspace_execution') or {}).get('history_id') or ''),
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


def _declared_workflow_callback(value: Any) -> bool:
    if type(value) in {str, type(None)}:
        return True
    metadata = _get_tool_runtime_metadata(value) if callable(value) else None
    return metadata is not None and metadata.host_file_access is HostFileAccess.NONE


def _trusted_workflow_toolkit(toolkit: Any) -> bool:
    # This factory accepts only the host SDK binding it constructs. An injected
    # client or instance method override must carry its own boundary elsewhere.
    return (
        type(toolkit) is HostWorkflowToolkit
        and set(vars(toolkit)) == {'_client_factory', '_origin_ref', '_allowed_workflow_ids'}
        and toolkit._client_factory is _client
        and type(toolkit._origin_ref) is str
        and type(toolkit._allowed_workflow_ids) is frozenset
        and all(type(value) is str for value in toolkit._allowed_workflow_ids)
    )


def _register_host_file(capability: str):
    if capability == HostFileAccess.UNDECLARED.value:
        return lambda function: function
    return fc_register(host_file=capability)


def _handoff_tool(
    session: Union[str, Callable[[], str]],
    user_input: Optional[Union[str, Callable[[], str]]] = None,
) -> Any:
    capability = ('NONE' if all(_declared_workflow_callback(value)
                                for value in (session, user_input)) else 'UNDECLARED')

    @_register_host_file(capability)
    def advance_step_and_hand_off(step_id: str) -> Union[str, Dict[str, Any]]:
        """Execute one Ready Workflow step, then hand off for result approval."""
        selected_session_id = session() if callable(session) else session
        selected_session_id = str(selected_session_id or '').strip()
        if not selected_session_id:
            raise WorkflowClientError(
                'WORKFLOW_SESSION_NOT_INITIALIZED',
                'Call the selected trigger Workflow tool before using Session tools.',
            )
        client = _client()
        state_refreshed = False
        for attempt in range(2):
            frontier = client.get_ready_steps(selected_session_id)
            allowed = set(frontier.get('ready_steps') or [])
            allowed.update(frontier.get('retryable_steps') or [])
            rewindable = set(frontier.get('rewindable_steps') or [])
            allowed.update(rewindable)
            allowed.update(frontier.get('continue_steps') or [])
            if step_id not in allowed:
                if state_refreshed:
                    return _result_text(_state_changed_result(frontier, [step_id]))
                raise WorkflowClientError(
                    'WORKFLOW_TARGET_NOT_PROJECTED',
                    'Handoff target is not currently actionable.',
                    details={'step_id': step_id, 'allowed': sorted(allowed)},
                )
            try:
                cfg = _agentic_config()
                focus_hints = [_WORKFLOW_EDIT_SCOPE_POLICY]
                focused_tab = str(cfg.get('focused_tab') or '').strip()
                focused_sort_order = cfg.get('focused_sort_order')
                if focused_tab:
                    focus_hints.append(f'User is currently viewing Workflow tab {focused_tab!r}.')
                if focused_sort_order not in (None, ''):
                    focus_hints.append(
                        f'User is currently focused on artifact sort order {focused_sort_order}.'
                    )
                bound_user_input = user_input() if callable(user_input) else user_input
                current_user_input, recovery_instruction = _step_request_input(
                    bound_user_input, selected_session_id,
                )
                if step_id in (frontier.get('retryable_steps') or []):
                    pending = _input_required_result(client.get_state(
                        selected_session_id), [step_id], current_user_input)
                    if pending:
                        return pending
                if recovery_instruction:
                    focus_hints.append(recovery_instruction)
                response = client.advance(AdvanceRequest(
                    session_id=selected_session_id,
                    expected_state_version=int(frontier.get('state_version') or 0),
                    steps=[StepCommand(
                        step_id=step_id,
                        user_input=current_user_input,
                        runtime_instruction=' '.join(focus_hints),
                    )],
                    handoff=True,
                    workflow_mode=(
                        'auto' if cfg.get('workflow_mode') == 'auto' else 'dynamic'
                    ),
                    retry_origin=(
                        'user' if bool(cfg.get('user_authorized_workflow_retry'))
                        else 'automatic'
                    ),
                ))
                result = dict(response.result)
                if result.get('accepted') is True:
                    cfg.pop('user_authorized_workflow_retry', None)
                if state_refreshed:
                    result.update(_state_refresh_notice())
                if step_id in rewindable:
                    result = _compact_transition_result(result)
                if result.get('accepted') is True:
                    # Structured tool results are consumed by the executor before
                    # another model call, including sessions created in this turn.
                    return {
                        **result,
                        '_agent_control': {
                            'stop': True,
                            'reason': 'workflow_handoff',
                            'final_text': '已交由工作流执行，请在工作流中查看进度和结果。',
                        },
                    }
                return _result_text(result)
            except WorkflowClientError as exc:
                pending = _approval_blocked_result(exc)
                if pending:
                    return pending
                if exc.code != 'STATE_VERSION_CONFLICT' or attempt > 0:
                    raise
                state_refreshed = True
        raise AssertionError('unreachable')

    advance_step_and_hand_off.__doc__ = (
        'Execute one exact Runtime-returned ready, retryable, rewindable, or continue target '
        'and end this Chat turn after the Host accepts the post-execution checkpoint. '
        'Use for the LAST step the user asks to run before stopping or viewing results, '
        'including reruns and regardless of automatic approval defaults. Use advance_step '
        'for earlier steps in a requested multi-step path or when the user requests a Chat '
        'explanation afterward. Results and failures remain visible in the Workflow.'
    )
    return advance_step_and_hand_off


_WORKFLOW_EDIT_SCOPE_POLICY = (
    'Edit scope: the current user request overrides broad step defaults. '
    'For a named section/paragraph/page, read the existing artifact first and change only '
    'that target; preserve all other content, formatting, page count and order. '
    'In main Chat, for a text/Markdown-only edit use patch_artifact_text with a unique exact excerpt, '
    'without rewinding steps; do not replace or regenerate other sections. '
    'For a composite resolve the target using current 1-based sort_order and selected '
    'artifact identity; list_index is a stable ID, NOT page number minus one. '
    'Explicit page/section numbers override UI focus; focus only resolves "this page". '
    'For linked composites preserve alignment across outline, prompts, images, slides and notes. '
    'In main Chat, for a pure page move/swap call reorder_artifact_pages directly with all linked lists; '
    'do not rewind or regenerate any step. '
    'Insert/delete/reorder whole pages together; do not confuse removing a bullet with deleting '
    'a page. Regenerate only requested pages. Never recreate the entire deck for a local edit. '
    'A request saying only edit the outline/section is a stop boundary: do not automatically '
    'regenerate downstream outputs. Report which outputs changed and which need regeneration. '
)


def _artifact_by_handle(toolkit: HostWorkflowToolkit, session_id: str,
                        artifact_ref: str) -> Dict[str, Any]:
    values = toolkit.list_artifacts(session_id).get('artifacts') or []
    ref = str(artifact_ref or '').strip()
    matches = []
    for item in values:
        index = item.get('list_index')
        handles = {str(item.get('artifact_id') or item.get('id') or ''), str(item.get('slot') or '')}
        if index is not None:
            handles.add(f'{item.get("slot")}[{index}]')
        if ref in handles:
            matches.append(item)
    if len(matches) != 1:
        raise WorkflowClientError(
            'ARTIFACT_NOT_SELECTED',
            f'Artifact reference {ref!r} must identify one selected Session artifact.',
            details={'available_artifacts': [
                f'{item.get("slot")}[{item.get("list_index")}]'
                if item.get('list_index') is not None else item.get('slot') for item in values
            ]},
        )
    return matches[0]


def _compact_model_frontier(result: Dict[str, Any]) -> Dict[str, Any]:
    """Keep runtime routing/approval evidence without replaying the compiled package.

    The graph embeds every step prompt and schema. Its live projection already
    carries the authoritative nodes, edge conditions and target classes needed
    by the agent; sending both causes tool spilling and extra file-reading turns.
    """
    compact = dict(result)
    for key in ('projection', 'workflow_state'):
        value = compact.get(key)
        if isinstance(value, dict) and 'graph' in value:
            compact[key] = {k: v for k, v in value.items() if k != 'graph'}
    return compact


def _compact_transition_result(result: Dict[str, Any]) -> Dict[str, Any]:
    """Keep a rewind transition receipt small and actionable.

    Core returns the full authoritative projection and Workflow state so UI and
    non-model clients can inspect them. Rewind repeats graph definitions, node
    prompts, and attempt history that the Agent has already seen. The normal
    forward path intentionally keeps the projection so choice-edge conditions
    remain visible; callers use this adapter only for a rewindable target.
    """
    projection = result.get('projection')
    projection = projection if isinstance(projection, dict) else {}
    workflow_state = result.get('workflow_state')
    workflow_state = workflow_state if isinstance(workflow_state, dict) else {}
    compact = {
        key: value
        for key, value in result.items()
        if key not in {'projection', 'workflow_state'}
    }

    frontier_fields = {
        'ready_steps': 'ready',
        'retryable_steps': 'retryable',
        'rewindable_steps': 'rewindable',
        'continue_steps': 'continue',
    }
    for result_key, projection_key in frontier_fields.items():
        if result_key not in compact and projection_key in projection:
            compact[result_key] = projection.get(projection_key) or []

    completed = (
        projection.get('completed') is True
        or workflow_state.get('status') == 'completed'
        or compact.get('status') == 'completed'
    )
    if completed:
        compact['status'] = 'completed'
        compact.setdefault('outcome', 'workflow_completed')
    elif not compact.get('status') and workflow_state.get('status'):
        compact['status'] = workflow_state['status']
    return compact


def _with_terminal_agent_control(result: Dict[str, Any], workflow_id: str = '') -> Dict[str, Any]:
    pending = _input_required_result(result)
    if pending:
        return {**result, **pending}
    # A material boundary needs a user-visible result, not guessed replacement input.
    reasons = result.get('failure_reasons') or {}
    if (result.get('status') == 'failed' and result.get('outcome') == 'step_failed'
            and isinstance(reasons, dict)
            and any('MISSING_MATERIAL:' in str(reason) for reason in reasons.values())):
        return {**result, '_agent_control': {
            'stop': True, 'reason': 'workflow_material_check_failed',
            'final_text': '本步骤的材料检查未通过，已停止自动推进并保留原输入。请在工作流查看失败原因；检查材料或修复后可重试。',
        }}
    workflow_state = result.get('workflow_state')
    workflow_state = workflow_state if isinstance(workflow_state, dict) else {}
    if (workflow_id == 'paper-writing'
            and result.get('status') == 'failed' and result.get('outcome') == 'step_failed'):
        return {**result, '_agent_control': {
            'stop': True, 'reason': 'workflow_step_failed',
            'final_text': '论文步骤执行失败，已保留当前材料并停止自动重试和回退。请在工作流查看原因，修复后重试。',
        }}
    display = workflow_state.get('display') or {}
    if isinstance(display, dict) and display.get('status') == 'waiting' and display.get('approval_step_id'):
        notice = '本阶段结果已保存，请在工作流面板审核后点击“继续执行”。'
        return {**result, 'status': 'waiting', 'outcome': 'workflow_approval_required',
                '_agent_control': {'stop': True, 'reason': 'workflow_approval_required', 'final_text': notice}}
    projection = result.get('projection')
    if not isinstance(projection, dict):
        projection = workflow_state.get('projection')
    projection = projection if isinstance(projection, dict) else {}
    completed = (
        projection.get('completed') is True
        or workflow_state.get('status') == 'completed'
        or result.get('status') == 'completed'
    )
    if not completed:
        return result
    return {
        **result,
        '_agent_control': {
            'stop': True,
            'reason': 'workflow_completed',
            'final_text': '工作流已完成，最终产物已生成。',
        },
    }


def _approval_blocked_result(error: WorkflowClientError) -> Optional[Dict[str, Any]]:
    if error.code != 'WORKFLOW_APPROVAL_REQUIRED':
        return None
    notice = '当前阶段等待审批，请在工作流面板点击“继续执行”后继续。'
    return {'status': 'waiting', 'outcome': 'workflow_approval_required', 'user_notice': notice,
            '_agent_control': {'stop': True, 'reason': 'workflow_approval_required', 'final_text': notice}}


def _input_required_result(
    result: Dict[str, Any], requested: Optional[List[str]] = None, current_input: Optional[str] = None,
) -> Optional[Dict[str, Any]]:
    """A paused attempt resumes on an answer, never on an identical automatic retry."""
    state = result.get('workflow_state') or result
    projection = state.get('projection') or {}
    if state.get('status') == 'completed' or projection.get('completed') is True:
        return None
    for step_id, attempts in (state.get('attempt_history') or {}).items():
        if requested is not None and step_id not in requested:
            continue
        retryable = projection.get('retryable', projection.get('retryable_steps'))
        if isinstance(retryable, list) and step_id not in retryable:
            continue
        effective = [item for item in attempts if item.get('validity') in {None, '', 'effective'}]
        if not effective:
            continue
        latest = max(effective, key=lambda item: item.get('attempt', 0))
        pending = latest.get('input_required')
        if latest.get('status') != 'cancelled' or not isinstance(pending, dict) or not pending.get('question'):
            continue
        text = str(current_input or '').strip()
        retry_only = text.rstrip('。.!！').lower() in {'重试', '重新执行', 'retry', 're-run', 'rerun', 'try again'}
        changed_input = hashlib.sha256(text.encode()).hexdigest() != pending.get('request_sha256')
        if current_input is not None and text and not retry_only and changed_input:
            continue
        question = pending['question']
        return {'status': 'waiting', 'outcome': 'workflow_input_required', 'user_notice': question,
                '_agent_control': {'stop': True, 'reason': 'workflow_input_required', 'final_text': question}}
    return None


def _safe_session_tools(
    toolkit: HostWorkflowToolkit,
    session: Union[str, Callable[[], str]],
    user_input: Optional[Union[str, Callable[[], str]]] = None,
    workflow_context: Optional[Dict[str, Any]] = None,
) -> List[Any]:
    """Model tools whose protocol and concurrency parameters are Host-injected."""
    capability = (
        'NONE' if _trusted_workflow_toolkit(toolkit)
        and all(_declared_workflow_callback(value)
                for value in (session, user_input)) else 'UNDECLARED'
    )

    def session_id() -> str:
        value = session() if callable(session) else session
        selected = str(value or '').strip()
        # A query must never initialize a session: that would discard the
        # trigger's not-yet-supplied input bindings and make recovery ambiguous.
        if not selected:
            raise WorkflowClientError(
                'WORKFLOW_SESSION_NOT_INITIALIZED',
                'Call the selected trigger Workflow tool before using Session tools. '
                'Do not infer that an attachment is required; the trigger determines '
                'whether any external input is actually required.',
            )
        return selected

    @_register_host_file(capability)
    def get_workflow_state() -> Dict[str, Any]:
        """Read this conversation's authoritative Workflow state."""
        return toolkit.get_workflow_state(session_id())

    @_register_host_file(capability)
    def get_ready_steps() -> Dict[str, Any]:
        """Read exact forward, retryable, and rewindable targets for this Session."""
        return _compact_model_frontier(toolkit.get_ready_steps(session_id()))

    @_register_host_file(capability)
    def advance_step(step_ids: List[str]) -> Dict[str, Any]:
        """Execute exactly one Runtime-returned target; never batch or parallelize steps."""
        requested = [str(value).strip() for value in step_ids if str(value).strip()]
        selected_session_id = session_id()
        state_refreshed = False
        for attempt in range(2):
            frontier = toolkit.get_ready_steps(selected_session_id)
            allowed = set(frontier.get('ready_steps') or [])
            allowed.update(frontier.get('retryable_steps') or [])
            rewindable = set(frontier.get('rewindable_steps') or [])
            allowed.update(rewindable)
            allowed.update(frontier.get('continue_steps') or [])
            if not requested or any(value not in allowed for value in requested):
                if state_refreshed:
                    return _state_changed_result(frontier, requested)
                raise WorkflowClientError(
                    'WORKFLOW_TARGET_NOT_PROJECTED',
                    'Every target must come from the latest Runtime target classes.',
                    details={'requested': requested, 'allowed': sorted(allowed)},
                )
            recovery = set(frontier.get('retryable_steps') or []) | set(
                frontier.get('rewindable_steps') or [],
            )
            if len(requested) > 1 and any(value in recovery for value in requested):
                raise WorkflowClientError(
                    'WORKFLOW_RECOVERY_MUST_BE_SINGULAR',
                    'Retryable and rewindable targets must be submitted one at a time.',
                )
            try:
                cfg = _agentic_config()
                focus_hints = [_WORKFLOW_EDIT_SCOPE_POLICY]
                focused_tab = str(cfg.get('focused_tab') or '').strip()
                focused_sort_order = cfg.get('focused_sort_order')
                if focused_tab:
                    focus_hints.append(f'User is currently viewing Workflow tab {focused_tab!r}.')
                if focused_sort_order not in (None, ''):
                    focus_hints.append(
                        f'User is currently focused on artifact sort order {focused_sort_order}.'
                    )
                bound_user_input = user_input() if callable(user_input) else user_input
                current_user_input, recovery_instruction = _step_request_input(
                    bound_user_input, selected_session_id,
                )
                if any(value in (frontier.get('retryable_steps') or []) for value in requested):
                    pending = _input_required_result(toolkit.get_workflow_state(
                        selected_session_id), requested, current_user_input)
                    if pending:
                        return pending
                if recovery_instruction:
                    focus_hints.append(recovery_instruction)
                user_retry = bool(cfg.get('user_authorized_workflow_retry'))
                result = toolkit.advance_step(
                    selected_session_id, int(frontier.get('state_version') or 0),
                    [
                        StepCommandInput(
                            step_id=value,
                            user_input=current_user_input,
                            runtime_instruction=' '.join(focus_hints),
                        )
                        for value in requested
                    ],
                    retry_origin=(
                        'user' if bool(cfg.get('user_authorized_workflow_retry'))
                        else 'automatic'
                    ),
                    workflow_mode=(
                        'auto' if cfg.get('workflow_mode') == 'auto' else 'dynamic'
                    ),
                )
                if state_refreshed:
                    result = {**result, **_state_refresh_notice()}
                result = _with_terminal_agent_control(result, str((workflow_context or {}).get('workflow_id') or ''))
                if user_retry:
                    # One user retry must not authorize further model-initiated attempts.
                    cfg.pop('user_authorized_workflow_retry', None)
                    result.setdefault('_agent_control', {
                        'stop': True, 'reason': 'workflow_retry_completed',
                        'final_text': '本次重试已执行，请在工作流中查看结果。',
                    })
                if any(value in rewindable for value in requested):
                    result = _compact_transition_result(result)
                return _compact_model_frontier(result)
            except WorkflowClientError as exc:
                pending = _approval_blocked_result(exc)
                if pending:
                    return pending
                if exc.code != 'STATE_VERSION_CONFLICT' or attempt > 0:
                    raise
                state_refreshed = True
        raise AssertionError('unreachable')

    @_register_host_file(capability)
    def list_workflow_inputs() -> Dict[str, Any]:
        """List durable input bindings for this Session."""
        return artifact_output(toolkit.list_workflow_inputs(session_id()))

    @_register_host_file(capability)
    def list_artifacts() -> Dict[str, Any]:
        """List selected Artifacts for this Session."""
        return artifact_output(toolkit.list_artifacts(session_id()))

    @_register_host_file(capability)
    def reorder_artifact_pages(slot_ids: List[str], order: List[int]) -> Dict[str, Any]:
        """Move/swap pages without rerunning steps. Include ALL linked page-list slots.

        order is the complete 1-based visual permutation (swap 1/3: [3,2,1]).
        Read current artifacts first. Preserve unmentioned pages and single documents.
        Host resolves stable IDs and versions; a conflict leaves every list unchanged.
        """
        return toolkit.reorder_artifact_pages(session_id(), slot_ids, order)

    @_register_host_file(capability)
    def read_artifact(artifact_ref: str) -> Dict[str, Any]:
        """Read a selected Artifact by exact slot handle such as report or images[0]."""
        artifact = _artifact_by_handle(toolkit, session_id(), artifact_ref)
        return artifact_output(toolkit.read_artifact(str(artifact.get('artifact_id') or artifact.get('id') or '')))

    @_register_host_file(capability)
    def patch_artifact_text(artifact_ref: str, old_text: str, new_text: str) -> Dict[str, Any]:
        """Edit one unique excerpt of a selected text/Markdown artifact without rerunning steps.

        Read the artifact first. Keep the edit scoped to a unique exact
        section/paragraph excerpt. Does not regenerate downstream pages.
        For a list use the exact handle returned by list_artifacts.
        Headings contain section names; the document editor supplies their numbering.
        """
        from lazymind.document_tools.artifacts import normalize_generated_markdown

        artifact = _artifact_by_handle(toolkit, session_id(), artifact_ref)
        artifact_id = str(artifact.get('artifact_id') or artifact.get('id') or '')
        current = toolkit.read_artifact(artifact_id)
        value = current.get('value')
        field = next((key for key in ('text', 'data')
                      if isinstance(value, dict) and isinstance(value.get(key), str)), None)
        text = value.get(field) if field else value
        content_type = str(current.get('content_type') or '').lower()
        text_type = content_type in ('text', 'markdown', 'file', 'text/markdown', 'text/plain')
        text_schema = isinstance(value, dict) and value.get('schema') in ('text/markdown', 'text/plain')
        if not (text_type or text_schema) or not isinstance(text, str):
            raise ValueError('Only a readable text/Markdown artifact supports excerpt editing')
        if re.search(r'<(?:!doctype\s+html|html)(?:\s|>)', text, re.IGNORECASE):
            raise ValueError('HTML slides require their scoped page-editing tool')
        if not old_text or text.count(old_text) != 1:
            raise ValueError('old_text must match exactly once; read the current artifact and retry')
        revised = normalize_generated_markdown(text.replace(old_text, new_text, 1))
        revised_value = {**value, field: revised} if field else revised
        return artifact_output(toolkit.patch_artifact(
            artifact_id, int(current.get('revision') or artifact.get('revision') or 0),
            revised_value, str(current.get('content_type') or 'text'), str(current.get('caption') or ''),
            base_draft_version=int(current.get('draft_version') or 0),
        ))

    @_register_host_file(capability)
    def patch_artifact(artifact_ref: str, value: Any, caption: str = '') -> Dict[str, Any]:
        """Patch a selected Artifact; Host injects id, base revision, type, and command."""
        artifact = _artifact_by_handle(toolkit, session_id(), artifact_ref)
        artifact_id = str(artifact.get('artifact_id') or artifact.get('id') or '')
        content_type = str(artifact.get('content_type') or 'json')
        return artifact_output(toolkit.patch_artifact(
            artifact_id, int(artifact.get('revision') or 0), value, content_type, caption,
        ))

    return [
        get_workflow_state, get_ready_steps, advance_step, list_workflow_inputs,
        list_artifacts, read_artifact, patch_artifact, patch_artifact_text, reorder_artifact_pages,
    ]


def _state_refresh_notice() -> Dict[str, Any]:
    return {
        'state_version_refreshed': True,
        'user_notice': (
            '工作流状态在提交期间发生变化，系统已自动刷新并使用最新状态继续执行；'
            'state_version 由系统维护，您无需提供或重试版本号。'
        ),
    }


def _state_changed_result(frontier: Dict[str, Any], requested: List[str]) -> Dict[str, Any]:
    """Return a user-visible, non-mutating result when refreshed targets changed."""
    return {
        'status': 'waiting',
        'outcome': 'workflow_state_changed',
        'requested_steps': requested,
        'ready_steps': frontier.get('ready_steps') or [],
        'retryable_steps': frontier.get('retryable_steps') or [],
        'rewindable_steps': frontier.get('rewindable_steps') or [],
        'continue_steps': frontier.get('continue_steps') or [],
        'user_notice': (
            '工作流状态已被其他执行或后台更新改变，本次没有继续提交步骤。'
            '系统已读取最新状态；请根据当前可执行步骤重新确认下一步。'
        ),
    }


def _safe_authoring_tools(toolkit: HostWorkflowToolkit) -> List[Any]:
    """Context-bound authoring tools; models author content, not concurrency metadata."""
    capability = 'NONE' if _trusted_workflow_toolkit(toolkit) else 'UNDECLARED'
    cfg = _agentic_config()

    def _draft() -> Dict[str, Any]:
        draft_id = str(cfg.get('workflow_authoring_draft_id') or '')
        if not draft_id:
            raise WorkflowClientError(
                'WORKFLOW_DRAFT_NOT_SELECTED',
                'Create or select a Workflow draft before using this authoring action.',
            )
        value = toolkit.get_workflow_draft(draft_id)
        cfg['workflow_authoring_draft_version'] = int(value.get('version') or 0)
        return value

    @_register_host_file(capability)
    def create_workflow_draft(name: str, files: Dict[str, str]) -> Dict[str, Any]:
        """Create a draft from authored files; Host injects pinned Skill metadata."""
        skill = cfg.get('workflow_authoring_skill_context') or {}
        source_type = 'skill' if skill else 'blank'
        value = toolkit.create_workflow_draft(
            name, files, str(skill.get('skill_id') or ''),
            str(skill.get('revision_id') or ''), str(skill.get('tree_hash') or ''), source_type,
        )
        cfg['workflow_authoring_draft_id'] = str(value.get('draft_id') or value.get('id') or '')
        cfg['workflow_authoring_draft_version'] = int(value.get('version') or 0)
        return value

    @_register_host_file(capability)
    def list_workflow_drafts() -> Dict[str, Any]:
        """List drafts available for exact selection."""
        return toolkit.list_workflow_drafts()

    @_register_host_file(capability)
    def select_workflow_draft(draft_id: str) -> Dict[str, Any]:
        """Select one exact draft returned by list_workflow_drafts."""
        value = toolkit.get_workflow_draft(draft_id)
        cfg['workflow_authoring_draft_id'] = draft_id
        cfg['workflow_authoring_draft_version'] = int(value.get('version') or 0)
        return value

    @_register_host_file(capability)
    def get_workflow_draft() -> Dict[str, Any]:
        """Read the selected authoring draft."""
        return _draft()

    @_register_host_file(capability)
    def update_workflow_draft_file(path: str, content: str) -> Dict[str, Any]:
        """Update one allowed package path; Host injects draft and optimistic version."""
        current = _draft()
        value = toolkit.update_workflow_draft_file(
            str(cfg['workflow_authoring_draft_id']), path, content,
            int(current.get('version') or 0),
        )
        cfg['workflow_authoring_draft_version'] = int(value.get('version') or 0)
        return value

    @_register_host_file(capability)
    def validate_workflow_draft() -> Dict[str, Any]:
        """Validate the selected draft."""
        _draft()
        return toolkit.validate_workflow_draft(str(cfg['workflow_authoring_draft_id']))

    @_register_host_file(capability)
    def get_workflow_diagnostics() -> Dict[str, Any]:
        """Read diagnostics for the selected draft."""
        _draft()
        return toolkit.get_workflow_diagnostics(str(cfg['workflow_authoring_draft_id']))

    @_register_host_file(capability)
    def publish_workflow() -> Dict[str, Any]:
        """Publish the selected validated draft."""
        _draft()
        return toolkit.publish_workflow(str(cfg['workflow_authoring_draft_id']))

    return [
        create_workflow_draft, list_workflow_drafts, select_workflow_draft,
        get_workflow_draft, update_workflow_draft_file,
        validate_workflow_draft, get_workflow_diagnostics, publish_workflow,
    ]


def _import_attachment(path: str) -> Dict[str, Any]:
    from lazymind.chat.workflow.file_adapter import LazyMindHostFileAdapter
    from lazymind.config import config
    cfg = _agentic_config()
    value = LazyMindHostFileAdapter(
        str(config['core_api_url']).rstrip('/'), str(cfg.get('user_id') or ''), transport=httpx,
    ).import_attachment(path)
    return normalize_input_reference(value)


def _import_text_binding(material_id: str, value: str) -> Dict[str, Any]:
    from lazymind.chat.workflow.file_adapter import LazyMindHostFileAdapter
    from lazymind.config import config
    cfg = _agentic_config()
    safe_name = re.sub(r'[^0-9A-Za-z_.-]+', '_', material_id).strip('._') or 'input'
    resource = LazyMindHostFileAdapter(
        str(config['core_api_url']).rstrip('/'), str(cfg.get('user_id') or ''), transport=httpx,
    ).import_text(f'{safe_name}.txt', str(value))
    return normalize_input_reference(resource)


def _conversation_has_attachments() -> bool:
    cfg = _agentic_config()
    if any(str(value or '').strip() for value in (cfg.get('files') or [])):
        return True
    history = cfg.get('history_files_per_turn') or {}
    return isinstance(history, dict) and any(
        any(str(value or '').strip() for value in (values or []))
        for values in history.values()
        if isinstance(values, list)
    )


def _resolve_workflow_attachment(reference: str) -> tuple[Optional[str], Optional[str]]:
    """Resolve a file binding without loading attachment tooling for scalar launches."""
    from lazymind.chat.engine.subagent.tools import _resolve_attachment
    return _resolve_attachment(reference)


def _clean_workflow_text(value: Any) -> str:
    return re.sub(r'\s+', ' ', str(value or '')).strip()


def _workflow_trigger_tool_name(workflow_id: str) -> str:
    stem = re.sub(r'[^0-9A-Za-z_]+', '_', workflow_id.lower()).strip('_')
    stem = stem.removesuffix('_workflow') or 'workflow'
    return f'trigger_{stem}_workflow'


def _selected_runtime_policy(
    workflow_context: Dict[str, Any],
    workflow_catalog: List[Dict[str, Any]],
    allowed_refs: set[str],
) -> Dict[str, Any]:
    """Resolve immutable package runtime policy without workflow-id branches."""
    direct = workflow_context.get('runtime')
    if isinstance(direct, dict):
        return dict(direct)
    identifiers: set[str] = set()
    for key in ('workflow_ref', 'workflow_id'):
        value = str(workflow_context.get(key) or '').strip()
        if value:
            identifiers.add(value)
    # An active session's exact identity must win over the broader set of
    # Workflows that happen to be available to the conversation. Without this,
    # catalog ordering can apply another package's completed-edit policy.
    if not identifiers:
        normalized_allowed = {
            value for raw in allowed_refs
            if (value := str(raw or '').strip())
        }
        if len(normalized_allowed) == 1:
            identifiers = normalized_allowed
    identifiers |= {value.removeprefix('builtin:') for value in identifiers}
    for item in workflow_catalog:
        item_ids = {
            str(item.get('workflow_ref') or '').strip(),
            str(item.get('workflow_id') or '').strip(),
        }
        item_ids |= {value.removeprefix('builtin:') for value in item_ids}
        runtime = item.get('runtime')
        if identifiers & item_ids and isinstance(runtime, dict):
            return dict(runtime)
    return {}


def _runtime_clarification_fields(runtime_policy: Any) -> List[Dict[str, Any]]:
    """Normalize package-declared semantic inputs for model-facing guidance."""
    if not isinstance(runtime_policy, dict):
        return []
    result: List[Dict[str, Any]] = []
    for raw in runtime_policy.get('clarification_fields') or []:
        if not isinstance(raw, dict):
            continue
        field_id = _clean_workflow_text(raw.get('id'))
        question = _clean_workflow_text(raw.get('question'))
        if not field_id or not question:
            continue
        question_type = _clean_workflow_text(raw.get('type')).lower() or 'text'
        if question_type not in {'text', 'boolean', 'single', 'multiple'}:
            question_type = 'text'
        choices = [
            choice for value in (raw.get('choices') or [])
            if (choice := _clean_workflow_text(value))
        ]
        choice_policy = _clean_workflow_text(raw.get('choice_policy')).lower() or 'seed'
        if choice_policy not in {'seed', 'subset', 'fixed'}:
            choice_policy = 'seed'
        field = {
            'id': field_id,
            'label': _clean_workflow_text(raw.get('label')) or field_id,
            'question': question,
            'type': question_type,
            'choices': choices,
            'choice_policy': choice_policy,
            'ask_when': _clean_workflow_text(raw.get('ask_when')),
        }
        for key in ('reason', 'required', 'resolution', 'default', 'boolean_labels', 'validation',
                    'applies_when', 'value_aliases', 'patterns'):
            if key in raw:
                field[key] = raw[key]
        # A conditional attachment augments an existing choice question. It uses
        # the normal conversation attachment transport instead of introducing a
        # workflow-specific file question or upload API.
        raw_attachment = raw.get('attachment')
        if question_type == 'single' and isinstance(raw_attachment, dict):
            when_choice = _clean_workflow_text(raw_attachment.get('when_choice'))
            material_id = _clean_workflow_text(raw_attachment.get('material_id'))
            accept = [
                value if value.startswith('.') else f'.{value}'
                for item in (raw_attachment.get('accept') or [])
                if (value := _clean_workflow_text(item).lower())
            ]
            max_files = raw_attachment.get('max_files', 1)
            if (
                when_choice in choices and material_id and accept
                and type(max_files) is int and (max_files == -1 or max_files >= 1)
            ):
                field['attachment'] = {
                    'when_choice': when_choice,
                    'material_id': material_id,
                    'accept': list(dict.fromkeys(accept)),
                    'max_files': max_files,
                    'required': raw_attachment.get('required') is True,
                }
        if question_type in {'single', 'multiple'}:
            # Fixed package choices are closed by definition. This default also
            # covers installed package revisions created before ``allow_other``
            # was serialized into their runtime policy.
            default_allow_other = choice_policy != 'fixed'
            field['allow_other'] = raw.get(
                'allow_other', default_allow_other,
            ) is not False
        result.append(field)
    return result


def _startup_clarification_policies(
    runtime_policy: Any,
    workflow_catalog: Any = None,
    *,
    discovery_mode: bool = False,
) -> List[Dict[str, Any]]:
    if isinstance(runtime_policy, dict) and _runtime_clarification_fields(runtime_policy):
        return [runtime_policy]
    if not discovery_mode:
        return []
    return [
        item['runtime']
        for item in (workflow_catalog or [])
        if isinstance(item, dict)
        and isinstance(item.get('runtime'), dict)
        and _runtime_clarification_fields(item['runtime'])
    ]


def _tool_call_arguments(tool_call: Any) -> Dict[str, Any]:
    if not isinstance(tool_call, dict):
        return {}
    function = tool_call.get('function')
    if not isinstance(function, dict) or str(function.get('name') or '') != 'ask_user':
        return {}
    arguments = function.get('arguments')
    if isinstance(arguments, str):
        try:
            arguments = json.loads(arguments)
        except json.JSONDecodeError:
            return {}
    if not isinstance(arguments, dict):
        return {}
    from lazymind.chat.engine.tools.ask_user import AskUserQuestion

    # History keeps the original model arguments, not the validated tool input.
    # Recognize the same accepted question format when restoring the user's goal.
    questions = arguments.get('questions')
    if not isinstance(questions, list):
        return {}
    try:
        return {**arguments, 'questions': [
            AskUserQuestion.model_validate(question).model_dump(exclude_none=True, exclude_unset=True)
            for question in questions
        ]}
    except ValueError:
        return {}


def _question_fingerprint(value: Any) -> str:
    return re.sub(r'[^0-9a-z\u4e00-\u9fff]+', '', str(value or '').lower())


def enforce_startup_clarification_policy(
    questions: Any,
    runtime_policy: Any,
) -> Any:
    """Apply package-owned fixed choice fields to an ``ask_user`` call.

    Startup clarification is model-routed, but a model omission must not turn a
    package-declared closed choice into an open-ended one.  Match by the declared
    field id first, then by question text or the exact declared choice set so the
    policy also survives providers that omit the optional id.
    """
    if not isinstance(questions, list):
        return questions
    governed_fields = [
        field for field in _runtime_clarification_fields(runtime_policy)
        if (
            field.get('choice_policy') == 'fixed'
            and field.get('type') in {'single', 'multiple'}
            and field.get('choices')
        ) or field.get('attachment')
    ]
    if not governed_fields:
        return questions

    governed: List[Any] = []
    for raw_question in questions:
        if not isinstance(raw_question, dict):
            governed.append(raw_question)
            continue
        question = dict(raw_question)
        question_id = _clean_workflow_text(question.get('id'))
        question_text = _question_fingerprint(question.get('text'))
        question_choices = {
            _clean_workflow_text(value)
            for value in (question.get('choices') or [])
            if _clean_workflow_text(value) and _clean_workflow_text(value) != '其他'
        }
        matched = None
        for field in governed_fields:
            field_text = _question_fingerprint(field.get('question'))
            field_choices = set(field.get('choices') or [])
            if question_id and question_id == field.get('id'):
                matched = field
                break
            if question_text and field_text and (
                question_text in field_text or field_text in question_text
            ):
                matched = field
                break
            if question_choices and question_choices == field_choices:
                matched = field
                break
        if matched is not None:
            if matched.get('choice_policy') == 'fixed':
                question['choice_policy'] = 'fixed'
                question['type'] = matched['type']
                question['choices'] = list(matched['choices'])
                question['allow_other'] = matched.get('allow_other', True)
            if matched.get('attachment'):
                question['attachment'] = dict(matched['attachment'])
        governed.append(question)
    return governed


def _history_startup_ask_index(
    conversation_history: Any,
    runtime_policy: Any,
    workflow_catalog: Any = None,
    *,
    discovery_mode: bool = False,
    current_query: str = '',
) -> int:
    policies = _startup_clarification_policies(
        runtime_policy,
        workflow_catalog,
        discovery_mode=discovery_mode,
    )
    declared_fields = [
        field
        for policy in policies
        for field in _runtime_clarification_fields(policy)
    ]
    if not declared_fields:
        return -1
    field_ids = {str(field['id']) for field in declared_fields}
    question_fingerprints = {
        fingerprint
        for field in declared_fields
        if (fingerprint := _question_fingerprint(field.get('question')))
    }
    history = conversation_history if isinstance(conversation_history, list) else []
    for index in range(len(history) - 1, -1, -1):
        message = history[index]
        if not isinstance(message, dict) or message.get('role') != 'assistant':
            continue
        matched = False
        for call in message.get('tool_calls') or []:
            arguments = _tool_call_arguments(call)
            if not arguments:
                continue
            # A failed invocation never displayed a card. In particular, old
            # validation failures must not suppress clarification after recovery.
            failed = False
            for reply in history[index + 1:]:
                if (not isinstance(reply, dict) or reply.get('role') != 'tool'
                        or reply.get('tool_call_id') != call.get('id')):
                    continue
                result = reply.get('content')
                if isinstance(result, str):
                    try:
                        result = json.loads(result)
                    except (TypeError, ValueError):
                        continue
                if isinstance(result, dict) and result.get('ok') is False:
                    failed = True
                    break
            if failed:
                continue
            for question in arguments.get('questions') or []:
                if not isinstance(question, dict):
                    continue
                if str(question.get('id') or '').strip() in field_ids:
                    matched = True
                    break
                fingerprint = _question_fingerprint(question.get('text'))
                if fingerprint and any(
                    fingerprint in declared or declared in fingerprint
                    for declared in question_fingerprints
                ):
                    matched = True
                    break
            if matched:
                break
        if not matched:
            continue

        # An ask belongs to the current startup exchange only while no later
        # assistant response has continued/completed that request. Otherwise a
        # historical PPT task would suppress clarification for every future PPT
        # started in the same conversation. The current answer may appear as a
        # trailing user message in some history adapters; allow only that one.
        tail = [entry for entry in history[index + 1:] if isinstance(entry, dict)]
        if any(entry.get('role') == 'assistant' for entry in tail):
            continue
        tail_users = [entry for entry in tail if entry.get('role') == 'user']
        if len(tail_users) > 1:
            continue
        # Most history adapters exclude the current user turn. Therefore one
        # historical user message after the Ask means that answer turn already
        # happened. It must not suppress clarification for a later task merely
        # because the prior workflow crashed before writing an assistant reply.
        # Some adapters do include the current turn; retain the Ask only when
        # that trailing message is the current query itself.
        if tail_users and current_query:
            trailing_query = _clean_workflow_text(tail_users[-1].get('content'))
            if trailing_query and trailing_query != _clean_workflow_text(current_query):
                continue
        return index
    return -1


def _merge_startup_clarification_context(
    current_query: str,
    conversation_history: Any,
    runtime_policy: Any,
) -> str:
    """Merge the original request with the answer turn before trigger creation."""
    ask_index = _history_startup_ask_index(
        conversation_history,
        runtime_policy,
        current_query=current_query,
    )
    if ask_index < 0:
        return current_query
    history = conversation_history if isinstance(conversation_history, list) else []
    original = ''
    for index in range(ask_index - 1, -1, -1):
        message = history[index]
        if isinstance(message, dict) and message.get('role') == 'user':
            original = str(message.get('content') or '').strip()
            if original:
                break
    answer = str(current_query or '').strip()
    if not original or not answer or original == answer:
        return answer or original
    return (
        f'Original workflow request:\n{original}\n\n'
        f'Clarification answers:\n{answer}'
    )


def _is_explicit_merged_request_context(value: Any) -> bool:
    """Recognize a caller-supplied original-request + clarification envelope."""
    text = str(value or '').strip().lower()
    return any(
        first in text and second in text
        for first, second in (
            ('original workflow request:', 'clarification answers:'),
            ('original request:', 'clarification answers:'),
            ('原始需求：', '补充：'),
            ('原始请求：', '澄清回答：'),
        )
    )


def _startup_clarification_guidance(runtime_policy: Any) -> str:
    fields = _runtime_clarification_fields(runtime_policy)
    if not fields:
        return ''
    from lazymind.chat.workflow.clarification import managed
    if managed(runtime_policy):
        return (
            'Startup fields are checked by the Host, without another model call. Call the trigger with '
            'only values explicitly supplied or unambiguously grounded in the current task and attachments. '
            'The trigger sends up to three missing required questions and stops automatically. Do not '
            'ask optional preferences or invent missing values. Reuse submitted answers; later batches '
            'ask only unresolved fields. Defaults and conditional applicability are package-owned. '
            'Do not call generic ask_user for startup fields; use the trigger. '
            'Preserve the original request and exact URLs and attachment names. Declared fields: '
            + json.dumps(fields, ensure_ascii=False, default=str)
        )
    return (
        'This Workflow declares startup_clarification_fields. Before calling its trigger, '
        'inspect the current request, relevant attachment names/content already available, '
        'and prior conversation turns. Treat a field as present only when it is explicit or '
        'unambiguously inferable. Do not ask for fields that are already present. If one or '
        'more fields declare ask_when, evaluate that condition against the request, attachments '
        'and conversation first. Exclude fields whose condition is false. Existing source '
        'attachments or supplied substantive source text satisfy material questions; never '
        'ask for those uploads again. A visual template alone is not source material. If one or '
        'more blocking fields remain, call ask_user with one to three unresolved decision dimensions, '
        'then stop without triggering the Workflow in that turn. Explain why each is needed. Include each '
        'declared field id in its question object. When useful, offer up to three real single-choice '
        'suggestions, or up to four multiple-choice suggestions when selections can coexist. '
        'Multiple permits one selection. For choice_policy=subset, choose only the most relevant declared '
        'choices, copy them verbatim, and never invent or rename an option. For '
        'choice_policy=fixed, use all declared choices verbatim. Otherwise declared choices are '
        'useful seeds, not a limit. In particular, for choice_policy=seed, any explicit '
        'free-form value in the request counts as present even when it is not one of the '
        'suggested choices; preserve it exactly and never ask that field again. In particular, '
        'never ask for it again merely because it is unlisted or differs from a suggestion. '
        'Treat numbers written as words or digits as equivalent '
        'explicit values when they are tied to the field, for example 六页 and 6页 both explicitly '
        'supply a slide count. Keep type=text only '
        'when responsible suggestions cannot be inferred. When a declared choice field has '
        'allow_other=false, pass allow_other=false in that ask_user question so the card does not '
        'append an Other option. Keep allow_other=true or omit it for other choice fields. '
        'When a declared field includes attachment metadata, copy that metadata verbatim into '
        'the matching ask_user question. The Chat card owns the upload button and sends the file '
        'as a normal conversation attachment; never ask the user to paste a filesystem path. '
        'On the answer turn, bind its declared attachment.material_id to the exact uploaded '
        'filename in trigger input_bindings for a single file. For multiple files, preserve '
        'EVERY exact attachment filename in request_context so the Workflow can resolve each '
        'through the conversation attachment reader. Do not stringify a filename list into '
        'a single file binding; omit that binding when multiple attachments are supplied. '
        'On the answer turn, never ask resolved fields again. Only a truly unresolved blocking field '
        'can require another batch. Combine the original request, all already-known '
        'fields, and the new answers into one concise request_context and pass that value to '
        'the trigger; an answer-only current_query must never replace the original request. '
        'If no field is missing, trigger immediately. Never ask for an upload unless the '
        'Workflow declares it as a required input or as the selected conditional attachment '
        'on a clarification field. Declared fields: '
        + json.dumps(fields, ensure_ascii=False, default=str)
    )


def workflow_activation_from_catalog_item(item: Dict[str, Any]) -> Dict[str, Any]:
    """Return the model-facing trigger metadata for one catalog item.

    Kept host-neutral so Codex and ChatAgent can share the same catalog contract:
    ``workflow_ref``, ``workflow_id``, ``name``, ``description``, ``when_to_use``,
    and revision fields when available.
    """
    workflow_id = _clean_workflow_text(item.get('workflow_id'))
    workflow_ref = _clean_workflow_text(item.get('workflow_ref'))
    if not workflow_id or not workflow_ref:
        return {}
    name = _clean_workflow_text(item.get('name')) or workflow_id
    description = _clean_workflow_text(item.get('description'))
    when_to_use = _clean_workflow_text(item.get('when_to_use'))
    runtime_policy = item.get('runtime') if isinstance(item.get('runtime'), dict) else {}
    clarification_guidance = _startup_clarification_guidance(runtime_policy)
    return {
        'workflow_ref': workflow_ref,
        'workflow_id': workflow_id,
        'revision_id': _clean_workflow_text(item.get('revision_id')),
        'tool_name': _workflow_trigger_tool_name(workflow_id),
        'tool_description': _clean_workflow_text(
            f'Start the executable Workflow "{name}" when it matches the user request. '
            f'Description: {description} When to use: {when_to_use} '
            f'{clarification_guidance}'
        ),
        'prompt': _clean_workflow_text(
            f'Workflow "{name}" ({workflow_ref}) is available. '
            f'Description: {description} When to use: {when_to_use} '
            f'{clarification_guidance}'
        ),
        'runtime': runtime_policy,
    }


def build_workflow_discovery_context(
    catalog: List[Dict[str, Any]],
    *,
    current_query: str = '',
) -> WorkflowDiscoveryContext:
    """Render available Workflow routing context and trigger metadata.

    This mirrors Skill discovery: the model gets names, descriptions, and
    when-to-use guidance before deciding whether any Workflow should run.
    """
    activations: List[Dict[str, Any]] = []
    items: List[Dict[str, Any]] = []
    used_names: set[str] = set()
    for item in catalog or []:
        if not isinstance(item, dict):
            continue
        activation = workflow_activation_from_catalog_item(item)
        if not activation:
            continue
        name = str(activation.get('tool_name') or '')
        if name in used_names:
            continue
        used_names.add(name)

        activations.append(activation)
        items.append({
            'workflow_ref': activation['workflow_ref'],
            'workflow_id': activation['workflow_id'],
            'name': _clean_workflow_text(item.get('name')) or activation['workflow_id'],
            'description': _clean_workflow_text(item.get('description')),
            'when_to_use': _clean_workflow_text(item.get('when_to_use')),
            'trigger_tool': name,
            'startup_clarification_fields': _runtime_clarification_fields(
                activation.get('runtime'),
            ),
        })
    if not items:
        return WorkflowDiscoveryContext([], '')
    prompt = (
        '## Available Workflow Catalog [AUTHORITATIVE]\n'
        'These are executable Workflows available in this conversation. Use this catalog the '
        'same way Skill descriptions are used: compare the current user request with each '
        'description and when_to_use before deciding. Call a trigger tool only when the '
        'Workflow is clearly appropriate, or when the user explicitly asks to run/open/start '
        'that Workflow. For a matching Workflow with startup_clarification_fields, inspect the '
        'request and prior conversation first. Follow each package startup policy. For Host-managed '
        'fields call its trigger with grounded values; the Host asks only unresolved required fields. '
        'Legacy packages may explicitly request their declared startup card. '
        'After the user answers, pass the trigger a merged request_context containing the original '
        'request plus the answers. Do not trigger a Workflow merely because it exists, and do not ask the '
        'user to list Workflows before making this routing decision. A triggered Workflow '
        'receives current_query as request_context/user_input; after a successful trigger, '
        'continue from returned ready_steps with advance_step until terminal, required input, '
        'explicit user boundary, or failure.\n'
        + json.dumps({
            'current_query': current_query,
            'workflows': items,
        }, ensure_ascii=False, default=str)
    )
    return WorkflowDiscoveryContext(activations, prompt)


def _trigger_input_contract(package: Dict[str, Any]):
    runtime = package.get('runtime') or (package.get('compiled_graph') or {}).get('runtime') or {}
    return runtime.get('trigger_inputs')


def _trigger_input_types(package: Dict[str, Any]) -> Dict[str, str]:
    inputs = workflow_package_input_types(package)
    declared = _trigger_input_contract(package)
    return inputs if declared is None else {key: value for key, value in inputs.items() if key in declared}


def _normalize_trigger_input_bindings(value: Optional[Union[Dict[str, str], str]]) -> Dict[str, str]:
    """Decode a model's JSON-encoded bindings without relaxing the string-map contract."""
    if value is None:
        return {}
    if isinstance(value, str):
        try:
            value = json.loads(value)
        except ValueError as exc:
            raise WorkflowClientError(
                'WORKFLOW_INPUT_INVALID', 'input_bindings must be an object or a JSON-encoded object.',
            ) from exc
    if not isinstance(value, dict) or any(
        not isinstance(key, str) or not isinstance(item, str) for key, item in value.items()
    ):
        raise WorkflowClientError(
            'WORKFLOW_INPUT_INVALID', 'input_bindings must map material IDs to string values.',
        )
    return value


def _workflow_trigger_tools(
    activations: List[Dict[str, Any]], allowed_refs: set[str], current_query: str = '',
    conversation_id: str = '', session_holder: Optional[Dict[str, Any]] = None,
    conversation_history: Optional[List[Dict[str, Any]]] = None,
    workflow_mode: str = 'dynamic',
    clarification: Optional[Dict[str, Any]] = None,
) -> List[Any]:
    """Bind backend-prepared activations to public package reads."""
    attachments_available = _conversation_has_attachments()
    candidates = [
        item for item in activations
        if not allowed_refs or str(item.get('workflow_ref') or '') in allowed_refs
    ]
    tools: List[Any] = []
    used_names: set[str] = set()
    for item in candidates:
        workflow_id = str(item.get('workflow_id') or '').strip()
        workflow_ref = str(item.get('workflow_ref') or '').strip()
        revision_id = str(item.get('revision_id') or '').strip()
        if not workflow_id or not workflow_ref:
            continue
        name = str(item.get('tool_name') or '').strip()
        if not name.startswith('trigger_') or not name.endswith('_workflow') or not name.isidentifier():
            continue
        if name in used_names:
            continue
        used_names.add(name)

        package_hint: Dict[str, Any] = {}
        input_types_hint: Dict[str, str] = {
            str(field['id']): 'text'
            for field in _runtime_clarification_fields(item.get('runtime'))
        }
        # An explicitly selected Workflow can afford one package read before
        # the model call so its complete typed input contract is visible. In
        # discovery mode avoid N package reads: declared clarification fields
        # provide the scalar trigger shape, and the selected package is read
        # authoritatively only when its trigger is called.
        if allowed_refs:
            try:
                package_hint = _client().get_workflow(workflow_id, revision_id).result
                input_types_hint = _trigger_input_types(package_hint)
            except Exception as exc:
                LOG.debug('Could not preload Workflow %s input contract: %s', workflow_id, exc)
        for field in _runtime_clarification_fields(item.get('runtime')):
            input_types_hint.setdefault(str(field['id']), 'text')

        # A resumed, owned card already has a task identity and structured
        # answers. Resolve its next batch without another planning-model turn.
        from lazymind.chat.workflow.clarification import managed, resolve_fields, startup_card, startup_metadata
        selected_runtime = (package_hint.get('runtime')
                            or (package_hint.get('compiled_graph') or {}).get('runtime') or item.get('runtime'))
        if allowed_refs and package_hint and managed(selected_runtime) and session_holder is not None:
            session_holder['startup_managed'] = True
            known, missing = resolve_fields(selected_runtime, current_query,
                                            previous=(clarification or {}).get('values'))
            direct_opt_in = (not attachments_available and '模板' not in current_query
                             and 'template' not in current_query.lower()
                             and all(field.get('resolution') == 'explicit' for field in missing))
            if missing and (clarification or direct_opt_in):
                session_holder['pending_ask'] = startup_card(missing, startup_metadata(
                    workflow_ref, str(package_hint.get('revision_id') or revision_id),
                    current_query, known, clarification,
                ))

        def make_trigger(
            bound_id: str, bound_ref: str, bound_revision: str, bound_query: str,
            bound_clarification_answer: bool, bound_package: Optional[Dict[str, Any]],
            supports_scalar_bindings: bool, bound_runtime: Any,
            bound_clarification: Optional[Dict[str, Any]],
        ) -> Any:
            def run_trigger(
                input_bindings: Optional[Union[Dict[str, str], str]] = None,
                request_context: Optional[str] = None,
            ) -> Dict[str, Any]:
                input_bindings = _normalize_trigger_input_bindings(input_bindings)
                # The Host-composed query is authoritative. A clearly marked
                # original-request + clarification envelope remains supported
                # for callers that already merged those two sections. An
                # ordinary model-authored paraphrase must never erase action
                # words such as "search, then edit" before the first step.
                supplied_context = str(request_context or '').strip()
                effective_context = (
                    bound_query
                    if bound_clarification_answer
                    else supplied_context
                    if _is_explicit_merged_request_context(supplied_context)
                    else bound_query or supplied_context
                )
                if bound_id == 'paper-writing':
                    from lazymind.document_tools.paper.analysis.attachments import workflow_input_bindings
                    input_bindings = workflow_input_bindings(input_bindings, effective_context)
                existing_session_id = str(
                    (session_holder or {}).get('session_id') or '',
                ).strip()
                if existing_session_id:
                    previous = (session_holder or {}).get('launch_bindings', {})
                    if ((session_holder or {}).get('workflow_id', bound_id) != bound_id or any(
                        key in previous and previous[key] != value.strip()
                        for key, value in input_bindings.items() if value.strip()
                    )):
                        raise WorkflowClientError(
                            'WORKFLOW_INPUT_BINDINGS_CONFLICT',
                            'A Workflow has already started with different inputs; '
                            'use explicit input revision recovery.',
                        )
                if existing_session_id and not input_bindings:
                    # Repeated launches without new bindings are idempotent;
                    # calls that supply inputs must continue through recovery.
                    return {
                        'status': 'prepared',
                        'outcome': 'already_initialized',
                        'reason': 'The selected Workflow is already initialized.',
                        'workflow_ref': bound_ref,
                        'workflow_id': bound_id,
                        'revision_id': bound_revision,
                        'request_context': effective_context,
                        'session_id': existing_session_id,
                        'next_action': {
                            'tool': 'get_ready_steps',
                            'instruction': 'Read the current Ready frontier and continue execution.',
                        },
                    }
                client = _client()
                package = bound_package or client.get_workflow(bound_id, bound_revision).result
                input_types = _trigger_input_types(package)
                runtime = (package.get('runtime') or (package.get('compiled_graph') or {}).get('runtime')
                           or bound_runtime)
                clarification_ids = {str(field['id']) for field in _runtime_clarification_fields(runtime)}
                has_contract = (_trigger_input_contract(package) is not None
                                or isinstance((package.get('compiled_graph') or {}).get('material_producers'), dict))
                if has_contract and set(input_bindings) - input_types.keys() - clarification_ids:
                    raise WorkflowClientError(
                        'WORKFLOW_INPUT_NOT_EXPOSED', 'Use only inputs advertised by this package.',
                    )
                from lazymind.chat.workflow.clarification import (
                    managed, resolve_fields, startup_card, startup_metadata,
                )
                semantic_values = {}
                if managed(runtime):
                    previous = (bound_clarification or {}).get('values') or {}
                    semantic_values, missing = resolve_fields(runtime, bound_query, input_bindings, previous)
                    if missing:
                        from lazyllm.tools.agent.base import _write_agent_data
                        card = startup_card(missing, startup_metadata(
                            bound_ref, str(package.get('revision_id') or bound_revision), effective_context,
                            semantic_values, bound_clarification,
                        ))
                        _write_agent_data('ask_pending', **card)
                        return {'status': 'needs_input', 'missing_clarification_fields': [f['id'] for f in missing],
                                '_agent_control': {'stop': True, 'reason': 'awaiting_user',
                                                   'final_text': '请补充问题卡片中的必要信息后继续。'}}
                    # Never append a second, model-guessed copy of fields after
                    # the resolver's canonical values (especially explicit opt-ins).
                    input_bindings = {key: value for key, value in input_bindings.items()
                                      if key not in clarification_ids or input_types.get(key) in {'file', 'image'}}
                    # Import defaults and submitted scalars through the existing
                    # typed material transport. Lists remain JSON, never filenames.
                    for key, value in semantic_values.items():
                        if input_types.get(key) in {'text', 'json'}:
                            input_bindings[key] = (value if isinstance(value, str)
                                                   else json.dumps(value, ensure_ascii=False))
                    effective_context += '\n\nConfirmed workflow parameters:\n' + json.dumps(
                        semantic_values, ensure_ascii=False,
                    )
                    if session_holder is not None:
                        session_holder['request_context'] = effective_context
                clarification_answers = {
                    key: value for key, value in input_bindings.items()
                    if key not in input_types and key in clarification_ids and value.strip()
                }
                input_bindings = {key: value for key, value in input_bindings.items()
                                  if key in input_types or key not in clarification_ids}
                if clarification_answers:
                    effective_context += '\n\nWorkflow clarification answers:\n' + json.dumps(
                        clarification_answers, ensure_ascii=False,
                    )
                    if session_holder is not None:
                        session_holder['request_context'] = effective_context
                resolved_bindings: Dict[str, Any] = {}
                for material_id, attachment_ref in (input_bindings or {}).items():
                    binding = str(attachment_ref or '').strip()
                    if not binding:
                        # Blank model placeholders are absent bindings. Core
                        # owns required-input checks; optional fields must keep
                        # their Workflow defaults instead of blocking startup.
                        continue
                    material_type = input_types.get(str(material_id), '')
                    if material_type in {'text', 'json'}:
                        resolved_bindings[material_id] = _import_text_binding(
                            str(material_id), binding,
                        )
                        continue
                    # Attachment resolution pulls in the full attachment/vision
                    # stack. Keep scalar-only Workflow launches independent of
                    # those optional dependencies and load it only for a file
                    # (or legacy untyped) material.
                    path, error = _resolve_workflow_attachment(binding)
                    if material_type in {'file', 'image'} and (error or not path):
                        raise WorkflowClientError(
                            'ATTACHMENT_NOT_SELECTED',
                            error or 'The referenced conversation attachment was not found.',
                        )
                    resolved_bindings[material_id] = (
                        _import_attachment(path)
                        if path and not error and material_type in {'', 'file', 'image'}
                        else _import_text_binding(str(material_id), binding)
                    )
                model_bindings = resolved_bindings
                if bound_id == 'paper-writing':
                    from lazymind.document_tools.paper.analysis.attachments import describe_input_bindings
                    model_bindings = describe_input_bindings(resolved_bindings, input_bindings)
                toolkit = HostWorkflowToolkit(
                    _client,
                    allowed_workflow_ids=[bound_id],
                    origin_ref=conversation_id,
                )
                if existing_session_id:
                    # Recovery may supply inputs after an earlier empty launch.
                    # Reuse the session, but never silently replace its inputs.
                    existing = {
                        str(item.get('material_id') or item.get('MaterialID')): item
                        for item in (toolkit.list_workflow_inputs(existing_session_id).get('inputs') or [])
                    }
                    for material_id, resource in resolved_bindings.items():
                        previous = existing.get(material_id)
                        previous_hash = (previous.get('content_hash') or previous.get(
                            'ContentHash')) if previous else None
                        if previous and previous_hash != resource.get('content_hash'):
                            raise WorkflowClientError(
                                'WORKFLOW_INPUT_CONFLICT',
                                f'{material_id} is already bound to different content; '
                                'use explicit input revision recovery.',
                            )
                    for material_id, resource in resolved_bindings.items():
                        if material_id not in existing:
                            toolkit.bind_workflow_input(existing_session_id, material_id, resource)
                    prepared = {'session_id': existing_session_id}
                else:
                    prepared = toolkit.prepare_workflow(
                        bound_id, input_bindings=resolved_bindings,
                        request_context=effective_context,
                        workflow_mode=workflow_mode,
                        **({'clarification_values': semantic_values,
                            'revision_id': str(package.get('revision_id') or bound_revision)}
                           if managed(runtime) else {}),
                    )
                session_id = str(prepared.get('session_id') or '')
                if not session_id:
                    if prepared.get('questions'):
                        from lazyllm.tools.agent.base import _write_agent_data
                        from lazymind.chat.engine.tools.ask_user import question_card_payload
                        card = question_card_payload(prepared['questions'], title='补充必要信息', workflow=startup_metadata(
                            bound_ref, str(prepared.get('workflow_revision') or bound_revision), effective_context,
                            prepared.get('clarification_values') or semantic_values, bound_clarification,
                        ))
                        _write_agent_data('ask_pending', **card)
                        return {**prepared, '_agent_control': {
                            'stop': True, 'reason': 'awaiting_user', 'final_text': '请补充问题卡片中的必要信息后继续。',
                        }}
                    return {
                        **prepared,
                        'status': 'waiting',
                        'outcome': 'waiting_for_input',
                        'reason': 'Workflow preparation requires additional input before a Session can be created.',
                        'workflow_ref': bound_ref,
                        'workflow_id': bound_id,
                        'revision_id': str(package.get('revision_id') or bound_revision),
                        'request_context': effective_context,
                        'input_bindings': model_bindings,
                    }
                if session_holder is not None:
                    session_holder['session_id'] = session_id
                    session_holder['workflow_id'] = bound_id
                    session_holder['launch_bindings'] = {
                        key: value.strip() for key, value in input_bindings.items() if value.strip()
                    }
                    session_holder['request_context'] = effective_context
                state = client.get_state(session_id)
                projection = state.get('projection') if isinstance(state.get('projection'), dict) else state

                def step_ids(values: Any) -> List[str]:
                    result: List[str] = []
                    for value in values or []:
                        step_id = value.get('step_id') if isinstance(value, dict) else value
                        step_id = str(step_id or '').strip()
                        if step_id:
                            result.append(step_id)
                    return result

                ready_steps = step_ids(
                    projection.get('ready') or projection.get('ready_steps')
                    or prepared.get('ready_steps')
                )
                reachable_steps = step_ids(
                    projection.get('reachable') or projection.get('reachable_steps')
                    or ready_steps
                )
                blocked_steps = step_ids(
                    projection.get('blocked') or projection.get('blocked_steps')
                )
                retryable_steps = step_ids(
                    projection.get('retryable') or projection.get('retryable_steps')
                )
                rewindable_steps = step_ids(
                    projection.get('rewindable') or projection.get('rewindable_steps')
                )
                continue_steps = step_ids(
                    projection.get('continue') or projection.get('continue_steps')
                )
                return {
                    **prepared,
                    'status': 'prepared',
                    'outcome': 'ready' if ready_steps else 'waiting',
                    'reason': (
                        'Workflow session was initialized; select an exact Ready step and call advance_step.'
                        if ready_steps else
                        'Workflow session was initialized but no step is currently Ready.'
                    ),
                    'workflow_ref': bound_ref,
                    'workflow_id': bound_id,
                    'revision_id': str(package.get('revision_id') or bound_revision),
                    'request_context': effective_context,
                    'input_bindings': model_bindings,
                    'session_id': session_id,
                    'state_version': int(state.get('state_version') or prepared.get('state_version') or 0),
                    'projection': projection,
                    'reachable_steps': reachable_steps,
                    'ready_steps': ready_steps,
                    'blocked_steps': blocked_steps,
                    'retryable_steps': retryable_steps,
                    'rewindable_steps': rewindable_steps,
                    'continue_steps': continue_steps,
                    'next_action': {
                        'tool': 'advance_step',
                        'instruction': (
                            'Call advance_step using only exact members of ready_steps, '
                            'retryable_steps, rewindable_steps, or continue_steps; Runtime resolves the operation.'
                        ),
                    },
                }
            if attachments_available:
                @fc_register(host_file='NONE')
                def bound_trigger(
                    input_bindings: Optional[Union[Dict[str, str], str]] = None,
                    request_context: Optional[str] = None,
                ) -> Dict[str, Any]:
                    """Initialize with optional attachments and a merged clarified request."""
                    return run_trigger(input_bindings, request_context)
            elif supports_scalar_bindings:
                @fc_register(host_file='NONE')
                def bound_trigger(
                    input_bindings: Optional[Union[Dict[str, str], str]] = None,
                    request_context: Optional[str] = None,
                ) -> Dict[str, Any]:
                    """Initialize with scalar bindings and a merged clarified request."""
                    return run_trigger(input_bindings, request_context)
            else:
                @fc_register(host_file='NONE')
                def bound_trigger(request_context: Optional[str] = None) -> Dict[str, Any]:
                    """Initialize with the current or merged clarified request context."""
                    return run_trigger(request_context=request_context)
            return bound_trigger

        trigger_query = _merge_startup_clarification_context(
            current_query,
            conversation_history,
            item.get('runtime'),
        )
        trigger_workflow = make_trigger(
            workflow_id,
            workflow_ref,
            revision_id,
            trigger_query,
            trigger_query != str(current_query or '').strip(),
            package_hint,
            any(kind in {'text', 'json'} for kind in input_types_hint.values()),
            item.get('runtime'),
            clarification if (clarification or {}).get('workflow_ref') == workflow_ref else None,
        )

        trigger_workflow.__name__ = name
        description = str(item.get('tool_description') or '').strip()
        input_contract = (
            (' Exact external material IDs and types: ' if package_hint else
             ' Startup fields (the package resolves material inputs versus clarification answers): ')
            + ', '.join(
                f'{material_id} ({material_type})'
                for material_id, material_type in sorted(input_types_hint.items())
            )
            + '. Pass input_bindings as an object mapping these exact IDs to string values, '
            'for example {"material_id": "value"}.'
            if input_types_hint else ''
        )
        attachment_guidance = (
            ' File/image input_bindings use exact filenames listed in conversation attachments; '
            'text/json input_bindings use literal values, never filesystem paths.'
            if attachments_available else
            ' No user attachments are available: do not bind file materials, but pass literal '
            'values for required text/json input_bindings.'
        )
        if workflow_id == 'paper-writing':
            if attachments_available:
                attachment_guidance = (
                    ' For paper analysis or writing, paper_input accepts the exact JSON attachment filename. '
                    'The Host reads the complete original file. Never transcribe, summarize, repair, '
                    'or embed the uploaded JSON in tool arguments; pass only its filename, or omit '
                    'paper_input when the request unambiguously selects one JSON attachment.'
                    ' For research-topic entry and drafting, Markdown background materials are imported '
                    'directly from conversation attachments. Omit paper_input for these Markdown materials. '
                    'Do not read or transcribe them before launching the research-topic step.'
                )
        trigger_workflow.__doc__ = (
            'Initialize this Workflow. The only argument names are input_bindings and request_context. '
            'Never pass input_type, inputBindings, step_id, or session_id. '
            + description + input_contract + attachment_guidance
            + ' Omit inputs whose values were not supplied; do not invent values or '
            'fill absent inputs with empty placeholders. Pass explicitly supplied '
            'optional scalar preferences as well as required inputs.'
            + ' If this turn follows startup clarification, request_context must merge the '
            'original request with every clarification answer; otherwise omit it. The Host '
            'forwards the exact current_query and ignores model-authored paraphrases.'
            '\n\nArgs:\n'
            '    input_bindings: Object mapping external material IDs to string values. '
            'The exact argument name is input_bindings (snake_case), not inputBindings. '
            + ('For uploaded paper JSON, pass its exact attachment filename; the Host reads it, preserving all fields. '
               'Literal JSON remains supported for programmatic inputs. '
               if workflow_id == 'paper-writing' and attachments_available else
               'For JSON materials, the value is the complete JSON text, preserving all fields. ')
            + 'Example call: {"input_bindings": {"material_id": "complete value"}}. '
            'On argument validation errors, correct the field name and retain the input content; do not drop bindings.\n'
            '    request_context: Optional merged original request and clarification answers.\n'
        )
        tools.append(trigger_workflow)
    return tools


def _is_bound_workflow_trigger(name: str) -> bool:
    return name.startswith('trigger_') and name.endswith('_workflow')


def _workflow_tool_group(name: str, desc: str, tools: List[Any], *, lazy: bool = True) -> Dict[str, Any]:
    return {
        'name': name,
        'desc': desc,
        'lazy': lazy,
        'tools': tools,
    }


def resolve_workflow_injection(
    workflow_context: Optional[Dict[str, Any]],
    conversation_id: str = '',
    current_query: str = '',
    workflow_catalog: Optional[List[Dict[str, Any]]] = None,
    disabled_builtin_workflows: Optional[List[str]] = None,
    allowed_workflow_refs: Optional[List[str]] = None,
    workflow_activations: Optional[List[Dict[str, Any]]] = None,
    conversation_history: Optional[List[Dict[str, Any]]] = None,
    clarification: Optional[Dict[str, Any]] = None,
) -> WorkflowAgentContribution:
    """Map public Workflow APIs to LazyMind Chat tools; no Runtime decisions live here."""
    cfg = _agentic_config()
    if not cfg.get('enable_workflow', True):
        return WorkflowAgentContribution([], [], {}, '')

    context = workflow_context if isinstance(workflow_context, dict) else {}
    if clarification and not context.get('session_id'):
        ref = str(clarification.get('workflow_ref') or '')
        if any(item.get('workflow_ref') == ref for item in (workflow_catalog or [])):
            allowed_workflow_refs = [ref]
            pinned_revision = str(clarification.get('revision_id') or '')
            if pinned_revision:
                workflow_activations = [{**item, 'revision_id': pinned_revision} if item.get('workflow_ref') == ref
                                        else item for item in (workflow_activations or [])]
            current_query = (str(clarification.get('original_request') or current_query)
                             + '\n\nClarification answers:\n' + current_query)
    session_id = str(context.get('session_id') or '')
    workflow_id = str(context.get('workflow_id') or context.get('workflow_ref') or '')
    revision_id = str(context.get('revision_id') or '')
    mode = 'auto' if context.get('workflow_mode') == 'auto' else 'dynamic'
    # Keep the model-facing advance tools narrow (step_id only), while restoring
    # the pre-refactor behaviour where every Workflow step receives the user's
    # exact current instruction. This is essential for completed-session edits
    # such as "change this slide"; request_context only contains the cold-start
    # request and must not be reused for a later local edit.
    patch: Dict[str, Any] = {
        'workflow_mode': mode,
        'workflow_current_query': current_query,
    }

    catalog = workflow_catalog or []
    allowed_refs = {
        str(value).strip() for value in (allowed_workflow_refs or []) if str(value).strip()
    }
    runtime_policy = _selected_runtime_policy(context, catalog, allowed_refs)
    allowed_items = [
        item for item in catalog
        if str(item.get('workflow_ref') or '') in allowed_refs
    ]
    allowed_ids = [
        str(item.get('workflow_id') or '').strip() for item in allowed_items
        if str(item.get('workflow_id') or '').strip()
    ]
    if not allowed_refs and not session_id:
        allowed_ids.extend(
            str(item.get('workflow_id') or '').strip() for item in catalog
            if isinstance(item, dict) and str(item.get('workflow_id') or '').strip()
        )
    for ref in allowed_refs:
        if ref.startswith('builtin:'):
            allowed_ids.append(ref.removeprefix('builtin:'))
    allowed_ids = list(dict.fromkeys(allowed_ids))

    activations = workflow_activations or []
    if allowed_refs and runtime_policy:
        activations = [
            {
                **item,
                **(
                    {'runtime': runtime_policy}
                    if isinstance(item, dict)
                    and str(item.get('workflow_ref') or '') in allowed_refs
                    and not isinstance(item.get('runtime'), dict)
                    else {}
                ),
            }
            for item in activations
        ]
    discovery_context = WorkflowDiscoveryContext([], '')
    if not allowed_refs and not session_id:
        discovery_context = build_workflow_discovery_context(
            catalog, current_query=current_query,
        )
        activation_names = {
            str(item.get('tool_name') or '') for item in activations if isinstance(item, dict)
        }
        activations = [
            *activations,
            *[
                item for item in discovery_context.activations
                if str(item.get('tool_name') or '') not in activation_names
            ],
        ]
    session_holder: Dict[str, Any] = {'session_id': session_id, 'workflow_id': workflow_id}

    def bind_successor(result: Dict[str, Any]) -> None:
        target = str(result.get('session_id') or '')
        if (not target or result.get('source_session_id') != session_holder['session_id']
                or result.get('workflow_id') != workflow_id
                or (conversation_id and result.get('conversation_id') != conversation_id)
                or (revision_id and result.get('workflow_revision_id') != revision_id)):
            raise WorkflowClientError(
                'WORKFLOW_SESSION_HANDOFF_INVALID',
                'Successor must preserve the bound conversation and package revision.',
            )
        # Authorize and refresh through the same SDK before changing the turn binding.
        state = _client().get_state(target)
        if state.get('session_id') != target:
            raise WorkflowClientError('WORKFLOW_SESSION_HANDOFF_INVALID', 'Successor session could not be verified.')
        session_holder['session_id'] = target
        _agentic_config()['workflow_session_id'] = target

    @fc_register(host_file='NONE')
    def selected_session_id() -> str:
        return session_holder.get('session_id', '')

    @fc_register(host_file='NONE')
    def selected_request_context() -> str:
        return session_holder.get('request_context', '')

    trigger_tools = _workflow_trigger_tools(
        activations, allowed_refs, current_query, conversation_id, session_holder,
        conversation_history, mode, clarification,
    )
    if session_id:
        # Recovery in a later Chat turn must also be able to supply missing
        # inputs. Bind this capability to the existing session's pinned package,
        # never to another activation selected in the same request.
        repair_tools = _workflow_trigger_tools([{
            'workflow_id': workflow_id,
            'workflow_ref': str(context.get('workflow_ref') or f'builtin:{workflow_id}'),
            'revision_id': revision_id,
            'tool_name': 'trigger_existing_workflow',
            'tool_description': ('Supply missing inputs to the existing Workflow Session; '
                                 'conflicting inputs are rejected.'),
        }], {str(context.get('workflow_ref') or f'builtin:{workflow_id}')},
            current_query, conversation_id, session_holder, conversation_history, mode)
        for tool in repair_tools:
            tool.__name__ = 'supply_workflow_inputs'
        trigger_tools = repair_tools
    toolkit = HostWorkflowToolkit(
        _client, allowed_workflow_ids=allowed_ids, origin_ref=conversation_id,
    )
    candidate_tools = [
        *trigger_tools,
        *toolkit.tools(),
    ]
    projection = _state(session_id) if session_id else {}
    tools = AgentWorkflowToolProjection(
        session_id=session_id,
        session_status=str(projection.get('status') or context.get('status') or ''),
    ).expose(candidate_tools)
    execution_tools = {
        'workflow_connection_status', 'get_workflow', 'get_workflow_state',
        'get_ready_steps', 'advance_step',
        'list_workflow_inputs', 'get_workflow_command',
        'list_artifacts', 'read_artifact', 'patch_artifact', 'delete_artifact',
        'reorder_artifact_pages', 'patch_artifact_text',
        'supply_workflow_inputs',
    }
    if allowed_refs or session_id:
        tools = [
            tool for tool in tools
            if _is_bound_workflow_trigger(str(getattr(tool, '__name__', '')))
            or str(getattr(tool, '__name__', '')) in execution_tools
        ]
    if allowed_refs and not session_id:
        # A ChatAgent tool set is fixed for the duration of one model turn. Expose
        # Host-bound Session tools up front and resolve their Session id only after
        # trigger_<workflow> creates it, so trigger -> advance works in the same turn.
        @fc_register(host_file='NONE')
        def launch_user_input() -> str:
            return session_holder.get('request_context', '')

        handoff = _handoff_tool(
            selected_session_id,
            user_input=launch_user_input,
        )
        tools = [
            *[tool for tool in tools if _is_bound_workflow_trigger(tool.__name__)],
            *_safe_session_tools(
                toolkit,
                selected_session_id,
                user_input=launch_user_input,
                workflow_context=session_holder,
            ),
            handoff,
        ]
    elif not session_id:
        trigger_entry_tools = [tool for tool in tools if _is_bound_workflow_trigger(tool.__name__)]
        authoring_tools = _safe_authoring_tools(toolkit)
        authoring_group = _workflow_tool_group(
            'workflow_authoring',
            (
                'Create, edit, validate, diagnose, or publish Workflow drafts. '
                'Use only when the user explicitly asks to author or modify a Workflow.'
            ),
            authoring_tools,
        )
        if trigger_entry_tools:
            # Register both phases once; their schemas are exposed according to
            # the live Session state below, without another discovery round.
            tools = [
                *trigger_entry_tools,
                *_safe_session_tools(
                    toolkit,
                    selected_session_id,
                    user_input=selected_request_context,
                    workflow_context=session_holder,
                ),
                _handoff_tool(
                    selected_session_id,
                    user_input=selected_request_context,
                ),
                authoring_group,
            ]
        else:
            tools = [authoring_group]
    if not session_id and trigger_tools:
        # LazyLLM refreshes conditional tool schemas before each model round.
        # Reading state must never create a Session; only a successful trigger
        # can switch from launch capabilities to execution capabilities.
        launch_tools = [tool for tool in tools if not isinstance(tool, dict)
                        and _is_bound_workflow_trigger(tool.__name__)]
        execution_tools = [tool for tool in tools if not isinstance(tool, dict)
                           and not _is_bound_workflow_trigger(tool.__name__)]
        tools = [
            {**_workflow_tool_group('workflow_launch', 'Initialize the selected Workflow.',
                                    launch_tools, lazy=False),
             'key_source': lambda: not selected_session_id()},
            {**_workflow_tool_group('workflow_session', 'Operate the initialized Workflow Session.',
                                    execution_tools, lazy=False),
             'key_source': selected_session_id},
            *[tool for tool in tools if isinstance(tool, dict)],
        ]
    if session_id:
        tools = [*trigger_tools, *_safe_session_tools(
            toolkit, selected_session_id, workflow_context=session_holder)]
        patch.update({
            'workflow_id': workflow_id,
            'workflow_session_id': session_id,
            'workflow_step': context.get('current_step') or '',
            'workflow_ref': context.get('workflow_ref') or '',
            'revision_id': revision_id,
            'focused_tab': context.get('focused_tab') or '',
            'focused_sort_order': context.get('focused_sort_order'),
        })
        tools.append(_handoff_tool(selected_session_id))
        session_projection = (
            projection.get('projection')
            if isinstance(projection.get('projection'), dict) else {}
        )
        completed_followup = ''
        completed_actions = []
        if session_projection.get('completed'):
            completed_actions = load_completed_actions(
                toolkit, session_id, runtime_policy,
                register=_register_host_file('NONE' if _trusted_workflow_toolkit(toolkit) else 'UNDECLARED'),
            )
            tools.extend(action.tool for action in completed_actions)
            completed_followup = (
                'A completed Session is not immutable. When the current user query asks to '
                'revise, delete, fix, or regenerate an existing Workflow output, first use '
                'patch_artifact_text for a text/Markdown-only edit or reorder_artifact_pages for '
                'a pure permutation; neither operation rewinds steps. For other edits, do not '
                'write replacement content in chat and do not use generic file/artifact '
                'tools. Call get_ready_steps, select the matching exact rewindable_steps or continue_steps '
                'target, and call advance_step so Runtime starts a new step attempt and '
                'publishes a new Workflow artifact revision. '
            )
            completed_edit_routing = str(
                runtime_policy.get('completed_edit_routing') or ''
            ).strip()
            if completed_edit_routing:
                completed_followup += (
                    'This Workflow declares semantic routing for completed-output edits. '
                    'Match the current user query and the existing Workflow state against '
                    'these rules before using the fallback edit step. Select the exact '
                    'matching rewindable step returned by get_ready_steps:\n'
                    + completed_edit_routing
                    + '\n'
                )
            completed_edit_step = str(runtime_policy.get('completed_edit_step') or '').strip()
            if completed_edit_step:
                if completed_edit_routing:
                    completed_followup += (
                        'This Workflow declares that requests to modify, repair, delete, or '
                        'regenerate completed output which do not match a semantic routing rule '
                        f'map to the fallback {completed_edit_step!r} step. '
                        f'If {completed_edit_step!r} is present in rewindable_steps, you MUST '
                        'call it only when no semantic routing rule matched. That step '
                        'owns the existing artifacts and publishes their new revisions; never '
                        'paste replacement content into chat as a substitute. '
                    )
                else:
                    completed_followup += (
                        'This Workflow declares that requests to modify, repair, delete, or '
                        f'regenerate completed output map to the {completed_edit_step!r} step. '
                        f'If {completed_edit_step!r} is present in rewindable_steps, you MUST '
                        f'call advance_step(step_ids=[{completed_edit_step!r}]) now. That step '
                        'owns the existing artifacts and publishes their new revisions; never '
                        'paste replacement content into chat as a substitute. '
                    )
        if completed_actions:
            completed_followup = (
                'This Workflow declares scoped completed-output actions. Their declared '
                'routing takes precedence over fallback step routing:\n'
                + '\n'.join(action.routing for action in completed_actions)
                + '\n' + completed_followup
            )
        runtime_context = (
            '## Workflow Runtime [AUTHORITATIVE]\n'
            + 'The Host owns session/version concurrency fields. Never ask the user for '
            + 'state_version or expected_state_version. '
            + _SERIAL_WORKFLOW_EXECUTION_POLICY
            + _WORKFLOW_EDIT_SCOPE_POLICY
            + _USER_BOUNDARY_HANDOFF_POLICY
            + 'If a Workflow tool returns user_notice, explicitly relay that notice to the '
            + 'user. advance_step waits for '
            + 'terminal execution. A failed result never means success and never permits a '
            + 'downstream advance. You may decide to retry only an exact retryable_steps ID; '
            + 'When configuration is completed after a post-step capability check blocked, '
            + 'retry that exact step: Runtime rechecks configuration using its saved analysis and route '
            + 'without rerunning the analysis. Then follow the returned ready_steps. '
            + 'Runtime enforces the finite AI automatic-retry budget. User-requested retries '
            + 'remain available and do not consume that budget. After step_succeeded, continue in the '
            + 'same turn using only the returned ready_steps until the Workflow is terminal, '
            + 'requires user input, reaches an explicit user boundary, or a step fails. '
            + 'In Human Approval chat mode, approval is for a step result after that step runs; '
            + 'it is not permission to start the step. Decide approval checkpoints by priority: '
            + 'first obey explicit user instructions in the current request; when absent, use '
            + 'the effective requires_approval flag from ready_step_details or projection.nodes[step_id]. '
            + 'Absent an explicit user boundary, requires_approval=false overrides a package '
            + 'mode=human: use advance_step and continue. '
            + 'Only when that flag is absent, fall back to the step mode/default_approval. '
            + 'mode=human/default_approval=required '
            + 'means execute that Ready step with advance_step_and_hand_off so the Host stops '
            + 'after execution for human review of its outputs. mode=auto/default_approval=not_required '
            + 'means execute with advance_step and continue unless an explicit user boundary '
            + 'requires advance_step_and_hand_off. Never ask whether to execute a Ready '
            + 'step merely because it requires approval; the approval checkpoint belongs to its result. '
            + 'Do not replace continued execution with a promise that later steps will run. '
            + completed_followup
            + ('\nDeclared clarification fields remain available through ask_user. '
               'Ask for unanswered fields before continuing; a default input binding is not a user answer. '
               'Preserve explicit answers and never overwrite existing input bindings silently.\n'
               + json.dumps(_runtime_clarification_fields(runtime_policy), ensure_ascii=False)
               if _runtime_clarification_fields(runtime_policy) else '')
            + '\n'
            + 'Artifact-mutation guard: when the user asks to change an Artifact already '
            + ('owned by this Workflow, the declared scoped completed-output actions '
               'are an authorized exception to this guard within their declared routing. '
               'For other changes to artifacts ' if completed_actions else '')
            + 'owned by this active Workflow (except scoped text edits through patch_artifact_text '
            + 'and pure page permutations through reorder_artifact_pages), including inserting, replacing, moving, or '
            + 'deleting an image in the current document, rerun the earliest owning Workflow '
            + 'step. Do not bypass the Workflow with generic file, image, artifact, or SubAgent '
            + 'tools; media acquisition belongs inside the owning step.\n'
            + json.dumps(projection, ensure_ascii=False, default=str)
        )
        return WorkflowAgentContribution(
            # advance_step is synchronous: its terminal result and refreshed
            # Ready frontier must be returned to the same ChatAgent turn so a
            # user-requested continuous run can keep advancing. Handoff stops
            # through its accepted structured result, never merely by tool name.
            tools, [action.tool.__name__ for action in completed_actions],
            patch, runtime_context, runtime_policy, bind_successor,
        )

    del disabled_builtin_workflows
    selection_context = ''
    if allowed_refs:
        activation_prompts = [
            str(item.get('prompt') or '').strip() for item in activations
            if str(item.get('workflow_ref') or '') in allowed_refs
            and str(item.get('prompt') or '').strip()
        ]
        clarification_guidance = _startup_clarification_guidance(runtime_policy)
        entry_instruction = (
            clarification_guidance + ' '
            if clarification_guidance else
            'No startup clarification fields are declared, so call the bound trigger now. '
        )
        selection_context = (
            '## Explicit Workflow Selection [AUTHORITATIVE]\n'
            + '\n'.join(activation_prompts) + '\n'
            + 'A Workflow is an executable, versioned procedure, not a document to search, '
            + 'summarize, or merely describe. The @workflow mention means the user explicitly '
            + 'selected and authorized this exact procedure. '
            + _SERIAL_WORKFLOW_EXECUTION_POLICY
            + _WORKFLOW_EDIT_SCOPE_POLICY
            + _USER_BOUNDARY_HANDOFF_POLICY
            + entry_instruction
            + 'Conversation attachments are optional unless the selected Workflow Runtime '
            + 'explicitly returns a required-input result. Never infer that an upload is '
            + 'required merely because the Workflow supports uploaded materials. A non-empty '
            + 'text-only current_query is sufficient to trigger generation Workflows once any '
            + 'declared startup clarification is complete. Treat current_query as the workflow '
            + 'request_context and as user_input for the first Ready step unless this is a '
            + 'clarification-answer turn; then pass the trigger a merged request_context containing '
            + 'the original request and all answers; do not ask for a second trigger message. After each '
            + 'successful advance_step, continue in this same turn from its returned ready_steps '
            + 'until terminal, required input, explicit user boundary, or failure. In Human Approval '
            + 'chat mode, approval is for the step result after execution, not for starting the step. '
            + 'Explicit user approval instructions override step defaults; otherwise use '
            + 'the effective requires_approval flag in ready_step_details/projection.nodes, '
            + 'falling back to mode only when the flag is absent. requires_approval=false '
            + 'overrides a package mode=human unless the user explicitly asks to stop at this '
            + 'target: then use advance_step_and_hand_off. Otherwise use advance_step and continue. If a Ready '
            + 'Workflow step requires human approval, execute it with advance_step_and_hand_off so '
            + 'the Host stops after execution for output review. Do not ask whether to execute it, '
            + 'and do not merely announce that later steps will run. For recovery, '
            + 'use only exact retryable_steps, rewindable_steps, or continue_steps returned by Runtime.\n'
            + json.dumps({
                'current_query': current_query,
                'allowed_workflow_refs': sorted(allowed_refs),
                'activations': activations,
                'allowed_workflow_ids': allowed_ids,
            }, ensure_ascii=False, default=str)
        )
    elif discovery_context.prompt:
        selection_context = discovery_context.prompt
    return WorkflowAgentContribution(
        tools, [], patch, selection_context, runtime_policy,
        pending_ask=session_holder.get('pending_ask'),
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
    try:
        async for item in initial_stream:
            yield item
    finally:
        await initial_stream.aclose()

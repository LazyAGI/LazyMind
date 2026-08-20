"""LazyMind Chat adapter for the public Workflow runtime.

This module deliberately owns no Workflow definition loading, graph policy,
transition state, input binding, or Artifact persistence.  It only turns the
public Workflow SDK into ChatAgent tools and applies LazyMind's handoff rule.
"""
from __future__ import annotations

import json
import logging
import re
from dataclasses import asdict, dataclass
from typing import Any, Callable, Dict, List, Optional, Union

import httpx
import lazyllm

from lazymind.chat.engine.tools.intent_writer import enable_workflow_intent_scopes
from lazymind.workflow_sdk import AdvanceRequest, StepCommand, WorkflowClient, WorkflowClientError
from lazymind.workflow_toolkit import (
    AgentWorkflowToolProjection, HostWorkflowToolkit, StepCommandInput,
)

LOG = logging.getLogger(__name__)


@dataclass
class WorkflowAgentContribution:
    tools: List[Any]
    stop_tools: List[str]
    agentic_config_patch: Dict[str, Any]
    runtime_context: str
    runtime_policy: Optional[Dict[str, Any]] = None


@dataclass(frozen=True)
class WorkflowDiscoveryContext:
    activations: List[Dict[str, Any]]
    prompt: str


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
        trace_context=lazyllm.get_trace_context,
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


def _handoff_tool(
    session: Union[str, Callable[[], str]],
    user_input: Optional[Union[str, Callable[[], str]]] = None,
) -> Any:
    def advance_step_and_hand_off(step_id: str) -> str:
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
            allowed.update(frontier.get('rewindable_steps') or [])
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
                focus_hints = []
                focused_tab = str(cfg.get('focused_tab') or '').strip()
                focused_sort_order = cfg.get('focused_sort_order')
                if focused_tab:
                    focus_hints.append(f'User is currently viewing Workflow tab {focused_tab!r}.')
                if focused_sort_order not in (None, ''):
                    focus_hints.append(
                        f'User is currently focused on artifact sort order {focused_sort_order}.'
                    )
                bound_user_input = user_input() if callable(user_input) else user_input
                current_user_input = str(
                    bound_user_input
                    or cfg.get('workflow_current_query')
                    or cfg.get('query')
                    or ''
                ).strip()
                response = client.advance(AdvanceRequest(
                    session_id=selected_session_id,
                    expected_state_version=int(frontier.get('state_version') or 0),
                    steps=[StepCommand(
                        step_id=step_id,
                        user_input=current_user_input,
                        runtime_instruction=' '.join(focus_hints),
                    )],
                    handoff=True,
                    retry_origin=(
                        'user' if bool(cfg.get('user_authorized_workflow_retry'))
                        else 'automatic'
                    ),
                ))
                result = dict(response.result)
                if state_refreshed:
                    result.update(_state_refresh_notice())
                return _result_text(result)
            except WorkflowClientError as exc:
                if exc.code != 'STATE_VERSION_CONFLICT' or attempt > 0:
                    raise
                state_refreshed = True
        raise AssertionError('unreachable')

    advance_step_and_hand_off.__doc__ = (
        'Execute a Ready Workflow step and end this LazyMind turn only after the '
        'Host acknowledges durable ownership of the post-execution approval checkpoint.'
    )
    return advance_step_and_hand_off


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


def _with_terminal_agent_control(result: Dict[str, Any]) -> Dict[str, Any]:
    workflow_state = result.get('workflow_state')
    workflow_state = workflow_state if isinstance(workflow_state, dict) else {}
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


def _safe_session_tools(
    toolkit: HostWorkflowToolkit,
    session: Union[str, Callable[[], str]],
    initialize_session: Optional[Callable[[], Any]] = None,
    user_input: Optional[Union[str, Callable[[], str]]] = None,
) -> List[Any]:
    """Model tools whose protocol and concurrency parameters are Host-injected."""
    def session_id() -> str:
        value = session() if callable(session) else session
        selected = str(value or '').strip()
        if not selected and initialize_session is not None:
            # Explicit Workflow mentions already authorize initialization.  Be
            # tolerant when the model reaches for a Session tool before the
            # bound trigger: initialize the sole selected Workflow from the
            # current text request, then continue the requested operation.
            initialize_session()
            value = session() if callable(session) else session
            selected = str(value or '').strip()
        if not selected:
            raise WorkflowClientError(
                'WORKFLOW_SESSION_NOT_INITIALIZED',
                'Call the selected trigger Workflow tool before using Session tools. '
                'Do not infer that an attachment is required; the trigger determines '
                'whether any external input is actually required.',
            )
        return selected

    def get_workflow_state() -> Dict[str, Any]:
        """Read this conversation's authoritative Workflow state."""
        return toolkit.get_workflow_state(session_id())

    def get_ready_steps() -> Dict[str, Any]:
        """Read exact forward, retryable, and rewindable targets for this Session."""
        return toolkit.get_ready_steps(session_id())

    def advance_step(step_ids: List[str]) -> Dict[str, Any]:
        """Execute exact Runtime-returned target IDs; Host injects version and commands."""
        requested = [str(value).strip() for value in step_ids if str(value).strip()]
        selected_session_id = session_id()
        state_refreshed = False
        for attempt in range(2):
            frontier = toolkit.get_ready_steps(selected_session_id)
            allowed = set(frontier.get('ready_steps') or [])
            allowed.update(frontier.get('retryable_steps') or [])
            allowed.update(frontier.get('rewindable_steps') or [])
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
                focus_hints = []
                focused_tab = str(cfg.get('focused_tab') or '').strip()
                focused_sort_order = cfg.get('focused_sort_order')
                if focused_tab:
                    focus_hints.append(f'User is currently viewing Workflow tab {focused_tab!r}.')
                if focused_sort_order not in (None, ''):
                    focus_hints.append(
                        f'User is currently focused on artifact sort order {focused_sort_order}.'
                    )
                bound_user_input = user_input() if callable(user_input) else user_input
                current_user_input = str(
                    bound_user_input
                    or cfg.get('workflow_current_query')
                    or cfg.get('query')
                    or ''
                ).strip()
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
                )
                if state_refreshed:
                    result = {**result, **_state_refresh_notice()}
                return _with_terminal_agent_control(result)
            except WorkflowClientError as exc:
                if exc.code != 'STATE_VERSION_CONFLICT' or attempt > 0:
                    raise
                state_refreshed = True
        raise AssertionError('unreachable')

    def list_workflow_inputs() -> Dict[str, Any]:
        """List durable input bindings for this Session."""
        return toolkit.list_workflow_inputs(session_id())

    def list_artifacts() -> Dict[str, Any]:
        """List selected Artifacts for this Session."""
        return toolkit.list_artifacts(session_id())

    def read_artifact(artifact_ref: str) -> Dict[str, Any]:
        """Read a selected Artifact by exact slot handle such as report or images[0]."""
        artifact = _artifact_by_handle(toolkit, session_id(), artifact_ref)
        return toolkit.read_artifact(str(artifact.get('artifact_id') or artifact.get('id') or ''))

    def patch_artifact(artifact_ref: str, value: Any, caption: str = '') -> Dict[str, Any]:
        """Patch a selected Artifact; Host injects id, base revision, type, and command."""
        artifact = _artifact_by_handle(toolkit, session_id(), artifact_ref)
        artifact_id = str(artifact.get('artifact_id') or artifact.get('id') or '')
        content_type = str(artifact.get('content_type') or 'json')
        return toolkit.patch_artifact(
            artifact_id, int(artifact.get('revision') or 0), value, content_type, caption,
        )

    return [
        get_workflow_state, get_ready_steps, advance_step, list_workflow_inputs,
        list_artifacts, read_artifact, patch_artifact,
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
        'user_notice': (
            '工作流状态已被其他执行或后台更新改变，本次没有继续提交步骤。'
            '系统已读取最新状态；请根据当前可执行步骤重新确认下一步。'
        ),
    }


def _safe_authoring_tools(toolkit: HostWorkflowToolkit) -> List[Any]:
    """Context-bound authoring tools; models author content, not concurrency metadata."""
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

    def list_workflow_drafts() -> Dict[str, Any]:
        """List drafts available for exact selection."""
        return toolkit.list_workflow_drafts()

    def select_workflow_draft(draft_id: str) -> Dict[str, Any]:
        """Select one exact draft returned by list_workflow_drafts."""
        value = toolkit.get_workflow_draft(draft_id)
        cfg['workflow_authoring_draft_id'] = draft_id
        cfg['workflow_authoring_draft_version'] = int(value.get('version') or 0)
        return value

    def get_workflow_draft() -> Dict[str, Any]:
        """Read the selected authoring draft."""
        return _draft()

    def update_workflow_draft_file(path: str, content: str) -> Dict[str, Any]:
        """Update one allowed package path; Host injects draft and optimistic version."""
        current = _draft()
        value = toolkit.update_workflow_draft_file(
            str(cfg['workflow_authoring_draft_id']), path, content,
            int(current.get('version') or 0),
        )
        cfg['workflow_authoring_draft_version'] = int(value.get('version') or 0)
        return value

    def validate_workflow_draft() -> Dict[str, Any]:
        """Validate the selected draft."""
        _draft()
        return toolkit.validate_workflow_draft(str(cfg['workflow_authoring_draft_id']))

    def get_workflow_diagnostics() -> Dict[str, Any]:
        """Read diagnostics for the selected draft."""
        _draft()
        return toolkit.get_workflow_diagnostics(str(cfg['workflow_authoring_draft_id']))

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
    return asdict(value)


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
    identifiers = set(allowed_refs)
    for key in ('workflow_ref', 'workflow_id'):
        value = str(workflow_context.get(key) or '').strip()
        if value:
            identifiers.add(value)
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
        result.append({
            'id': field_id,
            'label': _clean_workflow_text(raw.get('label')) or field_id,
            'question': question,
            'type': question_type,
            'choices': choices,
        })
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
    if isinstance(arguments, dict):
        return arguments
    if isinstance(arguments, str):
        try:
            parsed = json.loads(arguments)
        except json.JSONDecodeError:
            return {}
        return parsed if isinstance(parsed, dict) else {}
    return {}


def _question_fingerprint(value: Any) -> str:
    return re.sub(r'[^0-9a-z\u4e00-\u9fff]+', '', str(value or '').lower())


def _history_startup_ask_index(
    conversation_history: Any,
    runtime_policy: Any,
    workflow_catalog: Any = None,
    *,
    discovery_mode: bool = False,
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
        if sum(entry.get('role') == 'user' for entry in tail) > 1:
            continue
        return index
    return -1


def workflow_startup_clarification_already_asked(
    conversation_history: Any,
    runtime_policy: Any,
    workflow_catalog: Any = None,
    *,
    discovery_mode: bool = False,
) -> bool:
    """Return whether this Workflow's one startup question card was already shown."""
    return _history_startup_ask_index(
        conversation_history,
        runtime_policy,
        workflow_catalog,
        discovery_mode=discovery_mode,
    ) >= 0


def _merge_startup_clarification_context(
    current_query: str,
    conversation_history: Any,
    runtime_policy: Any,
) -> str:
    """Merge the original request with the answer turn before trigger creation."""
    ask_index = _history_startup_ask_index(conversation_history, runtime_policy)
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


def _startup_clarification_guidance(runtime_policy: Any) -> str:
    fields = _runtime_clarification_fields(runtime_policy)
    if not fields:
        return ''
    return (
        'This Workflow declares startup_clarification_fields. Before calling its trigger, '
        'inspect the current request, relevant attachment names/content already available, '
        'and prior conversation turns. Treat a field as present only when it is explicit or '
        'unambiguously inferable. Do not ask for fields that are already present. If one or '
        'more fields are missing, call ask_user exactly once TOTAL with only those missing fields, '
        'putting all of them in that single card, then stop without triggering the Workflow in '
        'that turn. Include each '
        'declared field id in its question object. Whenever meaningful, generate 2-4 concise, '
        'context-specific suggested answers from the current request and use type=single so the '
        'user can click one; declared choices are useful seeds, not a limit. Keep type=text only '
        'when responsible suggestions cannot be inferred. On the answer turn, NEVER call ask_user '
        'again or reassess fields as missing. Combine the original request, all already-known '
        'fields, and the new answers into one concise request_context and pass that value to '
        'the trigger; an answer-only current_query must never replace the original request. '
        'If no field is missing, trigger immediately. Never ask for an upload unless the '
        'Workflow separately declares it as a required input. Declared fields: '
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
        'request and prior conversation first. If fields are missing, call ask_user once with '
        'only the missing declared questions and stop; if none are missing, trigger immediately. '
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


def _workflow_trigger_tools(
    activations: List[Dict[str, Any]], allowed_refs: set[str], current_query: str = '',
    conversation_id: str = '', session_holder: Optional[Dict[str, str]] = None,
    conversation_history: Optional[List[Dict[str, Any]]] = None,
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

        def make_trigger(
            bound_id: str, bound_ref: str, bound_revision: str, bound_query: str,
            bound_clarification_answer: bool,
        ) -> Any:
            def run_trigger(
                input_bindings: Optional[Dict[str, str]] = None,
                request_context: Optional[str] = None,
            ) -> Dict[str, Any]:
                # Once startup clarification has happened, the Host-composed
                # query is authoritative because it contains both the original
                # request and this turn's answers. A model-supplied answer-only
                # request_context must not discard the known topic/page count.
                effective_context = (
                    bound_query
                    if bound_clarification_answer
                    else str(request_context or '').strip() or bound_query
                )
                if session_holder is not None:
                    # Keep the immutable launch brief available to the first
                    # Ready steps in this same Chat turn. In particular, an Ask
                    # answer is supplemental context; it must not become the
                    # step's answer-only ``user_input`` and erase the original
                    # topic, page count, or other supplied constraints.
                    session_holder['request_context'] = effective_context
                existing_session_id = str(
                    (session_holder or {}).get('session_id') or '',
                ).strip()
                if existing_session_id:
                    # Session tools may have initialized the sole explicitly
                    # selected Workflow as a recovery path. Keep a later model
                    # call to the advertised trigger idempotent within this turn.
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
                resolved_bindings: Dict[str, Any] = {}
                for material_id, attachment_ref in (input_bindings or {}).items():
                    from lazymind.chat.engine.subagent.tools import _resolve_attachment
                    path, error = _resolve_attachment(attachment_ref)
                    if error or not path:
                        raise WorkflowClientError(
                            'ATTACHMENT_NOT_SELECTED',
                            error or 'The referenced conversation attachment was not found.',
                        )
                    resolved_bindings[material_id] = _import_attachment(path)
                client = _client()
                package = client.get_workflow(bound_id, bound_revision).result
                toolkit = HostWorkflowToolkit(
                    _client,
                    allowed_workflow_ids=[bound_id],
                    origin_ref=conversation_id,
                )
                prepared = toolkit.prepare_workflow(
                    bound_id, input_bindings=resolved_bindings,
                    request_context=effective_context,
                )
                session_id = str(prepared.get('session_id') or '')
                if not session_id:
                    return {
                        **prepared,
                        'status': 'waiting',
                        'outcome': 'waiting_for_input',
                        'reason': 'Workflow preparation requires additional input before a Session can be created.',
                        'workflow_ref': bound_ref,
                        'workflow_id': bound_id,
                        'revision_id': str(package.get('revision_id') or bound_revision),
                        'request_context': effective_context,
                        'input_bindings': resolved_bindings,
                    }
                if session_holder is not None:
                    session_holder['session_id'] = session_id
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
                    'input_bindings': resolved_bindings,
                    'session_id': session_id,
                    'state_version': int(state.get('state_version') or prepared.get('state_version') or 0),
                    'projection': projection,
                    'reachable_steps': reachable_steps,
                    'ready_steps': ready_steps,
                    'blocked_steps': blocked_steps,
                    'retryable_steps': retryable_steps,
                    'rewindable_steps': rewindable_steps,
                    'next_action': {
                        'tool': 'advance_step',
                        'instruction': (
                            'Call advance_step using only exact members of ready_steps, '
                            'retryable_steps, or rewindable_steps; Runtime resolves the operation.'
                        ),
                    },
                }
            if attachments_available:
                def bound_trigger(
                    input_bindings: Optional[Dict[str, str]] = None,
                    request_context: Optional[str] = None,
                ) -> Dict[str, Any]:
                    """Initialize with optional attachments and a merged clarified request."""
                    return run_trigger(input_bindings, request_context)
            else:
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
        )

        trigger_workflow.__name__ = name
        description = str(item.get('tool_description') or '').strip()
        attachment_guidance = (
            ' input_bindings may map material IDs only to exact filenames listed in '
            'the conversation attachments; omit it for generation or web-search flows.'
            if attachments_available else
            ' No user attachments are available; start without input bindings so the '
            'Workflow can generate from text or collect images itself.'
        )
        trigger_workflow.__doc__ = (
            description + attachment_guidance
            + ' If this turn follows startup clarification, request_context must merge the '
            'original request with every clarification answer; otherwise omit it.'
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
) -> WorkflowAgentContribution:
    """Map public Workflow APIs to LazyMind Chat tools; no Runtime decisions live here."""
    cfg = _agentic_config()
    if not cfg.get('enable_workflow', True):
        return WorkflowAgentContribution([], [], {}, '')

    context = workflow_context if isinstance(workflow_context, dict) else {}
    session_id = str(context.get('session_id') or '')
    workflow_id = str(context.get('workflow_id') or context.get('workflow_ref') or '')
    revision_id = str(context.get('revision_id') or '')
    mode = str(context.get('workflow_mode') or 'dynamic')
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
    session_holder: Dict[str, str] = {'session_id': session_id}
    trigger_tools = _workflow_trigger_tools(
        activations, allowed_refs, current_query, conversation_id, session_holder,
        conversation_history,
    )
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
        initialize_selected_session = (
            (lambda: trigger_tools[0]()) if len(trigger_tools) == 1 else None
        )

        def launch_user_input() -> str:
            return session_holder.get('request_context', '')

        handoff = _handoff_tool(
            lambda: session_holder.get('session_id', ''),
            user_input=launch_user_input,
        )
        tools = [
            *[tool for tool in tools if _is_bound_workflow_trigger(tool.__name__)],
            *_safe_session_tools(
                toolkit,
                lambda: session_holder.get('session_id', ''),
                initialize_session=initialize_selected_session,
                user_input=launch_user_input,
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
            # Discovery mode: expose triggers directly so the model can route
            # from the injected catalog without a gateway hop. The tool set is
            # fixed for the model turn, so Session tools must also be available
            # for trigger -> advance execution in that same turn.
            tools = [
                *trigger_entry_tools,
                *_safe_session_tools(
                    toolkit,
                    lambda: session_holder.get('session_id', ''),
                    user_input=lambda: session_holder.get('request_context', ''),
                ),
                _handoff_tool(
                    lambda: session_holder.get('session_id', ''),
                    user_input=lambda: session_holder.get('request_context', ''),
                ),
                authoring_group,
            ]
        else:
            tools = [authoring_group]
    if session_id:
        tools = _safe_session_tools(toolkit, session_id)
        patch.update({
            'workflow_id': workflow_id,
            'workflow_session_id': session_id,
            'workflow_step': context.get('current_step') or '',
            'workflow_ref': context.get('workflow_ref') or '',
            'revision_id': revision_id,
            'focused_tab': context.get('focused_tab') or '',
            'focused_sort_order': context.get('focused_sort_order'),
        })
        tools.append(_handoff_tool(session_id))
        session_projection = (
            projection.get('projection')
            if isinstance(projection.get('projection'), dict) else {}
        )
        completed_followup = ''
        if session_projection.get('completed'):
            completed_followup = (
                'A completed Session is not immutable. When the current user query asks to '
                'revise, delete, fix, or regenerate an existing Workflow output, do not '
                'write replacement content in chat and do not use generic file/artifact '
                'tools. Call get_ready_steps, select the matching exact rewindable_steps '
                'target, and call advance_step so Runtime starts a new step attempt and '
                'publishes a new Workflow artifact revision. '
            )
            completed_edit_step = str(runtime_policy.get('completed_edit_step') or '').strip()
            if completed_edit_step:
                completed_followup += (
                    'This Workflow declares that requests to modify, repair, delete, or '
                    f'regenerate completed output map to the {completed_edit_step!r} step. '
                    f'If {completed_edit_step!r} is present in rewindable_steps, you MUST '
                    f'call advance_step(step_ids=[{completed_edit_step!r}]) now. That step '
                    'owns the existing artifacts and publishes their new revisions; never '
                    'paste replacement content into chat as a substitute. '
                )
        runtime_context = (
            '## Workflow Runtime [AUTHORITATIVE]\n'
            + 'The Host owns session/version concurrency fields. Never ask the user for '
            + 'state_version or expected_state_version. If a Workflow tool returns '
            + 'user_notice, explicitly relay that notice to the user. advance_step waits for '
            + 'terminal execution. A failed result never means success and never permits a '
            + 'downstream advance. You may decide to retry only an exact retryable_steps ID; '
            + 'Runtime enforces the finite AI automatic-retry budget. User-requested retries '
            + 'remain available and do not consume that budget. After step_succeeded, continue in the '
            + 'same turn using only the returned ready_steps until the Workflow is terminal, '
            + 'requires user input, reaches an explicit user boundary, or a step fails. '
            + 'In Human Approval chat mode, approval is for a step result after that step runs; '
            + 'it is not permission to start the step. Decide approval checkpoints by priority: '
            + 'first obey explicit user instructions in the current request; when absent, use '
            + 'the Ready step default from ready_step_details, projection.nodes[step_id].mode, '
            + 'or projection.nodes[step_id].requires_approval. mode=human/default_approval=required '
            + 'means execute that Ready step with advance_step_and_hand_off so the Host stops '
            + 'after execution for human review of its outputs. mode=auto/default_approval=not_required '
            + 'means execute with advance_step and continue. Never ask whether to execute a Ready '
            + 'step merely because it requires approval; the approval checkpoint belongs to its result. '
            + 'Do not replace continued execution with a promise that later steps will run. '
            + completed_followup
            + '\n'
            + 'Artifact-mutation guard: when the user asks to change an Artifact already '
            + 'owned by this active Workflow, including inserting, replacing, moving, or '
            + 'deleting an image in the current document, rerun the earliest owning Workflow '
            + 'step. Do not bypass the Workflow with generic file, image, artifact, or SubAgent '
            + 'tools; media acquisition belongs inside the owning step.\n'
            + json.dumps(projection, ensure_ascii=False, default=str)
        )
        return WorkflowAgentContribution(
            # advance_step is synchronous: its terminal result and refreshed
            # Ready frontier must be returned to the same ChatAgent turn so a
            # user-requested continuous run can keep advancing.  Only the
            # explicit hand-off variant transfers ownership and ends the turn.
            tools, ['advance_step_and_hand_off'],
            patch, runtime_context, runtime_policy,
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
            + 'selected and authorized this exact procedure. ' + entry_instruction
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
            + 'ready_step_details/projection.nodes mode and requires_approval to decide. If a Ready '
            + 'Workflow step requires human approval, execute it with advance_step_and_hand_off so '
            + 'the Host stops after execution for output review. Do not ask whether to execute it, '
            + 'and do not merely announce that later steps will run. For recovery, '
            + 'use only exact retryable_steps or rewindable_steps returned by Runtime.\n'
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

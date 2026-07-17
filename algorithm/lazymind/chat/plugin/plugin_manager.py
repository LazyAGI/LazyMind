"""Plugin manager — builds ChatAgent tools for cold-start triggers and step advancement.

Tool types registered dynamically per-conversation:

- trigger_<plugin_id>       : Cold-start tool. Injected when no active plugin session exists.
- advance_step_and_hand_off : Step-advancement tool (stop-tool). Default; queues step and hands off control to user.
- advance_step              : Synchronous step-advancement tool. Only in 'dynamic' mode; blocks until
                              the SubAgent finishes before ReAct continues.
- advance_steps             : Atomic synchronous batch advancement for multiple Ready steps.
- advance_steps_and_hand_off: Atomic asynchronous batch advancement (stop-tool).
- ask_user                  : Ask the user a question (stop-tool). ChatAgent only; absent in auto mode.
- intentwrite               : Extended with plugin-session and plugin-step scopes when active.
- list_plugin_steps         : Read-only step status query (ChatAgent only, when session active).
- get_step_result           : Read-only artifact summary for a step (ChatAgent only).
- get_failed_steps          : Read-only failed steps with error info (ChatAgent only).

Framework tools (save_artifact / get_artifact / list_artifacts) are always merged into
the step's tool list regardless of what the plugin's state.yml declares.  This ensures
every SubAgent can persist and retrieve artifacts without plugin authors having to
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

from lazymind.chat.plugin import plugin_loader
from lazymind.chat.engine.subagent import SUBAGENT_CORE_TOOL_NAMES
from lazymind.chat.engine.tools.intent_writer import enable_plugin_intent_scopes
from lazymind.model_config import is_model_role_available

LOG = logging.getLogger(__name__)

_PREFLIGHT_DECISIONS = {'ready', 'need_information', 'not_applicable'}
_PREFLIGHT_TIMEOUT_SECONDS = 30.0


@dataclass
class PluginAgentContribution:
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
    reason = str(error.get('message') or 'Go rejected the plugin state transition.')
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
        *, plugin_id: str, step_id: str, session_id: str, task_id: str,
        objective: str, user_input: str, hand_off: bool,
        runtime_instruction: str, partial_indices: Dict[str, List[int]],
        operation: str = 'advance', is_start: bool = False,
        preflight_id: str = '',
        targets: Optional[List[Dict[str, Any]]] = None) -> _TransitionSubmission:
    import httpx
    from lazymind.config import config as _cfg

    cfg = _agentic_config()
    core_url = str(_cfg['core_api_url']).rstrip('/')
    projection_data: Dict[str, Any] = {}
    if not is_start:
        try:
            projection_resp = httpx.get(
                f'{core_url}/internal/plugin-sessions/{session_id}/projection', timeout=5.0,
            )
            if projection_resp.status_code == 200:
                projection_data = _core_response_data(projection_resp)
        except Exception as exc:
            LOG.warning('[plugin.transition] projection prefetch failed session=%s error=%s', session_id, exc)
    command_id = str(uuid.uuid4())
    expected_version = int(projection_data.get('state_version') or cfg.get('_plugin_state_version') or 0)
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
        'plugin_mode': str(cfg.get('plugin_mode') or 'dynamic'),
        'chat_session_id': str(cfg.get('session_id') or ''),
        'history_files_per_turn': cfg.get('history_files_per_turn') or {},
        'filters': cfg.get('filters') or {},
        'llm_config': cfg.get('llm_config') or {},
        'tool_config': cfg.get('tool_config') or {},
        'parent_agentic_config': _export_parent_agentic_config(cfg),
        'plugin_id': plugin_id,
        'plugin_ref': str(cfg.get('plugin_ref') or ''),
        'plugin_revision_id': str(cfg.get('revision_id') or ''),
        'plugin_revision_no': int(cfg.get('revision_no') or 0),
        'plugin_tree_hash': str(cfg.get('tree_hash') or ''),
        'plugin_remote_root': str(cfg.get('remote_root') or ''),
        'conversation_id': str(cfg.get('conversation_id') or ''),
        'trigger_history_id': str(cfg.get('history_id') or ''),
        'user_id': str(cfg.get('user_id') or ''),
        'preflight_id': preflight_id,
        'external_materials': cfg.get('plugin_external_materials') or {},
    }
    if targets:
        payload['targets'] = targets
    endpoint = (
        f'{core_url}/internal/plugin-sessions:start'
        if is_start else f'{core_url}/internal/plugin-sessions/{session_id}:transition'
    )
    try:
        response = httpx.post(endpoint, json=payload, timeout=15.0)
        data = _core_response_data(response)
    except httpx.TimeoutException:
        # The command id makes an ambiguous network timeout reconcilable without
        # submitting a second transition.
        try:
            status_resp = httpx.get(
                f'{core_url}/internal/plugin-transition-commands/{command_id}', timeout=5.0,
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
                response = httpx.post(endpoint, json=payload, timeout=15.0)
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
            '[plugin.transition] rejected plugin=%s step=%s session=%s command=%s operation=%s '
            'http_status=%s code=%s reason=%s details=%s',
            plugin_id, step_id, session_id, command_id, operation,
            response.status_code, rejection.get('code', ''), rejection.get('message', ''),
            rejection.get('details') if isinstance(rejection.get('details'), dict) else {},
        )
        return _TransitionSubmission(
            False, _format_transition_rejection(step_id, data), command_id=command_id,
            state_version=int(data.get('state_version') or expected_version),
            projection=data.get('projection') if isinstance(data.get('projection'), dict) else {},
        )
    state_version = int(data.get('state_version') or expected_version)
    cfg['_plugin_state_version'] = state_version
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
    cfg['_last_plugin_task_id'] = normalised_tasks[0]['task_id']
    cfg['_last_plugin_tasks'] = normalised_tasks
    if is_start:
        cfg['plugin_session_id'] = str(data.get('session_id') or '')
        cfg['plugin_id'] = plugin_id
        cfg['plugin_step'] = step_id
    return _TransitionSubmission(
        True,
        (
            f'Batch advance for steps {[item["step_id"] for item in normalised_tasks]!r} '
            'accepted by Go and durably queued.'
            if len(normalised_tasks) > 1
            else f'Advance for step {step_id!r} accepted by Go and durably queued.'
        ),
        command_id=command_id,
        task_id=str(data.get('task_id') or task_id),
        session_id=str(data.get('session_id') or session_id),
        state_version=state_version,
        projection=data.get('projection') if isinstance(data.get('projection'), dict) else {},
        tasks=normalised_tasks,
    )


def _fetch_go_start_candidates(plugin_id: str) -> List[str]:
    """Return Go's authoritative Ready set for a not-yet-started session."""
    import httpx
    from lazymind.config import config as _cfg

    cfg = _agentic_config()
    core_url = str(_cfg['core_api_url']).rstrip('/')
    payload = {
        'plugin_id': plugin_id,
        'plugin_revision_id': str(cfg.get('revision_id') or ''),
        'external_materials': cfg.get('plugin_external_materials') or {},
    }
    response = httpx.post(
        f'{core_url}/internal/plugin-sessions:plan-start', json=payload, timeout=10.0,
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
    cfg['_plugin_start_route_hints'] = hints
    return [str(step_id) for step_id in ready if step_id]


def is_plugin_driver_turn(plugin_context: Any) -> bool:
    """Return whether this request is a synthetic turn initiated by DriverAgent."""
    return bool(
        isinstance(plugin_context, dict)
        and plugin_context.get('synthetic_source') == 'driver'
    )


_COLD_START_PLUGIN_PROMPT = (
    '## Available Plugins\n'
    'IMPORTANT: Only trigger a plugin when the capability matches the '
    "user's PRIMARY and DIRECT intent — the main goal they are asking for "
    'right now. Never trigger a plugin for a sub-step that the model has '
    "internally decided is part of a larger multi-step plan. If the user's "
    'request involves multiple steps and only one of those steps would use a '
    'plugin, do NOT trigger the plugin. Never infer plugin intent from '
    'indirect or implicit cues.\n'
    'When a plugin matches, call its `trigger_<plugin>` preflight tool. Trigger does NOT '
    'start a task. It loads the full plugin and returns ready, need_information, '
    'not_applicable, or preflight_failed.\n'
    'If trigger returns ready, you MUST immediately follow its returned instruction and '
    'call the applicable advancement tool in the SAME turn. Do not explain, confirm, or '
    'end the turn first.\n'
    'If it returns need_information, use ask_user only when that tool is available.\n\n'
    'CRITICAL — explicit plugin start requests:\n'
    'If the user explicitly asks to start, launch, or enable a plugin (e.g. '
    '"启动绘图插件", "打开图片生成插件", "启动图片插件", "start the image plugin"), '
    'you MUST call the matching `trigger_<plugin_id>_plugin` tool in this same '
    'response before any other action. Do NOT reply with text only, do NOT call '
    '`image_generator` / `image_editor` / `video_generator` / `video_to_gif` directly, '
    'and do NOT ask clarification '
    'questions first. Pass the user\'s request as `user_input` (or repeat their '
    'start phrase if they gave no further detail).\n'
    'For the AI image plugin (`image-plugin`), call `trigger_image_plugin`.\n\n'
)


# ---------------------------------------------------------------------------
# Framework tools always injected into every plugin step regardless of what
# the plugin's state.yml declares.
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
        resp = httpx.get(
            f'{core_url}/internal/plugin-sessions/{session_id}/projection', timeout=5.0,
        )
        if resp.status_code != 200:
            return {}
        data = _core_response_data(resp)
        projection = data.get('projection') or {}
        if isinstance(projection, dict):
            _agentic_config()['_plugin_state_version'] = int(data.get('state_version') or 0)
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
    """Return the JSON-safe request context a plugin SubAgent should inherit."""
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


def _trigger_plugin_step(
        plugin_id: str, step_id: str, user_input: str,
        is_cold_start: bool = False,
        hand_off: bool = False,
        preflight_id: str = '',
        runtime_instruction: str = '',
        partial_indices: Optional[Dict[str, List[int]]] = None,
        operation: str = 'advance') -> str:
    """Shared implementation for trigger_<plugin_id> and advance_step.

    Performs local request-shape validation, then submits a synchronous Go
    transition command. Go is the sole authority for Reachable/Ready admission.

    Args:
        plugin_id: The plugin identifier.
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
    session_id: str = cfg.get('plugin_session_id', '') or str(uuid.uuid4())
    current_step: str = cfg.get('plugin_step', '')
    LOG.info(
        '[plugin.advance] trigger requested plugin=%s step=%s session=%s current=%s cold=%s input_len=%d',
        plugin_id, step_id, session_id, current_step or '__start__', is_cold_start, len(user_input or ''),
    )

    # --- Layer 1: format validation (no DB needed) ---
    if not user_input or not user_input.strip():
        # Fall back to the current conversation query so the SubAgent always
        # receives meaningful context even when the LLM omits user_input.
        user_input = cfg.get('query', '').strip()
    if not user_input:
        raise ValueError('user_input must not be empty.')

    if plugin_loader.get_plugin(plugin_id) is None:
        raise ValueError(f'plugin {plugin_id!r} not found.')

    # Step existence and prompt rendering remain local metadata concerns. Never
    # reject a transition from the Python graph: Go evaluates the compiled graph
    # and returns a structured rejection with the authoritative projection.
    step_config = plugin_loader.get_step_config(plugin_id, step_id)
    if not step_config:
        raise ValueError(f'step {step_id!r} is not defined in plugin {plugin_id!r}.')
    # --- Submit transition command ---
    task_id = str(uuid.uuid4())
    LOG.info(
        '[plugin.advance] submitting command plugin=%s step=%s session=%s task=%s cold=%s',
        plugin_id, step_id, session_id, task_id, is_cold_start,
    )

    # Inject focused_tab (UI context hint) into the objective.
    # focused_sort_order is NOT injected — it is the UI scroll position,
    # not the user's intended operation target. The SubAgent reads the
    # runtime_instruction directly and decides which sort_order to pass
    # to save_artifact based on the user's stated intent.
    focused_tab = cfg.get('focused_tab')
    enriched_instruction = runtime_instruction or ''
    if focused_tab:
        sep = ' ' if enriched_instruction else ''
        enriched_instruction = enriched_instruction + sep + f'User is currently viewing tab: {focused_tab}.'

    objective = _render_step_objective(step_config, user_input, enriched_instruction)
    submission = _submit_transition_to_core(
        plugin_id=plugin_id,
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
    cfg['_last_plugin_transition_accepted'] = submission.accepted
    if submission.accepted:
        cfg['_last_plugin_task_id'] = submission.task_id
    LOG.info(
        '[plugin.transition] core result plugin=%s step=%s session=%s command=%s accepted=%s',
        plugin_id, step_id, submission.session_id or session_id,
        submission.command_id, submission.accepted,
    )
    return submission.message


def _trigger_plugin_steps(
        plugin_id: str,
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
    if plugin_loader.get_plugin(plugin_id) is None:
        raise ValueError(f'plugin {plugin_id!r} not found.')

    cfg = _agentic_config()
    session_id = str(cfg.get('plugin_session_id') or '')
    if not session_id:
        raise ValueError('batch advancement requires an active plugin session.')
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
        step_config = plugin_loader.get_step_config(plugin_id, step_id)
        if not step_config:
            raise ValueError(f'step {step_id!r} is not defined in plugin {plugin_id!r}.')
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
        plugin_id=plugin_id,
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
    cfg['_last_plugin_transition_accepted'] = submission.accepted
    if submission.accepted:
        cfg['_last_plugin_task_id'] = submission.task_id
        cfg['_last_plugin_tasks'] = submission.tasks or []
    return submission


def _build_step_choices_doc(
    forward_steps: List[str],
    rewind_steps: List[str],
    step_labels: Dict[str, str],
    plugin_id: str = '',
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
        lines.append('Ready steps reported by Go:')
        for s in forward_steps:
            label = step_labels.get(s, '')
            label_suffix = f'  ({label})' if label else ''
            approval_note = ''
            if include_default_approval:
                approval = (
                    'required'
                    if plugin_loader.get_step_mode(plugin_id, s) == 'human'
                    else 'not required'
                )
                approval_note = f'  [default approval: {approval}]'
            lines.append(f'  - {s}{label_suffix}{approval_note}')
    rerun_steps: List[str] = []
    if current_step and current_step not in {'__start__', '__end__'}:
        rerun_steps.append(current_step)
    rerun_steps.extend(step for step in rewind_steps if step not in rerun_steps)
    if rerun_steps:
        lines.append('Previously attempted steps that may be run again:')
        for s in rerun_steps:
            label = step_labels.get(s, '')
            suffix = f'  ({label})' if label else ''
            lines.append(f'  - {s}{suffix}  <- select this ID to run it again')
    lines.append('')
    lines.append('Pass one of the above IDs as step_id. Any other value will be rejected.')
    return '\n'.join(lines)


def _build_step_name_index(plugin_id: str) -> str:
    """Return a compact id-to-name index without graph or step details."""
    spec = plugin_loader.get_plugin(plugin_id)
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
        '## Plugin Step Name Index [AUTHORITATIVE]\n'
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


def _evaluate_plugin_preflight(
    *,
    plugin_id: str,
    plugin_name: str,
    description: str,
    when_to_use: str,
    scenario: str,
    request_context: str,
    previous: Optional[Dict[str, Any]],
    first_steps: List[str],
    plugin_mode: str,
    explicit_plugin_request: bool = False,
) -> Dict[str, Any]:
    """Run the side-effect-free LLM suitability check for a cold plugin start."""
    if not is_model_role_available('llm'):
        raise RuntimeError('the llm model role is not available for plugin preflight')
    previous_json = json.dumps(previous or {}, ensure_ascii=False)
    prompt = f'''You are a plugin launch preflight evaluator. Return exactly one JSON object and no prose.

Plugin id: {plugin_id}
Plugin name: {plugin_name}
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

Explicit plugin request: {json.dumps(bool(explicit_plugin_request))}

If Explicit plugin request is true, the user has authoritatively selected this plugin.
You MUST NOT return not_applicable. Return ready when safe defaults are available, or
need_information only when information is genuinely required before the first step can run.

Classify the request as exactly one of:
- ready: applicable and all truly required information is available or has an explicit safe default.
- need_information: applicable but required information is missing.
- not_applicable: this plugin should not be launched for the request.

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
        'plugin_preflight_updated',
        clear=snapshot is None,
        snapshot=snapshot or {},
    )


def build_cold_start_tools(
    plugin_catalog: Optional[List[Dict[str, Any]]] = None,
    disabled_builtin_plugins: Optional[List[str]] = None,
    allowed_plugin_refs: Optional[List[str]] = None,
) -> List[Any]:
    """Build one side-effect-free preflight trigger per loaded plugin."""
    tools = []
    disabled = set(disabled_builtin_plugins or [])
    allowed = set(allowed_plugin_refs or [])
    candidates = [
        (spec, None)
        for spec in (plugin_loader._registry or {}).values()
        if (
            not spec.plugin_id.startswith('user_')
            and spec.plugin_id not in disabled
            and (not allowed or f'builtin:{spec.plugin_id}' in allowed)
        )
    ]
    candidates.extend(
        (None, entry) for entry in (plugin_catalog or [])
        if not allowed or str(entry.get('plugin_ref') or '') in allowed
    )
    for spec, catalog_entry in candidates:
        if catalog_entry is not None:
            pid = str(catalog_entry.get('plugin_id') or 'plugin')
            name = str(catalog_entry.get('name') or pid)
            desc = str(catalog_entry.get('description') or f'Trigger the {name} plugin.')
            when_to_use = str(catalog_entry.get('when_to_use') or '').strip()
            first_steps: List[str] = []
            plugin_ref = str(catalog_entry.get('plugin_ref', pid)).encode()
            ref_digest = hashlib.sha256(plugin_ref).hexdigest()[:8]
            public_tool_name = f'trigger_{pid.replace("-", "_")}_{ref_digest}'
        else:
            assert spec is not None
            pid = spec.plugin_id
            name = spec.yaml.get('name', pid)
            desc = spec.yaml.get('description', f'Trigger the {name} plugin.')
            when_to_use = spec.yaml.get('when_to_use', '').strip()
            # Entry candidates are resolved by Go when the trigger runs. Keeping
            # them out of the static tool definition prevents stale local graph
            # semantics from being presented as runtime Ready state.
            first_steps = []
            public_tool_name = f'trigger_{pid.replace("-", "_")}'

        def _make_trigger(
            plugin_id: str,
            plugin_name: str,
            first: List[str],
            plugin_desc: str,
            plugin_when_to_use: str,
            entry=None,
            tool_name='',
        ):

            def _trigger(request_context: str, explicit_plugin_request: bool) -> str:
                request_context = str(request_context or '').strip()
                explicit_plugin_request = bool(explicit_plugin_request)
                if not request_context:
                    return json.dumps({
                        'status': 'preflight_failed',
                        'outcome': 'preflight_failed',
                        'reason': 'request_context must not be empty',
                        'error': 'request_context must not be empty',
                    }, ensure_ascii=False)
                resolved_plugin_id = plugin_id
                resolved_first = first
                runtime_meta: Dict[str, Any] = {}
                if entry is not None:
                    resolved_plugin_id, _runtime_spec = plugin_loader.resolve_remote_plugin(entry)
                    runtime_meta = {
                        key: entry.get(key)
                        for key in ('plugin_ref', 'revision_id', 'revision_no', 'tree_hash', 'remote_root')
                    }
                resolved_spec = plugin_loader.get_plugin(resolved_plugin_id)
                if resolved_spec is None:
                    return json.dumps({
                        'status': 'preflight_failed',
                        'outcome': 'preflight_failed',
                        'reason': f'plugin {resolved_plugin_id!r} is not loaded',
                        'error': f'plugin {resolved_plugin_id!r} is not loaded',
                    }, ensure_ascii=False)
                cfg = _agentic_config()
                cfg.update(runtime_meta)
                try:
                    resolved_first = _fetch_go_start_candidates(resolved_plugin_id)
                except Exception as exc:
                    return json.dumps({
                        'status': 'preflight_failed',
                        'outcome': 'preflight_failed',
                        'reason': f'Go could not plan the plugin start: {exc}',
                        'error': str(exc),
                    }, ensure_ascii=False)
                if not resolved_first:
                    return json.dumps({
                        'status': 'preflight_failed',
                        'outcome': 'preflight_failed',
                        'reason': 'Go reports no Ready entry step for the current materials',
                        'error': 'no Ready entry step',
                    }, ensure_ascii=False)
                cfg.pop('prepared_plugin', None)
                if cfg.get('plugin_session_id'):
                    return json.dumps({
                        'status': 'preflight_failed',
                        'outcome': 'preflight_failed',
                        'reason': 'an active plugin session already exists',
                        'error': 'an active plugin session already exists',
                    }, ensure_ascii=False)
                previous = cfg.get('plugin_preflight_context')
                if not isinstance(previous, dict) or previous.get('plugin_id') != resolved_plugin_id:
                    previous = None
                # Once the user explicitly selects a plugin, retain that choice
                # across any clarification turns whose text may no longer repeat
                # the plugin name.
                explicit_plugin_request = bool(
                    explicit_plugin_request
                    or (previous or {}).get('explicit_plugin_request')
                )
                plugin_mode = str(cfg.get('plugin_mode') or 'dynamic')
                try:
                    start_hints = cfg.pop('_plugin_start_route_hints', {})
                    preflight_scenario = resolved_spec.scenario_md
                    if start_hints:
                        hint_lines = ['Start route candidates (natural-language ChatAgent decision):']
                        for step_id in resolved_first:
                            hints = start_hints.get(step_id) or []
                            hint_lines.append(f'- {step_id}: {" OR ".join(hints) if hints else "always applicable"}')
                        preflight_scenario = preflight_scenario + '\n\n' + '\n'.join(hint_lines)
                    raw_result = _evaluate_plugin_preflight(
                        plugin_id=resolved_plugin_id,
                        plugin_name=plugin_name,
                        description=plugin_desc,
                        when_to_use=plugin_when_to_use,
                        scenario=preflight_scenario,
                        request_context=request_context,
                        previous=previous,
                        first_steps=resolved_first,
                        plugin_mode=plugin_mode,
                        explicit_plugin_request=explicit_plugin_request,
                    )
                    result = _normalise_preflight_result(
                        raw_result,
                        first_steps=resolved_first,
                        fallback_request=request_context,
                        require_hand_off=False,
                    )
                    # Explicit user selection outranks the model's suitability
                    # heuristic.  A preflight may still request genuinely required
                    # information, but it may not veto the selected plugin.  Treat a
                    # contradictory not_applicable result as ready so the launch
                    # invariant below can deterministically start the first step.
                    if explicit_plugin_request and result['decision'] == 'not_applicable':
                        LOG.warning(
                            '[plugin.preflight] overriding not_applicable for explicit request plugin=%s',
                            resolved_plugin_id,
                        )
                        result.update({
                            'decision': 'ready',
                            'reason': 'The user explicitly requested this plugin.',
                            'missing_information': [],
                            'first_step_id': resolved_first[0],
                        })
                except Exception as exc:
                    LOG.warning('[plugin.preflight] failed plugin=%s error=%s', resolved_plugin_id, exc)
                    failure_snapshot = {
                        **(previous or {}),
                        'preflight_id': str((previous or {}).get('preflight_id') or uuid.uuid4()),
                        'plugin_id': resolved_plugin_id,
                        'plugin_name': plugin_name,
                        'status': 'failed',
                        'original_intent': str(
                            (previous or {}).get('original_intent') or request_context
                        ).strip(),
                        'normalized_request': str(
                            (previous or {}).get('normalized_request') or request_context
                        ).strip(),
                        'missing_information': (previous or {}).get('missing_information') or [],
                        'explicit_plugin_request': explicit_plugin_request,
                        **runtime_meta,
                    }
                    cfg['plugin_preflight_context'] = failure_snapshot
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
                    cfg.pop('prepared_plugin', None)
                    cfg.pop('plugin_preflight_context', None)
                    _emit_preflight_snapshot(None)
                    return json.dumps({
                        'status': 'not_applicable',
                        'outcome': 'not_applicable',
                        'reason': result['reason'],
                    }, ensure_ascii=False)

                snapshot: Dict[str, Any] = {
                    'preflight_id': str((previous or {}).get('preflight_id') or uuid.uuid4()),
                    'plugin_id': resolved_plugin_id,
                    'plugin_name': plugin_name,
                    'status': 'collecting' if result['decision'] == 'need_information' else 'ready',
                    'original_intent': original_intent,
                    'confirmation_answers': confirmation_answers,
                    'normalized_request': result['normalized_request'],
                    'missing_information': result['missing_information'],
                    'explicit_plugin_request': explicit_plugin_request,
                    **runtime_meta,
                }
                cfg['plugin_preflight_context'] = snapshot
                _emit_preflight_snapshot(snapshot)
                if result['decision'] == 'need_information':
                    cfg.pop('prepared_plugin', None)
                    return json.dumps({
                        'status': 'need_information',
                        'outcome': 'need_information',
                        'reason': result['reason'],
                        'missing_information': result['missing_information'],
                    }, ensure_ascii=False)

                static_advancement = plugin_mode == 'auto'
                launch_plan: Dict[str, Any] = {
                    'first_step_id': result['first_step_id'],
                    'normalized_request': result['normalized_request'],
                }
                if static_advancement:
                    launch_plan.update({
                        'hand_off': True,
                        'advance_tool': 'advance_step_and_hand_off',
                    })
                step_name_index = _build_step_name_index(resolved_plugin_id)
                first_step_default_approval = (
                    'required'
                    if plugin_loader.get_step_mode(
                        resolved_plugin_id, result['first_step_id']
                    ) == 'human'
                    else 'not_required'
                )
                prepared = {
                    **snapshot,
                    'must_advance': True,
                    'advance_committed': False,
                    'requires_hand_off_choice': not static_advancement,
                    'fallback_hand_off': first_step_default_approval == 'required',
                    'step_name_index': step_name_index,
                    'launch_plan': launch_plan,
                    'scenario': resolved_spec.scenario_md,
                }
                cfg['prepared_plugin'] = prepared
                cfg.update(runtime_meta)
                visible_launch_plan = dict(launch_plan)
                if static_advancement:
                    visible_launch_plan.pop('hand_off', None)
                    instruction = (
                        'You MUST now call the advancement tool named by launch_plan in this '
                        'same turn. Do not answer with prose first.'
                    )
                else:
                    instruction = (
                        'You MUST now choose `advance_step` or `advance_step_and_hand_off` '
                        'for first_step_id using the current request policy, step-name index, '
                        'and first-step default approval. Do not answer with prose first.'
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
            if plugin_when_to_use:
                tool_desc = f'{plugin_when_to_use.rstrip(".")}.  ({plugin_desc.rstrip(".")})'
            else:
                tool_desc = plugin_desc
            _trigger.__doc__ = (
                f'{tool_desc}\n\n'
                'Args:\n'
                '    request_context (str): The complete user goal. When clarification has\n'
                '        occurred, consolidate the original request and all answers.\n\n'
                '    explicit_plugin_request (bool): Always supply this flag. Set true when the user explicitly names,\n'
                '        starts, enables, or asks to run this plugin. Explicit selection cannot\n'
                '        be rejected as not_applicable.\n\n'
                'Returns:\n'
                '    A structured preflight result. This tool never starts the plugin.\n'
                '    When status is ready, immediately call an advance tool in the same turn.'
            )
            return _trigger

        tools.append(_make_trigger(pid, name, first_steps, desc, when_to_use, catalog_entry, public_tool_name))
    return tools


def _commit_prepared_plugin(
    step_id: str,
    *,
    hand_off: bool,
    wait_for_result: bool = True,
) -> str:
    """Consume a ready preflight and emit the first cold-start task."""
    cfg = _agentic_config()
    prepared = cfg.get('prepared_plugin')
    if not isinstance(prepared, dict) or not prepared.get('must_advance'):
        raise ValueError('No ready plugin preflight. Call the matching trigger tool first.')
    if prepared.get('advance_committed'):
        raise ValueError('The prepared plugin has already been advanced.')
    plan = prepared.get('launch_plan') or {}
    expected_step = str(plan.get('first_step_id') or '')
    if step_id != expected_step:
        raise ValueError(f'First step must be {expected_step!r}, got {step_id!r}.')
    expected_hand_off = bool(plan.get('hand_off', True))
    if isinstance(plan.get('hand_off'), bool) and hand_off != expected_hand_off:
        expected_tool = 'advance_step_and_hand_off' if expected_hand_off else 'advance_step'
        raise ValueError(f'Launch plan requires {expected_tool}.')
    plugin_id = str(prepared.get('plugin_id') or '')
    normalised_request = str(plan.get('normalized_request') or '').strip()
    preflight_id = str(prepared.get('preflight_id') or '')
    result = _trigger_plugin_step(
        plugin_id,
        step_id,
        normalised_request,
        is_cold_start=True,
        hand_off=hand_off,
        preflight_id=preflight_id,
    )
    if not cfg.get('_last_plugin_transition_accepted', False):
        if hand_off:
            raise RuntimeError(result)
        return result
    prepared['advance_committed'] = True
    cfg['prepared_plugin'] = prepared
    if hand_off or not wait_for_result:
        return result

    task_id = str(cfg.get('_last_plugin_task_id') or '')
    if not task_id:
        raise RuntimeError('Cold-start task id was not recorded.')
    session_id = str(cfg.get('plugin_session_id') or '')
    if not session_id:
        raise RuntimeError('Go accepted cold start without a plugin session id.')
    cfg.update({
        'plugin_id': plugin_id,
        'plugin_session_id': session_id,
        'plugin_step': step_id,
    })
    summary = _wait_for_go_task(step_id, result)
    spec = plugin_loader.get_plugin(plugin_id)
    if spec is None:
        raise RuntimeError(f'Plugin {plugin_id!r} disappeared after launch was prepared.')
    labels = {
        sid: scfg.get('label', '')
        for sid, scfg in (spec._steps or {}).items()
        if scfg.get('label')
    }
    return _append_step_transition_hint(
        summary,
        plugin_id=plugin_id,
        current_step=step_id,
        rewind_steps=[],
        step_labels=labels,
    ) + '\n\n---\nPlugin scenario:\n' + str(prepared.get('scenario') or '')


def build_cold_advance_tools(plugin_mode: str = 'dynamic') -> List[Any]:
    """Build only the cold-start advance tools allowed by the current policy."""

    def advance_step(step_id: str) -> str:
        """Start the prepared plugin and wait for its first step to finish.

        Use after a ready trigger when current request policy calls for synchronous continuation.

        Args:
            step_id: The launch_plan.first_step_id returned by trigger.

        Returns:
            The first step result and live next-step guidance.
        """
        cfg = _agentic_config()
        if cfg.get('plugin_session_id') and cfg.get('plugin_id'):
            prepared = cfg.get('prepared_plugin') or {}
            plan = prepared.get('launch_plan') or {}
            return build_advance_step_tool(
                str(cfg['plugin_id']), str(cfg.get('plugin_step') or '')
            )(
                step_id=step_id,
                user_input=str(plan.get('normalized_request') or cfg.get('query') or ''),
            )
        return _commit_prepared_plugin(step_id, hand_off=False)

    def advance_step_and_hand_off(step_id: str) -> str:
        """Start the prepared plugin and hand control off immediately.

        Use after a ready trigger when current request policy calls for an asynchronous boundary.

        Args:
            step_id: The launch_plan.first_step_id returned by trigger.

        Returns:
            Confirmation that the first plugin step was queued.
        """
        cfg = _agentic_config()
        if cfg.get('plugin_session_id') and cfg.get('plugin_id'):
            prepared = cfg.get('prepared_plugin') or {}
            plan = prepared.get('launch_plan') or {}
            return build_advance_step_and_hand_off_tool(
                str(cfg['plugin_id']), str(cfg.get('plugin_step') or '')
            )(
                step_id=step_id,
                user_input=str(plan.get('normalized_request') or cfg.get('query') or ''),
            )
        return _commit_prepared_plugin(step_id, hand_off=True)

    if plugin_mode == 'auto':
        return [advance_step_and_hand_off]
    return [advance_step, advance_step_and_hand_off]


def commit_prepared_plugin_fallback() -> str:
    """Deterministically emit the launch plan after the ChatAgent skipped advance twice."""
    prepared = _agentic_config().get('prepared_plugin') or {}
    plan = prepared.get('launch_plan') or {}
    planned_hand_off = plan.get('hand_off')
    hand_off = (
        planned_hand_off
        if isinstance(planned_hand_off, bool)
        else bool(prepared.get('fallback_hand_off', True))
    )
    return _commit_prepared_plugin(
        str(plan.get('first_step_id') or ''),
        hand_off=hand_off,
        wait_for_result=False,
    )


def _should_suppress_prepared_plugin_text(event: Any) -> bool:
    """Return whether prose must be held until a ready launch plan is committed."""
    prepared = _agentic_config().get('prepared_plugin')
    return bool(
        isinstance(event, dict)
        and event.get('tag') == 'text'
        and isinstance(prepared, dict)
        and prepared.get('must_advance')
        and not prepared.get('advance_committed')
    )


async def _enforce_prepared_plugin_advance(
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

    ChatService owns generic agent streaming. This helper owns the plugin-specific
    invariant: one forced ReAct retry, followed by deterministic launch-plan commit.
    """
    prepared = _agentic_config().get('prepared_plugin')
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
        '[plugin.advance] mandatory retry plan=%s',
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
            '## Mandatory plugin launch correction\n'
            'The plugin trigger already returned ready. Do not answer, explain, confirm, '
            'or ask another question. Immediately start first_step_id. Choose between '
            '`advance_step` and `advance_step_and_hand_off` from the latest user request, '
            'the compact step-name index, and the first-step default approval. A requested '
            'confirmation at a later named boundary does not require handing off the first '
            'step. Launch plan:\n'
            + json.dumps(visible_launch_plan, ensure_ascii=False)
            + '\n'
            + str(prepared.get('step_name_index') or '')
        )
    else:
        correction = (
            '## Mandatory plugin launch correction\n'
            'The plugin trigger already returned ready. Do not answer, explain, '
            'confirm, or ask another question. Immediately execute this launch plan '
            'using the advancement tool named by this plan exactly as specified:\n'
            + json.dumps(visible_launch_plan, ensure_ascii=False)
        )
    retry_prompt = (
        PromptBuilder.for_role(AgentRole.CHAT)
        .runtime(
            'plugin_launch_correction', 'Mandatory Plugin Launch Correction', correction,
            'plugin.runtime',
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
        if kind == 'event' and _should_suppress_prepared_plugin_text(payload):
            continue
        yield kind, payload

    prepared = _agentic_config().get('prepared_plugin')
    if not (
        isinstance(prepared, dict)
        and prepared.get('must_advance')
        and not prepared.get('advance_committed')
    ):
        return

    LOG.error('[plugin.advance] deterministic prepared-plan fallback')
    try:
        final_result = commit_prepared_plugin_fallback()
        # The fallback runs outside StreamCallHelper, so expose its task event
        # through the same generic event path consumed by ChatService.
        for raw_event in lazyllm.FileSystemQueue().dequeue():
            yield 'event', json.loads(raw_event)
        yield 'final', final_result
    except Exception as exc:
        LOG.exception('[plugin.advance] deterministic fallback failed')
        yield 'final', f'PLUGIN_START_FAILED: {exc}'


async def guard_plugin_agent_stream(
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
    """Wrap the normal ChatAgent stream with the plugin launch invariant."""
    async for kind, payload in initial_stream:
        if kind == 'event' and _should_suppress_prepared_plugin_text(payload):
            continue
        yield kind, payload

    async for item in _enforce_prepared_plugin_advance(
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
    plugin_id: str,
    fallback_current_step: str,
    rewind_steps: Optional[List[str]] = None,
) -> _ReachabilitySnapshot:
    """Read Ready/Past from Go without a local graph fallback."""
    cfg = _agentic_config()
    current_step = cfg.get('plugin_step', '') or fallback_current_step
    session_id = cfg.get('plugin_session_id', '')
    forward_steps: List[str] = []
    rewind = list(rewind_steps or [])
    projection: Dict[str, Any] = {}
    if session_id:
        projection = _fetch_go_projection(session_id)
        forward_steps = list(projection.get('ready') or [])
        rewind = list(projection.get('past') or [])
    nodes = projection.get('nodes') if isinstance(projection.get('nodes'), dict) else {}
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
    plugin_id: str,
    current_step: str,
    rewind_steps: Optional[List[str]] = None,
    step_labels: Optional[Dict[str, str]] = None,
    include_approval_guidance: bool = True,
) -> Any:
    """Build the advance_step_and_hand_off tool (stop-tool).

    Queues the step asynchronously and immediately ends the current ReAct turn.
    Mode-specific continuation behavior is defined by the system guidance.
    """
    snapshot = _live_reachability_snapshot(plugin_id, current_step, rewind_steps)
    forward = snapshot.forward_steps
    rewind = snapshot.rewind_steps
    labels = step_labels or {}

    choices_doc = _build_step_choices_doc(
        forward,
        rewind,
        labels,
        plugin_id=plugin_id,
        current_step=current_step if current_step in snapshot.retry_steps else '',
        include_default_approval=include_approval_guidance,
    )

    def advance_step_and_hand_off(
        step_id: str,
        user_input: str,
        runtime_instruction: Optional[str] = None,
        partial_indices: Optional[Dict[str, List[int]]] = None,
    ) -> str:
        """Start the next step asynchronously and hand off subsequent control.

        After calling this tool, the current ReAct loop exits and the SSE stream closes.
        The step runs in the background. Mode-specific system guidance determines
        what happens after it completes.

        Use this when the user explicitly requests review/a boundary, or when the
        target step is annotated with default approval required. Use
        `advance_step` when approval is explicitly skipped or defaults to not required.

        Session completion is computed automatically by Go after all effective
        branches reach the graph end.
        """
        if step_id == '__end__':
            raise ValueError('Manual __end__ transitions are disabled; Go computes session completion.')
        result = _trigger_plugin_step(
            plugin_id, step_id, user_input,
            is_cold_start=False,
            hand_off=True,
            runtime_instruction=runtime_instruction or '',
            partial_indices=partial_indices or {},
            operation='advance',
        )
        # advance_step_and_hand_off remains a static stop-tool for compatibility.
        # Raising turns a Go rejection into an ok=false tool observation, so the
        # ReAct loop continues and the model sees the exact structured reason.
        if not _agentic_config().get('_last_plugin_transition_accepted', False):
            raise RuntimeError(result)
        _set_local_plugin_step(step_id)
        return result

    selection_guidance = (
        'Use the current request policy to decide when this asynchronous boundary is required.\n'
        if include_approval_guidance
        else 'Use this tool to start the selected next step.\n'
    )
    advance_step_and_hand_off.__doc__ = (
        'Start the next plugin step asynchronously and end the current ReAct turn.\n\n'
        + selection_guidance
        + 'Terminal steps are also boundaries; after a\n'
        'terminal task succeeds, the plugin event loop completes the session.\n\n'
        '## Running an earlier step again\n\n'
        'If the user expresses dissatisfaction with or changes to the result of a step that\n'
        'has ALREADY run, select the earliest affected step_id. The backend automatically\n'
        'decides whether the target is a normal advance, retry, or rewind.\n\n'
        'Examples:\n'
        '  User: "我不喜欢日系风格，改成北欧简约风" → the style was set in an earlier step\n'
        '    → advance_step_and_hand_off(step_id=<that_step>,\n'
        '        user_input="北欧简约风格，...")\n'
        '  User: "不要树，改成蓝天白云" → subject was defined in analyze_subject\n'
        '    → advance_step_and_hand_off(step_id="analyze_subject",\n'
        '        user_input="主体：蓝天白云...")\n\n'
        '## Checkpoint-Resume (interrupted steps)\n\n'
        'When the user says "继续" and the step was interrupted (not "重试"):\n'
        '  advance_step_and_hand_off(step_id=<current_step>, runtime_instruction=(\n'
        '    "Previous attempt was interrupted. Check existing artifacts for this step "\n'
        '    "and only produce missing outputs (resume from checkpoint). "\n'
        '    "Do not regenerate already-saved artifacts."))\n'
        'When the user says "重试", select that same step_id and describe the requested\n'
        'restart behavior in runtime_instruction.\n\n'
        '## Completing the plugin\n\n'
        'Hand off the terminal pipeline step itself. Go automatically marks the session\n'
        'complete when all effective branches finish; never submit `__end__`.\n\n'
        'If the DriverAgent or user indicates a prior step produced bad output, simply pass\n'
        'that step_id again. Do not reason about backend lifecycle operation names.\n\n'
        + choices_doc + '\n\n'
        'Args:\n'
        '    step_id (str): Step to advance to (see list above).\n'
        '    user_input (str): Concise goal statement for the SubAgent based on the latest\n'
        '        user query only. Do NOT pass vague phrases like "继续" or "continue", and\n'
        '        do NOT include prior-turn context unless the user explicitly repeats it.\n'
        '    runtime_instruction (str, optional): Ephemeral directive for this run only.\n'
        '    partial_indices (dict, optional): Maps slot → list_index values to\n'
        '        overwrite (list-cardinality slots only).\n\n'
        'Returns:\n'
        '    Confirmation that the step was queued. Exits ReAct immediately after.'
    )
    return advance_step_and_hand_off


def build_advance_step_tool(
    plugin_id: str,
    current_step: str,
    rewind_steps: Optional[List[str]] = None,
    step_labels: Optional[Dict[str, str]] = None,
) -> Any:
    """Build the synchronous advance_step tool for policies that allow it.

    Blocks until the SubAgent completes, then returns the step result summary so
    ChatAgent can continue reasoning. Use for explicit continuous execution and
    for steps whose default approval is not required.
    """
    snapshot = _live_reachability_snapshot(plugin_id, current_step, rewind_steps)
    forward = snapshot.forward_steps
    rewind = snapshot.rewind_steps
    labels = step_labels or {}

    choices_doc = _build_step_choices_doc(
        forward, rewind, labels, plugin_id=plugin_id,
        current_step=current_step if current_step in snapshot.retry_steps else '',
    )

    def advance_step(
        step_id: str,
        user_input: str,
        runtime_instruction: Optional[str] = None,
        partial_indices: Optional[Dict[str, List[int]]] = None,
    ) -> str:
        """Advance the active plugin to the next step and WAIT for completion.

        Blocks until the SubAgent finishes, then returns the step result summary.
        Use when the user explicitly requests continuous/no-approval execution, or
        when the target step defaults to no approval and the user has not overridden it.
        """
        if step_id == '__end__':
            raise ValueError('Manual __end__ transitions are disabled; Go computes session completion.')
        result = _trigger_plugin_step(
            plugin_id, step_id, user_input,
            is_cold_start=False,
            runtime_instruction=runtime_instruction or '',
            partial_indices=partial_indices or {},
            operation='advance',
        )
        if not _agentic_config().get('_last_plugin_transition_accepted', False):
            return result
        task_id = str(_agentic_config().get('_last_plugin_task_id') or '')
        # Keep only a conversational focus hint. It is not a runtime state fact;
        # parallel Current/Ready sets always come from Go's projection.
        _set_local_plugin_step(step_id)
        LOG.info(
            '[plugin.advance] local current_step updated plugin=%s step=%s session=%s task=%s',
            plugin_id, step_id, _agentic_config().get('plugin_session_id', ''), task_id,
        )
        LOG.info(
            '[plugin.advance] polling Go task plugin=%s step=%s session=%s task=%s',
            plugin_id, step_id, _agentic_config().get('plugin_session_id', ''), task_id,
        )
        summary = _wait_for_go_task(step_id, result)
        LOG.info(
            '[plugin.advance] advance_step completed plugin=%s step=%s session=%s task=%s summary_len=%d',
            plugin_id, step_id, _agentic_config().get('plugin_session_id', ''), task_id, len(summary or ''),
        )
        return _append_step_transition_hint(
            summary,
            plugin_id=plugin_id,
            current_step=step_id,
            rewind_steps=rewind_steps or [],
            step_labels=labels,
        )

    advance_step.__doc__ = (
        'Advance the active plugin step synchronously and return the result.\n\n'
        'Use this tool in continuous/uninterrupted mode, or when the target step is\n'
        'annotated `[default approval: not required]` and the user did not override it.\n'
        'Continuous mode is active when the user intent contains phrases like\n'
        '"一次性完成", "不要中断", "一次性写完", "run all steps", "no interruptions".\n'
        'In continuous mode with an explicit target boundary, use `advance_step` only\n'
        'for prerequisite steps before that boundary, then execute the boundary step\n'
        'with `advance_step_and_hand_off` and stop. If the user did not set a boundary,\n'
        'run prerequisite remaining steps with this tool, then execute the terminal step\n'
        'with `advance_step_and_hand_off` and stop.\n\n'
        'If the target step defaults to approval, or the user asks to review/confirm it,\n'
        'use `advance_step_and_hand_off` instead.\n\n'
        + choices_doc + '\n\n'
        'Args:\n'
        '    step_id (str): Step to advance to (see list above).\n'
        '    user_input (str): Concise goal statement from the latest user query only.\n'
        '    runtime_instruction (str, optional): Ephemeral directive for this run.\n'
        '    partial_indices (dict, optional): List-slot overwrite indices.\n\n'
        'Returns:\n'
        '    Step result summary after SubAgent completes.'
    )
    return advance_step


def build_advance_steps_and_hand_off_tool(
    plugin_id: str,
    current_step: str,
    rewind_steps: Optional[List[str]] = None,
    step_labels: Optional[Dict[str, str]] = None,
) -> Any:
    """Build the atomic asynchronous batch-advance stop tool."""
    forward = _live_reachability_snapshot(plugin_id, current_step, rewind_steps).forward_steps
    choices_doc = _build_step_choices_doc(
        forward, [], step_labels or {}, plugin_id=plugin_id, current_step=current_step,
    )

    def advance_steps_and_hand_off(steps: List[Dict[str, Any]]) -> str:
        """Atomically queue multiple currently-Ready steps and end this ReAct turn."""
        submission = _trigger_plugin_steps(plugin_id, steps, hand_off=True)
        if not submission.accepted:
            raise RuntimeError(submission.message)
        return submission.message

    advance_steps_and_hand_off.__doc__ = (
        'Atomically start two or more independent Ready plugin steps and end the current turn.\n\n'
        'Use one call when Go reports multiple Ready steps that should start now. Go evaluates all\n'
        'items against the same projection and either queues every item or rejects the entire batch.\n'
        'Never include a downstream step that needs an output from another item in this batch.\n'
        'Previously attempted targets are not supported in batches; use a single-step tool.\n\n'
        + choices_doc + '\n\n'
        'Args:\n'
        '    steps: At least two objects. Each object must contain step_id and user_input, and may\n'
        '        contain runtime_instruction and partial_indices. Give every step its own focused\n'
        '        instruction; do not combine instructions for different steps.\n\n'
        'Returns:\n'
        '    One durable acceptance for all steps. Exits ReAct only after Go accepts the full batch.'
    )
    return advance_steps_and_hand_off


def build_advance_steps_tool(
    plugin_id: str,
    current_step: str,
    rewind_steps: Optional[List[str]] = None,
    step_labels: Optional[Dict[str, str]] = None,
) -> Any:
    """Build the atomic synchronous batch-advance tool."""
    forward = _live_reachability_snapshot(plugin_id, current_step, rewind_steps).forward_steps
    choices_doc = _build_step_choices_doc(
        forward, [], step_labels or {}, plugin_id=plugin_id, current_step=current_step,
    )

    def advance_steps(steps: List[Dict[str, Any]]) -> str:
        """Atomically start multiple Ready steps and wait for every task result."""
        submission = _trigger_plugin_steps(plugin_id, steps, hand_off=False)
        if not submission.accepted:
            return submission.message
        summaries: List[str] = []
        cfg = _agentic_config()
        for task in submission.tasks or []:
            step_id = str(task.get('step_id') or '')
            task_id = str(task.get('task_id') or '')
            if not step_id or not task_id:
                continue
            cfg['_last_plugin_task_id'] = task_id
            result = _wait_for_go_task(step_id, submission.message)
            summaries.append(f'## {step_id}\n{result}')
        cfg['_last_plugin_tasks'] = submission.tasks or []
        if not summaries:
            return submission.message
        return '\n\n'.join(summaries) + _append_step_transition_hint(
            '', plugin_id=plugin_id, current_step='', rewind_steps=rewind_steps or [],
            step_labels=step_labels or {},
        )

    advance_steps.__doc__ = (
        'Atomically start two or more independent Ready plugin steps and wait for all results.\n\n'
        'Prefer this over repeated advance_step calls whenever the authoritative Ready list contains\n'
        'multiple steps that should run now. The batch is all-or-rejected and increments state_version\n'
        'once. Never batch a downstream dependency or a previously attempted target.\n\n'
        + choices_doc + '\n\n'
        'Args:\n'
        '    steps: At least two objects. Each object contains step_id, user_input, and optional\n'
        '        runtime_instruction / partial_indices specific to that step.\n\n'
        'Returns:\n'
        '    Per-step results after every task in the accepted batch reaches a terminal state.'
    )
    return advance_steps


def _append_step_transition_hint(
    summary: str,
    plugin_id: str,
    current_step: str,
    rewind_steps: List[str],
    step_labels: Dict[str, str],
) -> str:
    """Append live transition guidance to advance_step's tool result."""
    snapshot = _live_reachability_snapshot(plugin_id, current_step, rewind_steps)
    # Only expose retry option when Go's projection confirms the step is retryable
    # (i.e. its last execution was failed or interrupted). A just-succeeded step
    # must NOT appear as a Retry candidate — the LLM should advance forward instead.
    retryable_step = current_step if current_step in snapshot.retry_steps else ''
    choices_doc = _build_step_choices_doc(
        snapshot.forward_steps,
        snapshot.rewind_steps,
        step_labels,
        plugin_id=plugin_id,
        current_step=retryable_step,
    )
    return (
        f'{summary}\n\n'
        '---\n'
        'Plugin state after this step:\n'
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


def _set_local_plugin_step(step_id: str) -> None:
    """Update the ChatAgent display focus after Go accepts a transition."""
    try:
        lazyllm.globals['agentic_config']['plugin_step'] = step_id
    except Exception as exc:
        LOG.warning('[plugin.advance] failed to update local plugin_step step=%s error=%s', step_id, exc)


def _wait_for_go_task(step_id: str, trigger_result: str, timeout: float = 600.0) -> str:
    """Poll Go's persisted task status after transition acceptance."""
    import time
    try:
        import httpx
        from lazymind.config import config as _cfg
        cfg = _agentic_config()
        session_id = cfg.get('plugin_session_id', '')
        task_id = str(cfg.get('_last_plugin_task_id') or '')
        if not task_id:
            return trigger_result
        core_url = str(_cfg['core_api_url']).rstrip('/')
        deadline = time.monotonic() + timeout
        LOG.info(
            '[plugin.advance] polling Go task step=%s session=%s task=%s timeout=%.0fs',
            step_id, session_id, task_id, timeout,
        )
        while time.monotonic() < deadline:
            response = httpx.get(f'{core_url}/internal/subagent/tasks/{task_id}', timeout=5.0)
            if response.status_code == 200:
                data = _core_response_data(response)
                status = str(data.get('status') or '')
                if status in {'succeeded', 'failed', 'interrupted', 'canceled'}:
                    summary = str(data.get('summary') or '')
                    return summary or f"Step '{step_id}' finished with status {status}."
            time.sleep(2.0)
        return f"Step '{step_id}' was accepted and is still running after {timeout:.0f}s. Task id: {task_id}."
    except Exception as exc:
        LOG.warning('[plugin.advance] Go task polling failed step=%s error=%s', step_id, exc)
        return trigger_result


def update_intentwriter(tool: Any, plugin_context: Optional[Dict[str, Any]]) -> Any:
    """Extend a conversation IntentWriter with active-plugin scopes.

    ChatService owns the base tool. Plugin internals stay here: ChatService does
    not inspect step ids, DAG state, or plugin lifecycle.
    """
    if not isinstance(plugin_context, dict):
        return tool
    session_id = str(plugin_context.get('session_id') or '').strip()
    plugin_id = str(plugin_context.get('plugin_id') or '').strip()
    spec = plugin_loader.get_plugin(plugin_id) if session_id and plugin_id else None
    if not spec:
        return tool
    return enable_plugin_intent_scopes(
        tool,
        session_id=session_id,
        plugin_id=plugin_id,
        valid_step_ids=list(spec._steps.keys()),
    )


# ---------------------------------------------------------------------------
# Schedule management tools (ChatAgent only, always available)
# ---------------------------------------------------------------------------
# Read-only query tools (ChatAgent only, active session required)
# ---------------------------------------------------------------------------

def build_query_tools() -> List[Any]:
    """Build read-only plugin state query tools for ChatAgent."""

    def list_plugin_steps(session_id: Optional[str] = None) -> str:
        """List all steps and their current status in the active plugin session.

        Use this when the user asks "where are we in the pipeline" or
        "which steps are done / failed".  Read-only — does not trigger execution.
        """
        cfg = _agentic_config()
        sid = session_id or cfg.get('plugin_session_id', '')
        if not sid:
            return 'No active plugin session.'
        try:
            import httpx
            from lazymind.config import config as _cfg
            core_url = str(_cfg['core_api_url']).rstrip('/')
            resp = httpx.get(f'{core_url}/plugin-sessions/{sid}', timeout=5.0)
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
            lines = ['## Plugin session steps']
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
        session_id = cfg.get('plugin_session_id', '')
        if not session_id:
            return 'No active plugin session.'
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
        session_id = cfg.get('plugin_session_id', '')
        if not session_id:
            return 'No active plugin session.'
        try:
            import httpx
            from lazymind.config import config as _cfg
            core_url = str(_cfg['core_api_url']).rstrip('/')
            resp = httpx.get(f'{core_url}/plugin-sessions/{session_id}', timeout=5.0)
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

    return [list_plugin_steps, get_step_result, get_failed_steps]


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
    lines = TaskQueryDB().format_plugin_session_artifacts(session_id)
    if not lines:
        return ''
    # Replace the generic header with a plugin-specific one that warns against re-running steps.
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
            'preflight_id', 'plugin_id', 'plugin_name', 'status', 'original_intent',
            'confirmation_answers', 'normalized_request', 'missing_information',
        )
        if preflight.get(key) not in (None, '', [], {})
    }
    if not visible:
        return ''
    return (
        '## Plugin Preflight Context [AUTHORITATIVE]\n'
        'This durable snapshot survives history compaction. Preserve original_intent, '
        'merge new answers into normalized_request, and pass the consolidated result to '
        'trigger_<plugin>(request_context).\n'
        + json.dumps(visible, ensure_ascii=False, indent=2)
    )


def _build_cold_execution_policy(plugin_mode: str) -> str:
    """Return request-local guidance for choosing the first advancement tool."""
    if plugin_mode == 'auto':
        return (
            '## Current Plugin Launch Policy [AUTHORITATIVE]\n'
            'After a trigger returns ready, call the only available advancement tool named '
            'in launch_plan. Do not make an approval or continuation decision.'
        )
    return (
        '## Current Plugin Launch Policy [AUTHORITATIVE]\n'
        'After a trigger returns ready, it provides a compact index of every plugin step, '
        'the valid first step, and that first step\'s default approval. Match any user-named '
        'target boundary against the full id/name index. The index contains names only and '
        'does not imply order or reachability.\n'
        '- If the requested boundary is the first step, use `advance_step_and_hand_off`.\n'
        '- If the user requests continuous execution to a different named boundary, use '
        '`advance_step` for the first step. A request to confirm at that later boundary must '
        'not hand off the first step.\n'
        '- Otherwise explicit approval/continuation intent wins; when absent, use the first '
        'step\'s default approval.\n'
        'Always start only the first_step_id returned by the trigger. After each synchronous '
        '`advance_step` result, use only the newly returned reachable-step details and repeat '
        'the decision. Continue synchronously through prerequisites; when the named boundary '
        'itself becomes a valid target, start it with `advance_step_and_hand_off` and stop.'
    )


def resolve_plugin_injection(
    plugin_context: Optional[Dict[str, Any]],
    conversation_id: str = '',
    plugin_catalog: Optional[List[Dict[str, Any]]] = None,
    disabled_builtin_plugins: Optional[List[str]] = None,
    allowed_plugin_refs: Optional[List[str]] = None,
) -> PluginAgentContribution:
    """Resolve plugin tools, system prompt, stop-tools and agentic_config patches.

    Called once per request from handle_chat.  Encapsulates all plugin-context
    branching so chat_service stays free of plugin-internal details.

    Note: schedule tools and SubAgent task context are intentionally NOT injected
    here — they are handled independently in chat_service.py so that schedule
    availability and task context visibility are not affected by enable_plugin.

    Returns a structured contribution containing tools, stable plugin policy,
    stop tools, runtime config patches, and request-local runtime context.
    """
    plugin_tools: List[Any] = []
    plugin_system_prompt: str = ''
    plugin_stop_tools: List[str] = []
    agentic_config_patch: Dict[str, Any] = {}
    plugin_artifact_context: str = ''

    # Honour enable_plugin=false: skip all plugin tooling and fall back to pure QA.
    cfg = _agentic_config()
    if not cfg.get('enable_plugin', True):
        return PluginAgentContribution(
            plugin_tools, plugin_system_prompt, plugin_stop_tools,
            agentic_config_patch, plugin_artifact_context,
        )

    if not plugin_loader._registry:
        # No plugins registered — return empty; task context is injected by chat_service.
        return PluginAgentContribution(
            plugin_tools, plugin_system_prompt, plugin_stop_tools,
            agentic_config_patch, plugin_artifact_context,
        )

    # Resolve plugin_mode from plugin_context (injected by Go).
    plugin_mode = 'dynamic'
    if plugin_context and isinstance(plugin_context, dict):
        pm = plugin_context.get('plugin_mode', '')
        if pm in ('auto', 'dynamic'):
            plugin_mode = pm
    agentic_config_patch['plugin_mode'] = plugin_mode
    if plugin_context and isinstance(plugin_context, dict):
        preflight_context = plugin_context.get('plugin_preflight')
        if isinstance(preflight_context, dict):
            agentic_config_patch['plugin_preflight_context'] = preflight_context

    if plugin_context and isinstance(plugin_context, dict):
        p_session_id = plugin_context.get('session_id', '')
        p_plugin_id = plugin_context.get('plugin_id', '')
        p_current_step = plugin_context.get('current_step', '')

        if p_session_id and p_plugin_id:
            if plugin_context.get('plugin_ref') and not plugin_loader.get_plugin(p_plugin_id):
                _, restored_spec = plugin_loader.resolve_remote_plugin({
                    **plugin_context,
                    'plugin_id': p_plugin_id,
                })
                plugin_loader._registry[p_plugin_id] = restored_spec
            agentic_config_patch.update({
                'plugin_id': p_plugin_id,
                'plugin_session_id': p_session_id,
                'plugin_step': p_current_step,
                'plugin_mode': plugin_mode,
                'plugin_ref': plugin_context.get('plugin_ref'),
                'revision_id': plugin_context.get('revision_id'),
                'revision_no': plugin_context.get('revision_no'),
                'tree_hash': plugin_context.get('tree_hash'),
                'remote_root': plugin_context.get('remote_root'),
                'focused_tab': plugin_context.get('focused_tab'),
                'focused_sort_order': plugin_context.get('focused_sort_order'),
            })
            projection = _fetch_go_projection(p_session_id)
            projected_current = list(projection.get('current') or [])
            if p_current_step not in projected_current:
                p_current_step = projected_current[0] if projected_current else ''
                agentic_config_patch['plugin_step'] = p_current_step
            rewind_steps = list(projection.get('past') or [])

            step_labels: Dict[str, str] = {}
            spec = plugin_loader.get_plugin(p_plugin_id)
            if spec:
                for sid, scfg in spec._steps.items():
                    lbl = scfg.get('label', '')
                    if lbl:
                        step_labels[sid] = lbl

            # Build plugin tools according to plugin_mode.
            # advance_step_and_hand_off is always registered (stop-tool).
            # advance_step (sync) is only registered in dynamic mode.
            plugin_tools = [
                build_advance_step_and_hand_off_tool(
                    p_plugin_id, p_current_step,
                    rewind_steps=rewind_steps,
                    step_labels=step_labels,
                    include_approval_guidance=plugin_mode != 'auto',
                ),
                build_advance_steps_and_hand_off_tool(
                    p_plugin_id, p_current_step,
                    rewind_steps=rewind_steps,
                    step_labels=step_labels,
                ),
            ]
            plugin_stop_tools = ['advance_step_and_hand_off', 'advance_steps_and_hand_off']

            if plugin_mode == 'dynamic':
                plugin_tools.extend([
                    build_advance_step_tool(
                        p_plugin_id, p_current_step,
                        rewind_steps=rewind_steps,
                        step_labels=step_labels,
                    ),
                    build_advance_steps_tool(
                        p_plugin_id, p_current_step,
                        rewind_steps=rewind_steps,
                        step_labels=step_labels,
                    ),
                ])

            # Read-only query tools (active session required).
            plugin_tools.extend(build_query_tools())

            # save_plugin_artifact lets ChatAgent write an artifact directly.
            from lazymind.chat.engine.tools.subagent_chat_tools import save_plugin_artifact
            plugin_tools.append(save_plugin_artifact)

            plugin_system_prompt = plugin_loader.get_scenario(p_plugin_id)
            plugin_artifact_context = _build_session_artifact_section(p_session_id)

            # All step names stay compact and graph-free. Detailed conditions,
            # routing and approval metadata remain limited to live reachable steps.
            step_name_index = _build_step_name_index(p_plugin_id)
            if step_name_index:
                plugin_artifact_context = (
                    plugin_artifact_context + '\n\n' + step_name_index
                ).strip()

            # Inject intent/constraints into the artifact context (user-turn injection).
            intent_section = _build_intent_section(p_session_id, step_id=p_current_step)
            if intent_section:
                plugin_artifact_context = (plugin_artifact_context + '\n\n' + intent_section).strip()

            # Inject authoritative step execution status (user-turn injection).
            step_status_section = _build_step_status_section(
                p_plugin_id, p_session_id, p_current_step,
                rewind_steps, step_labels=step_labels,
            )
            if step_status_section:
                plugin_artifact_context = (plugin_artifact_context + '\n\n' + step_status_section).strip()

            # Inject the current execution policy into this request only. Keeping
            # it in plugin_artifact_context (rather than the system prompt/history)
            # makes configuration changes take effect on the next chat turn.
            mode_guidance = _build_mode_guidance(plugin_mode)
            if mode_guidance:
                plugin_artifact_context = (
                    plugin_artifact_context + '\n\n' + mode_guidance
                ).strip()
        else:
            # Cold start: no active session yet
            triggers = build_cold_start_tools(
                plugin_catalog, disabled_builtin_plugins, allowed_plugin_refs,
            )
            plugin_tools = triggers + build_cold_advance_tools(plugin_mode)
            plugin_stop_tools = ['advance_step_and_hand_off']
            plugin_artifact_context = _build_preflight_context_section(
                agentic_config_patch.get('plugin_preflight_context')
            )
            cold_policy = _build_cold_execution_policy(plugin_mode)
            plugin_artifact_context = (
                plugin_artifact_context + '\n\n' + cold_policy
            ).strip()
            if triggers:
                scenarios = [
                    plugin_loader.get_plugin_intro(spec.plugin_id)
                    for spec in (plugin_loader._registry or {}).values()
                    if (
                        spec.plugin_id not in set(disabled_builtin_plugins or [])
                        and (not allowed_plugin_refs or f'builtin:{spec.plugin_id}' in set(allowed_plugin_refs))
                        and not spec.plugin_id.startswith('user_')
                    )
                ]
                scenarios.extend(
                    _catalog_intro(entry) for entry in (plugin_catalog or [])
                    if not allowed_plugin_refs or str(entry.get('plugin_ref') or '') in set(allowed_plugin_refs)
                )
                plugin_system_prompt = (
                    _COLD_START_PLUGIN_PROMPT
                ) + '\n\n---\n\n'.join(s for s in scenarios if s)
    else:
        # No plugin_context provided: still inject cold-start triggers
        triggers = build_cold_start_tools(
            plugin_catalog, disabled_builtin_plugins, allowed_plugin_refs,
        )
        plugin_tools = triggers + build_cold_advance_tools(plugin_mode)
        plugin_stop_tools = ['advance_step_and_hand_off']
        plugin_artifact_context = _build_cold_execution_policy(plugin_mode)
        if triggers:
            scenarios = [
                plugin_loader.get_plugin_intro(spec.plugin_id)
                for spec in (plugin_loader._registry or {}).values()
                if (
                    spec.plugin_id not in set(disabled_builtin_plugins or [])
                    and (not allowed_plugin_refs or f'builtin:{spec.plugin_id}' in set(allowed_plugin_refs))
                    and not spec.plugin_id.startswith('user_')
                )
            ]
            scenarios.extend(
                _catalog_intro(entry) for entry in (plugin_catalog or [])
                if not allowed_plugin_refs or str(entry.get('plugin_ref') or '') in set(allowed_plugin_refs)
            )
            plugin_system_prompt = (
                _COLD_START_PLUGIN_PROMPT
            ) + '\n\n---\n\n'.join(s for s in scenarios if s)

    return PluginAgentContribution(
        plugin_tools, plugin_system_prompt, plugin_stop_tools,
        agentic_config_patch, plugin_artifact_context,
    )


def _catalog_intro(entry: Dict[str, Any]) -> str:
    lines = [f'## Plugin: {entry.get("plugin_id") or entry.get("name") or "plugin"}']
    if entry.get('description'):
        lines.append(str(entry['description']))
    if entry.get('when_to_use'):
        lines.append(f'When to use: {entry["when_to_use"]}')
    return '\n'.join(lines)


# ---------------------------------------------------------------------------
# Intent / constraint helpers
# ---------------------------------------------------------------------------

def _build_intent_section(session_id: str, step_id: Optional[str] = None) -> str:
    """Serialize plugin-session intent for ChatAgent prompt injection.

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
    plugin_id: str,
    session_id: str,
    current_step: str,
    rewind_steps: List[str],
    step_labels: Optional[Dict[str, str]] = None,
) -> str:
    # Build an authoritative snapshot of the pipeline execution state for this turn.
    # Injected into the user-turn prefix (not the system prompt) so it always reflects
    # the live DB state and overrides any stale information in chat history.
    if not session_id or not plugin_id:
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

        lines = ['## Plugin Step Status [AUTHORITATIVE — queried at request time]']
        lines.append('> Any step-status information in the conversation history is OUTDATED. Use only this section.')

        if current_step:
            lines.append(f'\nCurrent plugin step state: **{_label(current_step)}**')
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
                    'Decision hint: evaluate every natural-language `when` hint against the current '
                    'user intent. A hinted step is Reachable, not automatically selected. Batch only '
                    'the independent Ready steps that are simultaneously applicable; for N-select-1 '
                    'alternatives, advance only the selected step.'
                )

        return '\n'.join(lines)
    except Exception:
        return ''


def _build_mode_guidance(
        plugin_mode: str) -> str:
    """Return the request-local execution policy selected by application code."""
    if plugin_mode == 'auto':
        return (
            '## Current Plugin Execution Policy [AUTHORITATIVE]\n\n'
            'Only asynchronous advancement tools are available. Use '
            '`advance_steps_and_hand_off` exactly once when two or more independent Ready '
            'steps should start now; use `advance_step_and_hand_off` for one Ready step. '
            'Both tools end the current turn only after Go accepts the full command.\n'
            'After the step completes, the backend controller evaluates the result and '
            'starts the next decision turn. Do not wait for synchronous step results or ask '
            'the user questions during execution.'
        )

    global_rules = (
        '\n\n## Step decision rules (READ BEFORE EVERY ACTION)\n\n'
        '### Rule 0 — Intent capture from latest user query (highest priority)\n'
        'At the beginning of each plugin turn, compare the latest user query with the inherited intent.\n'
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
        'call `intentwrite(scope="plugin_session", operations=[...])`\n'
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
        '### Rule 3 — Approval precedence and workflow advancement\n'
        'Select the advancement tool with this priority:\n'
        '  1. Explicit intent in the latest query or persisted session intent wins. Match a\n'
        '     user-named target against the compact "Plugin Step Name Index". If that boundary\n'
        '     is a currently valid next step, use `advance_step_and_hand_off` for it. If it is\n'
        '     another known step and the user requests continuous execution until that boundary,\n'
        '     use `advance_step` or `advance_steps` for prerequisite Ready frontiers. Do NOT hand off an\n'
        '     intermediate step merely because the user requested confirmation at the later\n'
        '     boundary. If the user requests uninterrupted execution without a boundary, use\n'
        '     the singular or plural waiting tool according to the Ready frontier size.\n'
        '  2. If the user expresses no approval preference, read the target step\'s\n'
        '     `[default approval: ...]` annotation. Use a hand-off variant when approval is\n'
        '     required; use a waiting variant when it is not required. For multiple selected\n'
        '     Ready steps, use one plural tool; if any selected target needs an asynchronous\n'
        '     boundary, use `advance_steps_and_hand_off`.\n'
        'After an `advance_step` result, repeat this decision for the next target. This lets\n'
        'automatic steps continue until the workflow reaches a step that requires approval.\n\n'
        'If the user clearly asks to proceed with the existing plugin workflow and\n'
        'does not add new requirements, corrections, or dissatisfaction signals:\n'
        '  - If continuous mode is NOT active: apply the target step\'s default approval.\n'
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
        'Do NOT pass the current plugin step state unless it is explicitly listed\n'
        'as a valid forward or previously-attempted target.\n'
    )
    common = (
        '\n\n## Plugin execution guidance\n\n'
        'Tools for step advancement:\n'
        '- `advance_step_and_hand_off`: Start a step asynchronously and end the current turn.\n'
        '- `advance_steps_and_hand_off`: Atomically start multiple Ready steps and end the turn.\n'
    )
    common += (
        (
            'An asynchronous boundary returns the next decision to the user.\n'
            '- `advance_step`: Queue one step and WAIT for its result.\n'
            '- `advance_steps`: Atomically queue multiple Ready steps and WAIT for all results. '
            'Use this in continuous/uninterrupted mode (see Rule 4 below). '
            'Use `advance_step` for prerequisite steps before a requested boundary, then '
            '`advance_step_and_hand_off` for the boundary step.\n'
            'When there is no explicit approval preference, use the target step annotation: '
            '`advance_step_and_hand_off` for `[default approval: required]`, otherwise '
            '`advance_step` and evaluate the next target after it completes.\n\n'
            '### Rule 4 — Continuous / uninterrupted execution mode (MUST check before every action)\n'
            'Activate continuous mode when ANY of the following is true:\n'
            '  a) The "User Intent & Constraints" section contains phrases such as:\n'
            '     "一次性完成", "一次性写完", "不要中断", "不要打断", "中间不要停",\n'
            '     "run all steps", "do it all at once", "no interruptions", "without stopping".\n'
            '  b) The current user query contains any of the above phrases.\n'
            'Before executing continuous mode, determine whether the latest user query sets\n'
            'an explicit target boundary with phrases like "执行到 X", "做到 X", "到 X 为止",\n'
            '"生成到 X", "until X", or "up to X". Match X against the full compact\n'
            '"Plugin Step Name Index". Use detailed conditions, routing, and default approval\n'
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
            'Outside explicit continuous mode, step defaults still apply whenever the user has '
            'not stated an approval preference.\n\n'
            'When a step is interrupted and user says "继续": call advance_step_and_hand_off with '
            'runtime_instruction="Previous attempt was interrupted. Check existing artifacts '
            'and only produce missing outputs (resume from checkpoint)."\n'
            'When user says "重试": call the single-step advance_step_and_hand_off for that '
            'failed/interrupted step; never place a previously attempted step in a batch.'
        )
    )
    return global_rules + common

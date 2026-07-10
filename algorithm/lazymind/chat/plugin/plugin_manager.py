"""Plugin manager — builds ChatAgent tools for cold-start triggers and step advancement.

Tool types registered dynamically per-conversation:

- trigger_<plugin_id>       : Cold-start tool. Injected when no active plugin session exists.
- advance_step_and_hand_off : Step-advancement tool (stop-tool). Default; queues step and hands off control to user.
- advance_step              : Synchronous step-advancement tool. Only in 'dynamic' mode; blocks until
                              the SubAgent finishes before ReAct continues.
- ask_user                  : Ask the user a question (stop-tool). Registered on ChatAgent only.
- update_intent             : Upsert a global or step-level intent/constraint (ChatAgent only).
- list_plugin_steps         : Read-only step status query (ChatAgent only, when session active).
- get_step_result           : Read-only artifact summary for a step (ChatAgent only).
- get_failed_steps          : Read-only failed steps with error info (ChatAgent only).

Framework tools (save_artifact / get_artifact / list_artifacts) are always merged into
the step's tool list regardless of what the plugin's state.yml declares.  This ensures
every SubAgent can persist and retrieve artifacts without plugin authors having to
remember to list them explicitly.
"""
from __future__ import annotations

import json
import logging
import uuid
from dataclasses import dataclass
from typing import Any, Dict, List, Optional

import lazyllm
from lazyllm.tools.agent.base import _write_agent_data

from lazymind.chat.plugin import plugin_loader

LOG = logging.getLogger(__name__)


@dataclass(frozen=True)
class _ReachabilitySnapshot:
    current_step: str
    session_id: str
    forward_steps: List[str]
    rewind_steps: List[str]
    reachable_steps: List[str]


_COLD_START_PLUGIN_PROMPT = (
    '## Available Plugins\n'
    'IMPORTANT: Only trigger a plugin when the capability matches the '
    "user's PRIMARY and DIRECT intent — the main goal they are asking for "
    'right now. Never trigger a plugin for a sub-step that the model has '
    "internally decided is part of a larger multi-step plan. If the user's "
    'request involves multiple steps and only one of those steps would use a '
    'plugin, do NOT trigger the plugin. Never infer plugin intent from '
    'indirect or implicit cues.\n'
    'When a plugin does match the user\'s primary and direct intent, call '
    'the matching `trigger_<plugin>_plugin` tool before using `ask_user`. '
    'Do not ask clarification questions first just because optional details '
    "are missing; pass the user's exact original request to the plugin so its "
    'workflow can collect context or proceed with sensible defaults.\n\n'
    'CRITICAL — explicit plugin start requests:\n'
    'If the user explicitly asks to start, launch, or enable a plugin (e.g. '
    '"启动绘图插件", "打开图片生成插件", "启动图片插件", "start the image plugin"), '
    'you MUST call the matching `trigger_<plugin_id>_plugin` tool in this same '
    'response before any other action. Do NOT reply with text only, do NOT call '
    '`image_generator` / `image_editor` directly, and do NOT ask clarification '
    'questions first. Pass the user\'s request as `user_input` (or repeat their '
    'start phrase if they gave no further detail).\n'
    'For the AI image plugin (`image-plugin`), call `trigger_image_plugin`.\n\n'
)


# ---------------------------------------------------------------------------
# Framework tools always injected into every plugin step regardless of what
# the plugin's state.yml declares.
# ---------------------------------------------------------------------------

_FRAMEWORK_TOOLS: List[str] = [
    'save_artifact',
    'get_artifact',
    'list_artifacts',
    'list_knowledge_bases',
    'read_user_attachment',
    'find_user_attachment',
    'find_artifact',
    'patch_artifact',
    'discard_draft',
]


def _merge_tools(declared: List[str]) -> List[str]:
    """Return a deduplicated tool list with framework tools prepended."""
    seen = set()
    merged: List[str] = []
    for t in _FRAMEWORK_TOOLS + list(declared):
        if t not in seen:
            seen.add(t)
            merged.append(t)
    return merged


# ---------------------------------------------------------------------------
# Internal helpers
# ---------------------------------------------------------------------------

def _fetch_succeeded_steps(session_id: str) -> set:
    """Return the set of step_ids that have ever succeeded in this session.

    Queries the Go core REST API.  Returns an empty set on any error so that
    the caller degrades gracefully (ancestor rewind is simply not offered).
    """
    if not session_id:
        return set()
    try:
        import httpx
        from lazymind.config import config as _cfg
        core_url = str(_cfg['core_api_url']).rstrip('/')
        resp = httpx.get(f'{core_url}/plugin-sessions/{session_id}', timeout=3.0)
        if resp.status_code != 200:
            return set()
        steps = resp.json().get('data', {}).get('session', {}).get('steps', [])
        return {s['step_id'] for s in steps if isinstance(s, dict) and s.get('status') == 'succeeded'}
    except Exception:
        return set()


def _agentic_config() -> Dict[str, Any]:
    try:
        return lazyllm.globals['agentic_config'] or {}
    except Exception:
        return {}


def _export_parent_agentic_config(config: Dict[str, Any]) -> Dict[str, Any]:
    """Return the JSON-safe request context a plugin SubAgent should inherit."""
    exported: Dict[str, Any] = {}
    for key, value in (config or {}).items():
        if key == 'citation_state':
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
        runtime_instruction: str = '',
        partial_indices: Optional[Dict[str, List[int]]] = None) -> str:
    """Shared implementation for trigger_<plugin_id> and advance_step.

    Performs two-layer validation then emits a task_created signal.
    Returns a short status string (the tool return value seen by the LLM).

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

    sm = plugin_loader.get_state_machine(plugin_id)
    if sm is None:
        raise ValueError(f'plugin {plugin_id!r} not found.')

    if not sm.is_reachable(current_step, step_id):
        # Condition B: allow rewind to an ancestor that has previously succeeded.
        ancestors = sm.get_ancestors(current_step)
        if step_id in ancestors:
            succeeded = _fetch_succeeded_steps(session_id)
            if step_id not in succeeded:
                raise ValueError(
                    f'step {step_id!r} is an ancestor of {current_step!r} '
                    f'but has not succeeded in this session yet. '
                    f'Run it first before rewinding.'
                )
            # Ancestor rewind allowed — fall through to Layer 2.
        else:
            reachable = sm.get_reachable_steps(current_step)
            current_label = repr(current_step) if current_step else "'__start__'"
            raise ValueError(
                f'step {step_id!r} is not reachable from '
                f'{current_label}. '
                f'Reachable steps: {reachable}.'
            )
    LOG.info(
        '[plugin.advance] state_machine accepted plugin=%s step=%s session=%s current=%s cold=%s',
        plugin_id, step_id, session_id, current_step or '__start__', is_cold_start,
    )

    # --- Layer 2: dependency validation (via Go core REST API) ---
    step_config = plugin_loader.get_step_config(plugin_id, step_id)
    if not step_config:
        raise ValueError(f'step {step_id!r} is not defined in plugin {plugin_id!r}.')
    inputs: List[Dict[str, Any]] = step_config.get('inputs', [])
    if inputs and not is_cold_start and session_id:
        import httpx
        from lazymind.config import config as _cfg
        core_url = str(_cfg['core_api_url']).rstrip('/')
        try:
            resp = httpx.get(
                f'{core_url}/plugin-sessions/{session_id}',
                timeout=3.0,
            )
            if resp.status_code == 200:
                steps_data = {
                    s['step_id']: s['status']
                    for s in resp.json().get('data', {}).get('session', {}).get('steps', [])
                    if isinstance(s, dict)
                }
                for inp in inputs:
                    slot = inp.get('slot')
                    if not slot:
                        continue
                    required = inp.get('required', True)
                    producer_steps = plugin_loader.find_producer_steps(plugin_id, slot)
                    if not producer_steps:
                        continue
                    producer_statuses = {
                        producer_step: steps_data.get(producer_step)
                        for producer_step in producer_steps
                    }
                    if any(status == 'succeeded' for status in producer_statuses.values()):
                        continue

                    preferred_producer = (
                        current_step if current_step in producer_steps else producer_steps[0]
                    )
                    step_status = producer_statuses.get(preferred_producer)
                    if step_status is None:
                        if required:
                            LOG.warning(
                                '[plugin.advance] dependency missing plugin=%s step=%s session=%s slot=%s producer=%s',
                                plugin_id, step_id, session_id, slot, preferred_producer,
                            )
                            return (
                                f'Error: required artifact {slot!r} not available. '
                                f'Please trigger {preferred_producer!r} first.'
                            )
                        continue
                    if step_status in ('running', 'interrupted'):
                        LOG.warning(
                            '[plugin.advance] dependency not ready '
                            'plugin=%s step=%s session=%s slot=%s producer=%s status=%s',
                            plugin_id, step_id, session_id, slot, preferred_producer, step_status,
                        )
                        return (
                            f'Error: artifact {slot!r} not ready '
                            f'(producer step {preferred_producer!r} status: {step_status!r}).'
                        )
                    if step_status == 'failed':
                        if not required:
                            continue
                        LOG.warning(
                            '[plugin.advance] dependency failed plugin=%s step=%s session=%s slot=%s producer=%s',
                            plugin_id, step_id, session_id, slot, preferred_producer,
                        )
                        return (
                            f'Error: artifact {slot!r} not ready '
                            f'(producer step {preferred_producer!r} status: {step_status!r}).'
                        )
        except Exception as exc:
            LOG.warning(
                '[plugin.advance] dependency check skipped plugin=%s step=%s session=%s error=%s',
                plugin_id, step_id, session_id, exc,
            )
            pass  # Defensive: skip DB check on error; Go will re-validate

    # --- Emit task_created signal ---
    task_id = str(uuid.uuid4())
    output_defs = step_config.get('outputs', [])
    output_keys = [o['slot'] for o in output_defs if o.get('slot')]
    required_output_keys = [
        o['slot']
        for o in output_defs
        if o.get('slot') and o.get('required', True)
    ]
    input_keys = [i['slot'] for i in inputs if i.get('slot')]

    # Framework tools are always present regardless of plugin declaration.
    # Domain tools (e.g. kb) come only from state.yml — Go does not forward this
    # list to the SubAgent runner; runner re-resolves tools from plugin_loader.
    declared_tools: List[str] = step_config.get('tools', [])
    merged_tools = _merge_tools(declared_tools)

    params: Dict[str, Any] = {
        'plugin_id': plugin_id,
        'step_id': step_id,
        'session_id': session_id,
        'user_input': user_input,
        'is_cold_start': is_cold_start,
    }
    chat_session_id = str(cfg.get('session_id') or '').strip()
    if chat_session_id:
        params['chat_session_id'] = chat_session_id
    parent_agentic_config = _export_parent_agentic_config(cfg)
    if parent_agentic_config:
        params['parent_agentic_config'] = parent_agentic_config
    # Map Python-side runtime_instruction to Go-side retry_hint field name.
    if runtime_instruction:
        params['retry_hint'] = runtime_instruction
    if partial_indices:
        params['partial_indices'] = partial_indices
    params['required_output_artifact_keys'] = required_output_keys
    # Propagate full per-turn attachment index so SubAgent can access user files.
    history_files_per_turn: dict = cfg.get('history_files_per_turn') or {}
    if history_files_per_turn:
        params['history_files_per_turn'] = history_files_per_turn

    # Propagate KB filters and user_id so plugin SubAgents can call kb_search.
    filters: dict = dict(cfg.get('filters') or {})
    if filters:
        params['filters'] = filters
    user_id: str = str(cfg.get('user_id') or '').strip()
    if user_id:
        params['user_id'] = user_id
    LOG.info(
        '[plugin.advance] emitting task_created plugin=%s step=%s session=%s '
        'chat_sid=%s task=%s cold=%s inputs=%s outputs=%s required_outputs=%s',
        plugin_id, step_id, session_id, chat_session_id, task_id, is_cold_start,
        input_keys, output_keys, required_output_keys,
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

    _write_agent_data(
        'task_created',
        task_id=task_id,
        title=f'{plugin_id}:{step_id}',
        agent_type='plugin_step',
        mode='manual',          # Plugin steps always async; Go controls auto-advance
        objective=_render_step_objective(step_config, user_input, enriched_instruction),
        params=params,
        input_slots=input_keys,
        output_slots=output_keys,
        tools=merged_tools,
        resume=False,
    )
    LOG.info(
        '[plugin.advance] task_created emitted plugin=%s step=%s session=%s task=%s',
        plugin_id, step_id, session_id, task_id,
    )
    step_label = step_config.get('label', '')
    display_name = f'{step_id} ({step_label})' if step_label else step_id
    return f'Step {display_name!r} triggered. Stop here.'


def _trigger_plugin_end(plugin_id: str) -> str:
    """Emit a task_created event with step_id='__end__' to signal plugin session completion.

    Go's HandlePluginStepCreated intercepts this sentinel and marks the session as completed.
    """
    cfg = _agentic_config()
    session_id: str = cfg.get('plugin_session_id', '')
    if not session_id:
        raise ValueError('no active plugin session to complete.')
    task_id = str(uuid.uuid4())
    _write_agent_data(
        'task_created',
        task_id=task_id,
        title=f'{plugin_id}:__end__',
        agent_type='plugin_step',
        mode='manual',
        objective='',
        params={
            'plugin_id': plugin_id,
            'step_id': '__end__',
            'session_id': session_id,
            'user_input': '',
            'is_cold_start': False,
        },
        input_slots=[],
        output_slots=[],
        tools=[],
        resume=False,
    )
    return 'Plugin session completed. Stop here.'


def _build_step_choices_doc(
    forward_steps: List[str],
    rewind_steps: List[str],
    step_labels: Dict[str, str],
    plugin_id: str = '',
    current_step: str = '',
) -> str:
    """Return a formatted string listing available step choices for the LLM.

    When plugin_id and current_step are supplied, each forward step is annotated
    with the condition (if any) under which it should be taken, derived from the
    expanded transitions (skipif bypass conditions are already inlined).
    """
    sm = plugin_loader.get_state_machine(plugin_id) if plugin_id else None
    lines = [
        '## Available steps at this moment (authoritative — state machine computed)',
        '--------------------------------------------------------------------------',
        'These are the ONLY valid values for step_id right now.',
        'Do NOT infer step names from scenario descriptions or chat history.',
    ]
    if forward_steps:
        # Build a condition map from the expanded transitions so each step shows
        # the condition (if any) under which it should be taken.
        condition_map: Dict[str, str] = {}
        if sm and current_step is not None:
            for edge in sm.get_expanded_transitions(current_step):
                tgt = edge['to']
                cond = edge.get('condition', '').strip()
                if tgt not in condition_map and cond:
                    condition_map[tgt] = cond

        lines.append('Forward (next steps):')
        for s in forward_steps:
            label = step_labels.get(s, '')
            label_suffix = f'  ({label})' if label else ''
            cond = condition_map.get(s, '')
            cond_note = f'  [when: {cond}]' if cond else ''
            lines.append(f'  - {s}{label_suffix}{cond_note}')

        if len(forward_steps) > 1 and sm:
            lines.append('')
            lines.append(
                '  NOTE: If these exits belong to a parallel node (route:all), you MUST trigger\n'
                '  ALL of them by calling advance_step_and_hand_off once per step_id.\n'
                '  If they belong to a choice node (route:choice), pick exactly ONE based on conditions.\n'
                '  For steps annotated with [when: ...], only advance to that step if the condition holds.'
            )
    # Self-retry: current_step is injected into all_reachable without a graph self-loop.
    # Document it here so ChatAgent knows it can pass step_id=current_step to re-run.
    if current_step and current_step not in {'__start__', '__end__'}:
        label = step_labels.get(current_step, '')
        suffix = f'  ({label})' if label else ''
        lines.append('Retry (re-run current step):')
        lines.append(f'  - {current_step}{suffix}  <- full or partial retry of this step')
    if rewind_steps:
        lines.append('Rewind (re-run a past step):')
        for s in rewind_steps:
            label = step_labels.get(s, '')
            suffix = f'  ({label})' if label else ''
            lines.append(f'  - {s}{suffix}  <- previously completed, can re-trigger')
    lines.append('')
    lines.append('Pass one of the above IDs as step_id. Any other value will be rejected.')
    return '\n'.join(lines)


def build_cold_start_tools() -> List[Any]:
    """Build one trigger_<plugin_id> callable per loaded plugin."""
    tools = []
    for spec in (plugin_loader._registry or {}).values():
        pid = spec.plugin_id
        name = spec.yaml.get('name', pid)
        desc = spec.yaml.get('description', f'Trigger the {name} plugin.')
        # when_to_use is the primary trigger hint; fall back to a generic phrase.
        when_to_use = spec.yaml.get('when_to_use', '').strip()
        sm = spec.state_machine
        first_steps = sm.get_reachable_steps('__start__')

        def _make_trigger(plugin_id: str, first: List[str], desc: str, when_to_use: str):
            tool_name = f'trigger_{plugin_id.replace("-", "_")}'

            def _trigger(user_input: str) -> str:
                step_id = first[0] if first else ''
                if not step_id:
                    raise ValueError(f'plugin {plugin_id!r} has no reachable first step.')
                return _trigger_plugin_step(plugin_id, step_id, user_input, is_cold_start=True)

            # Set __name__ so the framework guard and logging use the public tool name.
            _trigger.__name__ = tool_name
            if when_to_use:
                tool_desc = f'{when_to_use.rstrip(".")}.  ({desc.rstrip(".")})'
            else:
                tool_desc = desc
            _trigger.__doc__ = (
                f'{tool_desc}\n\n'
                'Args:\n'
                '    user_input (str): A concise goal statement for the SubAgent that\n'
                '        will execute this step. Use ONLY the latest user query in this turn;\n'
                '        do NOT pass vague phrases like "继续", "请继续", or "continue".\n'
                '        Include: what the user wants to achieve, and style / quality\n'
                '        constraints explicitly mentioned in that query only.\n'
                '        Do NOT inject prior-turn context unless the user explicitly repeats it.\n'
                '        Example: "生成一张科幻风格的宇宙飞船插画，\n'
                '        线条简洁，色调冷蓝，适合作为游戏启动画面背景".\n\n'
                'Returns:\n'
                '    Confirmation that the plugin was started.'
            )
            return _trigger

        tools.append(_make_trigger(pid, first_steps, desc, when_to_use))
    return tools


def _live_reachability_snapshot(
    plugin_id: str,
    fallback_current_step: str,
    rewind_steps: Optional[List[str]] = None,
) -> _ReachabilitySnapshot:
    """Compute live step reachability from current ChatAgent state."""
    cfg = _agentic_config()
    current_step = cfg.get('plugin_step', '') or fallback_current_step
    session_id = cfg.get('plugin_session_id', '')
    sm = plugin_loader.get_state_machine(plugin_id)
    forward_steps = sm.get_reachable_steps(current_step) if sm else []
    rewind = list(rewind_steps or [])
    reachable = list(forward_steps) + rewind
    if current_step and current_step not in reachable:
        reachable = [current_step] + reachable
    return _ReachabilitySnapshot(
        current_step=current_step,
        session_id=session_id,
        forward_steps=forward_steps,
        rewind_steps=rewind,
        reachable_steps=reachable,
    )


def _validate_live_step_reachable(
    *,
    tool_name: str,
    plugin_id: str,
    step_id: str,
    fallback_current_step: str,
    rewind_steps: Optional[List[str]],
    runtime_instruction: Optional[str],
    partial_indices: Optional[Dict[str, List[int]]],
    input_len: Optional[int] = None,
) -> _ReachabilitySnapshot:
    """Validate a step tool call against live reachability and log consistently."""
    snapshot = _live_reachability_snapshot(plugin_id, fallback_current_step, rewind_steps)
    if input_len is None:
        LOG.info(
            '[plugin.advance] %s called plugin=%s target=%s session=%s current=%s '
            'reachable=%s runtime_instruction=%s partial=%s',
            tool_name, plugin_id, step_id, snapshot.session_id,
            snapshot.current_step or '__start__', snapshot.reachable_steps,
            bool(runtime_instruction), bool(partial_indices),
        )
    else:
        LOG.info(
            '[plugin.advance] %s called plugin=%s target=%s session=%s current=%s '
            'input_len=%d reachable=%s runtime_instruction=%s partial=%s',
            tool_name, plugin_id, step_id, snapshot.session_id,
            snapshot.current_step or '__start__', input_len, snapshot.reachable_steps,
            bool(runtime_instruction), bool(partial_indices),
        )
    if step_id not in snapshot.reachable_steps:
        LOG.warning(
            '[plugin.advance] %s rejected unreachable plugin=%s target=%s '
            'session=%s current=%s reachable=%s',
            tool_name, plugin_id, step_id, snapshot.session_id,
            snapshot.current_step or '__start__', snapshot.reachable_steps,
        )
        raise ValueError(
            f'step {step_id!r} is not reachable from '
            f'{snapshot.current_step!r}. Reachable: {snapshot.reachable_steps}.'
        )
    LOG.info(
        '[plugin.advance] %s reachable plugin=%s target=%s session=%s current=%s reachable=%s',
        tool_name, plugin_id, step_id, snapshot.session_id,
        snapshot.current_step or '__start__', snapshot.reachable_steps,
    )
    return snapshot


def build_advance_step_and_hand_off_tool(
    plugin_id: str,
    current_step: str,
    rewind_steps: Optional[List[str]] = None,
    step_labels: Optional[Dict[str, str]] = None,
) -> Any:
    """Build the advance_step_and_hand_off tool (stop-tool).

    Queues the step and immediately ends the current ReAct turn, handing off
    control to the SubAgent (auto mode) or the user (dynamic mode). This is the
    DEFAULT advancement tool registered for both auto and dynamic modes.
    """
    sm = plugin_loader.get_state_machine(plugin_id)
    forward = sm.get_reachable_steps(current_step) if sm else []
    rewind = list(rewind_steps or [])
    labels = step_labels or {}

    choices_doc = _build_step_choices_doc(forward, rewind, labels, plugin_id=plugin_id, current_step=current_step)

    def advance_step_and_hand_off(
        step_id: str,
        user_input: str,
        runtime_instruction: Optional[str] = None,
        partial_indices: Optional[Dict[str, List[int]]] = None,
    ) -> str:
        """Advance the active plugin to the next step and hand off control to user.

        After calling this tool, the current ReAct loop exits and the SSE stream closes.
        The step runs in the background; when it completes, the next decision is made by
        the DriverAgent (auto mode) or the user (dynamic mode).

        This is the DEFAULT tool for advancing steps. Use it unless you explicitly need
        to run multiple steps in sequence within a single turn (user said e.g. "re-run
        steps 1 through 3").  In that case use `advance_step` (synchronous, dynamic
        mode only) for intermediate steps and this tool for the final step.

        Terminal plugin steps are normally completed by the plugin event loop after
        the terminal task succeeds. Use `step_id="__end__"` only as an explicit
        close signal when the final step has already succeeded and the session is
        still open.
        """
        if step_id == '__end__':
            return _trigger_plugin_end(plugin_id)
        _validate_live_step_reachable(
            tool_name='advance_step_and_hand_off',
            plugin_id=plugin_id,
            step_id=step_id,
            fallback_current_step=current_step,
            rewind_steps=rewind_steps,
            runtime_instruction=runtime_instruction,
            partial_indices=partial_indices,
        )
        return _trigger_plugin_step(
            plugin_id, step_id, user_input,
            is_cold_start=False,
            runtime_instruction=runtime_instruction or '',
            partial_indices=partial_indices or {},
        )

    advance_step_and_hand_off.__doc__ = (
        'Advance the active plugin to the next step and hand off control to SubAgent/user.\n\n'
        'The step runs in the background. Use this as the DEFAULT tool in single-step mode.\n'
        'In continuous/uninterrupted mode (Rule 4 in system prompt), use `advance_step`\n'
        'for prerequisite steps before the requested target boundary, then call this tool\n'
        'for the boundary step and stop. Terminal steps are also boundary steps; after a\n'
        'terminal task succeeds, the plugin event loop completes the session.\n\n'
        '## Intent-change rewind (MUST read before advancing)\n\n'
        'If the user expresses dissatisfaction with or changes to the result of a step that\n'
        'has ALREADY SUCCEEDED, you MUST rewind to the earliest affected step instead of\n'
        'advancing the next forward step.\n\n'
        'Examples:\n'
        '  User: "我不喜欢日系风格，改成北欧简约风" → the style was set in an earlier step\n'
        '    → advance_step_and_hand_off(step_id=<that_step>, rewind=True,\n'
        '        user_input="北欧简约风格，...")\n'
        '  User: "不要树，改成蓝天白云" → subject was defined in analyze_subject\n'
        '    → advance_step_and_hand_off(step_id="analyze_subject", rewind=True,\n'
        '        user_input="主体：蓝天白云...")\n\n'
        '## Checkpoint-Resume (interrupted steps)\n\n'
        'When the user says "继续" and the step was interrupted (not "重试"):\n'
        '  advance_step_and_hand_off(step_id=<current_step>, runtime_instruction=(\n'
        '    "Previous attempt was interrupted. Check existing artifacts for this step "\n'
        '    "and only produce missing outputs (resume from checkpoint). "\n'
        '    "Do not regenerate already-saved artifacts."))\n'
        'When the user says "重试": advance_step_and_hand_off(step_id=..., rewind=True)\n'
        '  (rewind=True discards previous partial artifacts and restarts the step from scratch)\n\n'
        '## Completing the plugin\n\n'
        'Prefer handing off the terminal pipeline step itself. The plugin event loop will\n'
        'mark the session completed after that terminal task succeeds. Call with\n'
        'step_id="__end__" only if the final step has already succeeded but the session\n'
        'still needs an explicit close signal.\n\n'
        '## Rewind guidance\n\n'
        'If the DriverAgent or user indicates a prior step produced bad output, rewind by\n'
        'passing its step_id. Rewind-eligible steps are listed in the "Rewind" section below.\n\n'
        + choices_doc + '\n\n'
        'Args:\n'
        '    step_id (str): Step to advance to (see list above) or "__end__".\n'
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
    """Build the synchronous advance_step tool (dynamic mode only).

    Blocks until the SubAgent completes, then returns the step result summary so
    ChatAgent can continue reasoning.  Use only when running multiple steps in
    sequence within a single turn.
    """
    sm = plugin_loader.get_state_machine(plugin_id)
    forward = sm.get_reachable_steps(current_step) if sm else []
    rewind = list(rewind_steps or [])
    labels = step_labels or {}

    choices_doc = _build_step_choices_doc(forward, rewind, labels, plugin_id=plugin_id, current_step=current_step)

    def advance_step(
        step_id: str,
        user_input: str,
        runtime_instruction: Optional[str] = None,
        partial_indices: Optional[Dict[str, List[int]]] = None,
    ) -> str:
        """Advance the active plugin to the next step and WAIT for completion.

        Blocks until the SubAgent finishes, then returns the step result summary.
        Use ONLY when running multiple steps in sequence within a single turn
        (e.g. user said "re-run steps 1 to 3"). For single-step advancement,
        prefer `advance_step_and_hand_off` to let the user review results.
        """
        if step_id == '__end__':
            return _trigger_plugin_end(plugin_id)
        reachability = _validate_live_step_reachable(
            tool_name='advance_step',
            plugin_id=plugin_id,
            step_id=step_id,
            fallback_current_step=current_step,
            rewind_steps=rewind_steps,
            runtime_instruction=runtime_instruction,
            partial_indices=partial_indices,
            input_len=len(user_input or ''),
        )
        _clear_step_signal_queues(step_id)
        result = _trigger_plugin_step(
            plugin_id, step_id, user_input,
            is_cold_start=False,
            runtime_instruction=runtime_instruction or '',
            partial_indices=partial_indices or {},
        )
        # First wait for Go/Core to acknowledge that it consumed the streaming
        # task_created event and launched the plugin_step. Without this ack, a
        # lost task_created event would look like a long-running step.
        task_id = _wait_for_step_started(step_id)
        # Core updates plugin_sessions.current_step_id when it accepts
        # task_created. Keep ChatAgent's local state on the same boundary.
        _set_local_plugin_step(step_id)
        LOG.info(
            '[plugin.advance] local current_step updated plugin=%s step=%s session=%s task=%s',
            plugin_id, step_id, reachability.session_id, task_id,
        )
        # Poll for completion via FileSystemQueue.
        LOG.info(
            '[plugin.advance] waiting for step_done plugin=%s step=%s session=%s task=%s',
            plugin_id, step_id, reachability.session_id, task_id,
        )
        summary = _wait_for_step_done(step_id, result)
        LOG.info(
            '[plugin.advance] advance_step completed plugin=%s step=%s session=%s task=%s summary_len=%d',
            plugin_id, step_id, reachability.session_id, task_id, len(summary or ''),
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
        'Use this tool in continuous/uninterrupted mode (Rule 4 in system prompt).\n'
        'Continuous mode is active when the user intent contains phrases like\n'
        '"一次性完成", "不要中断", "一次性写完", "run all steps", "no interruptions".\n'
        'In continuous mode with an explicit target boundary, use `advance_step` only\n'
        'for prerequisite steps before that boundary, then execute the boundary step\n'
        'with `advance_step_and_hand_off` and stop. If the user did not set a boundary,\n'
        'run prerequisite remaining steps with this tool, then execute the terminal step\n'
        'with `advance_step_and_hand_off` and stop.\n\n'
        'In default single-step mode (no uninterrupted constraint), do NOT use this\n'
        'tool — use `advance_step_and_hand_off` instead so the user can review each result.\n\n'
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


def _append_step_transition_hint(
    summary: str,
    plugin_id: str,
    current_step: str,
    rewind_steps: List[str],
    step_labels: Dict[str, str],
) -> str:
    """Append live transition guidance to advance_step's tool result."""
    sm = plugin_loader.get_state_machine(plugin_id)
    forward = sm.get_reachable_steps(current_step) if sm else []
    choices_doc = _build_step_choices_doc(
        forward,
        rewind_steps,
        step_labels,
        plugin_id=plugin_id,
        current_step=current_step,
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
        'advance to downstream steps or manually close `__end__` after the boundary hand-off.'
    )


def _set_local_plugin_step(step_id: str) -> None:
    """Update ChatAgent's in-process current step after Core accepts the task."""
    try:
        lazyllm.globals['agentic_config']['plugin_step'] = step_id
    except Exception as exc:
        LOG.warning('[plugin.advance] failed to update local plugin_step step=%s error=%s', step_id, exc)


def _clear_step_signal_queues(step_id: str) -> None:
    """Drop stale started/done signals before launching a fresh dynamic step."""
    try:
        from lazyllm.common.queue import FileSystemQueue
        cfg = _agentic_config()
        session_id = cfg.get('plugin_session_id', '')
        for prefix in ('step_started', 'step_done'):
            FileSystemQueue(klass=f'{prefix}_{session_id}_{step_id}').clear()
        LOG.info('[plugin.advance] cleared step signal queues step=%s session=%s', step_id, session_id)
    except Exception as exc:
        LOG.warning('[plugin.advance] failed to clear step signal queues step=%s error=%s', step_id, exc)


def _wait_for_step_started(step_id: str, timeout: float = 15.0) -> str:
    """Poll FileSystemQueue for a step_started ack from Go/Core.

    Raises TimeoutError when the streaming task_created event was not consumed
    by Core in time. This is a launch failure, not a step execution timeout.
    """
    import time
    from lazyllm.common.queue import FileSystemQueue

    cfg = _agentic_config()
    session_id = cfg.get('plugin_session_id', '')
    queue_key = f'step_started_{session_id}_{step_id}'
    fsq = FileSystemQueue(klass=queue_key)
    deadline = time.monotonic() + timeout
    LOG.info('[plugin.advance] waiting for step_started step=%s session=%s timeout=%.0fs', step_id, session_id, timeout)
    while time.monotonic() < deadline:
        for raw in fsq.dequeue():
            try:
                msg = json.loads(raw)
            except Exception as exc:
                LOG.warning(
                    '[plugin.advance] ignored malformed step_started signal '
                    'step=%s session=%s error=%s',
                    step_id, session_id, exc,
                )
                continue
            if msg.get('tag') == 'step_started':
                task_id = str(msg.get('task_id') or '')
                LOG.info(
                    '[plugin.advance] received step_started step=%s session=%s task=%s',
                    step_id, session_id, task_id,
                )
                return task_id
            if msg.get('tag') == 'cancel':
                LOG.warning(
                    '[plugin.advance] received cancel before step_started step=%s session=%s',
                    step_id, session_id,
                )
                raise RuntimeError(f'Step {step_id!r} was stopped before launch completed.')
        time.sleep(0.2)
    LOG.error('[plugin.advance] step_started timeout step=%s session=%s timeout=%.0fs', step_id, session_id, timeout)
    raise TimeoutError(
        f'Step {step_id!r} was not acknowledged by Core within {timeout:.0f}s. '
        'The task_created stream event may not have been consumed.'
    )


def _wait_for_step_done(step_id: str, trigger_result: str, timeout: float = 600.0) -> str:
    """Poll FileSystemQueue for a step_done signal; return result summary or timeout message.

    The step_done signal is enqueued by the subagent runner at step completion.
    Polls every 2 seconds up to `timeout` seconds.  Exits early if a 'cancel' control
    message arrives on the step_done queue.
    """
    import time
    try:
        from lazyllm.common.queue import FileSystemQueue
        cfg = _agentic_config()
        session_id = cfg.get('plugin_session_id', '')
        queue_key = f'step_done_{session_id}_{step_id}'
        fsq = FileSystemQueue(klass=queue_key)
        deadline = time.monotonic() + timeout
        LOG.info('[plugin.advance] polling step_done step=%s session=%s timeout=%.0fs', step_id, session_id, timeout)
        while time.monotonic() < deadline:
            for raw in fsq.dequeue():
                try:
                    msg = json.loads(raw)
                    # Support both old tag='step_done' format and new {status, summary} format.
                    if msg.get('tag') == 'step_done' or 'status' in msg:
                        LOG.info(
                            '[plugin.advance] received step_done step=%s session=%s status=%s summary_len=%d',
                            step_id, session_id, msg.get('status', ''), len(msg.get('summary', '') or ''),
                        )
                        return msg.get('summary', f"Step '{step_id}' completed.")
                    if msg.get('tag') == 'cancel':
                        LOG.warning(
                            '[plugin.advance] received cancel while waiting step_done '
                            'step=%s session=%s',
                            step_id, session_id,
                        )
                        return f"Step '{step_id}' was stopped by the user."
                except Exception as exc:
                    LOG.warning(
                        '[plugin.advance] ignored malformed step_done signal '
                        'step=%s session=%s error=%s',
                        step_id, session_id, exc,
                    )
            time.sleep(2.0)
        LOG.error('[plugin.advance] step_done timeout step=%s session=%s timeout=%.0fs', step_id, session_id, timeout)
        return f"Step '{step_id}' timed out waiting for completion (partial result may be available)."
    except Exception as exc:
        LOG.warning('[plugin.advance] step_done wait failed step=%s error=%s; returning trigger result', step_id, exc)
        return trigger_result


# ---------------------------------------------------------------------------
# update_intent — ChatAgent only, persists intent/constraint to DB
# ---------------------------------------------------------------------------

def build_update_intent_tool() -> Any:
    """Build the update_intent tool for ChatAgent.

    UPSERT a global or step-level intent/constraint. Plugin-agnostic — the
    framework manages this, not the plugin author.
    """
    def update_intent(
        scope: str,
        content: str,
        step_id: Optional[str] = None,
    ) -> str:
        """Record or update an intent/constraint for this plugin session.

        ALWAYS call this tool BEFORE advancing any step when the user expresses
        a style preference, quality requirement, or execution constraint in their
        message. Do not skip this even if you are about to call advance_step_and_hand_off.

        Also call this tool when:
        - The user repeats or emphasizes the same point across multiple turns.
        - The user pushes back on a result and explains why (e.g. "that's wrong because...",
          "I didn't mean X, I meant Y") — capture the clarification so future steps honour it.

        Scope 'session' — applies to the entire session (global constraint):
          e.g. "keep the tone formal throughout", "always use bullet points"
          → update_intent(scope='session', content='keep the tone formal throughout')

        Scope 'step' — applies to a specific step only:
          e.g. "make step 2 output shorter", "use a different format for the summary step"
          → update_intent(scope='step', step_id='<step_id>', content='output should be shorter')

        Args:
            scope (str): 'session' for global or 'step' for step-specific constraint.
            content (str): A concise model-generated summary of the user's emphasized
                constraints in the latest query (not a full raw quote). If no explicit
                constraints are present, do not call this tool.
            step_id (str, optional): Required when scope='step'.

        Returns:
            Confirmation string.
        """
        cfg = _agentic_config()
        session_id = cfg.get('plugin_session_id', '')
        if not session_id:
            raise ValueError('no active plugin session.')
        if scope not in ('session', 'step'):
            raise ValueError(f'unknown scope {scope!r}. Use "session" or "step".')
        if scope == 'step' and not step_id:
            raise ValueError('step_id required for scope="step".')
        # Emit via SSE so Go writes the DB and pushes an intent_updated convEvent
        # to notify the frontend immediately — avoids the user having to refresh.
        _write_agent_data('intent_updated', **{
            'session_id': session_id,
            'scope': scope,
            'content': content,
            'step_id': step_id or '',
        })
        return '约束已更新'

    return update_intent


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


def resolve_plugin_injection(
    plugin_context: Optional[Dict[str, Any]],
    conversation_id: str = '',
) -> tuple:
    """Resolve plugin tools, system prompt, stop-tools and agentic_config patches.

    Called once per request from handle_chat.  Encapsulates all plugin-context
    branching so chat_service stays free of plugin-internal details.

    Note: schedule tools and SubAgent task context are intentionally NOT injected
    here — they are handled independently in chat_service.py so that schedule
    availability and task context visibility are not affected by enable_plugin.

    Returns:
        (plugin_tools, plugin_system_prompt, plugin_stop_tools, agentic_config_patch, plugin_artifact_context)

        plugin_tools             – list of callables to append to the agent tool list.
        plugin_system_prompt     – extra system-prompt text to append (may be empty).
        plugin_stop_tools        – list of tool names that terminate the ReAct loop.
        agentic_config_patch     – dict to merge into agentic_config (may be empty).
        plugin_artifact_context  – artifact summary to prepend to the current user-turn (not system prompt).
    """
    plugin_tools: List[Any] = []
    plugin_system_prompt: str = ''
    plugin_stop_tools: List[str] = []
    agentic_config_patch: Dict[str, Any] = {}
    plugin_artifact_context: str = ''

    # Honour enable_plugin=false: skip all plugin tooling and fall back to pure QA.
    cfg = _agentic_config()
    if not cfg.get('enable_plugin', True):
        return plugin_tools, plugin_system_prompt, plugin_stop_tools, agentic_config_patch, plugin_artifact_context

    if not plugin_loader._registry:
        # No plugins registered — return empty; task context is injected by chat_service.
        return plugin_tools, plugin_system_prompt, plugin_stop_tools, agentic_config_patch, plugin_artifact_context

    # Resolve plugin_mode from plugin_context (injected by Go).
    plugin_mode = 'dynamic'
    if plugin_context and isinstance(plugin_context, dict):
        pm = plugin_context.get('plugin_mode', '')
        if pm in ('auto', 'dynamic'):
            plugin_mode = pm

    if plugin_context and isinstance(plugin_context, dict):
        p_session_id = plugin_context.get('session_id', '')
        p_plugin_id = plugin_context.get('plugin_id', '')
        p_current_step = plugin_context.get('current_step', '')

        if p_session_id and p_plugin_id:
            agentic_config_patch = {
                'plugin_id': p_plugin_id,
                'plugin_session_id': p_session_id,
                'plugin_step': p_current_step,
                'plugin_mode': plugin_mode,
                'focused_tab': plugin_context.get('focused_tab'),
                'focused_sort_order': plugin_context.get('focused_sort_order'),
            }
            sm = plugin_loader.get_state_machine(p_plugin_id)

            rewind_steps: List[str] = []
            if sm and p_session_id and p_current_step:
                ancestors = sm.get_ancestors(p_current_step)
                succeeded = _fetch_succeeded_steps(p_session_id)
                # Only ancestors (not current_step itself) are rewind candidates.
                # current_step is the "pending" step for this turn and is shown
                # separately in the step-status context; including it in rewind
                # would mislead the LLM into thinking it has already succeeded.
                rewind_steps = sorted(ancestors & succeeded)

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
            plugin_tools = [build_advance_step_and_hand_off_tool(
                p_plugin_id, p_current_step,
                rewind_steps=rewind_steps,
                step_labels=step_labels,
            )]
            plugin_stop_tools = ['advance_step_and_hand_off']

            if plugin_mode == 'dynamic':
                plugin_tools.append(build_advance_step_tool(
                    p_plugin_id, p_current_step,
                    rewind_steps=rewind_steps,
                    step_labels=step_labels,
                ))

            # update_intent for ChatAgent only.
            plugin_tools.append(build_update_intent_tool())

            # Read-only query tools (active session required).
            plugin_tools.extend(build_query_tools())

            # save_plugin_artifact lets ChatAgent write an artifact directly.
            from lazymind.chat.engine.tools.subagent_chat_tools import save_plugin_artifact
            plugin_tools.append(save_plugin_artifact)

            plugin_system_prompt = plugin_loader.get_scenario(p_plugin_id)
            plugin_artifact_context = _build_session_artifact_section(p_session_id)

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

            # Append mode-specific system prompt guidance.
            sm_for_mode = plugin_loader.get_state_machine(p_plugin_id)
            terminal_steps = (
                sm_for_mode.get_terminal_steps(from_step=p_current_step)
                if sm_for_mode else []
            )
            plugin_system_prompt = (
                (plugin_system_prompt or '') + _build_mode_guidance(plugin_mode, terminal_steps, step_labels)
            )
        else:
            # Cold start: no active session yet
            plugin_tools = build_cold_start_tools()
            plugin_stop_tools = [t.__name__ for t in plugin_tools]
            if plugin_tools:
                scenarios = [
                    plugin_loader.get_plugin_intro(spec.plugin_id)
                    for spec in (plugin_loader._registry or {}).values()
                ]
                plugin_system_prompt = (
                    _COLD_START_PLUGIN_PROMPT
                ) + '\n\n---\n\n'.join(s for s in scenarios if s)
    else:
        # No plugin_context provided: still inject cold-start triggers
        plugin_tools = build_cold_start_tools()
        plugin_stop_tools = [t.__name__ for t in plugin_tools]
        if plugin_tools:
            scenarios = [
                plugin_loader.get_plugin_intro(spec.plugin_id)
                for spec in (plugin_loader._registry or {}).values()
            ]
            plugin_system_prompt = (
                _COLD_START_PLUGIN_PROMPT
            ) + '\n\n---\n\n'.join(s for s in scenarios if s)

    return plugin_tools, plugin_system_prompt, plugin_stop_tools, agentic_config_patch, plugin_artifact_context


# ---------------------------------------------------------------------------
# Intent / constraint helpers
# ---------------------------------------------------------------------------

def _build_intent_section(session_id: str, step_id: Optional[str] = None) -> str:
    """Serialize session-level and step-level intent/constraints for injection into ChatAgent prompts.

    Both global (session-level) and all recorded step-level constraints are injected here
    so ChatAgent has full visibility when deciding whether to call update_intent and which
    step to advance next.
    """
    if not session_id:
        return ''
    try:
        from lazymind.chat.engine.subagent.db import TaskQueryDB
        db = TaskQueryDB()
        session_intent = db.get_session_intent(session_id) if hasattr(db, 'get_session_intent') else None
        step_intents: Dict[str, str] = db.list_step_intents(session_id) if hasattr(db, 'list_step_intents') else {}

        if not session_intent and not step_intents:
            return ''

        lines = ['## User Intent & Constraints']
        lines.append('These constraints were recorded from the user and MUST be respected when advancing steps.')
        if session_intent:
            lines.append(f'Global: {session_intent}')
        for sid, txt in step_intents.items():
            lines.append(f'Step "{sid}": {txt}')
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

        sm = plugin_loader.get_state_machine(plugin_id)
        succeeded = _fetch_succeeded_steps(session_id) if session_id else set()

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
            sm_steps = list(sm._transitions.keys()) if sm else []
            ordered = [s for s in sm_steps if s not in sm._RESERVED and s in succeeded]
            unordered = sorted(succeeded - set(ordered))
            all_succeeded = ordered + unordered
            lines.append('Succeeded steps (in execution order): ' + ', '.join(_label(s) for s in all_succeeded))
        else:
            lines.append('Succeeded steps: none yet')

        if rewind_steps:
            lines.append('Rewind-eligible steps (already succeeded, can be re-run): '
                         + ', '.join(_label(s) for s in rewind_steps))

        if sm and current_step:
            forward = [s for s in sm.get_reachable_steps(current_step) if s not in sm._RESERVED]
            if forward:
                lines.append('Next forward steps (valid targets for continuing): '
                             + ', '.join(_label(s) for s in forward))

        return '\n'.join(lines)
    except Exception:
        return ''


def _build_mode_guidance(
        plugin_mode: str,
        terminal_steps: Optional[List[str]] = None,
        step_labels: Optional[Dict[str, str]] = None) -> str:
    """Return mode-specific system prompt instructions appended to the scenario."""
    # --- Global decision rules (apply to both auto and dynamic modes) ---
    global_rules = (
        '\n\n## Step decision rules (READ BEFORE EVERY ACTION)\n\n'
        '### Rule 0 — Intent capture from latest user query (highest priority)\n'
        'At the beginning of each plugin turn, inspect ONLY the latest user query.\n'
        'If it contains explicit constraints/emphasis (e.g. "必须/务必/一定/不要/不许/禁止/只能/根据..."),\n'
        'you MUST call `update_intent(scope="session", content="<concise summary>")` FIRST,\n'
        'before any step-advance tool call. Summarize 1-2 key constraints in concise Chinese.\n'
        'If the latest query has no explicit new constraints, do NOT call update_intent.\n\n'
        'ALSO: if the "User Intent & Constraints" section is empty (no session intent recorded yet)\n'
        'AND the conversation history contains "一次性", "不要中断", "不要打断", "中间不要停",\n'
        '"一次性写完", "run all steps", "do it all at once", or similar phrases,\n'
        'call `update_intent(scope="session", content="<concise summary of the constraint>")`\n'
        'to persist the constraint before advancing any step.\n\n'
        '### Rule 1 — Intent-change detection\n'
        'Before advancing any step, check whether the user is rejecting or changing\n'
        'the outcome of a step that has ALREADY SUCCEEDED. Signals include:\n'
        '  - Direct negation: "我不喜欢…", "换成…", "不要…", "重新…", "I don\'t like…"\n'
        '  - Implicit correction: user describes a different style/subject/content\n'
        '    than what the current artifacts reflect.\n'
        'If intent has changed, identify the EARLIEST step whose output is now\n'
        'invalidated and rewind to that step using `advance_step_and_hand_off` with\n'
        '`step_id=<affected_step>` and `rewind=True` (clears that step\'s artifacts\n'
        'and re-runs from scratch). Do NOT continue to the next forward step.\n\n'
        '### Rule 2 — Step order enforcement\n'
        'Steps MUST be executed in the order defined by the pipeline. You may only\n'
        'execute `current_step` or rewind to a rewind-eligible step. The next forward\n'
        'step becomes available only AFTER `current_step` succeeds.\n'
        'Never skip steps — do not call a downstream step while an upstream step is\n'
        'still pending.\n\n'
        '### Rule 3 — Workflow advancement requests\n'
        'If the user clearly asks to proceed with the existing plugin workflow and\n'
        'does not add new requirements, corrections, or dissatisfaction signals:\n'
        '  - If continuous mode is NOT active: call `advance_step_and_hand_off` and stop.\n'
        '  - If continuous mode IS active (Rule 4) and the user set a target boundary:\n'
        '    use `advance_step` for prerequisite steps before that boundary, then use\n'
        '    `advance_step_and_hand_off` for the boundary step and stop.\n'
        '  - If continuous mode IS active with no target boundary: use `advance_step`\n'
        '    for prerequisite remaining steps, then use `advance_step_and_hand_off`\n'
        '    for the terminal/final-deliverable step and stop.\n'
        'Select the target from "Next forward steps (valid targets for continuing)"\n'
        'in the step-status block. If multiple forward targets are listed, choose\n'
        'the target whose transition condition best matches the current artifacts\n'
        'and user intent; if the choice is genuinely ambiguous, ask the user.\n'
        'Do NOT reply only with prose such as "正在生成..." without calling a tool.\n'
        'Do NOT pass the current plugin step state unless it is explicitly listed\n'
        'as a valid forward or rewind target.\n'
    )
    common = (
        '\n\n## Plugin execution guidance\n\n'
        'Tools for step advancement:\n'
        '- `advance_step_and_hand_off`: Queue a step and hand off control (DEFAULT). '
        'Use for single-step advancement.\n'
    )
    if plugin_mode == 'dynamic':
        labels = step_labels or {}
        terminal_hint = ''
        if terminal_steps:
            names = ', '.join(
                f'`{s}`' + (f' ({labels[s]})' if s in labels else '')
                for s in terminal_steps
            )
            terminal_hint = (
                f'\n\n## Terminal steps (last steps before pipeline completion)\n\n'
                f'The following steps lead directly to the end of the pipeline: {names}.\n'
                'If the user explicitly targets one of these terminal steps as the boundary,\n'
                'execute it with `advance_step_and_hand_off` and stop. If the user asks to\n'
                'complete the whole pipeline and no narrower boundary is specified, run\n'
                'prerequisite steps with `advance_step`, execute the terminal step with\n'
                '`advance_step_and_hand_off`, and let the plugin event loop complete the\n'
                'session after that terminal task finishes.\n'
                'In default single-step mode, use `advance_step_and_hand_off` and stop.'
            )
        common += (
            '- `advance_step`: Queue a step and WAIT for its result (dynamic mode only). '
            'Use this in continuous/uninterrupted mode (see Rule 4 below). '
            'Use `advance_step` for prerequisite steps before a requested boundary, then '
            '`advance_step_and_hand_off` for the boundary step.\n'
            'In default single-step mode (no uninterrupted constraint), use '
            '`advance_step_and_hand_off` and stop.\n\n'
            '### Rule 4 — Continuous / uninterrupted execution mode (MUST check before every action)\n'
            'Activate continuous mode when ANY of the following is true:\n'
            '  a) The "User Intent & Constraints" section contains phrases such as:\n'
            '     "一次性完成", "一次性写完", "不要中断", "不要打断", "中间不要停",\n'
            '     "run all steps", "do it all at once", "no interruptions", "without stopping".\n'
            '  b) The current user query contains any of the above phrases.\n'
            'Before executing continuous mode, determine whether the latest user query sets\n'
            'an explicit target boundary with phrases like "执行到 X", "做到 X", "到 X 为止",\n'
            '"生成到 X", "until X", or "up to X". Match X against the current plugin\'s\n'
            'available step ids, step labels, and transition descriptions shown in the\n'
            'Plugin Step Status / tool candidate lists. Do not assume plugin-specific step\n'
            'names or meanings that are not present in the current plugin context.\n'
            'A target boundary has higher priority than generic uninterrupted phrases. For\n'
            'example, "一次性执行到 X，中间不要问我" means run only through the\n'
            'matched boundary step X, then stop after queuing X.\n'
            'In continuous mode:\n'
            '  1. If an explicit target boundary exists, use `advance_step` only for steps\n'
            '     before the boundary, in pipeline order.\n'
            '  2. Execute the target boundary step with `advance_step_and_hand_off`, then stop.\n'
            '     Do NOT wait for the boundary step with `advance_step`.\n'
            '  3. Do NOT call downstream steps and do NOT call `__end__` after a non-`__end__`\n'
            '     boundary hand-off.\n'
            '  4. If there is no explicit target boundary and the user requested the whole\n'
            '     pipeline/final deliverable, run prerequisite steps with `advance_step`,\n'
            '     execute the terminal step with `advance_step_and_hand_off`, then stop.\n'
            '  5. NEVER call `advance_step_and_hand_off` for intermediate steps — '
            '     it hands off control and breaks the continuous run.\n'
            '  6. If `advance_step` returns an error, stop the sequence immediately and '
            '     report the failure; do not skip or continue to a later step.\n\n'
            'After each step in default (non-continuous) mode, use `advance_step_and_hand_off` '
            'so the user can review the result and decide the next action.\n\n'
            'When a step is interrupted and user says "继续": call advance_step_and_hand_off with '
            'runtime_instruction="Previous attempt was interrupted. Check existing artifacts '
            'and only produce missing outputs (resume from checkpoint)."\n'
            'When user says "重试": call advance_step_and_hand_off with rewind=True '
            '(restarts the interrupted step from scratch, ignoring previous partial artifacts).'
            + terminal_hint
        )
    else:  # auto
        common += (
            '\nIn auto mode, always use `advance_step_and_hand_off`. '
            'Do not use `advance_step` (not available in auto mode). '
            'After calling advance_step_and_hand_off, the DriverAgent will evaluate the result '
            'and decide the next action automatically.\n\n'
            'Do not ask the user questions during step execution in auto mode '
            'unless the user explicitly requests it.'
        )
    return global_rules + common

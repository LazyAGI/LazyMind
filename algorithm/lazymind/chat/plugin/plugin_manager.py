"""Plugin manager — builds ChatAgent tools for cold-start triggers and step advancement.

Two tool types are registered dynamically per-conversation:

- trigger_<plugin_id>  : Cold-start tool.  Injected when no active plugin session exists.
- advance_step         : Step-advancement tool.  Injected when an active session exists.

Both are stop-tools: after a successful invocation the ReAct loop terminates immediately
without entering a summarize step.

Framework tools (save_artifact / get_artifact / list_artifacts) are always merged into
the step's tool list regardless of what the plugin's state.yml declares.  This ensures
every SubAgent can persist and retrieve artifacts without plugin authors having to
remember to list them explicitly.
"""
from __future__ import annotations

import uuid
from typing import Any, Dict, List, Optional

import lazyllm
from lazyllm.tools.agent.base import _write_agent_data

from lazymind.chat.plugin import plugin_loader
from lazymind.chat.engine.tools.infra import handle_tool_errors


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
        partial_indices: Maps artifact_key → list_index values that should overwrite
            existing list-slot entries rather than appending. None means full write.
    """
    cfg = _agentic_config()
    session_id: str = cfg.get('plugin_session_id', '') or str(uuid.uuid4())

    # --- Layer 1: format validation (no DB needed) ---
    if not user_input or not user_input.strip():
        # Fall back to the current conversation query so the SubAgent always
        # receives meaningful context even when the LLM omits user_input.
        user_input = cfg.get('query', '').strip()
    if not user_input:
        return 'Error: user_input must not be empty.'

    sm = plugin_loader.get_state_machine(plugin_id)
    if sm is None:
        return f'Error: plugin {plugin_id!r} not found.'

    current_step: str = cfg.get('plugin_step', '')
    if not sm.is_reachable(current_step, step_id):
        # Condition B: allow rewind to an ancestor that has previously succeeded.
        ancestors = sm.get_ancestors(current_step)
        if step_id in ancestors:
            succeeded = _fetch_succeeded_steps(session_id)
            if step_id not in succeeded:
                return (
                    f'Error: step {step_id!r} is an ancestor of {current_step!r} '
                    f'but has not succeeded in this session yet. '
                    f'Run it first before rewinding.'
                )
            # Ancestor rewind allowed — fall through to Layer 2.
        else:
            reachable = sm.get_reachable_steps(current_step)
            current_label = repr(current_step) if current_step else "'__start__'"
            return (
                f'Error: step {step_id!r} is not reachable from '
                f'{current_label}. '
                f'Reachable steps: {reachable}.'
            )

    # --- Layer 2: dependency validation (via Go core REST API) ---
    step_config = plugin_loader.get_step_config(plugin_id, step_id)
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
                    artifact_id = inp['artifact_id']
                    required = inp.get('required', True)
                    producer_step = plugin_loader.find_producer_step(plugin_id, artifact_id)
                    if not producer_step:
                        continue
                    step_status = steps_data.get(producer_step)
                    if step_status is None:
                        if required:
                            return (
                                f'Error: required artifact {artifact_id!r} not available. '
                                f'Please trigger {producer_step!r} first.'
                            )
                        continue
                    if step_status in ('running', 'failed', 'interrupted'):
                        return (
                            f'Error: artifact {artifact_id!r} not ready '
                            f'(producer step {producer_step!r} status: {step_status!r}).'
                        )
        except Exception:
            pass  # Defensive: skip DB check on error; Go will re-validate

    # --- Emit task_created signal ---
    task_id = str(uuid.uuid4())
    output_keys = [o['artifact_id'] for o in step_config.get('outputs', [])]
    input_keys = [i['artifact_id'] for i in inputs]

    # Framework tools are always present regardless of plugin declaration.
    declared_tools: List[str] = step_config.get('tools', [])
    merged_tools = _merge_tools(declared_tools)

    params: Dict[str, Any] = {
        'plugin_id': plugin_id,
        'step_id': step_id,
        'session_id': session_id,
        'user_input': user_input,
        'is_cold_start': is_cold_start,
    }
    # Map Python-side runtime_instruction to Go-side retry_hint field name.
    if runtime_instruction:
        params['retry_hint'] = runtime_instruction
    if partial_indices:
        params['partial_indices'] = partial_indices
    # Propagate full per-turn attachment index so SubAgent can access user files.
    history_files_per_turn: dict = cfg.get('history_files_per_turn') or {}
    if history_files_per_turn:
        params['history_files_per_turn'] = history_files_per_turn

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
        input_artifact_keys=input_keys,
        output_artifact_keys=output_keys,
        tools=merged_tools,
        resume=False,
    )
    return f'Step {step_id!r} triggered. Stop here.'


# ---------------------------------------------------------------------------
# Public tool factories
# ---------------------------------------------------------------------------
def _build_step_choices_doc(
    forward_steps: List[str],
    rewind_steps: List[str],
    step_labels: Dict[str, str],
) -> str:
    """Return a formatted string listing available step choices for the LLM."""
    lines = [
        '## Available steps at this moment (authoritative — state machine computed)',
        '--------------------------------------------------------------------------',
        'These are the ONLY valid values for step_id right now.',
        'Do NOT infer step names from scenario descriptions or chat history.',
    ]
    if forward_steps:
        lines.append('Forward (next steps):')
        for s in forward_steps:
            label = step_labels.get(s, '')
            suffix = f'  ({label})' if label else ''
            lines.append(f'  - {s}{suffix}')
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
                    return f'Error: plugin {plugin_id!r} has no reachable first step.'
                return _trigger_plugin_step(plugin_id, step_id, user_input, is_cold_start=True)

            # Set __name__ before wrapping so handle_tool_errors guard checks the
            # final public name (trigger_<plugin_id>), not the inner closure name.
            _trigger.__name__ = tool_name
            if when_to_use:
                tool_desc = f'{when_to_use.rstrip(".")}.  ({desc.rstrip(".")})'
            else:
                tool_desc = desc
            _trigger.__doc__ = (
                f'{tool_desc}\n\n'
                'Args:\n'
                '    user_input (str): A concise goal statement for the SubAgent that\n'
                '        will execute this step.  Synthesise the key intent from the\n'
                '        conversation — do NOT pass vague phrases like "继续", "请继续",\n'
                '        or "continue".  Include: what the user wants to achieve, any\n'
                '        style / quality constraints they mentioned, and relevant context\n'
                '        from the chat history.  Example: "生成一张科幻风格的宇宙飞船插画，\n'
                '        线条简洁，色调冷蓝，适合作为游戏启动画面背景".\n\n'
                'Returns:\n'
                '    Confirmation that the plugin was started.'
            )
            return handle_tool_errors(_trigger)

        tools.append(_make_trigger(pid, first_steps, desc, when_to_use))
    return tools


def build_advance_step_tool(
    plugin_id: str,
    current_step: str,
    rewind_steps: Optional[List[str]] = None,
    step_labels: Optional[Dict[str, str]] = None,
) -> Any:
    """Build the advance_step tool bound to the given plugin and current step.

    Args:
        plugin_id: Plugin identifier.
        current_step: The step that is currently active in the session.
        rewind_steps: Step IDs that are topological ancestors of current_step
            AND have already succeeded in this session.  These are offered to
            the LLM as valid rewind targets in addition to the forward steps.
        step_labels: Mapping of step_id to human-readable label for display in
            the docstring.  Sourced from plugin.yaml steps[].label.
    """
    sm = plugin_loader.get_state_machine(plugin_id)
    forward = sm.get_reachable_steps(current_step) if sm else []
    rewind = list(rewind_steps or [])
    labels = step_labels or {}
    all_reachable = list(forward) + rewind

    choices_doc = _build_step_choices_doc(forward, rewind, labels)

    @handle_tool_errors
    def advance_step(
        step_id: str,
        user_input: str,
        runtime_instruction: Optional[str] = None,
        partial_indices: Optional[Dict[str, List[int]]] = None,
    ) -> str:
        """Advance the active plugin to the next step."""
        if step_id not in all_reachable:
            return (
                f'Error: step {step_id!r} is not reachable from '
                f'{current_step!r}. Reachable: {all_reachable}.'
            )
        return _trigger_plugin_step(
            plugin_id, step_id, user_input,
            is_cold_start=False,
            runtime_instruction=runtime_instruction or '',
            partial_indices=partial_indices or {},
        )

    advance_step.__doc__ = (
        'Advance the active plugin to the next step.\n\n'
        'Use this when there is an active plugin session and you need to\n'
        'trigger or re-trigger a specific step based on user intent.\n\n'
        '## IMPORTANT — use the step list below as the single source of truth\n\n'
        'The "Available steps at this moment" section below is computed from the\n'
        'live state machine and reflects what is actually reachable right now.\n'
        'Any step descriptions in the scenario guide are for background context\n'
        'only. If they differ, trust the list below — not the scenario guide.\n\n'
        'For partial retries (re-running only a subset of list-slot items),\n'
        'set runtime_instruction to a concise directive that tells the SubAgent\n'
        'which items to regenerate, and set partial_indices to mark which list\n'
        'positions should be replaced rather than appended.\n'
        'Both values are ephemeral and only affect this single execution.\n\n'
        '## Rewind guidance\n\n'
        'If the user or the DriverAgent indicates that the problem originates\n'
        'from a prior step (e.g. "the subject analysis was wrong", "please\n'
        're-collect materials"), you should rewind to that earlier step by\n'
        'passing its step_id. The "Rewind" section below lists all previously\n'
        'completed steps that are eligible for re-triggering. Rewinding clears\n'
        'the downstream artifacts and lets the pipeline rebuild from that point.\n\n'
        + choices_doc + '\n\n'
        'Args:\n'
        '    step_id (str): The step to advance to.  Must be one of the\n'
        '        currently available steps listed above.\n'
        '    user_input (str): A concise goal statement for the SubAgent that\n'
        '        will execute this step.  Synthesise the key intent from the\n'
        '        conversation — do NOT pass vague phrases like "继续", "请继续",\n'
        '        "好的", or "continue".  Include: what the user wants to achieve,\n'
        '        constraints or preferences they expressed (style, quality, format),\n'
        '        and any relevant context from prior steps or the chat history.\n'
        '        Example for a retry: "重新生成图片，保持科幻风格，但人物表情要更有力量感".\n'
        '    runtime_instruction (str, optional): An ephemeral directive that\n'
        "        constrains the SubAgent's execution for this run only, e.g.\n"
        '        for partial retries.  Leave empty for normal full runs.\n'
        '    partial_indices (dict, optional): Maps artifact_key to a list\n'
        '        of 0-based list_index values that should be overwritten rather\n'
        '        than appended.  Only relevant for list-cardinality slots.\n\n'
        'Returns:\n'
        '    Confirmation that the step was triggered.'
    )

    return advance_step


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
    """Build the ## Tasks system-prompt section for ChatAgent."""
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

    if not plugin_loader._registry:
        # No plugins registered — inject SubAgent task context for pure SubAgent conversations.
        plugin_system_prompt = _build_chat_agent_task_context(conversation_id)
        return plugin_tools, plugin_system_prompt, plugin_stop_tools, agentic_config_patch, plugin_artifact_context

    if plugin_context and isinstance(plugin_context, dict):
        p_session_id = plugin_context.get('session_id', '')
        p_plugin_id = plugin_context.get('plugin_id', '')
        p_current_step = plugin_context.get('current_step', '')

        if p_session_id and p_plugin_id:
            agentic_config_patch = {
                'plugin_id': p_plugin_id,
                'plugin_session_id': p_session_id,
                'plugin_step': p_current_step,
                'focused_tab': plugin_context.get('focused_tab'),
                'focused_sort_order': plugin_context.get('focused_sort_order'),
            }
            sm = plugin_loader.get_state_machine(p_plugin_id)

            rewind_steps: List[str] = []
            if sm and p_session_id and p_current_step:
                ancestors = sm.get_ancestors(p_current_step)
                succeeded = _fetch_succeeded_steps(p_session_id)
                candidates = ancestors | {p_current_step}
                rewind_steps = sorted(candidates & succeeded)

            step_labels: Dict[str, str] = {}
            spec = plugin_loader.get_plugin(p_plugin_id)
            if spec:
                for sid, scfg in spec._steps.items():
                    lbl = scfg.get('label', '')
                    if lbl:
                        step_labels[sid] = lbl

            # advance_step is always available in an active session.
            # forward_steps / rewind_steps only influence the docstring choices;
            # they do not gate whether the tool itself is injected.
            plugin_tools = [build_advance_step_tool(
                p_plugin_id, p_current_step,
                rewind_steps=rewind_steps,
                step_labels=step_labels,
            )]
            # find_artifact lets ChatAgent look up plugin step outputs by key.
            from lazymind.chat.engine.subagent.tools import find_artifact
            plugin_tools.append(find_artifact)
            # save_plugin_artifact lets ChatAgent write an artifact directly.
            from lazymind.chat.engine.tools.subagent_chat_tools import save_plugin_artifact
            plugin_tools.append(save_plugin_artifact)
            plugin_stop_tools = ['advance_step']
            plugin_system_prompt = plugin_loader.get_scenario(p_plugin_id)
            plugin_artifact_context = _build_session_artifact_section(p_session_id)
        else:
            # Cold start: no active session yet
            plugin_tools = build_cold_start_tools()
            plugin_stop_tools = [t.__name__ for t in plugin_tools]
            if plugin_tools:
                scenarios = [
                    plugin_loader.get_scenario(spec.plugin_id)
                    for spec in (plugin_loader._registry or {}).values()
                ]
                plugin_system_prompt = '\n\n---\n\n'.join(s for s in scenarios if s)
    else:
        # No plugin_context provided: still inject cold-start triggers
        plugin_tools = build_cold_start_tools()
        plugin_stop_tools = [t.__name__ for t in plugin_tools]
        if plugin_tools:
            scenarios = [
                plugin_loader.get_scenario(spec.plugin_id)
                for spec in (plugin_loader._registry or {}).values()
            ]
            plugin_system_prompt = '\n\n---\n\n'.join(s for s in scenarios if s)
        task_context = _build_chat_agent_task_context(conversation_id)
        if task_context:
            plugin_system_prompt = (plugin_system_prompt + '\n\n' + task_context).strip()

    return plugin_tools, plugin_system_prompt, plugin_stop_tools, agentic_config_patch, plugin_artifact_context

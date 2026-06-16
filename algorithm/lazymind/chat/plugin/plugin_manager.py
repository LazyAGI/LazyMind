"""Plugin manager — builds ChatAgent tools for cold-start triggers and step advancement.

Two tool types are registered dynamically per-conversation:

- trigger_<plugin_id>  : Cold-start tool.  Injected when no active plugin session exists.
- advance_step         : Step-advancement tool.  Injected when an active session exists.

Both are stop-tools: after a successful invocation the ReAct loop terminates immediately
without entering a summarize step.

Framework tools (save_artifact / load_artifact / list_artifacts) are always merged into
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
    'load_artifact',
    'list_artifacts',
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
        core_url = str(_cfg.get('core_api_url', 'http://core:8000')).rstrip('/')
        resp = httpx.get(f'{core_url}/plugin-sessions/{session_id}', timeout=3.0)
        if resp.status_code != 200:
            return set()
        steps = resp.json().get('session', {}).get('steps', [])
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
            objective for this execution only.  Used for partial retries where the
            user wants to regenerate only a subset of list artifacts.
            Not persisted to session state.
        partial_indices: Maps artifact_key → list_index values that should overwrite
            existing list-slot entries rather than appending. None means full write.
    """
    cfg = _agentic_config()
    session_id: str = cfg.get('plugin_session_id', '') or str(uuid.uuid4())

    # --- Layer 1: format validation (no DB needed) ---
    if not user_input or not user_input.strip():
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
        core_url = str(_cfg.get('core_api_url', 'http://core:8000')).rstrip('/')
        try:
            resp = httpx.get(
                f'{core_url}/plugin-sessions/{session_id}',
                timeout=3.0,
            )
            if resp.status_code == 200:
                steps_data = {
                    s['step_id']: s['status']
                    for s in resp.json().get('session', {}).get('steps', [])
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

    _write_agent_data(
        'task_created',
        task_id=task_id,
        title=f'{plugin_id}:{step_id}',
        agent_type='plugin_step',
        mode='manual',          # Plugin steps always async; Go controls auto-advance
        objective=_render_step_objective(step_config, user_input, runtime_instruction),
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
        'Available steps at this moment',
        '------------------------------',
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
            @handle_tool_errors
            def _trigger(user_input: str) -> str:
                """Trigger plugin.

                Args:
                    user_input (str): The user's original request that triggered this plugin.

                Returns:
                    Confirmation that the plugin was started.
                """
                step_id = first[0] if first else ''
                if not step_id:
                    return f'Error: plugin {plugin_id!r} has no reachable first step.'
                return _trigger_plugin_step(plugin_id, step_id, user_input, is_cold_start=True)

            _trigger.__name__ = f'trigger_{plugin_id.replace("-", "_")}'
            # Build a short, LLM-friendly description that leads with the trigger condition.
            if when_to_use:
                tool_desc = f'{when_to_use.rstrip(".")}.  ({desc.rstrip(".")})'
            else:
                tool_desc = desc
            _trigger.__doc__ = (
                f'{tool_desc}\n\n'
                'Args:\n'
                "    user_input (str): The user's original request.\n\n"
                'Returns:\n'
                '    Confirmation that the plugin was started.'
            )
            return _trigger

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
        "    user_input (str): The user's latest input or instruction\n"
        '        relevant to this step.\n'
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

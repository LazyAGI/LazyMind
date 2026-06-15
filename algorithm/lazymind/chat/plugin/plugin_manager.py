"""Plugin manager — builds ChatAgent tools for cold-start triggers and step advancement.

Two tool types are registered dynamically per-conversation:

- trigger_<plugin_id>  : Cold-start tool.  Injected when no active plugin session exists.
- advance_step         : Step-advancement tool.  Injected when an active session exists.

Both are stop-tools: after a successful invocation the ReAct loop terminates immediately
without entering a summarize step.
"""
from __future__ import annotations

import uuid
from typing import Any, Dict, List

import lazyllm
from lazyllm.tools.agent.base import _write_agent_data

from lazymind.chat.plugin import plugin_loader
from lazymind.chat.engine.tools.infra import handle_tool_errors


# ---------------------------------------------------------------------------
# Internal helper
# ---------------------------------------------------------------------------

def _agentic_config() -> Dict[str, Any]:
    try:
        return lazyllm.globals['agentic_config'] or {}
    except Exception:
        return {}


def _render_step_objective(step_config: Dict[str, Any], user_input: str) -> str:
    """Replace {{user_input}} in state.yml step.prompt with the actual input.

    Other template vars (e.g. {{optimized_prompt}}) are injected by Go when
    constructing the sub_agent_tasks.objective from sub_agent_artifacts.
    """
    prompt = step_config.get('prompt', '')
    return prompt.replace('{{user_input}}', user_input)


def _trigger_plugin_step(
        plugin_id: str, step_id: str, user_input: str,
        is_cold_start: bool = False) -> str:
    """Shared implementation for trigger_<plugin_id> and advance_step.

    Performs two-layer validation then emits a task_created signal.
    Returns a short status string (the tool return value seen by the LLM).
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

    _write_agent_data(
        'task_created',
        task_id=task_id,
        title=f'{plugin_id}:{step_id}',
        agent_type='plugin_step',
        mode='manual',          # Plugin steps always async; Go controls auto-advance
        objective=_render_step_objective(step_config, user_input),
        params={
            'plugin_id': plugin_id,
            'step_id': step_id,
            'session_id': session_id,
            'user_input': user_input,
            'is_cold_start': is_cold_start,
        },
        input_artifact_keys=input_keys,
        output_artifact_keys=output_keys,
        tools=step_config.get('tools', []),
        resume=False,
    )
    return f'Step {step_id!r} triggered. Stop here.'


# ---------------------------------------------------------------------------
# Public tool factories
# ---------------------------------------------------------------------------

def build_cold_start_tools() -> List[Any]:
    """Build one trigger_<plugin_id> callable per loaded plugin."""
    tools = []
    for spec in (plugin_loader._registry or {}).values():
        pid = spec.plugin_id
        name = spec.yaml.get('name', pid)
        desc = spec.yaml.get('description', f'Trigger the {name} plugin.')
        # First reachable step from __start__
        sm = spec.state_machine
        first_steps = sm.get_reachable_steps('__start__')

        def _make_trigger(plugin_id: str, first: List[str], desc: str):
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
            _trigger.__doc__ = (
                f'{desc}\n\n'
                f'Call this when the user wants to {desc.lower().rstrip(".")}.\n\n'
                'Args:\n'
                "    user_input (str): The user's original request.\n\n"
                'Returns:\n'
                '    Confirmation that the plugin was started.'
            )
            return _trigger

        tools.append(_make_trigger(pid, first_steps, desc))
    return tools


def build_advance_step_tool(plugin_id: str, current_step: str) -> Any:
    """Build the advance_step tool bound to the given plugin and current step."""
    sm = plugin_loader.get_state_machine(plugin_id)
    reachable = sm.get_reachable_steps(current_step) if sm else []

    @handle_tool_errors
    def advance_step(step_id: str, user_input: str) -> str:
        """Advance the active plugin to the next step.

        Use this when there is an active plugin session and you need to
        trigger or re-trigger a specific step based on user intent.

        Args:
            step_id (str): The step to advance to.  Must be one of the
                currently reachable steps.
            user_input (str): The user's latest input or instruction
                relevant to this step.

        Returns:
            Confirmation that the step was triggered.
        """
        if step_id not in reachable:
            return (
                f'Error: step {step_id!r} is not reachable from '
                f'{current_step!r}. Reachable: {reachable}.'
            )
        return _trigger_plugin_step(plugin_id, step_id, user_input, is_cold_start=False)

    return advance_step

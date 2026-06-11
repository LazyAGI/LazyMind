from __future__ import annotations

import logging
from typing import Callable

import lazyllm

from .loader import plugin_loader, StateMachine

logger = logging.getLogger(__name__)


def _find_producer_step(plugin_id: str, artifact_id: str) -> str | None:
    """Find the step_id that produces the given artifact_id from cached step specs."""
    if not plugin_id or not plugin_loader.is_loaded(plugin_id):
        return None
    plugin_yaml = plugin_loader.get_plugin_yaml(plugin_id)
    for step in plugin_yaml.get('steps', []):
        step_id = step.get('id', '')
        spec = plugin_loader.get_step_config(plugin_id, step_id)
        for output in spec.get('outputs', []):
            if output.get('artifact_id') == artifact_id:
                return step_id
    return None


def trigger_plugin_step(step_id: str, user_input: str) -> str:
    """Internal implementation of trigger_<step_id> tools.

    Performs two-layer validation before emitting the step_trigger event:
    1. Format validation (no DB): reachability + non-empty user_input.
    2. Dependency status validation (queries DB via db_session_factory).

    Returns an error string on failure; the LLM re-plans in the same ReAct loop.
    """
    plugin_id = lazyllm.globals.get('agentic_config', {}).get('plugin_id', '')
    plugin_session_id = lazyllm.globals.get('agentic_config', {}).get('plugin_session_id', '')
    current_step = lazyllm.globals.get('agentic_config', {}).get('plugin_step', '')
    db_factory = lazyllm.globals.get('agentic_config', {}).get('db_session_factory')

    # --- Layer 1: format validation ---
    if not user_input or not user_input.strip():
        return ('Error: user_input must not be empty. '
                'Please provide a description of what the user wants.')

    if plugin_id and plugin_loader.is_loaded(plugin_id):
        sm = plugin_loader.get_state_machine(plugin_id)
        # Treat an unknown / empty current_step as a hard error for StateMachine plugins
        # (state.yml present). Only LegacyStateMachine tolerates a missing current_step.
        if isinstance(sm, StateMachine) and not current_step:
            return (
                'Error: current_step is unknown — the session state was not propagated correctly. '
                'Cannot validate reachability. Please do not call advance_step until the session '
                'state is restored.'
            )
        if not sm.is_reachable(current_step, step_id):
            reachable = sm.get_reachable_steps(current_step)
            return (f'Error: step {step_id!r} is not reachable from {current_step!r}. '
                    f'Reachable steps: {reachable}. Please trigger one of the reachable steps.')

    # --- Layer 2: dependency status validation ---
    step_config: dict = {}
    if plugin_id and plugin_loader.is_loaded(plugin_id):
        step_config = plugin_loader.get_step_config(plugin_id, step_id)

    inputs = step_config.get('inputs', [])

    if inputs and db_factory:
        try:
            with db_factory() as db:
                for inp in inputs:
                    artifact_id = inp.get('artifact_id', '')
                    required = inp.get('required', True)
                    producer_step = _find_producer_step(plugin_id, artifact_id)
                    if not producer_step:
                        continue

                    row = db.execute(
                        'SELECT step_status FROM plugin_session_steps '
                        'WHERE session_id=:sid AND step=:step ORDER BY created_at DESC LIMIT 1',
                        {'sid': plugin_session_id, 'step': producer_step}
                    ).fetchone()

                    if row is None:
                        if required:
                            return (f'Error: required artifact {artifact_id!r} is not available. '
                                    f'Step {producer_step!r} has never been executed. '
                                    f'Please trigger {producer_step!r} first.')
                        continue

                    status = row[0] if row else None
                    if status in ('running', 'failed', 'interrupted'):
                        return (f'Error: artifact {artifact_id!r} is not ready '
                                f'(producer step {producer_step!r} status: {status!r}). '
                                f'Cannot proceed until it completes or is retried.')
        except Exception as exc:
            logger.warning('trigger_plugin_step: dependency check failed (%s), continuing', exc)

    # --- Validation passed: emit step_trigger event ---
    step_mode = 'human'
    reachable_step_count = 1
    if plugin_id and plugin_loader.is_loaded(plugin_id):
        plugin_yaml = plugin_loader.get_plugin_yaml(plugin_id)
        step_mode = next(
            (s.get('default_mode', 'human') for s in plugin_yaml.get('steps', [])
             if s['id'] == step_id),
            'human',
        )
        sm = plugin_loader.get_state_machine(plugin_id)
        reachable_step_count = max(1, len(sm.get_reachable_steps(step_id)))

    # Use the shared queue from agentic_config (works across asyncio tasks).
    # Fall back to lazyllm.globals for compatibility.
    _queue = lazyllm.globals.get('agentic_config', {}).get('plugin_event_queue') \
        or lazyllm.globals.get('plugin_event_queue', [])
    _queue.append({
        'type': 'step_trigger',
        'plugin_id': plugin_id,
        'plugin_session_id': plugin_session_id,
        'step_id': step_id,
        'step_mode': step_mode,
        'user_input': user_input.strip(),
        'inputs': inputs,
        'reachable_step_count': reachable_step_count,
    })
    return f'Step {step_id!r} has been triggered. Stop here and do not output any further text.'


def _launch_plugin(plugin_id: str, user_input: str) -> str:
    """Cold-start a plugin: emit mount + step_trigger(initial_step) in one shot."""
    initial_step = plugin_loader.get_initial_step(plugin_id)
    if not initial_step:
        return f'Error: plugin {plugin_id!r} has no initial step defined.'

    plugin_yaml = plugin_loader.get_plugin_yaml(plugin_id)
    num_steps = len(plugin_yaml.get('steps', []))
    plugin_session_id = lazyllm.globals.get('agentic_config', {}).get('plugin_session_id', '')

    # Write plugin_id into agentic_config so hot-path trigger tools pick it up.
    agentic_cfg = lazyllm.globals.get('agentic_config', {})
    agentic_cfg['plugin_id'] = plugin_id
    lazyllm.globals['agentic_config'] = agentic_cfg

    # Use the shared queue from agentic_config (works across asyncio tasks).
    queue = agentic_cfg.get('plugin_event_queue') or lazyllm.globals.get('plugin_event_queue', [])

    # mount event — Go creates the session record and replaces the placeholder ID.
    queue.append({
        'type': 'mount',
        'plugin_id': plugin_id,
        'plugin_session_id': plugin_session_id,
        'num_steps': num_steps,
    })

    # Determine step_mode for the initial step.
    step_mode = next(
        (s.get('default_mode', 'human') for s in plugin_yaml.get('steps', [])
         if s.get('id') == initial_step),
        'human',
    )
    sm = plugin_loader.get_state_machine(plugin_id)
    reachable_step_count = max(1, len(sm.get_reachable_steps(initial_step)))

    queue.append({
        'type': 'step_trigger',
        'plugin_id': plugin_id,
        'plugin_session_id': plugin_session_id,
        'step_id': initial_step,
        'step_mode': step_mode,
        'user_input': user_input.strip(),
        'inputs': [],
        'reachable_step_count': reachable_step_count,
    })

    return f'Plugin {plugin_id!r} launched. Stop here and do not output any further text.'


def build_all_plugin_tools() -> list[Callable]:
    """Build one trigger_<plugin_id> cold-start tool per loaded plugin.

    These tools are always added to the ChatAgent tool set regardless of whether
    a plugin session is currently active. The LLM selects the right plugin based
    on user intent. Each tool emits mount + step_trigger events in a single shot,
    so Go can create the session and enter the plugin loop without a second round-trip.
    """
    tools: list[Callable] = []
    for pid in plugin_loader.list_plugin_ids():
        plugin_yaml = plugin_loader.get_plugin_yaml(pid)
        trigger_desc = plugin_yaml.get('trigger_description', '').strip()
        if not trigger_desc:
            trigger_desc = plugin_yaml.get('description', f'Launch the {pid} plugin.')

        def make_launcher(plugin_id: str, doc: str) -> Callable:
            def launcher(user_input: str) -> str:
                """Launch a plugin session.

                Args:
                    user_input: Description of what the user wants.
                """
                return _launch_plugin(plugin_id, user_input)

            launcher.__name__ = f'trigger_{plugin_id}'
            launcher.__qualname__ = f'trigger_{plugin_id}'
            launcher.__doc__ = doc
            return launcher

        tools.append(make_launcher(pid, trigger_desc))
    return tools


def build_advance_step_tool(plugin_id: str, current_step: str) -> list[Callable]:
    """Build a single advance_step tool for an active plugin session.

    When a plugin session is live (plugin_id is set), ChatAgent exposes exactly
    one tool instead of one-tool-per-step.  The step_id is a free-form string
    argument, but the docstring enumerates the currently reachable steps so the
    LLM has an explicit enum-like constraint without requiring dynamic schema
    generation.

    Returns a one-element list so the call-site can use the same
    `agent_tools += result` pattern as build_all_plugin_tools().
    """
    if not plugin_id or not plugin_loader.is_loaded(plugin_id):
        return []

    sm = plugin_loader.get_state_machine(plugin_id)

    # For StateMachine plugins (state.yml present), an empty current_step means the
    # session state was not propagated — expose no reachable steps and warn the LLM.
    if isinstance(sm, StateMachine) and not current_step:
        doc = (
            'ERROR: session state is missing (current_step is unknown). '
            'Do NOT call this tool. Inform the user that the session state could not be '
            'restored and ask them to retry.'
        )

        def advance_step_blocked(step_id: str, user_input: str) -> str:
            """Advance the active plugin session to the next step (currently blocked).

            Args:
                step_id: The step to trigger.
                user_input: Description of what the user wants for this step.
            """
            return (
                'Error: current_step is unknown — the session state was not propagated correctly. '
                'Cannot validate reachability. Please do not call advance_step until the session '
                'state is restored.'
            )

        advance_step_blocked.__doc__ = doc
        return [advance_step_blocked]

    reachable_steps = sm.get_reachable_steps(current_step)
    # Exclude current step from the reachable list exposed to the LLM.
    if current_step:
        reachable_steps = [s for s in reachable_steps if s != current_step]

    reachable_str = ', '.join(f'"{s}"' for s in reachable_steps) if reachable_steps else '(none)'

    doc = (
        f'Advance the active plugin session to the next step.\n\n'
        f'Reachable steps right now: {reachable_str}\n\n'
        'Args:\n'
        '    step_id: The step to trigger. Must be one of the reachable steps listed above.\n'
        '    user_input: Clear description of what the user wants for this step.\n\n'
        'Call this tool once and stop immediately after. Do not call any other tool in the same turn.'
    )

    def advance_step(step_id: str, user_input: str) -> str:
        """Advance the active plugin session to the next step.

        Args:
            step_id: The step to trigger (must be a reachable step).
            user_input: Description of what the user wants for this step.
        """
        return trigger_plugin_step(step_id, user_input)

    advance_step.__doc__ = doc
    return [advance_step]

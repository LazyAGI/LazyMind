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

import uuid
from typing import Any, Dict, List, Optional

import lazyllm
from lazyllm.tools.agent.base import _write_agent_data

from lazymind.chat.plugin import plugin_loader


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
        raise ValueError('user_input must not be empty.')

    sm = plugin_loader.get_state_machine(plugin_id)
    if sm is None:
        raise ValueError(f'plugin {plugin_id!r} not found.')

    current_step: str = cfg.get('plugin_step', '')
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
        input_artifact_keys=[],
        output_artifact_keys=[],
        tools=[],
        resume=False,
    )
    return 'Plugin session completed. Stop here.'


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
                '        will execute this step.  Synthesise the key intent from the\n'
                '        conversation — do NOT pass vague phrases like "继续", "请继续",\n'
                '        or "continue".  Include: what the user wants to achieve, any\n'
                '        style / quality constraints they mentioned, and relevant context\n'
                '        from the chat history.  Example: "生成一张科幻风格的宇宙飞船插画，\n'
                '        线条简洁，色调冷蓝，适合作为游戏启动画面背景".\n\n'
                'Returns:\n'
                '    Confirmation that the plugin was started.'
            )
            return _trigger

        tools.append(_make_trigger(pid, first_steps, desc, when_to_use))
    return tools


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
    all_reachable = list(forward) + rewind

    choices_doc = _build_step_choices_doc(forward, rewind, labels)

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

        Use `step_id="__end__"` when the pipeline is fully complete.
        """
        if step_id == '__end__':
            return _trigger_plugin_end(plugin_id)
        if step_id not in all_reachable:
            raise ValueError(
                f'step {step_id!r} is not reachable from '
                f'{current_step!r}. Reachable: {all_reachable}.'
            )
        return _trigger_plugin_step(
            plugin_id, step_id, user_input,
            is_cold_start=False,
            runtime_instruction=runtime_instruction or '',
            partial_indices=partial_indices or {},
        )

    advance_step_and_hand_off.__doc__ = (
        'Advance the active plugin to the next step and hand off control to SubAgent/user.\n\n'
        'The step runs in the background. Use this as the default advancement tool.\n'
        'Only use `advance_step` (synchronous) when you need intermediate step results\n'
        'within a single turn (e.g. user said "re-run steps 1 through 3").\n\n'
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
        'Call with step_id="__end__" when the final step has succeeded.\n\n'
        '## Rewind guidance\n\n'
        'If the DriverAgent or user indicates a prior step produced bad output, rewind by\n'
        'passing its step_id. Rewind-eligible steps are listed in the "Rewind" section below.\n\n'
        + choices_doc + '\n\n'
        'Args:\n'
        '    step_id (str): Step to advance to (see list above) or "__end__".\n'
        '    user_input (str): Concise goal statement for the SubAgent — synthesise intent\n'
        '        from the conversation.  Do NOT pass vague phrases like "继续" or "continue".\n'
        '    runtime_instruction (str, optional): Ephemeral directive for this run only.\n'
        '    partial_indices (dict, optional): Maps artifact_key → list_index values to\n'
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
    all_reachable = list(forward) + rewind

    choices_doc = _build_step_choices_doc(forward, rewind, labels)

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
        if step_id not in all_reachable:
            raise ValueError(
                f'step {step_id!r} is not reachable from '
                f'{current_step!r}. Reachable: {all_reachable}.'
            )
        result = _trigger_plugin_step(
            plugin_id, step_id, user_input,
            is_cold_start=False,
            runtime_instruction=runtime_instruction or '',
            partial_indices=partial_indices or {},
        )
        # Poll for completion via FileSystemQueue.
        return _wait_for_step_done(step_id, result)

    advance_step.__doc__ = (
        'Advance the active plugin step synchronously and return the result.\n\n'
        'ONLY use this when running multiple steps in one turn. Otherwise use\n'
        '`advance_step_and_hand_off`.\n\n'
        + choices_doc + '\n\n'
        'Args:\n'
        '    step_id (str): Step to advance to (see list above).\n'
        '    user_input (str): Concise goal statement for the SubAgent.\n'
        '    runtime_instruction (str, optional): Ephemeral directive for this run.\n'
        '    partial_indices (dict, optional): List-slot overwrite indices.\n\n'
        'Returns:\n'
        '    Step result summary after SubAgent completes.'
    )
    return advance_step


def _wait_for_step_done(step_id: str, trigger_result: str, timeout: float = 600.0) -> str:
    """Poll FileSystemQueue for a step_done signal; return result summary or timeout message.

    The step_done signal is enqueued by the subagent runner at step completion.
    Polls every 2 seconds up to `timeout` seconds.  Exits early if a 'cancel' control
    message arrives on the step_done queue.
    """
    import time
    import json
    try:
        from lazyllm.common.queue import FileSystemQueue
        cfg = _agentic_config()
        session_id = cfg.get('plugin_session_id', '')
        queue_key = f'step_done_{session_id}_{step_id}'
        fsq = FileSystemQueue(klass=queue_key)
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            for raw in fsq.dequeue():
                try:
                    msg = json.loads(raw)
                    # Support both old tag='step_done' format and new {status, summary} format.
                    if msg.get('tag') == 'step_done' or 'status' in msg:
                        return msg.get('summary', f"Step '{step_id}' completed.")
                    if msg.get('tag') == 'cancel':
                        return f"Step '{step_id}' was stopped by the user."
                except Exception:
                    pass
            time.sleep(2.0)
        return f"Step '{step_id}' timed out waiting for completion (partial result may be available)."
    except Exception:
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
            content (str): The intent/constraint description, in the user's own words.
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

            # find_artifact lets ChatAgent look up plugin step outputs by key.
            from lazymind.chat.engine.subagent.tools import find_artifact
            plugin_tools.append(find_artifact)
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
                    '## Available Plugins\n'
                    'IMPORTANT: Only trigger a plugin when the capability matches the '
                    'user\'s PRIMARY and DIRECT intent — the main goal they are asking for '
                    'right now. Never trigger a plugin for a sub-step that the model has '
                    'internally decided is part of a larger multi-step plan. If the user\'s '
                    'request involves multiple steps and only one of those steps would use a '
                    'plugin, do NOT trigger the plugin. Never infer plugin intent from '
                    'indirect or implicit cues.\n\n'
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
                '## Available Plugins\n'
                'IMPORTANT: Only trigger a plugin when the capability matches the '
                'user\'s PRIMARY and DIRECT intent — the main goal they are asking for '
                'right now. Never trigger a plugin for a sub-step that the model has '
                'internally decided is part of a larger multi-step plan. If the user\'s '
                'request involves multiple steps and only one of those steps would use a '
                'plugin, do NOT trigger the plugin. Never infer plugin intent from '
                'indirect or implicit cues.\n\n'
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
            lines.append(f'\nCurrent step (pending execution this turn): **{_label(current_step)}**')
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
                lines.append('Next forward steps (after current_step succeeds): '
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
        '### Rule 1 — Intent-change detection (highest priority)\n'
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
        '### Rule 3 — "继续" interpretation\n'
        'When the user says "继续" (or similar) with no other context, advance\n'
        '`current_step` (the pending step shown in the step-status block). Do NOT\n'
        'jump ahead to a later step, even if earlier steps already have artifacts.\n'
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
                'After one of these steps **succeeds**, immediately call '
                '`advance_step_and_hand_off(step_id="__end__")` in the same turn '
                'using `advance_step` (synchronous) so the pipeline completes without '
                'requiring the user to click "继续" after the final step.\n\n'
                'Concretely: use `advance_step(step_id=<terminal_step>, ...)` to run the '
                'terminal step and wait for its result, then call '
                '`advance_step_and_hand_off(step_id="__end__")` to close the session.\n'
                'Only do this when the terminal step is the **last** planned step — '
                'if the user wants to review results first, revert to `advance_step_and_hand_off`.'
            )
        common += (
            '- `advance_step`: Queue a step and WAIT for result (dynamic mode only). '
            'Use only when running multiple steps in one turn '
            '(e.g. user said "re-run steps 1 to 3" — use advance_step for steps 1..N-1, '
            'then advance_step_and_hand_off for the last step).\n\n'
            'After each step in dynamic mode, default to advance_step_and_hand_off so the user '
            'can review the result and decide the next action.\n\n'
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

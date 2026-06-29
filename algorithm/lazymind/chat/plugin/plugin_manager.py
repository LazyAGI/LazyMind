"""Plugin manager — builds ChatAgent tools for cold-start triggers and step advancement.

Tool types registered dynamically per-conversation:

- trigger_<plugin_id>       : Cold-start tool. Injected when no active plugin session exists.
- advance_step_and_exit     : Step-advancement tool (stop-tool). Default; queues step and exits ReAct.
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


def _trigger_plugin_end(plugin_id: str) -> str:
    """Emit a task_created event with step_id='__end__' to signal plugin session completion.

    Go's HandlePluginStepCreated intercepts this sentinel and marks the session as completed.
    """
    cfg = _agentic_config()
    session_id: str = cfg.get('plugin_session_id', '')
    if not session_id:
        return 'Error: no active plugin session to complete.'
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


def build_advance_step_and_exit_tool(
    plugin_id: str,
    current_step: str,
    rewind_steps: Optional[List[str]] = None,
    step_labels: Optional[Dict[str, str]] = None,
) -> Any:
    """Build the advance_step_and_exit tool (stop-tool).

    Queues the step and immediately ends the current ReAct turn. The SubAgent runs in
    the background; DriverAgent (auto mode) or the user (dynamic mode) decides next.
    This is the DEFAULT advancement tool registered for both auto and dynamic modes.
    """
    sm = plugin_loader.get_state_machine(plugin_id)
    forward = sm.get_reachable_steps(current_step) if sm else []
    rewind = list(rewind_steps or [])
    labels = step_labels or {}
    all_reachable = list(forward) + rewind

    choices_doc = _build_step_choices_doc(forward, rewind, labels)

    @handle_tool_errors
    def advance_step_and_exit(
        step_id: str,
        user_input: str,
        runtime_instruction: Optional[str] = None,
        partial_indices: Optional[Dict[str, List[int]]] = None,
    ) -> str:
        """Advance the active plugin to the next step and END the current conversation turn.

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

    advance_step_and_exit.__doc__ = (
        'Advance the active plugin to the next step and END this conversation turn.\n\n'
        'The step runs in the background. Use this as the default advancement tool.\n'
        'Only use `advance_step` (synchronous) when you need intermediate step results\n'
        'within a single turn (e.g. user said "re-run steps 1 through 3").\n\n'
        '## Completing the plugin\n\n'
        'Call with step_id="__end__" when the final step has succeeded.\n\n'
        '## Checkpoint-Resume (interrupted steps)\n\n'
        'When the user says "继续" and the step was interrupted (not "重试"):\n'
        '  advance_step_and_exit(step_id=..., runtime_instruction=(\n'
        '    "Previous attempt was interrupted. Check existing artifacts for this step "\n'
        '    "and only produce missing outputs. Do not regenerate already-saved artifacts."))\n'
        'When the user says "重试": advance_step_and_exit(step_id=..., rewind=True)\n\n'
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
    return advance_step_and_exit


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

    @handle_tool_errors
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
        prefer `advance_step_and_exit` to let the user review results.
        """
        if step_id == '__end__':
            return _trigger_plugin_end(plugin_id)
        if step_id not in all_reachable:
            return (
                f'Error: step {step_id!r} is not reachable from '
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
        '`advance_step_and_exit`.\n\n'
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
# ask_user — stop-tool for ChatAgent only
# ---------------------------------------------------------------------------

def build_ask_user_tool() -> Any:
    """Build the ask_user tool for ChatAgent.

    Suspends the current ReAct turn and sends a question to the user.
    The user's answer arrives in the next chat request.
    Registered as a stop-tool so ReAct exits immediately after invocation.
    """
    @handle_tool_errors
    def ask_user(
        question: str,
        choices: Optional[List[str]] = None,
        allow_multiple: bool = False,
    ) -> str:
        """Ask the user a question and end the current ReAct turn.

        The user's answer arrives on the next chat request.  Use this when key
        information is missing that the user must supply (e.g. style preference,
        target audience, specific constraints).

        When to ask:
          - Before starting the plugin: missing critical intent (both modes OK).
          - During plugin execution (dynamic mode only): per-step clarification.
          - Auto mode during execution: only if user explicitly asks to confirm.

        Args:
            question (str): The question to show the user.
            choices (list[str], optional): Predefined answer options.
                If provided, renders as a single/multi-select card in the UI.
            allow_multiple (bool): Whether multiple choices can be selected (default False).

        Returns:
            Placeholder string; ReAct exits immediately after this call.
        """
        ask_id = str(uuid.uuid4())
        _write_agent_data('ask_pending', {
            'ask_id': ask_id,
            'question': question,
            'choices': choices or [],
            'allow_multiple': allow_multiple,
        })
        return f'Question sent to user (ask_id={ask_id}). Waiting for answer on next turn.'

    return ask_user


# ---------------------------------------------------------------------------
# update_intent — ChatAgent only, persists intent/constraint to DB
# ---------------------------------------------------------------------------

def build_update_intent_tool() -> Any:
    """Build the update_intent tool for ChatAgent.

    UPSERT a global or step-level intent/constraint. Plugin-agnostic — the
    framework manages this, not the plugin author.
    """
    @handle_tool_errors
    def update_intent(
        scope: str,
        content: str,
        step_id: Optional[str] = None,
    ) -> str:
        """Record or update an intent/constraint for this plugin session.

        Scope 'session' affects the entire session (global constraint).
        Scope 'step' affects only the specified step_id.

        Use this whenever the user expresses a preference or constraint that
        should guide current and future step executions, e.g.:
          - "全程保持清淡风格" → scope='session'
          - "第2步只要竖版图片" → scope='step', step_id='generate_images'

        Args:
            scope (str): 'session' for global or 'step' for step-specific constraint.
            content (str): The intent/constraint description.
            step_id (str, optional): Required when scope='step'.

        Returns:
            Confirmation string.
        """
        cfg = _agentic_config()
        session_id = cfg.get('plugin_session_id', '')
        if not session_id:
            return 'Error: no active plugin session.'
        try:
            from lazymind.chat.engine.subagent.db import TaskQueryDB
            db = TaskQueryDB()
            if scope == 'session':
                db.upsert_session_intent(session_id, content)
            elif scope == 'step':
                if not step_id:
                    return 'Error: step_id required for scope="step".'
                db.upsert_step_intent(session_id, step_id, content)
            else:
                return f'Error: unknown scope {scope!r}. Use "session" or "step".'
            return '约束已更新'
        except Exception as exc:
            return f'Error updating intent: {exc}'

    return update_intent


# ---------------------------------------------------------------------------
# Schedule management tools (ChatAgent only, always available)
# ---------------------------------------------------------------------------

def build_schedule_tools() -> List[Any]:
    """Build create_schedule / list_schedules / cancel_schedule tools for ChatAgent."""

    @handle_tool_errors
    def create_schedule(
        cron_expr: str,
        prompt_template: str,
        timezone: str = 'Asia/Shanghai',
        conversation_id: Optional[str] = None,
    ) -> str:
        """Create a recurring scheduled task.

        Args:
            cron_expr: 5-field cron expression, e.g. '0 9 * * 1-5' for 9am weekdays.
            prompt_template: The query that will be sent to this conversation on each trigger.
            timezone: IANA timezone name. Defaults to 'Asia/Shanghai'.
            conversation_id: Bind to a specific conversation. Defaults to the current one.
        """
        import httpx
        from lazymind.config import config as _cfg
        cfg = _agentic_config()
        conv_id = conversation_id or cfg.get('conversation_id', '')
        core_url = str(_cfg['core_api_url']).rstrip('/')
        payload = {
            'cron_expr': cron_expr,
            'prompt_template': prompt_template,
            'timezone': timezone,
        }
        if conv_id:
            payload['conversation_id'] = conv_id
        resp = httpx.post(f'{core_url}/schedules', json=payload, timeout=10.0)
        if resp.status_code not in (200, 201):
            return f'Failed to create schedule: {resp.text}'
        data = resp.json()
        return (
            f"Schedule created (id={data.get('id')}).\n"
            f"Next run: {data.get('next_run_at')} | Cron: {cron_expr}"
        )

    @handle_tool_errors
    def list_schedules() -> str:
        """List all active recurring schedules for this user."""
        import httpx
        from lazymind.config import config as _cfg
        core_url = str(_cfg['core_api_url']).rstrip('/')
        resp = httpx.get(f'{core_url}/schedules', timeout=5.0)
        if resp.status_code != 200:
            return f'Could not fetch schedules: {resp.text}'
        items = resp.json().get('items', [])
        if not items:
            return 'No active schedules.'
        lines = ['## Active schedules']
        for s in items:
            lines.append(
                f"- id={s.get('id')} | cron={s.get('cron_expr')} "
                f"| next={s.get('next_run_at')} | {s.get('prompt_template', '')[:60]}"
            )
        return '\n'.join(lines)

    @handle_tool_errors
    def cancel_schedule(schedule_id: str) -> str:
        """Cancel (disable) a recurring schedule by its ID."""
        import httpx
        from lazymind.config import config as _cfg
        core_url = str(_cfg['core_api_url']).rstrip('/')
        resp = httpx.post(f'{core_url}/schedules/{schedule_id}:cancel', timeout=5.0)
        if resp.status_code != 200:
            return f'Failed to cancel schedule {schedule_id!r}: {resp.text}'
        return f'Schedule {schedule_id!r} has been cancelled.'

    return [create_schedule, list_schedules, cancel_schedule]


# ---------------------------------------------------------------------------
# Read-only query tools (ChatAgent only, active session required)
# ---------------------------------------------------------------------------

def build_query_tools() -> List[Any]:
    """Build read-only plugin state query tools for ChatAgent."""

    @handle_tool_errors
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
            lines = ['## Plugin session steps']
            for s in steps:
                lines.append(f'- {s.get("step_id")}: {s.get("status")} (attempt {s.get("attempt", 1)})')
            return '\n'.join(lines)
        except Exception as exc:
            return f'Error querying steps: {exc}'

    @handle_tool_errors
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

    @handle_tool_errors
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
    """Build the ## Tasks system-prompt section for ChatAgent."""
    conv_id = conversation_id.strip()
    if not conv_id:
        return ''
    from lazymind.chat.engine.subagent.db import TaskQueryDB
    return TaskQueryDB().build_chat_agent_task_context(conv_id)


def resolve_plugin_injection(
    plugin_context: Optional[Dict[str, Any]],
    conversation_id: str = '',
    ask_response: Optional[Dict[str, Any]] = None,
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
                candidates = ancestors | {p_current_step}
                rewind_steps = sorted(candidates & succeeded)

            step_labels: Dict[str, str] = {}
            spec = plugin_loader.get_plugin(p_plugin_id)
            if spec:
                for sid, scfg in spec._steps.items():
                    lbl = scfg.get('label', '')
                    if lbl:
                        step_labels[sid] = lbl

            # Build plugin tools according to plugin_mode.
            # advance_step_and_exit is always registered (stop-tool).
            # advance_step (sync) is only registered in dynamic mode.
            plugin_tools = [build_advance_step_and_exit_tool(
                p_plugin_id, p_current_step,
                rewind_steps=rewind_steps,
                step_labels=step_labels,
            )]
            plugin_stop_tools = ['advance_step_and_exit']

            if plugin_mode == 'dynamic':
                plugin_tools.append(build_advance_step_tool(
                    p_plugin_id, p_current_step,
                    rewind_steps=rewind_steps,
                    step_labels=step_labels,
                ))

            # ask_user is always available to ChatAgent (stop-tool).
            ask_tool = build_ask_user_tool()
            plugin_tools.append(ask_tool)
            plugin_stop_tools.append('ask_user')

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

            # Inject ask_response so ChatAgent knows the user replied to an ask_pending card.
            if ask_response and isinstance(ask_response, dict):
                ask_id = ask_response.get('ask_id', '')
                selected = ask_response.get('selected', [])
                if ask_id and selected:
                    ask_section = (
                        f'\n\n[ASK_RESPONSE] The user replied to ask request "{ask_id}".\n'
                        f'Selected options: {", ".join(str(s) for s in selected)}\n'
                        'Process this response and continue the workflow accordingly.'
                    )
                    plugin_artifact_context = (plugin_artifact_context + ask_section).strip()

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
            # ask_user is always available to ChatAgent, even pre-session.
            ask_tool = build_ask_user_tool()
            plugin_tools.append(ask_tool)
            plugin_stop_tools.append('ask_user')
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
        ask_tool = build_ask_user_tool()
        plugin_tools.append(ask_tool)
        plugin_stop_tools.append('ask_user')
        if plugin_tools:
            scenarios = [
                plugin_loader.get_scenario(spec.plugin_id)
                for spec in (plugin_loader._registry or {}).values()
            ]
            plugin_system_prompt = '\n\n---\n\n'.join(s for s in scenarios if s)

    return plugin_tools, plugin_system_prompt, plugin_stop_tools, agentic_config_patch, plugin_artifact_context


# ---------------------------------------------------------------------------
# Intent / constraint helpers
# ---------------------------------------------------------------------------

def _build_intent_section(session_id: str, step_id: Optional[str] = None) -> str:
    """Serialize session-level intent/constraints for injection into ChatAgent prompts.

    Only global (session-level) constraints are injected here. Step-level constraints
    are injected directly into SubAgent via runner.py:_build_intent_context_section.
    """
    if not session_id:
        return ''
    try:
        from lazymind.chat.engine.subagent.db import TaskQueryDB
        db = TaskQueryDB()
        session_intent = db.get_session_intent(session_id) if hasattr(db, 'get_session_intent') else None
        if session_intent:
            return f'## 全局约束\n{session_intent}'
        return ''
    except Exception:
        return ''


def _build_mode_guidance(
        plugin_mode: str,
        terminal_steps: Optional[List[str]] = None,
        step_labels: Optional[Dict[str, str]] = None) -> str:
    """Return mode-specific system prompt instructions appended to the scenario."""
    common = (
        '\n\n## Plugin execution guidance\n\n'
        'Tools for step advancement:\n'
        '- `advance_step_and_exit`: Queue a step and end this turn (DEFAULT). '
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
                '`advance_step_and_exit(step_id="__end__")` in the same turn '
                'using `advance_step` (synchronous) so the pipeline completes without '
                'requiring the user to click "继续" after the final step.\n\n'
                'Concretely: use `advance_step(step_id=<terminal_step>, ...)` to run the '
                'terminal step and wait for its result, then call '
                '`advance_step_and_exit(step_id="__end__")` to close the session.\n'
                'Only do this when the terminal step is the **last** planned step — '
                'if the user wants to review results first, revert to `advance_step_and_exit`.'
            )
        common += (
            '- `advance_step`: Queue a step and WAIT for result (dynamic mode only). '
            'Use only when running multiple steps in one turn '
            '(e.g. user said "re-run steps 1 to 3" — use advance_step for steps 1..N-1, '
            'then advance_step_and_exit for the last step).\n\n'
            'After each step in dynamic mode, default to advance_step_and_exit so the user '
            'can review the result and decide the next action.\n\n'
            'When a step is interrupted and user says "继续": call advance_step_and_exit with '
            'runtime_instruction="Previous attempt was interrupted. Check existing artifacts '
            'and only produce missing outputs."\n'
            'When user says "重试": call advance_step_and_exit (no special runtime_instruction).'
            + terminal_hint
        )
    else:  # auto
        common += (
            '\nIn auto mode, always use `advance_step_and_exit`. '
            'Do not use `advance_step` (not available in auto mode). '
            'After calling advance_step_and_exit, the DriverAgent will evaluate the result '
            'and decide the next action automatically.\n\n'
            'Do not ask the user questions during step execution in auto mode '
            'unless the user explicitly requests it.'
        )
    return common

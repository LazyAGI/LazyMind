from __future__ import annotations

import json
import os
import re
import time
from typing import Any, Dict, List, Optional

import lazyllm
from lazyllm import LOG, AutoModel

from lazymind.model_config import inject_model_config
from lazymind.chat.engine.agent_core import build_react_agent, drive_agent
from lazymind.chat.service.component.event_translator import AgentEventFrameTranslator

from lazymind.chat.service.component.tool_registry import DEFAULT_TOOLS, build_agent_tools
from lazyllm.tools.tool_config_inject import inject_tool_config

from .context import SubAgentContext, set_context, LARGE_TOOL_RESULT_THRESHOLD
from .db import SubAgentDB
from . import tools as subagent_tools


_ZH_RE = re.compile(r'[\u4e00-\u9fff]')


def _build_artifact_context_section(
    ctx: 'SubAgentContext', db: 'SubAgentDB'
) -> List[str]:
    """Build a multi-line artifact summary block to inject into the objective prompt.

    Returns an empty list when there are no input artifacts.

    Plugin scenario (params contains session_id):
      Reads from plugin_slot_revisions with sort_order from plugin_slot_order.
      Resolves human vs AI revision for each row, then builds per-key ordered summaries.

    Ordinary SubAgent (no session_id, but has input_artifact_keys):
      Reads from sub_agent_artifacts of succeeded steps in the same session.
      sort_order = seq within the same artifact_key group.
    """
    params = ctx.params
    session_id: str = params.get('session_id', '')

    if session_id:
        return db.format_plugin_session_artifacts(session_id)

    if ctx.input_artifact_keys:
        steps = db.load_plugin_session_steps(session_id) if session_id else []
        succeeded_task_ids = [
            s['task_id'] for s in steps
            if s.get('status') == 'succeeded' and s.get('task_id')
        ]
        if not succeeded_task_ids:
            return []
        return db.format_task_artifacts(succeeded_task_ids)

    return []


def _resolve_plugin_step_tools(params: Dict[str, Any]) -> Optional[List[str]]:
    """Resolve the tool list for a plugin_step task from plugin_loader.

    Returns None if the plugin or step cannot be resolved (falls back to caller default).
    """
    try:
        from lazymind.chat.plugin import plugin_loader as _loader
        plugin_id: str = params.get('plugin_id', '')
        step_id: str = params.get('step_id', '')
        if not plugin_id or not step_id:
            return None
        step_config = _loader.get_step_config(plugin_id, step_id)
        if not step_config and not plugin_id:
            return None
        # If the plugin itself doesn't exist, get_step_config returns {}.
        # Distinguish "step exists but has no tools" from "plugin not found"
        # by checking whether the plugin is registered.
        if _loader.get_plugin(plugin_id) is None:
            return None
        declared: List[str] = step_config.get('tools', [])
        # Mirror _merge_tools from plugin_manager: prepend framework tools.
        _FRAMEWORK_TOOLS = [
            'save_artifact', 'get_artifact', 'list_artifacts',
            'list_knowledge_bases', 'read_user_attachment', 'find_user_attachment',
            'find_artifact', 'patch_artifact', 'discard_draft',
        ]
        seen: set = set()
        merged: List[str] = []
        for t in _FRAMEWORK_TOOLS + list(declared):
            if t not in seen:
                seen.add(t)
                merged.append(t)
        return merged
    except Exception as exc:
        LOG.warning(f'[SubAgent] _resolve_plugin_step_tools failed: {exc}')
        return None


def _resolve_runtime_tools(explicit: Optional[List[str]], plugin_id: Optional[str] = None) -> List[Any]:
    """Build the runtime tool list for a SubAgent.

    If explicit tool names are provided, each name is resolved in order:
      1. Plugin script tools (loaded from the plugin's tool_scripts declarations).
      2. DEFAULT_TOOLS registry (framework / global tools).
    If a name is not found in either source it is silently skipped and a warning is logged.

    When explicit is None/empty, fall back to all DEFAULT_TOOLS.

    Note: save_artifact, get_artifact, and list_artifacts are always available regardless
    of this list — they are injected as mandatory base tools in _build_subagent_tools.
    Names of base tools in the explicit list are silently ignored (already present).
    """
    _BASE_TOOL_NAMES = {'save_artifact', 'get_artifact', 'list_artifacts',
                        'list_knowledge_bases', 'read_user_attachment', 'find_user_attachment',
                        'find_artifact', 'patch_artifact', 'discard_draft'}
    if explicit:
        name_list = [str(n).strip() for n in explicit if str(n).strip() and str(n).strip() not in _BASE_TOOL_NAMES]
        # Build lookup from DEFAULT_TOOLS
        default_by_name = {cfg.name: cfg for cfg in DEFAULT_TOOLS}
        # Build lookup from plugin script tools
        script_by_name: Dict[str, Any] = {}
        if plugin_id:
            try:
                from lazymind.chat.plugin import plugin_loader as _loader
                for fn_name in _loader.list_script_tool_names(plugin_id):
                    fn = _loader.get_script_tool(plugin_id, fn_name)
                    if fn is not None:
                        script_by_name[fn_name] = fn
            except Exception as exc:
                LOG.warning('[SubAgent] failed to load script tools for plugin=%s: %s', plugin_id, exc)

        result = []
        for name in name_list:
            if name in script_by_name:
                result.append(script_by_name[name])
            elif name in default_by_name:
                resolved = build_agent_tools([default_by_name[name]])
                result.extend(resolved)
            else:
                LOG.warning('[SubAgent] tool %r not found in plugin scripts or DEFAULT_TOOLS — skipped', name)
        return result
    return build_agent_tools(list(DEFAULT_TOOLS))


def _build_subagent_tools(extra_tools: Optional[List[Any]]) -> List[Any]:
    """Combine mandatory SubAgent infra tools with optional domain tools.

    save_artifact, get_artifact, list_artifacts, list_knowledge_bases,
    read_user_attachment, and find_user_attachment are always included regardless of
    the explicit tools list — they are the SubAgent's core interface and must never
    be stripped by plugin tool configurations.
    """
    base = [
        subagent_tools.save_artifact,
        subagent_tools.get_artifact,
        subagent_tools.list_artifacts,
        subagent_tools.list_knowledge_bases,
        subagent_tools.read_user_attachment,
        subagent_tools.find_user_attachment,
        subagent_tools.find_artifact,
        subagent_tools.patch_artifact,
        subagent_tools.discard_draft,
    ]
    if extra_tools:
        base.extend(extra_tools)
    return base


_ZH_RE = re.compile('[\u3400-\u4dbf\u4e00-\u9fff\uf900-\ufaff]')


def _build_partial_sort_order_hints(session_id: str, partial_indices: 'Dict[str, List[int]]',
                                    plugin_id: str = '') -> str:
    """Translate partial_indices (0-based list_index) into sort_order guidance for the AI.

    Queries Go core to resolve each list_index to its current 1-based sort_order,
    then returns a concise instruction block the AI can act on directly.
    Returns an empty string on any error or when translation is unnecessary.
    """
    try:
        import httpx
        from lazymind.config import config as _cfg
        from lazymind.chat.plugin import plugin_loader
        core_url = str(_cfg['core_api_url']).rstrip('/')

        if not plugin_id:
            return ''
        spec = plugin_loader.get_plugin(plugin_id)
        if not spec:
            return ''

        hints: List[str] = []
        for artifact_key, list_indexes in partial_indices.items():
            slot_def = spec.get_slot_for_artifact_key(artifact_key)
            if not slot_def:
                continue
            slot_id = slot_def.get('id', '')
            if not slot_id:
                continue
            # Fetch order_list for this slot.
            resp = httpx.get(
                f'{core_url}/plugin-sessions/{session_id}/slots/{slot_id}/order',
                timeout=3.0,
            )
            if resp.status_code != 200:
                continue
            order_list: list = resp.json().get('data', {}).get('order_list', [])
            if not order_list:
                continue
            # Build list_index → sort_order map.
            li_to_so = {li: (pos + 1) for pos, li in enumerate(order_list)}
            sort_orders = [li_to_so[li] for li in list_indexes if li in li_to_so]
            if sort_orders:
                so_str = ', '.join(str(s) for s in sort_orders)
                hints.append(
                    f'For artifact key "{artifact_key}": overwrite the item(s) at '
                    f'sort_order={so_str} — pass sort_order=N when calling save_artifact '
                    f'so that only those position(s) are replaced.'
                )
        if not hints:
            return ''
        return (
            '## Partial retry instruction (AUTHORITATIVE)\n'
            'This is a partial re-run. You must overwrite specific items rather than appending new ones.\n'
            + '\n'.join(hints)
            + '\nDo NOT omit sort_order for these items, and do NOT overwrite other positions.'
        )
    except Exception:
        return ''


def _build_attachment_context_for_subagent(history_files_per_turn: 'Dict[str, List[str]]') -> str:
    """Build the '## User Uploaded Files' context block for SubAgent prompts.

    Mirrors _build_user_attachment_context in chat_service without cross-layer import.
    Returns an empty string when history_files_per_turn is empty.
    """
    if not history_files_per_turn:
        return ''
    turns: Dict[int, List[str]] = {}
    for key, paths in history_files_per_turn.items():
        if not paths:
            continue
        try:
            turns[int(key)] = paths
        except ValueError:
            continue
    if not turns:
        return ''

    def _describe_file(path: str) -> str:
        name = os.path.basename(path)
        try:
            size_bytes = os.path.getsize(path)
            if size_bytes < 1024:
                return f'{name} ({size_bytes} B)'
            if size_bytes < 1024 * 1024:
                return f'{name} ({size_bytes / 1024:.1f} KB)'
            return f'{name} ({size_bytes / (1024 * 1024):.1f} MB)'
        except OSError:
            return name

    lines: List[str] = ['## User Uploaded Files [queried at request time]']
    for seq in sorted(turns.keys()):
        seen: Dict[str, int] = {}
        names: List[str] = []
        for path in turns[seq]:
            base = os.path.basename(path)
            if base not in seen:
                seen[base] = 0
                names.append(_describe_file(path))
            else:
                seen[base] += 1
                name_no_ext, ext = os.path.splitext(base)
                names.append(f'{name_no_ext}-{seen[base]}{ext}')
        lines.append(f'- Turn {seq}: {", ".join(names)}')
    lines.append('')
    lines.append('Turn numbers are 1-based integers matching the "Turn N" labels above.')
    lines.append('Omit the turn parameter to search the current turn first, then historical turns.')
    lines.append("To read a file's content, call read_user_attachment(filename, turn=N).")
    lines.append("To get a file's accessible URL/path, call find_user_attachment(filename, turn=N).")
    return '\n'.join(lines)


def _objective_prompt(ctx: SubAgentContext, db: Optional['SubAgentDB'] = None) -> str:
    # Detect language from the user_input param (primary) or the full objective text.
    user_input = str(ctx.params.get('user_input') or '')
    is_zh = bool(_ZH_RE.search(user_input) or _ZH_RE.search(ctx.objective))
    lines = [
        'You are an autonomous SubAgent. Complete the objective below using the available tools.',
        'You are NOT allowed to spawn or create sub-agents or delegate tasks to other agents. '
        'Only use the tools explicitly listed in your tool set.',
    ]
    if is_zh:
        lines.append('You MUST respond and write all artifact content in Simplified Chinese(简体中文).')
    lines += [
        '',
        f'Objective: {ctx.objective}',
    ]
    if ctx.params:
        # Filter out partial_indices from params: it contains internal 0-based list_index
        # values which would confuse the AI (it should use 1-based sort_order instead).
        display_params = {k: v for k, v in ctx.params.items() if k != 'partial_indices'}
        lines.append(f'Parameters: {json.dumps(display_params, ensure_ascii=False)}')
    # Inject artifact context: plugin session reads from slot revisions with sort_order;
    # ordinary SubAgent reads from sub_agent_artifacts of prior succeeded steps.
    session_id: str = ctx.params.get('session_id', '')
    if session_id or ctx.input_artifact_keys:
        artifact_section = _build_artifact_context_section(ctx, db) if db else []
        if artifact_section:
            lines.extend(artifact_section)
        elif ctx.input_artifact_keys:
            lines.append(f'Input artifact keys you may read: {", ".join(ctx.input_artifact_keys)}')
    # Inject user attachment context so the SubAgent knows which files were uploaded.
    history_files_per_turn: Dict[str, List[str]] = ctx.params.get('history_files_per_turn') or {}
    attachment_section = _build_attachment_context_for_subagent(history_files_per_turn)
    if attachment_section:
        lines.append(attachment_section)
    # Translate partial_indices (internal 0-based list_index) into sort_order guidance.
    # This tells the AI exactly which display position(s) to overwrite instead of append.
    partial_indices: Dict[str, List[int]] = ctx.params.get('partial_indices') or {}
    plugin_id_for_hints: str = ctx.params.get('plugin_id', '')
    if partial_indices and session_id:
        sort_order_hints = _build_partial_sort_order_hints(
            session_id, partial_indices, plugin_id=plugin_id_for_hints
        )
        if sort_order_hints:
            lines.append(sort_order_hints)
    lines.append(
        'You MUST call save_artifact for EACH of the following keys before you finish — '
        'do NOT skip this step even if you have already written the results in plain text: '
        + ', '.join(ctx.output_artifact_keys)
    )
    lines.append(
        'IMPORTANT: Writing results in your reply text does NOT count as saving an artifact. '
        'You must explicitly call save_artifact(key=..., value=...) for every required key. '
        'The task is considered INCOMPLETE and will be marked as FAILED if any required artifact '
        'key is missing. Do not write a final summary until all save_artifact calls are done.'
    )
    lines.append(
        '## Overwrite vs. Append for list slots\n'
        'save_artifact has an optional sort_order parameter (1-based):\n'
        '- Omit sort_order → append a new item at the end of the list.\n'
        '- Pass sort_order=N → overwrite the item currently at display position N.\n'
        'If the objective says the user wants to replace a specific item '
        '(e.g. "重新收集第二张图", "replace item 3", "redo position N"), '
        'you MUST pass sort_order=N. Omitting it will append a new item instead of replacing.'
    )
    lines.append(
        'After all required artifacts are saved, write a final summary that contains the '
        'actual results and key findings — not only a reference to the artifacts. '
        'For example, if you searched for information, include the information itself. '
        'The summary must be self-contained and directly usable by the caller without '
        'opening any artifact.'
    )
    lines.append(
        '## Artifact local editing guide\n'
        '### When to use patch_artifact vs save_artifact\n'
        '- Targeted edits (fix a paragraph, update a field, rename a section): '
        'use patch_artifact instead of regenerating the full content.\n'
        '- Full rewrite: use save_artifact directly.\n'
        '- You decide based on task semantics; the framework imposes no size restriction.\n'
        '\n'
        '### patch_artifact usage\n'
        '- patch_artifact edits a local draft only — changes are NOT committed yet.\n'
        '- After all edits are done, call save_artifact to commit (creates a new revision).\n'
        '- If you forget to call save_artifact, the framework auto-commits all pending '
        'drafts when the step ends.\n'
        '- patch failed (old_str not found): call get_artifact with start_line/end_line '
        'to read the relevant lines, confirm the exact text, then retry.\n'
        '- To discard all uncommitted edits and revert to the last saved version: '
        'call discard_draft(key).\n'
        '\n'
        '### Reading large artifacts in chunks\n'
        '- get_artifact supports start_line and end_line parameters (1-based, inclusive).\n'
        '- First call: get_artifact(key, start_line=1, end_line=1) — the response includes '
        'total_lines so you can plan subsequent reads without loading the whole file.\n'
        '- Chunked reads bypass the content-size truncation limit.\n'
        '- Typical workflow: read target lines → identify old_str → call patch_artifact.'
    )
    return '\n'.join(lines)


def _truncate_tool_result(ctx: SubAgentContext, result: Any, tool_name: str) -> str:
    """Truncate a large tool result for the LLM.

    If the serialised result exceeds LARGE_TOOL_RESULT_THRESHOLD the full
    content is written to the workspace filesystem and the LLM receives a
    compact notice with the file path and size so it can reference the file
    in subsequent tool calls or reasoning.
    """
    text = result if isinstance(result, str) else json.dumps(result, ensure_ascii=False, default=str)
    encoded = text.encode('utf-8', errors='replace')
    if len(encoded) <= LARGE_TOOL_RESULT_THRESHOLD:
        return text
    try:
        abs_path = ctx.write_large_content(text, hint=tool_name or 'tool_result')
        rel_path = os.path.relpath(abs_path, ctx.workspace_path) if ctx.workspace_path else abs_path
        size_kb = len(encoded) / 1024
        return (
            f'[Large result offloaded to file — {size_kb:.1f} KB]\n'
            f'File path (relative to workspace): {rel_path}\n'
            f'Use this path to reference the content in subsequent reasoning or tool calls.'
        )
    except Exception as exc:
        LOG.warning('[SubAgent] failed to offload large tool result for %s: %s', tool_name, exc)
        # Fallback: truncate with a notice.
        limit = LARGE_TOOL_RESULT_THRESHOLD
        truncated = text[:limit]
        return truncated + f'\n... [truncated — original {len(encoded) // 1024} KB]'


def _persist_step(ctx: SubAgentContext, seq: int, event: Dict[str, Any]) -> None:
    tag = event.get('tag')
    if tag == 'tool_calls':
        tool_calls = []
        for tc in event.get('tool_calls', []) or []:
            if not isinstance(tc, dict):
                continue
            tool_calls.append({
                'id': tc.get('id', ''),
                'name': tc.get('name') or (tc.get('function') or {}).get('name', ''),
                'args': tc.get('args') or (tc.get('function') or {}).get('arguments', {}),
            })
        ctx.db.append_step(ctx.task_id, seq, 'assistant', {'text': '', 'tool_calls': tool_calls})
    elif tag == 'tool_results':
        results = []
        for tr in event.get('tool_results', []) or []:
            if not isinstance(tr, dict):
                continue
            raw_result = tr.get('result', tr.get('content', ''))
            tool_name = tr.get('name', '')
            results.append({
                'tool_call_id': tr.get('id', ''),
                'name': tool_name,
                'result': _truncate_tool_result(ctx, raw_result, tool_name),
            })
        ctx.db.append_step(ctx.task_id, seq, 'tool', {'tool_results': results})


async def run_subagent_stream(
    task_id: str,
    db_dsn: str,
    resume: bool = False,
    model_config: Optional[Dict[str, Any]] = None,
    tool_config: Optional[Dict[str, Any]] = None,
    agent_type: Optional[str] = None,
    tools: Optional[List[str]] = None,
):
    """Async generator yielding Task SSE lines.

    Events: task_start / progress / text / think / artifact / done / error.
    text and think frames come from AgentEventFrameTranslator (same as ChatAgent),
    giving a unified LLM output representation across both agent types.
    """
    start_time = time.time()
    db: Optional[SubAgentDB] = None
    emitted: List[Dict[str, Any]] = []

    def _emit(ev: Dict[str, Any]) -> None:
        emitted.append(ev)

    def _sse(ev: Dict[str, Any]) -> str:
        return 'data: ' + json.dumps(ev, ensure_ascii=False, default=str) + '\n\n'

    try:
        db = SubAgentDB(db_dsn)
        task = db.load_task(task_id)
        if not task:
            yield _sse({'type': 'error', 'status': 'failed', 'message': f'task {task_id} not found'})
            yield 'data: [DONE]\n\n'
            return

        output_keys = _coerce_str_list(task.get('output_artifact_keys'))
        input_keys = _coerce_str_list(task.get('input_artifact_keys'))
        params = _coerce_dict(task.get('params'))

        ctx = SubAgentContext(
            task_id=task_id,
            conversation_id=str(task.get('conversation_id') or ''),
            agent_type=str(task.get('agent_type') or ''),
            objective=str(task.get('objective') or ''),
            params=params,
            workspace_path=str(task.get('workspace_path') or ''),
            input_artifact_keys=input_keys,
            output_artifact_keys=output_keys,
            db=db,
            emit=_emit,
        )
        ctx.ensure_workspace()

        # For plugin_step tasks: remove {{artifact_key}} placeholders from the objective
        # (artifact context is now injected as a summary section in _objective_prompt instead).
        # Also resolve tools from plugin_loader when no explicit list was provided.
        # Go no longer forwards the tools list for plugin_step tasks.
        effective_agent_type = str(task.get('agent_type') or agent_type or '')
        if effective_agent_type == 'plugin_step':
            # Strip any remaining {{artifact_key}} placeholders so they don't confuse the LLM.
            ctx.objective = re.sub(r'\{\{[^}]+\}\}', '', ctx.objective).strip()
            if not tools:
                tools = _resolve_plugin_step_tools(params)

        sid = task_id
        lazyllm.globals._init_sid(sid=sid)
        lazyllm.locals._init_sid(sid=sid)
        inject_model_config(model_config)
        inject_tool_config(tool_config)
        set_context(ctx)

        # For plugin_step tasks: inject plugin context into agentic_config so that
        # save_artifact can resolve sort_order → list_index via the Go core API.
        if effective_agent_type == 'plugin_step':
            history_files_per_turn = params.get('history_files_per_turn') or {}
            all_files = [p for paths in history_files_per_turn.values() for p in paths]
            lazyllm.globals['agentic_config'] = {
                'plugin_id': params.get('plugin_id', ''),
                'plugin_session_id': params.get('session_id', ''),
                'plugin_step': params.get('step_id', ''),
                'query': ctx.objective,
                'files': all_files,
                'history_files_per_turn': history_files_per_turn,
            }

        yield _sse({'type': 'task_start', 'task_id': task_id})

        llm = AutoModel(model='llm')
        runtime_tools = _resolve_runtime_tools(tools, plugin_id=params.get('plugin_id') or None)
        agent = build_react_agent(
            llm=llm,
            tools=_build_subagent_tools(runtime_tools),
            force_summarize_context=ctx.objective,
        )

        step_seq = db.max_step_seq(task_id) + 1 if resume else 0
        resume_history = _rebuild_history_from_steps(db, task_id) if resume else None
        progress = 5
        yield _sse({'type': 'progress', 'task_id': task_id, 'progress': progress,
                    'current_phase': '恢复执行...' if resume else '开始执行...'})

        # translator unifies text/think output with ChatAgent frame semantics.
        translator = AgentEventFrameTranslator(query=ctx.objective)
        final_result: Any = None
        # Accumulate streaming text/think chunks; flush to DB when a tool step follows or at end.
        _pending_text: str = ''
        _pending_think: str = ''

        async for kind, payload in drive_agent(agent, _objective_prompt(ctx, db), history=resume_history):
            if kind == 'event':
                item = payload
                tag = item.get('tag')
                # Persist tool steps for resume / breakpoint recovery.
                if tag in ('tool_calls', 'tool_results'):
                    # Flush accumulated text/think as a single step before tool call.
                    if _pending_think:
                        ctx.db.append_step(task_id, step_seq, 'think', {'content': _pending_think})
                        step_seq += 1
                        _pending_think = ''
                    if _pending_text:
                        ctx.db.append_step(task_id, step_seq, 'text', {'content': _pending_text})
                        step_seq += 1
                        _pending_text = ''
                    _persist_step(ctx, step_seq, item)
                    step_seq += 1
                    # Forward tool steps as SSE events so the frontend can render them.
                    if tag == 'tool_calls':
                        calls = [
                            {
                                'id': tc.get('id', ''),
                                'name': tc.get('name') or (tc.get('function') or {}).get('name', ''),
                                'args': tc.get('args') or (tc.get('function') or {}).get('arguments', {}),
                            }
                            for tc in (item.get('tool_calls') or [])
                            if isinstance(tc, dict)
                        ]
                        if calls:
                            yield _sse({'type': 'tool_calls', 'task_id': task_id, 'tool_calls': calls})
                    elif tag == 'tool_results':
                        results = [
                            {
                                'id': tr.get('id', ''),
                                'name': tr.get('name', ''),
                                'result': str(tr.get('result', tr.get('content', '')))[:2000],
                            }
                            for tr in (item.get('tool_results') or [])
                            if isinstance(tr, dict)
                        ]
                        if results:
                            yield _sse({'type': 'tool_results', 'task_id': task_id, 'tool_results': results})
                    # Drain artifact events emitted synchronously by tools.
                    while emitted:
                        ev = emitted.pop(0)
                        ev['task_id'] = task_id
                        yield _sse(ev)
                    if tag == 'tool_results' and progress < 90:
                        progress = min(90, progress + 15)
                        yield _sse({'type': 'progress', 'task_id': task_id, 'progress': progress,
                                    'current_phase': '执行中...'})
                # Translate all events (text/think/tool_calls/tool_results) via shared translator.
                for frame in translator.feed(item):
                    ev_type = 'think' if frame.get('think') else 'text'
                    yield _sse({'type': ev_type, 'task_id': task_id,
                                'think': frame.get('think'), 'text': frame.get('text')})
                    if ev_type == 'think':
                        _pending_think += frame.get('think') or ''
                    else:
                        _pending_text += frame.get('text') or ''
            else:  # 'final' -- drive_agent propagates future exceptions before yielding this.
                final_result = payload
                # Flush any remaining accumulated text/think as the final step.
                if _pending_think:
                    ctx.db.append_step(task_id, step_seq, 'think', {'content': _pending_think})
                    step_seq += 1
                    _pending_think = ''
                if _pending_text:
                    ctx.db.append_step(task_id, step_seq, 'text', {'content': _pending_text})
                    step_seq += 1
                    _pending_text = ''

        # Drain remaining artifact events.
        while emitted:
            ev = emitted.pop(0)
            ev['task_id'] = task_id
            yield _sse(ev)

        # Flush any buffered text/think from translator (e.g. citation scanning remainder).
        for frame in translator.finish(final_result):
            ev_type = 'think' if frame.get('think') else 'text'
            yield _sse({'type': ev_type, 'task_id': task_id,
                        'think': frame.get('think'), 'text': frame.get('text')})

        # Completeness check: every declared output key must have at least one artifact.
        saved = set(ctx.saved_keys())
        missing = [k for k in output_keys if k not in saved]
        if missing:
            steps = db.load_steps(task_id)
            is_ok, eval_summary = _evaluate_completion(
                llm=llm,
                objective=ctx.objective,
                steps=steps,
                saved_keys=list(saved),
                missing_keys=missing,
                force_result=final_result,
                ctx=ctx,
            )
            cost = round(time.time() - start_time, 3)
            if is_ok:
                _auto_flush_drafts(ctx, db)
                yield _sse({'type': 'done', 'task_id': task_id, 'status': 'succeeded',
                            'summary': eval_summary, 'cost': cost})
            else:
                yield _sse({'type': 'error', 'task_id': task_id, 'status': 'failed',
                            'summary': eval_summary,
                            'message': f'缺少 artifact: {", ".join(missing)}。{eval_summary}'})
            yield 'data: [DONE]\n\n'
            return

        summary = _result_summary(final_result, output_keys)
        cost = round(time.time() - start_time, 3)
        # Auto-flush any pending drafts before emitting done.
        _auto_flush_drafts(ctx, db)
        yield _sse({'type': 'done', 'task_id': task_id, 'status': 'succeeded',
                    'summary': summary, 'cost': cost})
        yield 'data: [DONE]\n\n'
    except Exception as exc:  # noqa: BLE001
        LOG.exception('[SubAgent] run failed')
        exc_summary = str(exc)
        if db is not None:
            try:
                steps = db.load_steps(task_id)
                trace = _steps_to_trace(steps)
                exc_summary = f'异常：{exc}\n执行路径：\n{trace}'
            except Exception:
                pass
        yield _sse({'type': 'error', 'task_id': task_id, 'status': 'failed',
                    'summary': exc_summary, 'message': exc_summary})
        yield 'data: [DONE]\n\n'
    finally:
        if db is not None:
            db.dispose()


def _auto_flush_drafts(ctx: 'SubAgentContext', db: 'SubAgentDB') -> None:
    """Commit any pending draft files as new artifact revisions before the step ends.

    This is a safety net: if the model called patch_artifact but forgot to call
    save_artifact, the edits are not lost — they are committed here at step boundary.
    """
    from . import tools as subagent_tools
    for draft_key, original_type, content in ctx.list_pending_drafts():
        try:
            # Re-use save_artifact logic by calling it with the draft content.
            # Reconstruct the plain key: strip trailing _<list_index> if present.
            subagent_tools.save_artifact(draft_key, content, content_type=original_type)
            ctx.delete_draft(draft_key)
            LOG.info('[SubAgent] auto-flushed draft key=%r for task=%s', draft_key, ctx.task_id)
        except Exception as exc:
            LOG.warning('[SubAgent] auto-flush draft key=%r failed: %s', draft_key, exc)


def _coerce_str_list(value: Any) -> List[str]:
    if isinstance(value, list):
        return [str(v) for v in value if str(v).strip()]
    if isinstance(value, str):
        try:
            parsed = json.loads(value)
        except ValueError:
            return []
        if isinstance(parsed, list):
            return [str(v) for v in parsed if str(v).strip()]
    return []


def _coerce_dict(value: Any) -> Dict[str, Any]:
    if isinstance(value, dict):
        return value
    if isinstance(value, str):
        try:
            parsed = json.loads(value)
        except ValueError:
            return {}
        if isinstance(parsed, dict):
            return parsed
    return {}


def _result_summary(result: Any, output_keys: List[str]) -> str:
    if isinstance(result, str) and result.strip():
        return result.strip()
    if output_keys:
        return f'已完成，产出：{", ".join(output_keys)}'
    return '已完成'


def _steps_to_trace(steps: List[Dict[str, Any]]) -> str:
    """Convert persisted steps into a compact execution trace string for LLM review."""
    lines: List[str] = []
    for s in steps:
        role = s.get('role', '')
        content = s.get('content') or {}
        if role == 'assistant':
            calls = content.get('tool_calls') or []
            names = ', '.join(tc.get('name', '?') for tc in calls) if calls else '（无工具调用）'
            lines.append(f'[assistant] called: {names}')
        elif role == 'tool':
            results = content.get('tool_results') or []
            for r in results:
                name = r.get('name', '?')
                res = str(r.get('result', ''))[:300]
                lines.append(f'[tool:{name}] {res}')
    return '\n'.join(lines) if lines else '（无步骤记录）'


def _evaluate_completion(
    llm: Any,
    objective: str,
    steps: List[Dict[str, Any]],
    saved_keys: List[str],
    missing_keys: List[str],
    force_result: Any,
    ctx: Optional[Any] = None,
) -> tuple:
    """Ask the LLM to judge whether the SubAgent substantively completed the objective.

    Returns (is_succeeded: bool, summary: str).
    The summary must contain actual findings/results, not references to artifacts.

    If the LLM judges YES and ctx is provided, the final output is auto-saved as a
    text artifact for each missing key so the task is not penalised for a missing
    save_artifact call when the content is clearly present in the final output.
    """
    trace = _steps_to_trace(steps)
    force_text = str(force_result or '').strip()
    saved_str = ', '.join(saved_keys) if saved_keys else '（无）'
    missing_str = ', '.join(missing_keys) if missing_keys else '（无）'

    prompt_lines = [
        'You are reviewing the execution of an autonomous SubAgent that stopped without '
        'calling save_artifact for all required output keys.',
        '',
        f'Original objective: {objective}',
        f'Required artifact keys: {missing_str or saved_str}',
        f'Actually saved artifact keys: {saved_str}',
        f'Missing artifact keys: {missing_str}',
        '',
        'Execution trace (tool calls and results):',
        trace,
    ]
    if force_text:
        prompt_lines += ['', f'Agent final output: {force_text[:2000]}']
    prompt_lines += [
        '',
        'Evaluation rules:',
        '- Answer YES if the agent gathered and delivered the information needed to satisfy '
        'the objective, even if it forgot to call save_artifact. The final output text counts '
        'as evidence of completion.',
        '- Answer NO only if the agent clearly failed to obtain the required information '
        '(e.g. all tool calls errored out, or the output is empty / irrelevant).',
        '',
        'Based on the above, answer TWO things:',
        '1. Did the SubAgent substantively achieve the objective? Reply YES or NO on the first line.',
        '2. Write a self-contained summary of what was actually accomplished (include key findings, '
        'data, or results inline — not references to artifacts). '
        'If nothing useful was accomplished, briefly explain what went wrong.',
    ]
    eval_prompt = '\n'.join(prompt_lines)

    try:
        summarize_llm = llm.share(stream=False)
        resp = summarize_llm(eval_prompt)
        text = resp if isinstance(resp, str) else (
            resp.get('content', '') if isinstance(resp, dict) else ''
        )
        text = (text or '').strip()
        first_line = text.split('\n')[0].strip().upper()
        is_succeeded = first_line.startswith('YES')
        rest = text[len(text.split('\n')[0]):].strip() if '\n' in text else text
        summary = rest if rest else text

        # Auto-save final output as text artifacts for each missing key when the
        # LLM judges the task as succeeded. This recovers from models that forget
        # to call save_artifact but include the results in their final reply.
        if is_succeeded and ctx is not None and force_text and missing_keys:
            content = summary if summary else force_text
            for key in missing_keys:
                try:
                    seq = ctx.next_artifact_seq(key)
                    ctx.record_local_artifact(key, 'text', {'text': content}, seq)
                    ctx.db.save_artifact(ctx.task_id, key, 'text', {'text': content}, seq)
                    ctx.emit({'type': 'artifact', 'artifact_key': key,
                              'content_type': 'text', 'seq': seq, 'value': {'text': content}})
                    LOG.info(f'[SubAgent] auto-saved missing artifact key={key!r} for task={ctx.task_id}')
                except Exception as save_err:
                    LOG.warning(f'[SubAgent] auto-save artifact key={key!r} failed: {save_err}')

        return is_succeeded, summary
    except Exception as e:
        LOG.warning(f'[SubAgent] _evaluate_completion LLM call failed: {e}')
        return False, f'执行中断，已完成步骤数：{len(steps)}，缺少产出：{missing_str}'


def _rebuild_history_from_steps(db: SubAgentDB, task_id: str) -> List[Dict[str, Any]]:
    """Rebuild LLM chat history from persisted steps for resume.

    Validates tool_call_id pairing: every assistant tool_call must have a matching tool result.
    A tool step whose result has no preceding assistant tool_call id (orphan) is discarded, and
    replay stops at the last complete assistant boundary.

    Also validates that every tool_call's function.arguments is valid JSON.  If any arguments
    field is malformed (e.g. persisted from a truncated stream), the offending assistant message
    and everything after it are dropped so the model never receives corrupt history.
    """
    steps = db.load_steps(task_id)
    history: List[Dict[str, Any]] = []
    pending_ids: set = set()
    for step in steps:
        role = step.get('role')
        content = step.get('content') or {}
        if role == 'assistant':
            tool_calls = content.get('tool_calls') or []
            # Validate function.arguments JSON before appending.
            for tc in tool_calls:
                args = (tc.get('function') or {}).get('arguments') or tc.get('args')
                if args and isinstance(args, str):
                    try:
                        json.loads(args)
                    except (ValueError, TypeError):
                        # Corrupt arguments: stop replay at the last clean boundary.
                        LOG.warning(
                            f'[SubAgent] resume: dropping corrupt tool_call '
                            f'(task={task_id}, name={(tc.get("function") or {}).get("name")})'
                        )
                        return history
            pending_ids = {tc.get('id') for tc in tool_calls if tc.get('id')}
            history.append({
                'role': 'assistant',
                'content': content.get('text', ''),
                'tool_calls': tool_calls,
            })
        elif role == 'tool':
            results = content.get('tool_results') or []
            valid = [r for r in results if r.get('tool_call_id') in pending_ids]
            if not valid:
                # Orphan tool results: drop and stop replay at the last complete boundary.
                if history and history[-1].get('role') == 'assistant':
                    history.pop()
                break
            for r in valid:
                history.append({
                    'role': 'tool',
                    'tool_call_id': r.get('tool_call_id'),
                    'name': r.get('name', ''),
                    'content': str(r.get('result', '')),
                })
            pending_ids = set()
    return history

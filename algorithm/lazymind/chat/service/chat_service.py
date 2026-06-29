from __future__ import annotations
import asyncio
import json
import re
import time
from typing import Any, Dict, List, Optional, Union
import lazyllm
from lazyllm import LOG, set_trace_context
from fastapi.responses import StreamingResponse
from lazymind.chat.config import (
    IMAGE_EXTENSIONS,
    LAZYMIND_LLM_PRIORITY,
    MAX_CONCURRENCY,
    RAG_MODE,
    SENSITIVE_FILTER_RESPONSE_TEXT,
    SENSITIVE_WORDS_PATH,
)
from lazymind.chat.engine.prompts import build_system_prompt
from lazymind.chat.service.component import (
    AgentEventFrameTranslator,
    DEFAULT_TOOLS,
    build_agent_tools,
    filter_tools,
    normalize_history_for_agent,
)
from lazymind.chat.engine.agent_core import build_react_agent, drive_agent
from lazymind.chat.service.utils import (
    SensitiveFilter,
    basename_from_path,
    log_and_emit_frame,
    register_image_url,
    response_payload,
    single_event_stream_response,
    sse_line,
    validate_and_resolve_files,
)
from lazyllm.tools.fs.client import FS
from lazymind.model_config import inject_model_config, summarize_model_config_for_log
from lazyllm.tools.tool_config_inject import inject_tool_config
from lazyllm import AutoModel
from lazyllm.tools.mcp.client import MCPClient
from lazymind.config import config as _cfg

rag_sem = asyncio.Semaphore(MAX_CONCURRENCY)
sensitive_filter = SensitiveFilter(SENSITIVE_WORDS_PATH)
_CITE_MESSAGE_PATTERN = re.compile(
    r'<cite_message>([\s\S]*?)</cite_message>\s*',
    re.IGNORECASE,
)


def _normalize_cite_message_query_for_agent(query: str) -> tuple[str, str]:
    cite_messages: list[str] = []

    def collect_cite_message(match: re.Match[str]) -> str:
        cite_message = match.group(1).strip()
        if cite_message:
            cite_messages.append(cite_message)
        return ''

    user_query = _CITE_MESSAGE_PATTERN.sub(collect_cite_message, query).strip()
    if not cite_messages:
        return query, query

    if len(cite_messages) == 1:
        cite_text = cite_messages[0]
    else:
        cite_text = '\n\n'.join(
            f'{index}. {cite_message}'
            for index, cite_message in enumerate(cite_messages, start=1)
        )

    agent_query = (
        f'用户本次引用的消息：\n{cite_text}\n\n'
        f'用户本次的问题：\n{user_query}'
    ).strip()
    return user_query, agent_query


def _normalize_kb_id_filter(raw_kb_id: Any) -> str | list[str] | None:
    if isinstance(raw_kb_id, str):
        return raw_kb_id.strip() or None
    if isinstance(raw_kb_id, list):
        cleaned = [item.strip() for item in raw_kb_id if isinstance(item, str) and item.strip()]
        return cleaned[0] if len(cleaned) == 1 else (cleaned or None)
    return None


def check_sensitive_content(
    query: str,
) -> Optional[str]:
    if not sensitive_filter.loaded:
        return None
    has_sensitive, sensitive_word = sensitive_filter.check(query)
    return sensitive_word if has_sensitive else None


def _build_mcp_tools(mcp_config: List[Dict[str, Any]]) -> list:
    """Build MCP tool list from mcp_config. Skip individual servers on failure with a warning."""
    tools = []
    for server in mcp_config:
        url = server.get('url')
        if not url:
            LOG.warning(
                f"[MCP] skipped server {server.get('name')}: missing 'url' field"
            )
            continue
        try:
            client = MCPClient(
                command_or_url=url,
                headers=server.get('headers'),
                timeout=server.get('timeout', 5),
                transport=server.get('transport', 'auto'),
            )
            allowed = server.get('allowed_tools') or None
            mcp_tools = client.get_tools(allowed_tools=allowed)
            tools.extend(mcp_tools)
            LOG.info(
                f"[MCP] loaded {len(mcp_tools)} tools from {server.get('name')}"
            )
        except Exception as e:
            LOG.warning(
                f"[MCP] failed to connect {server.get('name')}: {e}"
            )
    return tools


def _build_subagent_chat_tools(has_subagents: bool) -> list:
    """Assemble ChatAgent SubAgent tools. create_subagent is always available; query tools
    are registered only when the conversation already has SubAgent tasks."""
    from lazymind.chat.engine.tools.subagent_chat_tools import (
        create_subagent,
        get_subagent_artifacts,
        get_subagent_status,
        list_subagent_artifacts,
        list_subagents,
    )
    tools = [create_subagent]
    if has_subagents:
        tools.extend([
            list_subagents,
            get_subagent_status,
            list_subagent_artifacts,
            get_subagent_artifacts,
        ])
    return tools


def _build_user_attachment_tools(has_files: bool) -> list:
    """Register find_user_attachment / read_user_attachment when the conversation has uploads."""
    if not has_files:
        return []
    from lazymind.chat.engine.subagent.tools import find_user_attachment, read_user_attachment
    return [find_user_attachment, read_user_attachment]


def _build_schedule_tools() -> list:
    """Return schedule management tools (create/list/cancel).

    These are independent of plugin and subagent flags — scheduling is a
    standalone capability available whenever the chat service is running.
    """
    from lazymind.chat.plugin.plugin_manager import build_schedule_tools
    return build_schedule_tools()


def _collect_active_tool_names(configs: list) -> set[str]:
    # Build a per-request callable allowlist from filtered tool configs.
    # This is consumed by tool_runtime guard to prevent accidental execution
    # when the model tries to call a tool that is not active in this session.
    names: set[str] = set()
    for cfg in configs:
        inst = getattr(cfg, 'instance', None)
        if inst is None:
            continue
        if callable(inst):
            tool_name = str(getattr(inst, '__name__', '')).strip()
            if tool_name:
                names.add(tool_name)
        public_apis = getattr(inst, '__public_apis__', None)
        if isinstance(public_apis, (list, tuple)):
            for method_name in public_apis:
                method = str(method_name).strip()
                if method:
                    names.add(method)
    return names


def _build_user_attachment_context(history_files_per_turn: Dict[str, List[str]],
                                   current_turn_seq: Optional[int] = None) -> str:
    """Build the '## User Uploaded Files' context section from history_files_per_turn.

    history_files_per_turn is a map of "<seq>" -> [file_paths...] where seq is a
    1-based integer string matching the conversation turn sequence number.
    Only turns with actual attachments appear as keys (empty turns are omitted by Go).

    current_turn_seq: the authoritative seq for the current request turn, provided by
    Go core. When not None it is used as the marker for 当前轮次. If the current turn
    has no files it will not appear in the map, and no 当前轮次 marker is shown.
    Falls back to max(keys) only when current_turn_seq is not provided (legacy callers).

    Returns an empty string when there are no files.
    The current turn (if it has files) is listed first with a [当前轮次] marker.
    Historical turns follow in descending seq order.
    """
    if not history_files_per_turn:
        return ''

    # Parse all keys as integers (seq); skip unparseable entries.
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
        import os as _os
        name = _os.path.basename(path)
        try:
            size_bytes = _os.path.getsize(path)
            if size_bytes < 1024:
                size_str = f'{size_bytes} B'
            elif size_bytes < 1024 * 1024:
                size_str = f'{size_bytes / 1024:.1f} KB'
            else:
                size_str = f'{size_bytes / (1024 * 1024):.1f} MB'
            return f'{name} ({size_str})'
        except OSError:
            return name

    def _dedupe_names(paths: List[str]) -> List[tuple[str, str]]:
        """Return (display_name, abs_path) pairs with intra-turn dedup."""
        seen: Dict[str, int] = {}
        result: List[tuple[str, str]] = []
        import os as _os
        for path in paths:
            base = _os.path.basename(path)
            name_no_ext, ext = _os.path.splitext(base)
            if base not in seen:
                seen[base] = 0
                display = _describe_file(path)
            else:
                seen[base] += 1
                n = seen[base]
                new_base = f'{name_no_ext}-{n}{ext}'
                try:
                    import os as _os2
                    size_bytes = _os2.path.getsize(path)
                    if size_bytes < 1024:
                        size_str = f'{size_bytes} B'
                    elif size_bytes < 1024 * 1024:
                        size_str = f'{size_bytes / 1024:.1f} KB'
                    else:
                        size_str = f'{size_bytes / (1024 * 1024):.1f} MB'
                    display = f'{new_base} ({size_str})'
                except OSError:
                    display = new_base
            result.append((display, path))
        return result

    # Determine which seq is the current turn.
    # Prefer the authoritative value from Go (current_turn_seq); fall back to max(keys)
    # only when the caller did not provide it (legacy path).
    # If current_turn_seq is provided but has no attachments this turn, no 当前轮次 marker
    # is shown — the map simply won't contain that key.
    if current_turn_seq is not None and current_turn_seq in turns:
        _cur = current_turn_seq
    elif current_turn_seq is None:
        _cur = max(turns.keys())  # legacy fallback
    else:
        _cur = None  # current turn exists but has no attachments — no marker

    lines: List[str] = ['## User Uploaded Files [queried at request time]']

    # Descending order: current turn first (if it has files), then historical turns newest-first.
    for seq in sorted(turns.keys(), reverse=True):
        pairs = _dedupe_names(turns[seq])
        file_list = ', '.join(name for name, _ in pairs)
        if seq == _cur:
            lines.append(f'- [Turn {seq} 当前轮次]: {file_list}')
        else:
            lines.append(f'- [Turn {seq}]: {file_list}')

    if _cur is None:
        lines.append('')
        lines.append('Note: the current turn has no attachments. All entries above are historical.')

    lines.append('')
    lines.append(
        'Rules: Turn numbers are 1-based integers. '
        'When the user says "this image / 这张图 / 这个文件" without specifying a turn, '
        'default to the CURRENT TURN attachment (marked 当前轮次 above). '
        'Only fall back to historical turns when the user explicitly references a past turn '
        'or when the current turn has no attachments.'
    )
    lines.append("To read a file's content, call read_user_attachment(filename, turn=N).")
    lines.append("To get a file's accessible path, call find_user_attachment(filename, turn=N).")
    lines.append('When passing an attachment path to save_plugin_artifact, always use the `path` field '
                 '(local absolute path) from find_user_attachment, NOT the `url` field.')

    return '\n'.join(lines)


async def handle_chat(query: str, history: Optional[List[Dict[str, Any]]],
                      session_id: str, filters: Optional[Dict[str, Any]],
                      files: Optional[Dict[str, List[str]]],
                      databases: Optional[List[Dict[str, Any]]],
                      priority: Optional[int], disabled_tools: Optional[List[str]],
                      available_skills: Optional[List[str]], memory: Optional[str],
                      user_preference: Optional[str], use_memory: Optional[bool],
                      environment_context: Optional[Dict[str, Any]] = None,
                      user_id: Optional[str] = None,
                      conversation_id: Optional[str] = None,
                      mode: Optional[str] = 'auto',
                      has_subagents: Optional[bool] = False,
                      model_config: Optional[Dict[str, Any]] = None,
                      tool_config: Optional[Dict[str, Union[str, List[str]]]] = None,
                      mcp_config: Optional[List[Dict[str, Any]]] = None,
                      trace: Optional[bool] = False,
                      plugin_context: Optional[Dict[str, Any]] = None,
                      local_fs_sources: Optional[List[Dict[str, Any]]] = None,
                      ask_response: Optional[Dict[str, Any]] = None,
                      current_turn_seq: Optional[int] = None,
                      enable_plugin: Optional[bool] = None,
                      enable_subagent: Optional[bool] = None,
                      ) -> Union[Dict[str, Any], StreamingResponse]:
    LOG.info(
        f'[ChatServer] [MODEL_CONFIG_RECEIVED] [sid={session_id}] [user_id={user_id or ""}] '
        f'[{summarize_model_config_for_log(model_config)}]'
    )
    LOG.info(
        f'[ChatServer] [PLUGIN_CONTEXT] [sid={session_id}] [plugin_context={plugin_context!r}]'
    )
    LOG.info(
        f'[ChatServer] [TURN_SEQ] [sid={session_id}] [current_turn_seq={current_turn_seq!r}] '
        f'[files_map_keys={sorted(files.keys()) if isinstance(files, dict) else None}]'
    )
    start_time = time.time()
    priority = priority or LAZYMIND_LLM_PRIORITY
    query, agent_query = _normalize_cite_message_query_for_agent(query)
    sensitive_word = check_sensitive_content(query)
    if sensitive_word:
        cost = round(time.time() - start_time, 3)
        LOG.warning(
            f'[ChatServer] [SENSITIVE_FILTER_BLOCKED] [query={query[:50]}...] '
            f'[sensitive_word={sensitive_word}] [session_id={session_id}]'
        )
        return single_event_stream_response(response_payload(
            200,
            'success',
            {
                'think': None,
                'text': SENSITIVE_FILTER_RESPONSE_TEXT,
                'sources': [],
            },
            cost,
        ), final_data={'tool_call_turns': 0})

    filters = dict(filters or {})
    files_map: Dict[str, List[str]] = files if isinstance(files, dict) else {}
    flat_files: List[str] = []
    if files_map:
        for seq_key in sorted((k for k in files_map if k.isdigit()), key=int):
            flat_files.extend(files_map[seq_key])
    resolved_files = validate_and_resolve_files(flat_files)
    filters['kb_id'] = _normalize_kb_id_filter(filters.get('kb_id'))
    LOG.info(f'[KBToolGroup_DEBUG] filters={filters!r} kb_id={filters.get("kb_id")!r}')

    raw_history = list(history) if isinstance(history, list) else []
    agent_history = normalize_history_for_agent(raw_history)
    translator = AgentEventFrameTranslator(query=query)

    agentic_config = {
        'session_id': session_id,
        'filters': filters if RAG_MODE and filters else {},
        'files': resolved_files,
        'history_files_per_turn': files_map,
        'local_fs_sources': local_fs_sources or [],
        'priority': priority,
        'user_id': user_id or '',
        'use_memory': use_memory,
        'citation_state': translator.citation_state,
        'mode': mode if mode in ('auto', 'manual') else 'auto',
        'has_subagents': bool(has_subagents),
        'conversation_id': (conversation_id or '').strip(),
        'query': query or '',
        'memory': memory or '',
        'user_preference': user_preference or '',
    }
    # Inject per-conversation plugin flags from Go (resolved from conversations table).
    # enable_plugin=None means "not set"; default to True so behaviour is unchanged
    # for callers that do not yet pass the field.
    if enable_plugin is not None:
        agentic_config['enable_plugin'] = bool(enable_plugin)
    if enable_subagent is not None:
        agentic_config['enable_subagent'] = bool(enable_subagent)
    # plugin_mode is consumed directly from plugin_context by resolve_plugin_injection
    # (where it is only meaningful when enable_plugin=true); no need to store it in
    # agentic_config separately.

    display_files: list[str] = []
    # Use the authoritative current_turn_seq from Go; fall back to max(keys) only as a
    # last resort (handles callers that do not yet pass the field).
    _eff_current_seq: int | None = current_turn_seq
    if _eff_current_seq is None and files_map:
        int_keys = [int(k) for k in files_map if k.isdigit() and files_map[k]]
        if int_keys:
            _eff_current_seq = max(int_keys)
    current_turn_paths: set[str] = (set(files_map.get(str(_eff_current_seq), []))
                                    if _eff_current_seq is not None else set())

    for path in resolved_files:
        if path.lower().endswith(IMAGE_EXTENSIONS):
            register_image_url(translator.citation_state, path)
            name = basename_from_path(path) or path
        else:
            name = path
        if path in current_turn_paths:
            display_files.append(f'{name} [当前轮次]')
        else:
            display_files.append(name)

    from lazymind.chat.plugin.plugin_manager import (
        resolve_plugin_injection,
        _build_chat_agent_task_context,
    )
    lazyllm.globals._init_sid(sid=session_id)
    lazyllm.locals._init_sid(sid=session_id)
    inject_model_config(model_config)
    inject_tool_config(tool_config)
    lazyllm.globals['agentic_config'] = agentic_config

    plugin_tools, plugin_system_prompt, plugin_stop_tools, agentic_config_patch, plugin_artifact_context = \
        resolve_plugin_injection(plugin_context, conversation_id=(conversation_id or '').strip(),
                                 ask_response=ask_response)
    agentic_config.update(agentic_config_patch)

    # Inject SubAgent task context into the system prompt independently of plugin state.
    # Injected when either plugin or subagent is enabled so the model knows about ongoing tasks.
    # When both are disabled, the task context is suppressed (pure QA mode).
    _enable_plugin = agentic_config.get('enable_plugin', True)
    _enable_subagent = agentic_config.get('enable_subagent', True)
    LOG.info(
        f'[ChatServer] [PLUGIN_FLAGS] [sid={session_id}] '
        f'[enable_plugin={_enable_plugin!r}] [enable_subagent={_enable_subagent!r}] '
        f'[plugin_tools={[getattr(t, "__name__", str(t)) for t in plugin_tools]!r}]'
    )
    if _enable_plugin or _enable_subagent:
        task_ctx = _build_chat_agent_task_context((conversation_id or '').strip())
        if task_ctx:
            plugin_system_prompt = (plugin_system_prompt + '\n\n' + task_ctx).strip()

    # Build user attachment context from files_map and inject before plugin context.
    user_attachment_context = _build_user_attachment_context(files_map, _eff_current_seq)

    # Prepend artifact context and user attachment context before the user query.
    parts = []
    if plugin_artifact_context:
        parts.append(plugin_artifact_context)
    if user_attachment_context:
        parts.append(user_attachment_context)
    # Inject the authoritative current-turn declaration so the model is never misled
    # by conversation history into thinking an earlier turn is the current one.
    if _eff_current_seq is not None:
        parts.append(
            f'## Current Request Context [AUTHORITATIVE]\n'
            f'This is conversation turn **{_eff_current_seq}** (the current turn).\n'
            f'Any turn number mentioned in the chat history that appears to be "current" '
            f'is outdated. Turn {_eff_current_seq} is the only present moment.\n'
            f'When the user says "this image / 这张图 / 这个文件 / 现在 / 本次", '
            f'they are referring to turn {_eff_current_seq} unless they explicitly name another turn.'
        )
    if parts:
        agent_query = '\n\n---\n\n'.join(parts) + '\n\n---\n\n## User Request\n' + agent_query

    disabled = set(disabled_tools or [])
    active_configs = filter_tools(
        [cfg for cfg in DEFAULT_TOOLS if cfg.name not in disabled],
    )
    # Persist the allowlist in session globals so every @handle_tool_errors-wrapped
    # tool can do a cheap runtime check before executing business logic.
    lazyllm.globals['active_tool_names'] = _collect_active_tool_names(active_configs)
    # Plugin tools are dynamically injected and pre-validated by resolve_plugin_injection.
    # Register ALL plugin_tools (advance_step, find_artifact, save_plugin_artifact, …)
    # into the allowlist so the ToolGuard does not block any of them.
    # plugin_stop_tools is only used by set_stop_tools below to control loop exit;
    # it is not the source of the allowlist.
    lazyllm.globals['active_tool_names'] |= {
        getattr(fn, '__name__', '') for fn in plugin_tools if callable(fn)
    }
    agent_tools = build_agent_tools(active_configs)
    # Respect enable_subagent flag: when false, suppress create_subagent and related tools.
    enable_subagent = agentic_config.get('enable_subagent', True)
    subagent_tools = _build_subagent_chat_tools(bool(has_subagents)) if enable_subagent else []
    # SubAgent chat tools (create_subagent, list_subagents, …) are always active;
    # add their names to the allowlist so the ToolGuard does not block them.
    lazyllm.globals['active_tool_names'] |= {
        getattr(fn, '__name__', '') for fn in subagent_tools if callable(fn)
    }
    mcp_tools = _build_mcp_tools(mcp_config) if mcp_config else []
    # User attachment tools are only meaningful when the user has uploaded files.
    # Register them (and add to allowlist) whenever files_map is non-empty.
    attachment_tools = _build_user_attachment_tools(bool(files_map))
    lazyllm.globals['active_tool_names'] |= {
        getattr(fn, '__name__', '') for fn in attachment_tools if callable(fn)
    }
    # Schedule tools (create_schedule / list_schedules / cancel_schedule) are independent
    # of plugin and subagent flags — always inject them.
    schedule_tools = _build_schedule_tools()
    lazyllm.globals['active_tool_names'] |= {
        getattr(fn, '__name__', '') for fn in schedule_tools if callable(fn)
    }
    all_tools = agent_tools + subagent_tools + attachment_tools + schedule_tools + plugin_tools + mcp_tools
    set_trace_context({
        'enabled': bool(trace),
        'trace_id': session_id if trace else None,
        'session_id': session_id,
        'sampled': True,
        'module_trace': {'default': True},
        'request_tags': ['handle_chat'],
    })
    runtime_prompt = build_system_prompt(
        {cfg.name for cfg in active_configs},
        environment_context=environment_context,
        use_memory=use_memory,
        user_preference=user_preference,
        memory=memory,
        files=display_files,
    )
    if plugin_system_prompt:
        runtime_prompt = runtime_prompt + '\n\n' + plugin_system_prompt

    llm = AutoModel(model='llm')

    react_agent = build_react_agent(
        llm=llm,
        tools=all_tools,
        force_summarize_context=query,
        prompt=runtime_prompt,
        skills=available_skills,
        workspace=_cfg['agentic_workspace'],
        keep_full_turns=_cfg['agentic_keep_full_turns'],
        fs=FS,
        skills_dir=_cfg['skill_fs_url'],
    )
    if plugin_stop_tools:
        react_agent.set_stop_tools(plugin_stop_tools)

    async def event_stream() -> Any:
        final_result: Any = None

        try:
            async with rag_sem:
                async for kind, payload in drive_agent(react_agent, agent_query, history=agent_history):
                    if kind == 'event':
                        for frame in translator.feed(payload):
                            cost = round(time.time() - start_time, 3)
                            yield log_and_emit_frame(frame, cost, query, session_id, tag='FEED')
                    else:
                        # 'final' -- payload is already the resolved result value;
                        # if future.result() raised, drive_agent propagated it before yielding.
                        final_result = payload

            for frame in translator.finish(final_result):
                cost = round(time.time() - start_time, 3)
                yield log_and_emit_frame(frame, cost, query, session_id, tag='FINISH')

        except Exception as exc:
            LOG.exception('[ChatServer] agent failed')
            final_resp = response_payload(
                500,
                f'chat service failed: {exc}',
                {'status': 'FAILED', 'tool_call_turns': translator.tool_call_turns},
                0.0,
            )
        else:
            final_resp = response_payload(
                200,
                'success',
                {'status': 'FINISHED', 'tool_call_turns': translator.tool_call_turns},
                0.0,
            )

        cost = round(time.time() - start_time, 3)
        final_resp['cost'] = cost
        yield sse_line(final_resp)

        databases_str = json.dumps(databases, ensure_ascii=False) if databases else []
        LOG.info(
            f'[ChatServer] [KB_CHAT_STREAM_FINISH] [query={query}] [session_id={session_id}] '
            f'[filters={filters}] [files={resolved_files}] '
            f'[databases={databases_str}] [cost={cost}] [response=None]'
        )

    return StreamingResponse(
        event_stream(), media_type='text/event-stream'
    )

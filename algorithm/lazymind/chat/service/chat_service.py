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
from lazymind.chat.engine.prompts import add_standard_system_sections
from lazymind.chat.service.chat_request import ChatRequest
from lazymind.chat.service.component import (
    AgentEventFrameTranslator,
    ASK_USER_TOOL_CONFIG,
    DEFAULT_TOOLS,
    USER_ATTACHMENT_TOOL_CONFIGS,
    collect_system_prompt_appendices,
    filter_tools,
    normalize_history_for_agent,
)
from lazymind.chat.engine.agent_runtime import (
    AgentExecutionOptions,
    AgentExecutor,
    AgentRole,
    AgentRunPlan,
    PromptBuilder,
    normalize_attachments,
    estimate_context_usage,
    render_context_markdown,
    report_to_dict,
    render_attachment_content,
)
from lazymind.chat.engine.tools.chat_artifact import chat_agent_workspace
from lazymind.chat.engine.tools.intent_writer import (
    build_intentwrite_tool,
    render_intent_section,
)
from lazymind.chat.service.utils import (
    SensitiveFilter,
    log_and_emit_frame,
    register_image_url,
    response_payload,
    single_event_stream_response,
    sse_line,
    validate_and_resolve_files,
)
from lazyllm.tools.fs.client import FS
from lazymind.model_config import inject_model_config, summarize_model_config_for_log
from lazyllm.tools.rag import inject_reader_config
from lazyllm.tools.tool_config_inject import inject_tool_config
from lazyllm import AutoModel
from lazyllm.tools.mcp.client import MCPClient
from lazymind.config import config as _cfg

rag_sem = asyncio.Semaphore(MAX_CONCURRENCY)
sensitive_filter = SensitiveFilter(SENSITIVE_WORDS_PATH)

# Maps conversation_id → session_id for active chat sessions.
# Used by task-cancel endpoint to cancel ChatAgent by conversation_id.
_active_sessions: dict[str, str] = {}
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
        return query, ''

    if len(cite_messages) == 1:
        cite_text = cite_messages[0]
    else:
        cite_text = '\n\n'.join(
            f'{index}. {cite_message}'
            for index, cite_message in enumerate(cite_messages, start=1)
        )

    return user_query, cite_text


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


def _build_subagent_chat_tools() -> list:
    """Return all ChatAgent SubAgent tools as directly registered callables."""
    from lazymind.chat.engine.tools.subagent_chat_tools import (
        create_subagent,
        get_subagent_artifacts,
        get_subagent_status,
        list_subagent_artifacts,
        list_subagents,
    )
    return [
        create_subagent, list_subagents, get_subagent_status,
        list_subagent_artifacts, get_subagent_artifacts,
    ]


def _build_chat_artifact_tools() -> list:
    """Tools for artifacts produced directly by the main ChatAgent."""
    from lazymind.chat.engine.tools.chat_artifact import save_chat_artifact
    return [save_chat_artifact]


def _build_user_attachment_tools(has_files: bool) -> list:
    """Register find_user_attachment / read_user_attachment when the conversation has uploads."""
    if not has_files:
        return []
    from lazymind.chat.engine.subagent.tools import find_user_attachment, read_user_attachment
    return [find_user_attachment, read_user_attachment]


def _build_ask_user_tool() -> list:
    """Return the ask_user stop-tool for ChatAgent.

    Intentionally NOT added to DEFAULT_TOOLS so SubAgents never receive it.
    SubAgent tool resolution falls back to DEFAULT_TOOLS; ask_user is only
    injected here, into the ChatAgent's all_tools list.
    """
    from lazymind.chat.engine.tools.ask_user import ask_user
    return [ask_user]


def _should_register_ask_user(agentic_config: Dict[str, Any]) -> bool:
    """Auto plugin sessions are mechanically non-interactive."""
    return not (
        agentic_config.get('enable_plugin', True)
        and agentic_config.get('plugin_mode') == 'auto'
    )


async def handle_chat(request: ChatRequest) -> Union[Dict[str, Any], StreamingResponse]:
    message = request.message
    conversation = request.conversation
    retrieval = request.retrieval
    runtime = request.runtime
    personalization = request.personalization
    agent = request.agent
    plugin = request.plugin
    from lazymind.chat.plugin.plugin_manager import (
        _build_chat_agent_task_context,
        guard_plugin_agent_stream,
        is_plugin_driver_turn,
        resolve_plugin_injection,
        update_intentwriter,
    )

    conversation_id = (conversation.conversation_id or '').strip()
    user_id = (conversation.user_id or '').strip()
    LOG.info(
        f'[ChatServer] [MODEL_CONFIG_RECEIVED] [sid={conversation.session_id}] [user_id={user_id or ""}] '
        f'[{summarize_model_config_for_log(runtime.llm_config)}]'
    )
    LOG.info(
        f'[ChatServer] [PLUGIN_CONTEXT] [sid={conversation.session_id}] [plugin_context={plugin.plugin_context!r}]'
    )
    LOG.info(
        f'[ChatServer] [TURN_SEQ] [sid={conversation.session_id}] '
        f'[current_turn_seq={message.current_turn_seq!r}] '
        f'[files_map_keys={sorted(message.files.keys()) if isinstance(message.files, dict) else None}]'
    )
    start_time = time.time()
    priority = runtime.priority or LAZYMIND_LLM_PRIORITY
    query, cited_message_context = _normalize_cite_message_query_for_agent(message.query)
    user_input, user_cited_context = _normalize_cite_message_query_for_agent(
        message.user_query or query,
    )
    if user_cited_context:
        cited_message_context = user_cited_context
    language_query = user_input.strip()
    is_driver_turn = is_plugin_driver_turn(plugin.plugin_context)
    sensitive_word = (
        None if is_driver_turn or runtime.context_usage_preview or runtime.context_prompt_export
        else check_sensitive_content(query)
    )
    if sensitive_word:
        cost = round(time.time() - start_time, 3)
        LOG.warning(
            f'[ChatServer] [SENSITIVE_FILTER_BLOCKED] [query={query[:50]}...] '
            f'[sensitive_word={sensitive_word}] [session_id={conversation.session_id}]'
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
    filters = dict(retrieval.filters or {})
    files_map: Dict[str, List[str]] = message.files if isinstance(message.files, dict) else {}
    flat_files: List[str] = []
    if files_map:
        for seq_key in sorted((k for k in files_map if k.isdigit()), key=int):
            flat_files.extend(files_map[seq_key])
    resolved_files = validate_and_resolve_files(flat_files)
    filters['kb_id'] = _normalize_kb_id_filter(filters.get('kb_id'))

    raw_history = list(message.history) if isinstance(message.history, list) else []
    agent_history = normalize_history_for_agent(raw_history)
    translator = AgentEventFrameTranslator(query=query)

    agentic_config = {
        'session_id': conversation.session_id,
        'filters': filters if RAG_MODE and filters else {},
        'files': resolved_files,
        'history_files_per_turn': files_map,
        'databases': retrieval.databases or [],
        'dataset': retrieval.dataset,
        'local_fs_sources': retrieval.local_fs_sources or [],
        'priority': priority,
        'llm_config': runtime.llm_config or {},
        'tool_config': runtime.tool_config or {},
        'ocr_config': runtime.ocr_config or {},
        'mcp_config': runtime.mcp_config or [],
        'environment_context': runtime.environment_context or {},
        'user_id': user_id or '',
        'use_memory': personalization.use_memory,
        'citation_state': translator.citation_state,
        'mode': conversation.mode if conversation.mode in ('auto', 'manual') else 'auto',
        'has_subagents': bool(agent.has_subagents),
        'conversation_id': conversation_id,
        'query': query or '',
        'memory': personalization.memory or '',
        'user_preference': personalization.user_preference or '',
    }
    # Inject per-conversation plugin flags from Go (resolved from conversations table).
    # enable_plugin=None means "not set"; default to True so behaviour is unchanged
    # for callers that do not yet pass the field.
    if plugin.enable_plugin is not None:
        agentic_config['enable_plugin'] = bool(plugin.enable_plugin)
    if agent.enable_subagent is not None:
        agentic_config['enable_subagent'] = bool(agent.enable_subagent)
    # plugin_mode is consumed directly from plugin_context by resolve_plugin_injection
    # (where it is only meaningful when enable_plugin=true); no need to store it in
    # agentic_config separately.

    # Use the authoritative current_turn_seq from Go; fall back to max(keys) only as a
    # last resort (handles callers that do not yet pass the field).
    _eff_current_seq: int | None = message.current_turn_seq
    if _eff_current_seq is None and files_map:
        int_keys = [int(k) for k in files_map if k.isdigit() and files_map[k]]
        if int_keys:
            _eff_current_seq = max(int_keys)
    for path in resolved_files:
        if path.lower().endswith(IMAGE_EXTENSIONS):
            register_image_url(translator.citation_state, path)

    # Register the active session so the cancel endpoint can find it by conversation_id.
    _conv_id_key = conversation_id  # already stripped above
    if _conv_id_key and not runtime.context_usage_preview and not runtime.context_prompt_export:
        _active_sessions[_conv_id_key] = conversation.session_id
    lazyllm.globals._init_sid(sid=conversation.session_id)
    lazyllm.locals._init_sid(sid=conversation.session_id)
    inject_model_config(runtime.llm_config)
    inject_tool_config(runtime.tool_config)
    inject_reader_config(ocr_config=runtime.ocr_config)
    lazyllm.globals['agentic_config'] = agentic_config

    plugin_contribution = resolve_plugin_injection(
        plugin.plugin_context,
        conversation_id=conversation_id,
        plugin_catalog=plugin.catalog,
        disabled_builtin_plugins=plugin.disabled_builtin_plugins,
        allowed_plugin_refs=plugin.allowed_plugin_refs,
    )
    plugin_tools = plugin_contribution.tools
    agentic_config.update(plugin_contribution.agentic_config_patch)

    intentwriter = build_intentwrite_tool(
        conversation_id=conversation_id,
        current_query=query,
        current_intent=conversation.intent_context,
    )
    intentwriter = update_intentwriter(intentwriter, plugin.plugin_context)

    # Inject SubAgent task context into the system prompt independently of plugin state.
    # Injected when either plugin or subagent is enabled so the model knows about ongoing tasks.
    # When both are disabled, the task context is suppressed (pure QA mode).
    _enable_plugin = agentic_config.get('enable_plugin', True)
    _enable_subagent = agentic_config.get('enable_subagent', True)
    LOG.info(
        f'[ChatServer] [PLUGIN_FLAGS] [sid={conversation.session_id}] '
        f'[enable_plugin={_enable_plugin!r}] [enable_subagent={_enable_subagent!r}] '
        f'[plugin_tools={[getattr(t, "__name__", str(t)) for t in plugin_tools]!r}]'
    )
    task_ctx = ''
    if _enable_plugin or _enable_subagent:
        task_ctx = _build_chat_agent_task_context((conversation_id or '').strip())
    conversation_intent_section = render_intent_section(
        'Conversation Intent', conversation.intent_context,
    )
    attachment_content = render_attachment_content(
        normalize_attachments(files_map, _eff_current_seq),
        role=AgentRole.CHAT,
        current_turn_seq=_eff_current_seq,
    )

    disabled = set(agent.disabled_tools or [])
    active_configs = filter_tools(
        [cfg for cfg in DEFAULT_TOOLS if cfg.name not in disabled],
    )
    agent_tools = [cfg.tool for cfg in active_configs]
    # Respect enable_subagent flag: when false, suppress create_subagent and related tools.
    enable_subagent = agentic_config.get('enable_subagent', True)
    subagent_tools = _build_subagent_chat_tools() if enable_subagent else []
    mcp_tools = _build_mcp_tools(runtime.mcp_config) if runtime.mcp_config else []
    # User attachment tools are only meaningful when the user has uploaded files.
    attachment_tools = _build_user_attachment_tools(bool(files_map))
    attachment_configs = list(USER_ATTACHMENT_TOOL_CONFIGS) if attachment_tools else []
    # ask_user is a ChatAgent-only stop-tool. It is NOT in DEFAULT_TOOLS so SubAgents
    # (whose tool resolution falls back to DEFAULT_TOOLS) never see it.
    # Auto plugin mode is non-interactive by contract: ask_user must be absent,
    # not merely discouraged by prompt text.
    allow_ask_user = _should_register_ask_user(agentic_config)
    ask_user_tools = _build_ask_user_tool() if allow_ask_user else []
    ask_user_configs = [ASK_USER_TOOL_CONFIG] if ask_user_tools else []
    artifact_tools = _build_chat_artifact_tools()
    all_tools = ([intentwriter] + agent_tools + artifact_tools + subagent_tools + attachment_tools
                 + ask_user_tools + plugin_tools + mcp_tools)
    set_trace_context({
        'enabled': bool(runtime.trace),
        'trace_id': conversation.session_id if runtime.trace else None,
        'session_id': conversation.session_id,
        'sampled': True,
        'module_trace': {'default': True},
        'request_tags': ['handle_chat'],
    })
    prompt_builder = PromptBuilder.for_role(AgentRole.CHAT)
    add_standard_system_sections(
        prompt_builder,
        bool(all_tools),
        environment_context=runtime.environment_context,
        use_memory=personalization.use_memory,
        user_preference=personalization.user_preference,
        memory=personalization.memory,
        current_query=language_query,
        conversation_history=agent_history,
        tool_prompt_appendices=collect_system_prompt_appendices(
            active_configs + attachment_configs + ask_user_configs,
        ),
    )
    # Plugin policy historically followed the common system prompt.
    prompt_builder.system(
        'chat_plugin_policy', 'Plugin Policy', plugin_contribution.system_prompt,
        'plugin.scenario', priority=80,
    )
    prompt_builder.runtime(
        'chat_plugin_runtime', 'Plugin State', plugin_contribution.runtime_context,
        'plugin.runtime', priority=10, authoritative=True, content_kind='state',
    )
    prompt_builder.runtime(
        'chat_tasks', 'SubAgent Tasks', task_ctx, 'database.tasks',
        priority=20, authoritative=True, content_kind='state',
    )
    prompt_builder.runtime(
        'chat_intent', 'Conversation Intent', conversation_intent_section,
        'database.intent', priority=30, content_kind='instruction',
    )
    prompt_builder.runtime(
        'chat_quoted_message', 'Quoted Message', cited_message_context,
        'user.quote', priority=40, content_kind='reference',
    )
    prompt_builder.runtime(
        'chat_resource_context', 'Mentioned Resource Context', query,
        'backend.resources', priority=45, content_kind='reference',
        skip_if=lambda: query.strip() == language_query,
    )
    prompt_builder.runtime(
        'chat_attachments', 'Attachments', attachment_content,
        'request.attachments', priority=50, authoritative=True,
        content_kind='reference',
    )
    prompt_builder.runtime(
        'chat_current_turn', 'Current Turn', (
            f'This is conversation turn {_eff_current_seq}. Any turn described as current '
            f'in chat history is outdated; Turn {_eff_current_seq} is the present request. '
            f'Unless another turn is explicitly named, "现在 / 本次" refers to '
            f'Turn {_eff_current_seq}.'
        ),
        'backend.turn', priority=60, authoritative=True, content_kind='state',
        skip_if=lambda: _eff_current_seq is None,
    )
    prompt_bundle = prompt_builder.input(
        content=language_query,
        source='user',
    ).build()

    llm = AutoModel(model='llm')

    # ask_user is always a stop-tool for ChatAgent regardless of plugin state.
    stop_tools = list(plugin_contribution.stop_tools)
    if allow_ask_user and 'ask_user' not in stop_tools:
        stop_tools.append('ask_user')

    plan = AgentRunPlan(
        role=AgentRole.CHAT,
        prompt=prompt_bundle,
        history=agent_history,
        tools=all_tools,
        stop_tools=stop_tools,
        force_summarize_context=query,
        execution_options=AgentExecutionOptions(
            skills=agent.available_skills,
            workspace=chat_agent_workspace(user_id or '0', conversation_id),
            keep_full_turns=_cfg['agentic_keep_full_turns'],
            fs=FS,
            skills_dir=_cfg['skill_fs_url'],
        ),
    )
    executor = AgentExecutor()
    react_agent = executor.create_agent(llm, plan)
    if runtime.context_usage_preview or runtime.context_prompt_export:
        agent_context = await asyncio.to_thread(react_agent.describe_context, agent_history)
        if runtime.context_prompt_export:
            return {'prompt_markdown': render_context_markdown(plan, agent_context)}
        report = await estimate_context_usage(plan, agent_context)
        return report_to_dict(report)

    async def event_stream() -> Any:
        final_result: Any = None

        try:
            async with rag_sem:
                initial_agent_stream = executor.stream_agent(react_agent, plan)
                guarded_agent_stream = guard_plugin_agent_stream(
                    initial_agent_stream,
                    all_tools=all_tools,
                    query=query,
                    runtime_prompt=prompt_bundle.system_prompt,
                    agent=agent,
                    runtime_config=_cfg,
                    fs=FS,
                    stop_tools=stop_tools,
                    history=agent_history,
                )
                async for kind, payload in guarded_agent_stream:
                    if kind == 'event':
                        for frame in translator.feed(payload):
                            cost = round(time.time() - start_time, 3)
                            yield log_and_emit_frame(frame, cost, query, conversation.session_id, tag='FEED')
                    else:
                        # 'final' -- payload is already the resolved result value;
                        # AgentExecutor propagates future exceptions before yielding final.
                        final_result = payload

            for frame in translator.finish(final_result):
                cost = round(time.time() - start_time, 3)
                yield log_and_emit_frame(frame, cost, query, conversation.session_id, tag='FINISH')

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
        finally:
            # Unregister the active session so the cancel endpoint no longer targets it.
            if _conv_id_key:
                _active_sessions.pop(_conv_id_key, None)

        cost = round(time.time() - start_time, 3)
        final_resp['cost'] = cost
        yield sse_line(final_resp)

        databases_str = json.dumps(retrieval.databases, ensure_ascii=False) if retrieval.databases else []
        LOG.info(
            f'[ChatServer] [KB_CHAT_STREAM_FINISH] [query={query}] [session_id={conversation.session_id}] '
            f'[filters={filters}] [files={resolved_files}] '
            f'[databases={databases_str}] [cost={cost}] [response=None]'
        )

    return StreamingResponse(
        event_stream(), media_type='text/event-stream'
    )

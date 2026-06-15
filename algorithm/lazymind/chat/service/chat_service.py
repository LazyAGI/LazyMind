from __future__ import annotations
import asyncio
import json
import time
from typing import Any, Dict, List, Optional, Union
import lazyllm
from lazyllm import LOG, set_trace_context
from fastapi.responses import StreamingResponse
from lazymind.chat.config import (
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
    normalize_history_for_agent,
)
from lazymind.chat.engine.agent_core import build_react_agent, drive_agent
from lazymind.chat.service.utils import (
    SensitiveFilter,
    log_and_emit_frame,
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


async def handle_chat(query: str, history: Optional[List[Dict[str, Any]]],
                      session_id: str, filters: Optional[Dict[str, Any]],
                      files: Optional[List[str]],
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
                      ) -> Union[Dict[str, Any], StreamingResponse]:
    LOG.info(
        f'[ChatServer] [MODEL_CONFIG_RECEIVED] [sid={session_id}] [user_id={user_id or ""}] '
        f'[{summarize_model_config_for_log(model_config)}]'
    )
    start_time = time.time()
    priority = priority or LAZYMIND_LLM_PRIORITY
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
    resolved_files = validate_and_resolve_files(files)
    filters['kb_id'] = _normalize_kb_id_filter(filters.get('kb_id'))
    resolved_use_memory = use_memory is not False

    raw_history = list(history) if isinstance(history, list) else []
    agent_history = normalize_history_for_agent(raw_history)
    translator = AgentEventFrameTranslator(query=query)

    agentic_config = {
        'session_id': session_id,
        'filters': filters if RAG_MODE and filters else {},
        'files': resolved_files,
        'priority': priority,
        'user_id': user_id or '',
        'use_memory': resolved_use_memory,
        'citation_state': translator.citation_state,
        'mode': mode if mode in ('auto', 'manual') else 'auto',
        'has_subagents': bool(has_subagents),
        'conversation_id': (conversation_id or '').strip(),
    }
    lazyllm.globals._init_sid(sid=session_id)
    lazyllm.locals._init_sid(sid=session_id)
    inject_model_config(model_config)
    inject_tool_config(tool_config)
    lazyllm.globals['agentic_config'] = agentic_config
    disabled = set(disabled_tools or [])
    active_configs = [cfg for cfg in DEFAULT_TOOLS if cfg.name not in disabled]
    agent_tools = build_agent_tools(active_configs)
    subagent_tools = _build_subagent_chat_tools(bool(has_subagents))
    mcp_tools = _build_mcp_tools(mcp_config) if mcp_config else []
    all_tools = agent_tools + subagent_tools + mcp_tools
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
        use_memory=resolved_use_memory,
        user_preference=user_preference,
        memory=memory,
        files=resolved_files,
    )

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

    async def event_stream() -> Any:
        final_result: Any = None

        try:
            async with rag_sem:
                async for kind, payload in drive_agent(react_agent, query, history=agent_history):
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

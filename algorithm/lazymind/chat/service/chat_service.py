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
    filter_tools,
    normalize_history_for_agent,
)
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
from lazymind.config import config as _cfg
from lazymind.chat.plugins import PluginMiddleware as _PluginMiddleware
from lazymind.chat.plugins.loader import plugin_loader as _plugin_loader


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


async def handle_chat(query: str, history: Optional[List[Dict[str, Any]]],
                      session_id: str, filters: Optional[Dict[str, Any]],
                      files: Optional[List[str]],
                      databases: Optional[List[Dict[str, Any]]],
                      priority: Optional[int], disabled_tools: Optional[List[str]],
                      available_skills: Optional[List[str]], memory: Optional[str],
                      user_preference: Optional[str], use_memory: Optional[bool],
                      environment_context: Optional[Dict[str, Any]] = None,
                      user_id: Optional[str] = None,
                      model_config: Optional[Dict[str, Any]] = None,
                      tool_config: Optional[Dict[str, Union[str, List[str]]]] = None,
                      trace: Optional[bool] = False,
                      plugin_context: Optional[Dict[str, Any]] = None,
                      ) -> Union[Dict[str, Any], StreamingResponse]:
    LOG.info(
        f'[ChatServer] [MODEL_CONFIG_RECEIVED] [sid={session_id}] [user_id={user_id or ""}] '
        f'[{summarize_model_config_for_log(model_config)}]'
    )
    start_time = time.time()
    priority = priority or LAZYMIND_LLM_PRIORITY

    # Skip sensitive-word filtering for plugin loop internal turns.
    # When plugin_context is present the query is a DriverAgent judgment (system-generated),
    # not user input, so it must not be blocked by the content filter.
    is_plugin_turn = bool(plugin_context and plugin_context.get('plugin_session_id'))
    sensitive_word = None if is_plugin_turn else check_sensitive_content(query)
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
    }
    lazyllm.globals._init_sid(sid=session_id)
    lazyllm.locals._init_sid(sid=session_id)
    inject_model_config(model_config)
    inject_tool_config(tool_config)

    lazyllm.globals['agentic_config'] = agentic_config

    plugin_mw = await _PluginMiddleware.create(plugin_context, agentic_config)

    all_default_configs = filter_tools(DEFAULT_TOOLS, disabled_tools)
    lazyllm.globals['default_tools_for_step_agent'] = [c.instance for c in all_default_configs]
    agent_tools = [cfg.instance for cfg in all_default_configs] + plugin_mw.extra_tools

    set_trace_context({
        'enabled': bool(trace),
        'trace_id': session_id if trace else None,
        'session_id': session_id,
        'sampled': True,
        'module_trace': {'default': True},
        'request_tags': ['handle_chat'],
    })
    runtime_prompt = build_system_prompt(
        {cfg.name for cfg in all_default_configs},
        environment_context=environment_context,
        plugin_prompt=plugin_mw.plugin_prompt,
        has_plugin_tools=plugin_mw.has_plugin_tools,
        use_memory=resolved_use_memory,
        user_preference=user_preference,
        memory=memory,
        files=resolved_files,
    )

    llm = AutoModel(model='llm')

    # When inside a plugin session (advance_step turn), disable force_summarize so
    # the agent MUST call advance_step and cannot short-circuit with plain text.
    in_plugin_session = bool(
        plugin_context and plugin_context.get('plugin_session_id')
    )

    react_agent = lazyllm.tools.agent.ReactAgent(
        llm=llm,
        tools=agent_tools,
        max_retries=_cfg['max_retries'],
        stream=True,
        prompt=runtime_prompt,
        skills=available_skills,
        workspace=_cfg['agentic_workspace'],
        keep_full_turns=_cfg['agentic_keep_full_turns'],
        fs=FS,
        skills_dir=_cfg['skill_fs_url'],
        enable_builtin_tools=False,
        force_summarize=not in_plugin_session,
        force_summarize_context=query,
    )
    # Declare plugin-related tools as terminal so the ReAct loop stops immediately
    # after triggering a step — both cold-start launchers and the in-session advance_step.
    stop_tool_names = [f'trigger_{pid}' for pid in _plugin_loader.list_plugin_ids()]
    stop_tool_names.append('advance_step')
    react_agent.set_stop_tools(stop_tool_names)

    async def event_stream() -> Any:
        final_result: Any = None

        try:
            async with rag_sem:
                # Flush any plugin events queued before the agent runs.
                async for ev in plugin_mw.iter_pending_events():
                    yield sse_line({'type': 'plugin_event', 'data': ev})

                helper = lazyllm.module.stream_helper.StreamCallHelper(react_agent, init_sid=False)
                async for item in helper.astream(query, llm_chat_history=agent_history):
                    for frame in translator.feed(item):
                        cost = round(time.time() - start_time, 3)
                        yield log_and_emit_frame(frame, cost, query, session_id, tag='FEED')

                try:
                    result = helper.future.result()
                except Exception as exc:
                    LOG.exception('[ChatServer] agent failed')
                    # Flush any plugin events queued by tools before the crash.
                    # Ensures mount/step_trigger reach Go even when agent errors out
                    # (e.g. malformed second tool call with empty name).
                    flushed_events: list = []
                    async for ev in plugin_mw.iter_pending_events():
                        flushed_events.append(ev)
                        yield sse_line({'type': 'plugin_event', 'data': ev})
                    # If a plugin was successfully launched (mount event present), suppress
                    # the agent error — Go will take over the plugin execution loop.
                    if any(ev.get('type') == 'mount' for ev in flushed_events):
                        LOG.warning(
                            '[ChatServer] agent error suppressed after plugin mount: %s', exc
                        )
                        return  # Go takes over; no further SSE frames needed from Python
                    raise RuntimeError(f'agent failed: {exc}') from exc

                final_result = result

                # Flush plugin events emitted by trigger tools (e.g. mount, step_trigger).
                flushed_plugin_events = []
                async for ev in plugin_mw.iter_pending_events():
                    flushed_plugin_events.append(ev)
                    yield sse_line({'type': 'plugin_event', 'data': ev})

                # Fallback: if we are inside a plugin session and the LLM skipped
                # calling advance_step (e.g. reasoning/thinking mode produced plain text),
                # synthesize a step_trigger so Go can continue the plugin loop.
                if in_plugin_session and not any(
                    ev.get('type') == 'step_trigger' for ev in flushed_plugin_events
                ):
                    pctx_data = plugin_context or {}
                    p_id = agentic_config.get('plugin_id', '') or pctx_data.get('plugin_id', '')
                    p_session = agentic_config.get('plugin_session_id', '') or pctx_data.get('plugin_session_id', '')
                    p_step = agentic_config.get('plugin_step', '') or pctx_data.get('current_step_id', '')
                    if p_id and p_session and p_step:
                        # Try to advance to the next reachable step instead of repeating
                        # the current one.  This handles the case where the LLM produced a
                        # plain-text "advance_step(...)" call rather than an actual tool call.
                        from lazymind.chat.plugins.loader import plugin_loader
                        next_step = None  # None means "no reachable step found"
                        if plugin_loader.is_loaded(p_id):
                            sm = plugin_loader.get_state_machine(p_id)
                            if sm is not None:
                                reachable = sm.get_reachable_steps(p_step)
                                if reachable:
                                    next_step = reachable[0]
                        if next_step is None:
                            # Current step is the terminal step — no fallback needed.
                            # Go's plugin loop will exit when streamChatTurn returns nil.
                            LOG.warning(
                                '[ChatServer] plugin turn produced no step_trigger and '
                                'step=%r has no reachable successors; plugin session will end.',
                                p_step,
                            )
                        else:
                            LOG.warning(
                                '[ChatServer] plugin turn produced no step_trigger '
                                '(LLM likely used reasoning mode); synthesizing fallback '
                                'step_trigger for step=%r → next=%r session=%r',
                                p_step, next_step, p_session,
                            )
                            # Use the LLM's plain-text output as user_input if available,
                            # otherwise fall back to the original user query.
                            fallback_user_input = (
                                str(final_result).strip() if final_result else query
                            ) or query
                            fallback_trigger = {
                                'type': 'step_trigger',
                                'plugin_id': p_id,
                                'plugin_session_id': p_session,
                                'step_id': next_step,
                                'step_mode': 'auto',
                                'user_input': fallback_user_input,
                                'inputs': [],
                                'reachable_step_count': 1,
                            }
                            yield sse_line({'type': 'plugin_event', 'data': fallback_trigger})

            for frame in translator.finish(final_result):
                cost = round(time.time() - start_time, 3)
                yield log_and_emit_frame(frame, cost, query, session_id, tag='FINISH')

        except Exception as exc:
            LOG.exception(exc)
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

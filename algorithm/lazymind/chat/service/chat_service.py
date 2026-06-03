from __future__ import annotations
import asyncio
import json
import time
from typing import Any, Dict, List, Optional, Union
import lazyllm
from lazyllm import LOG
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
                      files: Optional[List[str]], debug: Optional[bool], reasoning: Optional[bool],
                      databases: Optional[List[Dict[str, Any]]], dataset: Optional[str],
                      priority: Optional[int], available_tools: Optional[List[str]],
                      available_skills: Optional[List[str]], memory: Optional[str],
                      user_preference: Optional[str], use_memory: Optional[bool],
                      trace: bool = False,
                      environment_context: Optional[Dict[str, Any]] = None,
                      user_id: Optional[str] = None,
                      model_config: Optional[Dict[str, Any]] = None,
                      tool_config: Optional[Dict[str, str]] = None) -> Union[Dict[str, Any], StreamingResponse]:
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
        ))

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
    active_configs = filter_tools(DEFAULT_TOOLS, available_tools)
    runtime_prompt = build_system_prompt(
        {cfg.name for cfg in active_configs},
        environment_context=environment_context,
        use_memory=resolved_use_memory,
        user_preference=user_preference,
        memory=memory,
        files=resolved_files,
    )

    llm = AutoModel(model='llm')

    react_agent = lazyllm.tools.agent.ReactAgent(
        llm=llm,
        tools=[cfg.instance for cfg in active_configs],
        max_retries=_cfg['max_retries'],
        stream=True,
        prompt=runtime_prompt,
        skills=available_skills,
        workspace=_cfg['agentic_workspace'],
        keep_full_turns=_cfg['agentic_keep_full_turns'],
        fs=FS,
        skills_dir=_cfg['skill_fs_url'],
        enable_builtin_tools=False,
        force_summarize=True,
        force_summarize_context=query,
    )

    async def event_stream() -> Any:
        final_result: Any = None

        try:
            async with rag_sem:
                helper = lazyllm.module.stream_helper.StreamCallHelper(react_agent, init_sid=False)
                async for item in helper.astream(query, llm_chat_history=agent_history):
                    for frame in translator.feed(item):
                        cost = round(time.time() - start_time, 3)
                        yield log_and_emit_frame(frame, cost, query, session_id, tag='FEED')

                try:
                    result = helper.future.result()
                except Exception as exc:
                    LOG.exception('[ChatServer] agent failed')
                    raise RuntimeError(f'agent failed: {exc}') from exc

                final_result = result

            for frame in translator.finish(final_result):
                cost = round(time.time() - start_time, 3)
                yield log_and_emit_frame(frame, cost, query, session_id, tag='FINISH')

        except Exception as exc:
            LOG.exception(exc)
            final_resp = response_payload(
                500, f'chat service failed: {exc}', {'status': 'FAILED'}, 0.0
            )
        else:
            final_resp = response_payload(200, 'success', {'status': 'FINISHED'}, 0.0)

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

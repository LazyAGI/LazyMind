from __future__ import annotations
import asyncio
import json
import time
from typing import Any, Dict, List, Optional, Union
import lazyllm
from lazyllm import LOG
import lazyllm.tracing.collect.configs  # noqa: F401
from lazyllm.tracing import enable_trace, get_trace_context, set_trace_context
from lazyllm.tracing.collect import runtime as tracing_runtime
from fastapi.responses import StreamingResponse
from chat.components.process.sensitive_filter import SensitiveFilter
from chat.config import (RAG_MODE, MULTIMODAL_MODE, MAX_CONCURRENCY,
                         LAZYMIND_LLM_PRIORITY, SENSITIVE_FILTER_RESPONSE_TEXT,
                         SENSITIVE_WORDS_PATH)
from chat.pipelines.agentic import agentic_rag
from chat.utils.helpers import validate_and_resolve_files
from chat.utils.load_config import get_config_path, inject_model_config, summarize_model_config_for_log
from chat.utils.markdown_images import rewrite_markdown_image_urls


rag_sem = asyncio.Semaphore(MAX_CONCURRENCY)
sensitive_filter = SensitiveFilter(SENSITIVE_WORDS_PATH)


def _run_ppl_with_trace(ppl, ppl_args, *, session_id, dataset, mode_tag, trace_enabled, model_config):
    lazyllm.globals._init_sid(sid=session_id)
    lazyllm.locals._init_sid(sid=session_id)
    inject_model_config(model_config)
    _set_request_trace(False, session_id=session_id, dataset=dataset, mode_tag=mode_tag)
    if not trace_enabled:
        return ppl(*ppl_args), None

    captured: Dict[str, Any] = {}

    def run_chat_pipeline(*args, **kwargs):
        out = ppl(*args, **kwargs)
        captured['trace_id'] = get_trace_context().trace_id
        return out

    result = enable_trace(
        run_chat_pipeline, *ppl_args,
        session_id=session_id,
        request_tags=[f'dataset:{dataset}', f'mode:{mode_tag}'],
        module_trace={'default': True},
    )
    _flush_trace_exporter()
    trace_id = captured.get('trace_id')
    if not trace_id:
        raise RuntimeError('LazyLLM trace did not expose a trace_id')
    return result, trace_id


def _set_request_trace(enabled: bool, *, session_id: str, dataset: str, mode_tag: str) -> None:
    set_trace_context({
        'enabled': bool(enabled),
        'sampled': True,
        'session_id': session_id,
        'request_tags': [f'dataset:{dataset}', f'mode:{mode_tag}'],
        'module_trace': {'default': True},
    })


def _flush_trace_exporter() -> None:
    try:
        if provider := getattr(tracing_runtime._runtime, '_provider', None):
            provider.force_flush()
    except Exception as exc:
        LOG.warning(f'[ChatServer] [TRACE_FLUSH_FAILED] {exc}')


def _sse_line(payload: Dict[str, Any]) -> str:
    return json.dumps(payload, ensure_ascii=False, default=str) + '\n\n'


def _resp(code: int, msg: str, data: Any, cost: float) -> Dict[str, Any]:
    return {'code': code, 'msg': msg, 'data': data, 'cost': cost}


def _single_event_stream_response(payload: Dict[str, Any]) -> StreamingResponse:
    async def _stream():
        yield _sse_line(payload)
        yield _sse_line(_resp(200, 'success', {'status': 'FINISHED'}, 0.0))

    return StreamingResponse(_stream(), media_type='text/event-stream')


def check_sensitive_content(
    query: str, session_id: str, start_time: float
) -> Optional[Dict[str, Any]]:
    if not sensitive_filter.loaded:
        return None
    has_sensitive, sensitive_word = sensitive_filter.check(query)
    if has_sensitive:
        cost = round(time.time() - start_time, 3)
        LOG.warning(
            f'[ChatServer] [SENSITIVE_FILTER_BLOCKED] [query={query[:50]}...] '
            f'[sensitive_word={sensitive_word}] [session_id={session_id}]'
        )
        return _resp(
            200,
            'success',
            {
                'think': None,
                'text': SENSITIVE_FILTER_RESPONSE_TEXT,
                'sources': [],
            },
            cost,
        )
    return None


def build_query_params(query: str, history: Optional[List[Dict[str, Any]]],
                       filters: Optional[Dict[str, Any]], other_files: List[str],
                       databases: Optional[List[Dict[str, Any]]], debug: bool,
                       image_files: List[str], priority: Optional[int],
                       dataset: Optional[str],
                       session_id: str,
                       available_tools: Optional[List[str]],
                       available_skills: Optional[List[str]],
                       memory: Optional[str],
                       user_preference: Optional[str],
                       use_memory: Optional[bool],
                       environment_context: Optional[Dict[str, Any]] = None,
                       user_id: Optional[str] = None) -> Dict[str, Any]:
    hist = [
        {
            'role': str(h.get('role', 'assistant')),
            'content': str(h.get('content', '')),
        }
        for h in (history or [])
        if isinstance(h, dict)
    ]
    return {
        'query': query, 'history': hist, 'filters': filters if RAG_MODE and filters else {},
        'files': other_files, 'image_files': image_files if MULTIMODAL_MODE and image_files else [],
        'debug': debug, 'databases': databases if RAG_MODE and databases else [], 'priority': priority,
        'dataset': dataset,
        'session_id': session_id,
        'available_tools': available_tools,
        'available_skills': available_skills,
        'memory': memory,
        'user_preference': user_preference,
        'use_memory': use_memory,
        'environment_context': environment_context if isinstance(environment_context, dict) else {},
        'user_id': user_id or '',
    }


def log_chat_request(query: str, session_id: str, filters: Optional[Dict[str, Any]],
                     other_files: List[str], databases: Optional[List[Dict[str, Any]]],
                     image_files: List[str], cost: float,
                     response: Any = None, log_type: str = 'KB_CHAT') -> None:
    databases_str = json.dumps(databases, ensure_ascii=False) if databases else []
    response_str = response if response is not None else None
    LOG.info(
        f'[ChatServer] [{log_type}] [query={query}] [session_id={session_id}] '
        f'[filters={filters}] [files={other_files}] [image_files={image_files}] '
        f'[databases={databases_str}] [cost={cost}] [response={response_str}]'
    )


def _attach_trace_info(data: Any, trace_id: Optional[str]) -> Any:
    if trace_id is None:
        return data
    return {**data, 'trace_id': trace_id} if isinstance(data, dict) else {'data': data, 'trace_id': trace_id}


def _normalize_stream_chunk(chunk: Any) -> Any:
    if isinstance(chunk, dict):
        return dict(chunk)
    if isinstance(chunk, str):
        try:
            payload = json.loads(chunk)
        except (TypeError, ValueError):
            return chunk
        if isinstance(payload, dict):
            return payload
    return chunk


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
    priority = LAZYMIND_LLM_PRIORITY if priority is None else priority

    start_time = time.time()
    sensitive_check_result = check_sensitive_content(query, session_id, start_time)
    log_tag = 'KB_CHAT_STREAM'
    LOG.info(f'[ChatServer] [{log_tag}] [query={query}] [sid={session_id}]')
    LOG.info(
        f'[ChatServer] [MODEL_CONFIG_RECEIVED] [sid={session_id}] [user_id={user_id or ""}] '
        f'[active_config={get_config_path()}] [{summarize_model_config_for_log(model_config)}]'
    )

    other_files, image_files = validate_and_resolve_files(files)
    query_params = build_query_params(
        query=query,
        history=history,
        filters=filters,
        other_files=other_files,
        databases=databases,
        debug=debug or False,
        image_files=image_files,
        priority=priority,
        dataset=dataset,
        session_id=session_id,
        available_tools=available_tools,
        available_skills=available_skills,
        memory=memory,
        user_preference=user_preference,
        use_memory=use_memory,
        environment_context=environment_context,
        user_id=user_id,
    )

    def _init_session():
        lazyllm.globals._init_sid(sid=session_id)
        lazyllm.locals._init_sid(sid=session_id)
        inject_model_config(model_config)
        from lazyllm.tools.tool_config_inject import inject_tool_config  # type: ignore[import]  # noqa: PLC0415
        inject_tool_config(tool_config)

    if sensitive_check_result:
        return _single_event_stream_response(sensitive_check_result)

    first_frame_logged = False
    collected_chunks: List[str] = []
    query_params['stream'] = True

    async def event_stream(params: Dict[str, Any]) -> Any:
        nonlocal first_frame_logged
        try:
            async with rag_sem:
                _init_session()
                async_result, trace_id = await asyncio.to_thread(
                    _run_ppl_with_trace, agentic_rag, (params,),
                    session_id=session_id, dataset=dataset,
                    mode_tag='stream_reasoning' if reasoning else 'stream',
                    trace_enabled=trace, model_config=model_config,
                )
                if trace_id is not None:
                    yield _sse_line(
                        _resp(200, 'success', _attach_trace_info({}, trace_id), 0.0)
                    )
                async for chunk in async_result:
                    now = time.time()
                    if not first_frame_logged:
                        first_cost = round(now - start_time, 3)
                        LOG.info(
                            f'[ChatServer] [KB_CHAT_STREAM_FIRST_FRAME] '
                            f'[query={query}] [session_id={session_id}] '
                            f'[cost={first_cost}]'
                        )
                        first_frame_logged = True

                    agentic_config = lazyllm.globals.get('agentic_config')
                    rewrite_config = (
                        agentic_config if isinstance(agentic_config, dict) else None
                    )

                    chunk_data = _normalize_stream_chunk(chunk)
                    if isinstance(chunk_data, dict):
                        text = chunk_data.get('text')
                        if isinstance(text, str) and text:
                            chunk_data['text'] = rewrite_markdown_image_urls(
                                text, config=rewrite_config,
                            )
                    elif isinstance(chunk_data, str):
                        chunk_data = rewrite_markdown_image_urls(
                            chunk_data, config=rewrite_config,
                        )

                    collected_chunks.append(
                        json.dumps(chunk_data, ensure_ascii=False, default=str)
                    )
                    cost = round(now - start_time, 3)
                    yield _sse_line(_resp(200, 'success', chunk_data, cost))
        except Exception as exc:
            LOG.exception(exc)
            collected_chunks.append(f'[EXCEPTION]: {str(exc)}')
            final_resp = _resp(
                500, f'chat service failed: {exc}', {'status': 'FAILED'}, 0.0
            )
        else:
            final_resp = _resp(200, 'success', {'status': 'FINISHED'}, 0.0)

        cost = round(time.time() - start_time, 3)
        final_resp['cost'] = cost
        yield _sse_line(final_resp)

        log_chat_request(query, session_id, filters, other_files, databases, image_files,
                         cost, '\n'.join(collected_chunks), 'KB_CHAT_STREAM_FINISH')

    return StreamingResponse(
        event_stream(query_params), media_type='text/event-stream'
    )

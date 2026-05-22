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
from chat.config import (RAG_MODE, MULTIMODAL_MODE, MAX_CONCURRENCY,
                         LAZYMIND_LLM_PRIORITY, SENSITIVE_FILTER_RESPONSE_TEXT,
                         URL_MAP, resolve_dataset_url)
from chat.utils.helpers import validate_and_resolve_files
from chat.app.core.chat_server import chat_server
from chat.utils.load_config import get_config_path, inject_model_config, summarize_model_config_for_log
from chat.utils.markdown_images import rewrite_markdown_image_urls


rag_sem = asyncio.Semaphore(MAX_CONCURRENCY)


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


def check_sensitive_content(
    query: str, session_id: str, start_time: float
) -> Optional[Dict[str, Any]]:
    if not chat_server.sensitive_filter.loaded:
        return None
    has_sensitive, sensitive_word = chat_server.sensitive_filter.check(query)
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
        'document_url': URL_MAP.get(dataset, ''),
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


def _build_ppl_call(reasoning: bool, dataset: str, query_params: Dict[str, Any],
                    stream: bool) -> tuple:
    if reasoning:
        dataset_url = resolve_dataset_url(dataset)
        if dataset_url is None:
            raise KeyError(f'dataset `{dataset}` not found in URL_MAP')
        ppl = chat_server.query_ppl_reasoning
        params = {**query_params, 'document_url': dataset_url, 'stream': stream}
    else:
        ppl = chat_server.get_query_pipeline(dataset, stream=stream)
        params = query_params
    return (ppl, params)


async def handle_chat(query: str, history: Optional[List[Dict[str, Any]]],
                      session_id: str, filters: Optional[Dict[str, Any]],
                      files: Optional[List[str]], debug: Optional[bool], reasoning: Optional[bool],
                      databases: Optional[List[Dict[str, Any]]], dataset: Optional[str],
                      priority: Optional[int], available_tools: Optional[List[str]],
                      available_skills: Optional[List[str]], memory: Optional[str],
                      user_preference: Optional[str], use_memory: Optional[bool],
                      is_stream: bool, trace: bool = False,
                      environment_context: Optional[Dict[str, Any]] = None,
                      user_id: Optional[str] = None,
                      model_config: Optional[Dict[str, Any]] = None) -> Union[Dict[str, Any], StreamingResponse]:
    result = None
    priority = LAZYMIND_LLM_PRIORITY if priority is None else priority

    if not chat_server.has_dataset(dataset):
        return _resp(400, f'dataset {dataset} not found', None, 0.0)

    start_time = time.time()
    sensitive_check_result = check_sensitive_content(query, session_id, start_time)
    log_tag = 'KB_CHAT_STREAM' if is_stream else 'KB_CHAT'
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

    if not is_stream:
        if sensitive_check_result:
            return sensitive_check_result

        try:
            async with rag_sem:
                _init_session()
                ppl_call = _build_ppl_call(bool(reasoning), dataset, query_params, stream=False)
                result, trace_id = await asyncio.to_thread(
                    _run_ppl_with_trace, ppl_call[0], ppl_call[1:],
                    session_id=session_id, dataset=dataset,
                    mode_tag='sync_reasoning' if reasoning else 'sync',
                    trace_enabled=trace, model_config=model_config,
                )
                cost = round(time.time() - start_time, 3)
                data = _attach_trace_info(result, trace_id)
                return _resp(200, 'success', data, cost)
        except Exception as exc:
            LOG.exception(exc)
            cost = round(time.time() - start_time, 3)
            return _resp(500, f'chat service failed: {exc}', None, cost)
        finally:
            cost = round(time.time() - start_time, 3)
            log_chat_request(
                query, session_id, filters, other_files, databases, image_files, cost, result
            )
    else:
        if sensitive_check_result:

            async def error_stream():
                yield _sse_line(sensitive_check_result)
                yield _sse_line(_resp(200, 'success', {'status': 'FINISHED'}, 0.0))

            return StreamingResponse(error_stream(), media_type='text/event-stream')

        first_frame_logged = False
        collected_chunks: List[str] = []
        ppl_call = _build_ppl_call(bool(reasoning), dataset, query_params, stream=True)

        async def event_stream(ppl, *args) -> Any:
            nonlocal first_frame_logged
            trace_id = None
            try:
                async with rag_sem:
                    _init_session()
                    _set_request_trace(
                        bool(trace),
                        session_id=session_id,
                        dataset=dataset,
                        mode_tag='stream_reasoning' if reasoning else 'stream',
                    )
                    async_result = ppl(*args)

                    async for chunk in async_result:
                        if trace and trace_id is None:
                            trace_id = get_trace_context().trace_id
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

                        if isinstance(chunk, dict):
                            payload = dict(chunk)
                            text = payload.get('text')
                            if isinstance(text, str) and text:
                                payload['text'] = rewrite_markdown_image_urls(
                                    text, config=rewrite_config,
                                )
                            chunk_data = payload
                        elif isinstance(chunk, str):
                            try:
                                payload = json.loads(chunk)
                            except (TypeError, ValueError):
                                payload = None
                            if isinstance(payload, dict):
                                text = payload.get('text')
                                if isinstance(text, str) and text:
                                    payload['text'] = rewrite_markdown_image_urls(
                                        text, config=rewrite_config,
                                    )
                                chunk_data = payload
                            else:
                                chunk_data = rewrite_markdown_image_urls(
                                    chunk, config=rewrite_config,
                                )
                        else:
                            chunk_data = chunk

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
            if trace and trace_id is None:
                trace_id = get_trace_context().trace_id
            if trace_id:
                final_resp['data'] = _attach_trace_info(final_resp['data'], trace_id)
                _flush_trace_exporter()
            yield _sse_line(final_resp)

            log_chat_request(query, session_id, filters, other_files, databases, image_files,
                             cost, '\n'.join(collected_chunks), 'KB_CHAT_STREAM_FINISH')

        return StreamingResponse(
            event_stream(*ppl_call), media_type='text/event-stream'
        )

from __future__ import annotations

# ruff: noqa: E402

import asyncio
import json
import os
import re
import threading
import time
from pathlib import Path
from queue import Empty, Queue
from typing import Any, Dict

import lazyllm
from lazyllm import loop, once_wrapper
from lazyllm.tracing import set_trace_context
from lazyllm.tools.agent.functionCall import FunctionCall
from lazyllm.tools.fs.client import FS

from config import config as _cfg


from chat.components.agentic.config import (  # noqa: E402
    _build_runtime_system_prompt,
    _filter_tools_for_request,
    _normalize_available_skills,
    _normalize_available_tools,
    _sync_request_context,
)
from chat.components.agentic.history import (  # noqa: E402
    _build_stream_citation_scanner,
    _count_tool_turns,
    _count_user_turns,
    _format_final_result,
    _normalize_history_for_agent,
    _reset_citation_state,
)
from chat.components.agentic.review import (  # noqa: E402
    _build_review_decision,
    _spawn_background_review,
)
from chat.utils.markdown_images import rewrite_markdown_image_urls  # noqa: E402
from chat.components.agentic.tool_stream import (  # noqa: E402
    _STREAM_CHUNK_SIZE,
    _format_tool_stream_frame,
    _iter_text_chunks,
    _normalize_tool_call,
    _stream_frame,
    _tool_call_id,
)
from lazyllm import AutoModel  # noqa: E402
from lazyllm.tools.fs.supplier.feishu import FeishuFS  # type: ignore[import]  # noqa: E402
from chat.utils.load_config import get_config_path  # noqa: E402


def _augment_query_with_attached_images(query: str, config: dict[str, Any]) -> str:
    '''Run VLM once on ``config['image_files']`` and merge summaries into ``query``.

    The main chat LLM stays text-only; paths remain in ``config`` for
    ``vision_extractor`` and image-node retrieval.
    '''
    raw_paths = config.get('image_files') or []
    if not isinstance(raw_paths, list) or not raw_paths:
        return query
    clean = [str(p).strip() for p in raw_paths if str(p).strip()]
    if not clean:
        return query
    try:
        from chat.components.process.query_image_rewriter import QueryImageRewriter

        payload: dict[str, Any] = {
            'query': query,
            'image_files': clean,
            'priority': int(config.get('priority', 0) or 0),
        }
        rewriter = QueryImageRewriter(
            vlm=AutoModel(model='vlm', config=get_config_path()),
        )
        out = rewriter.forward(payload)
        if isinstance(out, dict):
            nq = out.get('query')
            if isinstance(nq, str) and nq.strip():
                return nq.strip()
    except Exception as exc:
        lazyllm.LOG.warning(f'[agentic] attached-image VLM rewrite skipped: {exc}')
    return query


class _StreamingFunctionCall(FunctionCall):
    def __init__(self, *args: Any, stream_event_callback=None, **kwargs: Any):
        super().__init__(*args, **kwargs)
        self._stream_event_callback = stream_event_callback
        self._round_index = 0

    def _post_action(self, llm_output: Dict[str, Any]):
        self._round_index += 1
        if (
            isinstance(llm_output, dict)
            and not llm_output.get('tool_calls')
            and isinstance(llm_output.get('content'), str)
        ):
            match = re.search(
                r'Action:\s*Call\s+(\w+)\s+with\s+parameters\s+(\{.*?\})',
                llm_output['content'],
            )
            if match:
                try:
                    llm_output['tool_calls'] = [{
                        'type': 'function',
                        'function': {
                            'name': match.group(1),
                            'arguments': json.loads(match.group(2)),
                        },
                    }]
                except json.JSONDecodeError:
                    pass
        tool_calls = []
        if isinstance(llm_output, dict):
            for idx, tc in enumerate((llm_output.get('tool_calls') or []), start=1):
                if not isinstance(tc, dict):
                    continue
                normalized_tool_call = _normalize_tool_call(tc, coerce_arguments=False)
                normalized_tool_call['id'] = _tool_call_id(
                    normalized_tool_call, self._round_index, idx
                )
                tool_calls.append(normalized_tool_call)
            if tool_calls:
                execution_tool_calls = [
                    _normalize_tool_call(tool_call, coerce_arguments=True)
                    for tool_call in tool_calls
                ]
                llm_output['tool_calls'] = [
                    {
                        'id': tool_call['id'],
                        'type': 'function',
                        'function': {
                            'name': tool_call.get('name', ''),
                            'arguments': json.dumps(
                                tool_call.get('arguments', {}),
                                ensure_ascii=False,
                            ),
                        },
                    }
                    for tool_call in execution_tool_calls
                ]

        if self._stream_event_callback and isinstance(llm_output, dict) and tool_calls:
            self._stream_event_callback({
                'round': self._round_index,
                'content': llm_output.get('content', ''),
                'tool_calls': tool_calls,
                'tool_results': [],
            })

        result = super()._post_action(llm_output)

        if self._stream_event_callback and isinstance(llm_output, dict) and tool_calls:
            tool_call_trace = (
                lazyllm.locals.get('_lazyllm_agent', {})
                .get('workspace', {})
                .get('tool_call_trace', [])
            )
            self._stream_event_callback({
                'round': self._round_index,
                'content': '',
                'tool_calls': [],
                'tool_results': [
                    {
                        'id': tool_call.get('id', ''),
                        'tool_name': tool_call.get('name', ''),
                        'result': tool_trace.get('tool_call_result'),
                    }
                    for tool_call, tool_trace in zip(tool_calls, tool_call_trace)
                    if isinstance(tool_trace, dict)
                ],
            })
        return result


class _StreamingReactAgent(lazyllm.tools.agent.ReactAgent):
    def __init__(self, *args: Any, stream_event_callback=None, **kwargs: Any):
        super().__init__(*args, **kwargs)
        self._stream_event_callback = stream_event_callback

    @once_wrapper(reset_on_pickle=True)
    def build_agent(self):
        agent = loop(
            _StreamingFunctionCall(
                llm=self._llm,
                _prompt=self._prompt,
                return_trace=self._return_trace,
                stream=self._stream,
                _tool_manager=self._tools_manager,
                skill_manager=self._skill_manager,
                keep_full_turns=self._keep_full_turns,
                stream_event_callback=self._stream_event_callback,
            ),
            stop_condition=lambda x: isinstance(x, str),
            count=20,
        )
        self._agent = agent


def _feishu_key_source(_instance) -> str:
    try:
        mapping = lazyllm.globals.config['dynamic_fs_auth'] or {}
    except Exception:
        return ''
    r = (mapping.get('feishu') or '').strip()
    return r


_FEISHU_FS_INSTANCE = FeishuFS(space_id='dynamic', dynamic_auth=True)


def agentic_forward(
    query: str,
    history: list[dict[str, Any]],
    stream_event_callback=None,
) -> Any:
    config = lazyllm.globals['agentic_config'] or {}
    if not isinstance(config, dict):
        config = {}

    llm = AutoModel(model='llm', config=get_config_path())
    available_tools = _filter_tools_for_request(
        _normalize_available_tools(config.get('available_tools')),
        config,
    )
    available_skills = _normalize_available_skills(config.get('available_skills'))
    skills_dir = _cfg['skill_fs_url']
    config['available_tools'] = available_tools
    config['available_skills'] = available_skills

    original_query = query.strip()
    agent_query = _augment_query_with_attached_images(original_query, config)

    keep_full_turns = _cfg['agentic_keep_full_turns']
    runtime_prompt = _build_runtime_system_prompt(config, available_tools)
    agent_cls = _StreamingReactAgent if stream_event_callback else lazyllm.tools.agent.ReactAgent
    agent_kwargs = {
        'llm': llm,
        'tools': available_tools + [(_FEISHU_FS_INSTANCE, _feishu_key_source)],
        'max_retries': _cfg['max_retries'],
        'stream': bool(stream_event_callback),
        'prompt': runtime_prompt,
        'skills': available_skills,
        'workspace': _cfg['agentic_workspace'],
        'keep_full_turns': keep_full_turns,
        'fs': FS,
        'skills_dir': skills_dir,
        'enable_builtin_tools': False,
        'force_summarize': True,
        'force_summarize_context': agent_query,
    }
    if stream_event_callback:
        agent_kwargs['stream_event_callback'] = stream_event_callback

    react_agent = agent_cls(
        **agent_kwargs,
    )

    request_global_sid = lazyllm.globals._sid
    lazyllm.globals['agentic_config'] = config
    agent_output = react_agent(agent_query, llm_chat_history=history)
    agent_history = lazyllm.locals.get('_lazyllm_agent', {}).get('history', [])
    history_snapshot = agent_history
    if runtime_prompt and (not history_snapshot or history_snapshot[0].get('role') != 'system'):
        history_snapshot = (
            [{'role': 'system', 'content': runtime_prompt}]
            + history_snapshot
            + [{'role': 'assistant', 'content': agent_output}]
        )
    tool_turns = _count_tool_turns(agent_history)
    user_turns = _count_user_turns(history, original_query)
    memory_review_interval = _cfg['memory_review_interval']
    skill_review_interval = _cfg['skill_review_interval']
    review_decision = _build_review_decision(
        available_tools=available_tools,
        tool_turns=tool_turns,
        user_turns=user_turns,
        memory_review_interval=memory_review_interval,
        skill_review_interval=skill_review_interval,
    )
    print(
        '[bg-review] DECISION '
        f"mode={review_decision.get('mode')} "
        f"memory_due={review_decision.get('memory_due')} "
        f"skill_due={review_decision.get('skill_due')} "
        f"skill_due_by_tool_turns={review_decision.get('skill_due_by_tool_turns')} "
        f"skill_due_by_user_turns={review_decision.get('skill_due_by_user_turns')} "
        f"debug_force_combined={review_decision.get('debug_force_combined')} "
        f'tool_turns={tool_turns} user_turns={user_turns} '
        f'memory_interval={memory_review_interval} skill_interval={skill_review_interval} '
        f'available_tools={available_tools}'
    )
    review_mode = review_decision['mode']
    if review_mode is not None:
        _spawn_background_review(
            config=config,
            llm=llm,
            keep_full_turns=keep_full_turns,
            history_snapshot=history_snapshot,
            review_mode=review_mode,
            request_global_sid=request_global_sid,
        )

    return agent_output


def _lazyllm_queue_db_path() -> Path:
    from lazyllm.configs import config

    home = Path(os.path.expanduser(config['home']))
    return home / '.lazyllm_filesystem_queue.db'


def _clear_orphaned_lazyllm_queue_lock() -> None:
    db_path = _lazyllm_queue_db_path()
    lock_path = Path(f'{db_path}.lock')
    if lock_path.exists() and not db_path.exists():
        lock_path.unlink(missing_ok=True)


async def _agentic_forward_stream(
    query: str,
    history: list[dict[str, Any]],
    runtime_params: dict[str, Any],
    global_sid: str,
    local_sid: str,
    trace_config: dict[str, Any],
):
    event_queue: Queue = Queue()
    sentinel = object()
    closed = threading.Event()
    worker_done = threading.Event()
    output_lock = threading.Lock()
    streamed_text = False
    text_scanner, citation_plugin = _build_stream_citation_scanner(runtime_params)

    lazyllm.globals._init_sid(global_sid)
    lazyllm.locals._init_sid(local_sid)
    set_trace_context(trace_config)
    _clear_orphaned_lazyllm_queue_lock()
    lazyllm.FileSystemQueue().clear()
    lazyllm.FileSystemQueue.get_instance('think').clear()

    def _drain_stream_frames() -> list[dict[str, Any]]:
        nonlocal streamed_text
        frames: list[dict[str, Any]] = []

        think_values = lazyllm.FileSystemQueue.get_instance('think').dequeue()
        if think_values:
            think_text = ''.join(think_values)
            if think_text:
                frames.append(_stream_frame(think=think_text))

        text_values = lazyllm.FileSystemQueue().dequeue()
        if text_values:
            text = ''.join(text_values)
            if text:
                for field, seg in text_scanner.feed(text):
                    if not seg:
                        continue
                    if field == 'think':
                        frames.append(_stream_frame(think=seg))
                    else:
                        streamed_text = True
                        seg = rewrite_markdown_image_urls(seg, config=runtime_params)
                        frames.append(_stream_frame(text=seg))

        return frames

    def _flush_stream_frames_to_queue() -> None:
        if closed.is_set():
            return
        for frame in _drain_stream_frames():
            event_queue.put({'type': 'frame', 'frame': frame})

    def _emit_event(event: dict[str, Any]) -> None:
        if closed.is_set():
            return
        with output_lock:
            _flush_stream_frames_to_queue()
            tool_event = dict(event)
            tool_event['preview_text'] = query
            frame = _format_tool_stream_frame(tool_event)
            if frame is not None:
                event_queue.put({'type': 'frame', 'frame': frame})

    def _stream_monitor() -> None:
        lazyllm.globals._init_sid(global_sid)
        lazyllm.locals._init_sid(local_sid)
        set_trace_context(trace_config)
        while not worker_done.is_set() and not closed.is_set():
            with output_lock:
                _flush_stream_frames_to_queue()
            time.sleep(0.02)

    def _worker() -> None:
        lazyllm.globals._init_sid(global_sid)
        lazyllm.locals._init_sid(local_sid)
        set_trace_context(trace_config)
        lazyllm.globals['agentic_config'] = runtime_params
        try:
            result = agentic_forward(
                query=query,
                history=history,
                stream_event_callback=_emit_event,
            )
            if not closed.is_set():
                with output_lock:
                    _flush_stream_frames_to_queue()
                    event_queue.put({'type': 'final', 'result': result})
        except Exception as exc:
            if not closed.is_set():
                event_queue.put(exc)
        finally:
            worker_done.set()
            if not closed.is_set():
                event_queue.put(sentinel)

    worker = threading.Thread(target=_worker, daemon=True)
    monitor = threading.Thread(target=_stream_monitor, daemon=True)
    worker.start()
    monitor.start()
    final_result = None
    try:
        while True:
            try:
                event = await asyncio.to_thread(event_queue.get, True, 0.05)
            except Empty:
                continue

            if event is sentinel:
                break
            if isinstance(event, Exception):
                raise event
            if isinstance(event, dict) and event.get('type') == 'frame':
                frame = event.get('frame')
                if isinstance(frame, dict):
                    yield frame
            elif isinstance(event, dict) and event.get('type') == 'final':
                final_result = event.get('result')

        with output_lock:
            trailing_frames = _drain_stream_frames()
        for frame in trailing_frames:
            yield frame
        for field, seg in text_scanner.flush():
            if not seg:
                continue
            if field == 'think':
                yield _stream_frame(think=seg)
            else:
                streamed_text = True
                seg = rewrite_markdown_image_urls(seg, config=runtime_params)
                yield _stream_frame(text=seg)

        output = _format_final_result(final_result, runtime_params)
        chunk_size = int(_cfg['agentic_stream_chunk_size'] or _STREAM_CHUNK_SIZE)
        if not streamed_text:
            think = str(output.get('think') or '')
            if think:
                for chunk in _iter_text_chunks(think, chunk_size):
                    yield _stream_frame(think=chunk)
            final_text = rewrite_markdown_image_urls(
                str(output.get('text') or ''), config=runtime_params,
            )
            for chunk in _iter_text_chunks(final_text, chunk_size):
                yield _stream_frame(
                    text=chunk,
                )

        sources = output.get('sources') or citation_plugin.collect()
        if sources:
            yield _stream_frame(
                text='',
                sources=sources,
            )
    finally:
        closed.set()
        worker.join(timeout=0)
        monitor.join(timeout=0)


def _ensure_tools_registered() -> None:
    # Trigger @fc_register side effects once so ReactAgent can resolve tool names.
    from chat.tools import calculator, kb, memory, skill_manager, vocab, vision_extractor, web_search  # noqa: F401


def agentic_rag(
    params: Dict[str, Any],
) -> Any:
    _ensure_tools_registered()

    query = (params or {}).get('query', '')
    if not isinstance(query, str) or not query.strip():
        raise ValueError('query is required')

    runtime_params = dict(params or {})
    runtime_params['stream'] = True
    _sync_request_context(runtime_params)
    _reset_citation_state(runtime_params)

    history = (params or {}).get('history') or []
    if not isinstance(history, list):
        history = []
    history = _normalize_history_for_agent(history, runtime_params)

    lazyllm.globals['agentic_config'] = runtime_params

    return _agentic_forward_stream(
        query=query.strip(),
        history=history,
        runtime_params=runtime_params,
        global_sid=lazyllm.globals._sid,
        local_sid=lazyllm.locals._sid,
        trace_config=lazyllm.globals.get('trace') or {},
    )

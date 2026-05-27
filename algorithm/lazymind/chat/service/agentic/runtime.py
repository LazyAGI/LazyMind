from __future__ import annotations

# ruff: noqa: E402

import asyncio
import os
import threading
from pathlib import Path
from queue import Empty, Queue
from typing import Any, Dict

import lazyllm
from lazyllm.tracing import set_trace_context
from lazyllm.tools.fs.client import FS

from lazymind.config import config as _cfg


from lazymind.chat.engine.prompts.agentic_builder import _build_system_prompt  # noqa: E402
from lazymind.chat.service.agentic.request_context import (  # noqa: E402
    _filter_tools_for_request,
    _normalize_available_skills,
    _normalize_available_tools,
    _sync_request_context,
)
from lazymind.chat.service.agentic.history import (  # noqa: E402
    _build_stream_citation_scanner,
    _count_tool_turns,
    _count_user_turns,
    _format_final_result,
    _normalize_history_for_agent,
    _reset_citation_state,
)
from lazymind.review.service.review import (  # noqa: E402
    _build_review_decision,
    _spawn_background_review,
)
from lazymind.chat.service.utils.markdown_images import rewrite_markdown_image_urls  # noqa: E402
from lazymind.chat.service.agentic.tool_stream import (  # noqa: E402
    _STREAM_CHUNK_SIZE,
    _format_tool_stream_frame,
    _iter_text_chunks,
    _normalize_tool_call,
    _stream_frame,
    _tool_call_id,
)
from lazyllm import AutoModel  # noqa: E402
from lazyllm.tools.fs.supplier.feishu import FeishuFS  # type: ignore[import]  # noqa: E402
from lazymind.model_config import get_config_path  # noqa: E402
from lazymind.chat.engine.tools import (  # noqa: E402
    CalculatorToolGroup,
    KBToolGroup,
    MemoryToolGroup,
    MultimodalToolGroup,
    SkillManagerToolGroup,
    VocabToolGroup,
    WebSearchToolGroup,
)


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
        from lazymind.chat.service.agentic.query_image_rewriter import QueryImageRewriter

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


def _feishu_key_source(_instance) -> str:
    try:
        mapping = lazyllm.globals.config['dynamic_fs_auth'] or {}
    except Exception:
        return ''
    r = (mapping.get('feishu') or '').strip()
    lazyllm.LOG.warning(f'get feishu key: {r}')
    return r


_FEISHU_FS_INSTANCE = FeishuFS(space_id='dynamic', dynamic_auth=True)

_TOOL_GROUP_BUILDERS: dict[str, Any] = {
    'calculator': CalculatorToolGroup,
    'memory': MemoryToolGroup,
    'skill_manage': SkillManagerToolGroup,
    'vocab_manage': VocabToolGroup,
    'web_search': WebSearchToolGroup,
    'arxiv_search': WebSearchToolGroup,
    'url_fetch': WebSearchToolGroup,
}


def _split_agent_tools(available_tools: list[str], config: dict[str, Any]) -> tuple[list[str], list[Any]]:
    prompt_tools: list[str] = []
    runtime_tools: list[Any] = []
    kb_group_added = False
    tool_group_instances: dict[type, Any] = {}

    for tool_name in available_tools:
        if tool_name.startswith('kb_'):
            prompt_tools.append(tool_name)
            if not kb_group_added:
                runtime_tools.append(KBToolGroup())
                kb_group_added = True
            continue
        if tool_name == 'vision_extractor':
            prompt_tools.append(tool_name)
            runtime_tools.append(MultimodalToolGroup())
            continue
        tool_group_builder = _TOOL_GROUP_BUILDERS.get(tool_name)
        if tool_group_builder is not None:
            prompt_tools.append(tool_name)
            if tool_group_builder not in tool_group_instances:
                tool_group_instances[tool_group_builder] = tool_group_builder()
                runtime_tools.append(tool_group_instances[tool_group_builder])
            continue
        prompt_tools.append(tool_name)
        runtime_tools.append(tool_name)

    return prompt_tools, runtime_tools


def _build_agentic_run_context(
    query: str,
    *,
    stream: bool,
) -> dict[str, Any]:
    config = lazyllm.globals['agentic_config'] or {}
    if not isinstance(config, dict):
        config = {}

    llm = AutoModel(model='llm', config=get_config_path())
    requested_tools = _filter_tools_for_request(
        _normalize_available_tools(config.get('available_tools')),
        config,
    )
    available_tools, runtime_tools = _split_agent_tools(requested_tools, config)
    available_skills = _normalize_available_skills(config.get('available_skills'))
    skills_dir = _cfg['skill_fs_url']
    config['available_tools'] = available_tools
    config['available_skills'] = available_skills

    original_query = query.strip()
    agent_query = _augment_query_with_attached_images(original_query, config)

    keep_full_turns = _cfg['agentic_keep_full_turns']
    env_ctx = config.get('environment_context')
    time_ctx = (env_ctx or {}).get('time') if isinstance(env_ctx, dict) else None
    runtime_prompt = _build_system_prompt(
        available_tools,
        time_now=(time_ctx or {}).get('now') if isinstance(time_ctx, dict) else None,
        timezone=(time_ctx or {}).get('timezone') if isinstance(time_ctx, dict) else None,
        use_memory=config.get('use_memory', True),
        user_preference=config.get('user_preference'),
        memory=config.get('memory'),
        image_files=config.get('image_files'),
    )
    react_agent = lazyllm.tools.agent.ReactAgent(
        llm=llm,
        tools=runtime_tools + [(_FEISHU_FS_INSTANCE, _feishu_key_source)],
        max_retries=_cfg['max_retries'],
        stream=stream,
        prompt=runtime_prompt,
        skills=available_skills,
        workspace=_cfg['agentic_workspace'],
        keep_full_turns=keep_full_turns,
        fs=FS,
        skills_dir=skills_dir,
        enable_builtin_tools=False,
        force_summarize=True,
        force_summarize_context=agent_query,
    )
    return {
        'config': config,
        'llm': llm,
        'available_tools': available_tools,
        'keep_full_turns': keep_full_turns,
        'runtime_prompt': runtime_prompt,
        'original_query': original_query,
        'agent_query': agent_query,
        'react_agent': react_agent,
    }


def _finalize_agentic_run(
    *,
    agent_output: Any,
    history: list[dict[str, Any]],
    context: dict[str, Any],
    request_global_sid: str,
) -> Any:
    llm = context['llm']
    config = context['config']
    available_tools = context['available_tools']
    keep_full_turns = context['keep_full_turns']
    runtime_prompt = context['runtime_prompt']
    original_query = context['original_query']

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


def _agent_event_value(event: Any, field: str, default: Any = None) -> Any:
    if isinstance(event, dict):
        return event.get(field, default)
    return getattr(event, field, default)


def _normalize_stream_tool_calls(tool_calls: list[Any], round_index: int) -> list[dict[str, Any]]:
    normalized: list[dict[str, Any]] = []
    for idx, tool_call in enumerate(tool_calls, start=1):
        if not isinstance(tool_call, dict):
            continue
        item = _normalize_tool_call(tool_call, coerce_arguments=False)
        item['id'] = _tool_call_id(item, round_index, idx)
        normalized.append(item)
    return normalized


def _normalize_stream_tool_results(
    tool_results: list[Any],
    tool_calls: list[dict[str, Any]],
) -> list[dict[str, Any]]:
    normalized: list[dict[str, Any]] = []
    for idx, tool_result in enumerate(tool_results):
        if not isinstance(tool_result, dict):
            continue
        paired_tool_call = tool_calls[idx] if idx < len(tool_calls) else {}
        normalized.append({
            'id': str(tool_result.get('id') or paired_tool_call.get('id') or ''),
            'tool_name': str(
                tool_result.get('tool_name')
                or tool_result.get('name')
                or paired_tool_call.get('name')
                or ''
            ),
            'result': tool_result.get('result'),
        })
    return normalized


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
    streamed_text = False
    text_scanner, citation_plugin = _build_stream_citation_scanner(runtime_params)
    stream_state = {'round_index': 0, 'last_tool_calls': []}

    lazyllm.globals._init_sid(global_sid)
    lazyllm.locals._init_sid(local_sid)
    set_trace_context(trace_config)
    _clear_orphaned_lazyllm_queue_lock()

    def _agent_event_to_frames(event: Any) -> list[dict[str, Any]]:
        nonlocal streamed_text
        frames: list[dict[str, Any]] = []
        event_type = str(_agent_event_value(event, 'type', '') or '')
        if event_type == 'agent.reasoning.delta':
            delta = str(_agent_event_value(event, 'delta', '') or '')
            if delta:
                frames.append(_stream_frame(think=delta))
            return frames
        if event_type == 'agent.text.delta':
            delta = str(_agent_event_value(event, 'delta', '') or '')
            if not delta:
                return frames
            for field, seg in text_scanner.feed(delta):
                if not seg:
                    continue
                if field == 'think':
                    frames.append(_stream_frame(think=seg))
                else:
                    streamed_text = True
                    seg = rewrite_markdown_image_urls(seg, config=runtime_params)
                    frames.append(_stream_frame(text=seg))
            return frames
        if event_type == 'agent.tool.calls':
            stream_state['round_index'] += 1
            stream_state['last_tool_calls'] = _normalize_stream_tool_calls(
                list(_agent_event_value(event, 'tool_calls', []) or []),
                stream_state['round_index'],
            )
            frame = _format_tool_stream_frame({
                'preview_text': query,
                'tool_calls': stream_state['last_tool_calls'],
                'tool_results': [],
            })
            if frame is not None:
                frames.append(frame)
            return frames
        if event_type == 'agent.tool.results':
            frame = _format_tool_stream_frame({
                'preview_text': query,
                'tool_calls': [],
                'tool_results': _normalize_stream_tool_results(
                    list(_agent_event_value(event, 'tool_results', []) or []),
                    list(stream_state['last_tool_calls']),
                ),
            })
            if frame is not None:
                frames.append(frame)
        return frames

    def _worker() -> None:
        lazyllm.globals._init_sid(global_sid)
        lazyllm.locals._init_sid(local_sid)
        set_trace_context(trace_config)
        lazyllm.globals['agentic_config'] = runtime_params
        try:
            context = _build_agentic_run_context(query, stream=True)
            stream_iter = context['react_agent'].stream(
                context['agent_query'],
                llm_chat_history=history,
            )
            while True:
                try:
                    event = next(stream_iter)
                except StopIteration as stop:
                    result = stop.value
                    break
                if closed.is_set():
                    return
                for frame in _agent_event_to_frames(event):
                    event_queue.put({'type': 'frame', 'frame': frame})
            result = _finalize_agentic_run(
                agent_output=result,
                history=history,
                context=context,
                request_global_sid=global_sid,
            )
            if not closed.is_set():
                event_queue.put({'type': 'final', 'result': result})
        except Exception as exc:
            if not closed.is_set():
                event_queue.put(exc)
        finally:
            if not closed.is_set():
                event_queue.put(sentinel)

    worker = threading.Thread(target=_worker, daemon=True)
    worker.start()
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


def agentic_rag(
    params: Dict[str, Any],
) -> Any:
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

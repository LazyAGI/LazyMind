from __future__ import annotations

from typing import Any

from lazyllm.tracing.datamodel.structured import ExecutionStep
from lazyllm.tracing.semantics import SemanticType

from evo.projections.traces.models import TraceIOKind, TraceIOView
from evo.projections.traces.tools import is_tool_node
from evo.projections.traces.values import (
    arguments_summary,
    compact_display_value,
    display_summary,
    drop_empty,
    io,
    looks_internal_call_repr,
    parse_jsonish,
    project_dict,
    raw_projection,
    safe_json,
    string_field,
    text_summary,
    tool_result_summary,
)


def normalize_io(value: Any, *, node: ExecutionStep, direction: str) -> TraceIOView:
    parsed = parse_jsonish(value)
    first = _first_arg(parsed)
    tool_node = is_tool_node(node)

    if direction == 'input' and node.name in {'run_chat_pipeline', 'traced_call'}:
        return _request_context_io(first)
    if direction == 'input' and node.semantic_type == SemanticType.LLM:
        return _llm_input_io(parsed, first)
    if direction == 'input' and node.semantic_type == SemanticType.RETRIEVER:
        return _query_input_io(TraceIOKind.RETRIEVER_QUERY, node.semantic_data, first)
    if direction == 'input' and node.semantic_type == SemanticType.RERANK:
        return _query_input_io(TraceIOKind.RERANK_QUERY, node.semantic_data, first)
    if _is_assistant_message(first):
        return _assistant_message_io(first)
    if _is_assistant_message(parsed):
        return _assistant_message_io(parsed)
    if node.semantic_type == SemanticType.TOOL or node.name == 'ToolManager':
        if direction == 'input' and isinstance(first, list):
            return _tool_call_batch_io(first)
        if direction == 'output' and isinstance(parsed, list):
            return _tool_result_batch_io(parsed)
    if direction == 'input' and tool_node and isinstance(first, dict):
        return _tool_arguments_io(node.name, first)
    if direction == 'output' and tool_node:
        return _tool_output_io(node.name, parsed)
    if direction == 'output' and node.semantic_type == SemanticType.RETRIEVER:
        data = node.semantic_data if isinstance(node.semantic_data, dict) else {}
        return _retriever_result_io(data)
    if direction == 'output' and node.semantic_type == SemanticType.RERANK:
        data = node.semantic_data if isinstance(node.semantic_data, dict) else {}
        return _rerank_result_io(data)
    return _fallback_io(parsed)


def retriever_data(data: dict[str, Any]) -> dict[str, Any]:
    filters = data.get('filters') if isinstance(data.get('filters'), dict) else {}
    return drop_empty({
        'query': data.get('query'),
        'kb_id': filters.get('kb_id'),
        'filters': filters or None,
        'group_name': data.get('group_name'),
        'mode': data.get('mode'),
        'topk': data.get('topk'),
        'node_count': data.get('node_count'),
        'similarity': data.get('similarity'),
        'similarity_cut_off': data.get('similarity_cut_off'),
        'index': data.get('index'),
        'target': data.get('target'),
    })


def rerank_data(data: dict[str, Any]) -> dict[str, Any]:
    return drop_empty({
        'query': data.get('query'),
        'rerank_model': data.get('rerank_model'),
        'candidate_node_count': data.get('candidate_node_count'),
        'node_count': data.get('node_count'),
        'topk': data.get('topk'),
        'output_format': data.get('output_format'),
    })


def _first_arg(value: Any) -> Any:
    if isinstance(value, dict) and isinstance(value.get('args'), list) and value['args']:
        return parse_jsonish(value['args'][0])
    return value


def _request_context_io(value: Any) -> TraceIOView:
    data = value if isinstance(value, dict) else {}
    query = str(data.get('query') or '') if data else ''
    return io(
        TraceIOKind.REQUEST_CONTEXT,
        text_summary(query) or 'request context',
        drop_empty({
            'query': query,
            'dataset': string_field(data, 'dataset'),
            'stream': data.get('stream'),
            'use_memory': data.get('use_memory'),
            'filters': project_dict(data.get('filters')),
            'available_tools': data.get('available_tools')
            if isinstance(data.get('available_tools'), list)
            else None,
            'available_skill_count': len(data.get('available_skills') or []),
            'file_count': len(data.get('files') or []),
            'image_file_count': len(data.get('image_files') or []),
        }),
    )


def _llm_input_io(raw_input: Any, first: Any) -> TraceIOView:
    kwargs = raw_input.get('kwargs') if isinstance(raw_input, dict) else {}
    kwargs = kwargs if isinstance(kwargs, dict) else {}
    if isinstance(first, dict) and isinstance(first.get('input'), list):
        messages = [
            raw_projection(message)
            for message in first['input']
            if isinstance(message, dict)
        ]
        tool_count = sum(1 for item in messages if item.get('role') == 'tool')
        summary = (
            f'{len(messages)} messages'
            if tool_count != len(messages)
            else f'{tool_count} tool messages'
        )
        return io(
            TraceIOKind.LLM_MESSAGES,
            summary,
            drop_empty({
                'message_count': len(messages),
                'tool_message_count': tool_count,
                'messages': messages,
            }),
        )
    prompt = first
    if isinstance(first, dict):
        prompt = first.get('query') or first.get('prompt') or safe_json(first)
    prompt_text = str(prompt or '')
    return io(
        TraceIOKind.LLM_PROMPT,
        text_summary(prompt_text) or 'llm prompt',
        drop_empty({
            'prompt': text_summary(prompt_text, limit=2000),
            'history_count': len(kwargs.get('llm_chat_history') or []),
        }),
    )


def _query_input_io(kind: TraceIOKind, semantic_data: Any, first: Any) -> TraceIOView:
    data = semantic_data if isinstance(semantic_data, dict) else {}
    query = data.get('query') or (first if isinstance(first, str) else None)
    if isinstance(query, str) and looks_internal_call_repr(query):
        query = None
    return io(
        kind,
        text_summary(query) if query else kind.value,
        drop_empty({
            'query': text_summary(query, limit=1000) if query is not None else None,
            'filters': project_dict(data.get('filters')),
            'topk': data.get('topk'),
            'group_name': string_field(data, 'group_name'),
            'mode': string_field(data, 'mode'),
            'rerank_model': string_field(data, 'rerank_model'),
        }),
    )


def _assistant_message_io(message: dict[str, Any]) -> TraceIOView:
    calls = _tool_call_data(message.get('tool_calls') or [])
    content = str(message.get('content') or '')
    reasoning = str(message.get('reasoning_content') or '')
    suffix = f', {len(calls)} tool calls' if calls else ''
    return io(
        TraceIOKind.ASSISTANT_MESSAGE,
        f'assistant message{suffix}',
        drop_empty({
            'role': string_field(message, 'role'),
            'content': text_summary(content, limit=2000),
            'reasoning_content': text_summary(reasoning, limit=2000),
            'tool_call_count': len(calls),
            'tool_calls': calls,
        }),
    )


def _tool_call_batch_io(calls: list[Any]) -> TraceIOView:
    items = _tool_call_data(calls)
    names = [
        str(function.get('name'))
        for item in items
        for function in [item.get('function') if isinstance(item.get('function'), dict) else {}]
        if function.get('name')
    ]
    return io(
        TraceIOKind.TOOL_CALL_BATCH,
        f'{len(items)} tool calls' + (f': {", ".join(names)}' if names else ''),
        drop_empty({'tool_call_count': len(items), 'tool_calls': items}),
    )


def _tool_result_batch_io(results: list[Any]) -> TraceIOView:
    items: list[dict[str, Any]] = []
    success_count = 0
    error_count = 0
    for result in results:
        parsed = parse_jsonish(result)
        if isinstance(parsed, dict):
            if parsed.get('success') is True or parsed.get('status') == 'ok':
                success_count += 1
            if parsed.get('success') is False or parsed.get('status') == 'error' or parsed.get('error'):
                error_count += 1
            items.append(raw_projection(parsed))
        else:
            items.append({'value': raw_projection(parsed)})
    summary = f'{len(items)} results, {success_count} success'
    if error_count:
        summary += f', {error_count} error'
    return io(
        TraceIOKind.TOOL_RESULT_BATCH,
        summary,
        drop_empty({
            'result_count': len(items),
            'success_count': success_count,
            'error_count': error_count,
            'results': items,
        }),
    )


def _tool_arguments_io(tool_name: str, arguments: dict[str, Any]) -> TraceIOView:
    payload = raw_projection(arguments)
    summary = arguments_summary(payload)
    return io(TraceIOKind.TOOL_ARGUMENTS, summary or f'{tool_name} arguments', payload)


def _tool_output_io(tool_name: str, value: Any) -> TraceIOView:
    if not isinstance(value, dict):
        return _fallback_io(value)
    if value.get('success') is False or value.get('status') == 'error' or value.get('error'):
        return io(
            TraceIOKind.TOOL_ERROR,
            str(value.get('error') or value.get('reason') or 'tool error'),
            raw_projection(value),
        )
    if tool_name == 'calculator':
        expression = value.get('expression')
        result = value.get('result')
        summary = (
            f'{expression} = {result}'
            if expression is not None and result is not None
            else tool_result_summary(value)
        )
        return io(TraceIOKind.CALCULATOR_RESULT, summary, raw_projection(value))
    if tool_name.startswith('kb_') or tool_name == 'kb_search':
        return _result_collection_io(TraceIOKind.KB_SEARCH_RESULT, value)
    if tool_name in {'web_search', 'arxiv_search'}:
        return _result_collection_io(TraceIOKind.WEB_SEARCH_RESULT, value)
    return io(TraceIOKind.TOOL_RESULT, tool_result_summary(value), raw_projection(value))


def _result_collection_io(kind: TraceIOKind, value: dict[str, Any]) -> TraceIOView:
    payload = raw_projection(value)
    result = payload.get('result') if isinstance(payload.get('result'), dict) else payload
    items = result.get('items') if isinstance(result.get('items'), list) else []
    total = result.get('total', len(items))
    return io(kind, f'{total} results', payload)


def _retriever_result_io(data: dict[str, Any]) -> TraceIOView:
    payload = retriever_data(data)
    scores = data.get('scores') if isinstance(data.get('scores'), list) else []
    node_ids = data.get('returned_node_ids')
    node_ids = node_ids if isinstance(node_ids, list) else []
    if scores:
        payload['scores'] = raw_projection(scores)
    if node_ids:
        payload['returned_node_ids'] = raw_projection(node_ids)
    count = payload.get('node_count')
    group = payload.get('group_name')
    summary = (
        f'{group + " " if group else ""}retriever returned {count} nodes'
        if count is not None
        else 'retriever result'
    )
    return io(TraceIOKind.RETRIEVER_RESULT, summary, payload)


def _rerank_result_io(data: dict[str, Any]) -> TraceIOView:
    payload = rerank_data(data)
    scores = data.get('scores') if isinstance(data.get('scores'), list) else []
    if scores:
        payload['scores'] = raw_projection(scores)
        payload['score_count'] = len(scores)
    return io(
        TraceIOKind.RERANK_RESULT,
        f'{len(scores)} scores by {payload.get("rerank_model")}' if scores else 'rerank result',
        payload,
    )


def _fallback_io(value: Any) -> TraceIOView:
    parsed = parse_jsonish(value)
    if parsed is None:
        return io(TraceIOKind.NULL, '')
    if (
        isinstance(parsed, dict)
        and isinstance(parsed.get('args'), list)
        and isinstance(parsed.get('kwargs'), dict)
    ):
        return _call_input_io(parsed)
    if isinstance(parsed, str) and looks_internal_call_repr(parsed):
        return io(TraceIOKind.CALL_INPUT, 'internal call input')
    if isinstance(parsed, dict):
        return _object_io(parsed)
    if isinstance(parsed, list):
        return _list_io(parsed)
    if isinstance(parsed, bool):
        kind = TraceIOKind.BOOLEAN
    elif isinstance(parsed, (int, float)):
        kind = TraceIOKind.NUMBER
    else:
        kind = TraceIOKind.STRING
    return io(kind, text_summary(str(parsed)), {'value': compact_display_value(parsed)})


def _call_input_io(value: dict[str, Any]) -> TraceIOView:
    display_value = _call_display_value(value)
    summary = display_summary(display_value)
    payload = _call_input_data(display_value)
    return io(TraceIOKind.CALL_INPUT, summary or 'call input', payload)


def _tool_call_data(calls: Any) -> list[dict[str, Any]]:
    if not isinstance(calls, list):
        return []
    return [
        projected
        for call in calls
        if isinstance(call, dict)
        for projected in [raw_projection(call)]
        if isinstance(projected, dict)
    ]


def _call_display_value(value: dict[str, Any]) -> Any:
    args = value.get('args') if isinstance(value.get('args'), list) else []
    kwargs = value.get('kwargs') if isinstance(value.get('kwargs'), dict) else {}
    if args:
        return parse_jsonish(args[0])
    if 'x' in kwargs:
        return parse_jsonish(kwargs['x'])
    if len(kwargs) == 1:
        return parse_jsonish(next(iter(kwargs.values())))
    return kwargs


def _call_input_data(value: Any) -> dict[str, Any]:
    if isinstance(value, dict):
        payload = drop_empty({
            'argument': compact_display_value(value.get('value')) if 'value' in value else None,
            'query': string_field(value, 'query'),
            'expression': string_field(value, 'expression'),
            'keyword': string_field(value, 'keyword'),
            'url': string_field(value, 'url'),
            'filters': project_dict(value.get('filters')),
        })
        return payload or raw_projection(value)
    return drop_empty({'argument': compact_display_value(value)})


def _object_io(value: dict[str, Any], *, limit: int = 12) -> TraceIOView:
    items: list[dict[str, Any]] = []
    for key, item in list(value.items())[:limit]:
        items.append(drop_empty({
            'name': str(key),
            'summary': display_summary(item),
            'value': compact_display_value(item),
        }))
    return io(
        TraceIOKind.OBJECT,
        display_summary(value) or f'{len(value)} fields',
        drop_empty({
            'field_count': len(value),
            'truncated_count': max(len(value) - limit, 0) or None,
            'fields': items,
        }),
    )


def _list_io(values: list[Any], *, limit: int = 12) -> TraceIOView:
    items = [
        drop_empty({
            'index': idx,
            'summary': display_summary(item),
            'value': compact_display_value(item),
        })
        for idx, item in enumerate(values[:limit])
    ]
    return io(
        TraceIOKind.LIST,
        f'{len(values)} items',
        drop_empty({
            'item_count': len(values),
            'truncated_count': max(len(values) - limit, 0) or None,
            'values': items,
        }),
    )


def _is_assistant_message(value: Any) -> bool:
    return isinstance(value, dict) and (
        value.get('role') == 'assistant' or 'tool_calls' in value or 'reasoning_content' in value
    )

from __future__ import annotations

from dataclasses import asdict
from typing import Any

from lazyllm.tracing.datamodel.structured import ExecutionStep, StructuredTrace
from lazyllm.tracing.semantics import SemanticType

from evo.projections.traces.models import TraceNodeView, TraceView
from evo.projections.traces.io import normalize_io, rerank_data, retriever_data
from evo.projections.traces.tools import is_tool_node, tool_metadata
from evo.projections.traces.values import drop_empty, parse_jsonish, pick


def build_trace_view(trace: StructuredTrace | None) -> TraceView | None:
    if trace is None or trace.execution_tree is None:
        return None
    return TraceView(
        trace_id=trace.trace_id,
        metadata=_trace_metadata(trace),
        root=_view_root(trace.execution_tree),
    )


def _trace_metadata(trace: StructuredTrace) -> dict[str, Any]:
    metadata = asdict(trace.metadata)
    nested = metadata.pop('metadata', None)
    if isinstance(nested, dict):
        metadata.update(nested)
    return drop_empty(metadata)


def _view_root(root: ExecutionStep) -> TraceNodeView:
    view = _view_node(root)
    rounds = [_view_node(node) for node in _find_round_nodes(root)]
    if rounds:
        view.children = rounds
    return view


def _find_round_nodes(root: ExecutionStep) -> list[ExecutionStep]:
    rounds: list[ExecutionStep] = []
    stack = [root]
    while stack:
        node = stack.pop()
        if node.name == '_StreamingFunctionCall':
            rounds.append(node)
            continue
        stack.extend(reversed(node.children))
    return rounds


def _view_node(node: ExecutionStep) -> TraceNodeView:
    children_source = _merged_direct_children(node)
    raw_data = node.raw_data
    return TraceNodeView(
        id=node.step_id,
        name=node.name,
        type=str(node.semantic_type or node.node_type or 'unknown'),
        status=node.status,
        start_time=node.start_time,
        end_time=node.end_time,
        input=normalize_io(raw_data.input if raw_data else None, node=node, direction='input'),
        output=normalize_io(raw_data.output if raw_data else None, node=node, direction='output'),
        metadata=_node_metadata(node),
        children=_view_child_list(children_source),
    )


def _merged_direct_children(node: ExecutionStep) -> list[ExecutionStep]:
    children = list(node.children)
    if (
        len(children) == 1
        and children[0].name == 'Pipeline'
        and (node.name == '_StreamingFunctionCall' or is_tool_node(node))
    ):
        return list(children[0].children)
    return children


def _view_child_list(children: list[ExecutionStep]) -> list[TraceNodeView]:
    view: list[TraceNodeView] = []
    for child in children:
        if child.name == 'IFS':
            view.extend(_view_child_list(list(child.children)))
        elif child.name == '_post_action' and not child.children:
            continue
        elif child.name == '_post_action':
            view.extend(_view_post_action(child))
        elif child.name == '_safe_call':
            target = child.children[0] if len(child.children) == 1 else child
            view.append(_view_node(target))
        else:
            view.append(_view_node(child))
    return view


def _view_post_action(node: ExecutionStep) -> list[TraceNodeView]:
    if len(node.children) != 1 or node.children[0].name != 'ToolManager':
        return [_view_node(node)]
    manager = node.children[0]
    manager_children = list(manager.children)
    if len(manager_children) == 1 and manager_children[0].name == 'Diverter':
        manager_children = list(manager_children[0].children)
    view = _view_node(manager)
    view.children = _view_child_list(manager_children)
    return [view]


def _node_metadata(node: ExecutionStep) -> dict[str, Any]:
    semantic = node.semantic_type
    data = node.semantic_data if isinstance(node.semantic_data, dict) else {}
    metadata: dict[str, Any] = {}
    if semantic == SemanticType.LLM:
        usage = data.get('usage') if isinstance(data.get('usage'), dict) else {}
        metadata.update(pick(data, 'model_name', 'stream', 'temperature', 'top_p', 'max_tokens', 'answer_length'))
        metadata.update(pick(usage, 'input_tokens', 'output_tokens', 'total_tokens'))
    elif semantic == SemanticType.RETRIEVER:
        metadata.update({key: value for key, value in retriever_data(data).items() if key != 'filters'})
    elif semantic == SemanticType.RERANK:
        metadata.update(rerank_data(data))
    elif semantic == SemanticType.TOOL or is_tool_node(node):
        output = parse_jsonish(node.raw_data.output if node.raw_data else None)
        metadata.update(tool_metadata(output, fallback_tool=node.name))
    elif semantic == SemanticType.WORKFLOW_CONTROL:
        metadata.update({'control_type': node.name, 'child_count': len(node.children)})
    elif semantic == SemanticType.AGENT:
        metadata.update({'agent_name': node.name, 'child_count': len(node.children)})
    if node.error_message:
        metadata['error_message'] = node.error_message
    return drop_empty(metadata)


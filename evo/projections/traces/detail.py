from __future__ import annotations

import dataclasses
import os
import time
from dataclasses import asdict
from typing import Any, Callable, Iterator

from lazyllm.tracing.consume import get_single_trace
from lazyllm.tracing.consume.reconstruction.extractors.utils import parse_jsonish, query_from_input
from lazyllm.tracing.datamodel.structured import ExecutionStep, RawData, StructuredTrace, TraceMetadata
from lazyllm.tracing.semantics import SemanticType

from evo.projections.traces.models import TraceCompareView, TraceDetailView, TraceSummaryView
from evo.projections.traces.tree import build_trace_view


TraceArtifactLoader = Callable[[str], Any]


def build_trace_detail_view(trace_id: str, load_artifact: TraceArtifactLoader | None = None) -> dict:
    return asdict(_build_trace_detail(trace_id, load_artifact))


def build_trace_compare_view(a: str, b: str, load_artifact: TraceArtifactLoader | None = None) -> dict:
    return asdict(_build_trace_compare(a, b, load_artifact))


def _build_trace_detail(trace_id: str, load_artifact: TraceArtifactLoader | None) -> TraceDetailView:
    trace = _load_trace(trace_id, load_artifact)
    trace_status = 'success' if trace else 'trace_missing'
    return TraceDetailView(
        trace_id=trace_id,
        trace_status=trace_status,
        query=_trace_query(trace.execution_tree.raw_data if trace and trace.execution_tree else None),
        summary=_trace_summary(trace, trace_status),
        trace=build_trace_view(trace),
    )


def _build_trace_compare(a: str, b: str, load_artifact: TraceArtifactLoader | None) -> TraceCompareView:
    side_a = _build_trace_detail(a, load_artifact)
    side_b = _build_trace_detail(b, load_artifact)
    return TraceCompareView(query=side_a.query or side_b.query, a=side_a, b=side_b)


def _load_trace(trace_id: str, load_artifact: TraceArtifactLoader | None) -> StructuredTrace | None:
    trace_id = str(trace_id or '').strip()
    if not trace_id:
        return None
    if load_artifact is not None:
        for artifact_id in (f'trace_{trace_id}', trace_id):
            try:
                trace = _structured_trace_from_payload(load_artifact(artifact_id))
            except Exception:
                trace = None
            if trace:
                return trace
    backend = os.getenv('LAZYLLM_TRACE_CONSUME_BACKEND') or os.getenv('LAZYLLM_TRACE_BACKEND') or None
    for _ in range(3):
        try:
            trace = get_single_trace(trace_id, backend=backend) if backend else get_single_trace(trace_id)
        except Exception:
            trace = None
        if isinstance(trace, StructuredTrace):
            return trace
        converted = _structured_trace_from_payload(trace)
        if converted:
            return converted
        time.sleep(0.2)
    return None


def _structured_trace_from_payload(payload: Any) -> StructuredTrace | None:
    if isinstance(payload, StructuredTrace):
        return payload
    if dataclasses.is_dataclass(payload):
        payload = asdict(payload)
    if not isinstance(payload, dict):
        return None
    root = payload.get('execution_tree') or payload.get('root')
    if not isinstance(root, dict):
        return None
    metadata = payload.get('metadata') if isinstance(payload.get('metadata'), dict) else {}
    trace_id = str(payload.get('trace_id') or metadata.get('trace_id') or '')
    try:
        return StructuredTrace(
            trace_id=trace_id,
            metadata=_trace_metadata(metadata),
            execution_tree=_execution_step(root),
        )
    except Exception:
        return None


def _trace_metadata(value: dict[str, Any]) -> TraceMetadata:
    nested = value.get('metadata') if isinstance(value.get('metadata'), dict) else {}
    return TraceMetadata(
        name=_optional_str(value.get('name')),
        start_time=_float(value.get('start_time')),
        end_time=_optional_float(value.get('end_time')),
        latency_ms=_optional_float(value.get('latency_ms')),
        status=str(value.get('status') or 'unknown'),
        error_message=_optional_str(value.get('error_message')),
        tags=value.get('tags') if isinstance(value.get('tags'), list) else [],
        session_id=_optional_str(value.get('session_id')),
        user_id=_optional_str(value.get('user_id')),
        metadata=dict(nested),
    )


def _execution_step(value: dict[str, Any]) -> ExecutionStep:
    raw = value.get('raw_data') if isinstance(value.get('raw_data'), dict) else {}
    semantic_data = value.get('semantic_data') if isinstance(value.get('semantic_data'), dict) else None
    children = value.get('children') if isinstance(value.get('children'), list) else []
    return ExecutionStep(
        step_id=str(value.get('step_id') or value.get('node_id') or ''),
        name=str(value.get('name') or ''),
        node_type=str(value.get('node_type') or value.get('type') or ''),
        semantic_type=_optional_str(value.get('semantic_type')),
        status=str(value.get('status') or ''),
        start_time=_float(value.get('start_time')),
        end_time=_optional_float(value.get('end_time')),
        latency_ms=_optional_float(value.get('latency_ms')),
        raw_data=RawData(input=raw.get('input'), output=raw.get('output')),
        semantic_data=semantic_data,
        error_message=_optional_str(value.get('error_message')),
        children=[_execution_step(child) for child in children if isinstance(child, dict)],
    )


def _trace_query(raw_data: RawData | None) -> str:
    inputs = parse_jsonish(raw_data.input) if raw_data else None
    query = query_from_input(inputs)
    return query if isinstance(query, str) else ''


def _trace_summary(trace: StructuredTrace | None, trace_status: str) -> TraceSummaryView:
    tree = trace.execution_tree if trace else None
    metadata = trace.metadata if trace else None
    nodes = list(_walk_tree(tree)) if tree else []
    status = str((metadata.status if metadata else '') or (tree.status if tree else '') or trace_status)
    return TraceSummaryView(
        status=status,
        latency_ms=metadata.latency_ms if metadata else None,
        round_count=_agent_round_count(tree),
        tool_call_count=_tool_call_count(nodes),
        retrieval_count=sum(1 for node in nodes if node.semantic_type == SemanticType.RETRIEVER),
        rerank_count=sum(1 for node in nodes if node.semantic_type == SemanticType.RERANK),
    )


def _walk_tree(node: ExecutionStep | None) -> Iterator[ExecutionStep]:
    if node is None:
        return
    yield node
    for child in node.children:
        yield from _walk_tree(child)


def _agent_round_count(root: ExecutionStep | None) -> int:
    def visit(node: ExecutionStep, in_agent: bool) -> int:
        semantic_type = node.semantic_type
        in_agent = in_agent or semantic_type == SemanticType.AGENT
        if in_agent and semantic_type in (SemanticType.TOOL, SemanticType.RETRIEVER, SemanticType.RERANK):
            return 0
        return int(in_agent and semantic_type == SemanticType.LLM) + sum(visit(child, in_agent) for child in node.children)

    return visit(root, False) if root else 0


def _tool_call_count(nodes: list[ExecutionStep]) -> int:
    count = 0
    for node in nodes:
        if node.semantic_type != SemanticType.TOOL:
            continue
        raw_input = parse_jsonish(node.raw_data.input)
        args = raw_input.get('args') if isinstance(raw_input, dict) else None
        tool_calls = args[0] if isinstance(args, list) and args else None
        count += len(tool_calls) if isinstance(tool_calls, list) else 1
    return count


def _optional_str(value: Any) -> str | None:
    return str(value) if value not in (None, '') else None


def _float(value: Any) -> float:
    out = _optional_float(value)
    return out if out is not None else 0.0


def _optional_float(value: Any) -> float | None:
    if value in (None, ''):
        return None
    try:
        return float(value)
    except (TypeError, ValueError):
        return None

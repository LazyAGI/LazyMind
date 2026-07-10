from __future__ import annotations

import re
import time
from collections import Counter
from collections.abc import Mapping
from typing import Any

import networkx as nx

TRACE_READ_ATTEMPTS = 3
TRACE_RETRY_SECONDS = 3.0

STAGE_RULES = (
    ('query_rewrite', ('rewrite', 'rephrase', 'query_transform', '改写')),
    ('retrieve', ('retriever', 'retrieve', 'retriev', 'search', 'kb', 'knowledge', 'vector', 'bm25', '检索')),
    ('rerank', ('rerank', 'rank', '重排')),
    ('context_assembly', ('context', 'context_build', 'assemble', 'budget', '上下文')),
    ('prompt_build', ('prompt', 'template', 'instruction', 'slot', '提示词')),
    ('tool_call', ('tool', 'function', 'call', '工具')),
    ('llm_generate', ('llm', 'chat', 'generate', 'completion', 'model', '大模型')),
    ('postprocess', ('post', 'parse', 'format', 'normalize', 'clean', 'serialization', '后处理')),
    ('stream', ('stream', 'sse', 'chunk', '流式')),
)
DOC_KEYS = {
    'candidate_doc_ids', 'doc_id', 'doc_ids', 'doc_ref', 'doc_refs', 'docid',
    'document_id', 'document_ids', 'file_id', 'file_ids', 'ranked_doc_ids',
    'source_id', 'source_ids',
}
CHUNK_KEYS = {
    'chunk_id', 'chunk_ids', 'node_id', 'node_ids', 'returned_node_ids',
    'segment_id', 'segment_ids', 'segement_id', 'source_unit_ref',
    'source_unit_refs', 'uid', 'uids',
}
ID_KEYS = DOC_KEYS | CHUNK_KEYS
DIAGNOSTIC_STAGES = {stage for stage, _ in STAGE_RULES}


def build_trace_summary(case: Mapping[str, Any], answer: Mapping[str, Any]) -> dict[str, Any]:
    case_id = _text(case.get('id') or answer.get('case_id'))
    trace_id = _text(answer.get('trace_id'))
    if not trace_id:
        return _trace_unavailable(case_id, trace_id, 'eval.rag_answer trace_id missing')
    from lazyllm.tracing.consume import get_single_trace

    last_error: Exception | None = None
    for attempt in range(TRACE_READ_ATTEMPTS):
        try:
            trace = get_single_trace(trace_id)
            break
        except Exception as exc:
            last_error = exc
            if attempt + 1 < TRACE_READ_ATTEMPTS:
                time.sleep(TRACE_RETRY_SECONDS)
    else:
        return _trace_unavailable(
            case_id,
            trace_id,
            f'lazyllm.get_single_trace failed after {TRACE_READ_ATTEMPTS} attempts: {last_error}',
        )
    root = getattr(trace, 'execution_tree', None)
    if root is None:
        return _trace_unavailable(case_id, trace_id, 'lazyllm trace has no execution_tree')
    graph = nx.DiGraph()
    nodes: list[dict[str, Any]] = []
    edges: list[tuple[str, str]] = []
    _walk(root, '', graph, nodes, edges)
    if not nodes:
        return _trace_unavailable(case_id, trace_id, 'lazyllm trace produced no nodes')
    _set_exclusive_latency(graph, nodes)
    diagnostic = [node for node in nodes if node['stage'] in DIAGNOSTIC_STAGES]
    stage_sequence = [node['stage'] for node in nodes]
    diagnostic_sequence = [node['stage'] for node in diagnostic]
    stage_counts = Counter(stage_sequence)
    latency_by_stage = _latency_by_stage(diagnostic)
    ok_status = {'ok', 'success', 'done', 'completed', 'finished'}
    error_nodes = [
        node for node in nodes
        if node['error'] or node['status'] not in ok_status
    ]
    unknown_error_count = sum(1 for node in error_nodes if node['stage'] == 'unknown')
    retrieval = _retrieval_artifacts(nodes)
    final_doc_ids, final_chunk_ids = _final_context_ids(nodes)
    features = _features(graph, nodes, diagnostic, stage_counts, latency_by_stage, error_nodes, retrieval)
    return {
        'case_id': case_id,
        'trace_id': trace_id,
        'trace_source': 'lazyllm.get_single_trace',
        'route_signature': '>'.join(diagnostic_sequence) or 'unknown',
        'tree_text': _tree_text(root),
        'execution_tree': _step_payload(root),
        'stage_sequence': stage_sequence,
        'diagnostic_stage_sequence': diagnostic_sequence,
        'unknown_stage_count': unknown_error_count,
        'edges': [{'source': source, 'target': target} for source, target in edges],
        'critical_path': _critical_path(graph, nodes[0]['id']),
        'bottleneck_stage': max(latency_by_stage, key=latency_by_stage.get) if latency_by_stage else '',
        'stages': nodes,
        'stage_counts': dict(stage_counts),
        'latency_by_stage': latency_by_stage,
        'error_stages': [{'id': n['id'], 'stage': n['stage'], 'name': n['name'], 'status': n['status'],
                          'error': n['error']} for n in error_nodes],
        'retrieval_steps': retrieval['steps'],
        'retrieved_doc_ids': _unique(retrieval['doc_ids']),
        'retrieved_chunk_ids': _unique(retrieval['chunk_ids']),
        'final_context_doc_ids': _unique(final_doc_ids),
        'final_context_chunk_ids': _unique(final_chunk_ids),
        'semantic_metric_keys': sorted(retrieval['semantic_metric_keys']),
        'features': features,
    }


def _trace_unavailable(case_id: str, trace_id: str, reason: str) -> dict[str, Any]:
    error = {
        'id': 'trace',
        'stage': 'trace',
        'name': 'lazyllm.get_single_trace',
        'status': 'unavailable',
        'error': _preview(reason),
    }
    return {
        'case_id': case_id,
        'trace_id': trace_id,
        'trace_source': 'lazyllm.get_single_trace',
        'trace_status': 'unavailable',
        'route_signature': 'trace_unavailable',
        'tree_text': '',
        'execution_tree': {},
        'stage_sequence': [],
        'diagnostic_stage_sequence': [],
        'unknown_stage_count': 0,
        'edges': [],
        'critical_path': [],
        'bottleneck_stage': '',
        'stages': [],
        'stage_counts': {},
        'latency_by_stage': {},
        'error_stages': [error],
        'retrieval_steps': [],
        'retrieved_doc_ids': [],
        'retrieved_chunk_ids': [],
        'final_context_doc_ids': [],
        'final_context_chunk_ids': [],
        'semantic_metric_keys': [],
        'features': {
            'node_count': 0.0,
            'edge_count': 0.0,
            'max_depth': 0.0,
            'branching_factor_avg': 0.0,
            'error_span_count': 1.0,
            'trace_latency_ms': 0.0,
            'exclusive_latency_ms': 0.0,
            'retrieved_doc_count': 0.0,
            'retrieved_chunk_count': 0.0,
            'trace_unavailable': 1.0,
        },
    }


def _walk(step: Any, parent_id: str, graph: nx.DiGraph, nodes: list[dict[str, Any]],
          edges: list[tuple[str, str]], depth: int = 0) -> None:
    node_id = _required_text(getattr(step, 'step_id', ''), 'trace step_id')
    stage = _stage(step)
    semantic = _semantic_data(step)
    node = {
        'id': node_id,
        'span_id': node_id,
        'parent_id': parent_id,
        'name': _text(getattr(step, 'name', ''))[:160],
        'stage': stage,
        'node_type': _text(getattr(step, 'node_type', ''))[:80],
        'semantic_type': _text(getattr(step, 'semantic_type', ''))[:80],
        'status': _required_text(getattr(step, 'status', ''), f'trace status for {node_id}').lower(),
        'start_time': _number_required(
            getattr(step, 'start_time', None),
            f'trace start_time for {node_id}',
        ),
        'end_time': _optional_number(getattr(step, 'end_time', None)),
        'latency_ms': _latency(step, node_id),
        'exclusive_latency_ms': 0.0,
        'depth': depth,
        'error': _text(getattr(step, 'error_message', ''))[:300],
        'semantic_metrics': semantic,
        'raw_data': _raw_data(step),
    }
    graph.add_node(node_id, **node)
    nodes.append(node)
    if parent_id:
        graph.add_edge(parent_id, node_id)
        edges.append((parent_id, node_id))
    for child in getattr(step, 'children', None) or []:
        _walk(child, node_id, graph, nodes, edges, depth + 1)


def _stage(step: Any) -> str:
    name = _text(getattr(step, 'name', '')).lower()
    if name.startswith('get_') and 'toolgroup_methods' in name:
        return 'tool_call'
    fields = ' '.join(_text(value).lower() for value in (
        getattr(step, 'semantic_type', ''), getattr(step, 'node_type', ''), getattr(step, 'name', ''),
    ))
    return next((stage for stage, needles in STAGE_RULES if any(needle in fields for needle in needles)), 'unknown')


def _semantic_data(step: Any) -> dict[str, Any]:
    value = getattr(step, 'semantic_data', None)
    if not isinstance(value, Mapping):
        return {}
    doc_ids, chunk_ids = _extract_ids(value)
    scores = [
        float(item)
        for item in (value.get('scores') or [])
        if isinstance(item, (int, float))
    ]
    return {
        'doc_ids': _unique(doc_ids + _list(value.get('ranked_doc_ids')) + _list(value.get('candidate_doc_ids'))),
        'chunk_ids': _unique(chunk_ids + _list(value.get('returned_node_ids'))),
        'scores': scores[:20],
        'node_count': _number(value.get('node_count') or value.get('candidate_node_count')),
        'keys': sorted(str(key) for key in value),
    }


def _extract_ids(value: Any) -> tuple[list[str], list[str]]:
    docs: list[str] = []
    chunks: list[str] = []
    stack = [value]
    while stack:
        item = stack.pop()
        if isinstance(item, Mapping):
            for key, raw in item.items():
                lowered = str(key).lower()
                if lowered in DOC_KEYS:
                    docs.extend(_list(raw))
                elif lowered in CHUNK_KEYS:
                    chunks.extend(_list(raw))
                elif lowered in ID_KEYS or isinstance(raw, (Mapping, list, tuple, set)):
                    stack.append(raw)
        elif isinstance(item, (list, tuple, set)):
            stack.extend(item)
        elif hasattr(item, '__dict__'):
            stack.append(vars(item))
    return _unique(docs), _unique(chunks)


def _set_exclusive_latency(graph: nx.DiGraph, nodes: list[dict[str, Any]]) -> None:
    by_id = {node['id']: node for node in nodes}
    for node in reversed(nodes):
        child_latency = sum(float(by_id[child].get('latency_ms') or 0.0) for child in graph.successors(node['id']))
        node['exclusive_latency_ms'] = round(max(0.0, float(node.get('latency_ms') or 0.0) - child_latency), 4)


def _critical_path(graph: nx.DiGraph, root_id: str) -> list[str]:
    leaves = [node for node in graph.nodes if graph.out_degree(node) == 0]
    paths = (nx.shortest_path(graph, root_id, leaf) for leaf in leaves if nx.has_path(graph, root_id, leaf))
    path = max(paths, key=lambda p: sum(float(graph.nodes[n].get('exclusive_latency_ms') or 0.0) for n in p),
               default=[root_id])
    return [_text(graph.nodes[node].get('stage')) for node in path]


def _latency_by_stage(nodes: list[dict[str, Any]]) -> dict[str, float]:
    totals: dict[str, float] = {}
    for node in nodes:
        totals[node['stage']] = totals.get(node['stage'], 0.0) + float(node['exclusive_latency_ms'] or 0.0)
    return {stage: round(value, 4) for stage, value in sorted(totals.items())}


def _retrieval_artifacts(nodes: list[dict[str, Any]]) -> dict[str, Any]:
    steps: list[dict[str, Any]] = []
    doc_ids: list[str] = []
    chunk_ids: list[str] = []
    keys: set[str] = set()
    for node in nodes:
        metrics = node.get('semantic_metrics') if isinstance(node.get('semantic_metrics'), Mapping) else {}
        if node['stage'] not in {'retrieve', 'rerank', 'context_assembly'}:
            continue
        step_docs = _unique(metrics.get('doc_ids'))
        step_chunks = _unique(metrics.get('chunk_ids'))
        doc_ids.extend(step_docs)
        chunk_ids.extend(step_chunks)
        keys.update(metrics.get('keys') or [])
        steps.append({'id': node['id'], 'stage': node['stage'], 'name': node['name'],
                      'doc_ids': step_docs, 'chunk_ids': step_chunks,
                      'node_count': metrics.get('node_count', 0.0), 'scores': metrics.get('scores', [])})
    return {'steps': steps, 'doc_ids': _unique(doc_ids), 'chunk_ids': _unique(chunk_ids),
            'semantic_metric_keys': keys}


def _final_context_ids(nodes: list[dict[str, Any]]) -> tuple[list[str], list[str]]:
    selected = [n for n in nodes if n['stage'] in {'context_assembly', 'prompt_build'}]
    docs: list[str] = []
    chunks: list[str] = []
    for node in selected:
        metrics = node.get('semantic_metrics') if isinstance(node.get('semantic_metrics'), Mapping) else {}
        docs.extend(metrics.get('doc_ids') or [])
        chunks.extend(metrics.get('chunk_ids') or [])
    return _unique(docs), _unique(chunks)


def _features(graph: nx.DiGraph, nodes: list[dict[str, Any]], diagnostic: list[dict[str, Any]],
              stage_counts: Counter, latency_by_stage: Mapping[str, float], errors: list[dict[str, Any]],
              retrieval: Mapping[str, Any]) -> dict[str, float]:
    degrees = [graph.out_degree(node) for node in graph.nodes]
    features = {
        'node_count': float(len(diagnostic)),
        'edge_count': float(graph.number_of_edges()),
        'max_depth': float(max((node['depth'] for node in diagnostic), default=0)),
        'branching_factor_avg': round(sum(degrees) / len(degrees), 4) if degrees else 0.0,
        'error_span_count': float(len(errors)),
        'trace_latency_ms': round(sum(float(node.get('exclusive_latency_ms') or 0.0) for node in nodes), 4),
        'exclusive_latency_ms': round(sum(float(node.get('exclusive_latency_ms') or 0.0) for node in nodes), 4),
        'retrieved_doc_count': float(len(retrieval.get('doc_ids') or [])),
        'retrieved_chunk_count': float(len(retrieval.get('chunk_ids') or [])),
    }
    features.update({f'stage_count.{key}': float(value) for key, value in stage_counts.items()})
    features.update({f'latency.{key}': float(value) for key, value in latency_by_stage.items()})
    return features


def _tree_text(step: Any) -> str:
    label = re.sub(r'[^A-Za-z0-9_.-]+', '_', _stage(step)) or 'unknown'
    return '{' + label + ''.join(_tree_text(child) for child in getattr(step, 'children', None) or []) + '}'


def _step_payload(step: Any) -> dict[str, Any]:
    node_id = _required_text(getattr(step, 'step_id', ''), 'trace step_id')
    return {
        'id': node_id,
        'span_id': node_id,
        'name': _text(getattr(step, 'name', ''))[:160],
        'stage': _stage(step),
        'node_type': _text(getattr(step, 'node_type', ''))[:80],
        'semantic_type': _text(getattr(step, 'semantic_type', ''))[:80],
        'status': _required_text(getattr(step, 'status', ''), f'trace status for {node_id}').lower(),
        'start_time': _number_required(
            getattr(step, 'start_time', None),
            f'trace start_time for {node_id}',
        ),
        'end_time': _optional_number(getattr(step, 'end_time', None)),
        'latency_ms': _latency(step, node_id),
        'raw_data': _raw_data(step),
        'semantic_data_keys': sorted(str(key) for key in getattr(step, 'semantic_data', None) or {}),
        'children': [_step_payload(child) for child in getattr(step, 'children', None) or []],
    }


def _raw_data(step: Any) -> dict[str, str]:
    raw = getattr(step, 'raw_data', None)
    return {
        'input': _preview(getattr(raw, 'input', None)),
        'output': _preview(getattr(raw, 'output', None)),
    }


def _list(value: Any) -> list[str]:
    if value is None:
        return []
    if isinstance(value, str):
        return [value] if value.strip() else []
    if isinstance(value, (list, tuple, set)):
        return [str(item).strip() for item in value if str(item or '').strip()]
    return [str(value).strip()] if str(value or '').strip() else []


def _unique(value: Any) -> list[str]:
    return list(dict.fromkeys(_list(value)))


def _text(value: Any) -> str:
    return str(value or '').strip()


def _number(value: Any) -> float:
    try:
        return round(float(value or 0.0), 4)
    except (TypeError, ValueError):
        return 0.0


def _optional_number(value: Any) -> float | None:
    if value is None:
        return None
    try:
        return round(float(value), 4)
    except (TypeError, ValueError):
        return None


def _number_required(value: Any, name: str) -> float:
    try:
        return round(float(value), 4)
    except (TypeError, ValueError):
        raise ValueError(f'{name} is required and must be numeric') from None


def _required_text(value: Any, name: str) -> str:
    text = _text(value)
    if not text:
        raise ValueError(f'{name} is required')
    return text


def _latency(step: Any, node_id: str) -> float:
    value = getattr(step, 'latency_ms', None)
    if value is not None:
        return _number_required(value, f'trace latency_ms for {node_id}')
    start = _number_required(
        getattr(step, 'start_time', None),
        f'trace start_time for {node_id}',
    )
    end = _number_required(
        getattr(step, 'end_time', None),
        f'trace end_time for {node_id}',
    )
    return round(max(0.0, (end - start) * 1000.0), 4)


def _preview(value: Any) -> str:
    return _text(value)[:2000]

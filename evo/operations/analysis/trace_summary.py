from __future__ import annotations

import re
from collections import Counter
from collections.abc import Mapping
from typing import Any

import networkx as nx

from evo.operations.common import as_list, json_safe, text, unique_texts

STAGE_RULES: tuple[tuple[str, tuple[str, ...]], ...] = (
    ('retrieve', ('retriever',)),
    ('rerank', ('rerank',)),
    ('llm_generate', ('llm',)),
    ('tool_call', ('tool',)),
    ('query_rewrite', ('rewrite', 'rephrase', 'query_transform', 'query transform', '改写')),
    ('retrieve', ('retrieve', 'retriev', 'search', 'kb', 'knowledge', 'vector', 'bm25', '召回', '检索')),
    ('rerank', ('rerank', 'rank', '重排')),
    ('prompt_build', ('prompt', 'template', 'context_build', 'build_context', '组装')),
    ('llm_generate', ('llm', 'chat', 'generate', 'completion', 'model', '大模型')),
    ('tool_call', ('tool', 'function', 'call', '工具')),
    ('postprocess', ('post', 'parse', 'format', 'normalize', 'clean', '后处理')),
)

ID_KEYS = (
    'chunk_id',
    'segment_id',
    'segement_id',
    'node_id',
    'uid',
    'source_unit_ref',
    'doc_id',
    'document_id',
    'file_id',
    'docid',
)


def trace_summary(case: Mapping[str, Any], answer: Mapping[str, Any], services: Any) -> dict[str, Any]:
    case_id = text(case.get('id') or answer.get('case_id'))
    trace_id = text(answer.get('trace_id'))
    if not trace_id:
        return _missing(case_id, '', 'trace_id_missing', answer)
    try:
        from lazyllm.tracing.consume import get_single_trace

        trace = get_single_trace(trace_id)
    except Exception as error:  # noqa: BLE001 - analysis records trace collection gaps.
        return _missing(case_id, trace_id, f'{type(error).__name__}: {error}', answer)

    root = getattr(trace, 'execution_tree', None)
    if root is None:
        return _missing(case_id, trace_id, 'execution_tree_missing', answer)
    graph = nx.DiGraph()
    nodes: list[dict[str, Any]] = []
    edges: list[tuple[str, str]] = []
    _walk(root, None, graph, nodes, edges)
    _set_exclusive_latency(graph, nodes)
    stage_sequence = [node['stage'] for node in nodes]
    diagnostic_stage_sequence = [node['stage'] for node in nodes if _diagnostic_node(node)]
    stage_counts = Counter(diagnostic_stage_sequence)
    latency_by_stage = _latency_by_stage([node for node in nodes if _diagnostic_node(node)])
    error_nodes = [node for node in nodes if node['status'] not in {'', 'ok', 'success', 'completed'} or node['error']]
    critical_path = _critical_path(graph, root_id=nodes[0]['id'] if nodes else '')
    retrieval = _retrieval_artifacts(nodes)
    route_signature = '>'.join(diagnostic_stage_sequence) if diagnostic_stage_sequence else 'unknown'
    tree_text = _tree_text(root)
    features = {
        'node_count': len([node for node in nodes if _diagnostic_node(node)]),
        'edge_count': len(edges),
        'max_depth': max((int(node['depth']) for node in nodes if _diagnostic_node(node)), default=0),
        'branching_factor_avg': _branching_factor_avg(graph),
        'error_span_count': len(error_nodes),
        'trace_latency_ms': _number(getattr(getattr(trace, 'metadata', None), 'latency_ms', None)),
        'exclusive_latency_ms': round(sum(float(node['exclusive_latency_ms'] or 0.0) for node in nodes), 4),
        **{f'stage_count.{stage}': count for stage, count in stage_counts.items()},
        **{f'latency.{stage}': value for stage, value in latency_by_stage.items()},
        'retrieved_doc_count': len(retrieval['doc_ids']),
        'retrieved_chunk_count': len(retrieval['chunk_ids']),
    }
    return {
        'case_id': case_id,
        'trace_id': trace_id,
        'trace_available': True,
        'trace_missing_reason': '',
        'route_signature': route_signature,
        'tree_text': tree_text,
        'stage_sequence': stage_sequence,
        'diagnostic_stage_sequence': diagnostic_stage_sequence,
        'edges': [{'source': source, 'target': target} for source, target in edges],
        'critical_path': critical_path,
        'bottleneck_stage': _bottleneck_stage(latency_by_stage),
        'stages': nodes,
        'stage_counts': dict(stage_counts),
        'latency_by_stage': latency_by_stage,
        'error_stages': [
            {
                'id': node['id'],
                'stage': node['stage'],
                'name': node['name'],
                'status': node['status'],
                'error': node['error'],
            }
            for node in error_nodes
        ],
        'retrieval_steps': retrieval['steps'],
        'retrieved_doc_ids': unique_texts([*as_list(answer.get('doc_ids')), *retrieval['doc_ids']]),
        'retrieved_chunk_ids': unique_texts([*as_list(answer.get('chunk_ids')), *retrieval['chunk_ids']]),
        'features': features,
    }


def _missing(case_id: str, trace_id: str, reason: str, answer: Mapping[str, Any]) -> dict[str, Any]:
    return {
        'case_id': case_id,
        'trace_id': trace_id,
        'trace_available': False,
        'trace_missing_reason': reason[:300],
        'route_signature': 'trace_missing',
        'tree_text': '{trace_missing}',
        'stage_sequence': [],
        'diagnostic_stage_sequence': [],
        'edges': [],
        'critical_path': [],
        'bottleneck_stage': '',
        'stages': [],
        'stage_counts': {},
        'latency_by_stage': {},
        'error_stages': [],
        'retrieval_steps': [],
        'retrieved_doc_ids': unique_texts(answer.get('doc_ids')),
        'retrieved_chunk_ids': unique_texts(answer.get('chunk_ids')),
        'features': {'trace_missing': 1.0},
    }


def _walk(step: Any, parent_id: str | None, graph: nx.DiGraph, nodes: list[dict[str, Any]],
          edges: list[tuple[str, str]], depth: int = 0) -> None:
    node_id = text(getattr(step, 'step_id', '')) or f'node_{len(nodes)}'
    name = text(getattr(step, 'name', ''))
    stage = _stage(step)
    node = {
        'id': node_id,
        'parent_id': parent_id or '',
        'name': name[:120],
        'stage': stage,
        'node_type': text(getattr(step, 'node_type', ''))[:80],
        'semantic_type': text(getattr(step, 'semantic_type', ''))[:80],
        'status': text(getattr(step, 'status', '')).lower(),
        'latency_ms': _number(getattr(step, 'latency_ms', None)),
        'exclusive_latency_ms': 0.0,
        'depth': depth,
        'error': text(getattr(step, 'error_message', ''))[:200],
        'semantic_metrics': _semantic_metrics(getattr(step, 'semantic_data', None)),
    }
    graph.add_node(node_id, **node)
    nodes.append(node)
    if parent_id:
        graph.add_edge(parent_id, node_id)
        edges.append((parent_id, node_id))
    for child in getattr(step, 'children', None) or []:
        _walk(child, node_id, graph, nodes, edges, depth + 1)


def _stage(step: Any) -> str:
    fields = ' '.join(
        text(value).lower()
        for value in (
            getattr(step, 'semantic_type', ''),
            getattr(step, 'node_type', ''),
            getattr(step, 'name', ''),
        )
    )
    for stage, needles in STAGE_RULES:
        if any(needle in fields for needle in needles):
            return stage
    return 'unknown'


def _latency_by_stage(nodes: list[dict[str, Any]]) -> dict[str, float]:
    totals: dict[str, float] = {}
    for node in nodes:
        totals[node['stage']] = totals.get(node['stage'], 0.0) + float(node.get('exclusive_latency_ms') or 0.0)
    return {stage: round(value, 4) for stage, value in sorted(totals.items())}


def _set_exclusive_latency(graph: nx.DiGraph, nodes: list[dict[str, Any]]) -> None:
    by_id = {node['id']: node for node in nodes}
    for node in reversed(nodes):
        child_latency = sum(float(by_id[child].get('latency_ms') or 0.0) for child in graph.successors(node['id']))
        node['exclusive_latency_ms'] = round(max(0.0, float(node.get('latency_ms') or 0.0) - child_latency), 4)


def _critical_path(graph: nx.DiGraph, *, root_id: str) -> list[str]:
    if not root_id or root_id not in graph:
        return []
    leaves = [node for node in graph.nodes if graph.out_degree(node) == 0]
    if not leaves:
        return [text(graph.nodes[root_id].get('stage'))]
    paths = (nx.shortest_path(graph, root_id, leaf) for leaf in leaves if nx.has_path(graph, root_id, leaf))
    best = max(
        paths,
        key=lambda path: sum(float(graph.nodes[node].get('exclusive_latency_ms') or 0.0) for node in path),
        default=[],
    )
    return [text(graph.nodes[node].get('stage')) for node in best]


def _branching_factor_avg(graph: nx.DiGraph) -> float:
    degrees = [graph.out_degree(node) for node in graph.nodes]
    return round(sum(degrees) / len(degrees), 4) if degrees else 0.0


def _bottleneck_stage(latency_by_stage: Mapping[str, float]) -> str:
    return max(latency_by_stage.items(), key=lambda item: item[1])[0] if latency_by_stage else ''


def _tree_text(step: Any) -> str:
    label = re.sub(r'[^A-Za-z0-9_.-]+', '_', _stage(step)) or 'unknown'
    return '{' + label + ''.join(_tree_text(child) for child in getattr(step, 'children', None) or []) + '}'


def _retrieval_artifacts(nodes: list[dict[str, Any]]) -> dict[str, Any]:
    steps = []
    doc_ids: list[str] = []
    chunk_ids: list[str] = []
    for node in nodes:
        metrics = node.get('semantic_metrics') if isinstance(node.get('semantic_metrics'), Mapping) else {}
        if node['stage'] not in {'retrieve', 'rerank'} and not metrics:
            continue
        step_doc_ids = unique_texts(metrics.get('doc_ids'))
        step_chunk_ids = unique_texts(metrics.get('chunk_ids'))
        doc_ids.extend(step_doc_ids)
        chunk_ids.extend(step_chunk_ids)
        steps.append({
            'id': node['id'],
            'stage': node['stage'],
            'name': node['name'],
            'doc_ids': step_doc_ids,
            'chunk_ids': step_chunk_ids,
            'node_count': metrics.get('node_count', 0),
            'scores': metrics.get('scores', []),
        })
    return {'steps': steps, 'doc_ids': unique_texts(doc_ids), 'chunk_ids': unique_texts(chunk_ids)}


def _semantic_metrics(value: Any) -> dict[str, Any]:
    if not isinstance(value, Mapping):
        return {}
    doc_ids = unique_texts([
        *as_list(value.get('ranked_doc_ids')),
        *as_list(value.get('candidate_doc_ids')),
    ])
    chunk_ids = unique_texts(value.get('returned_node_ids'))
    nested_doc_ids, nested_chunk_ids = _extract_ids({
        key: value.get(key)
        for key in ('returned_nodes', 'ranked_nodes', 'candidate_nodes')
        if key in value
    })
    doc_ids = unique_texts([*doc_ids, *nested_doc_ids])
    chunk_ids = unique_texts([*chunk_ids, *nested_chunk_ids])
    node_count = value.get('node_count') or value.get('candidate_node_count') or 0
    scores = [float(item) for item in as_list(value.get('scores')) if isinstance(item, (int, float))]
    return {
        'doc_ids': doc_ids,
        'chunk_ids': chunk_ids,
        'node_count': _number(node_count),
        'scores': scores[:20],
    }


def _extract_ids(value: Any) -> tuple[list[str], list[str]]:
    doc_ids: list[str] = []
    chunk_ids: list[str] = []

    def visit(item: Any) -> None:
        if isinstance(item, Mapping):
            for key, raw in item.items():
                key_text = text(key).lower()
                if key_text in {'doc_id', 'document_id', 'file_id', 'docid'}:
                    doc_ids.extend(unique_texts(raw))
                elif key_text in {'chunk_id', 'segment_id', 'segement_id', 'node_id', 'uid', 'source_unit_ref'}:
                    chunk_ids.extend(unique_texts(raw))
                elif key_text in ID_KEYS or isinstance(raw, (Mapping, list, tuple, set)):
                    visit(raw)
        elif isinstance(item, (list, tuple, set)):
            for child in item:
                visit(child)
        elif hasattr(item, '__dict__'):
            visit(json_safe(vars(item)))

    visit(value)
    return unique_texts(doc_ids), unique_texts(chunk_ids)


def _diagnostic_node(node: Mapping[str, Any]) -> bool:
    if text(node.get('node_type')) == 'flow':
        return False
    if text(node.get('semantic_type')) == 'workflow_control':
        return False
    return bool(text(node.get('stage')) and text(node.get('stage')) != 'unknown')


def _number(value: Any) -> float:
    try:
        return round(float(value or 0.0), 4)
    except (TypeError, ValueError):
        return 0.0

from __future__ import annotations

import re
import dataclasses
import os
import time
from dataclasses import dataclass
from typing import Any

from ... import validate_id
from ...runtime import OperationContext
from .utils import jsonish

ID_RE = re.compile(r'doc_[A-Za-z0-9_-]+|(?:chunk|node|seg|segment|uid)_[A-Za-z0-9_-]+'
                   r'|[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}')
NODE_TYPES = {
    ('run_chat_pipeline', 'callable'): 'chat_entry', ('_StreamingReactAgent', 'module'): 'agent',
    ('Loop', 'flow'): 'agent_loop', ('_StreamingFunctionCall', 'module'): 'tool_planner',
    ('Pipeline', 'flow'): 'pipeline', ('_build_history', 'callable'): 'history_builder',
    ('_post_action', 'callable'): 'tool_call_parser', ('ToolManager', 'module'): 'tool_manager',
    ('Diverter', 'flow'): 'tool_router', ('_safe_call', 'callable'): 'tool_call',
    ('kb_search', 'module'): 'kb_search', ('Parallel', 'flow'): 'parallel_retrieval',
    ('parse_query', 'callable'): 'query_parse', ('IFS', 'flow'): 'retrieval_branch',
    ('has_files', 'callable'): 'file_branch_check', ('Retriever', 'module'): 'retriever',
    ('merge_rank_results', 'callable'): 'retrieval_merge', ('_rerank', 'callable'): 'reranker',
    ('merge_text_image_nodes', 'callable'): 'result_merge', ('<lambda>', 'callable'): 'query_passthrough',
}
NODE_KEYS = ('trace_id', 'node_id', 'name', 'node_type', 'status', 'path', 'role', 'kb_search_node_id',
             'kb_search_path')
ID_KEYS = dict.fromkeys(('docid', 'doc_id', 'document_id', 'core_document_id'), 'doc') | dict.fromkeys(
    ('id', 'uid', 'chunk_id', 'segment_id', 'segement_id', 'node_id'), 'chunk')
NAME_KEYS = {'file_id', 'file_name', 'filename', 'display_name'}
HIT_PATHS = {f'retriever_{k}_hits': ('retrievers', f'{k}_hits') for k in ('doc', 'chunk')} | {
    f'rerank_{d}_{k}_hits': ('rerankers', d, f'{k}_hits') for d in ('input', 'output') for k in ('doc', 'chunk')}
STAGE_PATHS = {'retrievers': ('retrievers',), 'merge': ('merge',), 'reranker_input': ('rerankers', 'input'),
               'reranker_output': ('rerankers', 'output')}


@dataclass(frozen=True)
class TraceAccess:
    raw_trace: dict[str, Any]
    trace_id: str
    flat_steps: list[dict[str, Any]]
    raw_node_by_step_id: dict[str, dict[str, Any]]

    def list_trace_steps(self, filters: dict[str, Any] | None = None) -> list[dict[str, Any]]:
        steps = self.flat_steps
        for key in ('role', 'name', 'node_type', 'status') if filters else ():
            vals = _filter_values(filters.get(key) or filters.get(f'{key}s'))
            if vals: steps = [step for step in steps if str(step.get(key) or '') in vals]
        return [dict(step) for step in steps]

    def get_trace_steps(self, selector: dict[str, Any], include_io: bool = True,
                        children_depth: int = 0) -> list[dict[str, Any]]:
        by_id, by_index = {s['step_id']: s for s in self.flat_steps}, {s['index']: s for s in self.flat_steps}
        step_ids = _filter_values(selector.get('step_ids')) | _filter_values(selector.get('step_id'))
        indices = _filter_ints(selector.get('indices')) + _filter_ints(selector.get('index'))
        names = _filter_values(selector.get('names')) | _filter_values(selector.get('name'))
        picked = [by_id[i] for i in step_ids if i in by_id] + [by_index[i] for i in indices if i in by_index]
        picked += [step for step in self.flat_steps if step['name'] in names]
        seen, out = set(), []
        for step in picked:
            depth = min(int(selector.get('children_depth', children_depth) or 0), 1)
            kids = set(step.get('children_step_ids') or []) if depth > 0 else set()
            for item in [step, *(s for s in self.flat_steps if s['step_id'] in kids)]:
                if not item['step_id'] or item['step_id'] in seen: continue
                seen.add(item['step_id'])
                data = dict(item)
                if include_io:
                    raw = self.raw_node_by_step_id.get(item['step_id']) or {}
                    raw_data = raw.get('raw_data') if isinstance(raw.get('raw_data'), dict) else {}
                    data['raw_data'] = {k: jsonish(raw_data.get(k)) for k in ('input', 'output') if k in raw_data}
                out.append(data)
        return out


def load_trace_payload(ctx: OperationContext, trace_id: str, rag: dict[str, Any]) -> dict[str, Any]:
    if isinstance(rag.get('trace'), dict): return _require_trace_payload(rag['trace'], trace_id)
    if not trace_id: raise ValueError('trace_id is required for classification')
    try:
        ref = ctx.artifact_graph.latest_ref(f"trace_{validate_id(trace_id, 'trace_id')}")
        if ctx.artifact_graph.schema_name(ref) == 'Trace':
            payload = ctx.artifact_graph.get(ref)
            if isinstance(payload, dict): return _require_trace_payload(payload, trace_id)
    except (KeyError, ValueError):
        pass
    try:
        from lazyllm.tracing.consume import get_single_trace
    except Exception as exc:
        raise ValueError(f'trace consumer unavailable for {trace_id}') from exc
    for _ in range(8):
        try:
            backend = os.getenv('LAZYLLM_TRACE_CONSUME_BACKEND') or os.getenv('LAZYLLM_TRACE_BACKEND') or 'local'
            trace = get_single_trace(trace_id, backend=backend)
            payload = dataclasses.asdict(trace) if dataclasses.is_dataclass(trace) else trace
            if isinstance(payload, dict): return _require_trace_payload(payload, trace_id)
        except Exception:
            time.sleep(1)
    raise ValueError(f'trace payload not found or unreadable: {trace_id}')


def _require_trace_payload(payload, trace_id):
    root = payload.get('execution_tree') or payload
    if not root or not isinstance(root, dict): raise ValueError(f'trace payload is not an object: {trace_id}')
    return payload


def build_trace_access(trace: dict[str, Any], trace_id: str) -> TraceAccess:
    raw_nodes: dict[str, dict[str, Any]] = {}
    flat: list[dict[str, Any]] = []

    def walk(node, path='execution_tree', parent='', depth=0):
        if not isinstance(node, dict): return
        sid = str(node.get('step_id') or node.get('node_id') or f'step_{len(flat)}')
        raw_nodes[sid] = node
        raw_data = node.get('raw_data') if isinstance(node.get('raw_data'), dict) else {}
        children = [child for child in (node.get('children') or []) if isinstance(child, dict)]
        name, node_type = str(node.get('name') or ''), str(node.get('node_type') or '')
        flat.append({
            'index': len(flat), 'trace_id': trace_id, 'step_id': sid, 'node_id': str(node.get('node_id') or sid),
            'name': name, 'node_type': node_type, 'status': str(node.get('status') or ''), 'path': path,
            'parent_step_id': parent, 'parent_node_id': parent,
            'children_step_ids': [str(child.get('step_id') or child.get('node_id') or '') for child in children],
            'depth': depth, 'role': NODE_TYPES.get((name, node_type), 'unknown'),
            'has_input': 'input' in raw_data, 'has_output': 'output' in raw_data,
            'input_preview': _preview(raw_data.get('input')), 'output_preview': _preview(raw_data.get('output')),
        })
        for index, child in enumerate(children): walk(child, f'{path}.children[{index}]', sid, depth + 1)

    walk(trace.get('execution_tree') or trace)
    if not flat: raise ValueError(f'trace has no readable steps: {trace_id}')
    return TraceAccess(trace, trace_id, flat, raw_nodes)


def flatten_trace(trace: dict[str, Any], trace_id: str) -> list[dict[str, Any]]:
    out: list[dict[str, Any]] = []

    def walk(node, path='execution_tree', parent_id='', parent_path='', kb_path='', kb_id=''):
        if not isinstance(node, dict): return
        raw_data = node.get('raw_data') if isinstance(node.get('raw_data'), dict) else {}
        raw = {key: jsonish(raw_data.get(key)) for key in ('input', 'output') if key in raw_data}
        parts, input_parts, output_parts = ids_in(raw), ids_in(raw.get('input')), ids_in(raw.get('output'))
        name, node_type = str(node.get('name') or ''), str(node.get('node_type') or '')
        role = NODE_TYPES.get((name, node_type), 'unknown')
        node_id = str(node.get('step_id') or node.get('node_id') or '')
        if role == 'kb_search': kb_path, kb_id = path, node_id
        out.append({
            'trace_id': trace_id, 'node_id': node_id, 'parent_node_id': parent_id, 'name': name,
            'node_type': node_type, 'status': str(node.get('status') or ''), 'path': path,
            'parent_path': parent_path, 'role': role, 'kb_search_path': kb_path, 'kb_search_node_id': kb_id,
            'in_kb_search': bool(kb_path), 'raw': raw, **parts,
            **{f'input_{k}': v for k, v in input_parts.items()},
            **{f'output_{k}': v for k, v in output_parts.items()},
        })
        for index, child in enumerate(node.get('children') or []):
            walk(child, f'{path}.children[{index}]', node_id, path, kb_path, kb_id)

    walk(trace.get('execution_tree') or trace)
    return out


def node_brief(node: dict[str, Any]) -> dict[str, str]:
    return {key: str(node.get(key) or '') for key in NODE_KEYS}


def kb_searches(nodes: list[dict[str, Any]], ref_docs: set[str], ref_chunks: set[str],
                ref_names: set[str]) -> list[dict[str, Any]]:
    searches, refs = [], (ref_docs, ref_chunks, ref_names)
    for kb in [node for node in nodes if node['role'] == 'kb_search']:
        kids = [n for n in nodes if n['kb_search_node_id'] == kb['node_id'] and n['node_id'] != kb['node_id']]
        by_role = {role: [n for n in kids if n['role'] == role] for role in sorted({n['role'] for n in kids})}
        searches.append({
            'node': kb, 'kb_search': node_brief(kb),
            'query_parse': [node_brief(n)
                            for n in by_role.get('query_parse', []) + by_role.get('query_passthrough', [])],
            'retrievers': [_metrics(n, *refs) for n in by_role.get('retriever', [])],
            'merge': [_metrics(n, *refs) for n in by_role.get('retrieval_merge', [])],
            'rerankers': [node_brief(n) | {'input': _metrics(n, *refs, 'input_'),
                                           'output': _metrics(n, *refs, 'output_')}
                          for n in by_role.get('reranker', [])],
            'result_merge': [_metrics(n, *refs) for n in by_role.get('result_merge', [])],
            'roles': {role: len(items) for role, items in by_role.items()},
        })
    return searches


def hit_union(searches: list[dict[str, Any]], key: str) -> set[str]:
    values: set[str] = set()
    path = HIT_PATHS[key]
    for search in searches:
        for item in search[path[0]]:
            values.update(item[path[1]] if len(path) == 2 else item[path[1]][path[2]])
    return values


def stage_hit_status(searches: list[dict[str, Any]], stage: str, kind: str, expected: set[str]) -> dict[str, Any]:
    if not expected: return _unknown_hit_status('no_reference_ids')
    path, items = STAGE_PATHS.get(stage) or (), []
    for search in searches if path else []:
        base = [item for item in (search.get(path[0]) or []) if isinstance(item, dict)]
        items += base if len(path) == 1 else [i[path[1]] for i in base if isinstance(i.get(path[1]), dict)]
    if not items: return _unknown_hit_status(f'{stage}_not_observed')
    key = 'doc_ids' if kind == 'doc' else 'chunk_ids'
    ids = {str(value) for item in items for value in item.get(key, []) if str(value)}
    if not ids: return _unknown_hit_status(f'{stage}_{kind}_ids_not_observed')
    hits, missing = sorted(expected & ids), sorted(expected - ids)
    status = 'hit' if hits and not missing else 'partial' if hits else 'miss'
    return {'status': status, 'hits': hits, 'missing': missing, 'unknown_reason': ''}


def search_stage_summary(index: int, search: dict[str, Any]) -> dict[str, Any]:
    first = (search.get('rerankers') or [None])[0]
    rerank = first if isinstance(first, dict) else {}
    return {
        'search_index': index, 'kb_search_step_id': (search.get('kb_search') or {}).get('node_id', ''),
        'query_summary': '',
        'retriever': _stage_summary((search.get('retrievers') or [None])[0], 'stage_not_observed'),
        'merge': _stage_summary((search.get('merge') or [None])[0], 'stage_not_observed'),
        'rerank_input': _stage_summary(rerank.get('input'), 'rerank_input_not_observed'),
        'rerank_output': _stage_summary(rerank.get('output'), 'rerank_output_not_observed'),
    }


def best_node(nodes: list[dict[str, Any]], required: set[str]) -> dict[str, Any] | None:
    def overlap(node):
        return len(node['ids'] & required) + len(node['input_ids'] & required) + len(node['output_ids'] & required)

    best = max(nodes, key=overlap, default=None)
    return best if best and overlap(best) else None


def _metrics(node, ref_docs, ref_chunks, ref_names, prefix=''):
    docs, chunks, names = node[f'{prefix}docs'], node[f'{prefix}chunks'], node[f'{prefix}names']
    return node_brief(node) | {
        'doc_ids': sorted(docs), 'chunk_ids': sorted(chunks), 'name_ids': sorted(names),
        'doc_hits': sorted(ref_docs & docs), 'chunk_hits': sorted(ref_chunks & chunks),
        'name_hits': sorted(ref_names & names),
        'doc_hit_rate': round(len(ref_docs & docs) / len(ref_docs), 4) if ref_docs else 0.0,
        'chunk_hit_rate': round(len(ref_chunks & chunks) / len(ref_chunks), 4) if ref_chunks else 0.0}


def _unknown_hit_status(reason):
    return {'status': 'unknown', 'hits': [], 'missing': [], 'unknown_reason': reason}


def _stage_summary(item, missing_reason):
    if not isinstance(item, dict):
        return {'doc': _unknown_hit_status(missing_reason), 'chunk': _unknown_hit_status(missing_reason)}
    return {'doc': _status_from_observed(item.get('doc_hits'), item.get('doc_ids')),
            'chunk': _status_from_observed(item.get('chunk_hits'), item.get('chunk_ids'))}


def _status_from_observed(hits, observed_ids):
    values = sorted({str(item) for item in hits or [] if str(item)})
    observed = sorted({str(item) for item in observed_ids or [] if str(item)})
    if not values and not observed: return _unknown_hit_status('ids_not_observed')
    return {'status': 'hit' if values else 'miss', 'hits': values, 'missing': [], 'unknown_reason': ''}


def ids_in(value: Any) -> dict[str, set[str]]:
    found = {'docs': set(), 'chunks': set(), 'names': set(), 'ids': set()}

    def add(value, bucket=''):
        text = str(value or '').strip()
        if not text: return
        if bucket == 'name': found['names'].add(text)
        elif bucket == 'doc' or text.startswith('doc_'): found['docs'].add(text)
        elif bucket == 'chunk' or ID_RE.fullmatch(text) or _looks_like_id(text): found['chunks'].add(text)
        found['ids'].update(found['docs'] | found['chunks'] | found['names'])

    def walk(item):
        if isinstance(item, dict):
            for key, val in item.items():
                if key in NAME_KEYS and val is not None: add(val, 'name')
                if key in ID_KEYS and _looks_like_id(val): add(val, ID_KEYS[key])
                walk(val)
        elif isinstance(item, list):
            for val in item: walk(val)
        elif isinstance(item, str):
            if (text := item.strip())[:1] in {'{', '['} and (parsed := jsonish(text)) is not text:
                return walk(parsed)
            for match in ID_RE.findall(text): add(match)

    walk(value)
    return found


def _looks_like_id(value):
    if not (text := str(value or '').strip()): return False
    if ID_RE.fullmatch(text): return True
    return len(text) >= 24 and all(char.isascii() and (char.isalnum() or char in '_-') for char in text)


def _filter_values(value):
    items = value if isinstance(value, (list, tuple, set)) else [value] if value is not None else []
    return {str(item) for item in items if str(item)}


def _filter_ints(value):
    out = []
    for item in value if isinstance(value, (list, tuple, set)) else [value] if value is not None else []:
        try:
            out.append(int(item))
        except (TypeError, ValueError):
            pass
    return out


def _preview(value, limit=160):
    return str(jsonish(value) if isinstance(value, str) else value or '')[:limit]

from __future__ import annotations

import re
import dataclasses
import os
import time
from dataclasses import dataclass
from typing import Any

from ...ids import validate_id
from ...runtime import OperationContext
from .utils import jsonish

ID_RE = re.compile(
    r'doc_[A-Za-z0-9_-]+|(?:chunk|node|seg|segment|uid)_[A-Za-z0-9_-]+'
    r'|[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-'
    r'[0-9a-fA-F]{4}-[0-9a-fA-F]{12}'
)
NODE_TYPES = {
    ('run_chat_pipeline', 'callable'): 'chat_entry',
    ('_StreamingReactAgent', 'module'): 'agent',
    ('Loop', 'flow'): 'agent_loop',
    ('_StreamingFunctionCall', 'module'): 'tool_planner',
    ('Pipeline', 'flow'): 'pipeline',
    ('_build_history', 'callable'): 'history_builder',
    ('_post_action', 'callable'): 'tool_call_parser',
    ('ToolManager', 'module'): 'tool_manager',
    ('Diverter', 'flow'): 'tool_router',
    ('_safe_call', 'callable'): 'tool_call',
    ('kb_search', 'module'): 'kb_search',
    ('Parallel', 'flow'): 'parallel_retrieval',
    ('parse_query', 'callable'): 'query_parse',
    ('IFS', 'flow'): 'retrieval_branch',
    ('has_files', 'callable'): 'file_branch_check',
    ('Retriever', 'module'): 'retriever',
    ('merge_rank_results', 'callable'): 'retrieval_merge',
    ('_rerank', 'callable'): 'reranker',
    ('merge_text_image_nodes', 'callable'): 'result_merge',
    ('<lambda>', 'callable'): 'query_passthrough',
}
NODE_KEYS = (
    'trace_id', 'node_id', 'name', 'node_type', 'status', 'path', 'role',
    'kb_search_node_id', 'kb_search_path',
)
ID_KEYS = {
    'docid': 'doc',
    'doc_id': 'doc',
    'document_id': 'doc',
    'core_document_id': 'doc',
    'id': 'chunk',
    'uid': 'chunk',
    'chunk_id': 'chunk',
    'segment_id': 'chunk',
    'segement_id': 'chunk',
    'node_id': 'chunk',
}
NAME_KEYS = {'file_id', 'file_name', 'filename', 'display_name'}
HIT_PATHS = {
    'retriever_doc_hits': ('retrievers', 'doc_hits'),
    'retriever_chunk_hits': ('retrievers', 'chunk_hits'),
    'rerank_input_doc_hits': ('rerankers', 'input', 'doc_hits'),
    'rerank_output_doc_hits': ('rerankers', 'output', 'doc_hits'),
    'rerank_input_chunk_hits': ('rerankers', 'input', 'chunk_hits'),
    'rerank_output_chunk_hits': ('rerankers', 'output', 'chunk_hits'),
}


@dataclass(frozen=True)
class TraceAccess:
    raw_trace: dict[str, Any]
    trace_id: str
    flat_steps: list[dict[str, Any]]
    raw_node_by_step_id: dict[str, dict[str, Any]]

    def list_trace_steps(self, filters: dict[str, Any] | None = None) -> list[dict[str, Any]]:
        steps = self.flat_steps
        if filters:
            for key in ('role', 'name', 'node_type', 'status'):
                vals = _filter_values(filters.get(key) or filters.get(f'{key}s'))
                if vals:
                    steps = [step for step in steps if str(step.get(key) or '') in vals]
        return [dict(step) for step in steps]

    def get_trace_steps(
        self,
        selector: dict[str, Any],
        include_io: bool = True,
        children_depth: int = 0,
    ) -> list[dict[str, Any]]:
        seen, out = set(), []
        for step in self._select(selector):
            depth = min(int(selector.get('children_depth', children_depth) or 0), 1)
            for item in [step, *self._children(step, depth)]:
                sid = item['step_id']
                if sid and sid not in seen:
                    seen.add(sid)
                    out.append(self._detail(item, include_io))
        return out

    def _select(self, selector: dict[str, Any]) -> list[dict[str, Any]]:
        by_id = {step['step_id']: step for step in self.flat_steps}
        by_index = {step['index']: step for step in self.flat_steps}
        step_ids = _filter_values(selector.get('step_ids')) | _filter_values(selector.get('step_id'))
        out = [by_id[item] for item in step_ids if item in by_id]
        indices = _filter_ints(selector.get('indices')) + _filter_ints(selector.get('index'))
        out += [by_index[item] for item in indices if item in by_index]
        names = _filter_values(selector.get('names')) | _filter_values(selector.get('name'))
        out += [step for step in self.flat_steps if step['name'] in names]
        seen = set()
        return [step for step in out if not (step['step_id'] in seen or seen.add(step['step_id']))]

    def _children(self, step: dict[str, Any], depth: int) -> list[dict[str, Any]]:
        if depth <= 0:
            return []
        ids = set(step.get('children_step_ids') or [])
        return [item for item in self.flat_steps if item['step_id'] in ids]

    def _detail(self, step: dict[str, Any], include_io: bool) -> dict[str, Any]:
        data = dict(step)
        raw = self.raw_node_by_step_id.get(step['step_id']) or {}
        if include_io:
            raw_data = raw.get('raw_data') if isinstance(raw.get('raw_data'), dict) else {}
            data['raw_data'] = {key: jsonish(raw_data.get(key)) for key in ('input', 'output') if key in raw_data}
        return data


def load_trace_payload(ctx: OperationContext, trace_id: str, rag: dict[str, Any]) -> dict[str, Any]:
    if isinstance(rag.get('trace'), dict):
        return _require_trace_payload(rag['trace'], trace_id)
    if not trace_id:
        raise ValueError('trace_id is required for classification')
    try:
        ref = ctx.artifact_graph.latest_ref(f"trace_{validate_id(trace_id, 'trace_id')}")
        if ctx.artifact_graph.schema_name(ref) == 'Trace':
            payload = ctx.artifact_graph.get(ref)
            if isinstance(payload, dict):
                return _require_trace_payload(payload, trace_id)
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
            if isinstance(payload, dict):
                return _require_trace_payload(payload, trace_id)
        except Exception:
            time.sleep(1)
    raise ValueError(f'trace payload not found or unreadable: {trace_id}')


def build_trace_access(trace: dict[str, Any], trace_id: str) -> TraceAccess:
    raw_nodes: dict[str, dict[str, Any]] = {}
    flat: list[dict[str, Any]] = []

    def walk(node: Any, path: str = 'execution_tree', parent: str = '', depth: int = 0) -> None:
        if not isinstance(node, dict):
            return
        sid = str(node.get('step_id') or node.get('node_id') or f'step_{len(flat)}')
        raw_nodes[sid] = node
        raw_data = node.get('raw_data') if isinstance(node.get('raw_data'), dict) else {}
        children = [child for child in (node.get('children') or []) if isinstance(child, dict)]
        name, node_type = str(node.get('name') or ''), str(node.get('node_type') or '')
        flat.append({
            'index': len(flat),
            'trace_id': trace_id,
            'step_id': sid,
            'node_id': str(node.get('node_id') or sid),
            'name': name,
            'node_type': node_type,
            'status': str(node.get('status') or ''),
            'path': path,
            'parent_step_id': parent,
            'parent_node_id': parent,
            'children_step_ids': [
                str(child.get('step_id') or child.get('node_id') or '')
                for child in children
            ],
            'depth': depth,
            'role': NODE_TYPES.get((name, node_type), 'unknown'),
            'has_input': 'input' in raw_data,
            'has_output': 'output' in raw_data,
            'input_preview': _preview(raw_data.get('input')),
            'output_preview': _preview(raw_data.get('output')),
        })
        for index, child in enumerate(children):
            walk(child, f'{path}.children[{index}]', sid, depth + 1)

    walk(trace.get('execution_tree') or trace)
    if not flat:
        raise ValueError(f'trace has no readable steps: {trace_id}')
    return TraceAccess(trace, trace_id, flat, raw_nodes)


def list_trace_steps(access: TraceAccess, filters: dict[str, Any] | None = None) -> list[dict[str, Any]]:
    return access.list_trace_steps(filters)


def get_trace_steps(
    access: TraceAccess,
    selector: dict[str, Any],
    include_io: bool = True,
    children_depth: int = 0,
) -> list[dict[str, Any]]:
    return access.get_trace_steps(selector, include_io, children_depth)


def _require_trace_payload(payload: dict[str, Any], trace_id: str) -> dict[str, Any]:
    root = payload.get('execution_tree') or payload
    if not root or not isinstance(root, dict):
        raise ValueError(f'trace payload is not an object: {trace_id}')
    return payload


def flatten_trace(trace: dict[str, Any], trace_id: str) -> list[dict[str, Any]]:
    out: list[dict[str, Any]] = []

    def walk(
        node: Any,
        path: str = 'execution_tree',
        parent_id: str = '',
        parent_path: str = '',
        kb_path: str = '',
        kb_id: str = '',
    ) -> None:
        if not isinstance(node, dict):
            return
        raw_data = node.get('raw_data') if isinstance(node.get('raw_data'), dict) else {}
        raw = {key: jsonish(raw_data.get(key)) for key in ('input', 'output') if key in raw_data}
        parts, input_parts, output_parts = ids_in(raw), ids_in(raw.get('input')), ids_in(raw.get('output'))
        name, node_type = str(node.get('name') or ''), str(node.get('node_type') or '')
        role = NODE_TYPES.get((name, node_type), 'unknown')
        node_id = str(node.get('step_id') or node.get('node_id') or '')
        if role == 'kb_search':
            kb_path, kb_id = path, node_id
        out.append({
            'trace_id': trace_id,
            'node_id': node_id,
            'parent_node_id': parent_id,
            'name': name,
            'node_type': node_type,
            'status': str(node.get('status') or ''),
            'path': path,
            'parent_path': parent_path,
            'role': role,
            'kb_search_path': kb_path,
            'kb_search_node_id': kb_id,
            'in_kb_search': bool(kb_path),
            'raw': raw,
            **parts,
            **{f'input_{k}': v for k, v in input_parts.items()},
            **{f'output_{k}': v for k, v in output_parts.items()},
        })
        for index, child in enumerate(node.get('children') or []):
            walk(child, f'{path}.children[{index}]', node_id, path, kb_path, kb_id)

    walk(trace.get('execution_tree') or trace)
    return out


def node_brief(node: dict[str, Any]) -> dict[str, str]:
    return {key: str(node.get(key) or '') for key in NODE_KEYS}


def kb_searches(
    nodes: list[dict[str, Any]],
    ref_docs: set[str],
    ref_chunks: set[str],
    ref_names: set[str],
) -> list[dict[str, Any]]:
    searches = []
    for kb in [node for node in nodes if node['role'] == 'kb_search']:
        children = [
            node for node in nodes
            if node['kb_search_node_id'] == kb['node_id'] and node['node_id'] != kb['node_id']
        ]
        by_role = {
            role: [node for node in children if node['role'] == role]
            for role in sorted({node['role'] for node in children})
        }
        searches.append({
            'node': kb,
            'kb_search': node_brief(kb),
            'query_parse': [
                node_brief(node)
                for node in by_role.get('query_parse', []) + by_role.get('query_passthrough', [])
            ],
            'retrievers': [_metrics(node, ref_docs, ref_chunks, ref_names) for node in by_role.get('retriever', [])],
            'merge': [_metrics(node, ref_docs, ref_chunks, ref_names) for node in by_role.get('retrieval_merge', [])],
            'rerankers': [
                node_brief(node) | {
                    'input': _metrics(node, ref_docs, ref_chunks, ref_names, 'input_'),
                    'output': _metrics(node, ref_docs, ref_chunks, ref_names, 'output_'),
                }
                for node in by_role.get('reranker', [])
            ],
            'result_merge': [
                _metrics(node, ref_docs, ref_chunks, ref_names)
                for node in by_role.get('result_merge', [])
            ],
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


def best_node(nodes: list[dict[str, Any]], required: set[str]) -> dict[str, Any] | None:
    best = max(
        nodes,
        key=lambda node: (
            len(node['ids'] & required)
            + len(node['input_ids'] & required)
            + len(node['output_ids'] & required)
        ),
        default=None,
    )
    return best if best and (best['ids'] | best['input_ids'] | best['output_ids']) & required else None


def _metrics(
    node: dict[str, Any],
    ref_docs: set[str],
    ref_chunks: set[str],
    ref_names: set[str],
    prefix: str = '',
) -> dict[str, Any]:
    docs, chunks, names = node[f'{prefix}docs'], node[f'{prefix}chunks'], node[f'{prefix}names']
    return node_brief(node) | {
        'doc_hits': sorted(ref_docs & docs),
        'chunk_hits': sorted(ref_chunks & chunks),
        'name_hits': sorted(ref_names & names),
        'doc_hit_rate': _rate(ref_docs & docs, ref_docs),
        'chunk_hit_rate': _rate(ref_chunks & chunks, ref_chunks),
    }


def _rate(hit: set[str], expected: set[str]) -> float:
    return round(len(hit) / len(expected), 4) if expected else 0.0


def ids_in(value: Any) -> dict[str, set[str]]:
    found = {'docs': set(), 'chunks': set(), 'names': set(), 'ids': set()}

    def add(value: Any, bucket: str = '') -> None:
        text = str(value or '').strip()
        if not text:
            return
        if bucket == 'name':
            found['names'].add(text)
        elif bucket == 'doc' or text.startswith('doc_'):
            found['docs'].add(text)
        elif bucket == 'chunk' or ID_RE.fullmatch(text) or _looks_like_id(text):
            found['chunks'].add(text)
        found['ids'].update(found['docs'] | found['chunks'] | found['names'])

    def walk(item: Any) -> None:
        if isinstance(item, dict):
            for key, val in item.items():
                if key in NAME_KEYS and val is not None:
                    add(val, 'name')
                if key in ID_KEYS and _looks_like_id(val):
                    add(val, ID_KEYS[key])
                walk(val)
        elif isinstance(item, list):
            for val in item:
                walk(val)
        elif isinstance(item, str):
            text = item.strip()
            if text[:1] in {'{', '['}:
                parsed = jsonish(text)
                if parsed is not text:
                    walk(parsed)
                    return
            for match in ID_RE.findall(text):
                add(match)

    walk(value)
    return found


def _looks_like_id(value: Any) -> bool:
    text = str(value or '').strip()
    return bool(
        text
        and (
            ID_RE.fullmatch(text)
            or (
                len(text) >= 24
                and all(char.isascii() and (char.isalnum() or char in '_-') for char in text)
            )
        )
    )


def _filter_values(value: Any) -> set[str]:
    items = value if isinstance(value, (list, tuple, set)) else [value] if value is not None else []
    return {str(item) for item in items if str(item)}


def _filter_ints(value: Any) -> list[int]:
    out = []
    for item in value if isinstance(value, (list, tuple, set)) else [value] if value is not None else []:
        try:
            out.append(int(item))
        except (TypeError, ValueError):
            pass
    return out


def _preview(value: Any, limit: int = 160) -> str:
    text = str(jsonish(value) if isinstance(value, str) else value or '')
    return text[:limit]

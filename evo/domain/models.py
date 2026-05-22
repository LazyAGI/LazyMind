from __future__ import annotations
import json
from dataclasses import dataclass, field, asdict
from typing import Any, Optional


@dataclass
class JudgeRecord:
    trace_id: str
    answer_correctness: float
    key: list[str]
    hit_key: list[str]
    reason: list[str]
    context_recall: float
    doc_recall: float
    retrieved_file: list[str]
    gt_file: list[str]
    retrieved_text: list[str]
    gt_text: list[str]
    generated_answer: str
    gt_answer: str
    faithfulness: float
    human_verified: bool
    gt_chunk_id: list[str] = field(default_factory=list)
    gt_docid: list[str] = field(default_factory=list)
    extra: dict[str, Any] = field(default_factory=dict)


@dataclass
class ModuleOutput:
    input: Any
    output: Any
    scores: list[float] = field(default_factory=list)


@dataclass
class TraceRecord:
    query: str
    modules: dict[str, ModuleOutput] = field(default_factory=dict)


@dataclass
class TraceMeta:
    flow_skeleton: list[dict[str, Any]] = field(default_factory=list)
    pipeline: list[str] = field(default_factory=list)
    flow_skeletons: dict[str, list[dict[str, Any]]] = field(default_factory=dict)


@dataclass
class MergedCaseView:
    dataset_id: str
    query: str
    judge: JudgeRecord
    trace: TraceRecord

    def to_dict(self) -> dict[str, Any]:
        return {
            'dataset_id': self.dataset_id,
            'query': self.query,
            'judge': asdict(self.judge),
            'trace': {
                'query': self.trace.query,
                'modules': {
                    k: {'input': v.input, 'output': v.output, **({'scores': v.scores} if v.scores else {})}
                    for (k, v) in self.trace.modules.items()
                },
            },
        }


@dataclass
class LoadSummary:
    total_cases: int
    field_histogram: dict[str, int]
    sample_keys: list[str]
    warnings: list[str]
    missing_traces: Optional[list[str]] = None


_JUDGE_REQUIRED = [
    'trace_id',
    'answer_correctness',
    'key',
    'hit_key',
    'reason',
    'context_recall',
    'doc_recall',
    'retrieved_file',
    'gt_file',
    'retrieved_text',
    'gt_text',
    'generated_answer',
    'gt_answer',
    'faithfulness',
    'human_verified',
]


def parse_judge_record(data: dict[str, Any]) -> tuple[JudgeRecord, list[str]]:
    warnings: list[str] = []
    for f in _JUDGE_REQUIRED:
        if f not in data:
            raise ValueError(f'Missing required field: {f}')
    extra = {k: v for (k, v) in data.items() if k not in _JUDGE_REQUIRED}
    record = JudgeRecord(
        trace_id=data['trace_id'],
        answer_correctness=float(data['answer_correctness']),
        key=list(data['key']),
        hit_key=list(data['hit_key']),
        reason=list(data['reason']),
        context_recall=float(data['context_recall']),
        doc_recall=float(data['doc_recall']),
        retrieved_file=list(data['retrieved_file']),
        gt_file=list(data['gt_file']),
        retrieved_text=list(data['retrieved_text']),
        gt_text=list(data['gt_text']),
        generated_answer=str(data['generated_answer']),
        gt_answer=str(data['gt_answer']),
        faithfulness=float(data['faithfulness']),
        human_verified=bool(data['human_verified']),
        extra=extra,
    )
    return (record, warnings)


def _maybe_json(value: Any) -> Any:
    if not isinstance(value, str):
        return value
    try:
        return json.loads(value)
    except json.JSONDecodeError:
        return value


def _raw_field(node: dict[str, Any], key: str) -> Any:
    raw = node.get('raw_data') or {}
    return _maybe_json(raw.get(key)) if isinstance(raw, dict) else None


def _extract_query(tree: dict[str, Any]) -> str:
    raw_in = _raw_field(tree, 'input')
    args = raw_in.get('args', []) if isinstance(raw_in, dict) else []
    if not args:
        return ''
    first = args[0]
    if isinstance(first, str):
        return first
    if isinstance(first, dict) and isinstance(first.get('query'), str):
        return first['query']
    return ''


def _walk_execution_tree(tree: dict[str, Any]) -> tuple[list[str], dict[str, ModuleOutput], list[dict[str, Any]]]:
    keys: list[str] = []
    modules: dict[str, ModuleOutput] = {}
    skeleton: list[dict[str, Any]] = []
    counter: dict[str, int] = {}

    def _walk(node: dict[str, Any], under_parallel: bool) -> None:
        name = node.get('name', 'unknown')
        children = node.get('children', [])
        if node.get('node_type') == 'flow':
            if name == 'Parallel':
                skel: dict[str, Any] = {'type': 'flow', 'name': name, 'branches': []}
                skeleton.append(skel)
                for i, child in enumerate(children):
                    before = len(keys)
                    _walk(child, True)
                    skel['branches'].append({'branch': i, 'steps': keys[before:]})
                return
            skeleton.append({'type': 'flow', 'name': name, 'children_count': len(children)})
            for child in children:
                _walk(child, under_parallel)
            return
        if children:
            skeleton.append({
                'type': node.get('node_type') or 'container',
                'name': name,
                'children_count': len(children),
            })
            for child in children:
                _walk(child, under_parallel)
            return
        raw = node.get('raw_data', {}) or {}
        sem_raw = _maybe_json(node.get('semantic_data') or {})
        sem = sem_raw if isinstance(sem_raw, dict) else {}
        raw_scores = sem.get('scores')
        scores = [float(s) for s in raw_scores if isinstance(s, (int, float))] if isinstance(raw_scores, (list, tuple)) else []  # noqa: E501
        counter[name] = counter.get(name, 0) + 1
        key = name if counter[name] == 1 else f'{name}_{counter[name]}'
        modules[key] = ModuleOutput(
            input=_maybe_json(raw.get('input')) if isinstance(raw, dict) else None,
            output=_maybe_json(raw.get('output')) if isinstance(raw, dict) else None,
            scores=scores,
        )
        keys.append(key)
        if not under_parallel:
            skeleton.append({'type': node.get('node_type') or 'module', 'key': key, 'name': name})

    _walk(tree, False)
    counts: dict[str, int] = {}
    for k in keys:
        base = k.split('_')[0] if '_' in k else k
        counts[base] = counts.get(base, 0) + 1
    multi = {n for (n, c) in counts.items() if c > 1}
    idx: dict[str, int] = {}
    renames: dict[str, str] = {}
    for i, k in enumerate(keys):
        base = k.split('_')[0] if '_' in k else k
        if base in multi and '_' not in k:
            idx[base] = idx.get(base, 0) + 1
            new_key = f'{base}_{idx[base]}'
            renames[k] = new_key
            keys[i] = new_key
    if renames:
        new_modules: dict[str, ModuleOutput] = {}
        for old_k, mod in modules.items():
            new_modules[renames.get(old_k, old_k)] = mod
        modules = new_modules
        for sk in skeleton:
            if sk.get('key') in renames:
                sk['key'] = renames[sk['key']]
            for br in sk.get('branches', []):
                br['steps'] = [renames.get(s, s) for s in br['steps']]
    return (keys, modules, skeleton)


def parse_trace_record(data: dict[str, Any]) -> tuple[TraceRecord, list[str]]:
    if 'execution_tree' in data:
        return _parse_execution_tree_trace(data)
    return _parse_legacy_trace(data)


def _parse_legacy_trace(data: dict[str, Any]) -> tuple[TraceRecord, list[str]]:
    warnings: list[str] = []
    for f in ('query', 'modules'):
        if f not in data:
            raise ValueError(f'Missing required field: {f}')
    modules: dict[str, ModuleOutput] = {}
    for name, md in data['modules'].items():
        if not isinstance(md, dict):
            warnings.append(f'Module {name} data is not a dict')
            continue
        scores = md.get('scores', [])
        modules[name] = ModuleOutput(input=md.get('input'), output=md.get('output'), scores=scores if scores else [])
    return (TraceRecord(query=str(data['query']), modules=modules), warnings)


def _parse_execution_tree_trace(data: dict[str, Any]) -> tuple[TraceRecord, list[str]]:
    warnings: list[str] = []
    tree = data.get('execution_tree', {})
    query = _extract_query(tree)
    _pipeline, modules, _skeleton = _walk_execution_tree(tree)
    return (TraceRecord(query=query, modules=modules), warnings)


def parse_trace_file(raw: dict[str, Any]) -> tuple[TraceMeta, dict[str, TraceRecord], list[str]]:
    warnings: list[str] = []
    traces: dict[str, TraceRecord] = {}
    ref_skeleton: list[dict[str, Any]] | None = None
    flow_skeletons: dict[str, list[dict[str, Any]]] = {}
    pipeline: list[str] = []
    seen_steps: set[str] = set()

    def add_pipeline(steps: list[str]) -> None:
        for step in steps:
            if step not in seen_steps:
                seen_steps.add(step)
                pipeline.append(step)

    for key, val in raw.items():
        if key == 'count' or not isinstance(val, dict):
            continue
        if 'execution_tree' not in val:
            try:
                rec, w = _parse_legacy_trace(val)
                traces[key] = rec
                warnings.extend((f'[trace:{key}] {x}' for x in w))
                steps = list(rec.modules)
                skeleton = [{'type': 'module', 'key': step_key, 'name': step_key} for step_key in steps]
                add_pipeline(steps)
                flow_skeletons[key] = skeleton
                if ref_skeleton is None:
                    ref_skeleton = skeleton
            except ValueError as e:
                warnings.append(f'[trace:{key}] {e}')
            continue
        tree = val.get('execution_tree', {})
        query = _extract_query(tree)
        steps, modules, skeleton = _walk_execution_tree(tree)
        traces[key] = TraceRecord(query=query, modules=modules)
        add_pipeline(steps)
        flow_skeletons[key] = skeleton
        if ref_skeleton is None:
            ref_skeleton = skeleton
    meta = TraceMeta(flow_skeleton=ref_skeleton or [], pipeline=pipeline, flow_skeletons=flow_skeletons)
    return (meta, traces, warnings)


def _normalize_correctness(val: Any) -> float:
    v = float(val)
    return v / 100.0 if v > 1.0 else v


_EVAL_KNOWN_FIELDS = {
    'case_id',
    'report_id',
    'eval_set_id',
    'kb_id',
    'trace_id',
    'query',
    'rag_answer',
    'ground_truth',
    'rag_response',
    'retrieve_contexts',
    'reference_contexts',
    'retrieve_doc',
    'refernce_doc',
    'reference_doc',
    'reference_chunk_ids',
    'reference_docids',
    'reference_doc_ids',
    'answer_correctness',
    'faithfulness',
    'context_recall',
    'doc_recall',
    'is_valid',
    'is_deleted',
    'key_points',
    'judge_reason',
    'is_manual_modify',
    'modify_user',
    'modify_time',
    'modify_reason',
    're_run_task_id',
    'hit_key',
}
_EVAL_META_FIELDS = {
    'report_id',
    'report_name',
    'eval_set_id',
    'eval_set_name',
    'kb_id',
    'kb_name',
    'trigger_type',
    'total_cases',
    'avg_score',
    'create_time',
    'finish_time',
    'creator',
    'description',
}


def parse_eval_case(case: dict[str, Any]) -> tuple[str, JudgeRecord]:
    case_id = str(case.get('case_id', 'unknown'))
    dataset_id = f'case_{case_id}'
    correctness = _normalize_correctness(case.get('answer_correctness', 0))
    key_points = case.get('key_points', [])
    if not isinstance(key_points, list):
        key_points = [str(key_points)]
    judge_reason = case.get('judge_reason', '')
    if isinstance(judge_reason, list):
        reason_list = judge_reason
    elif isinstance(judge_reason, str) and judge_reason:
        reason_list = [judge_reason]
    else:
        reason_list = []
    hit_key = case.get('hit_key', [])
    if not hit_key and key_points and (correctness > 0.5):
        hit_key = key_points[: max(1, int(len(key_points) * correctness))]
    extra = {k: v for (k, v) in case.items() if k not in _EVAL_KNOWN_FIELDS}
    judge = JudgeRecord(
        trace_id=case.get('trace_id', f'trace_{case_id}'),
        answer_correctness=correctness,
        key=key_points,
        hit_key=hit_key,
        reason=reason_list,
        context_recall=float(case.get('context_recall', 0)),
        doc_recall=float(case.get('doc_recall', 0)),
        retrieved_file=case.get('retrieve_doc', []),
        gt_file=case.get('refernce_doc', case.get('reference_doc', [])),
        retrieved_text=case.get('retrieve_contexts', []),
        gt_text=case.get('reference_contexts', []),
        generated_answer=case.get('rag_answer', ''),
        gt_answer=case.get('ground_truth', ''),
        faithfulness=float(case.get('faithfulness', 0)),
        human_verified=bool(case.get('is_valid', True)),
        gt_chunk_id=list(case.get('reference_chunk_ids', []) or []),
        gt_docid=list(case.get('reference_docids', case.get('reference_doc_ids', [])) or []),
        extra=extra,
    )
    return (dataset_id, judge)


def parse_eval_report(data: dict[str, Any]) -> tuple[dict[str, JudgeRecord], dict[str, Any], list[str]]:
    warnings: list[str] = []
    report_meta = {k: data[k] for k in _EVAL_META_FIELDS if k in data}
    cases = data.get('case_details', [])
    if not isinstance(cases, list):
        return ({}, report_meta, ['case_details is not a list'])
    judges: dict[str, JudgeRecord] = {}
    for c in cases:
        if not isinstance(c, dict) or c.get('is_deleted'):
            continue
        try:
            did, j = parse_eval_case(c)
            judges[did] = j
        except Exception as e:
            warnings.append(f"Failed to parse case {c.get('case_id', '?')}: {e}")
    return (judges, report_meta, warnings)

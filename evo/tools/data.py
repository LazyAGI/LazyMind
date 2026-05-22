from __future__ import annotations
from typing import Any
from evo.domain.tool_result import ErrorCode, ToolResult
from evo.harness.registry import tool
from evo.runtime.session import get_current_session


@tool(tags=['inspect'])
def inspect_step_for_case(dataset_id: str, step_key: str) -> ToolResult[dict[str, Any]]:
    if not dataset_id or not isinstance(dataset_id, str):
        return ToolResult.failure(
            'inspect_step_for_case', ErrorCode.INVALID_ARGUMENT, 'dataset_id must be a non-empty string'
        )
    if not step_key or not isinstance(step_key, str):
        return ToolResult.failure(
            'inspect_step_for_case', ErrorCode.INVALID_ARGUMENT, 'step_key must be a non-empty string'
        )
    session = get_current_session()
    if session is None or not session.parsed_judge:
        return ToolResult.failure('inspect_step_for_case', ErrorCode.DATA_NOT_LOADED, 'Judge corpus not loaded.')
    try:
        merged = session.get_merged_case(dataset_id)
    except KeyError:
        return ToolResult.failure(
            'inspect_step_for_case',
            ErrorCode.CASE_NOT_FOUND,
            f'Dataset ID not found: {dataset_id}. Use list_bad_cases() / list_cases_ranked() '
            f'to enumerate real IDs; examples: {session.sample_dataset_ids()}',
        )
    except ValueError as exc:
        return ToolResult.failure('inspect_step_for_case', ErrorCode.TRACE_NOT_FOUND, str(exc))
    module = merged.trace.modules.get(step_key)
    if module is None:
        return ToolResult.failure(
            'inspect_step_for_case',
            ErrorCode.INVALID_ARGUMENT,
            f'step_key {step_key!r} not present in trace; available: {list(merged.trace.modules.keys())}',
        )
    feats = session.case_step_features.get(dataset_id, {}).get(step_key, {})
    return ToolResult.success(
        'inspect_step_for_case',
        {
            'dataset_id': dataset_id,
            'step_key': step_key,
            'input': module.input,
            'output': module.output,
            'step_features': feats,
            'judge_score': merged.judge.answer_correctness,
            'pipeline': list(session.trace_meta.pipeline),
        },
    )


@tool(tags=['inspect'])
def list_bad_cases(
    threshold: float = 0.6, score_field: str | None = None, limit: int = 10, offset: int = 0, sort: str = 'asc'
) -> ToolResult[dict[str, Any]]:
    session = get_current_session()
    if session is None or not session.parsed_judge:
        return ToolResult.failure('list_bad_cases', ErrorCode.DATA_NOT_LOADED, 'Judge corpus not loaded.')
    if sort not in ('asc', 'desc'):
        return ToolResult.failure('list_bad_cases', ErrorCode.INVALID_ARGUMENT, "sort must be 'asc' or 'desc'")
    if limit < 1 or limit > 100:
        return ToolResult.failure('list_bad_cases', ErrorCode.INVALID_ARGUMENT, 'limit must be between 1 and 100')
    if offset < 0:
        return ToolResult.failure('list_bad_cases', ErrorCode.INVALID_ARGUMENT, 'offset must be non-negative')
    metric = score_field or session.config.badcase_score_field
    all_cases: list[dict[str, Any]] = []
    for did, j in session.iter_judge():
        score = getattr(j, metric, None)
        if not isinstance(score, (int, float)) or score >= threshold:
            continue
        trace = session.get_trace(j.trace_id)
        all_cases.append(
            {'dataset_id': did, 'score': score, 'trace_id': j.trace_id, 'query_preview': trace.query if trace else None}
        )
    all_cases.sort(key=lambda x: x['score'], reverse=sort == 'desc')
    buckets = {k: 0 for k in ('0.0-0.2', '0.2-0.4', '0.4-0.6', '0.6-0.8', '0.8-1.0')}
    for case in all_cases:
        s = case['score']
        if s < 0.2:
            buckets['0.0-0.2'] += 1
        elif s < 0.4:
            buckets['0.2-0.4'] += 1
        elif s < 0.6:
            buckets['0.4-0.6'] += 1
        elif s < 0.8:
            buckets['0.6-0.8'] += 1
        else:
            buckets['0.8-1.0'] += 1
    page = all_cases[offset: offset + limit]
    next_offset = offset + limit if offset + limit < len(all_cases) else None
    return ToolResult.success(
        'list_bad_cases',
        {
            'total_count': len(all_cases),
            'cases': page,
            'next_offset': next_offset,
            'histogram': buckets,
            'threshold': threshold,
            'score_field': metric,
        },
    )


@tool(tags=['inspect'])
def compare_cases(dataset_id1: str, dataset_id2: str) -> ToolResult[dict[str, Any]]:
    if not dataset_id1 or not dataset_id2:
        return ToolResult.failure('compare_cases', ErrorCode.INVALID_ARGUMENT, 'Both dataset IDs required.')
    session = get_current_session()
    if session is None or not session.parsed_judge:
        return ToolResult.failure('compare_cases', ErrorCode.DATA_NOT_LOADED, 'Judge corpus not loaded.')
    try:
        case1 = session.get_merged_case(dataset_id1)
        case2 = session.get_merged_case(dataset_id2)
    except KeyError as exc:
        return ToolResult.failure(
            'compare_cases',
            ErrorCode.CASE_NOT_FOUND,
            f'{exc}. Use list_bad_cases() / list_cases_ranked() to enumerate real IDs; '
            f'examples: {session.sample_dataset_ids()}',
        )
    metrics_to_compare = ['answer_correctness', 'context_recall', 'doc_recall', 'faithfulness']
    metrics_diff: dict[str, Any] = {}
    for m in metrics_to_compare:
        v1 = getattr(case1.judge, m, None)
        v2 = getattr(case2.judge, m, None)
        if v1 is None or v2 is None:
            continue
        diff = v1 - v2
        metrics_diff[m] = {
            'case1': v1,
            'case2': v2,
            'diff': diff,
            'better': 'case1' if diff > 0 else 'case2' if diff < 0 else 'equal',
        }
    ppl_meta = list(case1.trace.modules.keys())
    ppl2 = list(case2.trace.modules.keys())
    pipeline_diff = {
        'case1_pipeline': ppl_meta,
        'case2_pipeline': ppl2,
        'length_diff': len(ppl_meta) - len(ppl2),
        'common_modules': list(set(ppl_meta) & set(ppl2)),
        'unique_to_case1': list(set(ppl_meta) - set(ppl2)),
        'unique_to_case2': list(set(ppl2) - set(ppl_meta)),
    }
    module_diff = {
        'case1_module_count': len(case1.trace.modules),
        'case2_module_count': len(case2.trace.modules),
        'common_module_names': list(set(case1.trace.modules) & set(case2.trace.modules)),
    }
    hints: list[str] = []
    ac = metrics_diff.get('answer_correctness')
    if ac and ac['diff']:
        hints.append(
            f"{('case1' if ac['better'] == 'case1' else 'case2')} has higher correctness; "
            'examine its retrieval and generation pipeline for transferable patterns.'
        )
    if pipeline_diff['length_diff']:
        longer = 'case1' if pipeline_diff['length_diff'] > 0 else 'case2'
        hints.append(f'{longer} has longer pipeline; verify whether extra modules help.')
    return ToolResult.success(
        'compare_cases',
        {
            'dataset_id1': dataset_id1,
            'dataset_id2': dataset_id2,
            'metrics_diff': metrics_diff,
            'pipeline_diff': pipeline_diff,
            'module_diff': module_diff,
            'hypothesis_hints': hints,
        },
    )


@tool(tags=['inspect'])
def recall_handle(handle: str) -> ToolResult[dict[str, Any]]:
    session = get_current_session()
    if session is None or session.handle_store is None:
        return ToolResult.failure('recall_handle', ErrorCode.DATA_NOT_LOADED, 'no handle store on session')
    h = session.handle_store.get(handle)
    if h is None:
        return ToolResult.failure('recall_handle', ErrorCode.INVALID_ARGUMENT, f'unknown handle {handle!r}')
    return ToolResult.success('recall_handle', {'tool': h.tool, 'args': h.args, 'result': h.result})

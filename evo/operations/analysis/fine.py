from __future__ import annotations

import json
from typing import Any

from ...artifacts import ArtifactDraft, ArtifactRef
from ..dataset.utils import json_object, validate_case_id
from ...ids import validate_id
from ...runtime import AdapterCall, OperationContext, OperationOutput, evo_llm
from .trace import flatten_trace, hit_union, kb_searches, load_trace_payload, node_brief
from .utils import clean_contexts, short, typed_payload, values

TRACE_OK = {'', 'ok', 'success', 'succeeded'}
PROMPT = """只输出 JSON。fine_category 必须来自 allowed_subcategories，证据不足输出 insufficient_evidence。
输入:{packet}
输出:{{"fine_category":"...","confidence":"high|medium|low","reason":"...","missing_evidence":[]}}"""


class CaseFineClassificationOperation:
    def __init__(self, llm: Any | None = None):
        self.llm = llm

    def execute(self, ctx: OperationContext) -> OperationOutput:
        coarse_ref = ArtifactRef.parse(str(ctx.params.get('coarse_classification_ref') or ''))
        coarse = typed_payload(ctx, coarse_ref, 'CaseCoarseClassification')
        case_id = validate_case_id(str(coarse.get('case_id') or ''))
        output_id = validate_id(str(ctx.params.get('output_id') or f'case_fine_classification_{case_id}'),
                                'output_id')
        if output_id != f'case_fine_classification_{case_id}':
            raise ValueError(f'output_id does not match case_id: {output_id}')
        refs = _refs(coarse)
        case, rag, judge, report = [typed_payload(ctx, refs[k], s) for k, s in (
            ('case_ref', 'DatasetCase'), ('rag_answer_ref', 'RagAnswer'), ('judge_result_ref', 'JudgeResult'),
            ('eval_report_ref', 'EvalReport'),
        )]
        _validate(coarse, case, rag, judge, report, case_id, refs)
        ctx.report_progress(phase='fine_classify', status='running', message='fine classifying bad case',
                            current_item=case_id, detail={'coarse_category': coarse.get('coarse_category')})
        payload = classify_payload(ctx, coarse_ref, coarse, case, rag, judge, self)
        ctx.report_progress(phase='fine_classify', status='success',
                            message=f"fine classified as {payload['fine_category']}", current_item=case_id,
                            detail={'fine_category': payload['fine_category'], 'llm_used': payload['llm_used']})
        return OperationOutput([ArtifactDraft(output_id, 'CaseFineClassification', payload, ctx.operation_run_id,
                                              input_refs=[coarse_ref, *refs.values()])])

    def _llm_classify(self, ctx, evidence):
        if not evidence['llm_allowed'] or not evidence['allowed']:
            return _insufficient(['llm_not_allowed' if not evidence['llm_allowed'] else 'allowed_subcategories']), []
        packet = {'base': _base(evidence), 'trace_plan': _trace_plan(evidence)}
        call = AdapterCall('llm.fine_classify_case', lambda p: self._model()(p['prompt'], stream=False)).run(
            ctx, {'case_id': evidence['case_id'], 'prompt': PROMPT.format(
                packet=json.dumps(packet, ensure_ascii=False, sort_keys=True)
            )}, phase='fine_classify_llm', item_ref=evidence['case_id']
        )
        data = json_object(call.response)
        fine = str(data.get('fine_category') or '').strip()
        if fine == 'insufficient_evidence':
            return _insufficient(values(data.get('missing_evidence')) or ['llm_insufficient_evidence']), [call]
        if fine not in evidence['allowed']:
            raise ValueError(f'LLM fine_category not allowed: {fine}')
        return _result(fine, 'llm', data.get('confidence'), short(data.get('reason'), 120),
                       evidence['coarse_hits'], refs=[]), [call]

    def _model(self):
        if self.llm is None:
            self.llm = evo_llm()
        return self.llm


def classify_payload(ctx, coarse_ref, coarse, case, rag, judge, op) -> dict[str, Any]:
    evidence = _evidence(ctx, coarse, case, rag, judge)
    result, calls = _rule(evidence), []
    if result is None:
        result, calls = op._llm_classify(ctx, evidence)
    trace_plan = _trace_plan(evidence)
    payload = {
        'case_id': str(coarse.get('case_id') or ''),
        'coarse_classification_ref': str(coarse_ref),
        **{k: str(coarse.get(k) or '') for k in (
            'eval_report_ref', 'eval_dataset_ref', 'case_ref', 'rag_answer_ref', 'judge_result_ref',
        )},
        'coarse_category': evidence['coarse_category'],
        **result,
        'llm_used': bool(calls),
        'llm_call_refs': [_call_ref(call) for call in calls],
        'llm_call_reasons': ['final_classification'][:len(calls)],
        'trace_used': True,
        'trace_plan': trace_plan,
        'trace_reads': [],
        'source_message_id': str(ctx.params.get('source_message_id') or ''),
    }
    if (
        payload['classification_method'] != 'insufficient_evidence'
        and payload['fine_category'] not in evidence['allowed']
    ):
        raise ValueError(f"fine_category not allowed: {payload['fine_category']}")
    return payload


def _refs(coarse) -> dict[str, ArtifactRef]:
    keys = ('eval_report_ref', 'case_ref', 'rag_answer_ref', 'judge_result_ref')
    missing = [key for key in keys if not str(coarse.get(key) or '').strip()]
    if missing:
        raise ValueError(f'coarse missing refs: {missing}')
    return {key: ArtifactRef.parse(str(coarse[key])) for key in keys}


def _validate(coarse, case, rag, judge, report, case_id, refs) -> None:
    if str(case.get('id') or '') != case_id:
        raise ValueError(f"{refs['case_ref']} payload id mismatch")
    if not any(str(row.get('case_id') or '') == case_id for row in report.get('bad_cases') or []):
        raise ValueError(f'case is not a badcase in EvalReport: {case_id}')
    for name, payload, expected in (
        ('RagAnswer', rag, {'case_id': case_id, 'case_ref': str(refs['case_ref'])}),
        ('JudgeResult', judge, {'case_id': case_id, 'case_ref': str(refs['case_ref']),
                                'rag_answer_ref': str(refs['rag_answer_ref'])}),
    ):
        for key, value in expected.items():
            if str(payload.get(key) or '') != value:
                raise ValueError(f'{name} {key} mismatch: {payload.get(key)!r} != {value!r}')
    if str(rag.get('eval_dataset_ref') or '') != str(judge.get('eval_dataset_ref') or ''):
        raise ValueError('RagAnswer/JudgeResult eval_dataset_ref mismatch')
    if str(coarse.get('coarse_category') or '') == 'insufficient_evidence':
        raise ValueError('insufficient coarse classification cannot be fine classified')


def _evidence(ctx, coarse, case, rag, judge) -> dict[str, Any]:
    trace_id = str(rag.get('trace_id') or judge.get('trace_id') or '').strip()
    trace = load_trace_payload(ctx, trace_id, rag)
    nodes = flatten_trace(trace, trace_id or str(trace.get('trace_id') or trace.get('id') or ''))
    docs, chunks, names = values(case.get('reference_doc_ids')), values(case.get('reference_chunk_ids')), values(
        case.get('reference_doc')
    )
    return {
        'case_id': str(coarse.get('case_id') or ''),
        'coarse_category': str(coarse.get('coarse_category') or ''),
        'allowed': list((coarse.get('next_step') or {}).get('allowed_subcategories') or []),
        'llm_allowed': bool((coarse.get('next_step') or {}).get('llm_allowed')),
        'coarse_hits': _compact((coarse.get('evidence') or {}).get('rule_hits') or []),
        'case': case, 'rag': rag, 'judge': judge, 'nodes': nodes,
        'ref_docs': docs, 'ref_chunks': chunks, 'final_docs': values(rag.get('doc_ids')),
        'final_chunks': values(rag.get('chunk_ids')), 'searches': kb_searches(nodes, docs, chunks, names),
    }


def _rule(evidence):
    return {
        'dataset_or_reference_issue': _dataset_rule,
        'agentic_tool_issue': _agent_rule,
        'retrieval_issue': _retrieval_rule,
        'rerank_issue': _rerank_rule,
        'chunking_or_parse_issue': _chunk_rule,
    }.get(evidence['coarse_category'], lambda _e: None)(evidence)


def _dataset_rule(evidence):
    return None if evidence['ref_docs'] and evidence['ref_chunks'] else _result(
        'missing_reference', 'rule', 'high', 'DatasetCase missing reference ids', evidence['coarse_hits']
    )


def _agent_rule(evidence):
    if any('tool_error' in str(hit.get('rule_id') or '') for hit in evidence['coarse_hits']):
        return _result('tool_execution_issue', 'rule', 'high', 'tool trace contains execution error',
                       evidence['coarse_hits'])
    missing = [n for n in evidence['nodes'] if n['role'] in {'tool_call', 'tool_manager'} and _missing_arg(n)]
    return _result('tool_argument_issue', 'rule', 'high', 'tool call missing argument', evidence['coarse_hits']) \
        if missing else None


def _retrieval_rule(evidence):
    for kind, refs, key, fine in (
        ('doc', evidence['ref_docs'], 'retriever_doc_hits', 'retrieval_doc_miss'),
        ('chunk', evidence['ref_chunks'], 'retriever_chunk_hits', 'retrieval_chunk_miss'),
    ):
        missed = refs - hit_union(evidence['searches'], key)
        if evidence['searches'] and refs and missed:
            return _result(fine, 'rule', 'high', f'reference {kind}s missing from retriever outputs',
                           evidence['coarse_hits'], _hit(f'fine.{fine}', evidence, {f'missing_{kind}_ids': missed}))
    return None


def _rerank_rule(evidence):
    if not evidence['searches']:
        return _insufficient(['rerank_trace'])
    hits = {k: hit_union(evidence['searches'], k) for k in (
        'retriever_doc_hits', 'retriever_chunk_hits', 'rerank_input_doc_hits', 'rerank_input_chunk_hits',
        'rerank_output_doc_hits', 'rerank_output_chunk_hits',
    )}
    merged = {k: _merge_hits(evidence['searches'], k) for k in ('doc_hits', 'chunk_hits')}
    for fine, docs, chunks, reason in (
        ('rrf_merge_drop', hits['retriever_doc_hits'] - (merged['doc_hits'] | hits['rerank_input_doc_hits']),
         hits['retriever_chunk_hits'] - (merged['chunk_hits'] | hits['rerank_input_chunk_hits']), 'pre-rerank drop'),
        ('rerank_drop', hits['rerank_input_doc_hits'] - hits['rerank_output_doc_hits'],
         hits['rerank_input_chunk_hits'] - hits['rerank_output_chunk_hits'], 'reranker drop'),
        ('topk_cutoff_issue', hits['rerank_output_doc_hits'] - evidence['final_docs'],
         hits['rerank_output_chunk_hits'] - evidence['final_chunks'], 'final topk cutoff'),
    ):
        if docs or chunks:
            return _result(fine, 'rule', 'medium' if fine == 'topk_cutoff_issue' else 'high', reason,
                           evidence['coarse_hits'], _hit(f'fine.{fine}', evidence, {
                               'missing_doc_ids': docs, 'missing_chunk_ids': chunks,
                           }))
    return None


def _chunk_rule(evidence):
    texts = clean_contexts(evidence['case'].get('reference_context')) + clean_contexts(evidence['rag'].get('contexts'))
    if any('\ufffd' in text or '\x00' in text for text in texts):
        return _result('document_parse_missing_text', 'rule', 'medium', 'source text is garbled',
                       evidence['coarse_hits'])
    qtype = str(evidence['case'].get('question_type') or '')
    if qtype in {'table_list', 'formula'} and texts and not any(_structured(text) for text in texts):
        return _result('formula_parse_issue' if qtype == 'formula' else 'table_parse_issue', 'rule', 'medium',
                       'structured source text lost markers', evidence['coarse_hits'])
    return (
        _insufficient(['source_snapshot_neighbors'])
        if any('boundary' in str(hit) for hit in evidence['coarse_hits'])
        else None
    )


def _base(evidence) -> dict[str, Any]:
    case, rag, judge = evidence['case'], evidence['rag'], evidence['judge']
    contexts = clean_contexts(case.get('reference_context')) + clean_contexts(rag.get('contexts'))
    contexts += clean_contexts(judge.get('judge_contexts'))
    return {
        'case': {k: case.get(k) for k in ('id', 'question_type', 'difficulty', 'question', 'answer')},
        'rag': {'answer': rag.get('answer'), 'doc_ids': rag.get('doc_ids'), 'chunk_ids': rag.get('chunk_ids'),
                'contexts': [short(text, 1000) for text in contexts[:6]]},
        'judge': {k: judge.get(k) for k in ('answer_correctness', 'faithfulness', 'doc_recall', 'context_recall',
                                            'quality_label', 'failure_type', 'reason', 'defect')},
        'coarse': {'coarse_category': evidence['coarse_category'], 'allowed_subcategories': evidence['allowed'],
                   'rule_hits': evidence['coarse_hits'][:5]},
    }


def _trace_plan(evidence) -> dict[str, Any]:
    steps = list(enumerate(evidence['nodes']))
    priority = [item for item in steps if str(item[1].get('status') or '').lower() not in TRACE_OK][:8] or steps[-8:]
    return {'priority_steps': [_step(index, node) for index, node in priority], 'step_count': len(steps)}


def _result(category, method, confidence, reason, coarse_hits, fine_hit=None, refs=None) -> dict[str, Any]:
    return {'fine_category': category,
            'confidence': confidence if confidence in {'high', 'medium', 'low'} else 'medium',
            'classification_method': method, 'reason': reason,
            'evidence': {'coarse_rule_hits': coarse_hits, 'fine_rule_hits': [fine_hit] if fine_hit else [],
                         'llm_evidence_refs': refs or []},
            'missing_evidence': []}


def _insufficient(missing: Any) -> dict[str, Any]:
    return {'fine_category': 'insufficient_evidence', 'confidence': 'low',
            'classification_method': 'insufficient_evidence',
            'reason': 'fine classification lacks required evidence',
            'evidence': {'coarse_rule_hits': [], 'fine_rule_hits': [], 'llm_evidence_refs': []},
            'missing_evidence': sorted(values(missing))}


def _hit(rule_id, evidence, observed) -> dict[str, Any]:
    node = next((n for n in evidence['nodes'] if n['role'] in {'kb_search', 'retriever', 'reranker'}), None)
    return {'rule_id': rule_id, 'source': 'trace' if node else 'artifact',
            'trace_node': node_brief(node) if node else {},
            'expected': {'reference_doc_ids': sorted(evidence['ref_docs']),
                         'reference_chunk_ids': sorted(evidence['ref_chunks'])},
            'observed': {k: sorted(v) if isinstance(v, set) else v for k, v in observed.items()}}


def _compact(hits) -> list[dict[str, Any]]:
    out = []
    for hit in hits[:5]:
        observed = hit.get('observed') or {}
        out.append({'rule_id': hit.get('rule_id'), 'category': hit.get('category'), 'stage': hit.get('stage'),
                    'source': hit.get('source'), 'trace_node': hit.get('trace_node') or {},
                    'expected': {k: _cap(v) for k, v in (hit.get('expected') or {}).items()},
                    'observed': {k: _cap(v) for k, v in observed.items()
                                 if k.startswith('missing_') or k.endswith(('_hits', '_ids'))}})
    return out


def _step(index: int, node: dict[str, Any]) -> dict[str, Any]:
    return {'index': index, 'step_id': node.get('node_id'), **node_brief(node)}


def _merge_hits(searches, key) -> set[str]:
    out: set[str] = set()
    for search in searches:
        for item in search.get('merge') or []:
            out.update(item.get(key) or [])
    return out


def _missing_arg(node) -> bool:
    text = json.dumps(node['raw'].get('input') or {}, ensure_ascii=False)
    return node['role'] == 'tool_call' and 'kb_search' in text and not any(
        key in text for key in ('query', 'dataset', 'dataset_id')
    )


def _structured(text: str) -> bool:
    return any(mark in text for mark in ('|', '\t', '=', '∑', '公式', '表', 'Table', 'List', '1.', '- '))


def _cap(value: Any, limit: int = 20) -> Any:
    return (sorted(value) if isinstance(value, set) else value)[:limit] if isinstance(value, (list, set)) else value


def _call_ref(call) -> str:
    return call.record.record_ref or call.record.call_id

from __future__ import annotations

import hashlib
import json
from typing import Any

from ...artifacts import ArtifactDraft, ArtifactRef
from ..dataset.utils import json_object, validate_case_id
from ... import validate_id
from ...runtime import AdapterCall, OperationContext, OperationOutput, evo_llm
from .trace import flatten_trace, hit_union, kb_searches, load_trace_payload, node_brief
from .utils import bound_input_ref, check_fields, clean_contexts, has_structure, short, typed_payload, values

TRACE_OK = {'', 'ok', 'success', 'succeeded'}
REF_KEYS = ('eval_report_ref', 'eval_dataset_ref', 'case_ref', 'rag_answer_ref', 'judge_result_ref')
PROMPT = """只输出 JSON。fine_category 必须来自 allowed_subcategories，证据不足输出 insufficient_evidence。
输入:{packet}
输出:{{"fine_category":"...","confidence":"high|medium|low","reason":"...","missing_evidence":[]}}"""


class CaseFineClassificationOperation:
    def __init__(self, llm: Any | None = None):
        self.llm = llm

    def execute(self, ctx: OperationContext) -> OperationOutput:
        coarse_ref = bound_input_ref(ctx, ctx.params.get('coarse_classification_ref'), 'CaseCoarseClassification')
        coarse = typed_payload(ctx, coarse_ref, 'CaseCoarseClassification')
        case_id = validate_case_id(str(coarse.get('case_id') or ''))
        default_id = f'case_fine_classification_{case_id}'
        output_id = validate_id(str(ctx.params.get('output_id') or default_id), 'output_id')
        if output_id != default_id: raise ValueError(f'output_id does not match case_id: {output_id}')
        keys = ('eval_report_ref', 'case_ref', 'rag_answer_ref', 'judge_result_ref')
        if missing := [key for key in keys if not str(coarse.get(key) or '').strip()]:
            raise ValueError(f'coarse missing refs: {missing}')
        refs = {key: ArtifactRef.parse(str(coarse[key])) for key in keys}
        case, rag, judge, report = (typed_payload(ctx, refs[k], s) for k, s in (
            ('case_ref', 'DatasetCase'), ('rag_answer_ref', 'RagAnswer'), ('judge_result_ref', 'JudgeResult'),
            ('eval_report_ref', 'EvalReport')))
        if str(case.get('id') or '') != case_id: raise ValueError(f"{refs['case_ref']} payload id mismatch")
        if not any(str(row.get('case_id') or '') == case_id for row in report.get('bad_cases') or []):
            raise ValueError(f'case is not a badcase in EvalReport: {case_id}')
        check_fields('RagAnswer', rag, {'case_id': case_id, 'case_ref': str(refs['case_ref'])})
        check_fields('JudgeResult', judge, {'case_id': case_id, 'case_ref': str(refs['case_ref']),
                                            'rag_answer_ref': str(refs['rag_answer_ref'])})
        if str(rag.get('eval_dataset_ref') or '') != str(judge.get('eval_dataset_ref') or ''):
            raise ValueError('RagAnswer/JudgeResult eval_dataset_ref mismatch')
        if str(coarse.get('coarse_category') or '') == 'insufficient_evidence':
            raise ValueError('insufficient coarse classification cannot be fine classified')
        ctx.report_progress(phase='fine_classify', status='running', message='fine classifying bad case',
                            current_item=case_id, detail={'coarse_category': coarse.get('coarse_category')})
        payload = classify_payload(ctx, coarse_ref, coarse, case, rag, judge, self)
        ctx.report_progress(phase='fine_classify', status='success',
                            message=f"fine classified as {payload['fine_category']}", current_item=case_id,
                            detail={'fine_category': payload['fine_category'], 'llm_used': payload['llm_used']})
        return OperationOutput([ArtifactDraft(output_id, 'CaseFineClassification', payload, ctx.operation_run_id,
                                              input_refs=[coarse_ref, *refs.values()])])

    def _llm_classify(self, ctx, ev):
        if not ev['llm_allowed'] or not ev['allowed']:
            return _insufficient(['llm_not_allowed' if not ev['llm_allowed'] else 'allowed_subcategories']), []
        case, rag, judge = ev['case'], ev['rag'], ev['judge']
        contexts = clean_contexts(case.get('reference_context')) + clean_contexts(rag.get('contexts'))
        contexts += clean_contexts(judge.get('judge_contexts'))
        packet = {'base': {
            'case': {k: case.get(k) for k in ('id', 'question_type', 'difficulty', 'question', 'answer')},
            'rag': {'answer': short(rag.get('answer'), 8000), 'doc_ids': rag.get('doc_ids'),
                    'chunk_ids': rag.get('chunk_ids'), 'contexts': [short(text, 1000) for text in contexts[:6]]},
            'judge': {k: judge.get(k) for k in ('answer_correctness', 'faithfulness', 'doc_recall',
                                                'context_recall', 'quality_label', 'failure_type', 'reason',
                                                'defect')},
            'coarse': {'coarse_category': ev['coarse_category'], 'allowed_subcategories': ev['allowed'],
                       'rule_hits': ev['coarse_hits'][:5]},
        }, 'trace_plan': _trace_plan(ev)}
        call = AdapterCall('llm.fine_classify_case', lambda p: self._model()(p['prompt'], stream=False)).run(
            ctx, {'case_id': ev['case_id'],
                  'prompt': PROMPT.format(packet=json.dumps(packet, ensure_ascii=False, sort_keys=True))},
            phase='fine_classify_llm', item_ref=ev['case_id'])
        data = json_object(call.response)
        fine = str(data.get('fine_category') or '').strip()
        if fine == 'insufficient_evidence':
            return _insufficient(values(data.get('missing_evidence')) or ['llm_insufficient_evidence']), [call]
        if fine not in ev['allowed']: raise ValueError(f'LLM fine_category not allowed: {fine}')
        return _result(fine, 'llm', data.get('confidence'), short(data.get('reason'), 120), ev['coarse_hits'],
                       refs=[]), [call]

    def _model(self):
        if self.llm is None: self.llm = evo_llm()
        return self.llm


def classify_payload(ctx, coarse_ref, coarse, case, rag, judge, op) -> dict[str, Any]:
    if str(coarse.get('coarse_category') or '') == 'infra_failure': return _infra_payload(ctx, coarse_ref, coarse)
    trace_id = str(rag.get('trace_id') or judge.get('trace_id') or '').strip()
    trace = load_trace_payload(ctx, trace_id, rag)
    nodes = flatten_trace(trace, trace_id or str(trace.get('trace_id') or trace.get('id') or ''))
    docs, chunks = values(case.get('reference_doc_ids')), values(case.get('reference_chunk_ids'))
    ev = {'case_id': str(coarse.get('case_id') or ''), 'coarse_category': str(coarse.get('coarse_category') or ''),
          'allowed': list((coarse.get('next_step') or {}).get('allowed_subcategories') or []),
          'llm_allowed': bool((coarse.get('next_step') or {}).get('llm_allowed')),
          'coarse_hits': _compact((coarse.get('evidence') or {}).get('rule_hits') or []),
          'case': case, 'rag': rag, 'judge': judge, 'nodes': nodes,
          'ref_docs': docs, 'ref_chunks': chunks, 'final_docs': values(rag.get('doc_ids')),
          'final_chunks': values(rag.get('chunk_ids')),
          'searches': kb_searches(nodes, docs, chunks, values(case.get('reference_doc')))}
    result, calls, adjudication = _rule(ev), [], {}
    if result is None:
        result, calls = op._llm_classify(ctx, ev)
    # Deterministic hash sampling so rule classifications get LLM review at a stable coverage ratio.
    elif (ratio := float(ctx.params.get('adjudication_ratio') or 0.2)) > 0 and ev['llm_allowed'] and ev['allowed'] \
            and int.from_bytes(hashlib.sha256(ev['case_id'].encode('utf-8')).digest()[:4], 'big') / 0xFFFFFFFF < ratio:
        try:
            llm_result, calls = op._llm_classify(ctx, ev)
            agreement = llm_result['fine_category'] == result['fine_category']
            adjudication = {'sampled': True, 'rule_category': result['fine_category'],
                            'llm_category': llm_result['fine_category'], 'agreement': agreement}
            if not agreement and llm_result['classification_method'] != 'insufficient_evidence':
                result = dict(result, confidence='low',
                              reason=f"{result['reason']} (LLM复核分歧: {llm_result['fine_category']})")
        except ValueError:
            pass
    payload = {
        'case_id': str(coarse.get('case_id') or ''), 'coarse_classification_ref': str(coarse_ref),
        **{key: str(coarse.get(key) or '') for key in REF_KEYS},
        'coarse_category': ev['coarse_category'], **result,
        'llm_used': bool(calls), 'llm_call_refs': [call.record.record_ref or call.record.call_id for call in calls],
        'llm_call_reasons': (['adjudication'] if adjudication else ['final_classification'])[:len(calls)],
        'adjudication': adjudication, 'trace_used': True, 'trace_plan': _trace_plan(ev), 'trace_reads': [],
        'source_message_id': str(ctx.params.get('source_message_id') or ''),
    }
    if payload['classification_method'] != 'insufficient_evidence' and payload['fine_category'] not in ev['allowed']:
        raise ValueError(f"fine_category not allowed: {payload['fine_category']}")
    return payload


def _infra_payload(ctx, coarse_ref, coarse) -> dict[str, Any]:
    return {
        'case_id': str(coarse.get('case_id') or ''), 'coarse_classification_ref': str(coarse_ref),
        **{key: str(coarse.get(key) or '') for key in REF_KEYS},
        'coarse_category': 'infra_failure',
        'fine_category': 'infra_failure', 'confidence': 'high', 'classification_method': 'infra_failure',
        'reason': str(coarse.get('coarse_reason') or 'trace evidence unavailable'),
        'evidence': {'coarse_rule_hits': [], 'fine_rule_hits': [], 'llm_evidence_refs': []},
        'missing_evidence': list(coarse.get('missing_evidence') or []),
        'llm_used': False, 'llm_call_refs': [], 'llm_call_reasons': [], 'adjudication': {},
        'trace_used': False, 'trace_plan': {}, 'trace_reads': [],
        'source_message_id': str(ctx.params.get('source_message_id') or ''),
    }


def _rule(ev):
    cat, hits = ev['coarse_category'], ev['coarse_hits']
    if cat == 'dataset_or_reference_issue':
        if ev['ref_docs'] and ev['ref_chunks']: return None
        return _result('missing_reference', 'rule', 'high', 'DatasetCase missing reference ids', hits)
    if cat == 'agentic_tool_issue':
        rule_ids = [str(hit.get('rule_id') or '') for hit in hits]
        if any('no_kb_search' in rule for rule in rule_ids):
            return _result('tool_selection_issue', 'rule', 'high', 'agent never invoked kb_search', hits)
        if any('tool_error' in rule for rule in rule_ids):
            return _result('tool_execution_issue', 'rule', 'high', 'tool trace contains execution error', hits)
        for node in ev['nodes']:
            if node['role'] != 'tool_call': continue
            text = json.dumps(node['raw'].get('input') or {}, ensure_ascii=False)
            if 'kb_search' in text and not any(key in text for key in ('query', 'dataset', 'dataset_id')):
                return _result('tool_argument_issue', 'rule', 'high', 'tool call missing argument', hits)
        return None
    if cat == 'retrieval_issue':
        for kind, refs, key, fine in (('doc', ev['ref_docs'], 'retriever_doc_hits', 'retrieval_doc_miss'),
                                      ('chunk', ev['ref_chunks'], 'retriever_chunk_hits', 'retrieval_chunk_miss')):
            missed = refs - hit_union(ev['searches'], key)
            if ev['searches'] and refs and missed:
                return _result(fine, 'rule', 'high', f'reference {kind}s missing from retriever outputs', hits,
                               _hit(f'fine.{fine}', ev, {f'missing_{kind}_ids': missed}))
        return None
    if cat == 'rerank_issue': return _rerank_rule(ev)
    if cat == 'chunking_or_parse_issue':
        texts = clean_contexts(ev['case'].get('reference_context')) + clean_contexts(ev['rag'].get('contexts'))
        if any('\ufffd' in text or '\x00' in text for text in texts):
            return _result('document_parse_missing_text', 'rule', 'medium', 'source text is garbled', hits)
        qtype = str(ev['case'].get('question_type') or '')
        if qtype in {'table_list', 'formula'} and texts and not any(has_structure(text) for text in texts):
            return _result('formula_parse_issue' if qtype == 'formula' else 'table_parse_issue', 'rule', 'medium',
                           'structured source text lost markers', hits)
        if any('boundary' in str(hit) for hit in hits): return _insufficient(['source_snapshot_neighbors'])
        return None
    return None


def _rerank_rule(ev):
    if not ev['searches']: return _insufficient(['rerank_trace'])
    hits = {k: hit_union(ev['searches'], k) for k in (
        'retriever_doc_hits', 'retriever_chunk_hits', 'rerank_input_doc_hits', 'rerank_input_chunk_hits',
        'rerank_output_doc_hits', 'rerank_output_chunk_hits')}
    merged = {k: {v for s in ev['searches'] for item in s.get('merge') or [] for v in item.get(k) or []}
              for k in ('doc_hits', 'chunk_hits')}
    for fine, docs, chunks, reason in (
        ('rrf_merge_drop', hits['retriever_doc_hits'] - (merged['doc_hits'] | hits['rerank_input_doc_hits']),
         hits['retriever_chunk_hits'] - (merged['chunk_hits'] | hits['rerank_input_chunk_hits']), 'pre-rerank drop'),
        ('rerank_drop', hits['rerank_input_doc_hits'] - hits['rerank_output_doc_hits'],
         hits['rerank_input_chunk_hits'] - hits['rerank_output_chunk_hits'], 'reranker drop'),
        ('topk_cutoff_issue', hits['rerank_output_doc_hits'] - ev['final_docs'],
         hits['rerank_output_chunk_hits'] - ev['final_chunks'], 'final topk cutoff'),
    ):
        if docs or chunks:
            return _result(fine, 'rule', 'medium' if fine == 'topk_cutoff_issue' else 'high', reason,
                           ev['coarse_hits'],
                           _hit(f'fine.{fine}', ev, {'missing_doc_ids': docs, 'missing_chunk_ids': chunks}))
    return None


def _trace_plan(ev) -> dict[str, Any]:
    steps = list(enumerate(ev['nodes']))
    priority = [item for item in steps if str(item[1].get('status') or '').lower() not in TRACE_OK][:8] or steps[-8:]
    return {'priority_steps': [{'index': index, 'step_id': node.get('node_id'), **node_brief(node)}
                               for index, node in priority], 'step_count': len(steps)}


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


def _hit(rule_id, ev, observed) -> dict[str, Any]:
    node = next((n for n in ev['nodes'] if n['role'] in {'kb_search', 'retriever', 'reranker'}), None)
    return {'rule_id': rule_id, 'source': 'trace' if node else 'artifact',
            'trace_node': node_brief(node) if node else {},
            'expected': {'reference_doc_ids': sorted(ev['ref_docs']),
                         'reference_chunk_ids': sorted(ev['ref_chunks'])},
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


def _cap(value, limit=20):
    return (sorted(value) if isinstance(value, set) else value)[:limit] if isinstance(value, (list, set)) else value

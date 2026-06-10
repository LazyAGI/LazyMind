from __future__ import annotations

import json

from ...artifacts import ArtifactDraft, ArtifactRef
from ..dataset.utils import validate_case_id
from ... import validate_id
from ...runtime import OperationContext, OperationOutput
from .trace import HIT_PATHS, best_node, flatten_trace, hit_union, kb_searches, load_trace_payload, node_brief
from .utils import check_fields, has_structure, score, short, typed_payload, values

SUBCATEGORIES = {
    'dataset_or_reference_issue': ('missing_reference invalid_reference_binding '
                                   'answer_not_supported_by_reference').split(),
    'agentic_tool_issue': ('agent_task_understanding_issue agent_loop_or_control_issue tool_selection_issue '
                           'tool_argument_issue tool_execution_issue tool_result_integration_issue '
                           'multi_tool_reasoning_failure agent_answer_synthesis_issue').split(),
    'retrieval_issue': 'retrieval_doc_miss retrieval_chunk_miss query_or_route_issue'.split(),
    'rerank_issue': 'rerank_drop rrf_merge_drop topk_cutoff_issue score_inversion_issue'.split(),
    'chunking_or_parse_issue': ('chunk_boundary_issue table_parse_issue list_structure_issue formula_parse_issue '
                                'document_parse_missing_text').split(),
    'other': ('generation_missed_evidence generation_hallucination answer_not_addressing_question '
              'multi_hop_reasoning_failure structured_content_reasoning_failure query_design_issue '
              'judge_correctness_misjudge judge_faithfulness_misjudge judge_recall_metric_mismatch '
              'judge_reason_score_conflict').split(),
    'insufficient_evidence': [],
    # Evidence access itself failed (trace missing/unreadable): an infra problem, never a repair target.
    'infra_failure': [],
}
CATEGORIES = set(SUBCATEGORIES)
NON_REPAIRABLE_CATEGORIES = {'insufficient_evidence', 'infra_failure'}
LLM_FINE_CATEGORIES = CATEGORIES - {'rerank_issue'} - NON_REPAIRABLE_CATEGORIES
JUDGE_FIELDS = ('quality_label', 'failure_type', 'answer_correctness', 'faithfulness', 'doc_recall', 'context_recall')
HIT_CATEGORY = {'dataset': 'dataset_or_reference_issue', 'agentic': 'agentic_tool_issue',
                'retrieval': 'retrieval_issue', 'rerank': 'rerank_issue', 'chunking': 'chunking_or_parse_issue',
                'other': 'other'}


class CaseCoarseClassificationOperation:
    def execute(self, ctx: OperationContext) -> OperationOutput:
        report_ref = ArtifactRef.parse(str(ctx.params.get('eval_report_ref') or ''))
        case_id = validate_case_id(str(ctx.params.get('case_id') or ''))
        default_id = f'case_coarse_classification_{case_id}'
        output_id = validate_id(str(ctx.params.get('output_id') or default_id), 'output_id')
        report = typed_payload(ctx, report_ref, 'EvalReport')
        bad = next((row for row in report.get('bad_cases') or [] if str(row.get('case_id') or '') == case_id), None)
        if bad:
            judge_ref = ArtifactRef.parse(str(bad.get('judge_result_ref') or ''))
        elif ctx.params.get('calibration'):
            # Calibration runs classify known-good cases so the classifier's false-positive rate is observable.
            refs = (ArtifactRef.parse(str(raw)) for raw in report.get('judge_result_refs') or [])
            judge_ref = next((r for r in refs if str(ctx.artifact_graph.get(r).get('case_id') or '') == case_id),
                             None)
            if judge_ref is None:
                raise ValueError(f'JudgeResult not found in EvalReport for calibration case: {case_id}')
        else:
            raise ValueError(f'case is not a badcase in EvalReport: {case_id}')
        dataset_ref = ArtifactRef.parse(str(report.get('eval_dataset_ref') or ''))
        dataset = typed_payload(ctx, dataset_ref, 'EvalDataset')
        case_ids, case_refs = list(dataset.get('case_ids') or []), list(dataset.get('case_refs') or [])
        if len(case_ids) != len(case_refs) or case_id not in case_ids:
            raise ValueError(f'case_id not found in EvalDataset: {case_id}')
        judge = typed_payload(ctx, judge_ref, 'JudgeResult')
        rag_ref = ArtifactRef.parse(str(judge.get('rag_answer_ref') or ''))
        case_ref = ArtifactRef.parse(str(case_refs[case_ids.index(case_id)]))
        case, rag = typed_payload(ctx, case_ref, 'DatasetCase'), typed_payload(ctx, rag_ref, 'RagAnswer')
        if str(case.get('id') or '') != case_id: raise ValueError(f'{case_ref} payload id mismatch')
        expected = {'case_id': case_id, 'eval_dataset_ref': str(dataset_ref), 'case_ref': str(case_ref)}
        check_fields('RagAnswer', rag, expected)
        check_fields('JudgeResult', judge, expected | {'rag_answer_ref': str(rag_ref)})
        trace_id = str(rag.get('trace_id') or judge.get('trace_id') or '').strip()
        ctx.report_progress(phase='coarse_classify', status='running', message='classifying bad case',
                            current_item=case_id, detail={'trace_used': True})
        source_message_id = str(ctx.params.get('source_message_id') or '')
        try:
            trace = load_trace_payload(ctx, trace_id, rag)
        except ValueError as exc:
            payload = infra_failure_payload(report_ref, dataset_ref, case_ref, rag_ref, judge_ref, case, judge,
                                            str(exc)[:200], source_message_id)
        else:
            payload = classify_payload(report_ref, dataset_ref, case_ref, rag_ref, judge_ref, case, rag, judge,
                                       trace_id, trace, source_message_id)
        payload['calibration'] = bool(ctx.params.get('calibration'))
        ctx.report_progress(phase='coarse_classify', status='success',
                            message=f"coarse classified as {payload['coarse_category']}", current_item=case_id,
                            detail={'coarse_category': payload['coarse_category'], 'trace_used': True})
        return OperationOutput([ArtifactDraft(output_id, 'CaseCoarseClassification', payload, ctx.operation_run_id,
                                              input_refs=[report_ref, dataset_ref, case_ref, rag_ref, judge_ref])])


def classify_payload(report_ref, dataset_ref, case_ref, rag_ref, judge_ref, case, rag, judge, trace_id, trace,
                     source_message_id=''):
    ref_docs, ref_chunks = values(case.get('reference_doc_ids')), values(case.get('reference_chunk_ids'))
    ref_names = values(case.get('reference_doc'))
    final_docs, final_chunks = values(rag.get('doc_ids')), values(rag.get('chunk_ids'))
    nodes = flatten_trace(trace or {}, trace_id)
    roles = {role: sum(1 for node in nodes if node['role'] == role) for role in sorted({n['role'] for n in nodes})}
    contexts_text = json.dumps(rag.get('contexts') or [], ensure_ascii=False)
    evidence = {
        'refs': {'eval_report_ref': str(report_ref), 'eval_dataset_ref': str(dataset_ref),
                 'case_ref': str(case_ref), 'rag_answer_ref': str(rag_ref), 'judge_result_ref': str(judge_ref)},
        'summary': {key: judge.get(key) for key in JUDGE_FIELDS}
        | {'question_type': str(case.get('question_type') or ''), 'trace_role_counts': roles},
        'case': case, 'rag': rag, 'judge': judge, 'nodes': nodes,
        'searches': kb_searches(nodes, ref_docs, ref_chunks, ref_names),
        'ref_docs': ref_docs, 'ref_chunks': ref_chunks, 'ref_names': ref_names,
        'final_docs': final_docs, 'final_chunks': final_chunks,
        'final_doc_hit': bool(final_docs & ref_docs) or any(n and n in contexts_text for n in ref_names),
        'final_chunk_hit': bool(final_chunks & ref_chunks),
    }
    result = _classify(evidence)
    hits, category = result.pop('rule_hits'), result['coarse_category']
    return {
        'case_id': str(case.get('id') or judge.get('case_id') or ''),
        **evidence['refs'], **result, 'trace_used': True,
        'evidence': {'summary': evidence['summary'], 'rule_hits': hits},
        'next_step': {'operation_type': 'CaseFineClassificationOperation',
                      'allowed_subcategories': SUBCATEGORIES[category],
                      'llm_allowed': category in LLM_FINE_CATEGORIES},
        'source_message_id': source_message_id,
    }


def infra_failure_payload(report_ref, dataset_ref, case_ref, rag_ref, judge_ref, case, judge, error,
                          source_message_id=''):
    return {
        'case_id': str(case.get('id') or judge.get('case_id') or ''),
        'eval_report_ref': str(report_ref), 'eval_dataset_ref': str(dataset_ref), 'case_ref': str(case_ref),
        'rag_answer_ref': str(rag_ref), 'judge_result_ref': str(judge_ref),
        'coarse_category': 'infra_failure', 'coarse_reason': f'trace evidence unavailable: {error}',
        'confidence': 'high', 'trace_used': False,
        'evidence': {'summary': {key: judge.get(key) for key in JUDGE_FIELDS}, 'rule_hits': []},
        'missing_evidence': [error],
        'next_step': {'operation_type': 'CaseFineClassificationOperation', 'allowed_subcategories': [],
                      'llm_allowed': False},
        'source_message_id': source_message_id,
    }


def _classify(e):
    case, rag, judge = e['case'], e['rag'], e['judge']
    complete = all(str(case.get(k) or '').strip() for k in ('question', 'answer'))
    if not complete or not e['ref_docs'] or not e['ref_chunks']:
        observed = {'question': bool(case.get('question')), 'answer': bool(case.get('answer')),
                    'reference_doc_ids': sorted(e['ref_docs']), 'reference_chunk_ids': sorted(e['ref_chunks'])}
        return _result('dataset_or_reference_issue', 'DatasetCase missing question, answer or references', 'high',
                       [_hit('dataset.missing_reference', 'dataset', 'artifact', e, observed=observed)])
    if errors := [node for node in e['nodes'] if _tool_failures(node)]:
        node = next((item for item in errors if item['role'] in {'kb_search', 'tool_manager', 'tool_call'}), errors[0])
        return _result('agentic_tool_issue', 'agentic tool trace contains tool error', 'high', [
            _hit('agentic.tool_error', 'tool', 'trace', e, node=node, observed={'tool_errors': _tool_failures(node)})])
    if not e['searches'] and not (e['final_doc_hit'] or e['final_chunk_hit']):
        # No kb_search node in the trace means the agent never invoked retrieval:
        # an agent control-flow defect, not a retriever-stage miss.
        return _result('agentic_tool_issue', 'agent did not call kb_search', 'high',
                       [_hit('agentic.no_kb_search', 'tool', 'trace', e, observed={'kb_search_nodes': 0})])
    hits = {key: hit_union(e['searches'], key) for key in HIT_PATHS}
    retrievers = [node for node in e['nodes'] if node['role'] == 'retriever' and node['in_kb_search']]
    rerank = [node for node in e['nodes'] if node['role'] == 'reranker' and node['in_kb_search']]
    for kind, refs, hit_key, rule in (('doc', e['ref_docs'], 'retriever_doc_hits', 'retrieval.doc_miss'),
                                      ('chunk', e['ref_chunks'], 'retriever_chunk_hits', 'retrieval.chunk_miss')):
        if retrievers and refs and (missing := refs - hits[hit_key]):
            return _result('retrieval_issue', f'reference {kind}s missing from retriever output', 'high', [
                _hit(rule, 'retriever', 'trace', e, node=best_node(retrievers, refs) or retrievers[0],
                     observed=_obs(e['searches'], **{f'missing_{kind}_ids': missing}))])
    if e['searches'] and not retrievers and not (e['final_doc_hit'] or e['final_chunk_hit']):
        return _result('retrieval_issue', 'kb_search trace did not expose retriever evidence', 'medium', [
            _hit('retrieval.kb_search_no_retriever_hit', 'kb_search', 'trace', e, node=e['searches'][0]['node'],
                 observed=_obs(e['searches']))], ['retriever_trace'])
    for kind, input_key, output_key, rule in (
        ('doc', 'rerank_input_doc_hits', 'rerank_output_doc_hits', 'rerank.drop_reference_doc'),
        ('chunk', 'rerank_input_chunk_hits', 'rerank_output_chunk_hits', 'rerank.drop_reference_chunk'),
    ):
        if dropped := hits[input_key] - hits[output_key]:
            node = best_node(rerank, dropped) or (rerank[0] if rerank else e['searches'][0]['node'])
            return _result('rerank_issue', f'reference {kind}s entered rerank but disappeared', 'high', [
                _hit(rule, 'reranker', 'trace', e, node=node,
                     observed=_obs(e['searches'], **{f'missing_{kind}_ids': dropped}))])
    if missing := e['ref_chunks'] - e['final_chunks']:
        node = best_node(rerank, e['ref_chunks']) or best_node(retrievers, e['ref_chunks'])
        node = node or (e['searches'][0]['node'] if e['searches'] else None)
        return _result('retrieval_issue', 'reference chunks are missing from final RAG contexts', 'high', [
            _hit('retrieval.final_context_drop', 'final_context', 'trace', e, node=node,
                 observed=_obs(e['searches'], missing_chunk_ids=missing, final_chunk_ids=e['final_chunks'],
                               final_hit_chunk_ids=e['ref_chunks'] & e['final_chunks']))])
    texts = [str(item) for item in (case.get('reference_context') or []) + (rag.get('contexts') or [])]
    if any('\ufffd' in text or '\x00' in text for text in texts):
        return _result('chunking_or_parse_issue', 'source text is structurally unusable', 'medium',
                       [_hit('chunking.parse_garbled_text', 'chunking', 'artifact', e)])
    qtype = str(case.get('question_type') or '')
    if qtype in {'table_list', 'formula'} and rag.get('contexts') and not any(has_structure(t) for t in texts):
        return _result('chunking_or_parse_issue', 'structured source text lost markers', 'medium',
                       [_hit('chunking.structured_content_lost', 'chunking', 'artifact', e)])
    low = min(score(judge.get('answer_correctness')), score(judge.get('faithfulness'))) < 0.8
    if low or str(judge.get('quality_label') or '') in {'bad', 'partial'}:
        fields = ('quality_label', 'failure_type', 'answer_correctness', 'faithfulness', 'reason', 'defect')
        return _result('other', 'retrieval evidence reached generation/judge stage', 'medium', [
            _hit('other.semantic_or_generation', 'generation_or_judge', 'artifact', e,
                 observed={key: judge.get(key) for key in fields})])
    return _result('other', 'no deterministic coarse failure rule matched', 'low',
                   [_hit('other.no_rule_match', 'coarse', 'artifact', e)])


def _result(category, reason, confidence, hits, missing=None):
    if category not in CATEGORIES: raise ValueError(f'invalid coarse category: {category}')
    return {'coarse_category': category, 'coarse_reason': reason, 'confidence': confidence, 'rule_hits': hits,
            'missing_evidence': sorted(set(missing or []))}


def _hit(rule_id, stage, source, e, *, node=None, observed=None):
    return {'rule_id': rule_id, 'category': HIT_CATEGORY.get(rule_id.split('.', 1)[0], 'other'),
            'stage': stage, 'source': source, 'trace_node': node_brief(node) if node else {},
            'expected': {'reference_doc_ids': sorted(e['ref_docs']), 'reference_chunk_ids': sorted(e['ref_chunks']),
                         'reference_doc': sorted(e['ref_names'])},
            'observed': observed or {},
            'field_paths': {'reference_doc_ids': 'DatasetCase.reference_doc_ids',
                            'reference_chunk_ids': 'DatasetCase.reference_chunk_ids',
                            'rag_doc_ids': 'RagAnswer.doc_ids', 'rag_chunk_ids': 'RagAnswer.chunk_ids'}}


def _obs(searches, **extra):
    return {key: sorted(value) if isinstance(value, set) else value for key, value in extra.items()} | {
        'retriever_doc_hits': sorted(hit_union(searches, 'retriever_doc_hits')),
        'retriever_chunk_hits': sorted(hit_union(searches, 'retriever_chunk_hits'))}


def _tool_failures(node):
    if node['role'] not in {'tool_manager', 'tool_call', 'kb_search'}: return []
    status = str(node.get('status') or '')
    failures = [{'status': status}] if status.lower() in {'failed', 'error', 'exception', 'timeout'} else []
    text = json.dumps(node.get('raw') or {}, ensure_ascii=False)
    if any(token in text.lower() for token in ('error', 'exception', 'traceback', 'timeout', '"success": false')):
        failures.append({'error': short(text, 500)})
    return failures[:5]

from __future__ import annotations

import json
import re
from typing import Any

from ...artifacts import ArtifactDraft, ArtifactRef
from ...runtime import AdapterCall, OperationContext, OperationOutput, evo_llm
from .utils import QUESTION_TYPES, bounded_int, json_object, strings, validate_case_id

DIFFICULTIES = {'easy', 'medium', 'hard'}
MULTI_HOP = {'single_doc_multi_hop', 'multi_doc_multi_hop'}
FINAL_PROMPT = """生成 LazyMind 评测样本。只基于证据片段，问题独立完整，答案可验证，不用外部知识。
case_id:{case_id}
question_type:{question_type}
difficulty:{difficulty}
instruction:{instruction}
证据:
{refs}"""
PLAN_PROMPT = """只输出 JSON，不生成 question/answer。为 {question_type} 选择 2-3 个 chunk_id。
要求: single_doc_multi_hop 只能同一 doc_id；multi_doc_multi_hop 至少两个 doc_id；只能选候选。
输出:{{"selected_chunk_ids":["..."],"instruction":"...","prompt_focus":"..."}}
case_id:{case_id}
difficulty:{difficulty}
user_instruction:{user_instruction}
candidates:{candidates}"""


class PrepareDatasetCaseOperation:
    def __init__(self, llm: Any | None = None):
        self.llm = llm

    def execute(self, ctx: OperationContext) -> OperationOutput:
        snapshot_ref = ctx.input_refs[0] if ctx.input_refs else ArtifactRef.parse(
            str(ctx.params.get('source_snapshot_ref') or '')
        )
        snapshot = ctx.artifact_graph.get(snapshot_ref)
        case_id = validate_case_id(str(ctx.params.get('case_id') or ctx.params.get('output_case_id') or ''))
        qtype, difficulty = str(ctx.params.get('question_type') or '').strip(), str(
            ctx.params.get('difficulty') or 'medium'
        ).strip()
        if qtype not in QUESTION_TYPES or difficulty not in DIFFICULTIES:
            raise ValueError('case_id, valid question_type and valid difficulty are required')
        preview_chars = bounded_int(ctx.params.get('preview_chars'), 200, 20, 2000)
        user_note = str(ctx.params.get('user_instruction') or '').strip()
        units = _units(ctx, snapshot, set(strings(ctx.params.get('doc_ids'))),
                       set(strings(ctx.params.get('chunk_ids'))))
        ctx.report_progress(phase='select_candidates', status='running', message='selected candidate source units',
                            current_item=case_id,
                            detail={'question_type': qtype, 'candidate_count': len(units),
                                    'requires_llm_plan': qtype in MULTI_HOP})
        selected, focus = self._select(ctx, case_id, qtype, difficulty, user_note, units)
        _validate(qtype, selected)
        instruction = _instruction(qtype, difficulty, user_note, focus)
        refs = '\n\n'.join(f"[{i}] {u['filename']} / {u['chunk_id']} / {u['unit_type']}\n{u['content']}"
                           for i, u in enumerate(selected, 1))
        payload = {'case_id': case_id, 'question_type': qtype, 'difficulty': difficulty,
                   'doc_reference': _doc_ref(selected),
                   'context_reference': _ctx_ref(selected, preview_chars), 'instruction': instruction,
                   'prompt': FINAL_PROMPT.format(case_id=case_id, question_type=qtype, difficulty=difficulty,
                                                 instruction=instruction, refs=refs),
                   'source_snapshot_ref': str(snapshot_ref),
                   'source_message_id': str(ctx.params.get('source_message_id') or '')}
        ctx.report_progress(phase='prepare_case', status='success', message='case preparation ready',
                            current_item=case_id,
                            detail={'artifact_id': f'case_preparation_{case_id}',
                                    'chunk_count': len(selected), 'doc_count': len({u['doc_id'] for u in selected})})
        return OperationOutput([ArtifactDraft(f'case_preparation_{case_id}', 'CasePreparation', payload,
                                              ctx.operation_run_id, input_refs=[snapshot_ref])])

    def _select(self, ctx, case_id, qtype, difficulty, user_note, units):
        if qtype == 'single_hop':
            candidates = _first(units, lambda u: u['unit_type'] == 'paragraph', 'single_hop requires paragraph')
            return [candidates[_case_index(case_id) % len(candidates)]], ''
        if qtype == 'table_list':
            return _first(units, lambda u: u['unit_type'] in {'table', 'list', 'mixed'}, 'table/list required')[:2], ''
        if qtype == 'formula':
            selected = _first(units, lambda u: u['unit_type'] in {'formula', 'mixed'}, 'formula required')[:1]
            selected += _first([u for u in units if u not in selected and u['doc_id'] == selected[0]['doc_id']],
                               lambda u: u['unit_type'] in {'paragraph', 'mixed'}, 'formula context required')[:1]
            return selected, ''
        return self._multi(ctx, case_id, qtype, difficulty, user_note, units)

    def _multi(self, ctx, case_id, qtype, difficulty, user_note, units):
        candidates = _multi_candidates(qtype, units)
        by_chunk = {unit['chunk_id']: unit for unit in candidates}
        candidates_json = json.dumps([{k: u[k] for k in ('doc_id', 'filename', 'chunk_id', 'unit_type', 'content')}
                                      for u in candidates], ensure_ascii=False)
        feedback = ''
        for attempt in range(2):
            request = {'case_id': case_id, 'attempt': attempt + 1, 'prompt': PLAN_PROMPT.format(
                question_type=qtype, case_id=case_id, difficulty=difficulty, user_instruction=user_note,
                candidates=candidates_json
            ) + feedback}
            call = AdapterCall(f'llm.prepare_dataset_case.{qtype}', lambda p: self._model()(p['prompt'], stream=False))
            result = call.run(ctx, request, phase='prepare_case_plan', item_ref=case_id)
            plan = json_object(result.response)
            chunk_ids = strings(plan.get('selected_chunk_ids'))
            selected = [by_chunk[item] for item in chunk_ids if item in by_chunk]
            try:
                bad = [item for item in chunk_ids if item not in by_chunk]
                if bad:
                    raise ValueError(f'selected chunk outside candidates: {bad}')
                if 'question' in plan or 'answer' in plan:
                    raise ValueError('prepare plan must not include question or answer')
                _validate(qtype, selected)
                return selected, '\n'.join(strings([plan.get('instruction'), plan.get('prompt_focus')]))
            except ValueError as exc:
                feedback = f'\n上次选择无效：{exc}。只能从候选 chunk_id 选 2 到 3 个：{sorted(by_chunk)}。'
        raise ValueError('prepare plan selected invalid chunks after retry')

    def _model(self):
        if self.llm is None:
            self.llm = evo_llm()
        return self.llm


def _units(ctx, snapshot: dict[str, Any], doc_ids: set[str], chunk_ids: set[str]) -> list[dict[str, Any]]:
    out = []
    for ref in [ArtifactRef.parse(item) for item in snapshot.get('source_unit_page_refs', [])]:
        ctx.check_interrupt()
        for unit in ctx.artifact_graph.get(ref).get('source_units', []):
            normalized = _unit(unit)
            if (chunk_ids and normalized['chunk_id'] not in chunk_ids) or (
                not chunk_ids and doc_ids and normalized['doc_id'] not in doc_ids
            ):
                continue
            out.append(normalized)
    if not out:
        raise ValueError('no source units matched prepare scope')
    return out


def _unit(unit) -> dict[str, Any]:
    return {'source_unit_ref': str(unit.get('source_unit_ref') or ''), 'doc_ref': str(unit.get('doc_ref') or ''),
            'doc_id': str(unit.get('doc_id') or ''), 'filename': str(unit.get('filename') or ''),
            'chunk_id': str(unit.get('segment_id') or unit.get('chunk_id') or unit.get('source_unit_ref') or ''),
            'unit_type': str(unit.get('unit_type') or 'paragraph'), 'content': str(unit.get('content') or '')}


def _multi_candidates(qtype: str, units: list[dict[str, Any]]) -> list[dict[str, Any]]:
    docs: dict[str, list[dict[str, Any]]] = {}
    for unit in units:
        docs.setdefault(unit['doc_id'], []).append(unit)
    if qtype == 'single_doc_multi_hop':
        return next((items[:8] for items in docs.values() if len(items) >= 2), None) or _raise(
            'single_doc_multi_hop requires at least two chunks from one document')
    if len(docs) < 2:
        raise ValueError('multi_doc_multi_hop requires chunks from at least two documents')
    return [unit for items in list(docs.values())[:3] for unit in items[:3]][:10]


def _validate(qtype: str, units: list[dict[str, Any]]) -> None:
    docs = {unit['doc_id'] for unit in units}
    if qtype == 'single_doc_multi_hop' and (not 2 <= len(units) <= 3 or len(docs) != 1):
        raise ValueError('single_doc_multi_hop plan must select 2-3 chunks from one document')
    if qtype == 'multi_doc_multi_hop' and (not 2 <= len(units) <= 3 or len(docs) < 2):
        raise ValueError('multi_doc_multi_hop plan must select 2-3 chunks from at least two documents')
    if not units:
        raise ValueError(f'{qtype} has no selected source units')


def _first(units, predicate, error):
    selected = [unit for unit in units if predicate(unit)]
    if not selected:
        raise ValueError(error)
    return selected


def _case_index(case_id: str) -> int:
    match = re.search(r'(\d+)$', case_id)
    return max(0, int(match.group(1)) - 1) if match else sum(map(ord, case_id))


def _instruction(qtype, difficulty, user_note, focus):
    parts = [f'生成一个 {qtype}、{difficulty} 难度的问题，答案必须能由证据片段验证。']
    parts.extend([f'证据关系：{focus}'] if focus else [])
    parts.extend([f'用户要求：{user_note}'] if user_note else [])
    return '\n'.join(parts)


def _doc_ref(units) -> list[dict[str, Any]]:
    docs = {}
    for unit in units:
        docs.setdefault(unit['doc_id'], {'doc_id': unit['doc_id'], 'filename': unit['filename'],
                                         'doc_ref': unit['doc_ref']})
    return list(docs.values())


def _ctx_ref(units, preview_chars) -> list[dict[str, Any]]:
    return [{'chunk_id': u['chunk_id'], 'filename': u['filename'], 'content_preview': u['content'][:preview_chars],
             'doc_id': u['doc_id'], 'unit_type': u['unit_type'], 'source_unit_ref': u['source_unit_ref']}
            for u in units]


def _raise(message):
    raise ValueError(message)

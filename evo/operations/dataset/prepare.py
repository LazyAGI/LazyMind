import json
import re
from collections import Counter
from typing import Any

from ...artifacts import ArtifactDraft, ArtifactRef
from ... import validate_id
from ...runtime import AdapterCall, OperationContext, OperationOutput, evo_llm
from .utils import QUESTION_TYPES, bounded_int, json_object, progress, strings, validate_case_id

DIFFICULTIES = {'easy', 'medium', 'hard'}
MULTI_HOP = {'single_doc_multi_hop', 'multi_doc_multi_hop'}
# Generation policy: difficulty drives evidence count and reasoning depth, not just a prompt label.
DIFFICULTY_POLICY = {
    'easy': {'chunks': (2, 2), 'reasoning': '直接事实型问题，答案可由单个证据片段直接验证。'},
    'medium': {'chunks': (2, 3), 'reasoning': '需要综合证据片段中的信息，答案不能照抄单句。'},
    'hard': {'chunks': (3, 3), 'reasoning': '需要跨片段多步推理或处理结构化内容，问题不直接暗示证据位置。'},
}
FINAL_PROMPT = ('生成 LazyMind 评测样本。只基于证据片段，问题独立完整，答案可验证，不用外部知识。\n'
                'case_id:{case_id}\nquestion_type:{question_type}\ndifficulty:{difficulty}\n'
                'instruction:{instruction}\n证据:\n{refs}')
PLAN_PROMPT = ('只输出 JSON，不生成 question/answer。为 {question_type} 选择 {chunk_range} 个 chunk_id。\n'
               '要求: single_doc_multi_hop 只能同一 doc_id；multi_doc_multi_hop 至少两个 doc_id；只能选候选。\n'
               '输出:{{"selected_chunk_ids":["..."],"instruction":"...","prompt_focus":"..."}}\n'
               'case_id:{case_id}\ndifficulty:{difficulty}\nuser_instruction:{user_instruction}\n'
               'candidates:{candidates}')
GEN_PROMPT = ('严格基于生成计划生成一条可验证 LazyMind 评测样本，只输出 JSON。\n'
              '问题必须独立完整，不能出现“参考内容/证据片段/上述/本文”等来源指代；答案只能来自证据片段。\n'
              '英文问题涉及论文/文章/框架时必须写出标题、文件名或 arXiv id，不能只写 the paper/this paper。\n'
              'grading_guidance 写给 judge，说明覆盖哪些核心事实即可。\n\n生成计划:\n{prompt}\n\n'
              '输出字段: question, answer, grading_guidance, generate_reason')
SOURCE_PREFIXES = ('根据参考内容，', '根据参考内容', '根据证据片段，', '根据证据片段', '在参考内容中，', '在参考内容中',
                   '在证据片段中，', '在证据片段中', '参考内容中，', '证据片段中，')
SOURCE_DEICTICS = ('参考内容', '证据片段', '给定信息', '根据上述', '根据上文', '上述', '上文', '本文', '参考材料',
                   '给定材料', '参考上下文', '给定上下文',
                   'according to the paper', 'according to the passage', 'according to the text', 'the paper',
                   'this paper', 'the passage', 'the text', 'the provided')


class PrepareDatasetCaseOperation:
    def __init__(self, llm: Any | None = None):
        self.llm = llm

    def execute(self, ctx: OperationContext) -> OperationOutput:
        raw_ref = str(ctx.params.get('source_snapshot_ref') or '')
        snapshot_ref = ctx.input_refs[0] if ctx.input_refs else ArtifactRef.parse(raw_ref)
        snapshot = ctx.artifact_graph.get(snapshot_ref)
        case_id = validate_case_id(str(ctx.params.get('case_id') or ctx.params.get('output_case_id') or ''))
        qtype = str(ctx.params.get('question_type') or '').strip()
        difficulty = str(ctx.params.get('difficulty') or 'medium').strip()
        if qtype not in QUESTION_TYPES or difficulty not in DIFFICULTIES:
            raise ValueError('case_id, valid question_type and valid difficulty are required')
        preview_chars = bounded_int(ctx.params.get('preview_chars'), 200, 20, 2000)
        user_note = str(ctx.params.get('user_instruction') or '').strip()
        doc_ids, chunk_ids = set(strings(ctx.params.get('doc_ids'))), set(strings(ctx.params.get('chunk_ids')))
        units = []
        for ref in [ArtifactRef.parse(item) for item in snapshot.get('source_unit_page_refs', [])]:
            ctx.check_interrupt()
            for unit in ctx.artifact_graph.get(ref).get('source_units', []):
                g = unit.get
                n = {'source_unit_ref': str(g('source_unit_ref') or ''), 'doc_ref': str(g('doc_ref') or ''),
                     'doc_id': str(g('doc_id') or ''), 'filename': str(g('filename') or ''),
                     'chunk_id': str(g('segment_id') or g('chunk_id') or g('source_unit_ref') or ''),
                     'unit_type': str(g('unit_type') or 'paragraph'), 'content': str(g('content') or '')}
                if chunk_ids and n['chunk_id'] not in chunk_ids:
                    continue
                if not chunk_ids and doc_ids and n['doc_id'] not in doc_ids:
                    continue
                units.append(n)
        if not units:
            raise ValueError('no source units matched prepare scope')
        progress(ctx, 'select_candidates', 'running', 'selected candidate source units', current_item=case_id,
                 detail={'question_type': qtype, 'candidate_count': len(units),
                         'requires_llm_plan': qtype in MULTI_HOP})
        selected, focus = self._select(ctx, case_id, qtype, difficulty, user_note, units)
        self._validate(qtype, selected, difficulty)
        parts = [f'生成一个 {qtype}、{difficulty} 难度的问题，答案必须能由证据片段验证。',
                 f"难度要求：{DIFFICULTY_POLICY[difficulty]['reasoning']}"]
        parts.extend([f'证据关系：{focus}'] if focus else [])
        parts.extend([f'用户要求：{user_note}'] if user_note else [])
        instruction = '\n'.join(parts)
        refs = '\n\n'.join(f"[{i}] {u['filename']} / {u['chunk_id']} / {u['unit_type']}\n{u['content']}"
                           for i, u in enumerate(selected, 1))
        docs = {}
        for u in selected:
            docs.setdefault(u['doc_id'], {'doc_id': u['doc_id'], 'filename': u['filename'], 'doc_ref': u['doc_ref']})
        context_reference = [{'chunk_id': u['chunk_id'], 'filename': u['filename'],
                              'content_preview': u['content'][:preview_chars], 'doc_id': u['doc_id'],
                              'unit_type': u['unit_type'], 'source_unit_ref': u['source_unit_ref']}
                             for u in selected]
        payload = {'case_id': case_id, 'question_type': qtype, 'difficulty': difficulty,
                   'doc_reference': list(docs.values()),
                   'context_reference': context_reference, 'instruction': instruction,
                   'prompt': FINAL_PROMPT.format(case_id=case_id, question_type=qtype, difficulty=difficulty,
                                                 instruction=instruction, refs=refs),
                   'source_snapshot_ref': str(snapshot_ref),
                   'source_message_id': str(ctx.params.get('source_message_id') or '')}
        progress(ctx, 'prepare_case', 'success', 'case preparation ready', current_item=case_id,
                 detail={'artifact_id': f'case_preparation_{case_id}', 'chunk_count': len(selected),
                         'doc_count': len({u['doc_id'] for u in selected})})
        return OperationOutput([ArtifactDraft(f'case_preparation_{case_id}', 'CasePreparation', payload,
                                              ctx.operation_run_id, input_refs=[snapshot_ref])])

    def _select(self, ctx, case_id, qtype, difficulty, user_note, units):
        if qtype == 'single_hop':
            candidates = _first(units, lambda u: u['unit_type'] == 'paragraph', 'single_hop requires paragraph')
            if len(candidates) >= 3:
                # Single-hop evidence band: easy uses shorter units, hard uses longer/denser units.
                ranked = sorted(candidates, key=lambda u: len(u['content']))
                third = len(ranked) // 3
                bands = {'easy': ranked[:third or 1], 'medium': ranked[third:2 * third] or ranked,
                         'hard': ranked[2 * third:] or ranked}
                candidates = bands[difficulty]
            match = re.search(r'(\d+)$', case_id)
            index = max(0, int(match.group(1)) - 1) if match else sum(map(ord, case_id))
            return [candidates[index % len(candidates)]], ''
        if qtype == 'table_list':
            return _first(units, lambda u: u['unit_type'] in {'table', 'list', 'mixed'}, 'table/list required')[:2], ''
        if qtype == 'formula':
            selected = _first(units, lambda u: u['unit_type'] in {'formula', 'mixed'}, 'formula required')[:1]
            selected += _first([u for u in units if u not in selected and u['doc_id'] == selected[0]['doc_id']],
                               lambda u: u['unit_type'] in {'paragraph', 'mixed'}, 'formula context required')[:1]
            return selected, ''
        return self._multi(ctx, case_id, qtype, difficulty, user_note, units)

    def _multi(self, ctx, case_id, qtype, difficulty, user_note, units):
        docs: dict[str, list[dict[str, Any]]] = {}
        for unit in units:
            docs.setdefault(unit['doc_id'], []).append(unit)
        if qtype == 'single_doc_multi_hop':
            candidates = next((items[:8] for items in docs.values() if len(items) >= 2), None)
            if not candidates:
                raise ValueError('single_doc_multi_hop requires at least two chunks from one document')
        else:
            if len(docs) < 2:
                raise ValueError('multi_doc_multi_hop requires chunks from at least two documents')
            candidates = [items[0] for items in docs.values() if items][:10]
        by_chunk = {unit['chunk_id']: unit for unit in candidates}
        candidates_json = json.dumps([{k: u[k] for k in ('doc_id', 'filename', 'chunk_id', 'unit_type', 'content')}
                                      for u in candidates], ensure_ascii=False)
        low, high = DIFFICULTY_POLICY[difficulty]['chunks']
        chunk_range, feedback = str(low) if low == high else f'{low}-{high}', ''
        for attempt in range(2):
            request = {'case_id': case_id, 'attempt': attempt + 1, 'prompt': PLAN_PROMPT.format(
                question_type=qtype, case_id=case_id, difficulty=difficulty, user_instruction=user_note,
                candidates=candidates_json, chunk_range=chunk_range,
            ) + feedback}
            call = AdapterCall(f'llm.prepare_dataset_case.{qtype}', lambda p: _model(self)(p['prompt'], stream=False))
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
                self._validate(qtype, selected, difficulty)
                return selected, '\n'.join(strings([plan.get('instruction'), plan.get('prompt_focus')]))
            except ValueError as exc:
                feedback = f'\n上次选择无效：{exc}。只能从候选 chunk_id 选 {chunk_range} 个：{sorted(by_chunk)}。'
        raise ValueError('prepare plan selected invalid chunks after retry')

    def _validate(self, qtype, units, difficulty) -> None:
        docs = {unit['doc_id'] for unit in units}
        if qtype in MULTI_HOP:
            low, high = DIFFICULTY_POLICY[difficulty]['chunks']
            if not low <= len(units) <= high:
                raise ValueError(f'{qtype} {difficulty} plan must select {low}-{high} chunks, got {len(units)}')
            if qtype == 'single_doc_multi_hop' and len(docs) != 1:
                raise ValueError('single_doc_multi_hop plan must select chunks from one document')
            if qtype == 'multi_doc_multi_hop' and len(docs) < 2:
                raise ValueError('multi_doc_multi_hop plan must select chunks from at least two documents')
        if not units:
            raise ValueError(f'{qtype} has no selected source units')


class GenerateDatasetCaseOperation:
    def __init__(self, llm: Any | None = None):
        self.llm = llm

    def execute(self, ctx: OperationContext) -> OperationOutput:
        value = str(ctx.params.get('case_preparation_ref') or '').strip()
        ref = None if not value else ArtifactRef.parse(value) if '@' in value else ctx.artifact_graph.latest_ref(value)
        if ctx.input_refs and (ref is None or ctx.input_refs[0].artifact_id == ref.artifact_id):
            ref = ctx.input_refs[0]
        if ref is None:
            raise ValueError('case_preparation_ref is required')
        plan = ctx.artifact_graph.get(ref)
        progress(ctx, 'generate_case', 'running', 'generating dataset case', current_item=str(plan['case_id']))
        prompt, feedback, result = GEN_PROMPT.format(prompt=plan['prompt']), '', None
        for attempt in range(2):
            ctx.check_interrupt()
            result = AdapterCall('llm.generate_dataset_case', lambda p: _model(self)(p['prompt'], stream=False)).run(
                ctx, {'case_id': plan['case_id'], 'attempt': attempt + 1, 'prompt': prompt + feedback},
                phase='generate_case', item_ref=str(plan['case_id']))
            try:
                payload = self._case_payload(plan, json_object(result.response), str(ref))
                break
            except (ValueError, TypeError, KeyError, json.JSONDecodeError) as exc:
                feedback = f'\n\n上次输出无效：{exc}。请只输出合法 JSON，并包含全部必填字段。'
        else:
            raise ValueError('generated case remained invalid after retry')
        progress(ctx, 'generate_case', 'success', 'dataset case generated', current_item=payload['id'],
                 detail={'artifact_id': payload['id'], 'call_id': result.record.call_id})
        return OperationOutput([ArtifactDraft(payload['id'], 'DatasetCase', payload, ctx.operation_run_id, [ref])])

    def _case_payload(self, plan, data, preparation_ref) -> dict[str, Any]:
        contexts = list(plan.get('context_reference') or [])
        question = self._standalone(str(data.get('question') or '').strip())
        answer = str(data.get('answer') or data.get('ground_truth') or '').strip()
        guidance = str(data.get('grading_guidance') or data.get('judge_guidance')
                       or data.get('grading_judge') or data.get('evaluation_guidance') or '').strip()
        if not question or not answer or not guidance:
            raise ValueError('generated case missing question, answer or grading_guidance')
        return {'id': validate_case_id(str(plan['case_id'])), 'question': question, 'answer': answer,
                'question_type': str(plan['question_type']), 'difficulty': str(plan['difficulty']),
                'grading_guidance': guidance,
                'reference_context': [str(item.get('content_preview') or '') for item in contexts],
                'reference_doc': [str(item.get('filename') or '') for item in contexts],
                'reference_doc_ids': [str(item.get('doc_id') or '') for item in contexts],
                'reference_chunk_ids': [str(item.get('chunk_id') or '') for item in contexts],
                'generate_reason': str(data.get('generate_reason') or data.get('reason') or '').strip(),
                'source_preparation_ref': preparation_ref,
                'source_message_id': str(plan.get('source_message_id') or '')}

    def _standalone(self, question) -> str:
        for prefix in SOURCE_PREFIXES:
            if question.startswith(prefix):
                question = question[len(prefix):].strip()
        lower = question.lower()
        named = ("paper '" in lower or 'paper "' in lower or 'paper titled' in lower or 'arxiv' in lower
                 or '.pdf' in lower)
        deictic = any(p in question for p in SOURCE_DEICTICS[:12])
        if deictic or (not named and any(p in lower for p in SOURCE_DEICTICS[12:])):
            raise ValueError('generated question must name the referenced paper/source instead of using deictic '
                             'wording')
        return question


class AssembleDatasetOperation:
    def execute(self, ctx: OperationContext) -> OperationOutput:
        dataset_id = validate_id(str(ctx.params.get('dataset_id') or 'eval_dataset'), 'dataset_id')
        case_ids = [validate_case_id(item) for item in strings(ctx.params.get('case_ids'))]
        if not case_ids or len(case_ids) != len(set(case_ids)):
            raise ValueError('case_ids must be non-empty and unique')
        refs = [ctx.artifact_graph.latest_ref(case_id) for case_id in case_ids]
        refs_by_id = {ref.artifact_id: ref for ref in ctx.input_refs if ref.artifact_id in case_ids}
        refs = [refs_by_id.get(case_id, ref) for case_id, ref in zip(case_ids, refs)]
        cases = []
        for index, (case_id, ref) in enumerate(zip(case_ids, refs), 1):
            ctx.check_interrupt()
            if ctx.artifact_graph.schema_name(ref) != 'DatasetCase':
                raise ValueError(f'artifact is not DatasetCase: {ref}')
            case = ctx.artifact_graph.get(ref)
            missing = [key for key in ('id', 'question', 'answer', 'question_type', 'difficulty') if not case.get(key)]
            if missing:
                raise ValueError(f'{ref} missing required fields: {", ".join(missing)}')
            if str(case['id']) != case_id:
                raise ValueError(f'{ref} payload id mismatch: {case.get("id")} != {case_id}')
            cases.append(case)
            progress(ctx, 'assemble_dataset', 'running', f'assembled {index}/{len(case_ids)} cases',
                     current_item=case_id, done=index, total=len(case_ids))
        preview_keys = ('id', 'question', 'question_type', 'difficulty')
        payload = {'id': dataset_id, 'size': len(cases), 'case_ids': case_ids,
                   'case_refs': [str(ref) for ref in refs], 'stats': self._stats(cases),
                   'checks': self._checks(ctx, cases), 'diff': self._diff(ctx, dataset_id, case_ids, refs),
                   'preview': [{key: case[key] for key in preview_keys} for case in cases[:20]],
                   'source_message_id': str(ctx.params.get('source_message_id') or '')}
        progress(ctx, 'assemble_dataset', 'success', f'assembled dataset with {len(cases)} cases', done=len(cases),
                 current_item=dataset_id, total=len(cases), detail={'ready': payload['checks']['ready']})
        return OperationOutput([ArtifactDraft(dataset_id, 'EvalDataset', payload, ctx.operation_run_id, refs)])

    def _stats(self, cases) -> dict[str, Any]:
        return {'question_type_counts': dict(Counter(str(case.get('question_type') or '') for case in cases)),
                'difficulty_counts': dict(Counter(str(case.get('difficulty') or '') for case in cases)),
                'question_type_x_difficulty': dict(Counter(
                    f"{case.get('question_type') or ''}:{case.get('difficulty') or ''}" for case in cases
                )),
                'doc_counts': dict(Counter(doc for case in cases for doc in strings(case.get('reference_doc_ids'))))}

    def _checks(self, ctx, cases) -> dict[str, Any]:
        errors, warnings = [], []
        for text, count in Counter(re.sub(r'\s+', '', str(case.get('question') or '')).lower()
                                   for case in cases).items():
            if text and count > 1:
                errors.append({'code': 'duplicate_question', 'message': f'duplicate question appears {count} times'})
        # Gate the assembled dataset against a requested difficulty distribution (ratio per label).
        expected = ctx.params.get('difficulty_distribution')
        if isinstance(expected, dict) and expected and cases:
            tolerance = float(ctx.params.get('difficulty_tolerance') or 0.1)
            counts = Counter(str(case.get('difficulty') or '') for case in cases)
            for label, ratio in expected.items():
                actual = counts.get(str(label), 0) / len(cases)
                if abs(actual - float(ratio)) > tolerance:
                    errors.append({'code': 'difficulty_distribution',
                                   'message': f'difficulty {label}: actual {actual:.2f} vs expected '
                                              f'{float(ratio):.2f} (tolerance {tolerance})'})
        for case in cases:
            case_id = str(case.get('id') or '')
            if not strings(case.get('reference_doc_ids')) or not strings(case.get('reference_chunk_ids')):
                warnings.append({'code': 'missing_reference', 'case_id': case_id})
            if not case.get('source_preparation_ref'):
                warnings.append({'code': 'missing_source_preparation_ref', 'case_id': case_id})
        return {'ready': not errors, 'errors': errors, 'warnings': warnings}

    def _diff(self, ctx, dataset_id, case_ids, refs) -> dict[str, Any]:
        try:
            base_ref, base = ctx.artifact_graph.latest_ref(dataset_id), None
            base = ctx.artifact_graph.get(base_ref)
        except KeyError:
            return {'base_ref': '', 'added_case_ids': case_ids, 'removed_case_ids': [],
                    'changed_case_refs': [], 'order_changed': False}
        old_ids, old_refs = list(map(str, base.get('case_ids', []))), list(map(str, base.get('case_refs', [])))
        old_by_id, new_by_id = dict(zip(old_ids, old_refs)), dict(zip(case_ids, map(str, refs)))
        common = set(old_by_id) & set(new_by_id)
        return {'base_ref': str(base_ref),
                'added_case_ids': [case_id for case_id in case_ids if case_id not in old_by_id],
                'removed_case_ids': [case_id for case_id in old_ids if case_id not in new_by_id],
                'changed_case_refs': [case_id for case_id in case_ids if case_id in common
                                      and old_by_id[case_id] != new_by_id[case_id]],
                'order_changed': [case_id for case_id in old_ids if case_id in new_by_id]
                != [case_id for case_id in case_ids if case_id in old_by_id]}


def _model(op):
    if op.llm is None:
        op.llm = evo_llm()
    return op.llm


def _first(units, predicate, error):
    selected = [unit for unit in units if predicate(unit)]
    if not selected:
        raise ValueError(error)
    return selected

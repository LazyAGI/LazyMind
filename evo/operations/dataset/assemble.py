from __future__ import annotations

import re
from collections import Counter
from typing import Any

from ...artifacts import ArtifactDraft, ArtifactRef
from ...ids import validate_id
from ...runtime import OperationContext, OperationOutput
from .models import EvalDataset, artifact_payload
from .utils import strings, validate_case_id


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
            _validate_case(case_id, ref, case)
            cases.append(case)
            ctx.report_progress(
                phase='assemble_dataset', status='running',
                message=f'assembled {index}/{len(case_ids)} cases',
                current_item=case_id, done=index, total=len(case_ids)
            )
        payload = artifact_payload(EvalDataset(
            dataset_id, len(cases), case_ids, [str(ref) for ref in refs],
            _stats(ctx, cases), _checks(ctx, cases), _diff(ctx, dataset_id, case_ids, refs),
            _preview(cases), str(ctx.params.get('source_message_id') or ''),
        ))
        ctx.report_progress(
            phase='assemble_dataset', status='success',
            message=f'assembled dataset with {len(cases)} cases', current_item=dataset_id,
            done=len(cases), total=len(cases), detail={'ready': payload['checks']['ready']}
        )
        return OperationOutput([ArtifactDraft(dataset_id, 'EvalDataset', payload, ctx.operation_run_id, refs)])


def _validate_case(case_id: str, ref: ArtifactRef, case: dict[str, Any]) -> None:
    missing = [key for key in ('id', 'question', 'answer', 'question_type', 'difficulty') if not case.get(key)]
    if missing:
        raise ValueError(f'{ref} missing required fields: {", ".join(missing)}')
    if str(case['id']) != case_id:
        raise ValueError(f'{ref} payload id mismatch: {case.get("id")} != {case_id}')


def _stats(ctx: OperationContext, cases: list[dict[str, Any]]) -> dict[str, Any]:
    docs = Counter(doc for case in cases for doc in strings(case.get('reference_doc_ids')))
    return {
        'question_type_counts': dict(Counter(str(case.get('question_type') or '') for case in cases)),
        'doc_counts': dict(docs),
    }


def _checks(ctx: OperationContext, cases: list[dict[str, Any]]) -> dict[str, Any]:
    errors, warnings = [], []
    for text, count in Counter(_norm(case.get('question')) for case in cases).items():
        if text and count > 1:
            errors.append({'code': 'duplicate_question', 'message': f'duplicate question appears {count} times'})
    for case in cases:
        case_id = str(case.get('id') or '')
        if not strings(case.get('reference_doc_ids')) or not strings(case.get('reference_chunk_ids')):
            warnings.append({'code': 'missing_reference', 'case_id': case_id})
        if not case.get('source_preparation_ref'):
            warnings.append({'code': 'missing_source_preparation_ref', 'case_id': case_id})
    return {'ready': not errors, 'errors': errors, 'warnings': warnings}


def _diff(ctx: OperationContext, dataset_id: str, case_ids: list[str], refs: list[ArtifactRef]) -> dict[str, Any]:
    try:
        base_ref, base = ctx.artifact_graph.latest_ref(dataset_id), None
        base = ctx.artifact_graph.get(base_ref)
    except KeyError:
        return {'base_ref': '', 'added_case_ids': case_ids, 'removed_case_ids': [],
                'changed_case_refs': [], 'order_changed': False}
    old_ids, old_refs = list(map(str, base.get('case_ids', []))), list(map(str, base.get('case_refs', [])))
    old_by_id, new_by_id = dict(zip(old_ids, old_refs)), dict(zip(case_ids, map(str, refs)))
    common = set(old_by_id) & set(new_by_id)
    return {
        'base_ref': str(base_ref),
        'added_case_ids': [case_id for case_id in case_ids if case_id not in old_by_id],
        'removed_case_ids': [case_id for case_id in old_ids if case_id not in new_by_id],
        'changed_case_refs': [case_id for case_id in case_ids if case_id in common
                              and old_by_id[case_id] != new_by_id[case_id]],
        'order_changed': [case_id for case_id in old_ids if case_id in new_by_id]
        != [case_id for case_id in case_ids if case_id in old_by_id],
    }


def _preview(cases: list[dict[str, Any]]) -> list[dict[str, Any]]:
    keys = ('id', 'question', 'question_type', 'difficulty')
    return [{key: case[key] for key in keys} for case in cases[:20]]


def _norm(value: Any) -> str:
    return re.sub(r'\s+', '', str(value or '')).lower()

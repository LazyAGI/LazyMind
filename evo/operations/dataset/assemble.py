from collections import Counter
from collections.abc import Iterable, Mapping
from typing import Any

from evo.operations.public_contracts import DatasetRoot, case_source_label, dump_contract

from .csv_loader import CASE_FIELDS, case_source, norm_text, normalize_eval_case


def assemble_dataset(
    cases: Mapping[str, Any] | Iterable[Mapping[str, Any]], *, run_id: str, min_case_count: int = 1,
) -> dict[str, Any]:
    rows = _rows(cases)
    id_counts = Counter(row['id'] for row in rows)
    question_counts = Counter(norm_text(row['question']) for row in rows)
    errors = [{'code': 'duplicate_id', 'id': case_id} for case_id, count in id_counts.items() if count > 1]
    errors += [{'code': 'duplicate_question', 'question': question} for question, count in question_counts.items()
               if question and count > 1]
    if len(rows) < min_case_count:
        errors.append({'code': 'too_few_cases', 'size': len(rows), 'min_case_count': min_case_count})
    if errors:
        raise ValueError(f'eval.dataset contract errors: {errors}')
    return dump_contract(DatasetRoot, {
        'run_id': run_id,
        'case_num': len(rows),
        'cases': [_case(row) for row in rows],
    })


def _rows(cases: Mapping[str, Any] | Iterable[Mapping[str, Any]]) -> list[dict[str, Any]]:
    if not isinstance(cases, Mapping):
        return [normalize_eval_case(row, default_id=f'case_{index:04d}') for index, row in enumerate(cases, 1)]
    rows = []
    for case_id, row in sorted(cases.items()):
        if isinstance(row, Mapping) and row.get('id') and row.get('id') != case_id:
            raise ValueError(f'case partition mismatch: {case_id} != {row["id"]}')
        rows.append(normalize_eval_case(row, default_id=case_id))
    return rows


def _case(row: Mapping[str, Any]) -> dict[str, Any]:
    audit = case_source(row)
    return {
        'case_id': row.get('id', ''),
        'source': case_source_label(row, csv_first=True),
        **{field: row.get(field, '') for field in CASE_FIELDS if field != 'id'},
        'original_id': audit.get('original_id', ''),
    }

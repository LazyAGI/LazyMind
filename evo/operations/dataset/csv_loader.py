import csv
import json
from collections import Counter
from collections.abc import Mapping
from pathlib import Path
from typing import Any

CASE_FIELDS = tuple('answer difficulty difficulty_rationale grading_guidance id question question_type reasoning_steps '
                    'reference_chunk_ids reference_context reference_doc reference_doc_ids source_message_id '
                    'source_preparation type_rationale'.split())
AUDIT_FIELDS = ('original_id', 'source', 'kb_id', 'csv_path')
DEFAULT_MIN_CASE_COUNT = 100
QUESTION_TYPES = ('single_hop', 'single_doc_multi_hop', 'multi_doc_multi_hop', 'table_list', 'formula')
DIFFICULTIES = ('easy', 'medium', 'hard')
LIST_FIELDS = {'reasoning_steps', 'reference_chunk_ids', 'reference_context', 'reference_doc', 'reference_doc_ids'}
REQUIRED_IMPORT_FIELDS = CASE_FIELDS[:12]
GENERATED_CASE_FIELDS = ('question', 'answer', 'grading_guidance', 'reasoning_steps',
                         'difficulty_rationale', 'type_rationale')


def as_text(value: Any) -> str:
    return '' if value is None else str(value).strip()


def norm_text(value: Any) -> str:
    return ' '.join(as_text(value).lower().split())


def as_list(value: Any) -> list[str]:
    if value is None:
        return []
    if isinstance(value, str):
        text = value.strip()
        if not text:
            return []
        try:
            parsed = json.loads(text)
        except json.JSONDecodeError:
            parsed = text.split(';')
        value = parsed if isinstance(parsed, list) else text
    if isinstance(value, list | tuple):
        return [as_text(item) for item in value if as_text(item)]
    text = as_text(value)
    return [text] if text else []


def json_object(raw: str, *, message: str = 'value must be a JSON object') -> Mapping[str, Any]:
    try:
        data = json.loads(raw)
    except json.JSONDecodeError as exc:
        raise ValueError(message) from exc
    if not isinstance(data, Mapping):
        raise ValueError(message)
    return data


def warning(code: str, message: str, *, case_id: str = '', original_id: str = '', kb_id: str = '') -> dict[str, str]:
    return {'code': code, 'message': message, 'case_id': case_id, 'original_id': original_id, 'kb_id': kb_id}


def case_source(case: Mapping[str, Any]) -> dict[str, str]:
    prep = case.get('source_preparation') if isinstance(case.get('source_preparation'), Mapping) else {}
    source = prep.get('case_source') if isinstance(prep.get('case_source'), Mapping) else {}
    return {
        'final_id': as_text(source.get('final_id') or case.get('id')),
        'original_id': as_text(source.get('original_id') or case.get('original_id')),
        'source': as_text(source.get('source') or case.get('source')),
        'kb_id': as_text(source.get('kb_id') or case.get('kb_id')),
        'csv_path': as_text(source.get('csv_path') or case.get('csv_path')),
    }


def normalize_eval_case(raw: Mapping[str, Any], *, default_id: str = '') -> dict[str, Any]:
    if not isinstance(raw, Mapping):
        raise ValueError('eval case must be an object')
    prep = raw.get('source_preparation')
    prep = json_object(prep, message='source_preparation must be valid JSON') if isinstance(prep, str) and prep.strip() \
        else prep if isinstance(prep, Mapping) else {}
    row = {field: raw.get(field, '') for field in CASE_FIELDS}
    row.update({'id': as_text(row['id'] or default_id), 'question_type': as_text(row['question_type']),
                'difficulty': as_text(row['difficulty']), 'source_message_id': as_text(row['source_message_id']),
                'source_preparation': dict(prep)})
    for field in LIST_FIELDS:
        row[field] = as_list(row[field])

    refs = row['source_preparation'].get('context_reference')
    if refs and (not isinstance(refs, list) or not all(isinstance(item, Mapping) for item in refs)):
        raise ValueError('source_preparation.context_reference must be a list of objects')

    missing = [field for field in REQUIRED_IMPORT_FIELDS if (
        not any(row[field]) if isinstance(row[field], list) else not as_text(row[field])
    )]
    if missing:
        raise ValueError(f'eval case missing required fields: {", ".join(missing)}')
    if row['question_type'] not in QUESTION_TYPES:
        raise ValueError(f'unsupported question_type: {row["question_type"]}')
    if row['difficulty'] not in DIFFICULTIES:
        raise ValueError(f'unsupported difficulty: {row["difficulty"]}')
    return row


def load_eval_dataset_csv(path: str | Path) -> list[dict[str, Any]]:
    report = load_eval_dataset_csv_report(path, kb_id='')
    if report['warnings'] and not report['cases']:
        raise ValueError(report['warnings'][0]['message'])
    return [{field: row.get(field, '') for field in CASE_FIELDS} for row in report['cases']]


def load_eval_dataset_csv_report(path: str | Path, *, kb_id: str) -> dict[str, Any]:
    rows, warnings = [], []
    with Path(path).expanduser().open(newline='', encoding='utf-8-sig') as handle:
        reader = csv.DictReader(handle)
        header, allowed = reader.fieldnames or [], set(CASE_FIELDS) | set(AUDIT_FIELDS)
        if [name for name, count in Counter(header).items() if count > 1] \
                or not set(CASE_FIELDS) <= set(header) or set(header) - allowed:
            return {'cases': [], 'warnings': [warning(
                'csv_row_invalid', 'eval dataset csv fields do not match required dataset fields', kb_id=kb_id)]}
        for row_number, raw in enumerate(reader, 1):
            original_id = as_text(raw.get('id') if isinstance(raw, Mapping) else '')
            missing = [field for field in REQUIRED_IMPORT_FIELDS
                       if not as_text(raw.get(field)) or (field in LIST_FIELDS and not as_list(raw.get(field)))]
            if None in raw or missing:
                message = 'has extra columns' if None in raw else f'missing: {", ".join(missing)}'
                warnings.append(warning('csv_row_invalid', f'row {row_number} {message}',
                                        original_id=original_id, kb_id=kb_id))
                continue
            try:
                final_id = f'case_{len(rows) + 1:04d}'
                prep = raw.get('source_preparation')
                prep = dict(json_object(prep, message='source_preparation must be valid JSON')) \
                    if isinstance(prep, str) and prep.strip() else dict(prep or {})
                prep['case_source'] = {'final_id': final_id, 'original_id': original_id or final_id,
                                       'source': 'imported_csv', 'kb_id': kb_id,
                                       'csv_path': str(Path(path).expanduser())}
                row = normalize_eval_case({**dict(raw), 'id': final_id, 'source_preparation': prep},
                                          default_id=final_id)
            except ValueError as exc:
                warnings.append(warning('csv_row_invalid', f'row {row_number}: {exc}',
                                        original_id=original_id, kb_id=kb_id))
                continue
            rows.append(row)
    return {'cases': rows, 'warnings': warnings}

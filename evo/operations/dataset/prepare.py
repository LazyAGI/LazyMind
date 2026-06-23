from __future__ import annotations

import json
import re
from collections import Counter
from typing import Any, Mapping

from evo.operations.common import as_list, clip, norm, text


QUESTION_TYPES = (
    'single_hop',
    'single_doc_multi_hop',
    'multi_doc_multi_hop',
    'table_list',
    'formula',
)
DIFFICULTIES = ('easy', 'medium', 'hard')
TYPE_RULES = {
    'single_hop': 'Use exactly one chunk. The answer must be directly supported by that chunk alone.',
    'single_doc_multi_hop': (
        'Use two or three chunks from one document. The answer must combine evidence across chunks.'
    ),
    'multi_doc_multi_hop': 'Use chunks from at least two documents. The answer must combine evidence across documents.',
    'table_list': (
        'Use only table or list chunks. Ask a lookup, comparison, count, filter, rank, '
        'or enumeration question.'
    ),
    'formula': 'Use formula chunks. Ask about formula meaning, substitution, calculation, or numeric relationship.',
}
PLAN_RULES = {
    'single_hop': 'Select exactly one chunk.',
    'single_doc_multi_hop': 'Select two or three chunks, all from the same doc_id.',
    'multi_doc_multi_hop': 'Select two or three chunks across at least two doc_id values.',
    'table_list': 'Select one to three chunks, and every selected chunk must be table or list.',
    'formula': 'Select one or two chunks, and every selected chunk must be formula.',
}
DIFFICULTY_RULES = {
    'easy': 'Use the minimum evidence required by the question type and ask for an explicit fact.',
    'medium': 'Use the question type evidence to require comparison, filtering, or a simple calculation.',
    'hard': 'Use the richest evidence available for the question type and require multi-constraint synthesis.',
}


def prepare_case(config: Mapping[str, Any], snapshot: Mapping[str, Any], case_id: str, services: Any) -> dict[str, Any]:
    services.raise_if_cancelled()
    units = [unit for unit in snapshot.get('source_units') or [] if isinstance(unit, Mapping)]
    if not units:
        raise ValueError('corpus snapshot has no source units')
    index = _case_index(case_id)
    requested_qtype, qtype, candidates, fallback_reason = _select_question_type(config, units, index)
    difficulty = _choice(config, 'difficulties', 'difficulty', DIFFICULTIES, index)
    feedback = ''
    allowed_chunk_ids = [text(unit.get('chunk_id')) for unit in candidates]
    for attempt in range(2):
        try:
            plan = _json_object(services.llm_complete(_planning_prompt(case_id, qtype, difficulty, candidates)
                                                      + feedback))
            selected = _selected_units(plan, candidates)
            _validate_contexts(qtype, selected)
            instruction = _required_text(plan, 'instruction')
            plan_rationale = _required_text(plan, 'plan_rationale')
            break
        except ValueError as exc:
            if attempt:
                raise
            feedback = (
                f'\nPrevious plan was invalid: {exc}. '
                'selected_chunk_ids must copy values exactly from allowed_chunk_ids_json: '
                f'{json.dumps(allowed_chunk_ids, ensure_ascii=False)}'
            )
    refs = [_reference(unit) for unit in selected]
    payload = {
        'case_id': case_id,
        'question_type': qtype,
        'difficulty': difficulty,
        'doc_reference': _unique_docs(selected),
        'context_reference': refs,
        'instruction': instruction,
        'type_rule': TYPE_RULES[qtype],
        'difficulty_rule': DIFFICULTY_RULES[difficulty],
        'plan_rationale': plan_rationale,
        'source_snapshot_dataset_id': text(snapshot.get('dataset_id')),
        'source_message_id': text(config.get('source_message_id')),
    }
    if qtype != requested_qtype:
        payload['requested_question_type'] = requested_qtype
        payload['question_type_fallback_reason'] = fallback_reason
    return payload


def generate_case(preparation: Mapping[str, Any], services: Any) -> dict[str, Any]:
    services.raise_if_cancelled()
    raw = services.llm_complete(_generation_prompt(preparation))
    data = _json_object(raw)
    return _case_payload_from_llm(preparation, data)


def prepare_and_generate_case(
    config: Mapping[str, Any],
    snapshot: Mapping[str, Any],
    case_id: str,
    services: Any,
) -> tuple[dict[str, Any], dict[str, Any]]:
    preparation = prepare_case(config, snapshot, case_id, services)
    return preparation, generate_case(preparation, services)


def assemble_dataset(cases: Mapping[str, Any]) -> dict[str, Any]:
    rows = [_case_payload(case_id, item) for case_id, item in sorted(cases.items())]
    return {
        'id': 'eval.dataset',
        'size': len(rows),
        'case_ids': [row['id'] for row in rows],
        'stats': {
            'question_type_counts': dict(Counter(row['question_type'] for row in rows)),
            'difficulty_counts': dict(Counter(row['difficulty'] for row in rows)),
        },
        'checks': _dataset_checks(rows),
        'preview': [{key: row[key] for key in ('id', 'question', 'question_type', 'difficulty')} for row in rows],
        'cases': rows,
    }


def _case_payload_from_llm(preparation: Mapping[str, Any], data: Mapping[str, Any]) -> dict[str, Any]:
    case_id = text(preparation.get('case_id'))
    contexts = [item for item in preparation.get('context_reference', []) if isinstance(item, Mapping)]
    allowed_chunks = {text(item.get('chunk_id')) for item in contexts}
    allowed_docs = set(_unique_text(item.get('doc_id') for item in contexts))
    chunks = _texts(data.get('reference_chunk_ids'))
    docs = _texts(data.get('reference_doc_ids'))
    if len(chunks) != len(allowed_chunks) or set(chunks) != allowed_chunks:
        raise ValueError('generated case must cite every selected chunk and no unselected chunks')
    if len(docs) != len(allowed_docs) or set(docs) != allowed_docs:
        raise ValueError('generated case must cite every selected document and no unselected documents')
    row = {
        'id': case_id,
        'question': _required_text(data, 'question'),
        'answer': _required_text(data, 'answer'),
        'question_type': text(preparation.get('question_type')),
        'difficulty': text(preparation.get('difficulty')),
        'grading_guidance': _required_text(data, 'grading_guidance'),
        'reference_context': [text(item.get('content_preview')) for item in contexts],
        'reference_doc': [text(item.get('filename')) for item in contexts],
        'reference_doc_ids': _unique_text(item.get('doc_id') for item in contexts),
        'reference_chunk_ids': [text(item.get('chunk_id')) for item in contexts],
        'reasoning_steps': _texts(data.get('reasoning_steps')),
        'difficulty_rationale': text(data.get('difficulty_rationale')),
        'type_rationale': text(data.get('type_rationale')),
        'source_preparation': dict(preparation),
        'source_message_id': text(preparation.get('source_message_id')),
    }
    _validate_case(row, contexts)
    return row


def _case_index(case_id: str) -> int:
    match = re.search(r'(\d+)$', case_id)
    return max(0, int(match.group(1)) - 1) if match else sum(map(ord, case_id))


def _case_payload(case_id: str, value: Any) -> dict[str, Any]:
    if not isinstance(value, Mapping):
        raise ValueError(f'DatasetCase payload for {case_id} must be an object')
    row = dict(value)
    if row.get('id') != case_id:
        raise ValueError(f"case partition mismatch: {case_id} != {row.get('id')}")
    return row


def _choice(config: Mapping[str, Any], list_key: str, scalar_key: str, allowed: tuple[str, ...], index: int) -> str:
    raw = config.get(list_key)
    values = _option_values(raw if raw not in (None, '') else config.get(scalar_key), allowed, list_key)
    pool = tuple(values) if values else allowed
    return pool[index % len(pool)]


def _select_question_type(
    config: Mapping[str, Any],
    units: list[Mapping[str, Any]],
    index: int,
) -> tuple[str, str, list[Mapping[str, Any]], str]:
    pool = _question_type_pool(config)
    offset = index % len(pool)
    ordered = list(pool[offset:] + pool[:offset])
    ordered.extend(qtype for qtype in QUESTION_TYPES if qtype not in ordered)
    requested = ordered[0]
    errors = []
    for qtype in ordered:
        try:
            return requested, qtype, _candidate_units(units, qtype, index), (errors[0] if errors else '')
        except ValueError as exc:
            errors.append(f'{qtype}: {exc}')
    raise ValueError(f'no question_type has usable candidate chunks: {"; ".join(errors)}')


def _question_type_pool(config: Mapping[str, Any]) -> tuple[str, ...]:
    raw = config.get('question_types')
    values = _option_values(raw if raw not in (None, '') else config.get('question_type'),
                            QUESTION_TYPES, 'question_types')
    return tuple(values) if values else QUESTION_TYPES


def _option_values(raw: Any, allowed: tuple[str, ...], name: str) -> list[str]:
    values = [text(value) for value in as_list(raw) if text(value)]
    invalid = [value for value in values if value not in allowed]
    if invalid:
        raise ValueError(f'{name} contains unsupported values: {", ".join(invalid)}')
    return values


def _dataset_checks(rows: list[Mapping[str, Any]]) -> dict[str, Any]:
    duplicates = [question for question, count in Counter(norm(row.get('question')) for row in rows).items()
                  if question and count > 1]
    errors = [{'code': 'duplicate_question', 'message': question} for question in duplicates]
    for row in rows:
        if error := _case_errors(row):
            errors.append(error)
    return {
        'ready': not errors and bool(rows),
        'errors': errors,
        'warnings': [{'code': 'missing_reference', 'case_id': row['id']} for row in rows
                     if not row.get('reference_chunk_ids')],
    }


def _case_errors(row: Mapping[str, Any]) -> dict[str, str]:
    contexts = [item for item in row.get('source_preparation', {}).get('context_reference', [])
                if isinstance(item, Mapping)]
    try:
        _validate_case(row, contexts)
    except ValueError as exc:
        return {'code': 'invalid_case', 'case_id': text(row.get('id')), 'message': str(exc)}
    return {}


def _candidate_units(units: list[Mapping[str, Any]], qtype: str, index: int) -> list[Mapping[str, Any]]:
    usable = [unit for unit in units if text(unit.get('content')) and text(unit.get('chunk_id'))]
    if qtype == 'single_hop':
        out = usable
    elif qtype in {'single_doc_multi_hop', 'multi_doc_multi_hop'}:
        out = [unit for unit in usable if text(unit.get('doc_id'))]
    elif qtype == 'table_list':
        out = [unit for unit in usable if text(unit.get('unit_type')) in {'table', 'list'}]
    elif qtype == 'formula':
        out = [unit for unit in usable if text(unit.get('unit_type')) == 'formula']
    else:
        raise ValueError(f'unsupported question_type: {qtype}')
    if not out:
        raise ValueError(f'{qtype} has no usable candidate chunks')
    offset = index % len(out)
    rotated = out[offset:] + out[:offset]
    candidates = _candidate_window(qtype, rotated)
    _validate_candidate_pool(qtype, candidates)
    return candidates


def _candidate_window(qtype: str, units: list[Mapping[str, Any]]) -> list[Mapping[str, Any]]:
    if qtype == 'multi_doc_multi_hop':
        by_doc: dict[str, list[Mapping[str, Any]]] = {}
        for unit in units:
            by_doc.setdefault(text(unit.get('doc_id')), []).append(unit)
        heads = [items[0] for items in by_doc.values() if items]
        rest = [unit for unit in units if unit not in heads]
        return (heads + rest)[:40]
    if qtype == 'single_doc_multi_hop':
        by_doc: dict[str, list[Mapping[str, Any]]] = {}
        for unit in units:
            by_doc.setdefault(text(unit.get('doc_id')), []).append(unit)
        pair = next((items[:2] for items in by_doc.values() if len(items) >= 2), [])
        rest = [unit for unit in units if unit not in pair]
        return (pair + rest)[:40]
    return units[:40]


def _validate_candidate_pool(qtype: str, units: list[Mapping[str, Any]]) -> None:
    docs = {text(unit.get('doc_id')) for unit in units if text(unit.get('doc_id'))}
    if qtype == 'single_doc_multi_hop' and not any(
        sum(1 for item in units if text(item.get('doc_id')) == doc_id) >= 2 for doc_id in docs
    ):
        raise ValueError('single_doc_multi_hop requires at least two usable chunks from one document')
    if qtype == 'multi_doc_multi_hop' and len(docs) < 2:
        raise ValueError('multi_doc_multi_hop requires usable chunks from at least two documents')


def _selected_units(plan: Mapping[str, Any], candidates: list[Mapping[str, Any]]) -> list[Mapping[str, Any]]:
    if any(key in plan for key in ('question', 'answer', 'reference_chunk_ids', 'reference_doc_ids')):
        raise ValueError('case plan must not include generated question, answer, or reference fields')
    by_chunk = {text(unit.get('chunk_id')): unit for unit in candidates}
    chunk_ids = _texts(plan.get('selected_chunk_ids'))
    if not chunk_ids:
        raise ValueError('case plan missing selected_chunk_ids')
    if len(chunk_ids) != len(set(chunk_ids)):
        raise ValueError('case plan selected duplicate chunks')
    unknown = [chunk_id for chunk_id in chunk_ids if chunk_id not in by_chunk]
    if unknown:
        raise ValueError(f'case plan selected chunks outside candidates: {", ".join(unknown)}')
    return [by_chunk[chunk_id] for chunk_id in chunk_ids]


def _reference(unit: Mapping[str, Any]) -> dict[str, str]:
    return {
        'chunk_id': text(unit.get('chunk_id')),
        'doc_id': text(unit.get('doc_id')),
        'filename': text(unit.get('filename')),
        'content_preview': clip(unit.get('content'), 1200),
        'unit_type': text(unit.get('unit_type')),
    }


def _unique_docs(units: list[Mapping[str, Any]]) -> list[dict[str, str]]:
    by_id = {}
    for unit in units:
        by_id.setdefault(text(unit.get('doc_id')), {
            'doc_id': text(unit.get('doc_id')),
            'filename': text(unit.get('filename')),
            'doc_ref': text(unit.get('doc_ref')),
        })
    return list(by_id.values())


def _planning_prompt(case_id: str, qtype: str, difficulty: str, candidates: list[Mapping[str, Any]]) -> str:
    chunks = [{
        'chunk_id': text(unit.get('chunk_id')),
        'doc_id': text(unit.get('doc_id')),
        'filename': text(unit.get('filename')),
        'unit_type': text(unit.get('unit_type')),
        'content': clip(unit.get('content'), 900),
    } for unit in candidates]
    return (
        'You plan one grounded LazyRAG evaluation case. Return exactly one JSON object and no markdown.\n'
        'Do not generate the final question or answer. Use only candidate chunks.\n'
        'Required JSON fields: selected_chunk_ids, instruction, plan_rationale.\n'
        'selected_chunk_ids must copy values exactly from allowed_chunk_ids_json.\n'
        f'case_id: {case_id}\n'
        f'question_type: {qtype}\n'
        f'question_type_rule: {TYPE_RULES[qtype]}\n'
        f'selection_rule: {PLAN_RULES[qtype]}\n'
        f'difficulty: {difficulty}\n'
        f'difficulty_rule: {DIFFICULTY_RULES[difficulty]}\n'
        f'allowed_chunk_ids_json: {json.dumps([item["chunk_id"] for item in chunks], ensure_ascii=False)}\n'
        f'candidate_chunks_json: {json.dumps(chunks, ensure_ascii=False, sort_keys=True)}'
    )


def _generation_prompt(preparation: Mapping[str, Any]) -> str:
    evidence = [{
        'chunk_id': text(item.get('chunk_id')),
        'doc_id': text(item.get('doc_id')),
        'filename': text(item.get('filename')),
        'unit_type': text(item.get('unit_type')),
        'content': text(item.get('content_preview')),
    } for item in preparation.get('context_reference', []) if isinstance(item, Mapping)]
    expected_chunk_ids = [item['chunk_id'] for item in evidence]
    expected_doc_ids = list(dict.fromkeys(item['doc_id'] for item in evidence))
    return (
        'You generate grounded LazyRAG evaluation cases. Return exactly one JSON object and no markdown.\n'
        'Use only the evidence below. Do not use outside knowledge. The question must be standalone and must not say '
        '"the evidence", "the context", "above", "this document", or similar source-deictic wording.\n'
        'Required JSON fields: question, answer, grading_guidance, reference_chunk_ids, reference_doc_ids, '
        'reasoning_steps, difficulty_rationale, type_rationale.\n'
        'reference_chunk_ids must exactly copy expected_reference_chunk_ids_json. '
        'reference_doc_ids must exactly copy expected_reference_doc_ids_json.\n'
        f'case_id: {text(preparation.get("case_id"))}\n'
        f'question_type: {text(preparation.get("question_type"))}\n'
        f'question_type_rule: {text(preparation.get("type_rule"))}\n'
        f'difficulty: {text(preparation.get("difficulty"))}\n'
        f'difficulty_rule: {text(preparation.get("difficulty_rule"))}\n'
        f'instruction: {text(preparation.get("instruction"))}\n'
        f'expected_reference_chunk_ids_json: {json.dumps(expected_chunk_ids, ensure_ascii=False)}\n'
        f'expected_reference_doc_ids_json: {json.dumps(expected_doc_ids, ensure_ascii=False)}\n'
        f'evidence_json: {json.dumps(evidence, ensure_ascii=False, sort_keys=True)}'
    )


def _json_object(raw: str) -> Mapping[str, Any]:
    try:
        data = json.loads(raw)
    except json.JSONDecodeError as exc:
        raise ValueError('LLM did not return a JSON object') from exc
    if not isinstance(data, Mapping):
        raise ValueError('LLM response must be a JSON object')
    return data


def _required_text(data: Mapping[str, Any], key: str) -> str:
    value = text(data.get(key))
    if not value:
        raise ValueError(f'generated case missing {key}')
    return value


def _texts(value: Any) -> list[str]:
    return [item for item in (text(item) for item in as_list(value)) if item]


def _unique_text(values: Any) -> list[str]:
    return list(dict.fromkeys(item for item in (text(value) for value in values) if item))


def _validate_case(row: Mapping[str, Any], contexts: list[Mapping[str, Any]]) -> None:
    _validate_contexts(text(row.get('question_type')), contexts)
    if not text(row.get('question')) or not text(row.get('answer')) or not text(row.get('grading_guidance')):
        raise ValueError('case must include question, answer, and grading_guidance')


def _validate_contexts(qtype: str, contexts: list[Mapping[str, Any]]) -> None:
    qtype = text(qtype)
    if qtype not in QUESTION_TYPES:
        raise ValueError(f'unsupported question_type: {qtype}')
    chunks = [text(item.get('chunk_id')) for item in contexts]
    docs = [text(item.get('doc_id')) for item in contexts]
    unit_types = {text(item.get('unit_type')) for item in contexts}
    if len(set(chunks)) != len(chunks) or not all(chunks):
        raise ValueError(f'{qtype} must use non-empty unique chunks')
    if qtype == 'single_hop' and len(chunks) != 1:
        raise ValueError('single_hop must use exactly one chunk')
    if qtype == 'single_doc_multi_hop' and (not 2 <= len(chunks) <= 3 or not all(docs) or len(set(docs)) != 1):
        raise ValueError('single_doc_multi_hop must use two or three chunks from one document')
    if qtype == 'multi_doc_multi_hop' and (not 2 <= len(chunks) <= 3 or not all(docs) or len(set(docs)) < 2):
        raise ValueError('multi_doc_multi_hop must use two or three chunks from multiple documents')
    if qtype == 'table_list' and (not 1 <= len(chunks) <= 3 or not unit_types <= {'table', 'list'}):
        raise ValueError('table_list must use one to three table or list chunks')
    if qtype == 'formula' and (not 1 <= len(chunks) <= 2 or unit_types != {'formula'}):
        raise ValueError('formula must use one or two formula chunks')

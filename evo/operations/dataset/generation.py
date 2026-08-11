import json
from collections import Counter
from collections.abc import Callable, Iterable, Mapping
from typing import Any

from evo.llm import parse_json_object

from .csv_loader import DIFFICULTIES, GENERATED_CASE_FIELDS, QUESTION_TYPES, as_list, as_text
from .csv_loader import norm_text, normalize_eval_case

QUESTION_RETRY_COUNT = 3


def build_case_requests(config: Mapping[str, Any],
                        snapshot: Mapping[str, Any]
                        ) -> dict[str, dict[str, str]]:
    cases = [row for row in snapshot.get('cases') or () if isinstance(row, Mapping)]
    case_ids = [as_text(row.get('id')) for row in cases]
    if any(not case_id for case_id in case_ids):
        raise ValueError('corpus snapshot cases require ids')
    if len(set(case_ids)) != len(case_ids):
        raise ValueError('corpus snapshot case ids must be unique')

    stats = snapshot.get('stats') if isinstance(snapshot.get('stats'), Mapping) else {}
    inputs = config.get('inputs') if isinstance(config.get('inputs'), Mapping) else {}
    raw_target = (
        config.get('num_case')
        or inputs.get('num_case')
        or stats.get('min_case_count')
        or snapshot.get('case_count')
        or len(case_ids)
        or 1
    )
    target = int(raw_target)
    if target < 1:
        raise ValueError('corpus snapshot target case count must be positive')

    index = 1
    while len(case_ids) < target:
        case_id = f'case_{index:04d}'
        index += 1
        if case_id not in case_ids:
            case_ids.append(case_id)
    return {case_id: {'case_id': case_id} for case_id in case_ids}


def prepare_case(config: Mapping[str, Any], snapshot: Mapping[str, Any], case_id: str,
                 request: Mapping[str, Any] | None = None
                 ) -> dict[str, Any]:
    request = request or {}
    requested_id = as_text(request.get('case_id'))
    if requested_id and requested_id != case_id:
        raise ValueError('case request id does not match partition key')
    guidance = {
        key: value
        for key, value in request.items()
        if key not in {'case_id', 'case'}
    }
    manual_case = request.get('case')
    if isinstance(manual_case, Mapping):
        preparation = {
            'case_id': case_id,
            'mode': 'manual_case',
            'case': dict(manual_case),
        }
        if guidance:
            preparation['user_request'] = guidance
        return preparation

    suffix = case_id.rsplit('_', 1)[-1]
    index = max(int(suffix) - 1, 0) if suffix.isdigit() else 0
    case = _case_by_id(snapshot, case_id)
    if case is not None:
        prep = dict(case.get('source_preparation') or {})
        prep.update({'case_id': case_id, 'mode': 'imported_eval_dataset',
                     'question_type': as_text(case.get('question_type')),
                     'difficulty': as_text(case.get('difficulty')),
                     'source_snapshot_dataset_id': as_text(snapshot.get('dataset_id')),
                     'source_message_id': as_text(case.get('source_message_id'))})
        if guidance:
            prep['user_request'] = guidance
        return _with_warnings(prep, snapshot, index)
    if snapshot.get('cases') and not snapshot.get('source_units'):
        raise ValueError(f'imported eval dataset has no case for partition {case_id}')

    units = [unit for unit in snapshot.get('source_units') or [] if isinstance(unit, Mapping)]
    if not units:
        raise ValueError('corpus snapshot has no source units')
    sources = list(dict.fromkeys(as_text(unit.get('source_id')) for unit in units if as_text(unit.get('source_id'))))
    if sources:
        source = sources[index % len(sources)]
        units = [unit for unit in units if as_text(unit.get('source_id')) == source]
    qtype = _choice(config, 'question_type', QUESTION_TYPES, index)
    required_chunks = _unique_texts(as_list(guidance.get('required_chunks')))
    if required_chunks:
        contexts = _required_contexts(units, required_chunks)
    else:
        try:
            contexts = _contexts(units, qtype, index)
        except ValueError:
            if as_list(config.get('question_types') or config.get('question_type')):
                raise
            qtype, contexts = 'single_hop', _contexts(units, 'single_hop', index)
    prep = {
        'case_id': case_id,
        'mode': 'generated_kb_dataset',
        'question_type': qtype,
        'difficulty': _choice(config, 'difficulty', DIFFICULTIES, index),
        'context_reference': contexts,
        'source_snapshot_dataset_id': as_text(snapshot.get('dataset_id')),
        'source_message_id': as_text(config.get('source_message_id')),
        'case_source': {'final_id': case_id, 'original_id': '', 'source': 'generated_kb',
                        'kb_id': ';'.join(dict.fromkeys(as_text(item['source_id'])
                                                        for item in contexts if as_text(item['source_id'])))},
    }
    if guidance:
        prep['user_request'] = guidance
    return _with_warnings(prep, snapshot, index)


def generate_case(config: Mapping[str, Any], snapshot: Mapping[str, Any], prep: Mapping[str, Any],
                  llm_complete: Callable[[str], str] | None = None,
                  duplicate_questions: Callable[[Mapping[str, Any]], list[str]] | None = None) -> dict[str, Any]:
    if not (case_id := as_text(prep.get('case_id'))):
        raise ValueError('case preparation missing case_id')
    if prep.get('mode') == 'manual_case':
        case = normalize_eval_case(_mapping(prep.get('case'), 'manual case'), default_id=case_id)
        if case['id'] != case_id:
            raise ValueError('manual case id does not match case preparation')
        source = dict(case.get('source_preparation') or {})
        source.update({'case_id': case_id, 'mode': 'manual_case'})
        return {**case, 'source_preparation': source}
    if prep.get('mode') == 'imported_eval_dataset':
        case = _case_by_id(snapshot, case_id)
        if case is None:
            raise ValueError(f'imported eval dataset has no case for partition {case_id}')
        return {**dict(case), 'source_preparation': dict(prep)}
    if prep.get('mode') != 'generated_kb_dataset':
        raise ValueError(f'unsupported case preparation mode: {as_text(prep.get("mode"))}')
    contexts = prep.get('context_reference')
    if not isinstance(contexts, list) or not all(isinstance(item, Mapping) for item in contexts):
        raise ValueError('case preparation context_reference must be a list of objects')
    avoid_questions: list[str] = []
    for attempt in range(QUESTION_RETRY_COUNT + 1):
        row = normalize_eval_case({
            **_complete_case(config, prep, llm_complete, avoid_questions=avoid_questions, attempt=attempt + 1),
            'id': case_id,
            'question_type': as_text(prep.get('question_type')),
            'difficulty': as_text(prep.get('difficulty')),
            'reference_context': [as_text(item.get('content_preview')) for item in contexts],
            'reference_doc': [as_text(item.get('filename')) for item in contexts],
            'reference_doc_ids': list(dict.fromkeys(as_text(item.get('doc_ref')) for item in contexts
                                                    if as_text(item.get('doc_ref')))),
            'reference_chunk_ids': [as_text(item.get('source_unit_ref') or item.get('chunk_id'))
                                    for item in contexts],
            'source_message_id': as_text(prep.get('source_message_id')),
            'source_preparation': prep,
        }, default_id=case_id)
        duplicates = duplicate_questions(row) if duplicate_questions else []
        if not duplicates:
            return row
        avoid_questions = _unique_texts([*avoid_questions, *duplicates, row['question']])
    question = as_text(row.get('question')) or (duplicates[0] if duplicates else '')
    raise ValueError(f'dataset.generate_case duplicate_question after {QUESTION_RETRY_COUNT} retries: {question}')


def _case_by_id(snapshot: Mapping[str, Any], case_id: str) -> Mapping[str, Any] | None:
    return next((row for row in snapshot.get('cases') or []
                 if isinstance(row, Mapping) and as_text(row.get('id')) == case_id), None)


def _mapping(value: object, name: str) -> Mapping[str, Any]:
    if not isinstance(value, Mapping):
        raise ValueError(f'{name} must be a mapping')
    return value


def _with_warnings(prep: dict[str, Any], snapshot: Mapping[str, Any], index: int) -> dict[str, Any]:
    if index == 0 and (warnings := [item for item in snapshot.get('warnings', []) if isinstance(item, Mapping)]):
        prep['warnings'] = [*list(prep.get('warnings') or []), *warnings]
    return prep


def _complete_case(config: Mapping[str, Any], prep: Mapping[str, Any], complete: Callable[[str], str] | None, *,
                   avoid_questions: Iterable[str] = (), attempt: int = 1):
    if complete is None:
        from evo.llm import LazyLLMClient

        client = LazyLLMClient(llm_config=config.get('llm_config') if isinstance(config.get('llm_config'), Mapping)
                               else {})

        def complete(prompt: str) -> str:
            return as_text(client(prompt, stream=False))
    prompt = (
        'Prepare one grounded RAG evaluation dataset row as one JSON object, no markdown. '
        'Use only source_preparation_json. Required dataset fields: question, answer, grading_guidance, '
        'reasoning_steps, difficulty_rationale, type_rationale. reasoning_steps must be a list of strings. '
        f'source_preparation_json: {json.dumps(prep, ensure_ascii=False, sort_keys=True)}'
    )
    avoid = _unique_texts(avoid_questions)
    if avoid:
        prompt += (
            f'\nretry_attempt: {attempt}. The previous question was a duplicate. '
            'Generate a question that is not semantically equivalent to any item in avoid_questions_json. '
            f'avoid_questions_json: {json.dumps(avoid, ensure_ascii=False)}'
        )
    error: Exception | None = None
    for structured_attempt in range(2):
        try:
            data = parse_json_object(complete(prompt))
            if missing := [field for field in GENERATED_CASE_FIELDS if not data.get(field)]:
                raise ValueError(f'generated case missing fields: {", ".join(missing)}')
            if not isinstance(steps := data.get('reasoning_steps'), list) or not all(as_text(step) for step in steps):
                raise ValueError('generated case reasoning_steps must be a non-empty list of strings')
            return dict(data)
        except Exception as exc:
            error = exc
            if structured_attempt == 0:
                prompt += '\nThe previous response was invalid. Return exactly one complete JSON object.'
    raise ValueError(f'LLM did not return a valid dataset case: {error}') from error


def _unique_texts(values: Iterable[object]) -> list[str]:
    seen, result = set(), []
    for value in values:
        text = as_text(value)
        key = norm_text(text)
        if text and key not in seen:
            seen.add(key)
            result.append(text)
    return result


def _contexts(units: list[Mapping[str, Any]], qtype: str, index: int) -> list[dict[str, str]]:
    usable = [unit for unit in units if as_text(unit.get('content')) and as_text(unit.get('chunk_id'))]
    usable = [unit for unit in usable if as_text(unit.get('unit_type')) in {'table', 'list'}] \
        if qtype == 'table_list' else usable
    usable = [unit for unit in usable if as_text(unit.get('unit_type')) == 'formula'] if qtype == 'formula' else usable
    if not usable:
        raise ValueError(f'{qtype} has no usable source units')
    rotated = usable[index % len(usable):] + usable[:index % len(usable)]
    if qtype == 'single_doc_multi_hop':
        doc_id = next((doc for doc, count in Counter(_doc_ref(unit) for unit in rotated).items()
                       if doc and count >= 2), '')
        if not doc_id:
            raise ValueError('single_doc_multi_hop needs two chunks from one document')
        rotated = [unit for unit in rotated if _doc_ref(unit) == doc_id]
    if qtype == 'multi_doc_multi_hop':
        seen, picked = set(), []
        for unit in rotated:
            doc_id = _doc_ref(unit)
            if doc_id not in seen:
                seen.add(doc_id)
                picked.append(unit)
        if len(picked) < 2:
            raise ValueError('multi_doc_multi_hop needs chunks from two documents')
        rotated = picked
    limit = 1 if qtype == 'single_hop' else min(2 if qtype == 'formula' else 3, len(rotated))
    return [_context(unit) for unit in rotated[:limit]]


def _required_contexts(units: list[Mapping[str, Any]], required: list[str]
                       ) -> list[dict[str, str]]:
    by_id = {
        identity: unit
        for unit in units
        for identity in (
            as_text(unit.get('chunk_id')),
            as_text(unit.get('source_unit_ref')),
        )
        if identity
    }
    missing = [chunk_id for chunk_id in required if chunk_id not in by_id]
    if missing:
        raise ValueError(f'required chunks are not present in corpus snapshot: {", ".join(missing)}')
    return [_context(by_id[chunk_id]) for chunk_id in required]


def _context(unit: Mapping[str, Any]) -> dict[str, str]:
    return {
        **{key: as_text(unit.get(key)) for key in (
            'source_id', 'source_unit_ref', 'doc_ref', 'chunk_id', 'doc_id', 'filename',
        )},
        'unit_type': as_text(unit.get('unit_type') or 'paragraph'),
        'content_preview': as_text(unit.get('content'))[:1200],
    }


def _choice(config: Mapping[str, Any], key: str, allowed: tuple[str, ...], index: int) -> str:
    values = as_list(config.get({'difficulty': 'difficulties'}.get(key, f'{key}s')) or config.get(key))
    invalid = [value for value in values if value not in allowed]
    if invalid:
        raise ValueError(f'{key} contains unsupported values: {", ".join(invalid)}')
    return values[index % len(values)] if values else allowed[index % len(allowed)]


def _doc_ref(unit: Mapping[str, Any]) -> str:
    return as_text(unit.get('doc_ref')) or ':'.join([as_text(unit.get('source_id')), as_text(unit.get('doc_id'))])

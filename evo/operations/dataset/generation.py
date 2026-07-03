import json
from collections import Counter
from collections.abc import Callable, Mapping
from typing import Any

from .assemble import assemble_dataset
from .csv_loader import DIFFICULTIES, GENERATED_CASE_FIELDS, QUESTION_TYPES, as_list
from .csv_loader import as_text, json_object, normalize_eval_case
from .kb_loader import build_corpus_snapshot, load_corpus


def generate_case(
    config: Mapping[str, Any],
    snapshot: Mapping[str, Any],
    case_id: str,
    llm_complete: Callable[[str], str] | None = None,
) -> tuple[dict[str, Any], dict[str, Any]]:
    index = int(case_id.rsplit('_', 1)[-1]) - 1
    imported = {as_text(row.get('id')): row for row in snapshot.get('cases') or [] if isinstance(row, Mapping)}
    if case_id in imported:
        case = dict(imported[case_id])
        prep = dict(case.get('source_preparation') or {})
        prep.update({'case_id': case_id, 'mode': 'imported_eval_dataset',
                     'question_type': as_text(case.get('question_type')),
                     'difficulty': as_text(case.get('difficulty')),
                     'source_snapshot_dataset_id': as_text(snapshot.get('dataset_id')),
                     'source_message_id': as_text(case.get('source_message_id'))})
        warnings = [item for item in snapshot.get('warnings', []) if isinstance(item, Mapping)]
        if index == 0 and warnings:
            prep['warnings'] = [*list(prep.get('warnings') or []), *warnings]
        case['source_preparation'] = prep
        return prep, case
    if imported and not snapshot.get('source_units'):
        raise ValueError(f'imported eval dataset has no case for partition {case_id}')

    units = [unit for unit in snapshot.get('source_units') or [] if isinstance(unit, Mapping)]
    if not units:
        raise ValueError('corpus snapshot has no source units')
    sources = list(dict.fromkeys(as_text(unit.get('source_id')) for unit in units if as_text(unit.get('source_id'))))
    if sources:
        source = sources[index % len(sources)]
        units = [unit for unit in units if as_text(unit.get('source_id')) == source]
    qtype = _choice(config, 'question_type', QUESTION_TYPES, index)
    try:
        contexts = _contexts(units, qtype, index)
    except ValueError:
        if as_list(config.get('question_types') or config.get('question_type')):
            raise
        qtype, contexts = 'single_hop', _contexts(units, 'single_hop', index)
    difficulty = _choice(config, 'difficulty', DIFFICULTIES, index)
    source_ids = list(dict.fromkeys(as_text(item['source_id']) for item in contexts if as_text(item['source_id'])))
    prep = {
        'case_id': case_id,
        'mode': 'generated_kb_dataset',
        'question_type': qtype,
        'difficulty': difficulty,
        'context_reference': contexts,
        'source_snapshot_dataset_id': as_text(snapshot.get('dataset_id')),
        'source_message_id': as_text(config.get('source_message_id')),
        'case_source': {'final_id': case_id, 'original_id': '', 'source': 'generated_kb',
                        'kb_id': ';'.join(source_ids)},
    }
    warnings = [item for item in snapshot.get('warnings', []) if isinstance(item, Mapping)]
    if index == 0 and warnings:
        prep['warnings'] = warnings
    data = _complete_case(config, prep, llm_complete)
    return prep, normalize_eval_case({
        **data,
        'id': case_id,
        'question_type': qtype,
        'difficulty': difficulty,
        'reference_context': [item['content_preview'] for item in contexts],
        'reference_doc': [item['filename'] for item in contexts],
        'reference_doc_ids': list(dict.fromkeys(item['doc_ref'] for item in contexts if item['doc_ref'])),
        'reference_chunk_ids': [item['source_unit_ref'] or item['chunk_id'] for item in contexts],
        'source_message_id': prep['source_message_id'],
        'source_preparation': prep,
    }, default_id=case_id)


def dataset_materializers(case_ids, *, llm_complete: Callable[[str], str] | None = None) -> dict[str, Any]:
    partitions = tuple(sorted(dict.fromkeys(case_ids)))
    if not partitions:
        raise ValueError('case_ids must not be empty')

    def load(ctx, inputs) -> Mapping[str, object]:
        return {'report': load_corpus(inputs['source_config'], partitions)}

    def snapshot(ctx, inputs) -> Mapping[str, object]:
        return {'snapshot': build_corpus_snapshot(inputs['report'], inputs['source_config'])}

    def case(ctx, inputs) -> Mapping[str, object]:
        key = ctx.output_key_by_name.get('case') or ctx.output_key_by_name.get('preparation')
        if key is None or not key.partition:
            raise ValueError('dataset.generate_case requires a partitioned case output')
        prep, row = generate_case(inputs['config'], inputs['snapshot'], key.partition, llm_complete)
        return {'preparation': prep, 'case': row}

    def assemble(ctx, inputs) -> Mapping[str, object]:
        values = inputs['cases']
        runtime_partitions = tuple(sorted(ref.key.partition for ref in ctx.input_ref_by_key.values()
                                          if ref.key.partition))
        if not isinstance(values, tuple):
            raise ValueError('cases input must be a partitioned tuple')
        if runtime_partitions != partitions:
            raise ValueError('dataset materializer case_ids do not match runtime partitions')
        return {'dataset': assemble_dataset(dict(zip(partitions, values, strict=True)),
                                            run_id=ctx.run_id,
                                            min_case_count=len(partitions))}

    return {'dataset.load_corpus': load, 'dataset.build_corpus_snapshot': snapshot,
            'dataset.generate_case': case, 'dataset.assemble': assemble}


def _complete_case(config: Mapping[str, Any], prep: Mapping[str, Any], complete: Callable[[str], str] | None):
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
    data = json_object(complete(prompt), message='LLM did not return a JSON object')
    missing = [field for field in GENERATED_CASE_FIELDS if not data.get(field)]
    if missing:
        raise ValueError(f'generated case missing fields: {", ".join(missing)}')
    if not isinstance(data.get('reasoning_steps'), list) or not all(
        as_text(step) for step in data.get('reasoning_steps')
    ):
        raise ValueError('generated case reasoning_steps must be a non-empty list of strings')
    return data


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
    return [{**{key: as_text(unit.get(key)) for key in (
        'source_id', 'source_unit_ref', 'doc_ref', 'chunk_id', 'doc_id', 'filename')},
        'unit_type': as_text(unit.get('unit_type') or 'paragraph'),
        'content_preview': as_text(unit.get('content'))[:1200]} for unit in rotated[:limit]]


def _choice(config: Mapping[str, Any], key: str, allowed: tuple[str, ...], index: int) -> str:
    values = as_list(config.get({'difficulty': 'difficulties'}.get(key, f'{key}s')) or config.get(key))
    invalid = [value for value in values if value not in allowed]
    if invalid:
        raise ValueError(f'{key} contains unsupported values: {", ".join(invalid)}')
    return values[index % len(values)] if values else allowed[index % len(allowed)]


def _doc_ref(unit: Mapping[str, Any]) -> str:
    return as_text(unit.get('doc_ref')) or ':'.join([as_text(unit.get('source_id')), as_text(unit.get('doc_id'))])

"""Model reference encoding must never relax the canonical guide contract."""
import json
from concurrent.futures import ThreadPoolExecutor
from copy import deepcopy
from pathlib import Path

import pytest

from lazymind.structured_tasks.paper_guide_schemas import (
    Extract, GuideInput, Plan, PlanInput, Review, ReviewInput, Section, SectionInput, bind_output,
)

A = 'ev_' + 'a' * 64
B = 'ev_' + 'b' * 64
S = 'ev_' + 'c' * 64
ROLES = ('background', 'question', 'contribution', 'justification')


def cases():
    evidence = [{'id': A, 'source_block_id': 'internal-block', 'source_type': 'table',
                 'text': f'e1 is literal; {A} stays in source prose.',
                 'locator': {'kind': 'page', 'page': 0}}]
    claim = {'id': 'e1', 'statement': 'The study reports a comparison.', 'attribution': 'paper_statement',
             'evidence_ids': [A], 'argument_type': None, 'conditions': [], 'support_status': 'supported'}
    plan = {'profiles': {'primary': 'method_system', 'secondary': [], 'uncertainty_reason': ''},
            'questions': ['What is compared?'], 'claims': [claim], 'term_policy': [],
            'required_assets': [{'id': A, 'included': True, 'reason': 'Central comparison.'}],
            'sections': [{'role': role, 'title': role.title(), 'focus': 'Explain this comparison.',
                          'terms': [], 'claim_ids': ['e1'], 'evidence_ids': [A],
                          'asset_ids': [A]} for role in ROLES]}
    section = {'role': 'background', 'title': 'Background', 'claims': [], 'claim_ids': ['e1'],
               'summary': 'The key comparison.', 'blocks': [{'id': 'e1', 'kind': 'table',
               'text': 'The table compares conditions.', 'attribution': 'paper_statement',
               'claim_ids': ['e1'], 'evidence_ids': [A], 'asset_id': A}]}
    base = {'language': 'en', 'evidence': evidence, 'source_sections': [{'id': S,
            'title': 'Comparison e1', 'kind': 'section', 'pages': [0], 'evidence_ids': [A]}]}
    return {
        'extract': (GuideInput, Extract, base, {'claims': [claim], 'open_questions': []}),
        'plan': (PlanInput, Plan, {**base, 'extracts': [{'claims': [claim], 'open_questions': []}]}, plan),
        'section': (SectionInput, Section, {**base, 'plan': plan, 'role': 'background'}, section),
        'review': (ReviewInput, Review, {**base, 'plan': plan, 'sections': [section]},
                   {'findings': [{'target_id': 'e1', 'target_type': 'block', 'issue_type': 'coverage',
                                  'suggestion': 'Explain the central comparison.'}]}),
    }


def model_output(task, output):
    """Independent fixture transformation, not the production mapping routine."""
    result = deepcopy(output)
    if task != 'review':
        for claim in result['claims']:
            claim['evidence_ids'] = ['e1' for _ in claim['evidence_ids']]
    if task == 'plan':
        for section in result['sections']:
            section['evidence_ids'] = ['e1']
            section['asset_ids'] = ['e1']
        result['required_assets'][0]['id'] = 'e1'
    if task == 'section':
        result['blocks'][0].update(evidence_ids=['e1'], asset_id='e1')
    return result


@pytest.mark.parametrize('task', cases())
def test_four_tasks_round_trip_preserves_canonical_contract_and_text(task):
    from lazymind.structured_tasks.paper_guide_references import SourceReferences
    I, O, raw, output = cases()[task]
    original = deepcopy(raw)
    data = I.model_validate(raw)
    refs = SourceReferences(task, data)
    view = refs.model_input
    assert view['evidence'][0]['id'] == 'e1'
    assert 'source_block_id' not in view['evidence'][0]
    assert view['evidence'][0]['text'] == raw['evidence'][0]['text']
    assert view['source_sections'][0]['id'] == 's1'
    assert view['source_sections'][0]['title'] == 'Comparison e1'
    assert view['source_sections'][0]['evidence_ids'] == ['e1']
    if 'plan' in view:
        assert view['plan']['claims'][0]['id'] == 'e1'
        assert view['plan']['claims'][0]['evidence_ids'] == ['e1']
    expanded = refs.canonical_output(model_output(task, output), 256 << 10)
    parsed = O.model_validate(expanded)
    bind_output(data, parsed)
    assert parsed.model_dump(exclude_unset=True) == output
    assert raw == original


@pytest.mark.parametrize('bad', ['e0', 'e01', 'E1', ' e1', 'e1 ', 's1', 'e2', A,
                                  'ev_15bba796ba4874a03d8422e8',
                                  'ev_94d77e4efdec3e6c9bda9cf14c6d8d5c9a1f1f1b2256b4807b585fc31b3617eb66',
                                  1, None, True])
def test_invalid_alias_never_recovers_or_infers_a_source(bad):
    from lazymind.structured_tasks.paper_guide_references import SourceReferences
    I, _, raw, output = cases()['extract']
    refs = SourceReferences('extract', I.model_validate(raw))
    encoded = model_output('extract', output)
    encoded['claims'][0]['evidence_ids'] = ['e1', bad]
    with pytest.raises(ValueError):
        refs.canonical_output(encoded, 256 << 10)


def test_visible_context_outside_packet_does_not_authorize_citation():
    from lazymind.structured_tasks.paper_guide_references import SourceReferences
    I, O, raw, output = cases()['section']
    raw['plan']['claims'][0]['evidence_ids'].append(B)
    data = I.model_validate(raw)
    refs = SourceReferences('section', data)
    assert refs.model_input['plan']['claims'][0]['evidence_ids'] == ['e1', 'e2']
    encoded = model_output('section', output)
    encoded['blocks'][0]['evidence_ids'].append('e2')
    with pytest.raises(ValueError, match='unknown ID'):
        bind_output(data, O.model_validate(refs.canonical_output(encoded, 256 << 10)))


@pytest.mark.parametrize(('field', 'limit'), [('claim_ids', 96), ('evidence_ids', 128), ('asset_ids', 32)])
def test_raw_list_limit_checked_before_alias_expansion_or_dedup(field, limit):
    from lazymind.structured_tasks.paper_guide_references import SourceReferences
    I, O, raw, output = cases()['plan']
    refs = SourceReferences('plan', I.model_validate(raw))
    encoded = model_output('plan', output)
    encoded['sections'][0][field] = ['e1'] * (limit + 1)
    with pytest.raises(ValueError):
        refs.canonical_output(encoded, 256 << 10)
    encoded['sections'][0][field] = ['e1', 'e1']
    canonical = O.model_validate(refs.canonical_output(encoded, 256 << 10))
    assert len(getattr(canonical.sections[0], field)) == 1


def test_request_maps_are_deterministic_and_isolated_under_concurrency():
    from lazymind.structured_tasks.paper_guide_references import SourceReferences
    def run(source):
        I, _, raw, output = cases()['extract']
        raw['evidence'][0]['id'] = source
        raw['source_sections'][0]['evidence_ids'] = [source]
        refs = SourceReferences('extract', I.model_validate(raw))
        first = refs.model_input
        assert first == SourceReferences('extract', I.model_validate(raw)).model_input
        expanded = refs.canonical_output(model_output('extract', output), 256 << 10)
        return expanded['claims'][0]['evidence_ids']
    with ThreadPoolExecutor(max_workers=4) as pool:
        assert list(pool.map(run, [A, B] * 10)) == [[x] for x in [A, B] * 10]


def test_expanded_canonical_output_has_its_own_size_limit():
    from lazymind.structured_tasks.paper_guide_references import SourceReferences
    I, _, raw, output = cases()['plan']
    refs = SourceReferences('plan', I.model_validate(raw))
    encoded = model_output('plan', output)
    for section in encoded['sections']:
        section['evidence_ids'] = ['e1'] * 128
    expanded = refs.canonical_output(encoded, 1 << 20)
    size = len(json.dumps(expanded, ensure_ascii=False, separators=(',', ':'), allow_nan=False).encode())
    assert len(json.dumps(encoded).encode()) < size
    assert refs.canonical_output(encoded, size) == expanded
    with pytest.raises(ValueError, match='canonical output size'):
        refs.canonical_output(encoded, size - 1)


def schema_source_paths(schema, pattern):
    found = set()
    def visit(node, path=()):
        if '$ref' in node:
            visit(schema['$defs'][node['$ref'].split('/')[-1]], path)
        if node.get('pattern') == pattern:
            found.add(path)
        for key, value in node.get('properties', {}).items():
            visit(value, (*path, key))
        if 'items' in node:
            visit(node['items'], (*path, '*'))
        for key in ('anyOf', 'oneOf', 'allOf'):
            for child in node.get(key, []):
                visit(child, path)
    visit(schema)
    return found


@pytest.mark.parametrize('task', cases())
def test_schema_source_paths_match_complete_explicit_mapping(task):
    from lazymind.structured_tasks.paper_guide_references import (
        INPUT_PATHS, OUTPUT_PATHS, ALIAS_PATTERN, CANONICAL_PATTERN, model_output_schema,
    )
    I, O, _, _ = cases()[task]
    assert schema_source_paths(I.model_json_schema(), CANONICAL_PATTERN) == (
        set(INPUT_PATHS[task]) | {('source_sections', '*', 'id')})
    assert schema_source_paths(O.model_json_schema(), CANONICAL_PATTERN) == set(OUTPUT_PATHS[task])
    shadow = model_output_schema(O)
    assert schema_source_paths(shadow, ALIAS_PATTERN) == set(OUTPUT_PATHS[task])
    assert not schema_source_paths(shadow, CANONICAL_PATTERN)
    # Canonical schema is neither mutated nor broadened by generating the view.
    assert schema_source_paths(O.model_json_schema(), CANONICAL_PATTERN) == set(OUTPUT_PATHS[task])


@pytest.mark.parametrize('task', cases())
def test_max_catalog_and_section_view_remains_bounded_and_complete(task):
    from lazymind.structured_tasks.paper_guide_references import SourceReferences
    I, _, raw, _ = cases()[task]
    raw['evidence'] = [{**raw['evidence'][0], 'id': f'ev_{n:064x}'} for n in range(2048)]
    raw['source_sections'] = [{**raw['source_sections'][0], 'id': f'ev_{n + 3000:064x}',
                               'evidence_ids': [raw['evidence'][n]['id']]} for n in range(512)]
    data = I.model_validate(raw)
    view = SourceReferences(task, data).model_input
    assert len(view['evidence']) == 2048 and len(view['source_sections']) == 512
    assert view['evidence'][-1]['id'] == 'e2048'
    assert view['source_sections'][-1]['id'] == 's512'
    assert len(json.dumps(view, ensure_ascii=False).encode()) <= len(json.dumps(data.model_dump(exclude_none=True), ensure_ascii=False).encode())


@pytest.mark.parametrize('duplicate', ['claim', 'decision', 'block', 'claim_evidence'])
def test_aliases_do_not_merge_definitions_or_other_reference_lists(duplicate):
    from lazymind.structured_tasks.paper_guide_references import SourceReferences
    task = 'section' if duplicate == 'block' else 'plan'
    I, O, raw, output = cases()[task]
    encoded = model_output(task, output)
    if duplicate == 'claim':
        encoded['claims'] *= 2
    elif duplicate == 'decision':
        encoded['required_assets'] *= 2
    elif duplicate == 'block':
        encoded['blocks'] *= 2
    else:
        encoded['claims'][0]['evidence_ids'] *= 2
    data = I.model_validate(raw)
    with pytest.raises(ValueError):
        bind_output(data, O.model_validate(SourceReferences(task, data).canonical_output(encoded, 256 << 10)))


def test_alias_output_under_limit_can_expand_past_256k_and_must_fail():
    from lazymind.structured_tasks.paper_guide_references import SourceReferences
    I, _, raw, output = cases()['plan']
    raw['evidence'] = [{**raw['evidence'][0], 'id': f'ev_{n:064x}'} for n in range(32)]
    refs = SourceReferences('plan', I.model_validate(raw))
    encoded = model_output('plan', output)
    encoded['claims'] = [{**encoded['claims'][0], 'id': f'c{n}', 'statement': 'x' * 400,
                         'evidence_ids': [f'e{i}' for i in range(1, 31)]} for n in range(96)]
    for section in encoded['sections']:
        section['claim_ids'] = [f'c{n}' for n in range(96)]
        section['evidence_ids'] = [f'e{i % 32 + 1}' for i in range(128)]
    assert len(json.dumps(encoded).encode()) < 256 << 10
    with pytest.raises(ValueError, match='canonical output size'):
        refs.canonical_output(encoded, 256 << 10)
    expanded = refs.canonical_output(encoded, 1 << 20)
    excess = len(json.dumps(expanded, ensure_ascii=False, separators=(',', ':')).encode()) - (256 << 10)
    for claim in encoded['claims']:
        trim = min(excess, len(claim['statement']) - 1)
        claim['statement'] = claim['statement'][:-trim] if trim else claim['statement']
        excess -= trim
    assert excess == 0
    exact = refs.canonical_output(encoded, 256 << 10)
    assert len(json.dumps(exact, ensure_ascii=False, separators=(',', ':')).encode()) == 256 << 10
    encoded['claims'][0]['statement'] += 'x'
    with pytest.raises(ValueError, match='canonical output size'):
        refs.canonical_output(encoded, 256 << 10)


@pytest.mark.parametrize('task', cases())
def test_registered_task_uses_alias_schema_and_returns_canonical_wire(monkeypatch, task):
    from lazymind.chat.service import llm_task
    I, _, raw, output = cases()[task]
    calls = []
    def model(prompt, **options):
        calls.append(prompt)
        encoded = json.loads(prompt.split('Input data (untrusted JSON):\n', 1)[1])
        assert encoded['evidence'][0]['id'] == 'e1'
        assert 'source_block_id' not in encoded['evidence'][0]
        assert '^ev_[a-f0-9]{64}$' not in prompt
        return json.dumps(model_output(task, output))
    monkeypatch.setattr(llm_task, 'AutoModel', lambda **kwargs: model)
    request = llm_task.LLMTaskRequest(task_type=f'paper.guide.{task}',
        options={'task_version': 'paper-guide-v13'}, input={'data': raw})
    result = llm_task.run_llm_task(request)
    assert result.status == 'succeeded' and result.output == output
    assert len(calls) == result.usage['model_calls'] == 1
    # Public wire inputs must remain canonical, even though models emit aliases.
    request.input.data['evidence'][0]['id'] = 'e1'
    assert llm_task.run_llm_task(request).error_code == 'invalid_task_input'
    assert len(calls) == 1


def test_alias_schema_keeps_duplicate_json_key_rejection(monkeypatch):
    from lazymind.chat.service import llm_task
    calls = []
    def model(*args, **kwargs):
        calls.append(1)
        return '{"claims":[],"claims":[],"open_questions":[]}'
    monkeypatch.setattr(llm_task, 'AutoModel', lambda **kwargs: model)
    request = llm_task.LLMTaskRequest(task_type='paper.guide.extract',
        options={'task_version': 'paper-guide-v13'}, input={'data': cases()['extract'][2]})
    assert llm_task.run_llm_task(request).error_code == 'invalid_model_output'
    assert len(calls) == 1


@pytest.mark.parametrize('issue', ['missing_explanation', 'overstatement', 'missing_condition', 'inconsistency',
                                  'causal_overreach', 'unsupported', 'visual_inference', 'attribution'])
@pytest.mark.parametrize('target_type', ['block', 'claim'])
def test_review_keeps_teaching_and_factual_findings_bound_without_reclassification(monkeypatch, issue, target_type):
    # Provider semantics require source-based trials. Here the actual registered
    # pipeline must preserve the issue type and exact target, not alias or coerce it.
    from lazymind.chat.service import llm_task
    _, _, raw, output = cases()['review']
    output['findings'][0].update(issue_type=issue, target_type=target_type)
    monkeypatch.setattr(llm_task, 'AutoModel', lambda **kwargs: lambda *a, **k: json.dumps(output))
    request = llm_task.LLMTaskRequest(task_type='paper.guide.review',
        options={'task_version': 'paper-guide-v13'}, input={'data': raw})
    result = llm_task.run_llm_task(request)
    assert result.status == 'succeeded'
    assert result.output == output
    assert result.usage['model_calls'] == 1
    # The canonical evidence ID is not a claim/block repair target, even when
    # its request-local alias happens to match the valid target's literal ID.
    output['findings'][0]['target_id'] = A
    rejected = llm_task.run_llm_task(request)
    assert rejected.error_code == 'invalid_model_output'
    assert rejected.usage['model_calls'] == 1


def test_shared_go_fixture_is_the_exact_canonical_model_response():
    from lazymind.structured_tasks.paper_guide_references import SourceReferences
    fixture = json.loads((Path(__file__).parents[2] / 'fixtures/paper-guide-alias-v4.json').read_text())
    for task, (I, O, raw, _) in cases().items():
        data = I.model_validate(raw)
        expanded = SourceReferences(task, data).canonical_output(fixture['model_outputs'][task], 256 << 10)
        canonical = O.model_validate(expanded)
        bind_output(data, canonical)
        assert canonical.model_dump(exclude_unset=True) == fixture[task]


def test_formula_and_output_prose_literals_are_not_reference_locations():
    from lazymind.structured_tasks.paper_guide_references import SourceReferences
    I, O, raw, output = cases()['extract']
    formula = f'e1 + {A}'
    raw['evidence'][0].update(source_type='formula', locator={'kind': 'formula', 'page': 0, 'latex': formula})
    refs = SourceReferences('extract', I.model_validate(raw))
    assert refs.model_input['evidence'][0]['locator']['latex'] == formula
    encoded = model_output('extract', output)
    encoded['claims'][0]['statement'] = f'e1 and {A} are literal labels in this example.'
    expanded = refs.canonical_output(encoded, 256 << 10)
    assert expanded['claims'][0]['statement'] == encoded['claims'][0]['statement']
    assert O.model_validate(expanded).claims[0].evidence_ids == [A]


@pytest.mark.parametrize('bad', ['e2', 's1', 'E1', 'e01', A])
def test_alias_failure_never_triggers_an_extra_model_attempt(monkeypatch, bad):
    from lazymind.chat.service import llm_task
    calls = []
    output = model_output('extract', cases()['extract'][3])
    output['claims'][0]['evidence_ids'] = [bad]
    def model(*args, **kwargs):
        calls.append(1)
        return json.dumps(output)
    monkeypatch.setattr(llm_task, 'AutoModel', lambda **kwargs: model)
    request = llm_task.LLMTaskRequest(task_type='paper.guide.extract',
        options={'task_version': 'paper-guide-v13'}, input={'data': cases()['extract'][2]})
    result = llm_task.run_llm_task(request)
    assert result.error_code == 'invalid_model_output'
    assert len(calls) == result.usage['model_calls'] == 1


def test_concurrent_registered_calls_cannot_share_alias_identity(monkeypatch):
    from lazymind.chat.service import llm_task
    output = model_output('extract', cases()['extract'][3])
    def model(*args, **kwargs):
        return json.dumps(output)
    monkeypatch.setattr(llm_task, '_call_model', model)
    def run(source):
        raw = cases()['extract'][2]
        raw['evidence'][0]['id'] = source
        raw['source_sections'][0]['evidence_ids'] = [source]
        request = llm_task.LLMTaskRequest(task_type='paper.guide.extract',
            options={'task_version': 'paper-guide-v13'}, input={'data': raw})
        result = llm_task.run_llm_task(request)
        assert result.status == 'succeeded' and result.usage['model_calls'] == 1
        return result.output['claims'][0]['evidence_ids']
    with ThreadPoolExecutor(max_workers=4) as pool:
        assert list(pool.map(run, [A, B] * 4)) == [[x] for x in [A, B] * 4]


def test_page_region_identity_does_not_leak_or_become_an_asset():
    from lazymind.structured_tasks.paper_guide_references import SourceReferences
    raw = cases()['extract'][2]
    raw['evidence'][0].update(source_region_id='region_' + 'f' * 64, source_type='text',
                              text='Verified continuation on physical page seven.',
                              locator={'kind': 'page', 'page': 6})
    packet = GuideInput.model_validate(raw)
    aliases = SourceReferences('extract', packet)
    evidence = aliases.model_input['evidence'][0]
    assert evidence['locator'] == {'kind': 'page', 'page': 6}
    assert 'source_region_id' not in evidence and 'source_block_id' not in evidence
    assert evidence['source_type'] == 'text' and evidence['id'] == 'e1'


def fragment_input():
    raw = cases()['extract'][2]
    raw['evidence'][0]['fragment_evidence_ids'] = [B]
    raw['evidence'].append({'id': B, 'source_block_id': 'internal-block',
                            'source_region_id': 'region_physical', 'source_type': 'text',
                            'parent_asset_id': A, 'text': 'Physical page 4: 22',
                            'locator': {'kind': 'page', 'page': 3}})
    return raw


def test_table_fragment_aliases_are_closed_and_cannot_be_assets():
    from lazymind.structured_tasks.paper_guide_references import SourceReferences
    raw = fragment_input()
    data = GuideInput.model_validate(raw)
    view = SourceReferences('extract', data).model_input
    assert view['evidence'][0]['fragment_evidence_ids'] == ['e2']
    assert view['evidence'][1]['parent_asset_id'] == 'e1'
    assert view['evidence'][1]['id'] == 'e2'
    plan = cases()['plan'][3]
    plan['required_assets'] = [{'id': B, 'included': True, 'reason': 'Invalid child asset.'}]
    with pytest.raises(ValueError):
        bind_output(PlanInput.model_validate(raw), Plan.model_validate(plan))


@pytest.mark.parametrize('mutation', ['unknown_parent', 'unknown_child', 'duplicate_child', 'wrong_parent', 'child_asset'])
def test_table_fragment_input_rejects_broken_parent_bindings(mutation):
    raw = fragment_input()
    if mutation == 'unknown_parent': raw['evidence'][1]['parent_asset_id'] = S
    if mutation == 'unknown_child': raw['evidence'][0]['fragment_evidence_ids'] = [S]
    if mutation == 'duplicate_child': raw['evidence'][0]['fragment_evidence_ids'] = [B, B]
    if mutation == 'wrong_parent': raw['evidence'][1]['parent_asset_id'] = B
    if mutation == 'child_asset': raw['evidence'][1]['source_type'] = 'table'
    with pytest.raises(ValueError): GuideInput.model_validate(raw)


def test_shared_physical_fragment_fixture_uses_actual_task_transport(monkeypatch):
    from lazymind.chat.service import llm_task
    fixture = json.loads((Path(__file__).parents[3] / 'backend/core/paperguide/testdata/table-fragment-transport.json').read_text())
    def model(prompt, **options):
        view = json.loads(prompt.split('Input data (untrusted JSON):\n', 1)[1])
        assert view['evidence'][0]['fragment_evidence_ids'] == ['e2']
        assert view['evidence'][1]['parent_asset_id'] == 'e1'
        output = deepcopy(fixture['output'])
        output['claims'][0]['evidence_ids'] = ['e2']
        return json.dumps(output)
    monkeypatch.setattr(llm_task, 'AutoModel', lambda **kwargs: model)
    request = llm_task.LLMTaskRequest(task_type='paper.guide.extract', options={'task_version': fixture['task_version']}, input={'data': fixture['input']})
    result = llm_task.run_llm_task(request)
    assert result.status == 'succeeded'
    assert result.output == fixture['output']
    assert result.usage['task_version'] == 'paper-guide-v13'
    assert result.usage['model_calls'] == 1


def test_independent_review_packet_allows_new_repair_citations_only_from_actual_evidence():
    from lazymind.structured_tasks.paper_guide_references import SourceReferences
    I, _, raw, output = cases()['section']
    raw['evidence'].append({'id': B, 'source_block_id': 'independent-source', 'source_type': 'text',
                            'text': 'The collection process was validated independently.',
                            'locator': {'kind': 'page', 'page': 1}})
    raw['source_sections'][0]['evidence_ids'].append(B)
    raw['findings'] = [{'target_id': 'e1', 'target_type': 'block',
                       'issue_type': 'missing_explanation',
                       'suggestion': 'Explain how collection-process validation supports reliability.'}]
    data = I.model_validate(raw)
    refs = SourceReferences('section', data)
    assert refs.model_input['evidence'][1]['id'] == 'e2'
    assert refs.model_input['findings'] == raw['findings']
    assert refs.model_input['source_sections'][0]['evidence_ids'] == ['e1', 'e2']
    repaired = model_output('section', output)
    repaired['claims'] = [{'id': 'background-new', 'statement': raw['evidence'][1]['text'],
                           'attribution': 'paper_statement', 'evidence_ids': ['e2'],
                           'argument_type': None, 'conditions': [], 'support_status': 'supported'}]
    repaired['claim_ids'].append('background-new')
    repaired['blocks'].append({'id': 'background-validation', 'kind': 'paragraph',
                               'text': raw['evidence'][1]['text'], 'attribution': 'paper_statement',
                               'claim_ids': ['background-new'], 'evidence_ids': []})
    canonical = Section.model_validate(refs.canonical_output(repaired, 256 << 10))
    bind_output(data, canonical)
    assert canonical.claims[0].evidence_ids == [B]
    review_data = ReviewInput.model_validate(
        {k: v for k, v in {**raw, 'sections': [canonical.model_dump()]}.items()
         if k not in ('role', 'findings')})
    view = SourceReferences('review', review_data).model_input
    assert view['evidence'][1]['text'] == raw['evidence'][1]['text']
    assert view['sections'][0]['claims'][0]['evidence_ids'] == ['e2']
    assert view['plan']['claims'][0]['evidence_ids'] == ['e1']
    # A directory reference and a suggestion cannot authorize an absent source.
    narrowed = deepcopy(raw)
    narrowed['evidence'] = narrowed['evidence'][:1]
    narrow_data = I.model_validate(narrowed)
    narrow_refs = SourceReferences('section', narrow_data)
    with pytest.raises(ValueError, match='unknown ID'):
        bind_output(narrow_data, Section.model_validate(narrow_refs.canonical_output(repaired, 256 << 10)))
    repaired['claims'][0]['evidence_ids'] = ['e9999']
    with pytest.raises(ValueError):
        refs.canonical_output(repaired, 256 << 10)

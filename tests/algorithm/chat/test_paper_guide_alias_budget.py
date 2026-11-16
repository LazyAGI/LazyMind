"""Offline byte bounds over actual Go serialization and prepared model requests."""
from base64 import b64decode
from collections import Counter
from copy import deepcopy
import json
import os
from pathlib import Path
import subprocess
import sys

import pytest

from lazymind.structured_tasks.paper_guide_references import INPUT_PATHS, SourceReferences, _positions
from test_paper_guide_aliases import A, B, S, cases, section_claim_case
from test_paper_guide_asset_boundary import synthetic_evidence
from test_paper_guide_native_schema import (
    INPUT_MARKER, config, execute, http, section_maximum_asset_case,
)
from test_paper_guide_tasks import _request_budget_contract


def packet_cases():
    literal = f'测量 e1 < & > "\\\" \u2028\u2029 {A} $\\frac{{a}}{{b}}$'
    _, _, extract, _ = cases()['extract']
    extract['evidence'][0].update(
        fragment_evidence_ids=[B], text=literal,
        locator={'kind': 'table_cells', 'page': 0, 'bbox': [0, -0.0, 1e-9, sys.float_info.max]})
    extract['evidence'] += [
        dict(id=B, source_block_id='internal-block', source_region_id='span', source_type='text',
             parent_asset_id=A, text=literal + ' Printed page iv.', locator={'kind': 'page', 'page': 6}),
        dict(id='ev_' + 'd' * 64, source_block_id='text', source_type='text', text=literal,
             locator={'kind': 'text_span', 'page': 0, 'bbox': [0, 1, 5e-324, 1e20], 'end': 1}),
        dict(id='ev_' + 'e' * 64, source_block_id='formula', source_type='formula', text=literal,
             locator={'kind': 'formula', 'page': 0, 'latex': literal, 'bbox': None}),
    ]
    extract['source_sections'][0].update(title=literal, evidence_ids=[A, B, S, S])

    evidence = [synthetic_evidence(i, 'table') for i in range(1, 2039)]
    evidence += [synthetic_evidence(i, 'chart') for i in range(2039, 2043)]
    evidence += [synthetic_evidence(i, 'formula') for i in range(2043, 2049)]
    plan = {'language': 'zh', 'evidence': evidence, 'original_asset_candidates': {
        'figure': [{'id': ev['id'], 'mode': 'page'} for ev in evidence[-10:-6]],
        'formula': [{'id': ev['id'], 'mode': 'page'} for ev in evidence[-6:]],
    }}
    plan['source_sections'] = [
        {'id': f'ev_{i+2048:064x}', 'title': '目录', 'kind': 'section', 'pages': [0],
         'evidence_ids': [evidence[i]['id']]} for i in range(1, 513)
    ]
    _, _, template, _ = cases()['plan']
    claim = template['extracts'][0]['claims'][0]
    plan['extracts'] = [{'claims': [{**claim, 'evidence_ids': [evidence[0]['id']]}], 'open_questions': []}]
    context = deepcopy(template)
    context['source_sections'][0]['evidence_ids'] = [f'ev_{i:064x}' for i in range(9997)] + [A]
    too_many_bytes = deepcopy(context)
    too_many_bytes['evidence'][0]['text'] = 'x' * 262144
    too_many_bytes['source_sections'][0]['title'] = 'x' * 262144

    section, _ = section_claim_case()
    section['plan']['sections'][2]['evidence_ids'] = [A] * 128  # Pydantic narrows the raw list to one ID.
    _, _, review, _ = cases()['review']
    review['sections'][0]['blocks'][0]['text'] = literal * 18
    review['sections'][0]['claims'] = [
        {**review['plan']['claims'][0], 'id': 'draft', 'evidence_ids': [A, A]}]
    review['sections'][0]['blocks'][0]['evidence_ids'] = [A, A]
    return {
        'extract_defaults': ('extract', extract),
        'extract_empty': ('extract', {'language': 'en', 'evidence': []}),
        'plan_maximum_map': ('plan', plan),
        'plan_context_digits': ('plan', context),
        'plan_over_raw_cap': ('plan', too_many_bytes),
        'section_raw_before_projection': ('section', section),
        'section_maximum_map': ('section', section_maximum_asset_case('en')[0]),
        'review_actual_draft': ('review', review),
    }


@pytest.fixture(scope='module')
def go_packets(tmp_path_factory):
    if os.environ.get('LAZYMIND_TEST_GUIDE_ALIAS_WIRE') != '1':
        pytest.skip('set LAZYMIND_TEST_GUIDE_ALIAS_WIRE=1 to include the offline Go renderer cross-check')
    core = Path(__file__).resolve().parents[3] / 'backend/core'
    path = tmp_path_factory.mktemp('guide-go-budget') / 'packets.json'
    inputs = packet_cases()
    path.write_text(json.dumps({k: {'Task': t, 'Input': d} for k, (t, d) in inputs.items()},
                               ensure_ascii=False, allow_nan=False))
    env = dict(os.environ, LAZYMIND_GUIDE_ALIAS_CASES=str(path), GOPROXY='off', GOSUMDB='off',
               GOTOOLCHAIN='local', GOENV='off', GOFLAGS='', GOWORK='off', GOTELEMETRY='off')
    subprocess.run(['go', 'test', '-mod=readonly', '-count=1', '-run', '^TestExportGuideSourceAliasBudget$',
                    './paperguide'], cwd=core, env=env, check=True)
    results = json.loads(path.with_suffix('.json.result').read_text())
    return {key: (task, data, results[key]) for key, (task, data) in inputs.items()}


@pytest.mark.parametrize('case', packet_cases())
def test_alias_bound_with_actual_wire_and_go_bytes(go_packets, http, case):
    task, fixture, measured = go_packets[case]
    from lazymind.structured_tasks.registry import TASKS
    raw = b64decode(measured['Raw'])
    data = json.loads(raw)
    assert data == fixture
    spec = TASKS[f'paper.guide.{task}']
    canonical = spec.input_schema.model_validate(data)
    refs = SourceReferences(task, canonical)
    budget = _request_budget_contract()
    pre_credit = (len(raw) + budget['input_defaults_reserve_bytes']
                  + len(data['evidence']) * budget['evidence_expansion_reserve_bytes'])
    if case == 'plan_over_raw_cap':
        assert pre_credit > 1 << 20 and not measured['Eligible']
        return
    assert pre_credit <= 1 << 20 and measured['Eligible']

    def typed_values(obj):
        return [parent[key] for path in INPUT_PATHS[task] for parent, key in _positions(obj, path)]

    canonical_values = Counter(typed_values(data))
    model_values = typed_values(refs.model_input)
    assert all(alias in refs._alias_to_canonical for alias in model_values)
    assert Counter(refs._alias_to_canonical[alias] for alias in model_values) <= canonical_values
    gross_savings = sum(len(value) * times for value, times in canonical_values.items()) - sum(map(len, model_values))
    credit = pre_credit + budget['prompt_reserve_bytes'] - measured['Bound']
    dynamic_plan_growth = 0
    if task == 'plan':
        assets = refs.asset_instruction.partition('(JSON): ')[2]
        dynamic_plan_growth = len(assets.encode()) - 2
        evidence_aliases = [entry['id'] for entry in refs.model_input['evidence']]
        assert all(alias in evidence_aliases for alias in json.loads(assets))
        first_savings = sum(67 - len(alias) for alias in evidence_aliases)
        assert dynamic_plan_growth <= 18 * len(evidence_aliases) <= first_savings
    assert 0 <= credit <= gross_savings - dynamic_plan_growth
    assert credit > 0 or case == 'extract_empty'
    if case == 'plan_maximum_map':
        assert len(data['source_sections']) == 512
        assert len(json.loads(assets)) == 2048 and dynamic_plan_growth == 31676
    if case == 'plan_context_digits':
        assert sum(canonical_values.values()) == 10000 and len(refs._alias_to_canonical) > 2048
        assert credit == 122  # One source appears three times, each repeat saves at least 61.
    if case == 'section_maximum_map':
        assets = refs.asset_instruction.partition('(JSON): ')[2]
        assert len(assets.encode()) == 577 and len(json.loads(assets)) == 32
    if case == 'section_raw_before_projection':
        assert sum(canonical_values.values()) == 145 and len(canonical_values) == 3
        assert credit == 8946 and B in refs._alias_to_canonical.values()
        assert len(refs.model_input['plan']['sections'][2]['evidence_ids']) == 1
        assert any(c['id'] == 'c2' for c in data['plan']['claims'])
        assert all(c['id'] != 'c2' for c in refs.model_input['plan']['claims'])
    if case in ('extract_defaults', 'review_actual_draft'):
        for original, aliased in zip(data['evidence'], refs.model_input['evidence']):
            assert original['text'] == aliased['text']
            assert original['locator']['page'] == aliased['locator']['page']
            if 'latex' in original['locator']:
                assert original['locator']['latex'] == aliased['locator']['latex']
    if case == 'review_actual_draft':
        assert data['sections'][0]['blocks'][0]['text'] == refs.model_input['sections'][0]['blocks'][0]['text']

    sent = http({})
    for model in ('gpt-6-luna', 'unknown-model'):
        execute(task, data, config(model=model))
    assert len(sent) == 2
    for _, body in sent:
        user_message = body['input'][-1]['content']
        assert json.loads(user_message.split(INPUT_MARKER, 1)[1]) == refs.model_input
        system_bytes = sum(len(item['content'].encode()) for item in body['input'] if item['role'] != 'user')
        contract_bytes = len(json.dumps({'text': body['text']}, ensure_ascii=False, separators=(',', ':')).encode())
        actual = (len(user_message.encode()) + contract_bytes + max(system_bytes, budget['provider_system_bytes'])
                  + budget['provider_frame_reserve_bytes'])
        assert actual <= measured['Bound'], (case, body['model'], actual, measured['Bound'])

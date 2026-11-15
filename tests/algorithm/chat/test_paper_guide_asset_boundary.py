"""A caption can support prose without becoming a displayable catalog asset."""
import json
from copy import deepcopy

import pytest

from test_paper_guide_aliases import A, B, cases


def mixed_material():
    _, _, source, output = cases()['plan']
    source['evidence'].append({'id': B, 'source_block_id': 'missing-caption',
        'source_type': 'text', 'text': 'Supplement Figure: subgroup trends. Image not supplied.',
        'locator': {'kind': 'page', 'page': 1}})
    claim = {'id': 'missing_material', 'statement': 'Only the supplement caption is supplied.',
             'attribution': 'paper_statement', 'evidence_ids': [B], 'argument_type': None,
             'conditions': ['The image itself is unavailable.'], 'support_status': 'partial'}
    output['claims'].append(claim)
    output['sections'][0]['claim_ids'].append('missing_material')
    output['sections'][0]['evidence_ids'].append(B)
    encoded = deepcopy(output)
    for claim in encoded['claims']:
        claim['evidence_ids'] = ['e1' if v == A else 'e2' for v in claim['evidence_ids']]
    for section in encoded['sections']:
        section['evidence_ids'] = ['e1' if v == A else 'e2' for v in section['evidence_ids']]
        section['asset_ids'] = ['e1']
    encoded['required_assets'][0]['id'] = 'e1'
    return source, output, encoded


def invoke(monkeypatch, source, output, version='paper-guide-v14'):
    from lazymind.chat.service import llm_task
    calls = []
    def model(prompt, **options):
        calls.append(prompt)
        return json.dumps(output)
    monkeypatch.setattr(llm_task, 'AutoModel', lambda **kwargs: model)
    result = llm_task.run_llm_task(llm_task.LLMTaskRequest(task_type='paper.guide.plan',
        options={'task_version': version}, input={'data': source}))
    return result, calls


@pytest.mark.parametrize('kind', ['image', 'chart', 'table', 'formula'])
@pytest.mark.parametrize('included', [True, False])
def test_supplied_asset_and_missing_caption_keep_distinct_roles(monkeypatch, kind, included):
    source, canonical, encoded = mixed_material()
    # No pixels/table cells/LaTeX metadata: eligibility follows the real source type.
    source['evidence'][0]['source_type'] = kind
    for plan in (canonical, encoded):
        plan['required_assets'][0]['included'] = included
        if not included:
            for section in plan['sections']:
                section['asset_ids'] = []
    result, calls = invoke(monkeypatch, source, encoded)
    assert result.status == 'succeeded' and result.output == canonical
    assert len(calls) == result.usage['model_calls'] == 1
    assert result.output['claims'][1]['evidence_ids'] == [B]
    assert result.output['claims'][1]['conditions'] == ['The image itself is unavailable.']
    assert source['evidence'][1]['source_type'] == 'text'


@pytest.mark.parametrize('included', [True, False])
def test_actual_failure_shape_text_caption_decision_is_rejected_without_repair(monkeypatch, included):
    source, _, encoded = mixed_material()
    # Minimal form of both real v4 failures: genuine caption evidence excluded as an asset.
    encoded['required_assets'].append({'id': 'e2', 'included': included,
                                      'reason': 'Only a caption, no supplement image.'})
    if included:
        encoded['sections'][0]['asset_ids'].append('e2')
    original = deepcopy(encoded)
    result, calls = invoke(monkeypatch, source, encoded)
    assert result.error_code == 'invalid_model_output' and result.output == {}
    assert len(calls) == result.usage['model_calls'] == 1
    assert encoded == original


def test_unknown_excluded_asset_is_not_a_missing_resource_escape_hatch(monkeypatch):
    source, _, encoded = mixed_material()
    encoded['required_assets'].append({'id': 'e999', 'included': False, 'reason': 'Unavailable.'})
    result, calls = invoke(monkeypatch, source, encoded)
    assert result.error_code == 'invalid_model_output' and len(calls) == 1


@pytest.mark.parametrize('version', ['paper-guide-v4', 'paper-guide-v5', 'paper-guide-v6', 'paper-guide-v7', 'paper-guide-v8', 'paper-guide-v9'])
def test_old_request_cannot_spend_under_precision_contract(monkeypatch, version):
    source, _, encoded = mixed_material()
    result, calls = invoke(monkeypatch, source, encoded, version=version)
    assert result.error_code == 'unsupported_task_version' and calls == []


def invoke_section(monkeypatch, data, output):
    from lazymind.chat.service import llm_task
    from lazymind.structured_tasks.registry import PAPER_GUIDE_VERSION

    prompts = []

    def model(prompt, **options):
        prompts.append(prompt)
        return json.dumps(output)

    monkeypatch.setattr(llm_task, 'AutoModel', lambda **kwargs: model)
    result = llm_task.run_llm_task(llm_task.LLMTaskRequest(
        task_type='paper.guide.section', options={'task_version': PAPER_GUIDE_VERSION}, input={'data': data}))
    assert len(prompts) == result.usage['model_calls'] == 1
    return result, prompts[0]


def synthetic_evidence(i, source_type='text', text='Synthetic measurement.'):
    return {'id': f'ev_{i:064x}', 'source_block_id': f'block-{i}',
            'source_type': source_type, 'text': text, 'locator': {'kind': 'page', 'page': 1}}


def visual_section_input(mode):
    from test_paper_guide_tasks import PLAN

    table = synthetic_evidence(1, 'table', 'Fig. 1. Measurements by group.')
    fragment = synthetic_evidence(2, 'text', 'Physical table continuation.')
    chart = synthetic_evidence(3, 'chart')
    image = synthetic_evidence(4, 'image')
    formula = synthetic_evidence(5, 'formula')
    other = synthetic_evidence(6, 'table')
    missing = synthetic_evidence(7, 'image')
    excluded = synthetic_evidence(8, 'table')
    table['fragment_evidence_ids'] = [fragment['id']]
    fragment.update(parent_asset_id=table['id'], source_block_id=table['source_block_id'],
                    source_region_id='region_synthetic')
    assets = [chart, table, formula, image, other, missing]
    plan = deepcopy(PLAN)
    plan['required_assets'] = [
        {'id': ev['id'], 'included': True, 'reason': 'Explain the evidence.'} for ev in assets]
    plan['required_assets'].append({'id': excluded['id'], 'included': False, 'reason': 'Prose is sufficient.'})
    for s in plan['sections']:
        s['evidence_ids'] = [table['id']]
    plan['sections'][0]['asset_ids'] = [ev['id'] for ev in assets if ev is not other]
    plan['sections'][1]['asset_ids'] = [other['id']]
    plan['sections'][0]['evidence_ids'].extend([other['id'], fragment['id'], excluded['id']])
    evidence = [table, fragment, chart, image, formula, other, excluded]
    data = {'language': 'en', 'evidence': evidence, 'plan': plan,
            'role': 'question' if mode == 'other_role' else 'background'}
    if mode in ('local_repair', 'whole_repair'):
        data['findings'] = [{'target_id': 'background-b1', 'target_type': 'block',
                             'issue_type': 'coherence', 'suggestion': 'Explain the table measurements.'}]
    if mode == 'whole_repair':
        data['source_sections'] = [{'id': synthetic_evidence(9)['id'], 'title': 'Independent sources',
                                    'kind': 'section', 'pages': [1],
                                    'evidence_ids': [ev['id'] for ev in evidence]}]
    return data


def section_output(role, asset=None, kind='paragraph', evidence=('e2', 'e6')):
    block = {'id': f'{role}-synthetic', 'kind': kind, 'text': 'Explain the supplied measurement.',
             'attribution': 'teaching', 'evidence_ids': list(evidence), 'claim_ids': []}
    if asset is not None:
        block['asset_id'] = asset
    return {'role': role, 'title': role.title(), 'summary': 'Synthetic measurements.',
            'blocks': [block], 'claims': [], 'claim_ids': []}


def split_visual_prefix(prompt):
    marker = '\n\nInput data (untrusted JSON):\n'
    prefix, encoded = prompt.split(marker, 1)
    instruction, line = prefix.rsplit('\n', 1)
    label = 'Allowed visual blocks by source alias for the requested role (JSON): '
    assert line.startswith(label), 'SECTION model request lacks the trusted role asset field'
    mapping = line.removeprefix(label)
    return instruction, mapping, encoded, len((prefix + marker).encode('utf-8'))


@pytest.mark.parametrize(('mode', 'allowed', 'asset', 'kind'), [
    ('normal', '{"e3":"figure","e1":"table","e5":"formula","e4":"figure"}', 'e1', 'table'),
    ('local_repair', '{"e3":"figure","e1":"table","e5":"formula","e4":"figure"}', 'e5', 'formula'),
    ('whole_repair', '{"e3":"figure","e1":"table","e5":"formula","e4":"figure"}', 'e4', 'figure'),
    ('other_role', '{"e6":"table"}', 'e6', 'table'),
])
def test_section_model_receives_only_current_roles_present_asset_pairs(monkeypatch, mode, allowed, asset, kind):
    data = visual_section_input(mode)
    result, prompt = invoke_section(monkeypatch, data, section_output(data['role'], asset, kind))
    assert result.status == 'succeeded'
    _, mapping, encoded, _ = split_visual_prefix(prompt)
    assert mapping == allowed
    view = json.loads(encoded)
    assert view['evidence'][0]['text'] == 'Fig. 1. Measurements by group.'
    assert view['evidence'][1]['parent_asset_id'] == 'e1'
    assert view['plan']['sections'][0]['asset_ids'] == ['e3', 'e1', 'e5', 'e4', 'e8']
    assert result.output['blocks'][0]['asset_id'] == data['evidence'][int(asset[1:]) - 1]['id']
    assert result.output['blocks'][0]['evidence_ids'] == [data['evidence'][1]['id'], data['evidence'][5]['id']]


@pytest.mark.parametrize(('mutation', 'asset', 'kind'), [
    ('wrong kind despite caption', 'e1', 'figure'),
    ('asset for another role in packet', 'e6', 'table'),
    ('fragment is not an asset', 'e2', 'table'),
    ('included asset missing from packet', 'e8', 'figure'),
    ('excluded asset in packet', 'e7', 'table'),
    ('unknown alias', 'e999', 'table'),
    ('asset on text block', 'e1', 'paragraph'),
    ('extra field', 'e1', 'table'),
    ('unbound claim', 'e1', 'table'),
])
def test_visual_prefix_does_not_relax_section_output_binding(monkeypatch, mutation, asset, kind):
    data = visual_section_input('whole_repair')
    output = section_output('background', asset, kind)
    if mutation == 'extra field':
        output['blocks'][0]['source_kind_override'] = 'table'
    if mutation == 'unbound claim':
        output['claims'] = [{
            'id': 'not-used', 'statement': 'A measurement.', 'attribution': 'paper_statement',
            'evidence_ids': ['e1'], 'argument_type': None, 'conditions': [], 'support_status': 'supported',
        }]
        output['claim_ids'] = ['not-used']
    result, _ = invoke_section(monkeypatch, data, output)
    assert result.error_code == 'invalid_model_output' and result.output == {}


@pytest.mark.parametrize('language', ['en', 'zh'])
@pytest.mark.parametrize('mode', ['normal', 'local_repair', 'whole_repair'])
@pytest.mark.parametrize('allow_assets', [True, False])
def test_maximum_actual_section_prefix_fits_shared_reserve(monkeypatch, language, mode, allow_assets):
    from lazyllm.module.llms.onlinemodule.supplier.openai import OpenAIChat
    from lazymind.structured_tasks.paper_guide_references import SourceReferences
    from lazymind.structured_tasks.registry import TASKS
    from test_paper_guide_tasks import PLAN, _request_budget_contract

    data = {'language': language, 'role': 'background', 'plan': deepcopy(PLAN), 'evidence': [
        synthetic_evidence(i, 'text' if i <= 2016 else 'formula') for i in range(1, 2049)]}
    context_only, plan_only = synthetic_evidence(2049)['id'], synthetic_evidence(2050)['id']
    plan = data['plan']
    for section in plan['sections']:
        section['evidence_ids'] = [plan_only]
    if allow_assets:
        allowed = [ev['id'] for ev in data['evidence'][-32:]]
        plan['sections'][0]['asset_ids'] = allowed
        plan['required_assets'] = [{'id': id_, 'included': True, 'reason': 'Explain each quantity.'}
                                   for id_ in allowed]
    if mode in ('local_repair', 'whole_repair'):
        data['findings'] = [{'target_id': 'background-b1', 'target_type': 'block',
                             'issue_type': 'coherence', 'suggestion': 'Explain the measurement.'}]
    if mode in ('normal', 'whole_repair'):
        data['source_sections'] = [{'id': synthetic_evidence(2051)['id'], 'title': 'Source directory',
                                    'kind': 'section', 'pages': [1], 'evidence_ids': [context_only]}]

    result, prompt = invoke_section(monkeypatch, data, section_output('background', evidence=()))
    assert result.status == 'succeeded'
    instruction, mapping, encoded, prefix_bytes = split_visual_prefix(prompt)
    budget = _request_budget_contract()
    spec = TASKS['paper.guide.section']
    canonical = spec.input_schema.model_validate(data)
    # The only additions appear outside the existing untrusted input wrapper.
    assert instruction == spec.instruction(canonical)
    expected_input = SourceReferences('section', canonical).model_input
    assert encoded == json.dumps(expected_input, ensure_ascii=False, allow_nan=False)
    view = json.loads(encoded)
    assert len(view['evidence']) == 2048 and view['evidence'][-1]['id'] == 'e2048'
    context_alias = 'e2049'
    if mode != 'local_repair':
        assert view['source_sections'][0]['evidence_ids'] == [context_alias]
        context_alias = 'e2050'
    assert view['plan']['sections'][0]['evidence_ids'] == [context_alias]
    expected = {f'e{i}': 'formula' for i in range(2017, 2049)} if allow_assets else {}
    assert mapping == json.dumps(expected, separators=(',', ':'))
    assert len(mapping.encode('utf-8')) == (577 if allow_assets else 2)
    system_bytes = len(OpenAIChat._get_system_prompt(None).encode('utf-8'))
    assert prefix_bytes + system_bytes + budget['provider_frame_reserve_bytes'] <= budget['prompt_reserve_bytes']
    assert prefix_bytes == budget['measured_prompt_bytes']['paper.guide.section'] - (0 if allow_assets else 575)

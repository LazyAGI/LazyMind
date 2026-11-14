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


def invoke(monkeypatch, source, output, version='paper-guide-v10'):
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

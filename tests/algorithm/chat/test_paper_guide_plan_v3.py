import json
from copy import deepcopy
from pathlib import Path

import pytest

from lazymind.structured_tasks.paper_guide_schemas import Plan, PlanInput, bind_output


def live_case():
    return json.loads((Path(__file__).parents[2] / 'fixtures/paper-guide-plan-v3.json').read_text())


def test_live_plan_references_are_canonicalized_without_changing_definitions():
    case = live_case()
    plan = Plan.model_validate(case['raw_plan'])
    bind_output(PlanInput.model_validate(case['input']), plan)
    assert plan.model_dump() == case['canonical_plan']
    assert plan.claims[0].id == 'existing_claim'


def test_live_missing_asset_decision_still_fails():
    case = live_case()
    raw = case['canonical_plan']
    raw['required_assets'] = []
    assert raw['sections'][2]['asset_ids'] == [case['missing_decision_asset_id']]
    with pytest.raises(ValueError):
        Plan.model_validate(raw)


@pytest.mark.parametrize(('field', 'limit'), [('claim_ids', 96), ('evidence_ids', 128), ('asset_ids', 32)])
@pytest.mark.parametrize('bad', ['too_many', 'wrong_type', 'bad_format', 'unknown', 'fuzzy'])
def test_reference_normalization_never_bypasses_raw_limits_or_identity(field, limit, bad):
    case = live_case()
    raw = deepcopy(case['canonical_plan'])
    value = raw['sections'][2][field][0]
    if bad == 'too_many':
        raw['sections'][2][field] = [value] * (limit + 1)
    elif bad == 'wrong_type':
        raw['sections'][2][field] = [value, 3]
    elif bad == 'bad_format':
        raw['sections'][2][field] = [value, value + ' ']
    elif bad == 'unknown':
        raw['sections'][2][field] = [value, 'unknown' if field == 'claim_ids' else 'ev_' + 'f' * 64]
    else:
        raw['sections'][2][field] = [value, value[:-1] + ('1' if value[-1] != '1' else '2')]
    with pytest.raises(ValueError):
        plan = Plan.model_validate(raw)
        bind_output(PlanInput.model_validate(case['input']), plan)


@pytest.mark.parametrize('duplicate', ['claim', 'asset_decision', 'source'])
def test_duplicate_definitions_remain_invalid(duplicate):
    case = live_case()
    if duplicate == 'claim':
        case['raw_plan']['claims'] *= 2
    elif duplicate == 'asset_decision':
        case['raw_plan']['required_assets'] *= 2
    else:
        case['input']['evidence'].append(case['input']['evidence'][0])
    with pytest.raises(ValueError):
        bind_output(PlanInput.model_validate(case['input']), Plan.model_validate(case['raw_plan']))


def test_excluded_or_unused_assets_remain_invalid():
    case = live_case()
    for excluded in (True, False):
        raw = deepcopy(case['canonical_plan'])
        if excluded:
            raw['required_assets'][0]['included'] = False
        else:
            raw['sections'][2]['asset_ids'] = []
        with pytest.raises(ValueError):
            Plan.model_validate(raw)


@pytest.mark.parametrize('mode', ['canonical', 'missing_decision', 'duplicate_json_key'])
def test_model_boundary_returns_only_canonical_wire_with_one_call(monkeypatch, mode):
    from lazymind.chat.service import llm_task
    case = live_case()
    raw = case['raw_plan']
    if mode == 'missing_decision':
        raw['required_assets'] = []
    output = json.dumps(raw)
    # Synthetic v4 encoding of this structural fixture, not a repair of paid output.
    for index, evidence in enumerate(case['input']['evidence'], 1):
        output = output.replace(evidence['id'], f'e{index}')
    if mode == 'duplicate_json_key':
        output = output.replace('"questions":', '"questions": [], "questions":', 1)
    calls = []

    def model(*args, **options):
        calls.append(options)
        return output

    monkeypatch.setattr(llm_task, 'AutoModel', lambda **kwargs: model)
    request = llm_task.LLMTaskRequest(task_type='paper.guide.plan',
        options={'task_version': 'paper-guide-v19'}, input={'data': case['input']})
    result = llm_task.run_llm_task(request)
    assert len(calls) == result.usage['model_calls'] == 1
    if mode == 'canonical':
        assert result.status == 'succeeded'
        assert result.output == case['canonical_plan']
    else:
        assert result.error_code == 'invalid_model_output'


@pytest.mark.parametrize('version', ['paper-guide-v1', 'paper-guide-v2', 'paper-guide-v3'])
def test_old_task_versions_make_zero_model_calls(monkeypatch, version):
    from lazymind.chat.service import llm_task
    monkeypatch.setattr(llm_task, 'AutoModel', lambda **kwargs: pytest.fail('old contract must not call model'))
    request = llm_task.LLMTaskRequest(task_type='paper.guide.plan',
        options={'task_version': version}, input={'data': live_case()['input']})
    assert llm_task.run_llm_task(request).error_code == 'unsupported_task_version'

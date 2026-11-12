"""Evidence-limited questions remain visible without inventing numeric results."""
import copy

import pytest

from lazymind.document_tools.paper.analysis import pipeline
from lazymind.document_tools.paper.analysis.schema import ContractError, normalize_input, validate_analysis


@pytest.fixture
def case():
    inputs = normalize_input(dict(topic='Separate aggregate measurements', data=dict(
        columns=['cohort', 'metric', 'before', 'after'], column_types=['string', 'string', 'number', 'number'],
        units=[None, None, 'point', 'point'], rows=[['A', 'x', 1., 2.], ['B', 'y', 3., 4.]])))
    plan = dict(questions=['Describe observed changes?', 'Compare the same metric between cohorts?'],
                outline='Keep the two measurement definitions separate.')
    sample_size = dict(source_rows=[dict(table_id='data', count=1)], n=None, unit='summary', excluded_n=None)
    row = dict(question_id='q1', label='Observed change', metric='score change', method='Supplied difference',
               value=1., unit='point', kind='effect', scope='Only the supplied measurement', sample_size=sample_size,
               columns=[dict(table_id='data', column='before'), dict(table_id='data', column='after')])
    unavailable = dict(question_id='q2', reason='Cohorts have disjoint metric identifiers.',
                       columns=[dict(table_id='data', column='metric'), dict(table_id='data', column='cohort')])
    results = dict(results=[row], unavailable_questions=[unavailable])
    return inputs, plan, results


def test_unavailable_question_preserves_identity_reason_and_real_sources_without_a_zero_result(case):
    inputs, plan, results = case
    before = copy.deepcopy(case)
    output = pipeline.build_analysis(inputs, plan, results)
    assert [(target['id'], target['status']) for target in output['targets']] == [
        ('q1', 'answered'), ('q2', 'unavailable')]
    target = output['targets'][1]
    assert target['question'] == plan['questions'][1]
    assert target['reason'] == results['unavailable_questions'][0]['reason']
    assert target['columns'] == results['unavailable_questions'][0]['columns']
    assert len(output['findings']) == 1 and output['findings'][0]['value'] == 1.
    assert output['findings'][0]['target_id'] == 'q1'
    assert any(target['reason'] in limit for limit in output['limitations'])
    assert case == before
    assert validate_analysis(output, inputs) == output


@pytest.mark.parametrize('fault', [
    'unknown', 'duplicate', 'also_answered', 'missing_reason', 'empty_sources', 'fake_source'])
def test_unavailable_entry_cannot_bypass_question_or_source_validation(case, fault):
    inputs, plan, results = case
    item = results['unavailable_questions'][0]
    if fault == 'unknown':
        item['question_id'] = 'unknown'
    elif fault == 'duplicate':
        results['unavailable_questions'].append(copy.deepcopy(item))
    elif fault == 'also_answered':
        item['question_id'] = 'q1'
    elif fault == 'missing_reason':
        item['reason'] = ' '
    elif fault == 'empty_sources':
        item['columns'] = []
    else:
        item['columns'][0]['column'] = 'invented'
    with pytest.raises(ValueError):
        pipeline.build_analysis(inputs, plan, results)


def test_unmentioned_question_still_fails_instead_of_being_silently_removed(case):
    inputs, plan, results = case
    results.pop('unavailable_questions')
    with pytest.raises(ContractError, match='q2: Compare the same metric between cohorts'):
        pipeline.build_analysis(inputs, plan, results)


def test_all_unavailable_reports_insufficient_material_instead_of_fabricating_findings(case):
    inputs, plan, results = case
    results['results'] = []
    results['unavailable_questions'].append(dict(
        question_id='q1', reason='No observation supports the requested target population.',
        columns=[dict(table_id='data', column='cohort')]))
    with pytest.raises(ContractError, match='没有可估计的数值结果') as failure:
        pipeline.build_analysis(inputs, plan, results)
    assert failure.value.code == 'MISSING_MATERIAL'
    assert 'q1:' in str(failure.value) and 'q2:' in str(failure.value)


def test_public_analysis_rejects_numeric_results_for_unavailable_targets(case):
    inputs, plan, results = case
    output = pipeline.build_analysis(inputs, plan, results)
    output['findings'][0]['target_id'] = 'q2'
    with pytest.raises(ContractError, match='目标或模型引用错误'):
        validate_analysis(output, inputs)


def test_old_targets_default_to_answered_and_f4_accepts_shared_section_constraints(case):
    inputs, plan, results = case
    plan['questions'] = plan['questions'][:1]
    results.pop('unavailable_questions')
    output = pipeline.build_analysis(inputs, plan, results)
    for target in output['targets']:
        target.pop('status')
        target.pop('reason')
    assert validate_analysis(output, inputs)['targets'][0]['status'] == 'answered'
    inputs['constraints']['required_sections'] = ['abstract', 'methods', 'results', 'limitations']
    assert normalize_input(inputs)['constraints']['required_sections'] == inputs['constraints']['required_sections']


def test_all_unavailable_gets_only_one_code_check_then_preserves_insufficiency(case, monkeypatch):
    from unittest.mock import Mock
    inputs, plan, results = case
    results['results'] = []
    results['unavailable_questions'].append(dict(
        question_id='q1', reason='No observations support the requested population.',
        columns=[dict(table_id='data', column='cohort')]))
    generate = Mock(return_value='def analyze(tables, plan, rng): return {}')
    execute = Mock(return_value=dict(results=results))
    monkeypatch.setattr(pipeline.model, 'generate_program', generate)
    monkeypatch.setattr(pipeline.executor, 'execute', execute)
    with pytest.raises(ContractError) as failure:
        pipeline.execute_analysis(inputs, plan, attempts=5)
    assert failure.value.code == 'MISSING_MATERIAL'
    assert 'q1:' in str(failure.value) and 'q2:' in str(failure.value)
    assert generate.call_count == execute.call_count == 2
    assert 'ALL_UNAVAILABLE_SELF_CHECK' in generate.call_args.args[3]


def test_empty_mapping_bug_gets_one_source_grounded_repair_without_changing_data(case, monkeypatch):
    from unittest.mock import Mock
    inputs, plan, results = case
    plan['questions'] = plan['questions'][:1]
    source_row = results['results'][0]
    source = f'''def analyze(tables, plan, rng):
    question_by_cohort = {{'q1': 'A'}}
    results = []
    for values in tables['data']['rows']:
        question_id = question_by_cohort.get(values[0])
        if question_id is not None:
            result = {source_row!r}
            result.update(question_id=question_id, value=values[3] - values[2])
            results.append(result)
    return dict(results=results, unavailable_questions=[] if results else [dict(
        question_id='q1', reason='No matching cohort observations.',
        columns=[dict(table_id='data', column='cohort')])])
'''
    original = copy.deepcopy(inputs)
    calls = Mock(side_effect=[dict(code=source), dict(replacements=[dict(
        start_line=2, end_line=2, code="    question_by_cohort = {'A': 'q1'}")])])
    executions = []

    def execute(payload):
        executions.append(copy.deepcopy(payload))
        namespace = {}
        exec(payload['code'], namespace)
        return dict(results=namespace['analyze'](payload['tables'], payload['plan'], None), versions={})

    monkeypatch.setattr(pipeline.model, 'call_code_model', calls)
    monkeypatch.setattr(pipeline.executor, 'execute', execute)
    code, history, rendered, analysis = pipeline.execute_analysis(inputs, plan, attempts=5)
    assert len(executions) == 2 and len(history) == 2
    assert history[0]['outcome'] == 'failed' and history[1]['outcome'] == 'passed'
    assert "{'A': 'q1'}" in code and analysis['findings'][0]['value'] == 1.
    assert analysis['targets'][0]['status'] == 'answered'
    assert 'ALL_UNAVAILABLE_SELF_CHECK' in str(calls.call_args)
    assert inputs == original and executions[0]['tables'] == executions[1]['tables']
    assert calls.call_count == 2


def test_all_unavailable_self_check_rejects_no_op_without_five_retries(case, monkeypatch):
    from unittest.mock import Mock
    inputs, plan, results = case
    results['results'] = []
    results['unavailable_questions'].append(dict(
        question_id='q1', reason='No requested population in these observations.',
        columns=[dict(table_id='data', column='cohort')]))
    calls = Mock(side_effect=[dict(code='def analyze(tables, plan, rng): return {}'), dict(replacements=[dict(
        start_line=1, end_line=1, code='def analyze(tables, plan, rng): return {}')])])
    execute = Mock(return_value=dict(results=results))
    monkeypatch.setattr(pipeline.model, 'call_code_model', calls)
    monkeypatch.setattr(pipeline.executor, 'execute', execute)
    with pytest.raises(ContractError) as failure:
        pipeline.execute_analysis(inputs, plan, attempts=5)
    assert failure.value.code == 'EXECUTION_FAILED'
    assert 'q1:' in str(failure.value) and 'q2:' in str(failure.value)
    assert '未改变源码' in str(failure.value)
    execute.assert_called_once()
    assert calls.call_count == 2


def test_all_unavailable_respects_one_attempt_budget(case, monkeypatch):
    from unittest.mock import Mock
    inputs, plan, results = case
    results['results'] = []
    results['unavailable_questions'].append(dict(
        question_id='q1', reason='No requested population in these observations.',
        columns=[dict(table_id='data', column='cohort')]))
    generate = Mock(return_value='def analyze(tables, plan, rng): return {}')
    execute = Mock(return_value=dict(results=results))
    monkeypatch.setattr(pipeline.model, 'generate_program', generate)
    monkeypatch.setattr(pipeline.executor, 'execute', execute)
    with pytest.raises(ContractError) as failure:
        pipeline.execute_analysis(inputs, plan, attempts=1)
    assert failure.value.code == 'MISSING_MATERIAL'
    generate.assert_called_once()
    execute.assert_called_once()

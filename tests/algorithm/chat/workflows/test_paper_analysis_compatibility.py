"""Shared native/external analysis parameter compatibility without invented results."""
import copy

import pytest

from lazymind.document_tools.paper.analysis import generated_executor, generated_worker, pipeline
from lazymind.document_tools.paper.analysis.schema import normalize_input


@pytest.fixture
def case():
    inputs = normalize_input(dict(topic='Observed scores', data=dict(
        columns=['score'], column_types=['number'], units=['point'], rows=[[2.], [4.]])))
    plan = dict(questions=['Describe recorded scores'], outline='Use actual observations.')
    row = dict(question_id='q1_recorded_scores', label='Mean score', metric='score', method='mean',
               value=3., kind='observation', columns=[dict(column='score')], scope=None,
               sample_size=dict(source_rows=[dict(table_id='data', count=2)], n=2, unit='observations'))
    result = dict(results=[row], models=None, limitations=None, unavailable_questions=None)
    return inputs, plan, result


def test_analysis_compatibility_preserves_values_and_source_parameters(case):
    inputs, plan, result = case
    original = copy.deepcopy(case)
    analysis = pipeline.build_analysis(inputs, plan, result)
    finding = analysis['findings'][0]
    assert finding['target_id'] == 'q1'
    assert finding['kind'] == 'observation'
    assert finding['value'] == 3.
    assert finding['columns'] == [dict(table_id='data', column='score')]
    assert finding['sample_size']['n'] == 2
    assert 'data: 2 source rows' in finding['calculation']
    assert case == original


@pytest.mark.parametrize('fault', ['unknown_question', 'unknown_source', 'ambiguous_source', 'missing_value',
                                   'missing_sample', 'excess_rows', 'nonfinite'])
def test_compatibility_does_not_guess_ambiguous_or_invalid_measurements(case, fault):
    inputs, plan, result = case
    row = result['results'][0]
    if fault == 'unknown_question':
        row['question_id'] = 'q9_unbound'
    elif fault == 'unknown_source':
        row['columns'][0]['table_id'] = 'invented'
    elif fault == 'ambiguous_source':
        table = inputs['data']
        inputs['data'] = dict(tables=[dict(id=identifier, table=copy.deepcopy(table)) for identifier in ['a', 'b']])
    elif fault == 'missing_value':
        row.pop('value')
    elif fault == 'missing_sample':
        row.pop('sample_size')
    elif fault == 'excess_rows':
        row['sample_size']['source_rows'][0]['count'] = 3
    elif fault == 'nonfinite':
        row['value'] = float('nan')
    with pytest.raises(ValueError):
        pipeline.build_analysis(inputs, plan, result)


CODE = '''import numpy as np
TABLE = "data"
COLUMNS = {TABLE: ["score"]}
def analyze(tables, plan, rng):
    table = tables[TABLE]
    assert table['id'] == table['table_id'] == TABLE
    return {'value': np.mean(table['rows']), 'n': np.int64(len(table['rows'])),
            'values': np.array(table['rows'])}
'''


def test_runtime_supplies_bound_table_identity_and_serializes_scientific_values(case):
    inputs, _, _ = case
    payload = dict(tables=dict(data=inputs['data']), plan={}, code=CODE, seed=42)
    original = copy.deepcopy(payload)
    generated_executor.validate_code(CODE)
    result = generated_worker.run(payload)['results']
    assert result == dict(value=3., n=2, values=[[2.], [4.]])
    assert payload == original


@pytest.mark.parametrize('statement', ['OTHER = unknown', 'OTHER = np.mean([1, 2])',
                                       'OTHER = TABLE.__class__', 'TABLE[0] = "x"'])
def test_constant_references_do_not_allow_top_level_computation_or_io(statement):
    with pytest.raises(ValueError):
        generated_executor.validate_code(CODE.replace('COLUMNS = {TABLE: ["score"]}', statement))


def test_scientific_serialization_still_rejects_nonfinite_results():
    with pytest.raises(ValueError):
        generated_worker.run(dict(tables={}, plan={}, seed=42,
                                  code='import numpy as np\ndef analyze(tables, plan, rng):\n'
                                       '    return {"value": np.float64(np.inf)}\n'))

"""Result-record snapshots preserve scientific identities before JSON serialization."""
import copy
import json
from unittest.mock import Mock

import pytest

from lazymind.document_tools.paper.analysis import generated_worker as worker, pipeline
from lazymind.document_tools.paper.analysis.schema import ContractError, normalize_input


CODE = '''def analyze(tables, plan, rng):
    results = []
    coordinates = {'method': 'A'}
    def add_result(version, value, coordinates):
        results.append(dict(question_id='q1', label=version, metric='score', method='Supplied score',
            scope='Actual source conditions', comparison=version, coordinates=coordinates,
            value=value, unit='point', kind='estimate', columns=[dict(table_id='data', column='score')],
            sample_size=dict(source_rows=[dict(table_id='data', count=1)], n=1, unit='observation')))
    for version, value in tables['data']['rows']:
        coordinates['version'] = version
        add_result(version, value, coordinates)
    return dict(results=results)
'''


def payload(source=CODE):
    return dict(tables=dict(data=dict(columns=['version', 'score'], column_types=['string', 'number'],
                                      units=[None, 'point'], rows=[['first', 1.], ['second', 2.]])),
                plan=dict(questions=[dict(id='q1', question='Compare supplied conditions')]), code=source, seed=42)


def test_worker_rejects_shared_coordinates_before_json_hides_the_alias():
    source = payload()
    before = copy.deepcopy(source)
    with pytest.raises(ValueError, match='SHARED_RESULT_COORDINATES: results 1 .*first.* and 2 .*second'):
        worker.run(source)
    assert source == before


def test_worker_accepts_independent_snapshots_and_retains_each_source_condition():
    source = CODE.replace('comparison=version, coordinates=coordinates,',
                          'comparison=version, coordinates=dict(coordinates),')
    output = worker.run(payload(source))['results']['results']
    assert [row['coordinates']['version'] for row in output] == ['first', 'second']
    assert [row['comparison'] for row in output] == ['first', 'second']
    assert [row['value'] for row in output] == [1., 2.]


def test_identical_independent_coordinates_can_describe_different_estimands():
    first = dict(metric='score', unit='point', kind='estimate', comparison='marginal effect',
                 coordinates=dict(group='A'), value=1.)
    second = dict(first, comparison='conditional effect', coordinates=dict(group='A'), value=2.)
    output = dict(results=[first, second])
    original = copy.deepcopy(output)
    worker.check_coordinate_snapshots(output)
    assert output == original


def test_shared_coordinates_with_same_comparison_or_different_quantity_are_not_relabelled():
    first = dict(metric='score', unit='point', kind='estimate', comparison='A-B',
                 coordinates=dict(group='A'), value=1.)
    output = dict(results=[first, dict(first, value=2.), dict(first, metric='count', comparison='A', value=3.)])
    original = copy.deepcopy(output)
    worker.check_coordinate_snapshots(output)
    assert output == original


def test_coordinate_alias_error_uses_existing_bounded_code_repair_and_preserves_values(monkeypatch):
    inputs = normalize_input(dict(topic='Compare conditions', data=payload()['tables']['data']))
    plan = dict(questions=['Compare supplied conditions'], outline='Keep both conditions separate.')
    line = next(index for index, value in enumerate(CODE.splitlines(), 1) if 'scope=' in value)
    replacement = CODE.splitlines()[line - 1].replace('coordinates=coordinates,', 'coordinates=dict(coordinates),')
    calls = Mock(side_effect=[dict(code=CODE), dict(replacements=[dict(
        start_line=line, end_line=line, code=replacement)])])
    monkeypatch.setattr(pipeline.model, 'call_code_model', calls)
    monkeypatch.setattr(pipeline.executor, 'execute', worker.run)
    code, history, rendered, analysis = pipeline.execute_analysis(inputs, plan, attempts=2)
    assert [item['outcome'] for item in history] == ['failed', 'passed']
    assert 'SHARED_RESULT_COORDINATES' in history[0]['error']
    assert [row['value'] for row in analysis['findings']] == [1., 2.]
    assert [row['comparison'] for row in rendered['results']['results']] == ['first', 'second']
    assert [row['coordinates']['version'] for row in rendered['results']['results']] == ['first', 'second']
    assert 'coordinates=dict(coordinates)' in code
    assert calls.call_count == 2


@pytest.mark.parametrize('converges', [True, False])
def test_code_repair_uses_current_error_and_retains_complete_failure_history(monkeypatch, converges):
    inputs = normalize_input(dict(topic='Compare conditions', data=payload()['tables']['data']))
    plan = dict(questions=['Compare supplied conditions'], outline='Keep both conditions separate.')
    correct = CODE.replace('comparison=version, coordinates=coordinates,',
                           'comparison=version, coordinates=dict(coordinates),')
    invalid_refs = correct.replace("table_id='data', column='score'", "table_id='table_id', column='column'")
    responses = [dict(code='def analyze(tables, plan, rng):\n    return row.耗时（秒）\n'), dict(code=invalid_refs)]
    if converges:
        responses.extend([dict(replacements=[dict(start_line=999, end_line=999, code='pass')]), dict(code=correct)])
    calls = Mock(side_effect=responses)
    monkeypatch.setattr(pipeline.model, 'call_code_model', calls)
    monkeypatch.setattr(pipeline.executor, 'execute', worker.run)
    if not converges:
        with pytest.raises(ContractError) as failure:
            pipeline.execute_analysis(inputs, plan, attempts=2)
        assert 'Attempt 1:' in str(failure.value) and 'SyntaxError' in str(failure.value)
        assert 'Attempt 2:' in str(failure.value) and '不存在的列引用' in str(failure.value)
        return
    _, history, _, analysis = pipeline.execute_analysis(inputs, plan, attempts=4)
    feedback = json.loads(calls.call_args.args[0]['input'][-1]['content'].split('INPUT_JSON:\n', 1)[1])['error']
    assert '不存在的列引用' in feedback and "ref['table_id']" in feedback
    assert '替换行号无效' in feedback
    assert 'SyntaxError' not in feedback and 'Attempt 1:' not in feedback
    assert 'SyntaxError' in history[0]['error'] and '不存在的列引用' in history[1]['error']
    assert [item['outcome'] for item in history] == ['failed', 'failed', 'failed', 'passed']
    assert [row['value'] for row in analysis['findings']] == [1., 2.]

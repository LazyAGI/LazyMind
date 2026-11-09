"""F3 contracts and dynamic SDK integration with offline provider responses."""
import copy
import csv
import json
import subprocess
import sys
from pathlib import Path
from zipfile import ZipFile

import pytest
import yaml

from lazymind.document_tools.paper import contracts, experiment, workflow
from lazymind.document_tools.paper.experiment.algorithm import build_output
from lazymind.document_tools.paper.experiment.artifacts import export_experiment
from lazymind.document_tools.paper.experiment.schema import ExperimentDraft
from lazymind.document_tools.paper.experiment.results import records_to_table, validate_table

ROOT = Path(__file__).resolve().parents[4]
PACKAGE = ROOT / 'workflows/paper-writing'


@pytest.fixture
def example():
    return json.loads((ROOT / 'tests/fixtures/paper-writing/experiment.json').read_text())


def test_experiment_package_keeps_one_public_algorithm_entry():
    from inspect import signature
    from lazymind.document_tools.paper import api
    from lazymind.document_tools.paper.experiment import algorithm

    assert experiment.__all__ == ['run']
    assert api.ALGORITHMS['experiment'] is experiment
    assert signature(experiment.run) == signature(algorithm.run)
    assert not hasattr(experiment, 'build_output')


def test_source_notice_is_idempotent_when_existing_charts_are_republished(example):
    from lazymind.document_tools.paper.experiment.handoff import mark_analysis
    output = dict(analysis=dict(limitations=[]), charts=[dict(id='fig1', title='Measured values', caption='Observed.')],
                  is_demo=False)
    original = copy.deepcopy(output)
    first = mark_analysis(output, example['output']['result_meta'])
    assert mark_analysis(first, example['output']['result_meta']) == first
    assert output == original


def test_example_matches_actual_contract_and_rendering(example):
    before = copy.deepcopy(example)
    draft = ExperimentDraft.model_validate(example['model_response'])
    output = build_output(draft, contracts.validate_input('experiment', example['input']))
    assert contracts.validate_output('experiment', output) == example['output']
    assert example == before


def test_existing_writer_parser_handles_fences_and_missing_comma(example):
    pytest.importorskip('json_repair', reason='Writer parser dependency is available in the Chat image')
    from lazyllm.tools.writer.tools.base import WriterToolBase
    raw = json.dumps(example['model_response'], ensure_ascii=False)
    malformed = raw.replace('", "explanation"', '" "explanation"', 1)
    for response in ('```json\n' + raw + '\n```', malformed):
        parsed = WriterToolBase._parse_structured_response(response, ExperimentDraft)
        draft = ExperimentDraft.model_validate(parsed)
        output = build_output(draft, example['input'])
        # Writer's syntax repair may trim trailing whitespace in string values.
        files = output.pop('code_files')
        expected = copy.deepcopy(example['output'])
        expected_files = expected.pop('code_files')
        assert output == expected
        assert [(f['path'], f['content'].rstrip()) for f in files] == [
            (f['path'], f['content'].rstrip()) for f in expected_files]


def test_model_extra_fields_are_dropped_without_mutating_input(example):
    candidate = example['model_response']
    candidate['executed'] = True
    candidate['code_files'][1]['requirements.txt'] = ''
    candidate['results']['metrics'][0]['unrequested_note'] = 'extra'
    before = copy.deepcopy(candidate)
    output = build_output(ExperimentDraft.model_validate(candidate), example['input'])
    assert output == example['output']
    assert candidate == before
    assert output['experiment']['executed'] is False
    invalid = copy.deepcopy(candidate)
    del invalid['code_files'][1]['content']
    with pytest.raises(ValueError):
        ExperimentDraft.model_validate(invalid)


@pytest.mark.parametrize('reason', ['', '  ', '未执行代码，但已提供模拟预览。'])
def test_unused_no_result_reason_is_cleared_when_preview_exists(example, reason):
    example['model_response']['no_results_reason'] = reason
    output = build_output(ExperimentDraft.model_validate(example['model_response']), example['input'])
    assert output == example['output']
    example['model_response']['results'] = None
    if not reason.strip():
        with pytest.raises(ValueError):
            ExperimentDraft.model_validate(example['model_response'])


def test_preview_column_order_is_normalized_by_name_not_relabelled(example):
    candidate = example['model_response']
    file = candidate['code_files'][0]
    file['content'] = file['content'].replace(
        "{'trial': 'integer', 'method': 'string', 'absolute_error': 'number'}",
        "{'absolute_error': 'number', 'method': 'string', 'trial': 'integer'}")
    output = build_output(ExperimentDraft.model_validate(candidate), example['input'])
    table = output['experiment']['results']
    assert table['columns'] == ['absolute_error', 'method', 'trial']
    assert table['rows'][0] == [0.45, 'mean', 0]
    assert table['rows'][1] == [0.1, 'median', 0]
    contracts.validate_input('data_analysis', {
        'topic': 'T', 'experiment': output['experiment'], 'result_meta': output['result_meta'],
    })


@pytest.mark.parametrize('change', [
    lambda v: v['code_files'][0].update(path='../escape.py'),
    lambda v: v['code_files'][0].update(path='README.md'),
    lambda v: v['code_files'][0].update(content='invalid python : :'),
    lambda v: v['code_files'][1].update(content='[]'),
    lambda v: v['code_files'][1].update(content='{"x":NaN}'),
    lambda v: v['results']['metrics'][0]['values'].append(42),
    lambda v: v['results']['metrics'][0]['values'].__setitem__(0, float('inf')),
    lambda v: v['results']['metrics'][0]['values'].__setitem__(0, True),
    lambda v: v['results']['metrics'][0].update(column='trial'),
    lambda v: v['results']['metrics'][0]['values'].__setitem__(0, '0.45'),
    lambda v: v.update(results=None),
    lambda v: v.update(plan='   '),
    lambda v: v.update(explanation='   '),
    lambda v: v.update(assumptions=['   ']),
    lambda v: v['results']['metrics'][0].update(description='   '),
    lambda v: v['results']['metrics'][0].update(unit='   '),
])
def test_invalid_generation_is_rejected(example, change):
    change(example['model_response'])
    with pytest.raises((ValueError, SyntaxError)):
        ExperimentDraft.model_validate(example['model_response'])


def test_execution_claim_cannot_be_faked(example):
    candidate = copy.deepcopy(example['model_response'])
    candidate.update(executed=True, execution_status='succeeded')
    output = build_output(ExperimentDraft.model_validate(candidate), example['input'])
    assert output['experiment']['executed'] is False
    assert output['result_meta']['execution_status'] == 'not_run'
    output['experiment']['executed'] = True
    with pytest.raises(ValueError, match='不一致'):
        contracts.validate_output('experiment', output)


def test_download_package_never_executes_code_and_records_preference(example, tmp_path):
    marker = tmp_path / 'must_not_exist'
    example['output']['code_files'][0]['content'] += f'\nopen({str(marker)!r}, "w").write("unsafe")\n'
    files = export_experiment(example['output'], str(tmp_path), preference='allow_local')
    assert not marker.exists()
    manifest = json.loads(Path(files['experiment_results']).read_text())
    assert manifest['execution_preference'] == 'allow_local'
    assert manifest['execution_available'] is False
    assert manifest['result_meta']['data_origin'] == 'synthetic'
    with ZipFile(files['experiment_code']) as archive:
        assert {'experiment.py', 'config.json', 'requirements.txt', 'README.md',
                'preview_results.json', 'preview_results.csv', '结果来源.json',
                '使用前必读.txt', 'result_schema.json', 'run_experiment.py'} == set(archive.namelist())
        assert json.loads(archive.read('preview_results.json')) == example['output']['experiment']['results']
        schema = json.loads(archive.read('result_schema.json'))
        assert schema['properties']['columns']['const'] == example['output']['experiment']['results']['columns']
        assert [item['type'] for item in schema['properties']['rows']['items']['prefixItems']] == [
            'integer', 'string', 'number']
    files = export_experiment(example['output'], str(tmp_path), preference='unknown')
    assert json.loads(Path(files['experiment_results']).read_text())['execution_preference'] == 'simulate_only'


def test_csv_preview_escapes_formulas_but_input_is_preserved():
    from lazymind.document_tools.paper.experiment.artifacts import csv_text
    table = {'columns': ['label', 'value'], 'rows': [['=1+1', -2]]}
    assert "'=1+1,-2" in csv_text(table)
    assert "'=1+1" not in csv_text(table, spreadsheet_safe=False)


def test_downloaded_results_can_enter_f4_without_reauthoring(example, tmp_path):
    files = export_experiment(example['output'], str(tmp_path), preference='allow_local')
    manifest = json.loads(Path(files['experiment_results']).read_text())
    before = copy.deepcopy(manifest)
    # The attachment validator and the runtime initializer must both accept the original file.
    workflow.pool_from_business(manifest)
    request = '分析附件中的实验结果，不运行实验'
    pool = workflow.initialize_pool({'paper_input': files['experiment_results']}, request)
    value = workflow.business_input('data_analysis', pool)
    assert value['data']['rows'] == manifest['experiment']['results']['rows']
    assert value['data']['columns'] == manifest['experiment']['results']['columns']
    assert workflow.material(pool, 'result_meta')['content'] == manifest['result_meta']
    assert '模型生成' in value['data_context']['description']
    assert not {'execution_preference', 'execution_available', 'notice'} & pool.keys()
    assert manifest == before
    with pytest.raises(ValueError, match='未知字段'):
        workflow.pool_from_business({**manifest, 'unexpected': True})
    invalid = copy.deepcopy(manifest)
    invalid['experiment']['results']['rows'][0].append(123)
    with pytest.raises(ValueError):
        workflow.pool_from_business(invalid)


def experiment_pool(example):
    pool = workflow.pool_from_business(dict(example['input'], data={'columns': ['old'], 'rows': [[999]]}))
    derived = list(pool['current'].values())
    outputs = [workflow.add_material(pool, key, example['output'][key], derived, [], is_demo=True)
               for key in contracts.OUTPUTS['experiment']]
    pool['history'].append({'node': 'F3', 'inputs': derived, 'outputs': outputs})
    pool['sequence'] = 1
    return workflow.validate_pool(pool)


def test_f4_receives_new_results_not_old_input_and_preserves_origin(example):
    pool = experiment_pool(example)
    value = contracts.validate_input('data_analysis', workflow.business_input('data_analysis', pool))
    assert value['experiment'] is None
    assert value['data']['rows'] == example['output']['experiment']['results']['rows']
    assert value['data']['column_types'] == ['integer', 'string', 'number']
    assert '模型生成' in value['data_context']['description']
    retry = contracts.validate_input('data_analysis', workflow.business_input('data_analysis', pool))
    assert retry == value


def test_no_results_cannot_fall_back_to_old_data(example):
    example['model_response'].update(results=None, no_results_reason='用户只接受真实结果，本版本不执行。')
    draft = ExperimentDraft.model_validate(example['model_response'])
    example['output'] = build_output(draft, example['input'])
    contracts.validate_output('experiment', example['output'])
    pool = experiment_pool(example)
    with pytest.raises(contracts.MissingMaterials, match='数据或实验结果'):
        contracts.validate_input('data_analysis', workflow.business_input('data_analysis', pool))


def test_no_result_package_keeps_planned_schema_without_fake_preview(example, tmp_path):
    example['model_response'].update(results=None, no_results_reason='需要用户数据，本版本未执行。')
    output = build_output(ExperimentDraft.model_validate(example['model_response']), example['input'])
    files = export_experiment(output, str(tmp_path))
    with ZipFile(files['experiment_code']) as archive:
        assert 'preview_results.json' not in archive.namelist()
        assert 'preview_results.csv' not in archive.namelist()
        assert json.loads(archive.read('result_schema.json'))['properties']['columns']['const'] == [
            'trial', 'method', 'absolute_error']
    assert output['result_meta']['data_origin'] == 'none'
    assert output['result_meta']['metrics'] == []


def test_uploaded_synthetic_metadata_cannot_lose_demo_marker(example):
    value = {key: example['output'][key] for key in ('experiment', 'result_meta')}
    pool = workflow.pool_from_business(dict(topic='T', **value))
    assert workflow.material(pool, 'experiment')['mock'] is True
    assert '模型生成' in workflow.business_input('data_analysis', pool)['data_context']['description']


@pytest.mark.parametrize('columns', [
    "{'trial': 'integer', 'sample_id': 'string', 'absolute_error': 'number'}",
    "{'trial': 'string', 'method': 'string', 'absolute_error': 'number'}",
    "{'trial': 'integer', 'method': 'string', 'absolute_error': 'string'}",
    "{'trial': 'integer', 'method': 'string', 'absolute_error': 'float'}",
    "{'trial': 'integer', 'trial': 'integer', 'absolute_error': 'number'}",
    "{' trial ': 'integer', 'method': 'string', 'absolute_error': 'number'}",
    "dict(trial='integer', method='string', absolute_error='number')",
])
def test_code_columns_must_match_preview_names_order_and_types(example, columns):
    source = example['model_response']['code_files'][0]['content']
    old = "{'trial': 'integer', 'method': 'string', 'absolute_error': 'number'}"
    example['model_response']['code_files'][0]['content'] = source.replace(old, columns)
    with pytest.raises(ValueError):
        ExperimentDraft.model_validate(example['model_response'])


@pytest.mark.parametrize('signature', [
    'def run_experiment(config):',
    'def run_experiment(config, input_data, extra):',
    'def run_experiment(config, input_data=None):',
    'def run_experiment(config, **kwargs):',
    'def run_experiment(config, *, input_data):',
    'async def run_experiment(config, input_data):',
])
def test_generated_algorithm_has_one_standard_entry(example, signature):
    file = example['model_response']['code_files'][0]
    file['content'] = file['content'].replace('def run_experiment(config, input_data):', signature)
    with pytest.raises(ValueError, match='run_experiment'):
        ExperimentDraft.model_validate(example['model_response'])


@pytest.mark.parametrize('value', [None, True, '0.4', float('inf'), float('nan'), [], {}])
def test_f4_rejects_invalid_metric_cells_before_analysis(example, value):
    inputs = {'topic': 'T', 'experiment': example['output']['experiment'],
              'result_meta': example['output']['result_meta']}
    inputs['experiment']['results']['rows'][0][-1] = value
    with pytest.raises(ValueError):
        contracts.validate_input('data_analysis', inputs)


@pytest.mark.parametrize('change', [
    lambda v: v['result_meta'].update(column_types=None),
    lambda v: v['result_meta']['column_types'].update(absolute_error='string'),
    lambda v: v['result_meta'].update(metrics=[]),
    lambda v: v['result_meta']['metrics'][0].update(column='missing'),
    lambda v: v['experiment']['results']['columns'].__setitem__(0, 'sample_id'),
    lambda v: v['experiment']['results']['rows'][0].append(5),
    lambda v: v['experiment']['results']['rows'][0].__setitem__(0, '0'),
    lambda v: v['experiment']['results']['rows'][0].__setitem__(1, 2),
])
def test_invalid_public_output_is_rejected_before_publishing(example, tmp_path, change):
    change(example['output'])
    with pytest.raises(ValueError):
        export_experiment(example['output'], str(tmp_path))
    assert not list(tmp_path.iterdir())


def test_metadata_object_order_does_not_change_table_contract(example):
    output = json.loads(json.dumps(example['output'], sort_keys=True))
    contracts.validate_output('experiment', output)
    value = contracts.validate_input('data_analysis', dict(
        topic='T', experiment=output['experiment'], result_meta=output['result_meta']))
    assert value['data']['columns'] == ['trial', 'method', 'absolute_error']


def test_legacy_metadata_without_column_types_remains_readable_by_f4(example):
    meta = copy.deepcopy(example['output']['result_meta'])
    meta.pop('column_types')
    result = contracts.validate_input('data_analysis', dict(
        topic='T', experiment=example['output']['experiment'], result_meta=meta))
    assert result['data']['rows'] == example['output']['experiment']['results']['rows']
    pool = experiment_pool(example)
    workflow.material(pool, 'code_files')['content'][0]['content'] = 'print("legacy download")'
    workflow.material(pool, 'result_meta')['content'].pop('column_types')
    workflow.validate_pool(pool)
    contracts.validate_input('data_analysis', workflow.business_input('data_analysis', pool))


def test_data_only_entry_validates_corresponding_result_metadata(example):
    table = copy.deepcopy(example['output']['experiment']['results'])
    meta = example['output']['result_meta']
    value = contracts.validate_input('data_analysis', dict(topic='T', data=table, result_meta=meta))
    assert value['experiment'] is None
    table['rows'][0][-1] = 'not a number'
    with pytest.raises(ValueError):
        contracts.validate_input('data_analysis', dict(topic='T', data=table, result_meta=meta))


@pytest.mark.parametrize('include_meta', [True, False])
def test_conflicting_uploaded_data_and_experiment_are_rejected(example, include_meta):
    with pytest.raises(ValueError):
        contracts.validate_input('data_analysis', dict(
            topic='T', data={'columns': ['x'], 'rows': [[1]]},
            experiment=example['output']['experiment'],
            result_meta=example['output']['result_meta'] if include_meta else None))


@pytest.mark.parametrize('inputs', [
    {'topic': 'T'}, {'topic': 'T', 'question': ''}, {'topic': 'T', 'question': 1},
    {'topic': 'T', 'question': 'Q', 'extra': 1},
    {'topic': 'T', 'question': 'Q', 'data': {'columns': ['x'], 'rows': []}},
    {'topic': 'T', 'question': 'Q', 'data': {'columns': ['x', 'x'], 'rows': [[1, 2]]}},
    {'topic': 'T', 'question': 'Q', 'data': {'columns': ['x'], 'rows': [[1, 2]]}},
    {'topic': 'T', 'question': 'Q', 'data': {'columns': ['x'], 'rows': [[float('inf')]]}},
])
def test_invalid_inputs_stop_before_model_call(inputs):
    # run validates parameters before constructing a model; no model replacement.
    with pytest.raises(ValueError):
        experiment.run(inputs)


def test_records_are_ordered_by_contract_and_never_modify_input():
    records = [{'value': 2.5, 'method': 'A'}, {'method': 'B', 'value': 3}]
    before = copy.deepcopy(records)
    table = records_to_table(records, {'method': 'string', 'value': 'number'})
    assert table == {'columns': ['method', 'value'], 'rows': [['A', 2.5], ['B', 3]]}
    assert records == before


@pytest.mark.parametrize('records', [
    None, [], {'value': 1}, [[1]], [{'other': 1}], [{'value': 1, 'extra': 2}],
    [{'value': None}], [{'value': True}], [{'value': '1'}], [{'value': float('nan')}],
])
def test_runner_rejects_malformed_records(records):
    with pytest.raises(ValueError):
        records_to_table(records, {'value': 'number'})


def test_download_runner_with_known_fixture_computes_matching_json_and_csv(example, tmp_path):
    """Run only the checked-in deterministic fixture, never model-generated source."""
    files = export_experiment(example['output'], str(tmp_path))
    with ZipFile(files['experiment_code']) as archive:
        archive.extractall(tmp_path)
    result = subprocess.run([sys.executable, str(tmp_path / 'run_experiment.py')],
                            cwd=tmp_path, capture_output=True, text=True, timeout=10)
    assert result.returncode == 0, result.stderr
    table = json.loads((tmp_path / 'results.json').read_text())
    validate_table(table, example['output']['result_meta']['column_types'])
    assert len(table['rows']) == 6
    assert {row[1] for row in table['rows']} == {'mean', 'median'}
    with (tmp_path / 'results.csv').open(newline='') as stream:
        rows = list(csv.reader(stream))
    assert rows[0] == table['columns']
    assert rows[1:] == [[str(cell) for cell in row] for row in table['rows']]
    again = subprocess.run([sys.executable, str(tmp_path / 'run_experiment.py')],
                           cwd=tmp_path, capture_output=True, text=True, timeout=10)
    assert again.returncode == 0
    assert json.loads((tmp_path / 'results.json').read_text()) == table


def test_runner_reports_wrong_field_without_writing_results(example, tmp_path):
    """A syntactically valid algorithm can still violate its declared return contract."""
    source = "RESULT_COLUMNS = {'trial': 'integer', 'method': 'string', 'absolute_error': 'number'}\n"
    source += "def run_experiment(config, input_data):\n    return [{'sample_id': 1}]\n"
    example['output']['code_files'][0]['content'] = source
    files = export_experiment(example['output'], str(tmp_path))
    with ZipFile(files['experiment_code']) as archive:
        archive.extractall(tmp_path)
    result = subprocess.run([sys.executable, str(tmp_path / 'run_experiment.py')],
                            cwd=tmp_path, capture_output=True, text=True, timeout=10)
    assert result.returncode != 0
    assert 'RESULT_COLUMNS' in result.stderr
    assert not (tmp_path / 'results.json').exists()
    assert not (tmp_path / 'results.csv').exists()


def test_runner_accepts_csv_input_and_checks_a_hand_calculable_result(example, tmp_path):
    # Three known observations have mean 4 and median 2. This is repository-owned test code.
    source = "RESULT_COLUMNS = {'trial': 'integer', 'method': 'string', 'absolute_error': 'number'}\n"
    source += ('def run_experiment(config, input_data):\n'
               '    import statistics\n'
               "    if input_data is None:\n        raise ValueError('input.csv required')\n"
               "    column = input_data['columns'].index('value')\n"
               "    values = [float(row[column]) for row in input_data['rows']]\n"
               "    return [{'trial': 0, 'method': 'mean', 'absolute_error': statistics.mean(values)},\n"
               "            {'trial': 0, 'method': 'median', 'absolute_error': statistics.median(values)}]\n")
    example['output']['code_files'][0]['content'] = source
    inputs = {'columns': ['value'], 'rows': [[1], [2], [9]]}
    files = export_experiment(example['output'], str(tmp_path), inputs)
    with ZipFile(files['experiment_code']) as archive:
        archive.extractall(tmp_path)
    run = subprocess.run([sys.executable, str(tmp_path / 'run_experiment.py')],
                         cwd=tmp_path, capture_output=True, text=True, timeout=10)
    assert run.returncode == 0, run.stderr
    assert json.loads((tmp_path / 'results.json').read_text()) == {
        'columns': ['trial', 'method', 'absolute_error'], 'rows': [[0, 'mean', 4.0], [0, 'median', 2.0]]}
    (tmp_path / 'results.json').unlink()
    (tmp_path / 'results.csv').unlink()
    (tmp_path / 'input.csv').write_text('value\ninvalid\n', encoding='utf-8')
    invalid = subprocess.run([sys.executable, str(tmp_path / 'run_experiment.py')],
                             cwd=tmp_path, capture_output=True, text=True, timeout=10)
    assert invalid.returncode != 0
    assert not (tmp_path / 'results.json').exists()


def test_input_csv_preserves_wide_data_and_long_text():
    table = {'columns': [f'feature{i}' for i in range(40)], 'rows': [['text' * 300] * 40]}
    assert validate_table(table, input_data=True) is table
    with pytest.raises(ValueError):
        validate_table(table)


def test_streams_research_code_and_results_before_final_response(example):
    from lazymind.document_tools.paper.experiment.stream import ExperimentStream
    events = []
    stream = ExperimentStream(events.append, 'T', [
        'experiment_document', 'experiment_code', 'experiment_results'])
    stream({'tag': 'think', 'delta': '正在比较实验的对照条件。'})
    raw = json.dumps(example['model_response'], ensure_ascii=False)
    first = raw.index(', "explanation"')
    stream({'tag': 'text', 'delta': raw[:first]})
    assert any(e['type'] == 'artifact_stream' and e['slot'] == 'experiment_document' for e in events)
    assert any(e.get('think') == '正在比较实验的对照条件。' for e in events)
    for start in range(first, len(raw), 90):
        stream.last_parse = 0
        stream({'tag': 'text', 'delta': raw[start:start + 90]})
    stream.preview()
    assert {'experiment_document', 'experiment_code', 'experiment_results'} == {
        e['slot'] for e in events if e['type'] == 'artifact_stream_start'}
    assert 'statistics' in stream.contents['experiment_code']
    stream.validated(example['output'])
    assert stream.contents['experiment_document'] == example['output']['experiment']['report']
    stream.close()
    assert not any(e['type'] == 'artifact' for e in events)  # previews never publish a committed result
    assert all(len(e['delta']) <= 4096 for e in events if e['type'] == 'artifact_stream')
    assert all(emitter._closed for emitter in stream.streams.values())
    progress = [e['progress'] for e in events if e['type'] == 'progress']
    assert progress == sorted(progress)


def test_stream_preserves_model_chunks_without_character_replay():
    from lazymind.document_tools.paper.experiment.stream import ExperimentStream
    events = []
    stream = ExperimentStream(events.append, 'T', ['experiment_document'])
    text = '逐字打印，中英文 mixed，emoji 😀。\n第二行。'
    stream.publish('experiment_document', text)
    deltas = [event['delta'] for event in events if event['type'] == 'artifact_stream']
    assert deltas == [text]
    assert ''.join(deltas) == text
    stream.close()


@pytest.mark.parametrize('fail', [False, True])
def test_generation_wait_updates_progress_and_stops_on_exit(monkeypatch, fail):
    from threading import Event
    from lazymind.document_tools.paper.experiment import stream as module
    received = Event()
    emitted = []
    monkeypatch.setattr(module, 'GENERATION_HEARTBEAT_SECONDS', 0.01)
    stream = module.ExperimentStream(emitted.append, 'T', ['experiment_document'])

    def observe(event):
        stream(event)
        received.set()

    try:
        with module.generation_progress(observe):
            assert received.wait(2), 'waiting model call never reported its progress'
            if fail:
                raise ValueError('model request failed')
    except ValueError:
        assert fail
    assert any(e['type'] == 'progress' and '尚未执行代码' in e['current_phase'] for e in emitted)
    assert max(e.get('progress', 0) for e in emitted) == 5
    assert not any(e['type'].startswith('artifact_stream') for e in emitted)
    stream.close()


def repaired_records(example):
    table = example['output']['experiment']['results']
    return {
        'rows': [dict(zip(table['columns'], row)) for row in table['rows']],
        'metrics': example['output']['result_meta']['metrics'],
        'assumptions': ['按固定方案重新构造的模拟预览；未运行代码，不是实证结果。'],
        'seed': example['model_response']['seed'], 'method': example['model_response']['method'],
    }


@pytest.mark.parametrize('defect', ['missing_column', 'wrong_column', 'unequal_lengths'])
def test_preview_repair_keeps_code_and_exports_exact_declared_columns(example, tmp_path, defect):
    from lazymind.document_tools.paper.experiment.preview_repair import (
        apply_repair, repair_base, repair_schema,
    )
    candidate = example['model_response']
    if defect == 'missing_column':
        candidate['results']['dimensions'].pop(0)  # The user's missing trial column.
    elif defect == 'wrong_column':
        candidate['results']['metrics'][0]['column'] = 'different_error'
    else:
        candidate['results']['metrics'][0]['values'].append(99)
    before = copy.deepcopy(candidate)
    with pytest.raises(ValueError):
        ExperimentDraft.model_validate(candidate)
    base, columns = repair_base(candidate)
    schema = repair_schema(columns)
    result = apply_repair(base, columns, schema, repaired_records(example))
    assert result.code_files == base.code_files
    assert result.plan == candidate['plan']
    assert result.explanation == candidate['explanation']
    assert result.assumptions != candidate['assumptions']
    assert candidate == before
    output = build_output(result, example['input'])
    assert output['experiment']['results'] == example['output']['experiment']['results']
    contracts.validate_input('data_analysis', {
        'topic': 'T', 'experiment': output['experiment'], 'result_meta': output['result_meta'],
    })
    files = export_experiment(output, str(tmp_path))
    with ZipFile(files['experiment_code']) as archive:
        assert json.loads(archive.read('result_schema.json'))['properties']['columns']['const'] == list(columns)
        assert json.loads(archive.read('preview_results.json'))['columns'] == list(columns)
    assert output['experiment']['executed'] is False
    assert output['result_meta']['data_origin'] == 'synthetic'


@pytest.mark.parametrize('defect', [
    'missing_cell', 'extra_cell', 'numeric_string', 'bool_as_number', 'nonfinite',
    'missing_metric', 'duplicate_metric', 'unknown_metric', 'blank_assumption',
])
def test_repaired_preview_stays_strict_and_never_pads_or_relabels(example, defect):
    from lazymind.document_tools.paper.experiment.preview_repair import (
        apply_repair, repair_base, repair_schema,
    )
    base, columns = repair_base(example['model_response'])
    repaired = copy.deepcopy(repaired_records(example))
    if defect == 'missing_cell':
        del repaired['rows'][0]['trial']
    elif defect == 'extra_cell':
        repaired['rows'][0]['extra'] = 1
    elif defect == 'numeric_string':
        repaired['rows'][0]['absolute_error'] = '0.5'
    elif defect == 'bool_as_number':
        repaired['rows'][0]['absolute_error'] = True
    elif defect == 'nonfinite':
        repaired['rows'][0]['absolute_error'] = float('nan')
    elif defect == 'missing_metric':
        repaired['metrics'] = []
    elif defect == 'duplicate_metric':
        repaired['metrics'] *= 2
    elif defect == 'unknown_metric':
        repaired['metrics'][0]['column'] = 'unknown'
    else:
        repaired['assumptions'] = ['  ']
    with pytest.raises(ValueError):
        apply_repair(base, columns, repair_schema(columns), repaired)


@pytest.mark.parametrize('field,value', [('plan', ''), ('explanation', ''), ('results', None)])
def test_non_preview_failures_do_not_enter_preview_repair(example, field, value):
    from lazymind.document_tools.paper.experiment.preview_repair import repair_base
    candidate = copy.deepcopy(example['model_response'])
    candidate[field] = value
    with pytest.raises(ValueError):
        repair_base(candidate)
    candidate = copy.deepcopy(example['model_response'])
    candidate['code_files'][0]['content'] = 'not valid python : :'
    with pytest.raises(SyntaxError):
        repair_base(candidate)


def test_missing_preview_metadata_is_repaired_without_replacing_the_design(example):
    from lazymind.document_tools.paper.experiment.preview_repair import (
        apply_repair, repair_base, repair_schema,
    )
    response = repaired_records(example)
    candidate = copy.deepcopy(example['model_response'])
    for field in ('assumptions', 'seed', 'method'):
        del candidate[field]
    base, columns = repair_base(candidate)
    repaired = apply_repair(base, columns, repair_schema(columns, candidate), response)
    assert repaired.seed == 42
    assert repaired.method == 'simulation'
    assert repaired.code_files == base.code_files
    assert repaired.plan == candidate['plan']


@pytest.mark.parametrize('field,value', [('seed', 123), ('method', 'empirical')])
def test_preview_repair_cannot_change_existing_seed_or_method(example, field, value):
    from lazymind.document_tools.paper.experiment.preview_repair import (
        apply_repair, repair_base, repair_schema,
    )
    base, columns = repair_base(example['model_response'])
    response = repaired_records(example)
    response[field] = value
    with pytest.raises(ValueError):
        apply_repair(base, columns, repair_schema(columns, example['model_response']), response)


def test_fixed_seed_literal_does_not_coerce_true_to_one(example):
    from lazymind.document_tools.paper.experiment.preview_repair import repair_schema
    response = repaired_records(example)
    response['seed'] = True
    with pytest.raises(ValueError):
        repair_schema({'trial': 'integer', 'method': 'string', 'absolute_error': 'number'},
                      {'seed': 1, 'method': 'simulation'}).model_validate(response)


def test_validated_stream_replaces_invalid_preview_with_repaired_result(example):
    from lazymind.document_tools.paper.experiment.stream import ExperimentStream
    events = []
    stream = ExperimentStream(events.append, 'T', ['experiment_results'])
    stream.publish('experiment_results', 'incomplete preview without trial')
    stream.validated(example['output'])
    assert '"trial"' in stream.contents['experiment_results']
    assert 'incomplete preview' not in stream.contents['experiment_results']
    assert all(len(e['delta']) <= 4096 for e in events if e['type'] == 'artifact_stream')
    stream.close()


def test_failed_stream_aborts_previews_and_never_publishes_files(example):
    from lazymind.document_tools.paper.experiment.stream import ExperimentStream
    events = []
    stream = ExperimentStream(events.append, 'T', ['experiment_document'])
    stream({'tag': 'text', 'delta': '{"plan":"尚未验证的方案"'})
    stream.close(ValueError('指标类型错误'))
    assert events[-1]['type'] == 'artifact_stream_abort'
    assert '指标类型错误' in events[-1]['message']
    assert not any(e['type'] == 'artifact' for e in events)


def test_direct_entry_accepts_question_without_pool_and_missing_question_fails(tmp_path):
    path = tmp_path / 'question.txt'
    path.write_text('离群值对均值和中位数有什么影响？')
    pool = workflow.initialize_pool({'experiment_question': str(path)}, '请设计实验')
    value = workflow.business_input('experiment', pool)
    assert contracts.validate_input('experiment', value)['question'] == path.read_text()
    with pytest.raises(contracts.MissingMaterials, match='实验问题或用户数据'):
        contracts.validate_input('experiment', {'topic': 'T'})


def test_experiment_question_is_dynamic_and_not_required_at_startup():
    from lazymind.chat.workflow.workflow_manager import enforce_startup_clarification_policy
    package = yaml.safe_load((PACKAGE / 'workflow.yaml').read_text())
    assert not package['runtime'].get('clarification_fields')
    questions = [{'id': 'q1', 'text': '当前没有耗时实验数据，接下来如何准备？',
                  'type': 'single', 'choices': ['我会补充真实数据', '使用模拟数据']}]
    assert enforce_startup_clarification_policy(questions, package['runtime']) == questions
    state = yaml.safe_load((PACKAGE / 'scenario/state.yml').read_text())
    assert 'prepare' not in state['steps']
    assert all(not item.get('required', True) for item in state['steps']['experiment']['inputs'])
    assert {'experiment_execution', 'experiment_question'} <= {
        item['material'] for item in state['steps']['experiment']['inputs']}
    assert {'experiment_code', 'experiment_results'} <= {
        item['material'] for item in state['steps']['experiment']['outputs']}


def test_f3_answer_keeps_original_goal():
    pool = workflow.pool_from_business({'topic': '比较均值和中位数', 'request': '比较均值和中位数，设计实验'})
    merged = workflow._step_request('experiment', pool, {
        'step_id': 'experiment', 'current_user_input': '另做模拟实验',
        'remote_inputs': {'literature_review_pool': 'pool.json'},
    })
    assert '比较均值和中位数，设计实验' in merged
    assert '另做模拟实验' in merged


def test_dynamic_model_experiment_to_f4_contract_and_downloads(example, tmp_path, offline_paper_model):
    """Exercise the dynamic SDK boundary and real contracts without a live provider."""
    response = copy.deepcopy(example['model_response'])
    files = {item['path']: item['content'] for item in response.pop('code_files')}
    response.update(code=files['experiment.py'], config=json.loads(files['config.json']),
                    dependencies=[line for line in files['requirements.txt'].splitlines() if line.strip()])
    offline_paper_model({'action': 'simulation', 'table_ids': [], 'reason': '本轮只生成模拟实验。'},
                        response)
    pool = workflow.pool_from_business({**example['input'], 'request': '明确进行模拟研究，比较均值与中位数。'})
    result = workflow.execute_step('experiment', pool)
    output = {key: workflow.material(result['pool'], key)['content'] for key in contracts.OUTPUTS['experiment']}
    output['is_demo'] = result['mock']
    contracts.validate_output('experiment', output)
    assert output['experiment']['results'] is not None
    assert output['result_meta']['execution_status'] == 'not_run'
    contracts.validate_input('data_analysis', workflow.business_input('data_analysis', result['pool']))
    files = export_experiment(output, str(tmp_path))
    assert all(Path(path).is_file() for path in files.values())
    print('REAL_F3_RESULT', json.dumps({'rows': len(output['experiment']['results']['rows']),
          'origin': output['result_meta']['data_origin'], 'artifacts': files}, ensure_ascii=False))

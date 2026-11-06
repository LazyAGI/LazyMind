"""Real F3 preparation and artifact contracts; only the model/attachment IO boundaries are substituted."""
import copy
import json
import subprocess
import sys
from pathlib import Path
from types import SimpleNamespace
from unittest.mock import Mock
from zipfile import ZipFile

import pytest

from lazyllm.tools.writer.tools.base import WriterToolBase
from lazymind.document_tools.paper import source_materials as sources, workflow, contracts
from lazymind.document_tools.paper.experiment import preparation, algorithm
from lazymind.document_tools.paper.experiment.artifacts import export_experiment
from lazymind.document_tools.paper.experiment.handoff import to_analysis
from lazymind.document_tools.paper.experiment.stream import ExperimentStream

MATERIAL = '''# 研究问题

考察修订任务与资源消耗；以下为已有实测结果，不是新实验计划。

## 删除任务

| 指标 | 成功/总数 | 比例 |
| --- | ---: | ---: |
| 删除成功 | 28/30 | 93.3% |
| 范围识别 | 29/30 | 96.7% |

来源 S02；只有汇总计数，没有逐项观测。

## 运行资源

| 任务 | 耗时（秒） | 输入token |
| --- | ---: | ---: |
| P01 | 154.697 | 158,406 |
| P02 | 205.439 | 176,800 |

来源 S01；两项任务，不能当成重复采样。

## 无关背景

这段背景不进入材料池。
'''


@pytest.fixture
def model_boundary(monkeypatch):
    calls = []
    action = {'value': 'use_existing'}

    def response(prompt):
        calls.append(prompt)
        payload = json.loads(prompt[prompt.index('\n{') + 1:])
        if 'blocks' in payload:
            ids = [b['id'] for b in payload['blocks'] if '无关' not in b['text'] and '这段背景' not in b['text']]
            return json.dumps({'blocks': ids})
        return json.dumps({'action': action['value'], 'table_ids': list(payload['tables']),
                           'reason': '保留已有观测及来源；汇总数据不补造逐项样本。'}, ensure_ascii=False)

    monkeypatch.setattr(sources, 'AutoModel', lambda **_: Mock())
    monkeypatch.setattr(preparation, 'AutoModel', lambda **_: Mock())
    monkeypatch.setattr(WriterToolBase, '_build_structured_llm', lambda *_a, **_kw: response)
    from lazymind.document_tools.paper import research_topic
    monkeypatch.setattr(research_topic, '_prompt_token_limit', lambda: 200000)
    return calls, action


def make_pool():
    return workflow.pool_from_business({'topic': '已有实验结果', 'request': '分析材料中的结果并绘图'})


@pytest.mark.parametrize('count', [35, 38, 100])
def test_attachment_evidence_has_no_fixed_block_count_limit(model_boundary, count):
    passages = [f'实验结果段落 {index}：保留原始观测与来源。' for index in range(count)]
    paper = {'id': 'many-passages', 'title': '已有研究结果', 'text': '\n\n'.join(passages)}
    pool = make_pool()
    read = Mock(return_value=[paper])
    history = {'1': ['/uploads/many-passages.md']}
    assert sources.sync_attachments(pool, history, '整理已有实验结果', read_papers=read)
    stored = workflow.material(pool, 'papers')['content'][0]['text']
    assert stored.count('[原文第 ') == count
    assert all(passage in stored for passage in passages)
    assert not sources.sync_attachments(pool, history, '继续', read_papers=read)
    read.assert_called_once_with(history)


def test_extracts_evidence_without_whole_document_or_model_transcription(model_boundary):
    paper = sources.extract_paper({'id': 'original', 'title': '材料', 'text': MATERIAL}, '分析已有实验')
    assert '这段背景不进入材料池' not in paper['text']
    assert '| 删除成功' not in paper['text']
    assert '来源 S02' in paper['text']
    assert paper['content_type'] == 'excerpt'
    tables = paper['source_tables']
    assert len(tables) == 2
    assert tables[0]['table']['rows'][0] == ['删除成功', '28/30', 93.3]
    assert tables[0]['table']['units'] == [None, None, '%']
    assert tables[1]['table']['rows'][1] == ['P02', 205.439, 176800]
    assert tables[1]['table']['units'][1] == '秒'
    assert '原文第' in tables[0]['description']


@pytest.mark.parametrize('selected,known,expected', [
    ('paper', None, 'paper'), ('background', None, 'background'), ('paper', 'background', 'background')])
def test_evidence_extraction_preserves_document_outline_and_background_role(
        model_boundary, monkeypatch, selected, known, expected):
    monkeypatch.setattr(WriterToolBase, '_build_structured_llm', lambda *_a, **_kw: lambda _prompt:
                        json.dumps({'blocks': [0], 'review_role': selected}))
    source = dict(id='source', title='Study material', text='# Study background\n\nMethods and measurements.')
    if known:
        source['review_role'] = known
    result = sources.extract_paper(source, 'Use the supplied study material')
    assert result['review_role'] == expected
    assert result['document_outline'] == ['Study background']


def test_incremental_uploads_are_read_once_and_tables_stored_once(model_boundary):
    pool = make_pool()
    reads = []

    def read(history):
        reads.append(copy.deepcopy(history))
        return [{'id': 'original' if path == '/uploads/one.md' else 'second', 'title': path,
                 'text': MATERIAL, 'attachment_key': workflow.text_digest(path)}
                for paths in history.values() for path in paths]

    first = {'1': ['/uploads/one.md']}
    assert sources.sync_attachments(pool, first, '分析', read_papers=read)
    assert len(reads) == 1
    assert not sources.sync_attachments(pool, first, '进入实验', read_papers=read)
    assert len(reads) == 1
    papers = workflow.material(pool, 'papers')['content']
    assert all('source_tables' not in paper for paper in papers)
    assert len(workflow.material(pool, 'source_data')['content']['tables']) == 2
    assert sources.sync_attachments(pool, {**first, '2': ['/uploads/two.md']}, '补充', read_papers=read)
    assert reads[-1] == {'2': ['/uploads/two.md']}
    assert len(workflow.material(pool, 'source_data')['content']['tables']) == 4
    persisted = json.loads(json.dumps(pool))
    assert not sources.sync_attachments(persisted, {**first, '2': ['/uploads/two.md']}, '继续', read_papers=read)
    assert len(reads) == 2


def test_existing_results_flow_through_real_f3_to_f4_and_download(model_boundary, tmp_path):
    pool = make_pool()
    # Legacy pools already contain the original text. F3 must consume it in memory.
    workflow.add_material(pool, 'papers', [{'id': 'original', 'title': '材料', 'text': MATERIAL}], [], ['original'])
    before = copy.deepcopy(pool)
    result = workflow.execute_step('experiment', pool, '进入实验')
    assert pool == before
    assert result['pool']['history'][-1]['inputs'] == [pool['current']['papers']]
    output = {kind: workflow.material(result['pool'], kind)['content'] for kind in contracts.OUTPUTS['experiment']}
    output['is_demo'] = result['mock']
    assert output['experiment']['executed'] is False
    assert output['code_files'] == []
    assert output['result_meta']['data_origin'] == 'user_supplied'
    assert output['is_demo'] is False
    f4 = workflow.business_input('data_analysis', result['pool'], '分析并绘图')
    assert f4['data'] == output['experiment']['results']
    assert '平台未复现实验' in f4['data_context']['description']
    paths = export_experiment(output, str(tmp_path))
    with ZipFile(paths['experiment_code']) as archive:
        assert 'experiment.py' not in archive.namelist()
        assert 'preview_results.json' not in archive.namelist()
        assert json.loads(archive.read('results.json')) == f4['data']
        assert len([p for p in archive.namelist() if p.endswith('.csv')]) == 2
    stream = ExperimentStream(lambda _event: None, '研究', ['experiment_document', 'experiment_results'])
    stream.validated(output)
    stream.close()


def test_f3_never_substitutes_simulation_for_supplied_data(model_boundary):
    _, action = model_boundary
    action['value'] = 'simulation'
    with pytest.raises(contracts.MissingMaterials, match='不能自动替换'):
        algorithm.run({'topic': 'T', 'data': {'columns': ['x'], 'rows': [[1], [2]]}})


def test_compute_selects_complete_multi_table_inputs(model_boundary):
    _, action = model_boundary
    action['value'] = 'compute'
    inputs = contracts.validate_input('experiment', {'topic': 'T', 'papers': [
        {'id': 'source', 'title': '实验数据', 'text': MATERIAL}], 'request': '基于用户数据进行新的实验计算'})
    existing, selected = preparation.prepare(inputs)
    assert existing is None
    assert len(selected['data']['tables']) == 2
    assert selected['selected_table_ids'] == [t['id'] for t in selected['data']['tables']]
    assert selected['data']['tables'][0]['table']['rows'][0][-1] == 93.3


def test_user_data_contract_rejects_false_execution_claims():
    table = sources.typed_table(['value'], [[1], [None]])
    output = preparation.existing_output({'topic': 'T'}, [{'id': 'provided', 'description': '原始观测', 'table': table}],
                                         '整理已有数据')
    output['experiment']['executed'] = True
    with pytest.raises(ValueError):
        contracts.validate_output('experiment', output)


def test_table_extraction_rejects_malformed_rows_and_keeps_identifiers():
    with pytest.raises(contracts.MissingMaterials):
        sources.markdown_tables('| A | B |\n| --- | --- |\n| 1 |', 'source', '坏表')
    table = sources.typed_table(['编号', '值'], [['001', '1,000'], ['002', '2,000']])
    assert table['rows'] == [['001', 1000], ['002', 2000]]
    assert table['column_types'] == ['string', 'integer']


def test_fenced_example_is_not_observed_data():
    tables, _ = sources.markdown_tables('```\n| A |\n| --- |\n| 99 |\n```', 'source', '示例')
    assert tables == []


def test_f3_result_is_readable_by_f4_without_original_attachment():
    table = sources.typed_table(['耗时（秒）'], [['205.439']])
    output = preparation.existing_output({'topic': 'T'}, [{'id': 'timing', 'description': 'S01 表2', 'table': table}],
                                         '只整理已有记录')
    value = to_analysis({'topic': 'T', 'experiment': output['experiment'], 'result_meta': output['result_meta']})
    assert value['data']['tables'][0]['table']['rows'] == [[205.439]]
    assert 'S01 表2' in value['data_context']['description']


@pytest.fixture
def runtime(monkeypatch, tmp_path):
    from lazymind.chat.engine.subagent import context, tools
    batches = []
    ctx = SimpleNamespace(task_id='f3-pool-test', params={}, workspace_path=str(tmp_path),
                          emit=lambda _: None, output_slots=['experiment_pool', 'experiment_document'])
    monkeypatch.setattr(context, 'require_context', lambda: ctx)
    monkeypatch.setattr(tools, 'save_artifacts', lambda items: batches.append(copy.deepcopy(items)) or {})
    return ctx, batches


def test_runtime_late_upload_enters_pool_and_is_not_read_on_f3_rerun(model_boundary, runtime, tmp_path, monkeypatch):
    ctx, batches = runtime
    pool = make_pool()
    read = Mock(return_value=[{'id': 'new', 'title': '补传材料', 'text': MATERIAL}])
    monkeypatch.setattr(workflow, '_conversation_papers', read)
    bound = tmp_path / 'prior.json'
    bound.write_text(json.dumps(pool))
    ctx.params = {'step_id': 'experiment', 'current_user_input': '整理补传的实验结果',
                  'remote_inputs': {'research_topic_pool': str(bound)},
                  'history_files_per_turn': {'2': ['/uploads/new.md']}}
    workflow.run_paper_step()
    published = next(item['value'] for item in batches[-1] if item['key'] == 'experiment_pool')
    saved = workflow._read_bound(published)
    assert saved['imported_attachments'] == [workflow.text_digest('/uploads/new.md')]
    assert len(workflow.material(saved, 'source_data')['content']['tables']) == 2
    assert saved['current']['source_data'] in saved['history'][-1]['inputs']
    ctx.params['remote_inputs'] = {'experiment_pool': published}
    ctx.params['current_user_input'] = '继续整理'
    workflow.run_paper_step()
    assert read.call_count == 1
    repeated = workflow._read_bound(next(item['value'] for item in batches[-1] if item['key'] == 'experiment_pool'))
    assert repeated['current']['source_data'] == saved['current']['source_data']


def test_f4_new_upload_is_routed_back_before_reading_original(model_boundary, runtime, tmp_path, monkeypatch):
    ctx, batches = runtime
    pool = workflow.pool_from_business({'topic': 'T', 'data': {'columns': ['x'], 'rows': [[3]]}})
    prepared = workflow.execute_step('experiment', pool)['pool']
    bound = tmp_path / 'prepared.json'
    bound.write_text(json.dumps(prepared))
    read = Mock(side_effect=AssertionError('F4 must not reread attachments'))
    monkeypatch.setattr(workflow, '_conversation_papers', read)
    ctx.params = {'step_id': 'data_analysis', 'remote_inputs': {'experiment_pool': str(bound)},
                  'history_files_per_turn': {'3': ['/uploads/late.md']}}
    with pytest.raises(contracts.MissingMaterials, match='先回到 F3'):
        workflow.run_paper_step()
    read.assert_not_called()
    assert not batches


def test_existing_demo_tables_keep_their_source_label(model_boundary, tmp_path):
    output = algorithm.run({'topic': 'T', 'data': {'columns': ['x'], 'rows': [[1]]}, 'input_is_demo': True})
    assert output['result_meta']['data_origin'] == 'synthetic'
    assert output['is_demo'] is True
    assert output['code_files'] == []
    paths = export_experiment(output, str(tmp_path))
    with ZipFile(paths['experiment_code']) as archive:
        assert json.loads(archive.read('结果来源.json'))['result_meta']['data_origin'] == 'synthetic'


def test_compute_without_execution_keeps_failure_without_generated_preview(model_boundary, monkeypatch):
    _, action = model_boundary
    action['value'] = 'compute'
    original_builder = WriterToolBase._build_structured_llm
    source = Path(__file__).resolve().parents[3] / 'fixtures/paper-writing/experiment.json'
    draft = json.loads(source.read_text())['model_response']
    files = {item['path']: item['content'] for item in draft.pop('code_files')}
    draft.update(code=files['experiment.py'], config=json.loads(files['config.json']), dependencies=[],
                 method='empirical')

    def build(writer, *args, **kwargs):
        select = original_builder(writer, *args, **kwargs)
        return lambda prompt: json.dumps(draft) if 'Input JSON:' in prompt else select(prompt)

    monkeypatch.setattr(WriterToolBase, '_build_structured_llm', build)
    monkeypatch.setattr(algorithm, 'AutoModel', lambda **_: Mock())
    output = algorithm.run({'topic': 'T', 'data': {'columns': ['x'], 'rows': [[3], [4]]},
                            'request': '使用现有数据计算新实验'}, allow_execution=False)
    assert output['experiment']['results'] is None
    assert output['result_meta']['data_origin'] == 'none'
    assert '使用模拟示例' not in output['result_meta']['reason']
    assert output['result_meta']['input_table_ids'] == ['supplied']


def test_table_parser_preserves_booleans_scientific_notation_and_missingness():
    table = sources.typed_table(['id', 'measured', 'ok'], [['001', '1e-3', True], ['002', None, False]])
    assert table['rows'] == [['001', .001, True], ['002', None, False]]
    assert table['column_types'] == ['string', 'number', 'boolean']


@pytest.mark.skipif(sys.platform != 'win32' and not Path('/.dockerenv').exists()
                    and not Path('/run/.containerenv').exists(), reason='Requires the experiment sandbox')
def test_selected_user_data_computation_and_download_use_identical_values(model_boundary, monkeypatch, tmp_path):
    _, action = model_boundary
    action['value'] = 'compute'
    original_builder = WriterToolBase._build_structured_llm
    source = Path(__file__).resolve().parents[3] / 'fixtures/paper-writing/experiment.json'
    draft = json.loads(source.read_text())['model_response']
    draft.pop('code_files')
    code = ("RESULT_COLUMNS = {'total': 'number'}\n"
            'def run_experiment(config, input_data):\n'
            "    return [{'total': sum(row[0] for row in input_data['rows'] if row[0] is not None)}]\n")
    draft.update(code=code, config={}, dependencies=[], method='empirical', results=None,
                 no_results_reason='等待实际计算', metrics=[{'column': 'total', 'description': '非缺失值合计', 'unit': None}])

    def build(writer, *args, **kwargs):
        select = original_builder(writer, *args, **kwargs)
        return lambda prompt: json.dumps(draft) if 'Input JSON:' in prompt else select(prompt)

    monkeypatch.setattr(WriterToolBase, '_build_structured_llm', build)
    monkeypatch.setattr(algorithm, 'AutoModel', lambda **_: Mock())
    data = {'columns': ['value'], 'rows': [[1.25], [None], [2.75]]}
    output = algorithm.run({'topic': 'T', 'data': data, 'request': '基于现有数据执行新计算'}, allow_execution=True)
    assert output['experiment']['executed'] is True
    assert output['experiment']['results']['rows'] == [[4.0]]
    paths = export_experiment(output, str(tmp_path), data)
    with ZipFile(paths['experiment_code']) as archive:
        assert json.loads(archive.read('input.json')) == data
        archive.extractall(tmp_path)
    ran = subprocess.run([sys.executable, str(tmp_path / 'run_experiment.py')],
                         cwd=tmp_path, capture_output=True, text=True, timeout=15)
    assert ran.returncode == 0, ran.stderr
    assert json.loads((tmp_path / 'results.json').read_text()) == output['experiment']['results']


def test_existing_report_renders_tables_with_readable_headings_and_preserves_handoff():
    from lazyllm.tools.writer.utils.serialization import parse_document_markdown

    tables, _ = sources.markdown_tables(MATERIAL, 'upload-' + 'a' * 64, 'paper-materials-v3')
    before = copy.deepcopy(tables)
    output = preparation.existing_output({'topic': 'Original workflow request:\n原始指令'}, tables,
                                         '已有删除任务与资源消耗记录；只进行描述性分析。')
    report = output['experiment']['report']
    document = parse_document_markdown(report, 'prepared-experiment')
    assert len([block for block in document.iter_blocks() if block.type == 'table']) == 2
    assert '## 删除任务' in report and '## 运行资源' in report
    assert 'Original workflow request' not in report
    assert 'upload-' not in report and all(entry['id'] not in report for entry in tables)
    assert '原文未声明单位' not in report and '展示前 20 行' not in report
    assert '| 指标 | 成功/总数 | 比例（%） |' in report
    assert '| 耗时（秒）（秒） |' not in report
    assert output['experiment']['results']['tables'] == before
    handed = to_analysis({'topic': 'T', 'experiment': output['experiment'], 'result_meta': output['result_meta']})
    assert handed['data']['tables'] == before
    assert tables == before


@pytest.mark.parametrize('action', ['use_existing', 'compute'])
def test_f3_selects_complete_data_without_forwarding_literature_documents(model_boundary, monkeypatch, action):
    calls, choice = model_boundary
    choice['value'] = action
    from lazymind.document_tools.paper import research_topic
    monkeypatch.setattr(research_topic, '_prompt_token_limit', lambda: 103219)
    inputs = contracts.validate_input('experiment', {
        'topic': '已有实验结果', 'request': '分析现有结果',
        'papers': [
            {'id': 'user-results', 'title': '实验记录', 'text': MATERIAL},
            {'id': 'reference', 'title': 'Related work', 'abstract': 'A concise literature abstract.',
             'text': 'EXTERNAL_DOCUMENT_BODY' * 30000,
             'reading': {'content_type': 'document', 'content': 'NESTED_DOCUMENT_BODY' * 30000}},
        ],
    })
    before = copy.deepcopy(inputs)
    existing, updated = preparation.prepare(inputs)
    payload = json.loads(calls[-1][calls[-1].index('\n{') + 1:])
    assert payload['facts']['papers'] == [
        {'id': 'user-results', 'title': '实验记录'},
        {'id': 'reference', 'title': 'Related work', 'abstract': 'A concise literature abstract.'},
    ]
    assert 'EXTERNAL_DOCUMENT_BODY' not in calls[-1]
    assert 'NESTED_DOCUMENT_BODY' not in calls[-1]
    candidates = preparation.candidate_tables(inputs)
    assert len(candidates) == 2
    selected = existing['experiment']['results'] if existing else updated['data']
    assert selected['tables'] == list(candidates.values())
    assert payload['tables'] == preparation.prompt_tables({key: entry['table'] for key, entry in candidates.items()})
    assert inputs == before

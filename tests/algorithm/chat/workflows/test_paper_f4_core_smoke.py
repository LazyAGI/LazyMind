"""Compact offline F4 checks using the real generated-code executors."""
import copy
import json
import shutil
from contextvars import ContextVar
from pathlib import Path
from threading import Barrier, Event
from types import SimpleNamespace
from unittest.mock import Mock

import lazyllm
import pytest

from lazymind.chat.engine.subagent.context import SubAgentContext
from lazymind.document_tools.paper import workflow
from lazymind.document_tools.paper.analysis import model
from lazymind.document_tools.paper.analysis.schema import ContractError
from lazymind.document_tools.paper.plotting import analysis as plots


ANALYSIS_CODE = """def analyze(tables: InputTables, plan, rng) -> AnalysisResults:
    table = tables['data']
    rows, metric = table['rows'], table['columns'][1]
    results = []
    for group in dict.fromkeys(row[0] for row in rows):
        selected = [row for row in rows if row[0] == group]
        values = [row[1] for row in selected if row[1] is not None]
        results.append(dict(question_id=plan['questions'][0]['id'], label=group + ' mean', metric=metric,
            method='Arithmetic mean', value=sum(values) / len(values), unit=table['units'][1],
            columns=[dict(table_id='data', column=metric)], scope=plan['facts']['topic'],
            coordinates={table['columns'][0]: group}, sample_size=dict(
                source_rows=[dict(table_id='data', count=len(selected))],
                n=len(values), unit='observation', excluded_n=len(selected)-len(values))))
    return dict(results=results)
"""
# Annotation-only names must not require an additional runtime import.
PLOT_CODE = """import matplotlib.pyplot as plt

def draw(data, statistics, spec) -> matplotlib.figure.Figure:
    rows = [row for row in data['analysis_results'] if row['id'] in spec['finding_ids']]
    fig, ax = plt.subplots()
    ax.bar([row['label'] for row in rows], [row['value'] for row in rows])
    ax.set_ylabel(rows[0]['metric'] + ' (' + rows[0]['unit'] + ')')
    return fig
"""


@pytest.fixture(autouse=True)
def runtime_state():
    previous = {key: lazyllm.globals.get(key) for key in ('agentic_config', 'subagent_ctx')}
    yield
    for key, value in previous.items():
        if value is None:
            lazyllm.globals.pop(key, None)
        else:
            lazyllm.globals[key] = value


@pytest.fixture
def replay(monkeypatch, tmp_path):
    request = '只执行 F4，分析上传的 input.json，输出分析、结论和图表。'
    inputs = dict(topic='模拟：方法得分比较', request=request,
                  data=dict(columns=['method', 'score'], column_types=['string', 'number'],
                            units=[None, 'point'], rows=[['baseline', 1.], ['baseline', 3.],
                                                       ['candidate', 4.], ['candidate', 6.]]))
    plan = dict(questions=['各组平均得分如何？'], outline='按方法计算得分均值，仅作模拟描述。')
    arrangement = dict(figures=[dict(title='Mean scores', purpose='Compare group means.',
                                    style='bar_grouped_hatch', result_indices=[0, 1])],
                       terminology={'baseline': 'Baseline', 'candidate': 'Candidate'},
                       series_order=['baseline', 'candidate'])
    conclusion = '候选组均值为 5 分，基线为 2 分；仅反映当前模拟样本。'
    structured = Mock(side_effect=[plan, arrangement])
    analysis = Mock(return_value={'code': ANALYSIS_CODE})
    plot = Mock(return_value={'code': PLOT_CODE, 'caption': '模拟样本的组均值。'})
    repair = Mock()
    narrative = Mock(return_value=conclusion)
    monkeypatch.setattr(model, 'call_model', structured)
    monkeypatch.setattr(model, 'call_code_model', analysis)
    monkeypatch.setattr(model, 'call_markdown_model', narrative)
    monkeypatch.setattr(plots, 'call_code_model', plot)
    monkeypatch.setattr(plots, 'call_model', repair)
    monkeypatch.setattr(lazyllm, 'AutoModel', Mock(side_effect=AssertionError('Unexpected live model call')))
    monkeypatch.setenv('LAZYMIND_PAPER_GENERATED_ANALYSIS_ATTEMPTS', '2')
    monkeypatch.setenv('LAZYMIND_PAPER_PLOT_ATTEMPTS', '2')
    monkeypatch.delenv('LAZYMIND_PAPER_PLOT_SKILLS', raising=False)
    source = tmp_path / 'input.json'
    source.write_text(json.dumps(inputs), encoding='utf-8')
    lazyllm.globals['agentic_config'] = dict(user_id='f4-test', conversation_id='f4-test',
                                          query=request, files=[str(source)],
                                          history_files_per_turn={'1': [str(source)]})
    from lazymind.document_tools.paper.analysis.attachments import workflow_input_bindings
    events = []
    slots = ['data_analysis_pool', 'data_analysis_document', 'analysis_chart']
    context = SubAgentContext(
        task_id='f4-test', conversation_id='f4-test', agent_type='workflow_step', objective=request,
        params=dict(step_id='data_analysis', user_input=request,
                    remote_inputs=workflow_input_bindings({}, request),
                    output_slot_types=dict.fromkeys(slots, 'file')),
        workspace_path=str(tmp_path), input_slots=['paper_input'], output_slots=slots,
        db=None, emit=events.append)
    lazyllm.globals['subagent_ctx'] = context
    return SimpleNamespace(source=source, inputs=inputs, plan=plan, arrangement=arrangement, conclusion=conclusion,
                           structured=structured, analysis=analysis, plot=plot, repair=repair,
                           narrative=narrative, events=events, context=context)


def payload(prompt):
    fields = {}
    for message in prompt['input']:
        if message['role'] == 'user':
            fields.update(json.loads(message['content'].split('INPUT_JSON:\n', 1)[1]))
    return fields


def published(replay):
    artifacts = [event for event in replay.events if event['type'] == 'artifact']
    paths = {Path(event['value']['path']).suffix: Path(event['value']['path']) for event in artifacts}
    pool = workflow._read_bound(str(paths['.json']))
    return (workflow.material(pool, 'analysis')['content'], workflow.material(pool, 'charts')['content'],
            pool['history'][-1]['analysis_execution'][0], paths)


@pytest.mark.parametrize('fault', ['none', 'plan', 'figure_plan', 'analysis', 'plot',
                                 'fallback', 'missing_plot', 'no_figures'])
def test_f4_workflow_publishes_executed_files(replay, fault, monkeypatch):
    if fault == 'plan':
        replay.structured.side_effect = [{}, replay.plan, replay.arrangement]
    elif fault == 'figure_plan':
        invalid = copy.deepcopy(replay.arrangement)
        invalid['figures'][0]['result_indices'] = [99]
        replay.structured.side_effect = [replay.plan, invalid, replay.arrangement]
    elif fault in ('analysis', 'plot'):
        source = ANALYSIS_CODE if fault == 'analysis' else PLOT_CODE
        statement = '    return dict(results=results)' if fault == 'analysis' else '    return fig'
        line = source.splitlines().index(statement) + 1
        replacement = dict(replacements=[dict(start_line=line, end_line=line, code=statement)])
        code = source.replace(statement, '    return missing_name')
        if fault == 'analysis':
            replay.analysis.return_value = {'code': code}
            replay.structured.side_effect = [replay.plan, replacement, replay.arrangement]
        else:
            replay.plot.return_value = {'code': code}
            replay.repair.return_value = replacement
    elif fault == 'fallback':
        replay.plot.return_value = {'code': PLOT_CODE.replace('return fig', 'return missing_name')}
        line = PLOT_CODE.splitlines().index('    return fig') + 1
        replay.repair.return_value = dict(replacements=[dict(start_line=line, end_line=line,
                                                           code='    return missing_name')])
    elif fault == 'missing_plot':
        replay.arrangement['figures'].append(dict(replay.arrangement['figures'][0], title='Missing comparison'))
        original = plots.draw_one
        def draw(*args):
            if args[2]['id'] == 'fig2':
                raise plots.FigureRenderError('injected unavailable figure', [])
            return original(*args)
        monkeypatch.setattr(plots, 'draw_one', draw)
    elif fault == 'no_figures':
        replay.arrangement['figures'] = []

    assert workflow.run_paper_step()['status'] == 'completed'
    artifacts = [event for event in replay.events if event['type'] == 'artifact']
    expected_slots = replay.context.output_slots[:2] if fault == 'no_figures' else replay.context.output_slots
    assert {event['slot'] for event in artifacts} == set(expected_slots)
    analysis, charts, execution, paths = published(replay)
    assert all(path.is_file() and path.stat().st_size for path in paths.values())
    assert [row['value'] for row in analysis['findings']] == pytest.approx([2., 5.])
    assert all(json.loads(row['calculation'])['scope'] == replay.inputs['topic'] for row in analysis['findings'])
    assert payload(replay.analysis.call_args.args[0])['tables']['data']['rows'] == replay.inputs['data']['rows']
    figure_request = next(payload(call.args[0]) for call in replay.structured.call_args_list
                          if 'analysis_results' in payload(call.args[0]))
    assert [row['value'] for row in figure_request['analysis_results']] == [2., 5.]
    assert set(figure_request['outline']) == {'questions', 'outline'}
    assert replay.conclusion in analysis['summary']
    assert replay.conclusion in paths['.md'].read_text()
    assert '完整统计结果' not in paths['.md'].read_text()
    replay.narrative.assert_called_once()
    narrative = payload(replay.narrative.call_args.args[0])
    assert [row['value'] for row in narrative['analysis_results']] == [2., 5.]
    assert [figure['title'] for figure in narrative['figures']] == [chart['title'] for chart in charts]
    if fault == 'no_figures':
        assert charts == [] and not replay.plot.called
        return
    assert len(charts) == 1 and charts[0]['title'] == 'Mean scores'
    assert charts[0]['finding_ids'] == [row['id'] for row in analysis['findings']]
    assert bool(execution['figures'][0].get('fallback')) == (fault == 'fallback')
    if fault in ('fallback', 'missing_plot'):
        assert '图表部分完成' in analysis['summary']
        if fault == 'fallback':
            assert execution['figures'][0]['fallback']['relationship_not_shown']
            assert len(execution['figures'][0]['original_attempts']) == 2
        else:
            assert execution['figures'][1]['status'] == 'failed'
            assert execution['plan']['failed_figures'][0]['id'] == 'fig2'
            assert payload(replay.narrative.call_args.args[0])['unavailable_figures'] == ['Missing comparison']
    assert '<svg' in paths['.svg'].read_text()
    assert paths['.png'].read_bytes().startswith(b'\x89PNG\r\n\x1a\n')
    assert paths['.pdf'].read_bytes().startswith(b'%PDF-')
    assert replay.structured.call_count == 2 + (fault in ('plan', 'figure_plan', 'analysis'))
    assert replay.analysis.call_count == replay.plot.call_count == 1
    assert replay.repair.call_count == (fault in ('plot', 'fallback'))


@pytest.mark.parametrize('stage', ['analysis', 'plot'])
@pytest.mark.parametrize('recover', [True, False])
def test_four_local_repairs_then_success_or_explicit_failure(replay, monkeypatch, stage, recover):
    monkeypatch.delenv('LAZYMIND_PAPER_GENERATED_ANALYSIS_ATTEMPTS')
    monkeypatch.delenv('LAZYMIND_PAPER_PLOT_ATTEMPTS')
    source = ANALYSIS_CODE if stage == 'analysis' else PLOT_CODE
    statement = '    return dict(results=results)' if stage == 'analysis' else '    return fig'
    line = source.splitlines().index(statement) + 1
    broken = source.replace(statement, '    return missing_name')
    (replay.analysis if stage == 'analysis' else replay.plot).return_value = {'code': broken}
    repairs = []

    def repair(prompt):
        sent = payload(prompt)
        assert f'{line} |     return missing_name' in sent['previous_code']
        assert 'missing_name' in sent['error'] and f'line {line}' in sent['error']
        repairs.append(sent)
        code = statement if recover and len(repairs) == 4 else '    return missing_name'
        return dict(replacements=[dict(start_line=line, end_line=line, code=code)])

    def structured(prompt):
        sent = payload(prompt)
        if sent.get('previous_code'):
            return repair(prompt)
        return replay.arrangement if 'analysis_results' in sent else replay.plan

    replay.structured.side_effect = structured
    replay.repair.side_effect = repair
    if stage == 'analysis' and not recover:
        with pytest.raises(ContractError, match='missing_name'):
            workflow.run_paper_step()
        assert not any(event['type'] == 'artifact' for event in replay.events)
        replay.narrative.assert_not_called()
        replay.plot.assert_not_called()
    else:
        assert workflow.run_paper_step()['status'] == 'completed'
        analysis, _, execution, _ = published(replay)
        attempts = execution['attempts'] if stage == 'analysis' else execution['figures'][0].get(
            'original_attempts', execution['figures'][0]['attempts'])
        assert len(attempts) == 5
        assert [row['value'] for row in analysis['findings']] == [2., 5.]
        if recover:
            field, status = ('outcome', 'passed') if stage == 'analysis' else (
                'status', 'passed_execution_and_file_checks')
            assert attempts[-1][field] == status and not execution['figures'][0].get('fallback')
        else:
            assert execution['figures'][0]['fallback']['relationship_not_shown']
            assert '图表部分完成' in analysis['summary']
    assert len(repairs) == 4 and replay.analysis.call_count == 1
    assert replay.plot.call_count == (0 if stage == 'analysis' and not recover else 1)


@pytest.mark.parametrize('stage,limit', [('plan', 3), ('figure_plan', 2)])
def test_code_repair_budget_does_not_expand_planning_retries(replay, monkeypatch, stage, limit):
    monkeypatch.delenv('LAZYMIND_PAPER_GENERATED_ANALYSIS_ATTEMPTS')
    replay.structured.side_effect = ([{}] * limit if stage == 'plan' else [replay.plan] + [{}] * limit)
    with pytest.raises(ContractError, match='analysis.plan' if stage == 'plan' else 'analysis.figures'):
        workflow.run_paper_step()
    assert replay.structured.call_count == limit + (stage == 'figure_plan')
    assert replay.analysis.call_count == (stage == 'figure_plan')
    assert not any(event['type'] == 'artifact' for event in replay.events)
    replay.plot.assert_not_called()


def test_parallel_figures_keep_full_evidence_private_context_and_order(replay, monkeypatch):
    replay.arrangement['figures'] = [dict(replay.arrangement['figures'][0], result_indices=[index],
                                        title=f'Mean {index + 1}') for index in range(2)]
    monkeypatch.setenv('LAZYMIND_PAPER_PLOT_CONCURRENCY', '2')
    barrier, second_done = Barrier(2), Event()
    request = ContextVar('f4_test_request', default=None)
    original, completed = plots.generate_code, []
    source_bytes = replay.source.read_bytes()

    def generate(data, statistics, spec, reference, previous, error):
        assert request.get() == 'f4-request'
        assert lazyllm.globals['agentic_config']['user_id'] == 'f4-test'
        data['test_marker'] = spec['id']
        barrier.wait(timeout=10)
        assert data['test_marker'] == spec['id']
        if spec['id'] == 'fig1':
            assert second_done.wait(10)
        completed.append(spec['id'])
        second_done.set()
        return original(data, statistics, spec, reference, previous, error)

    monkeypatch.setattr(plots, 'generate_code', generate)
    token = request.set('f4-request')
    try:
        assert workflow.run_paper_step()['status'] == 'completed'
    finally:
        request.reset(token)
    analysis, charts, execution, _ = published(replay)
    assert completed == ['fig2', 'fig1'] and [chart['id'] for chart in charts] == ['fig1', 'fig2']
    assert [chart['finding_ids'] for chart in charts] == [['r1'], ['r2']]
    for call in replay.plot.call_args_list:
        sent = payload(call.args[0])
        assert [row['value'] for row in sent['analysis_results']] == [2., 5.]
        assert sent['tables']['data']['rows'] == replay.inputs['data']['rows']
        assert sent['shared_style']['series_order'] == ['baseline', 'candidate']
        assert sent['shared_style']['terminology']['baseline'] == 'Baseline'
        assert 'HELPER SOURCE' in sent['skill_reference'] and 'def ' in sent['skill_reference']
    assert replay.source.read_bytes() == source_bytes and replay.analysis.call_count == 1
    assert all(item['concurrency'] == 2 and not item.get('fallback') for item in execution['figures'])
    assert [row['value'] for row in analysis['findings']] == [2., 5.]


@pytest.mark.parametrize('stage', ['preparation', 'render'])
@pytest.mark.parametrize('failed_id', ['fig1', 'fig2', 'fig3', 'fig1+fig3'])
def test_one_figure_exception_keeps_other_exports_and_writing_handoff(replay, monkeypatch, stage, failed_id):
    replay.arrangement['figures'] *= 3
    failed_ids = failed_id.split('+')
    original = plots.prepare_figures if stage == 'preparation' else plots.draw_one

    def prepare(analysis, figures, inputs, shared=None):
        if any(figure['id'] in failed_ids for figure in figures):
            raise ValueError('injected figure preparation failure')
        return original(analysis, figures, inputs, shared)

    def render(*args):
        if args[2]['id'] in failed_ids:
            raise RuntimeError('injected figure execution failure')
        return original(*args)

    monkeypatch.setattr(plots, 'prepare_figures' if stage == 'preparation' else 'draw_one',
                        prepare if stage == 'preparation' else render)
    assert workflow.run_paper_step()['status'] == 'completed'
    analysis, charts, execution, paths = published(replay)
    expected = [identifier for identifier in ['fig1', 'fig2', 'fig3'] if identifier not in failed_ids]
    assert [chart['id'] for chart in charts] == expected
    assert [row['value'] for row in analysis['findings']] == [2., 5.]
    assert f'已生成 {len(expected)}/3 张图' in analysis['summary']
    assert '未生成：Mean scores' in paths['.md'].read_text()
    assert [item['id'] for item in execution['figures']] == ['fig1', 'fig2', 'fig3']
    for failed in (item for item in execution['figures'] if item['id'] in failed_ids):
        assert failed['status'] == 'failed' and 'injected figure' in failed['error']['message']
    assert [item['id'] for item in execution['plan']['failed_figures']] == failed_ids
    pool = workflow._read_bound(str(paths['.json']))
    from lazymind.document_tools.paper import contracts
    writing = contracts.validate_input('write_outline', workflow.business_input('write_outline', pool))
    assert [chart['id'] for chart in writing['charts']] == expected
    exports = [Path(event['value']['path']) for event in replay.events
               if event['type'] == 'artifact' and event['slot'] == 'analysis_chart']
    assert len(exports) == 3 * len(expected) and all(path.is_file() and path.stat().st_size for path in exports)
    assert {path.suffix for path in exports} == {'.svg', '.png', '.pdf'}
    assert payload(replay.narrative.call_args.args[0])['unavailable_figures'] == ['Mean scores'] * len(failed_ids)


@pytest.mark.parametrize('failure', [RuntimeError, plots.FigureRenderError])
def test_all_requested_figures_failing_does_not_publish_success(replay, monkeypatch, failure):
    def render(*args):
        if failure is plots.FigureRenderError:
            raise failure('injected render and fallback failure', [])
        raise failure('injected unhandled figure failure')

    monkeypatch.setattr(plots, 'draw_one', render)
    with pytest.raises(RuntimeError, match='全部请求图表生成失败'):
        workflow.run_paper_step()
    assert not any(event['type'] == 'artifact' for event in replay.events)
    replay.narrative.assert_not_called()


def test_figure_cancellation_is_not_converted_to_partial_success(replay, monkeypatch):
    from concurrent.futures import CancelledError
    replay.arrangement['figures'] *= 2
    original = plots.draw_one

    def render(*args):
        if args[2]['id'] == 'fig1':
            raise CancelledError('injected cancellation')
        return original(*args)

    monkeypatch.setattr(plots, 'draw_one', render)
    with pytest.raises(CancelledError):
        workflow.run_paper_step()
    assert not any(event['type'] == 'artifact' for event in replay.events)


@pytest.mark.parametrize('fault', ['source', 'nonfinite'])
def test_invalid_executed_results_are_not_published_or_hidden_by_plot_fallback(replay, monkeypatch, fault):
    monkeypatch.setenv('LAZYMIND_PAPER_GENERATED_ANALYSIS_ATTEMPTS', '1')
    source = (ANALYSIS_CODE.replace('column=metric', "column='missing_column'") if fault == 'source' else
              ANALYSIS_CODE.replace('sum(values) / len(values)', "float('nan')"))
    replay.analysis.return_value = {'code': source}
    with pytest.raises(ContractError):
        workflow.run_paper_step()
    assert not any(event['type'] == 'artifact' for event in replay.events)
    replay.plot.assert_not_called()
    replay.narrative.assert_not_called()


def test_short_plan_accepts_changed_columns_groups_units_and_missing_values(replay):
    inputs = copy.deepcopy(replay.inputs)
    inputs['data'] = dict(columns=['condition', 'latency'], column_types=['string', 'number'],
                          units=[None, 'ms'], rows=[['a', 1.], ['a', None], ['a', 3.], ['b', 5.], ['c', 8.]])
    replay.source.write_text(json.dumps(inputs))
    replay.plan['questions'] = ['Mean latency by condition?']
    replay.arrangement['figures'][0]['result_indices'] = [0, 1, 2]
    replay.narrative.return_value = '三组均值为 2、5、8 ms；仅限本次模拟样本。'
    assert workflow.run_paper_step()['status'] == 'completed'
    analysis, charts, _, _ = published(replay)
    assert [row['value'] for row in analysis['findings']] == [2., 5., 8.]
    assert {row['unit'] for row in analysis['findings']} == {'ms'}
    assert [row['sample_size']['excluded_n'] for row in analysis['findings']] == [1, 0, 0]
    assert len(analysis['targets']) == 1 and charts[0]['finding_ids'] == ['r1', 'r2', 'r3']
    assert replay.analysis.call_count == replay.plot.call_count == 1


def test_invalid_input_is_not_published(replay):
    inputs = json.loads(replay.source.read_text())
    inputs['data']['rows'][0].pop()
    replay.source.write_text(json.dumps(inputs))
    with pytest.raises(ContractError):
        workflow.run_paper_step()
    assert not any(event['type'] == 'artifact' for event in replay.events)
    replay.structured.assert_not_called()


def test_chat_hands_f4_to_workflow_and_stops(monkeypatch):
    from lazyllm.tools.agent.functionCall import _tool_result_stop_text
    from lazymind.chat.workflow import workflow_manager

    client = Mock()
    client.get_ready_steps.return_value = {'state_version': 1, 'ready_steps': ['data_analysis']}
    client.advance.return_value = SimpleNamespace(result={'accepted': True, 'task_id': 'f4-task'})
    monkeypatch.setattr(workflow_manager, '_client', lambda: client)
    lazyllm.globals['agentic_config'] = {'query': '只执行 F4'}
    result = workflow_manager._handoff_tool('session')('data_analysis')
    request = client.advance.call_args.args[0]
    assert request.handoff and [step.step_id for step in request.steps] == ['data_analysis']
    assert result['task_id'] == 'f4-task' and _tool_result_stop_text(result)
    client.advance.assert_called_once()


def test_skill_override_requires_the_actual_skill_root(tmp_path):
    from lazymind.document_tools.paper.plotting.skills import PlotSkills

    root = tmp_path / 'plot-from-data'
    shutil.copytree(PlotSkills().root, root)
    with pytest.raises(RuntimeError, match='缺少绘图 skill'):
        PlotSkills(str(tmp_path))
    assert 'bar_grouped_hatch' in PlotSkills(str(root)).catalog()


def test_json_attachment_binding_stays_in_analysis(tmp_path, monkeypatch):
    from lazymind.document_tools.paper.analysis import attachments, trace
    from lazymind.chat.engine.tools.file_resources import resolver
    from lazymind.chat.engine.tools import host_file_resolution

    source = tmp_path / 'data.json'
    value = {'topic': 'Analysis', 'data': {'columns': ['score'], 'column_types': ['number'],
             'units': [None], 'rows': [[1], [2], [3]]}}
    value.update(request='只分析附件中的得分',
                 papers=[{'id': 'SRC-001', 'title': '测试来源', 'text': '虚构测试材料'}],
                 evidence='[SRC-001] 测试证据')
    source.write_text(json.dumps(value))
    lazyllm.globals['agentic_config'] = {'history_files_per_turn': {'1': [str(source)]}}
    assert attachments.workflow_input_bindings({}, '根据上传附件写论文') == {}
    assert attachments.workflow_input_bindings({}, '只做数据分析，使用上传附件') == {
        'paper_input': str(source)}
    monkeypatch.setattr(resolver, 'resolve_text_target',
                        lambda *a, **kw: SimpleNamespace(kind='attachment_text', path=str(source)))
    monkeypatch.setattr(host_file_resolution, 'open_input_file', lambda path: open(path, 'rb'))
    monkeypatch.setattr(trace, 'save', Mock())
    reference = tmp_path / 'reference.txt'
    reference.write_text('data.json')
    bound = attachments.bind_attachment({'paper_input': str(reference)}, str(tmp_path), '分析数据')
    assert json.loads(Path(bound['paper_input']).read_text()) == value
    pool = workflow.initialize_pool(bound, '分析数据')
    assert workflow.material(pool, 'data')['content']['rows'] == [[1], [2], [3]]
    request = workflow._step_request('data_analysis', pool, {'user_input': '启动请求和实验授权问答'})
    assert request == value['request']
    writing = workflow.business_input('write_outline', pool, value['request'])
    assert writing['papers'] == value['papers']
    assert writing['evidence'] == value['evidence']

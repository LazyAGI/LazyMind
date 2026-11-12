"""Redrawing keeps scientific results and untouched figures frozen."""
import copy
from unittest.mock import Mock

import pytest

from lazymind.document_tools.paper.analysis import pipeline
from lazymind.document_tools.paper.analysis.plan import validate_plan
from lazymind.document_tools.paper.analysis.schema import ContractError, digest, normalize_input
from lazymind.document_tools.paper.plotting import analysis as plots


SVG = '<svg xmlns="http://www.w3.org/2000/svg"><text>rate</text></svg>'


@pytest.mark.parametrize('user_request,answer', [
    ('Change only the color palette.', True), ('Recalculate after excluding failed runs.', False), ('retry', False)])
def test_redraw_intent_uses_structured_meaning(user_request, answer, monkeypatch):
    from lazymind.document_tools.paper import data_analysis
    classifier = Mock(return_value=dict(only_visual_changes=answer))
    monkeypatch.setattr(pipeline.model, 'call_model', classifier)
    assert data_analysis.redraw_requested(user_request) is answer
    classifier.assert_called_once()
    prompt = classifier.call_args.args[0]
    assert any(user_request in message['content'] for message in prompt['input'])


def test_empty_redraw_request_does_not_call_model(monkeypatch):
    from lazymind.document_tools.paper import data_analysis
    classifier = Mock(side_effect=AssertionError('empty request classified'))
    monkeypatch.setattr(pipeline.model, 'call_model', classifier)
    assert not data_analysis.redraw_requested('  ')
    classifier.assert_not_called()


@pytest.fixture
def frozen():
    inputs = normalize_input(dict(topic='Method scores', request='original analysis', data=dict(
        columns=['method', 'score'], column_types=['string', 'number'], units=[None, 'point'],
        rows=[['A', 1.], ['B', 2.]])))
    plan = validate_plan(dict(questions=['What are the observed method scores?'], outline='Describe scores.'), inputs)
    rows = [dict(question_id='q1', label=method, metric='score', method='Observed value', value=value,
                 columns=[dict(table_id='data', column='score')], scope='One observation per method',
                 coordinates=dict(method=method), unit='point', sample_size=dict(
                     source_rows=[dict(table_id='data', count=1)], n=1, unit='observation', excluded_n=0))
            for method, value in [('A', 1.), ('B', 2.)]]
    analysis = pipeline.build_analysis(inputs, plan, dict(results=rows))
    charts = [dict(id=identifier, title=identifier, columns=[dict(table_id='data', column='score')],
                   finding_ids=[finding], caption='Original caption.', svg=SVG)
              for identifier, finding in [('chart-a', 'r1'), ('chart-b', 'r2')]]
    inputs['request'] = 'Only redraw chart-a as a point plot.'
    return inputs, analysis, charts


@pytest.mark.parametrize('user_request', [
    'Only redraw chart-a as a point plot.', 'Do not redraw all charts. Only update chart-a.',
    '不要重画所有图表，只改第一张。'])
def test_redraw_never_reexecutes_statistics_or_changes_other_figure(frozen, monkeypatch, user_request):
    inputs, analysis, charts = frozen
    inputs['request'] = user_request
    before = copy.deepcopy((inputs, analysis, charts))
    forbidden = Mock(side_effect=AssertionError('redraw executed statistical analysis or rewrote interpretation'))
    for name in ('make_plan', 'generate_program', 'interpret_results'):
        monkeypatch.setattr(pipeline.model, name, forbidden)
    monkeypatch.setattr(pipeline.executor, 'execute', forbidden)
    monkeypatch.setattr(pipeline.model, 'plan_figures', Mock(return_value=dict(all_existing_figures=False, figures=[dict(
        title='A score', purpose='Observed score for A.', style='point_interval', result_indices=[0],
        source_chart_id='chart-a')])))

    def render(bound_analysis, figures, **kwargs):
        assert digest(bound_analysis) == digest(analysis)
        assert [(item['id'], item['finding_ids']) for item in figures] == [('chart-a', ['r1'])]
        replacement = dict(charts[0], title='A score', caption='New point display.')
        return [replacement], [dict(id='chart-a', caption=replacement['caption'], exports={})]

    monkeypatch.setattr(plots, 'draw', render)
    with pipeline.capture_execution() as records:
        output = pipeline.redraw(inputs, analysis, charts)
    assert output['analysis'] == analysis and output['charts'][1] == charts[1]
    assert output['charts'][0]['id'] == 'chart-a' and output['charts'][0]['caption'] == 'New point display.'
    assert (inputs, analysis, charts) == before
    assert records[0]['mode'] == 'redraw'
    assert records[0]['analysis_preserved_sha256'] == digest(analysis)
    forbidden.assert_not_called()


def test_redraw_planner_receives_existing_style_and_coordinate_bindings(frozen, monkeypatch):
    inputs, analysis, charts = frozen
    charts[0].update(style='scatter_tradeoff', category='method', series='metric', facet=None)
    charts[0]['caption'] = 'Scientific description.\n\nSources: internal-file.tsv\nLimitations: source notes'

    def plan(bound_inputs, bound_analysis, previous, error):
        assert previous['existing_figures'][0]['style'] == 'scatter_tradeoff'
        assert previous['existing_figures'][0]['series'] == 'metric'
        assert previous['existing_figures'][0]['caption'] == 'Scientific description.'
        return dict(figures=[dict(title='A', purpose='Only labels.', style='point_interval',
                                  source_chart_id='chart-a', finding_ids=['r1'])])

    monkeypatch.setattr(pipeline.model, 'plan_figures', plan)
    monkeypatch.setattr(plots, 'draw', lambda *args, **kwargs: ([charts[0]], []))
    result = pipeline.redraw(inputs, analysis, charts)
    assert result['analysis'] == analysis and result['charts'][1] == charts[1]


@pytest.mark.parametrize('recovers', [True, False])
def test_explicit_all_figures_scope_requires_complete_coverage_before_rendering(frozen, monkeypatch, recovers):
    inputs, analysis, charts = frozen
    inputs['request'] = 'Only redraw all existing charts.'
    drafts = [dict(title=chart['title'], purpose='Improve layout.', style='point_interval',
                   source_chart_id=chart['id'], finding_ids=chart['finding_ids']) for chart in charts]
    planner = Mock(side_effect=[dict(all_existing_figures=True, figures=drafts[:1]),
                                dict(all_existing_figures=True, figures=drafts if recovers else drafts[:1])])
    monkeypatch.setattr(pipeline.model, 'plan_figures', planner)
    renderer = Mock(return_value=(charts, []))
    monkeypatch.setattr(plots, 'draw', renderer)
    if recovers:
        assert pipeline.redraw(inputs, analysis, charts)['charts'] == charts
        renderer.assert_called_once()
    else:
        with pytest.raises(ContractError, match='missing source_chart_id'):
            pipeline.redraw(inputs, analysis, charts)
        renderer.assert_not_called()
    assert planner.call_count == 2
    assert 'chart-b' in planner.call_args.args[-1]


def test_redraw_rejects_changed_raw_data_before_planning(frozen, monkeypatch):
    inputs, analysis, charts = frozen
    inputs['data']['rows'][0][1] = 99.
    planner = Mock(side_effect=AssertionError('changed data reached plotting'))
    monkeypatch.setattr(pipeline.model, 'plan_figures', planner)
    with pytest.raises(ContractError, match='数据变更'):
        pipeline.redraw(inputs, analysis, charts)
    planner.assert_not_called()


def test_redraw_cannot_replace_a_figure_with_another_comparison(frozen, monkeypatch):
    inputs, analysis, charts = frozen
    monkeypatch.setattr(pipeline.model, 'plan_figures', Mock(return_value=dict(figures=[dict(
        title='Wrong binding', purpose='Different comparison.', style='point_interval', result_indices=[1],
        source_chart_id='chart-a')])))
    renderer = Mock(side_effect=AssertionError('changed comparison reached drawing'))
    monkeypatch.setattr(plots, 'draw', renderer)
    original = digest(analysis)
    with pytest.raises(ContractError, match='不得添加或删除'):
        pipeline.redraw(inputs, analysis, charts)
    assert digest(analysis) == original
    renderer.assert_not_called()


@pytest.mark.parametrize('raw', [
    '<think>provider reasoning mentions {"only_visual_changes":false}</think>\n{"only_visual_changes":true}',
    '<think>provider reasoning</think>\n```json\n{"only_visual_changes":true}\n```',
    '<think>first</think>\n<think>second</think>\n{"only_visual_changes":true}'])
def test_redraw_intent_accepts_actual_leading_think_envelope_without_reasoning_leak_or_recompute(raw, monkeypatch):
    from lazymind.document_tools.paper import data_analysis
    from lazymind.document_tools.paper.plotting import model as plot_model
    response = Mock(return_value=raw)
    monkeypatch.setattr(plot_model, 'request_model', response)
    assert data_analysis.redraw_requested('Only improve labels and move the legend; keep all statistics.')
    response.assert_called_once()


def test_redraw_intent_repairs_format_once_with_original_request_and_strict_boolean(monkeypatch):
    from lazymind.document_tools.paper import data_analysis
    from lazymind.document_tools.paper.plotting import model as plot_model
    response = Mock(side_effect=['{"only_visual_changes":"true"}', '{"only_visual_changes":true}'])
    monkeypatch.setattr(plot_model, 'request_model', response)
    request = 'Only change label wording and legend placement.'
    assert data_analysis.redraw_requested(request)
    assert response.call_count == 2
    prompts = [str(call.args[0]) for call in response.call_args_list]
    assert all(request in prompt for prompt in prompts)
    assert 'previous_format_error' in prompts[1] and 'valid boolean' in prompts[1]


@pytest.mark.parametrize('raw', [
    '<think>unfinished reasoning {"only_visual_changes":true}',
    '<think>{"only_visual_changes":true}</think>',
    '{"only_visual_changes":1}',
    '{}',
    '[{"only_visual_changes":true}]',
    'Only visual changes, yes.'])
def test_bad_redraw_classification_never_falls_back_to_reanalysis(raw, monkeypatch):
    from lazymind.document_tools.paper import data_analysis
    from lazymind.document_tools.paper.plotting import model as plot_model
    response = Mock(return_value=raw)
    monkeypatch.setattr(plot_model, 'request_model', response)
    forbidden = Mock(side_effect=AssertionError('malformed classification triggered analysis'))
    monkeypatch.setattr(data_analysis, 'generated_analysis', forbidden)
    with pytest.raises(ContractError) as failure:
        data_analysis.redraw_requested('Keep all statistics and only move the legend.')
    assert failure.value.code == 'INVALID_OUTPUT'
    assert failure.value.field == 'input.redraw_intent'
    assert response.call_count == 2
    forbidden.assert_not_called()


def test_empty_provider_redraw_response_is_service_failure_not_format_repair(monkeypatch):
    from lazymind.document_tools.paper import data_analysis
    from lazymind.document_tools.paper.plotting.model import PlotModelError
    classifier = Mock(side_effect=PlotModelError('provider returned no content'))
    monkeypatch.setattr(pipeline.model, 'call_model', classifier)
    with pytest.raises(PlotModelError, match='no content'):
        data_analysis.redraw_requested('Only move the legend.')
    classifier.assert_called_once()


def test_redraw_intent_has_public_owner_stage_and_saves_validated_boolean(tmp_path, monkeypatch):
    import json
    from lazymind.document_tools.paper import data_analysis
    from lazymind.document_tools.paper.analysis import trace as diagnostics
    from lazymind.document_tools.paper.plotting import model as plot_model
    monkeypatch.setattr(plot_model, 'request_model', Mock(return_value='{"only_visual_changes":true}'))
    with diagnostics.capture(tmp_path, task_id='redraw-intent-test') as recorder:
        assert data_analysis.redraw_requested('Only move the legend.')
    public = json.loads((recorder.root / 'public.json').read_text())
    assert public['process_steps'][0]['title'] == '确认是否仅重画图表 · 第 1 次'
    assert public['process_steps'][0]['status'] == 'succeeded'
    assert json.loads((recorder.root / 'redraw-intent.json').read_text()) == {'only_visual_changes': True}

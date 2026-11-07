"""Scientific bindings and real Matplotlib behavior of the fixed estimate renderer."""
import copy
import json

import pytest

from lazymind.document_tools.paper.analysis.figure_plan import bind_figures
from lazymind.document_tools.paper.analysis.results import result_rows
from lazymind.document_tools.paper.plotting.standard import PROGRAM, check_encoding, evidence_caption


def finding(identifier, method, value, n, **coordinates):
    return dict(id=identifier, target_id='q1', kind='estimate', statement='Observed rate.', value=value,
                unit='proportion', columns=[dict(table_id='data', column='success')],
                calculation=json.dumps(dict(label=method, metric='success', method='Conditional proportion',
                                            scope='Only correct plans', coordinates=dict(method=method, **coordinates),
                                            comparison='Methods conditional on a correct plan')),
                sample_size=dict(n=n, unit='correct plans', excluded_n=None,
                                 source_rows=[dict(table_id='data', count=24)]), interval=None,
                test=None, probability=None, model_id=None)


@pytest.fixture
def analysis():
    return dict(findings=[finding('r1', 'structured', 18 / 19, 19), finding('r2', 'direct', 20 / 21, 21),
                          finding('r3', 'unselected', 1., 24)], methods=['Conditional proportion'], models=[],
                limitations=['Simulation'], targets=[], data_sha256='a' * 64, input_sha256='b' * 64)


def test_bound_plan_exposes_condition_denominators_and_sources(analysis):
    source = dict(figures=[dict(
        title='Conditional execution', purpose='Compare execution given correct planning.',
        style='bar_grouped_hatch', result_indices=[0, 1], data_status='simulated')])
    original = copy.deepcopy(analysis)
    plan = bind_figures(source, analysis)['figures'][0]
    assert [row['sample_size']['n'] for row in plan['evidence']] == [19, 21]
    assert plan['evidence'][0]['scope'] == 'Only correct plans'
    assert plan['evidence'][0]['columns'] == [dict(table_id='data', column='success')]
    caption = evidence_caption(plan, result_rows(analysis)[:2])
    assert 'n=19' in caption and 'n=21' in caption
    assert 'n=24' not in caption and 'Simulation data' in caption and 'Sources: data' in caption
    assert analysis == original


@pytest.mark.parametrize('category', [None, 'method', 'invented'])
def test_line_cannot_connect_unordered_conditions(analysis, category):
    with pytest.raises(ValueError, match='coordinate'):
        check_encoding(dict(style='line_training_curve', category=category), result_rows(analysis))


def test_line_rejects_duplicate_positions_and_outcome_sorting():
    row = dict(metric='score', unit='point', kind='estimate', value=1., label='Score',
               coordinates=dict(epoch=1, method='A'))
    with pytest.raises(ValueError, match='Duplicate'):
        check_encoding(dict(style='line_training_curve', category='epoch'), [row, copy.deepcopy(row)])
    with pytest.raises(ValueError, match='never sort'):
        check_encoding(dict(style='line_training_curve', category='epoch', sort='value_descending'), [row])


def test_standard_bar_preserves_values_selection_and_condition_n(analysis):
    pytest.importorskip('matplotlib')
    import matplotlib
    matplotlib.use('Agg')
    import matplotlib.pyplot as plt
    data = dict(analysis_results=result_rows(analysis), shared_style=dict(
        terminology={'structured': 'Structured', 'direct': 'Direct'}, colors=['#546BF5', '#888888']))
    original = copy.deepcopy(data)
    namespace = {}
    exec(PROGRAM, namespace)
    figure = namespace['draw'](data, {}, dict(
        style='bar_grouped_hatch', finding_ids=['r1', 'r2'], data_status='simulated', sort='source'))
    try:
        axis = figure.axes[0]
        assert [patch.get_height() for patch in axis.patches] == pytest.approx([18 / 19, 20 / 21])
        assert [text.get_text() for text in axis.get_xticklabels()] == ['Structured', 'Direct']
        assert axis.get_ylim() == (0, 1)
        assert [text.get_text() for text in axis.texts] == ['94.7%', '95.2%']
        assert axis.get_legend() is None
        assert any(text.get_text() == 'Simulation' for text in figure.texts)
        assert data == original
    finally:
        plt.close(figure)


@pytest.mark.parametrize('category', ['metric', 'label'])
@pytest.mark.parametrize('mixed_units', [False, True])
def test_comparable_metric_categories_share_axes_without_losing_unit_boundaries(analysis, category, mixed_units):
    pytest.importorskip('matplotlib')
    import matplotlib.pyplot as plt

    prototype = analysis['findings'][0]
    analysis['findings'] = []
    for index in range(6):
        item = copy.deepcopy(prototype)
        item.update(id=f'r{index + 1}', value=.9 + index / 100)
        metadata = json.loads(item['calculation'])
        metadata.update(metric=f'Outcome {index + 1}', label=f'Outcome {index + 1}', coordinates={})
        item['calculation'] = json.dumps(metadata)
        if mixed_units and index == 5:
            item['unit'] = 's'
        analysis['findings'].append(item)
    original = copy.deepcopy(analysis)
    plan = bind_figures(dict(figures=[dict(
        title='Evaluation profile', purpose='Compare the recorded outcomes.', style='point_interval',
        finding_ids=[item['id'] for item in analysis['findings']], category=category,
    )]), analysis)
    assert len(plan['figures']) == 1
    figure = draw_rows(result_rows(analysis), plan['figures'][0])
    try:
        assert len(figure.axes) == (2 if mixed_units else 1)
        assert sum(len(axis.collections) for axis in figure.axes) == 6
        assert analysis == original
    finally:
        plt.close(figure)


def test_shared_legend_moves_above_axes_without_hiding_observations():
    pytest.importorskip('matplotlib')
    import matplotlib.pyplot as plt
    from lazymind.document_tools.paper.plotting.worker import place_figure_legends
    figure, axis = plt.subplots(figsize=(6.2, 2.5))
    try:
        for index in range(5):
            axis.scatter(index, index, label=f'Task {index + 1}')
        figure.legend(loc='upper right')
        place_figure_legends(figure)
        renderer = figure.canvas.get_renderer()
        box = figure.legends[0].get_window_extent(renderer)
        assert not box.overlaps(axis.get_window_extent(renderer))
        assert box.height < figure.bbox.height * .25
        assert [text.get_text() for text in figure.legends[0].get_texts()] == [f'Task {i + 1}' for i in range(5)]
        assert [tuple(item.get_offsets()[0]) for item in axis.collections] == [(i, i) for i in range(5)]
    finally:
        plt.close(figure)


@pytest.mark.parametrize('geometry,invalid_limits,valid_limits,error', [
    ('bar(0, 10, width=.4)', 'set_xlim(0, 1)', 'set_xlim(-.5, .5)', 'Bar is clipped on categorical'),
    ('barh(0, 10, height=.4)', 'set_ylim(0, 1)', 'set_ylim(-.5, .5)', 'Bar is clipped on categorical'),
    ('bar(0, 85)', 'set_ylim(0, 3)', 'set_ylim(0, 90)', 'Measured bar endpoint 85 is outside numeric'),
    ('barh(0, 85)', 'set_xlim(0, 3)', 'set_xlim(0, 90)', 'Measured bar endpoint 85 is outside numeric'),
])
def test_actual_export_rejects_clipping_and_accepts_complete_limits(geometry, invalid_limits, valid_limits, error):
    pytest.importorskip('matplotlib')
    from lazymind.document_tools.paper.plotting import worker

    code = f'''import matplotlib.pyplot as plt
def draw(data, statistics, spec):
    figure, axis = plt.subplots(figsize=(5.8, 1.8))
    axis.{geometry}
    axis.{invalid_limits}
    return figure
'''
    payload = dict(table={}, statistics={}, spec={}, data_sha256='a' * 64, code=code)
    with pytest.raises(ValueError, match=error):
        worker.render(payload)
    payload['code'] = code.replace(invalid_limits, valid_limits)
    assert '<svg' in worker.render(payload)['svg']


def test_long_category_bars_are_horizontal_and_preserve_values_and_intervals(analysis):
    pytest.importorskip('matplotlib')
    import matplotlib.pyplot as plt
    rows = result_rows(analysis)[:2]
    rows[0]['coordinates']['method'] = 'Overall document task completion'
    rows[1]['coordinates']['method'] = 'Target scope classification accuracy'
    rows[0]['interval'] = dict(lower=.8, upper=.99, kind='confidence', level=.95, method='supplied')
    original = copy.deepcopy(rows)
    figure = draw_rows(rows, dict(style='bar_grouped_hatch', category='method'))
    try:
        axis = figure.axes[0]
        assert [patch.get_width() for patch in axis.patches] == pytest.approx([18 / 19, 20 / 21])
        assert axis.get_xlim() == (0, 1)
        assert list(axis.lines[0].get_xdata()) == [.8, .99]
        assert [text.get_text().replace('\n', ' ') for text in axis.get_yticklabels()] == [
            'Overall document task completion', 'Target scope classification accuracy']
        assert figure.get_figwidth() > figure.get_figheight()
        assert rows == original
    finally:
        plt.close(figure)


def test_standard_line_orders_x_without_recomputing_intervals(analysis):
    pytest.importorskip('matplotlib')
    import matplotlib
    matplotlib.use('Agg')
    import matplotlib.pyplot as plt
    rows = result_rows(analysis)[:2]
    rows[0]['coordinates'] = dict(epoch=2)
    rows[1]['coordinates'] = dict(epoch=1)
    rows[0]['interval'] = dict(lower=.8, upper=.99, kind='credible', level=.95, method='supplied posterior')
    namespace = {}
    exec(PROGRAM, namespace)
    data = dict(analysis_results=rows, shared_style=dict(terminology={}, colors=['#546BF5']))
    figure = namespace['draw'](data, {}, dict(
        style='line_confidence_band', category='epoch', finding_ids=['r1', 'r2']))
    try:
        lines = figure.axes[0].lines
        assert list(lines[0].get_xdata()) == [1, 2]
        assert list(lines[0].get_ydata()) == pytest.approx([20 / 21, 18 / 19])
        assert len(lines) == 2 and list(lines[1].get_ydata()) == [.8, .99]
        assert '95%credible' in evidence_caption({}, rows)
        assert rows[0]['interval']['method'] == 'supplied posterior'
    finally:
        plt.close(figure)


def test_public_standard_draw_uses_actual_renderer_without_model(analysis, monkeypatch):
    pytest.importorskip('matplotlib')
    from lazymind.document_tools.paper.plotting import analysis as plots, pipeline, worker
    monkeypatch.setattr(pipeline, 'execute_plot', worker.render)  # Replace transport, keep actual drawing/export.
    monkeypatch.setattr(plots, 'call_code_model', lambda *_: pytest.fail('standard figure requested generated code'))
    figure = bind_figures(dict(figures=[dict(
        title='Rates', purpose='Conditional execution.', style='bar_grouped_hatch', result_indices=[0, 1])]),
        analysis)['figures'][0]
    inputs = dict(topic='Conditional execution', data=dict(
        columns=['success'], column_types=['number'], units=[None], rows=[[1.]]))
    charts, traces = plots.draw(analysis, [figure], inputs)
    assert '<svg' in charts[0]['svg'] and set(traces[0]['exports']) == {'png', 'pdf'}
    assert 'n=19' in charts[0]['caption'] and 'n=21' in charts[0]['caption']
    assert traces[0]['renderer'] == 'standard_estimates_v2' and not traces[0].get('fallback')


@pytest.mark.parametrize('normalize', [False, True])
@pytest.mark.parametrize('series', ['phase', 'label'])
def test_native_horizontal_stack_uses_existing_helper_and_preserves_components(analysis, monkeypatch, normalize, series):
    pytest.importorskip('matplotlib')
    import xml.etree.ElementTree as ET
    from lazymind.document_tools.paper.plotting import analysis as plots, pipeline, worker
    monkeypatch.setattr(pipeline, 'execute_plot', worker.render)
    monkeypatch.setattr(plots, 'call_code_model', lambda *_: pytest.fail('stack requested generated code'))
    prototype = analysis['findings'][0]
    analysis['findings'] = []
    for index, value in enumerate([12., 18., 64., 6.]):
        item = copy.deepcopy(prototype)
        item.update(id=f'r{index + 1}', value=value, unit='s')
        metadata = json.loads(item['calculation'])
        metadata.update(label=f'Component {index + 1}', metric='Elapsed time',
                        coordinates={'phase': f'Phase {index + 1}'})
        item['calculation'] = json.dumps(metadata)
        analysis['findings'].append(item)
    original = copy.deepcopy(analysis)
    figure = bind_figures(dict(figures=[dict(
        title='Stage composition', purpose='Show additive stage durations.', style='bar_stacked',
        series=series, normalize=normalize, finding_ids=['r1', 'r2', 'r3', 'r4'])]), analysis)['figures'][0]
    charts, traces = plots.draw(analysis, [figure], dict(topic='Stage timing', data=dict(
        columns=['success'], column_types=['number'], units=[None], rows=[[1.]])))
    assert traces[0]['renderer'] == 'native_stacked_v1' and len(traces[0]['attempts']) == 1
    root = ET.fromstring(charts[0]['svg'])
    assert float(root.attrib['width'].removesuffix('pt')) > float(root.attrib['height'].removesuffix('pt'))
    visible = [''.join(node.itertext()) for node in root.iter() if node.tag.rsplit('}', 1)[-1] == 'text']
    assert all(f'Phase {index}' in visible for index in range(1, 5))
    assert any('Total 100' in text for text in visible)
    assert any('Share (%)' in text for text in visible) is normalize
    assert (charts[0]['style'], charts[0]['series']) == ('bar_stacked', 'phase')
    assert analysis == original


@pytest.mark.parametrize('style', ['line_confidence_band', 'point_interval', 'bar_grouped_hatch'])
@pytest.mark.parametrize('low,high,endpoint_name', [(.8, None, 'lower'), (None, 1.1, 'upper')])
def test_partial_intervals_show_available_endpoint_without_inventing_other_end(
        analysis, style, low, high, endpoint_name):
    pytest.importorskip('matplotlib')
    import matplotlib
    matplotlib.use('Agg')
    import matplotlib.pyplot as plt
    rows = result_rows(analysis)[:1]
    rows[0]['coordinates'] = dict(epoch=2, method='structured')
    rows[0]['interval'] = dict(lower=low, upper=high, kind='confidence', level=.95, method='supplied interval')
    data = dict(analysis_results=rows, shared_style=dict(terminology={}, colors=['#546BF5']))
    before = copy.deepcopy(data)
    namespace = {}
    exec(PROGRAM, namespace)
    figure = namespace['draw'](data, {}, dict(style=style, category='epoch', finding_ids=['r1']))
    try:
        axis = figure.axes[0]
        endpoint = low if low is not None else high
        markers = [tuple(offset) for collection in axis.collections for offset in collection.get_offsets()]
        expected = ((2., endpoint) if style.startswith('line_') else
                    (0., endpoint) if style == 'bar_grouped_hatch' else (endpoint, 0.))
        assert expected in markers
        assert len(axis.lines) == (0 if style == 'point_interval' else 1)
        assert f'only {endpoint_name} endpoint {endpoint:g} available' in evidence_caption({}, rows)
        assert 'opposite endpoint not supplied' in evidence_caption({}, rows)
        assert data == before
    finally:
        plt.close(figure)


@pytest.mark.parametrize('sort,expected', [
    ('source', ['Direct', 'Structured', 'Unselected']),
    ('value_descending', ['Unselected', 'Direct', 'Structured'])])
def test_series_order_keeps_unknown_groups_and_value_sort_remains_explicit(analysis, sort, expected):
    pytest.importorskip('matplotlib')
    import matplotlib
    matplotlib.use('Agg')
    import matplotlib.pyplot as plt
    data = dict(analysis_results=result_rows(analysis), shared_style=dict(
        terminology={'structured': 'Structured', 'direct': 'Direct', 'unselected': 'Unselected'},
        colors=['#546BF5'], series_order=['direct', 'structured']))
    before = copy.deepcopy(data)
    namespace = {}
    exec(PROGRAM, namespace)
    figure = namespace['draw'](data, {}, dict(
        style='bar_grouped_hatch', sort=sort, finding_ids=['r1', 'r2', 'r3']))
    try:
        assert [item.get_text() for item in figure.axes[0].get_xticklabels()] == expected
        values = [patch.get_height() for patch in sorted(figure.axes[0].patches, key=lambda patch: patch.get_x())]
        assert values == pytest.approx([20 / 21, 18 / 19, 1.] if sort == 'source' else [1., 20 / 21, 18 / 19])
        assert data == before
    finally:
        plt.close(figure)


def test_line_series_order_is_independent_of_first_x_observation(analysis):
    pytest.importorskip('matplotlib')
    import matplotlib
    matplotlib.use('Agg')
    import matplotlib.pyplot as plt
    rows = result_rows(analysis)
    rows[0]['coordinates'] = dict(epoch=1, method='structured')
    rows[1]['coordinates'] = dict(epoch=2, method='direct')
    rows[2]['coordinates'] = dict(epoch=1, method='unselected')
    namespace = {}
    exec(PROGRAM, namespace)
    data = dict(analysis_results=rows, shared_style=dict(
        terminology={}, colors=['#546BF5'], series_order=['direct', 'structured']))
    figure = namespace['draw'](data, {}, dict(
        style='line_training_curve', category='epoch', series='method', finding_ids=['r1', 'r2', 'r3']))
    try:
        axis = figure.axes[0]
        assert axis.get_legend_handles_labels()[1] == ['direct', 'structured', 'unselected']
        assert [list(line.get_xdata()) for line in axis.lines] == [[2], [1], [1]]
        assert [list(line.get_ydata()) for line in axis.lines] == [[20 / 21], [18 / 19], [1.]]
    finally:
        plt.close(figure)


def test_actual_svg_export_keeps_visible_cjk_labels_for_fact_checks():
    pytest.importorskip('matplotlib')
    import base64
    import io
    import xml.etree.ElementTree as ET
    from PIL import Image
    from lazymind.document_tools.paper.plotting import worker
    from lazymind.document_tools.paper.fact_checks import check_facts
    from lazymind.document_tools.paper.review.schema import ReviewResult
    code = '''import matplotlib.pyplot as plt

def draw(data, statistics, spec):
    figure, axis = plt.subplots(figsize=(3.35, 2))
    axis.plot([0, 1], [0, 1])
    axis.text(.2, .7, '条件样本 n=19', fontsize=9)
    axis.set_xlabel('成功率')
    return figure
'''
    output = worker.render(dict(table={}, statistics={}, spec={}, data_sha256='a' * 64, code=code))
    root = ET.fromstring(output['svg'])
    visible = [''.join(node.itertext()) for node in root.iter() if node.tag.rsplit('}', 1)[-1] == 'text']
    assert any('条件样本 n=19' in text for text in visible) and any('成功率' in text for text in visible)
    assert 'display: none' not in output['svg'] and 'visibility: hidden' not in output['svg']
    png = Image.open(io.BytesIO(base64.b64decode(output['exports']['png'])))
    assert png.width > 600 and png.height > 300
    assert png.convert('L').getextrema()[0] < 100
    claim = dict(category='figure', location='结果', quote='图中条件样本 n=19。', asserted_text='19',
                 operation='figure_value', sources=[
                     {'path': '/analysis/n'}, {'path': '/charts/0/svg', 'quote': 'n=19', 'value_text': '19'}])
    result = ReviewResult(summary='核对真实导出', issues=[], limitations=[], fact_claims=[claim])
    inputs = dict(manuscript=claim['quote'], analysis=dict(n=19), charts=[dict(svg=output['svg'])])
    checked = next(group for group in check_facts(result, inputs) if group.category == 'figure').checks[0]
    assert checked.status == 'verified'
    inputs['analysis']['n'] = 24
    checked = next(group for group in check_facts(result, inputs) if group.category == 'figure').checks[0]
    assert checked.status == 'conflict'


def grouped_rows():
    rows = []
    for cohort, count in [('First set', 10), ('Second set', 20)]:
        for category in ['Accuracy', 'Scope']:
            for period, hits in [('old', count - 2), ('new', count - 1)]:
                row = finding(str(len(rows)), category, hits / count, count)
                row = result_rows({'findings': [row]})[0]
                row['coordinates'] = dict(cohort=cohort, outcome=category, outcome_id=category[:2],
                                          period=period, hits=hits, denominator=count)
                rows.append(row)
    return rows


def draw_rows(rows, spec):
    namespace = {}
    exec(PROGRAM, namespace)
    return namespace['draw'](dict(analysis_results=rows, shared_style=dict(
        terminology={}, colors=['#D3D3D3', '#546BF5'], series_order=['old', 'new'])), {},
        dict(spec, finding_ids=[row['id'] for row in rows]))


def test_grouped_plan_infers_only_unambiguous_facet_and_fraction_coordinates():
    rows = grouped_rows()
    spec = dict(style='bar_grouped_hatch', category='outcome', series='period')
    original = copy.deepcopy(rows)
    check_encoding(spec, rows)
    assert spec['facet'] == 'cohort'
    assert (spec['numerator'], spec['denominator']) == ('hits', 'denominator')
    pytest.importorskip('matplotlib')
    import matplotlib.pyplot as plt
    figure = draw_rows(rows, spec)
    try:
        assert len(figure.axes) == 2
        for axis in figure.axes:
            by_category = [sorted(axis.patches, key=lambda patch: patch.get_x())[index:index + 2]
                           for index in (0, 2)]
            assert all(abs(pair[0].get_x() - pair[1].get_x()) < .5 for pair in by_category)
            assert [text.get_text() for text in axis.get_xticklabels()] == ['Accuracy', 'Scope']
            assert axis.get_ylim() == (0, 1)
            assert all('/' in text.get_text() for text in axis.texts)
            legend_handles = axis.get_legend().legend_handles
            assert [handle.get_facecolor() for handle in legend_handles] == [
                axis.patches[0].get_facecolor(), axis.patches[2].get_facecolor()]
            assert [handle.get_hatch() for handle in legend_handles] == ['', '//']
        assert rows == original
    finally:
        plt.close(figure)


def test_ambiguous_faceting_and_duplicate_cells_require_an_explicit_plan():
    rows = grouped_rows()
    for index, row in enumerate(rows):
        row['coordinates']['machine'] = 'X' if index % 3 else 'Y'
    with pytest.raises(ValueError, match='Multiple additional grouping'):
        check_encoding(dict(style='bar_grouped_hatch', category='outcome', series='period'), rows)
    repeated = [copy.deepcopy(rows[0]), copy.deepcopy(rows[0])]
    with pytest.raises(ValueError, match='Duplicate category/series'):
        check_encoding(dict(style='bar_grouped_hatch', category='outcome', series='period', facet='cohort'), repeated)


def test_missing_and_negative_values_are_not_zero_filled_and_partial_ci_remains_visible():
    pytest.importorskip('matplotlib')
    import matplotlib.pyplot as plt
    rows = grouped_rows()[:3]
    for row in rows:
        row['metric'], row['unit'], row['kind'] = 'change', 'point', 'effect'
    rows[0]['value'] = -2.
    rows[0]['interval'] = dict(lower=-3., upper=None, kind='confidence', level=.95, method='supplied')
    rows[1]['coordinates'].update(outcome='Scope', period='old')
    rows[2]['coordinates'].update(outcome='Scope', period='new')
    rows[1]['value'], rows[2]['value'] = 1., 2.
    spec = dict(style='bar_grouped_hatch', category='outcome', series='period', facet='cohort')
    check_encoding(spec, rows)
    original = copy.deepcopy(rows)
    figure = draw_rows(rows, spec)
    try:
        axis = figure.axes[0]
        assert sorted(patch.get_height() for patch in axis.patches) == [-2., 1., 2.]
        assert axis.get_ylim()[0] < -3. and axis.get_ylim()[1] > 1.
        assert sum(text.get_text() == 'missing' for text in axis.texts) == 1
        assert any(offset[1] == -3. for collection in axis.collections for offset in collection.get_offsets())
        assert rows == original
    finally:
        plt.close(figure)


def test_same_quantity_facets_share_limits_but_other_units_do_not():
    pytest.importorskip('matplotlib')
    import matplotlib.pyplot as plt
    rows = grouped_rows()[:3]
    for row, (cohort, value, unit) in zip(rows, [('A', 12.9, 'pp'), ('B', 26.7, 'pp'), ('C', 400., 's')]):
        row['coordinates'] = dict(cohort=cohort, outcome='Same category')
        row['metric'], row['unit'], row['kind'], row['value'] = 'change', unit, 'effect', value
    spec = dict(style='bar_grouped_hatch', category='outcome', facet='cohort')
    check_encoding(spec, rows)
    figure = draw_rows(rows, spec)
    try:
        assert figure.axes[0].get_ylim() == figure.axes[1].get_ylim()
        assert figure.axes[2].get_ylim() != figure.axes[0].get_ylim()
        assert [axis.patches[0].get_height() for axis in figure.axes] == [12.9, 26.7, 400.]
    finally:
        plt.close(figure)


def test_proportion_and_percent_scales_are_displayed_without_changing_values():
    pytest.importorskip('matplotlib')
    import matplotlib.pyplot as plt
    rows = grouped_rows()[:2]
    rows[0]['value'], rows[0]['unit'] = .5, 'proportion'
    rows[1]['value'], rows[1]['unit'] = 50., '%'
    for row in rows:
        row['coordinates'] = dict(outcome='Observed rate')
    spec = dict(style='bar_grouped_hatch', category='outcome', annotation='value')
    check_encoding(spec, rows)
    figure = draw_rows(rows, spec)
    try:
        assert [axis.patches[0].get_height() for axis in figure.axes] == [.5, 50.]
        assert [axis.texts[0].get_text() for axis in figure.axes] == ['50%', '50%']
        assert [axis.get_ylim() for axis in figure.axes] == [(0, 1), (0, 100)]
    finally:
        plt.close(figure)


@pytest.mark.parametrize('fault', ['wrong_count', 'missing_numerator', 'missing_denominator', 'wrong_unit'])
def test_invalid_fraction_hint_uses_only_executed_values_and_keeps_sample_sizes(fault):
    pytest.importorskip('matplotlib')
    import matplotlib.pyplot as plt
    rows = grouped_rows()
    if fault == 'wrong_count':
        rows[0]['coordinates']['hits'] = 999
    elif fault == 'missing_numerator':
        rows[0]['coordinates'].pop('hits')
    elif fault == 'missing_denominator':
        rows[0]['coordinates'].pop('denominator')
    else:
        for row in rows:
            row['unit'] = 'point'
    original = copy.deepcopy(rows)
    spec = dict(style='bar_grouped_hatch', category='outcome', series='period', facet='cohort',
                annotation='fraction', numerator='hits', denominator='denominator')
    check_encoding(spec, rows)
    assert spec['annotation'] == 'value' and spec['annotation_adjustment']
    assert spec['numerator'] is None and spec['denominator'] is None
    figure = draw_rows(rows, spec)
    try:
        assert all('/' not in text.get_text() for axis in figure.axes for text in axis.texts)
        assert [patch.get_height() for axis in figure.axes for patch in axis.patches] == [
            .8, .8, .9, .9, .9, .9, .95, .95]
        caption = evidence_caption(spec, rows)
        assert 'n=10' in caption and 'n=20' in caption
        assert rows == original
    finally:
        plt.close(figure)


def test_short_caption_keeps_original_research_limits_and_readable_source():
    rows = grouped_rows()
    limits = ['历史观察未经受控比较，不能解释为因果收益。', '不同指标可能复用同一批题目，不能合并分母。']
    caption = evidence_caption(dict(facet='cohort', source_names={'data': '内部历史评测记录'},
                                    limitations=limits), rows, 'zh-CN')
    assert all(item in caption for item in limits)
    assert '内部历史评测记录（data）' in caption
    assert 'no interval' not in caption and '来源' in caption


def test_invalid_percent_hint_retains_each_source_unit_and_value():
    pytest.importorskip('matplotlib')
    import matplotlib.pyplot as plt
    rows = grouped_rows()[:3]
    values = [(.5, 'proportion', 'estimate'), (12.9, 'pp', 'effect'), (7.5, 'unknown score', 'estimate')]
    for row, (value, unit, kind) in zip(rows, values):
        row.update(value=value, unit=unit, kind=kind)
        row['coordinates'] = dict(outcome='Same outcome')
    original = copy.deepcopy(rows)
    spec = dict(style='bar_grouped_hatch', category='outcome', value_format='percent', annotation='value')
    check_encoding(spec, rows)
    assert spec['value_format'] == 'auto'
    assert 'original numbers and units' in spec['format_adjustment']
    figure = draw_rows(rows, spec)
    try:
        assert [axis.patches[0].get_height() for axis in figure.axes] == [.5, 12.9, 7.5]
        assert [axis.texts[0].get_text() for axis in figure.axes] == ['50%', '12.9pp', '7.5']
        assert 'unknown score' in figure.axes[2].get_ylabel()
        assert '%' not in figure.axes[1].get_ylabel() + figure.axes[2].get_ylabel()
        assert rows == original
    finally:
        plt.close(figure)


def grouped_terminology_proposal(analysis):
    for index, item in enumerate(analysis['findings']):
        metadata = json.loads(item['calculation'])
        metadata['coordinates'].update(metric='Observed rate', metric_id='q01', test_set='Set A',
                                       version='before' if index == 0 else 'after')
        item['calculation'] = json.dumps(metadata)
    return dict(figures=[dict(title='Rates', purpose='Compare the frozen results.', style='bar_grouped_hatch',
                              result_indices=[0, 1], category='method')], terminology={
        'metric': {'Observed rate': 'Rate'}, 'metric_id': {'q01': 'Q1'},
        'test_set': {'Set A': 'Set A'}, 'version': {'before': 'Before', 'after': 'After'}})


def test_observed_grouped_terminology_becomes_strict_flat_aliases_without_changing_analysis(analysis):
    proposal = grouped_terminology_proposal(analysis)
    original = copy.deepcopy((analysis, proposal))
    bound = bind_figures(proposal, analysis)
    assert bound['terminology'] == {
        'Observed rate': 'Rate', 'q01': 'Q1', 'Set A': 'Set A', 'before': 'Before', 'after': 'After'}
    assert bound['figures'][0]['finding_ids'] == ['r1', 'r2']
    assert (analysis, proposal) == original


@pytest.mark.parametrize('terms,match', [
    ({'not-a-coordinate': {'before': 'Before'}}, 'observed coordinate'),
    ({'version': {'never-observed': 'Before'}}, 'exact values'),
    ({'version': {'before': {'label': 'Before'}}}, 'must be strings'),
    ({'version': {'before': 1}}, 'must be strings'),
    ({'version': {'before': 'Before'}, 'before': 'Old'}, 'Conflicting terminology'),
    ({'metric': {'Observed rate': 'Rate'}, 'test_set': {'Observed rate': 'Dataset'}}, 'Conflicting terminology')])
def test_terminology_rejects_unknown_nested_values_non_strings_and_conflicting_flat_meanings(analysis, terms, match):
    proposal = grouped_terminology_proposal(analysis)
    if 'test_set' in terms:
        for item in analysis['findings']:
            metadata = json.loads(item['calculation'])
            metadata['coordinates']['test_set'] = 'Observed rate'
            item['calculation'] = json.dumps(metadata)
    proposal['terminology'] = terms
    with pytest.raises(ValueError, match=match):
        bind_figures(proposal, analysis)


@pytest.mark.parametrize('unit', ['pp', 'percentage point', 'percentage points', '百分点', '个百分点'])
def test_percentage_point_spellings_keep_numbers_and_use_paper_language(unit):
    pytest.importorskip('matplotlib')
    import matplotlib.pyplot as plt
    rows = grouped_rows()[:1]
    rows[0].update(metric='change', unit=unit, kind='effect', value=12.9)
    spec = dict(style='bar_grouped_hatch', category='outcome', paper_language='zh-CN')
    original = copy.deepcopy(rows)
    check_encoding(spec, rows)
    figure = draw_rows(rows, spec)
    try:
        assert figure.axes[0].patches[0].get_height() == 12.9
        assert figure.axes[0].texts[0].get_text() == '12.9个百分点'
        assert figure.axes[0].get_ylabel() == '变化 (个百分点)'
        assert rows == original
    finally:
        plt.close(figure)


def test_exact_result_ids_survive_reordering_without_recomputing_or_shifting(analysis):
    analysis['findings'] = [analysis['findings'][2], analysis['findings'][0], analysis['findings'][1]]
    original = copy.deepcopy(analysis)
    proposal = dict(figures=[dict(title='Rates', purpose='Compare chosen rates.',
                                  style='bar_grouped_hatch', finding_ids=['r2', 'r1'])])
    bound = bind_figures(proposal, analysis)['figures'][0]
    assert bound['finding_ids'] == ['r2', 'r1']
    assert [row['value'] for row in bound['evidence']] == [20 / 21, 18 / 19]
    assert analysis == original


@pytest.mark.parametrize('ids,indices,match', [
    ([], [], 'select exact finding_ids'),
    (['invented'], [], 'unique exact'),
    (['r1', 'r1'], [], 'unique exact'),
    (['r1', 'r2'], [1, 0], 'different results or order'),
    (['r2'], [0], 'different results or order')])
def test_exact_result_selection_rejects_unknown_duplicate_or_conflicting_legacy_selection(analysis, ids, indices, match):
    proposal = dict(figures=[dict(title='Rates', purpose='Compare chosen rates.', style='bar_grouped_hatch',
                                  finding_ids=ids, result_indices=indices)])
    with pytest.raises(ValueError, match=match):
        bind_figures(proposal, analysis)


def test_consistent_dual_selection_remains_compatible(analysis):
    proposal = dict(figures=[dict(title='Rates', purpose='Compare chosen rates.', style='bar_grouped_hatch',
                                  finding_ids=['r2', 'r1'], result_indices=[1, 0])])
    assert bind_figures(proposal, analysis)['figures'][0]['finding_ids'] == ['r2', 'r1']


def test_planner_receives_compact_identified_scientific_catalog(analysis, monkeypatch):
    from lazymind.document_tools.paper.analysis import model
    captured = []
    proposal = dict(figures=[dict(title='Rates', purpose='Compare chosen rates.', style='bar_grouped_hatch',
                                  finding_ids=['r2', 'r1'])])
    monkeypatch.setattr(model, 'call_model', lambda prompt: captured.append(prompt) or proposal)
    inputs = dict(topic='Rate comparison', data=dict(columns=['success'], column_types=['number'],
                                                     units=['proportion'], rows=[[1.]]))
    result = model.plan_figures(inputs, analysis, dict(outline='Compare methods', questions=['Rates?']))
    blocks = [json.loads(message['content'].split('INPUT_JSON:\n', 1)[1])
              for message in captured[0]['input'] if message['role'] == 'user']
    catalog = next(block['analysis_results'] for block in blocks if 'analysis_results' in block)
    assert [row['id'] for row in catalog] == ['r1', 'r2', 'r3']
    assert all(set(('id', 'metric', 'kind', 'coordinates', 'value', 'unit', 'sample_size')) <= set(row)
               and 'index' not in row for row in catalog)
    assert catalog[1]['coordinates']['method'] == 'direct'
    assert catalog[1]['unit'] == 'proportion' and catalog[1]['sample_size']['n'] == 21
    assert bind_figures(result, analysis)['figures'][0]['finding_ids'] == ['r2', 'r1']


def test_caption_support_is_facet_local_and_does_not_call_unit_conflicts_varying_n(analysis):
    rows = result_rows(analysis)
    rows[0]['coordinates']['dataset'] = rows[1]['coordinates']['dataset'] = 'Set A'
    rows[2]['coordinates']['dataset'] = 'Set B'
    rows[0]['sample_size'].update(n=31, unit='items')
    rows[1]['sample_size'].update(n=31, unit='proportion')
    rows[2]['sample_size'].update(n=15, unit='items')
    caption = evidence_caption(dict(facet='dataset'), rows, 'zh-CN')
    assert 'Set A: n=31（样本单位记录不一致：items, proportion' in caption
    assert 'Set B: n=15' in caption and '样本量因组而异' not in caption
    rows[1]['sample_size'].update(n=30, unit='items')
    caption = evidence_caption(dict(facet='dataset'), rows, 'zh-CN')
    assert 'Set A: direct: n=30' in caption and 'Set A: unselected' not in caption
    assert caption.count('n=15') == 1 and 'Set B: n=15' in caption


def test_result_fields_bind_and_render_without_rewriting_frozen_coordinates(analysis):
    pytest.importorskip('matplotlib')
    import matplotlib
    matplotlib.use('Agg')
    import matplotlib.pyplot as plt

    for finding in analysis['findings']:
        metadata = json.loads(finding['calculation'])
        metadata['coordinates'] = {'实际分组字段': '单次测量'}
        finding['calculation'] = json.dumps(metadata)
    original = copy.deepcopy(analysis)
    plan = bind_figures(dict(figures=[dict(
        title='Observed results', purpose='Display existing estimates.', style='bar_grouped_hatch',
        finding_ids=['r1', 'r2'], category='label', facet='metric')]), analysis)['figures'][0]
    namespace = {}
    exec(PROGRAM, namespace)
    data = dict(analysis_results=result_rows(analysis), shared_style=dict(terminology={}, colors=['#546BF5']))
    figure = namespace['draw'](data, {}, plan)
    try:
        assert [p.get_height() for p in figure.axes[0].patches] == pytest.approx([18 / 19, 20 / 21])
        assert [t.get_text() for t in figure.axes[0].get_xticklabels()] == ['structured', 'direct']
        assert analysis == original
    finally:
        plt.close(figure)


def test_dimension_binding_discovers_actual_fields_and_keeps_unknown_fields_invalid(analysis):
    rows = result_rows(analysis)[:2]
    for row in rows:
        row['研究条件说明'] = row['label']
    spec = dict(style='bar_grouped_hatch', category='研究条件说明')
    check_encoding(spec, rows)
    with pytest.raises(ValueError, match='Available coordinates or descriptive result fields'):
        check_encoding(dict(style='bar_grouped_hatch', category='不存在的字段'), rows)
    with pytest.raises(ValueError, match='unavailable'):
        check_encoding(dict(style='bar_grouped_hatch', category='value'), rows)


@pytest.mark.parametrize('dimension', ['category', 'series', 'facet'])
def test_dimension_value_alias_uses_its_unique_field_without_changing_evidence(analysis, dimension):
    for row in analysis['findings']:
        metadata = json.loads(row['calculation'])
        metadata['coordinates']['指标'] = '图片数量'
        row['calculation'] = json.dumps(metadata)
    original = copy.deepcopy(analysis)
    encoding = dict(category='method', series=None, facet=None)
    encoding[dimension] = '图片数量'
    if dimension == 'category':
        encoding['series'] = 'method'
    plan = bind_figures(dict(figures=[dict(
        title='Observed results', purpose='Display the selected observations.', style='bar_grouped_hatch',
        finding_ids=['r1', 'r2'], **encoding)]), analysis)['figures'][0]
    assert plan[dimension] == '指标'
    assert plan['finding_ids'] == ['r1', 'r2']
    assert analysis == original


@pytest.mark.parametrize('defect', ['ambiguous', 'varying', 'partial_field'])
def test_dimension_value_alias_keeps_ambiguous_or_incomplete_fields_invalid(analysis, defect):
    rows = result_rows(analysis)[:2]
    for row in rows:
        row['coordinates']['指标'] = '图片数量'
    if defect == 'ambiguous':
        for row in rows:
            row['coordinates']['另一个指标'] = '图片数量'
    elif defect == 'varying':
        rows[1]['coordinates']['指标'] = '耗时'
    else:
        rows[0]['coordinates']['图片数量'] = 2
    original = copy.deepcopy(rows)
    with pytest.raises(ValueError, match='unavailable'):
        check_encoding(dict(style='bar_grouped_hatch', category='method', series='图片数量'), rows)
    assert rows == original


@pytest.mark.parametrize('mode', ['metrics', 'facets', 'dense'])
def test_dense_standard_figure_is_split_without_losing_evidence(analysis, mode):
    count = 70 if mode == 'dense' else 6
    prototype = analysis['findings'][0]
    analysis['findings'] = []
    for index in range(count):
        row = copy.deepcopy(prototype)
        row['id'] = f'r{index + 1}'
        metadata = json.loads(row['calculation'])
        metadata.update(label=f'Observation {index + 1}',
                        metric=f'metric-{index}' if mode == 'metrics' else 'score',
                        coordinates={'condition': str(index), 'item': str(index)})
        row['calculation'] = json.dumps(metadata)
        analysis['findings'].append(row)
    before = copy.deepcopy(analysis)
    ids = [row['id'] for row in analysis['findings']]
    proposal = dict(figures=[dict(
        title='Measurements', purpose='Show every observed result.',
        style='bar_grouped_hatch', finding_ids=ids, category='label',
        facet={'facets': 'condition', 'metrics': 'metric'}.get(mode))])
    figures = bind_figures(proposal, analysis)['figures']
    selected = [identifier for figure in figures for identifier in figure['finding_ids']]
    assert selected == ids
    assert len(figures) > 1
    rows = result_rows(analysis)
    for figure in figures:
        check_encoding(figure, [row for row in rows if row['id'] in figure['finding_ids']])
        assert len(figure['finding_ids']) <= 32
    assert analysis == before

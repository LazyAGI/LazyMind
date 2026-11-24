"""Basic shared-Agent regressions for retrieval coverage, plots and dynamic vision."""
import base64
from dataclasses import replace
import json
from pathlib import Path

import lazyllm
import pytest

from lazymind.document_tools.paper import agent_tools as tools
from lazymind.document_tools.paper.workflow import add_material, material, pool_from_business


@pytest.fixture
def context(monkeypatch, tmp_path):
    monkeypatch.setenv('LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN', 'paper-coverage-test')
    monkeypatch.setenv('LAZYLLM_HOME', str(tmp_path))
    pool = pool_from_business({'topic': 'Interactive writing'})
    return tools.PaperContext('session', 'attempt', 'revision', 'literature_review',
                              {'seed_pool': {'source_revision': '1'}}, {'seed_pool': pool},
                              request='Retrieve approximately 12 relevant papers.')


@pytest.mark.parametrize('count', [4, 12])
def test_requested_coverage_preserves_real_sources_and_never_blocks(context, count):
    papers = [{'id': f'p{i}', 'title': f'Actual research {i}', 'text': f'Observed evidence {i}.'}
              for i in range(count)]
    with tools.paper_scope(context):
        receipt = tools.receipt_reference(context, 'papers', papers)
        bundle = tools.paper_prepare_candidate({
            'evidence': 'Actual evidence ' + ' '.join(f'[文献:p{i}]' for i in range(count)),
            'selected_paper_ids': [paper['id'] for paper in papers],
            'search_plan': {'requested_paper_count': 12}}, [receipt])
        assert tools.verify_publication(context, bundle['artifacts']) == {'valid': True}
    assert bool(bundle['quality_warnings']) == (count < 12)
    if count < 12:
        assert '12' in bundle['quality_warnings'][0] and '4' in bundle['quality_warnings'][0]
    artifact = next(item for item in bundle['artifacts'] if item['slot'] == 'literature_review_pool')
    pool = json.loads(base64.b64decode(artifact['value']['content_base64']))
    assert len(material(pool, 'papers')['content']) == count
    assert pool['review_search_plan']['requested_paper_count'] == 12
    assert [paper['text'] for paper in material(pool, 'papers')['content']] == [p['text'] for p in papers]


def test_search_exposes_complementary_sources_and_reuses_snapshot(context, monkeypatch):
    from lazymind.document_tools.paper.review import materials, search
    calls = []

    def retrieve(query, **kwargs):
        calls.append(kwargs)
        return {'sources': [], 'papers': [
            {'id': f'{source}-{i}', 'title': f'{source} research {i}', 'source': source,
             'abstract': f'{source} actual abstract {i}'}
            for source in ('crossref', 'sciverse') for i in range(3)]}
    monkeypatch.setattr(materials, '_api_keys', lambda: {})
    monkeypatch.setattr(search, 'search_papers', retrieve)
    with tools.paper_scope(context):
        first = tools.paper_search('interactive writing', count=2, arxiv_terms=['collaborative writing'])
        second = tools.paper_search('interactive writing', count=2, page=2,
                                    arxiv_terms=['collaborative writing'], search_receipt=first['search_receipt'])
        assert {p['source'] for p in first['papers']} == {'crossref', 'sciverse'}
        assert first['more'] and second['more']
        assert len(calls) == 1 and calls[0]['limit'] == 20
        assert calls[0]['arxiv_terms'] == ['collaborative writing']
        with pytest.raises(ValueError, match='concise'):
            tools.paper_search('interactive writing systems revision history local editing empirical study')


def test_agent_receives_existing_selection_rules_and_exact_style_helpers(context, prepared_analysis_pool):
    with tools.paper_scope(context):
        assert 'requested_paper_count' in tools._prepared_inputs(context)['search_plan_schema']['properties']
        ctx = replace(context, step_id='data_analysis', values={'seed_pool': prepared_analysis_pool({
            'topic': 'Observed scores', 'data': {
                'columns': ['score'], 'column_types': ['number'], 'units': [None], 'rows': [[1]]}})})
        from lazymind.document_tools.paper.analysis.figure_plan import FIGURE_PLANNING_PROMPT
        full = tools._prepared_inputs(ctx)
        assert full['figure_planning_contract']['rules'] == FIGURE_PLANNING_PROMPT
        assert {'point_interval', 'scatter_tradeoff', 'bar_stacked'} <= full['figure_planning_contract']['styles'].keys()
    with tools.paper_scope(ctx):
        reference = tools.paper_read_material('plot_style:point_interval', limit=64000)
        value = json.loads(reference['text'])
        assert reference['next_offset'] is None
        assert value['style_contract']['source_style'] == 'point_interval'
        assert 'def draw_point_intervals' in value['reference']


@pytest.mark.parametrize('style', ['point_interval', 'bar_grouped_hatch', 'custom'])
def test_shared_plot_renders_actual_files_with_existing_style_rules(context, prepared_analysis_pool, monkeypatch, style):
    from lazymind.document_tools.paper.plotting.worker import render
    fixture = json.loads((Path(__file__).resolve().parents[4]
                          / 'tests/fixtures/paper-writing/data_analysis.json').read_text())
    fixture['input']['constraints'] = {'paper_language': 'en'}
    finding = fixture['output']['analysis']['findings'][0]
    finding['calculation'] = json.dumps({
        'label': 'Score difference', 'metric': 'Score', 'method': 'Difference',
        'scope': 'Two supplied records', 'coordinates': {}, 'comparison': 'evidence - baseline'})
    pool = prepared_analysis_pool(fixture['input'])
    add_material(pool, 'analysis', fixture['output']['analysis'], [], [])
    ctx = replace(context, step_id='data_analysis', values={'seed_pool': pool})
    payloads = []

    def execute(payload, **kwargs):
        payloads.append(payload)
        return render(payload)
    monkeypatch.setattr('lazymind.document_tools.paper.plotting.executor.execute_plot', execute)
    chart = {**fixture['output']['charts'][0], 'id': 'observed-' + style, 'style': style,
             'title': 'Observed score difference', 'caption': 'Two supplied records.',
             'base_style': 'point_interval' if style == 'custom' else None}
    chart.pop('svg')
    code = ('import matplotlib.pyplot as plt\n'
            'def draw(data, statistics, spec):\n'
            '    fig, ax = plt.subplots()\n'
            '    ax.scatter([0], [statistics["results"][0]["value"]])\n'
            '    paper_axes(ax, title="", xlabel="Condition", ylabel="Score", grid=True)\n'
            '    return fig\n') if style == 'custom' else ''
    with tools.paper_scope(ctx):
        receipt = tools.paper_prepare_inputs()['existing_analysis_receipt']
        result = tools.paper_plot(code, chart, receipt)
        signed = tools.unseal(ctx, result['receipt'], 'chart')
    assert signed['chart']['style'] == style
    assert '<svg' in signed['chart']['svg']
    assert base64.b64decode(signed['exports']['png']).startswith(b'\x89PNG\r\n\x1a\n')
    assert base64.b64decode(signed['exports']['pdf']).startswith(b'%PDF')
    assert payloads[0]['spec']['style_contract']['title_placement'] == 'metadata_only'
    assert payloads[0]['table']['rows'] == fixture['input']['data']['rows']
    if style == 'custom':
        assert 'def paper_axes' in payloads[0]['code']


def test_dynamic_vlm_configuration_uses_the_request_models(monkeypatch):
    from lazymind import vision_model
    from lazymind.model_config import inject_model_config, load_model_config
    config_path = Path(__file__).resolve().parents[4] / 'algorithm/lazymind/common/runtime_models.yaml'
    monkeypatch.setenv('LAZYMIND_MODEL_CONFIG_PATH', str(config_path))
    assert load_model_config(str(config_path))['vlm']['source'] == 'dynamic'
    with lazyllm.new_session():
        inject_model_config({'llm': {'source': 'openai', 'model': 'selected', 'vision': True}})
        assert vision_model.select_vision_model_role() == 'llm'
    with lazyllm.new_session():
        inject_model_config({'llm': {'source': 'openai', 'model': 'selected', 'vision': True},
                             'vlm': {'source': 'openai', 'model': 'explicit-vision', 'vision': True}})
        assert vision_model.select_vision_model_role() == 'vlm'

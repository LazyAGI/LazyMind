"""Quality handoffs and redraw routing through the real workflow adapter."""
import copy
import json
from pathlib import Path

import pytest

from lazymind.document_tools.paper import contracts, data_analysis, workflow
from lazymind.document_tools.paper.analysis.schema import binding, normalize_input


@pytest.fixture
def materials(prepared_analysis_pool):
    inputs = normalize_input({'topic': 'T', 'data': {
        'columns': ['score'], 'column_types': ['number'], 'units': ['point'], 'rows': [[2.]]}})
    columns = [{'table_id': 'data', 'column': 'score'}]
    analysis = dict(**binding(inputs), summary='Mean score is 2.', methods=['Mean'],
                    targets=[dict(id='mean', question='Mean?', estimand='mean', scope='one row', columns=columns)],
                    findings=[dict(id='score', target_id='mean', kind='estimate', statement='Mean score',
                                   columns=columns, calculation='mean(score)', value=2., unit='point',
                                   sample_size=dict(source_rows=[dict(table_id='data', count=1)], n=1,
                                                    unit='observation', excluded_n=0),
                                   interval=None, test=None, probability=None, model_id=None)],
                    models=[], diagnostics=[], limitations=['One observation.'])
    charts = [dict(id='score', title='Score', caption='One observation.', columns=columns,
                   finding_ids=['score'], svg='<svg xmlns="http://www.w3.org/2000/svg"><text>2</text></svg>')]
    pool = prepared_analysis_pool(inputs)
    outputs = [workflow.add_material(pool, 'analysis', analysis, [], []),
               workflow.add_material(pool, 'charts', charts, [], [])]
    workflow.add_material(pool, 'manuscript', '# Manuscript\nMean score is 2.', [], [])
    pool['history'].append(dict(node='F4', step='data_analysis', inputs=[], outputs=outputs))
    pool['sequence'] += 1
    return pool


def test_review_receives_only_manuscript_references_and_used_figures(materials):
    root = Path(__file__).resolve().parents[4] / 'tests/fixtures/paper-writing'
    status = json.loads((root / 'experiment.json').read_text())['output']['result_meta']
    papers = [{'id': 'ref1', 'title': 'Source', 'abstract': 'Only an abstract.'}]
    workflow.add_material(materials, 'result_meta', status, [], [])
    workflow.add_material(materials, 'papers', papers, [], [])
    workflow.add_material(materials, 'manuscript', '# Manuscript\nMean score is 2. [ref1]\n'
                          '<a id="block-score"></a>\n![Score](media-placeholder://score)', [], [])
    value = workflow.business_input('peer_review', materials)
    original = workflow.material(materials, 'charts')['content'][0]
    assert value['charts'] == [{key: original[key] for key in ('id', 'title', 'caption', 'svg')}]
    assert value['papers'] == papers
    assert set(value) == {'topic', 'request', 'manuscript', 'papers', 'charts'}
    checked = contracts.validate_input('peer_review', value)
    assert set(checked) == set(value)
    checked['papers'][0]['title'] = 'Changed copy'
    assert workflow.material(materials, 'papers')['content'] == papers


def test_review_accepts_bibliographic_metadata_without_fabricating_fulltext():
    value = contracts.validate_input('peer_review', {
        'topic': 'T', 'manuscript': '# Manuscript\nExisting citation [ref1].',
        'papers': [{'id': 'ref1', 'title': 'Only bibliographic metadata'}], 'charts': [],
    })
    assert value['papers'][0] == {'id': 'ref1', 'title': 'Only bibliographic metadata'}
    assert 'result_meta' not in value
    with pytest.raises(contracts.MissingMaterials):
        contracts.validate_value('papers', value['papers'])


@pytest.mark.parametrize('previous', ['write_document', 'peer_review'])
def test_visual_only_request_reaches_redraw_after_writing(materials, monkeypatch, previous):
    materials['history'].append(dict(node=workflow.STEP_NODE[previous], step=previous, inputs=[], outputs=[]))
    materials['sequence'] += 1
    original_analysis = copy.deepcopy(workflow.material(materials, 'analysis')['content'])
    original_charts = copy.deepcopy(workflow.material(materials, 'charts')['content'])
    old_refs = [materials['current'][key] for key in ('analysis', 'charts')]
    monkeypatch.setattr(data_analysis, 'redraw_requested', lambda request: request == 'Only improve layout')
    monkeypatch.setattr(workflow, 'run_algorithm', lambda *args: pytest.fail('Must not rerun statistics'))
    seen = []

    def redraw(inputs, current, figures):
        seen.append((copy.deepcopy(current), copy.deepcopy(figures)))
        assert inputs['request'] == 'Only improve layout'
        figures[0]['caption'] = 'Improved layout; same value and sample.'
        return {'analysis': current, 'charts': figures, 'is_demo': False}

    monkeypatch.setattr(data_analysis, 'redraw', redraw)
    before = copy.deepcopy(materials)
    result = workflow.execute_step('data_analysis', materials, 'Only improve layout')
    assert result['status'] == 'completed'
    assert seen == [(original_analysis, original_charts)]
    assert materials == before
    assert workflow.material(result['pool'], 'analysis')['content'] == original_analysis
    assert workflow.material(result['pool'], 'manuscript')['content'] == before['materials'][-1]['content']
    assert all(ref in result['pool']['history'][-1]['inputs'] for ref in old_refs)


def test_redraw_consumes_current_request_not_original_launch(materials):
    result = workflow._step_request('data_analysis', materials, {
        'user_input': 'Original full analysis', 'current_user_input': 'Only improve layout',
    })
    assert result == 'Only improve layout'


def test_redraw_updates_existing_manuscript_and_invalidates_stale_review(materials, monkeypatch):
    from lazymind.document_tools.paper import publication
    old = workflow.material(materials, 'charts')['content'][0]
    body = ('# Paper\nSee [Score](#block-score).\n\n<a id="block-score"></a>\n'
            '![Score](' + publication.chart_uri(old) + ')')
    workflow.add_material(materials, 'manuscript', body, [], [])
    workflow.add_material(materials, 'review', '# Old review', [], [])
    monkeypatch.setattr(data_analysis, 'redraw_requested', lambda request: True)

    def redraw(inputs, analysis, charts):
        charts[0]['svg'] = '<svg xmlns="http://www.w3.org/2000/svg"><text fill="blue">2</text></svg>'
        return dict(analysis=analysis, charts=charts, is_demo=False)
    monkeypatch.setattr(data_analysis, 'redraw', redraw)
    result = workflow.execute_step('data_analysis', materials, 'Only improve layout')['pool']
    manuscript = workflow.material(result, 'manuscript')['content']
    current = workflow.material(result, 'charts')['content']
    publication.validate_chart_references(manuscript, current)
    assert manuscript == body.replace(publication.chart_uri(old), publication.chart_uri(current[0]))
    assert workflow.material(result, 'review') is None
    assert workflow.material(materials, 'manuscript')['content'] == body
    assert any(item['kind'] == 'review' for item in result['materials'])


@pytest.mark.parametrize('new_slot', [False, True])
def test_redraw_publisher_saves_new_image_for_old_and_new_graphs(materials, tmp_path, monkeypatch, new_slot):
    import lazyllm
    from lazymind.chat.engine.subagent.context import SubAgentContext, set_context
    from lazymind.document_tools.paper import publication
    monkeypatch.setenv('LAZYMIND_UPLOAD_ROOT', str(tmp_path / 'uploads'))
    old = workflow.material(materials, 'charts')['content'][0]
    source = '# Paper\nSee [Score](#block-score).\n\n<a id="block-score"></a>\n![Score](media-placeholder://score)'
    workflow.add_material(materials, 'manuscript', publication.publish_charts(source, [old]), [], [])
    path = tmp_path / 'pool.json'
    path.write_text(json.dumps(materials))
    monkeypatch.setattr(data_analysis, 'redraw_requested', lambda request: True)

    def redraw(inputs, analysis, charts):
        charts[0]['svg'] = '<svg xmlns="http://www.w3.org/2000/svg"><text fill="blue">2</text></svg>'
        return dict(analysis=analysis, charts=charts, is_demo=False)
    monkeypatch.setattr(data_analysis, 'redraw', redraw)
    slots = ['data_analysis_pool', 'data_analysis_document', 'analysis_chart']
    if new_slot:
        slots.append('redrawn_manuscript_document')
    events = []
    previous = lazyllm.globals.get('subagent_ctx')
    set_context(SubAgentContext(
        task_id='redraw-repair', conversation_id='paper-test', agent_type='workflow_step',
        objective='Change layout only', params=dict(
            step_id='data_analysis', current_user_input='Only improve layout',
            remote_inputs={'data_analysis_pool': str(path)}, output_slot_types={slot: 'file' for slot in slots}),
        workspace_path=str(tmp_path), input_slots=['data_analysis_pool'], output_slots=slots,
        db=None, emit=events.append))
    try:
        assert workflow.run_paper_step()['status'] == 'completed'
        artifacts = {item['slot']: item['value'] for item in events if item['type'] == 'artifact'}
        saved = workflow._read_bound(artifacts['data_analysis_pool']['path'])
        charts = workflow.material(saved, 'charts')['content']
        manuscript = workflow.material(saved, 'manuscript')['content']
        publication.validate_chart_references(manuscript, charts)
        resource = tmp_path / 'uploads' / publication.chart_uri(charts[0]).removeprefix('/static-files/')
        assert resource.read_text() == charts[0]['svg']
        assert ('redrawn_manuscript_document' in artifacts) is new_slot
        if new_slot:
            assert Path(artifacts['redrawn_manuscript_document']['path']).read_text() == manuscript
    finally:
        if previous is None:
            lazyllm.globals.pop('subagent_ctx', None)
        else:
            lazyllm.globals['subagent_ctx'] = previous

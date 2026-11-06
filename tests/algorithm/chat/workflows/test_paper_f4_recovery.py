"""Configured rewind snapshots reach F4 without re-running analysis or losing edits."""
import copy
import json
from pathlib import Path
from unittest.mock import Mock

import lazyllm
import pytest
import yaml

from lazymind.chat.engine.subagent.context import SubAgentContext, set_context
from lazymind.document_tools.paper import data_analysis, publication, workflow
from lazymind.document_tools.paper.analysis import model, pipeline
from lazymind.document_tools.paper.analysis.plan import validate_plan
from lazymind.document_tools.paper.analysis.render import render
from lazymind.document_tools.paper.analysis.schema import normalize_input
from lazymind.document_tools.paper.plotting import analysis as plots

ROOT = Path(__file__).resolve().parents[4]
STEPS = ('data_analysis', 'write_outline', 'write_document', 'peer_review', 'revise_document')


@pytest.fixture
def package():
    folder = ROOT / 'workflows/paper-writing'
    plugin = yaml.safe_load((folder / 'workflow.yaml').read_text())
    state = yaml.safe_load((folder / 'scenario/state.yml').read_text())
    return plugin, state


@pytest.fixture
def snapshots(tmp_path, monkeypatch, prepared_analysis_pool):
    monkeypatch.setenv('LAZYMIND_UPLOAD_ROOT', str(tmp_path / 'uploads'))
    example = json.loads((ROOT / 'tests/fixtures/paper-writing/data_analysis.json').read_text())
    inputs = normalize_input(example['input'])
    output = example['output']
    finding = output['analysis']['findings'][0]
    plan = validate_plan(dict(questions=['Observed score difference?'], outline='Compare the two observations.'), inputs)
    output['analysis'] = pipeline.build_analysis(inputs, plan, dict(results=[dict(
        question_id='q1', label='Score difference', metric='score', method='Difference of two observations',
        value=finding['value'], unit=finding['unit'], columns=finding['columns'], kind='effect',
        scope='Two single observations.', coordinates={'comparison': 'evidence minus baseline'},
        comparison='evidence minus baseline', sample_size=finding['sample_size'])]))
    output['charts'][0]['finding_ids'] = ['r1']
    pool = prepared_analysis_pool(inputs)
    outputs = [workflow.add_material(pool, key, output[key], [], []) for key in ('analysis', 'charts')]
    pool['history'].append(dict(node='F4', step='data_analysis', inputs=[], outputs=outputs))
    pool['sequence'] += 1
    saved = {'data_analysis_pool': copy.deepcopy(pool), 'data_analysis_document': render(output['analysis'])}
    chart = output['charts'][0]
    manuscript = ('# Paper\n\nSee [score](#block-' + chart['id'] + ').\n\n<a id="block-'
                  + chart['id'] + '"></a>\n![Score](' + publication.chart_uri(chart) + ')\nUnchanged prose.')
    for step, kind, content in [
        ('write_outline', 'outline', '# Paper\n\n## Results'),
        ('write_document', 'manuscript', manuscript),
        ('peer_review', 'review', '# Review\nReview this exact version.'),
        ('revise_document', 'manuscript', manuscript + '\nRevision kept.'),
    ]:
        ref = workflow.add_material(pool, kind, content, [], [])
        pool['history'].append(dict(node=workflow.STEP_NODE[step], step=step, inputs=[], outputs=[ref]))
        pool['sequence'] += 1
        saved[step + '_pool'] = copy.deepcopy(pool)
        saved[step + '_document'] = content
    return inputs, output, saved


def recovery_inputs(package, saved, tmp_path):
    plugin, state = package
    declared = {item['material'] for item in state['steps']['data_analysis']['inputs']}
    bindings = plugin['runtime']['recovery_inputs']['data_analysis']
    assert set(bindings.values()) == {step + '_' + kind for step in STEPS for kind in ('pool', 'document')}
    assert set(bindings) <= declared
    slots = {item['id']: item for item in plugin['slots']}
    bound = {}
    for target, source in bindings.items():
        assert slots[target]['external'] is True and slots[target]['type'] == slots[source]['type'] == 'file'
        if source in saved:
            path = tmp_path / target
            value = saved[source]
            path.write_text(json.dumps(value, ensure_ascii=False) if isinstance(value, dict) else value)
            bound[target] = str(path)
    return bound


@pytest.mark.parametrize('last', STEPS)
def test_configured_recovery_uses_latest_pool_and_current_document(package, snapshots, tmp_path, last):
    _, _, saved = snapshots
    latest = STEPS.index(last)
    available = {key: value for key, value in saved.items()
                 if any(key == step + '_' + kind for step in STEPS[:latest + 1] for kind in ('pool', 'document'))}
    available[last + '_document'] += '\nHuman edit kept.'
    if last == 'data_analysis':
        # Structured analysis documents cannot silently replace executed numerical results.
        with pytest.raises(ValueError, match='STALE_MATERIAL'):
            workflow.initialize_pool(recovery_inputs(package, available, tmp_path))
        return
    restored = workflow.initialize_pool(recovery_inputs(package, available, tmp_path))
    kind = {'write_outline': 'outline', 'peer_review': 'review'}.get(last, 'manuscript')
    assert restored['sequence'] == latest + 2
    assert workflow.material(restored, kind)['content'] == available[last + '_document']
    assert workflow.material(restored, 'analysis')['content'] == snapshots[1]['analysis']
    if last == 'write_outline':
        assert workflow.material(restored, 'manuscript') is None


def test_recovery_snapshot_wins_same_sequence_tie(package, snapshots, tmp_path):
    _, _, saved = snapshots
    newer = saved['write_document_pool']
    stale = copy.deepcopy(newer)
    workflow.material(stale, 'manuscript')['content'] = '# Stale manuscript'
    path = tmp_path / 'seed_pool.json'
    path.write_text(json.dumps(stale))
    bound = {'seed_pool': str(path), **recovery_inputs(package, {
        key: value for key, value in saved.items() if key.startswith('write_document_')}, tmp_path)}
    restored = workflow.initialize_pool(bound)
    assert workflow.material(restored, 'manuscript')['content'] == saved['write_document_document']


def test_revision_recovery_uses_latest_selected_manuscript_instead_of_original_draft(package, snapshots, tmp_path):
    plugin, state = package
    _, _, saved = snapshots
    bindings = plugin['runtime']['recovery_inputs']['revise_document']
    declared = {item['material'] for item in state['steps']['revise_document']['inputs']}
    assert set(bindings) <= declared
    assert set(bindings.values()) == {'revise_document_pool', 'revise_document_document'}
    edited = saved['revise_document_document'] + '\nLatest human edit retained.'
    available = {**saved, 'revise_document_document': edited}
    bound = {'peer_review_pool': saved['peer_review_pool']}
    for target, source in bindings.items():
        bound[target] = available[source]
    restored = workflow.pool_from_inputs(bound)
    assert workflow.material(restored, 'manuscript')['content'] == edited
    original_version = saved['write_document_pool']['current']['manuscript']['version']
    assert restored['current']['manuscript']['version'] > original_version


def test_real_runtime_redraw_uses_declared_snapshots_and_preserves_latest_manuscript(
        package, snapshots, tmp_path, monkeypatch):
    inputs, output, saved = snapshots
    original = saved['revise_document_document'] + '\nHuman edit kept.'
    saved['revise_document_document'] = original
    bound = recovery_inputs(package, saved, tmp_path)
    source = tmp_path / 'paper_input.json'
    source.write_text(json.dumps(inputs))
    bound['paper_input'] = str(source)  # Core also rebinds the original launch input on rewind.
    forbidden = Mock(side_effect=AssertionError('Redraw re-executed analysis or body generation'))
    for name in ('make_plan', 'generate_program', 'interpret_results'):
        monkeypatch.setattr(model, name, forbidden)
    monkeypatch.setattr(pipeline.executor, 'execute', forbidden)
    classifier = Mock(return_value={'only_visual_changes': True})
    monkeypatch.setattr(model, 'call_model', classifier)
    chart = output['charts'][0]
    monkeypatch.setattr(model, 'plan_figures', Mock(return_value=dict(figures=[dict(
        title=chart['title'], purpose='Keep the observed comparison; change colors only.', style='point_interval',
        result_indices=[0], source_chart_id=chart['id'])])))

    def draw(analysis, figures, **kwargs):
        assert analysis == output['analysis'] and [item['id'] for item in figures] == [chart['id']]
        updated = dict(chart, svg=chart['svg'].replace('<rect ', '<rect fill="blue" '),
                       caption='Same single observations, new colors.')
        return [updated], [dict(id=chart['id'], caption=updated['caption'], exports={})]

    monkeypatch.setattr(plots, 'draw', draw)
    plugin, state = package
    slots = [item['material'] for item in state['steps']['data_analysis']['outputs']]
    events = []
    previous = lazyllm.globals.get('subagent_ctx')
    set_context(SubAgentContext(
        task_id='configured-redraw', conversation_id='paper-test', agent_type='workflow_step',
        objective='Only change colors', params=dict(
            step_id='data_analysis', user_input=inputs['request'],
            current_user_input='Only change colors',
            retry_hint='Recovery request for this rerun only: Only change colors', remote_inputs=bound,
            output_slot_types={slot['id']: slot['type'] for slot in plugin['slots'] if slot['id'] in slots}),
        workspace_path=str(tmp_path), input_slots=list(bound), output_slots=slots, db=None, emit=events.append))
    try:
        assert workflow.run_paper_step()['status'] == 'completed'
        artifacts = {event['slot']: event['value'] for event in events if event['type'] == 'artifact'}
        result = workflow._read_bound(artifacts['data_analysis_pool']['path'])
        current = workflow.material(result, 'charts')['content']
        manuscript = workflow.material(result, 'manuscript')['content']
        assert workflow.material(result, 'analysis')['content'] == output['analysis']
        assert manuscript == original.replace(publication.chart_uri(chart), publication.chart_uri(current[0]))
        assert Path(artifacts['redrawn_manuscript_document']['path']).read_text() == manuscript
        assert result['sequence'] == 7 and result['history'][-1]['analysis_execution'][0]['mode'] == 'redraw'
        publication.validate_chart_references(manuscript, current)
        classifier.assert_called_once()
        forbidden.assert_not_called()
    finally:
        if previous is None:
            lazyllm.globals.pop('subagent_ctx', None)
        else:
            lazyllm.globals['subagent_ctx'] = previous


@pytest.mark.parametrize('recovery', [False, True])
def test_old_graph_missing_analysis_rejects_visual_only_followup(
        snapshots, tmp_path, monkeypatch, recovery, prepared_analysis_pool):
    inputs, _, _ = snapshots
    source = tmp_path / 'paper_input.json'
    source.write_text(json.dumps(prepared_analysis_pool(inputs)))
    classifier = Mock(return_value=True)
    monkeypatch.setattr(data_analysis, 'redraw_requested', classifier)
    forbidden = Mock(side_effect=AssertionError('Missing prior analysis must not trigger fresh analysis'))
    monkeypatch.setattr(workflow, 'run_algorithm', forbidden)
    params = dict(step_id='data_analysis', remote_inputs={'experiment_pool': str(source)}, user_input=inputs['request'],
                  current_user_input='Only change colors', output_slot_types={})
    if recovery:
        params['retry_hint'] = 'Recovery request for this rerun only: Only change colors'
    context = SubAgentContext(
        task_id='legacy-redraw', conversation_id='paper-test', agent_type='workflow_step',
        objective='Only change colors', params=params, workspace_path=str(tmp_path), input_slots=['experiment_pool'],
        output_slots=[], db=None, emit=lambda _: None)
    with pytest.raises(workflow.MissingMaterials, match='无法只重画'):
        workflow._run_paper_step(context)
    classifier.assert_called_once_with('Only change colors')
    forbidden.assert_not_called()


def test_initial_analysis_does_not_add_redraw_classification(
        snapshots, tmp_path, monkeypatch, prepared_analysis_pool):
    inputs, output, _ = snapshots
    source = tmp_path / 'paper_input.json'
    source.write_text(json.dumps(prepared_analysis_pool(inputs)))
    monkeypatch.setattr(data_analysis, 'redraw_requested', Mock(side_effect=AssertionError('Fresh analysis classified')))
    monkeypatch.setattr(workflow, 'run_algorithm', Mock(return_value=output))
    context = SubAgentContext(
        task_id='initial-analysis', conversation_id='paper-test', agent_type='workflow_step',
        objective=inputs['request'], params=dict(
            step_id='data_analysis', remote_inputs={'experiment_pool': str(source)},
            user_input=inputs['request'], current_user_input=inputs['request'], output_slot_types={}),
        workspace_path=str(tmp_path), input_slots=['experiment_pool'],
        output_slots=['data_analysis_pool', 'data_analysis_document', 'analysis_chart'], db=None, emit=lambda _: None)
    # Publishing is unrelated; reaching execution proves the new guard adds no model call.

    class Executed(Exception):
        pass
    monkeypatch.setattr(workflow, 'execute_step', Mock(side_effect=Executed))
    with pytest.raises(Executed):
        workflow._run_paper_step(context)

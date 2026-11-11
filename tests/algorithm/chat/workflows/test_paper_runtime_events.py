"""Offline lifecycle contracts shared by every paper step, including multi-artifact outputs."""
import copy
import json
from pathlib import Path
from types import SimpleNamespace

import lazyllm
import pytest

from lazymind.document_tools.paper import api, contracts, research_topic, workflow
from lazymind.document_tools.paper.material_bindings import MaterialBindings
from lazymind.document_tools.paper.runtime_events import (
    bind_runtime_events, current_events, generate_markdown, StructuredPreview,
)

ROOT = Path(__file__).resolve().parents[4]
STEPS = list(workflow.STEP_NODE)


@pytest.fixture(autouse=True)
def restore_context():
    previous = lazyllm.globals.get('subagent_ctx')
    yield
    if previous is None:
        lazyllm.globals.pop('subagent_ctx', None)
    else:
        lazyllm.globals['subagent_ctx'] = previous


def test_markdown_stream_filters_reasoning_and_restores_split_citations_before_return():
    emitted = []
    body = '# 研究选题\n\n## 研究问题\n证据[P1]。'
    bindings = MaterialBindings([{'id': 'P1'}])

    def share(*, stream):
        def generate(prompt):
            stream['_stream_sink']({'tag': 'think', 'delta': 'private reasoning'})
            for delta in ['<thi', 'nk>private ', 'reasoning</think>', '# 研究选题\n\n## 研究问题\n证据', '[', 'M', '1', ']', '。']:
                stream['_stream_sink']({'tag': 'text', 'delta': delta})
            assert ''.join(e['delta'] for e in emitted if e['type'] == 'artifact_stream') == body
            return '<think>private reasoning</think>' + body.replace('[P1]', '[M1]')
        return generate

    with bind_runtime_events(emitted.append, 'research_topic_document') as events:
        assert generate_markdown(SimpleNamespace(share=share), 'prompt', bindings=bindings) == body
        events.completed_preview(body)
        events.complete()
    assert 'private reasoning' not in json.dumps(emitted)
    assert sum(e['type'] == 'artifact_stream_start' for e in emitted) == 1
    assert current_events() is None


def test_full_result_replaces_preview_once_and_can_be_withdrawn_after_generation_ends():
    emitted = []
    with pytest.raises(ValueError, match='publication rejected'):
        with bind_runtime_events(emitted.append, 'experiment_document') as events:
            events.delta('partial')
            events.completed_preview('# 完整实验报告')
            events.completed_preview('# 完整实验报告')
            raise ValueError('publication rejected')
    latest = [e for e in emitted if e.get('stream_id') == next(
        e['stream_id'] for e in reversed(emitted) if e['type'] == 'artifact_stream_start')]
    assert [e['delta'] for e in latest if e['type'] == 'artifact_stream'] == ['# 完整实验报告']
    assert latest[-1]['type'] == 'artifact_stream_abort'
    assert not any(e['type'] == 'artifact' for e in emitted)


def test_stage_failure_and_retry_keep_independent_public_records():
    emitted = []
    with bind_runtime_events(emitted.append, 'data_analysis_document') as events:
        events.progress(5, '读取材料')
        events.begin_stage('first', '执行分析', 40)
        events.begin_stage('figure', '生成图表', 50)
        events.end_stage('first', ValueError('invalid result'))
        events.begin_stage('second', '修正分析', 45)
        events.end_stage('second')
        events.end_stage('figure')
        events.complete()
    snapshots = [e for e in emitted if e['type'] == 'progress']
    assert [e['progress'] for e in snapshots] == sorted(e['progress'] for e in snapshots)
    assert max(e['progress'] for e in snapshots) == 99  # Core owns authoritative success.
    stages = {s['step_id']: s for event in snapshots for s in event['public_display']['process_steps']}
    assert stages['paper-first']['status'] == 'failed'
    assert stages['paper-second']['status'] == stages['paper-figure']['status'] == 'succeeded'
    assert stages['paper-first']['finished_at'] and stages['paper-first']['elapsed_ms'] >= 0


def test_structured_preview_replaces_revised_fields_and_does_not_emit_raw_json():
    emitted = []
    with bind_runtime_events(emitted.append, 'peer_review_document') as events:
        preview = StructuredPreview(lambda value: '# 审稿\n' + value.get('summary', ''))
        preview({'tag': 'think', 'delta': 'private'})
        preview({'tag': 'text', 'delta': '{"summary":"意见'})
        preview.flush()
        assert any(e['type'] == 'artifact_stream' for e in emitted)
        preview({'tag': 'text', 'delta': '已形成"}'})
        preview.flush()
        events.complete()
    assert 'private' not in json.dumps(emitted)
    assert all(not e['delta'].startswith('{') for e in emitted if e['type'] == 'artifact_stream')


@pytest.mark.parametrize('step', STEPS)
def test_all_steps_use_shared_progress_preview_and_real_artifact_publication(
        step, monkeypatch, tmp_path, prepared_analysis_pool):
    from lazymind.chat.engine.subagent.context import SubAgentContext
    from lazymind.document_tools.paper.review import materials as review_materials
    monkeypatch.setattr(review_materials, 'current_handoff', lambda: None)
    monkeypatch.setattr(review_materials, 'prepare', lambda inputs, *a, **kw: copy.deepcopy(inputs['papers']))
    if step == 'write_outline':
        example = {'input': {'topic': '方法比较', 'question': '如何比较两种方法？'},
                   'output': {'outline': '# 方法比较\n\n## 方法\n\n## 局限', 'is_demo': False}}
    else:
        example = json.loads((ROOT / 'tests/fixtures/paper-writing' / (step + '.json')).read_text())
    for paper in example['output'].get('papers', []):
        paper.setdefault('citation', paper['title'] + '[Z].')
    if step == 'revise_document':
        from lazymind.document_tools.paper.writing import revise_document
        monkeypatch.setattr(revise_document, 'resolve_review_issues', lambda request, review, **kwargs: [])
    value = {key: copy.deepcopy(item) for key, item in example['input'].items() if key != 'mode'}
    pool = prepared_analysis_pool(value) if step == 'data_analysis' else workflow.pool_from_business(value)
    seed = tmp_path / 'seed.json'
    seed.write_text(json.dumps(pool, ensure_ascii=False))
    module = contracts.ALIASES.get(step, step)
    monkeypatch.setattr(research_topic, 'prepare_papers', lambda papers, *args: [
        {**paper, 'abstract': paper.get('abstract') or paper.get('text') or 'Summary'} for paper in papers])

    def generate(inputs, **kwargs):
        assert current_events() is not None
        current_events().progress(35, '生成本步骤内容')
        current_events().delta('# 生成中的预览')
        return {key: copy.deepcopy(example['output'][key]) for key in ('is_demo', *contracts.OUTPUTS[module])}

    monkeypatch.setattr(api.ALGORITHMS[module], 'run', generate)
    slots = [f'{step}_pool', f'{step}_document']
    if step == 'experiment':
        slots += ['experiment_code', 'experiment_results']
    if step == 'data_analysis':
        slots += ['analysis_chart']
    emitted = []
    lazyllm.globals['subagent_ctx'] = SubAgentContext(
        task_id=step, conversation_id='test', agent_type='workflow_step', objective='Test lifecycle',
        workspace_path=str(tmp_path), input_slots=['seed_pool'], output_slots=slots, db=None, emit=emitted.append,
        params={'step_id': step, 'current_user_input': example['input'].get('request') or '按本轮要求完善内容',
                'user_input': example['input'].get('request') or '按本轮要求完善内容',
                'remote_inputs': {'seed_pool': str(seed)},
                'output_slot_types': dict.fromkeys(slots, 'file')},
    )
    assert workflow.run_paper_step()['status'] == 'completed'
    document = next(e for e in emitted if e['type'] == 'artifact' and e['slot'] == f'{step}_document')
    saved = Path(document['value']['path']).read_text()
    latest_stream = next(e['stream_id'] for e in reversed(emitted)
                         if e['type'] == 'artifact_stream_start' and e['slot'] == f'{step}_document')
    preview = ''.join(e['delta'] for e in emitted if e['type'] == 'artifact_stream' and e['stream_id'] == latest_stream)
    assert preview == saved
    assert any(e['type'] == 'progress' and e.get('public_display') for e in emitted)
    assert current_events() is None


def test_finish_publishes_whole_document_without_a_model_call(tmp_path):
    from lazymind.chat.engine.subagent.context import SubAgentContext
    emitted = []
    lazyllm.globals['subagent_ctx'] = SubAgentContext(
        task_id='finish', conversation_id='test', agent_type='workflow_step', objective='Finish',
        workspace_path=str(tmp_path), input_slots=[], output_slots=['finish_document'], db=None, emit=emitted.append,
        params={'step_id': 'finish', 'output_slot_types': {'finish_document': 'file'}},
    )
    workflow.run_paper_step()
    assert len([e for e in emitted if e['type'] == 'artifact_stream']) == 1
    assert any(e['type'] == 'artifact' for e in emitted)


def test_revision_clarification_waits_without_publishing_or_reporting_success(tmp_path, monkeypatch):
    from lazymind.chat.engine.subagent.context import SubAgentContext
    from lazymind.document_tools.paper.writing.revise_document import RevisionInputRequired
    question = '请确认采用哪些审稿意见'

    def clarify(context, stream):
        current_events().progress(7, '正在确定修订范围')
        raise RevisionInputRequired(question)
    monkeypatch.setattr(workflow, '_run_paper_step', clarify)
    emitted = []
    lazyllm.globals['subagent_ctx'] = SubAgentContext(
        task_id='revision', conversation_id='test', agent_type='workflow_step', objective='Revise',
        workspace_path=str(tmp_path), input_slots=[], output_slots=['revise_document_document'],
        db=None, emit=emitted.append,
        params={'step_id': 'revise_document', 'current_user_input': '继续，修订论文'},
    )
    result = workflow.run_paper_step()
    assert result['status'] == 'needs_input'
    assert result['control']['awaiting_input'] == {
        'question': question, 'request_sha256': workflow.text_digest('继续，修订论文'),
    }
    assert result['_agent_control']['stop'] is True
    assert not any(e['type'] == 'artifact' for e in emitted)
    progress = [e for e in emitted if e['type'] == 'progress']
    assert max(e['progress'] for e in progress) == 7
    assert question in progress[-1]['current_phase']
    assert all(s['status'] != 'failed' for e in progress for s in e['public_display']['process_steps'])
    assert current_events() is None


def test_review_projects_partial_results_while_preserving_strict_review_validation(tmp_path, monkeypatch):
    from lazymind.document_tools.paper import peer_review
    example = json.loads((ROOT / 'tests/fixtures/paper-writing/peer_review.json').read_text())
    emitted = []

    class Reviewer:
        def _structured_output_prompt(self, schema):
            return ''

        def _build_structured_llm(self, prompt, *, stream_output, apply_formatter):
            def generate(_):
                raw = json.dumps(example['model_response'], ensure_ascii=False)
                stream_output['_stream_sink']({'tag': 'text', 'delta': raw})
                assert any(e['type'] == 'artifact_stream' for e in emitted)
                return raw
            return generate

    with bind_runtime_events(emitted.append, 'peer_review_document') as events:
        peer_review.call_once(Reviewer(), 'prompt', peer_review.ReviewResult)
        events.complete()
    assert all(not e['delta'].startswith('{') for e in emitted if e['type'] == 'artifact_stream')
    assert any(s['status'] == 'succeeded' for e in emitted if e['type'] == 'progress'
               for s in e['public_display']['process_steps'])


def test_f4_keeps_diagnostics_and_projects_interpretation_to_shared_document_stream(tmp_path):
    from lazymind.document_tools.paper.analysis import trace
    emitted = []
    value = '结果仅适用于当前样本，仍需补充验证。'
    with bind_runtime_events(emitted.append, 'data_analysis_document') as events:
        with trace.capture(tmp_path, 'analysis', emitted.append):
            with trace.stage('interpretation'):
                preview = trace.response_preview(False, output_format='markdown')
                preview.sink({'tag': 'text', 'delta': value})
                assert any(e['type'] == 'artifact_stream' for e in emitted)
                preview.finish(value)
        events.complete()
    assert list(tmp_path.glob('f4-trace/*/public.json'))
    assert any(e.get('slot') == 'data_analysis_document' for e in emitted)


def test_f4_parallel_stages_and_retry_reach_shared_execution_log(tmp_path):
    from threading import Barrier
    from lazymind.document_tools.paper.analysis import trace
    from lazymind.document_tools.paper.execution.threads import ThreadPoolExecutor

    emitted = []
    barrier = Barrier(2)

    def draw(figure_id):
        with trace.stage('figure_execution', figure_id=figure_id, attempt=1):
            barrier.wait(timeout=5)
            with trace.stage('model_request'):
                if figure_id == 'fig2':
                    raise ValueError('private diagnostic detail')

    with bind_runtime_events(emitted.append, 'data_analysis_document') as events:
        with trace.capture(tmp_path, 'analysis', emitted.append) as recorder:
            with ThreadPoolExecutor(max_workers=2) as executor:
                first = executor.submit(draw, 'fig1')
                second = executor.submit(draw, 'fig2')
                first.result(timeout=10)
                with pytest.raises(ValueError, match='private diagnostic detail'):
                    second.result(timeout=10)
            with trace.stage('figure_execution', figure_id='fig2', attempt=2):
                pass
        events.complete()

    phases = [e for e in emitted if e['type'] == 'progress']
    shared = {}
    for event in phases:
        for step in event.get('public_display', {}).get('process_steps', []):
            shared.setdefault(step['step_id'], []).append(step)
    assert len(shared) == 4  # Three drawing attempts and the final publication phase.
    for stage in recorder.summary['stages']:
        key = 'paper-' + stage['id']
        if stage['stage'] == 'model_request':
            assert key not in shared  # Internal model calls stay in F4 diagnostics.
            continue
        records = shared[key]
        assert [item['status'] for item in records] == [
            'running', 'failed' if stage['status'] == 'failed' else 'succeeded']
        assert records[-1]['finished_at'] and records[-1]['elapsed_ms'] >= 0
        title = f"执行绘图代码 · 第 {stage['attempt']} 次 · {stage['figure_id']}"
        assert title + '\n' in [e['text'] for e in emitted if e['type'] == 'text']
        assert records[-1]['title'] == title
    assert [e['progress'] for e in phases] == sorted(e['progress'] for e in phases)
    assert max(e['progress'] for e in phases) == 99
    public = json.loads((recorder.root / 'public.json').read_text())
    assert len(public['process_steps']) == 3
    assert 'failed' in {step['status'] for step in public['process_steps']}
    assert 'private diagnostic detail' not in json.dumps(emitted)


def test_f4_without_shared_runtime_keeps_legacy_progress(tmp_path):
    from lazymind.document_tools.paper.analysis import trace

    emitted = []
    with trace.capture(tmp_path, 'standalone', emitted.append):
        with trace.stage('figure_execution', figure_id='fig1', attempt=1):
            pass
    phases = [e for e in emitted if e['type'] == 'progress']
    assert len(phases) == 1
    assert phases[0]['current_phase'] == 'F4 · 执行绘图代码 · 第 1 次 · fig1'


def test_f4_progress_counts_estimated_tokens_without_changing_output(tmp_path, monkeypatch):
    from lazymind.common.token_estimation import estimate_tokens
    from lazymind.document_tools.paper.analysis import progress_routes, trace

    task_id = 'test-token-display'
    monkeypatch.setattr(progress_routes, 'WORKSPACES', tmp_path)
    body, reasoning = 'The observed mean is 2. 本次结果仅适用于模拟样本。', 'Compare the observed conditions.'
    with trace.capture(tmp_path / 'user' / task_id, task_id, emit=lambda _: None):
        with trace.stage('interpretation'):
            preview = trace.response_preview(False, output_format='markdown')
            preview.finish(dict(content=body, reasoning_content=reasoning))
    snapshot = progress_routes.read_snapshot(task_id, dict(status='succeeded'))
    step = snapshot['process_steps'][0]
    for channel, text in [('output', body), ('reasoning', reasoning)]:
        assert step[channel]['text'] == text
        assert step[channel]['estimated_tokens'] == estimate_tokens(text)
        assert step[channel]['estimated_tokens'] != len(text)


@pytest.mark.parametrize('wrapped', [False, True])
def test_topic_body_response_and_numbered_references_match_saved_preview(monkeypatch, wrapped):
    identifier = 'upload-' + 'a' * 64
    papers = [dict(id=identifier, title='Study notes', filename='study-notes.md',
                   attachment_key='uploaded', abstract='Uploaded findings.', text='Source text.'),
              dict(id='P2', title='Prior work', citation='Author. Prior work. 2024.',
                   abstract='Previous findings.', text='Prior work text.')]
    before = copy.deepcopy(papers)
    body = '# 研究选题\n\n## 研究问题\n比较证据[M1]与[M2]，再核对[M1]。'

    class Model:
        def share(self, *, stream):
            def call(prompt):
                for delta in ['# 研究选题\n\n## 研究问题\n比较证据', '[M', '1]', '与[M2]，再核对[M1]。']:
                    stream['_stream_sink']({'tag': 'text', 'delta': delta})
                return {'content': body, 'reasoning_content': 'PRIVATE_REASONING'} if wrapped else body
            return call

    monkeypatch.setattr(research_topic, 'AutoModel', lambda **kwargs: Model())
    monkeypatch.setattr(research_topic, '_prompt_token_limit', lambda: 100000)
    emitted = []
    with bind_runtime_events(emitted.append, 'research_topic_document') as events:
        result = api.run_algorithm('research_topic', {'topic': '研究主题', 'papers': papers})
        events.complete()
    saved = result['question']
    assert saved.count(rf'[\[1\]](#paper-ref-{identifier})') == 2
    assert r'[\[2\]](#paper-ref-P2)' in saved
    assert saved.count('## 参考文献') == 1
    assert '[1] study-notes.md（用户上传材料）' in saved
    assert '[2] Author. Prior work. 2024.' in saved
    assert f'[{identifier}]' not in saved
    assert 'PRIVATE_REASONING' not in json.dumps(emitted, ensure_ascii=False)
    latest = next(e['stream_id'] for e in reversed(emitted) if e['type'] == 'artifact_stream_start')
    assert ''.join(e['delta'] for e in emitted if e.get('stream_id') == latest
                   and e['type'] == 'artifact_stream') == saved
    assert not any(f'[{identifier}]' in e.get('delta', '') for e in emitted)
    assert papers == before


@pytest.mark.parametrize('wrapped', [False, True])
def test_topic_inference_and_summary_extract_body_without_reasoning(monkeypatch, wrapped):
    monkeypatch.setattr(research_topic, '_prompt_token_limit', lambda: 100000)
    raw = '{"topic":"证据追溯"}'
    monkeypatch.setattr(research_topic, 'AutoModel', lambda **kwargs: lambda prompt:
                        {'content': raw, 'reasoning_content': 'PRIVATE'} if wrapped else raw)
    assert research_topic.infer_topic('根据附件选题', []) == '证据追溯'
    raw = '提炼出的材料正文。'
    assert research_topic._condense('材料', {'id': 'P1', 'title': '材料'}, 100000) == raw


@pytest.mark.parametrize('raw', ['', {'content': '', 'reasoning_content': '{"topic":"假的主题"}'},
                                 {'reasoning_content': '{"topic":"假的主题"}'},
                                 {'content': '{broken', 'reasoning_content': '{"topic":"假的主题"}'}])
def test_topic_inference_keeps_empty_and_invalid_json_failures(monkeypatch, raw):
    monkeypatch.setattr(research_topic, '_prompt_token_limit', lambda: 100000)
    monkeypatch.setattr(research_topic, 'AutoModel', lambda **kwargs: lambda prompt: raw)
    with pytest.raises((ValueError, RuntimeError)):
        research_topic.infer_topic('主题', [])


def test_real_topic_step_publishes_numbered_document_and_pool(monkeypatch, tmp_path):
    from lazymind.chat.engine.subagent.context import SubAgentContext
    identifier = 'upload-' + 'a' * 64
    papers = [dict(id=identifier, title='研究记录', filename='研究记录.md', attachment_key='upload',
                   text='研究材料。', abstract='材料摘要。')]
    pool = workflow.pool_from_business({'topic': '证据追溯', 'papers': papers})
    seed = tmp_path / 'seed.json'
    seed.write_text(json.dumps(pool, ensure_ascii=False))
    monkeypatch.setattr(research_topic, '_prompt_token_limit', lambda: 100000)
    monkeypatch.setattr(research_topic, 'AutoModel', lambda **kwargs: lambda prompt: {
        'content': '# 研究选题\n\n## 研究问题\n如何核验已有材料[M1]？',
        'reasoning_content': 'PRIVATE_REASONING'})
    emitted = []
    slots = ['research_topic_pool', 'research_topic_document']
    lazyllm.globals['subagent_ctx'] = SubAgentContext(
        task_id='topic-response', conversation_id='test', agent_type='workflow_step', objective='Research',
        workspace_path=str(tmp_path), input_slots=['seed_pool'], output_slots=slots, db=None, emit=emitted.append,
        params={'step_id': 'research_topic', 'current_user_input': '根据材料确定选题',
                'user_input': '根据材料确定选题', 'remote_inputs': {'seed_pool': str(seed)},
                'output_slot_types': dict.fromkeys(slots, 'file')})
    assert workflow.run_paper_step()['status'] == 'completed'
    artifacts = {e['slot']: e['value']['path'] for e in emitted if e['type'] == 'artifact'}
    assert set(artifacts) == set(slots)
    saved = Path(artifacts['research_topic_document']).read_text()
    published = workflow._read_bound(artifacts['research_topic_pool'])
    assert workflow.material(published, 'question')['content'] == saved
    assert rf'[\[1\]](#paper-ref-{identifier})' in saved
    assert '[1] 研究记录.md（用户上传材料）' in saved
    assert 'PRIVATE_REASONING' not in saved
    assert workflow.material(published, 'papers')['content'] == papers
    latest = next(e['stream_id'] for e in reversed(emitted) if e['type'] == 'artifact_stream_start')
    assert ''.join(e['delta'] for e in emitted if e.get('stream_id') == latest
                   and e['type'] == 'artifact_stream') == saved

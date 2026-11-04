import copy
import json
import re
import sys
from pathlib import Path
from types import ModuleType, SimpleNamespace

import pytest
import yaml

ROOT = Path(__file__).resolve().parents[4]
PACKAGE = ROOT / 'workflows/paper-writing'
from lazymind.document_tools.paper import api
from lazymind.document_tools.paper import workflow as tools

pytestmark = pytest.mark.usefixtures('paper_simulation')


def draft_pool():
    pool = tools.seed_pool()
    pool['current'].pop('manuscript', None)
    return pool


@pytest.fixture(autouse=True)
def mock_f1_model(monkeypatch):
    from lazymind.document_tools.paper.review import materials as review_materials
    monkeypatch.setattr(review_materials, 'prepare', lambda inputs, *_args, **_kwargs: inputs.get('papers', []))
    monkeypatch.setattr(review_materials, 'current_handoff', lambda: None)

    def model(prompt):
        if '"paper_clues"' in prompt:
            return '{"topic": "证据可追溯性"}'
        if 'Input materials (JSON):\n' not in prompt:
            return '论文讨论证据追溯的方法与局限。'
        suffix = '\n\n## 文献反馈与调整依据\n根据综述调整验证路线。' if '"mode": "refine"' in prompt else ''
        return '# 研究选题\n\n## 研究问题\n现有方法能否改善效果？' + suffix

    monkeypatch.setattr(api.ALGORITHMS['research_topic'], 'AutoModel', lambda **_kwargs: model)


@pytest.mark.parametrize('path', [
    ['F1', 'F2', 'F1', 'F2'],
    ['F2', 'F5', 'F6', 'F5', 'F6'],
    ['F4', 'F5'], ['F7'], ['F2', 'F7'],
    ['F2', 'F3', 'F4'], *[[f'F{i}'] for i in range(1, 8)],
])
def test_scenarios_preserve_sources_versions_and_inputs(path, reflection_calls):
    pool = tools.seed_pool()
    original = copy.deepcopy(pool)
    for node in path:
        if node == 'F5':
            if pool['history'] and pool['history'][-1]['node'] == 'F6':
                pool['selected_issue_ids'] = ['R1']
            else:
                pool['current'].pop('manuscript', None)
        if node == 'F4' and not any(item['node'] == 'F3' for item in pool['history']):
            pool['current'].pop('experiment', None)  # Select raw data, not the unexecuted F3 demo.
        before = copy.deepcopy(pool)
        result = tools.execute_mock(node, pool, '给导师汇报')
        assert pool == before
        pool = result['pool']
        assert result['document'].startswith('# ')
        assert any(label in result['document'] for label in ('演示数据', '模拟示例'))
        assert 'MOCK' not in result['document']
        assert pool['materials'][:len(before['materials'])] == before['materials']
        assert result['next'] == tools.routes_after(result['pool']) + ['END']
    assert pool['sequence'] == len(path)
    assert original['materials'] == pool['materials'][:len(original['materials'])]


@pytest.mark.parametrize('start,feedback', [('F2', 'F1'), ('F6', 'F5')])
def test_ten_feedback_rounds_from_direct_entry(start, feedback):
    pool = tools.execute_mock(start, tools.seed_pool())['pool']
    for _ in range(10):
        if feedback == 'F5':
            pool['selected_issue_ids'] = ['R1']
        pool = tools.execute_mock(feedback, pool)['pool']
        before = copy.deepcopy(pool)
        pool = tools.execute_mock(start, pool)['pool']
        assert pool['materials'][:len(before['materials'])] == before['materials']
        kind = 'question' if start == 'F2' else 'manuscript'
        assert before['current'][kind] in pool['history'][-1]['inputs']
        assert tools.material(pool, 'evidence' if start == 'F2' else 'review')['content']


@pytest.mark.parametrize('count', [1, 2])
def test_single_multiple_paper_reflection(count, reflection_calls):
    pool = tools.seed_pool()
    tools.material(pool, 'papers')['content'] = tools.material(pool, 'papers')['content'][:count]
    result = tools.execute_mock('F7', pool)
    papers = tools.material(pool, 'papers')['content']
    assert reflection_calls[0]['papers'] == [
        {'id': paper['id'], 'title': paper['title'], 'text': paper['text'], 'source_kind': 'partial_text'}
        for paper in papers
    ]
    assert all(paper['id'] in result['document'] for paper in papers)


def test_edited_artifact_is_imported_as_new_version_and_used_for_review():
    pool = tools.execute_mock('F5', draft_pool())['pool']
    before = copy.deepcopy(pool)
    edited = '# 用户确认的修订稿\n保留该句'
    selected = tools.pool_from_inputs({'write_document_pool': pool, 'write_document_document': edited})
    assert pool == before
    assert tools.material(selected, 'manuscript')['content'] == edited
    assert selected['current']['manuscript']['version'] == pool['current']['manuscript']['version'] + 1
    result = tools.execute_mock('F6', selected)
    assert selected['current']['manuscript'] in result['pool']['history'][-1]['inputs']


def test_feedback_snapshot_wins_over_old_upstream_pool():
    seed = tools.seed_pool()
    reviewed = tools.execute_mock('F6', seed)['pool']
    reviewed['selected_issue_ids'] = ['R1']
    revised = tools.execute_mock('F5', reviewed)['pool']
    selected = tools.pool_from_inputs({'seed_pool': seed, 'recovery_review_pool': revised,
                                      'recovery_review_document': '# 修改后的正文'})
    assert selected['sequence'] == 2
    assert tools.material(selected, 'manuscript')['content'] == '# 修改后的正文'
    assert tools.execute_mock('F6', selected)['pool']['sequence'] == 3


@pytest.mark.parametrize('node,kind', [('F4', 'data'), ('F6', 'manuscript'), ('F7', 'papers')])
def test_missing_input_fails_without_mutation_then_can_resume(node, kind, reflection_calls):
    pool = tools.seed_pool()
    ref = pool['current'].pop(kind)
    before = copy.deepcopy(pool)
    with pytest.raises(tools.MissingMaterials, match='缺少'):
        tools.execute_mock(node, pool)
    assert not reflection_calls
    assert pool == before
    pool['current'][kind] = ref
    assert tools.execute_mock(node, pool)['status'] == 'completed'


@pytest.mark.parametrize('corrupt', [
    lambda p: p.update(schema='invalid'),
    lambda p: p.update(sequence=True),
    lambda p: p.update(sequence=5),
    lambda p: p['current']['data'].update(version=999),
    lambda p: p['materials'].append(copy.deepcopy(p['materials'][0])),
    lambda p: p['materials'][0].update(mock='false'),
    lambda p: p['materials'][0].update(content='not papers'),
    lambda p: p['history'].append({'node': 'F2', 'inputs': 'bad', 'outputs': []}),
])
def test_invalid_input_rejected(corrupt):
    pool = tools.seed_pool()
    corrupt(pool)
    with pytest.raises(ValueError):
        tools.execute_mock('F2', pool)


@pytest.mark.parametrize('a,b', [('F1','F3'), ('F1','F5'), ('F3','F5'), ('F6','F2')])
def test_forbidden_execution_edges(a, b):
    pool = tools.execute_mock(a, tools.seed_pool())['pool']
    with pytest.raises(ValueError, match='不允许'):
        tools.execute_mock(b, pool)


def test_failed_or_invalid_algorithm_does_not_publish_or_change_input(monkeypatch):
    pool = draft_pool()
    before = copy.deepcopy(pool)
    with pytest.raises(RuntimeError, match='模拟执行失败'):
        tools.execute_mock('F5', pool, fail=True)
    assert pool == before
    assert tools.execute_mock('F5', pool)['status'] == 'completed'
    monkeypatch.setattr(api.ALGORITHMS['write_document'], 'run', lambda inputs: {'manuscript': 'x'})
    with pytest.raises(ValueError, match='算法输出字段'):
        tools.execute_mock('F5', pool)
    assert pool == before


def test_algorithm_cannot_publish_wrong_material_kind(monkeypatch):
    monkeypatch.setattr(api.ALGORITHMS['write_document'], 'run', lambda inputs: {'review': 'wrong output', 'is_demo': False})
    with pytest.raises(ValueError, match='算法输出字段'):
        tools.execute_mock('F5', draft_pool())


def test_edited_experiment_report_keeps_simulated_result_and_history():
    pool = tools.execute_mock('F3', tools.seed_pool())['pool']
    selected = tools.pool_from_inputs({'experiment_pool': pool, 'experiment_document': '# 用户修改的实验方案'})
    experiment = tools.material(selected, 'experiment')
    assert experiment['content']['report'] == '# 用户修改的实验方案'
    assert experiment['content']['executed'] is False
    assert experiment['version'] == 2
    assert tools.execute_mock('F4', selected)['status'] == 'completed'


@pytest.fixture
def runtime(monkeypatch, tmp_path):
    ctx = SimpleNamespace(task_id='paper-runtime', params={}, workspace_path=str(tmp_path), emit=lambda event: None, output_slots=[])
    batches = []
    context_module = ModuleType('lazymind.chat.engine.subagent.context')
    context_module.require_context = lambda: ctx
    artifact_module = ModuleType('lazymind.chat.engine.subagent.tools')
    artifact_module.save_artifacts = lambda items: batches.append(copy.deepcopy(items)) or {'status': 'ok'}
    monkeypatch.setitem(sys.modules, context_module.__name__, context_module)
    monkeypatch.setitem(sys.modules, artifact_module.__name__, artifact_module)
    monkeypatch.setattr(tools, '_load_f7_handoff', lambda: None)
    return ctx, batches


def test_runtime_wrapper_uses_host_inputs_and_saves_all_declared_outputs(runtime, tmp_path, reflection_calls):
    ctx, batches = runtime
    workflow = yaml.safe_load((PACKAGE / 'workflow.yaml').read_text())
    state = yaml.safe_load((PACKAGE / 'scenario/state.yml').read_text())
    assert workflow['runtime']['transactional_outputs']
    ctx.params = {'step_id': 'prepare', 'user_input': '[paper:demo]'}
    tools.run_paper_step()
    seed = tools._read_bound(batches[-1][0]['value'])
    path = tmp_path / 'seed.json'
    path.write_text(json.dumps({'data': seed}))
    for step in ['research_topic', 'literature_review', 'experiment', 'data_analysis', 'write_document', 'peer_review', 'reading_reflection']:
        selected_pool = experiment_pool if step == 'data_analysis' else str(path)
        if step == 'write_document':
            writing = copy.deepcopy(seed)
            writing['current'].pop('manuscript', None)
            writing_path = tmp_path / 'writing-seed.json'
            writing_path.write_text(json.dumps(writing))
            selected_pool = str(writing_path)
        ctx.params = {'step_id': step, 'remote_inputs': {'seed_pool': selected_pool}}
        ctx.output_slots = [item['material'] for item in state['steps'][step]['outputs']]
        tools.run_paper_step()
        actual = {item['key'] for item in batches[-1]}
        declared = {item['material'] for item in state['steps'][step]['outputs']}
        required = {item['material'] for item in state['steps'][step]['outputs'] if item.get('required', True)}
        assert required <= actual <= declared
        document = next(item for item in batches[-1] if item['key'].endswith('_document'))
        assert document['content_type'] == 'file'
        assert Path(document['value']).name == tools.STEP_LABELS[step] + '.md'
        assert any(label in tools._read_bound(document['value']) for label in ('演示数据', '模拟示例'))
        if step == 'experiment':
            experiment_pool = next(item['value'] for item in batches[-1] if item['key'] == 'experiment_pool')
    ctx.params = {'step_id': 'finish'}
    tools.run_paper_step()
    assert batches[-1][0]['key'] == 'finish_document'


def test_runtime_failure_retry_and_bad_external_input_never_fallback(runtime, tmp_path):
    ctx, batches = runtime
    path = tmp_path / 'pool.json'
    path.write_text(json.dumps(draft_pool()))
    ctx.params = {'step_id': 'write_document', 'remote_inputs': {'seed_pool': str(path)}, 'current_user_input': '[mock:fail]'}
    with pytest.raises(RuntimeError):
        tools.run_paper_step()
    assert not batches
    ctx.params['runtime_instruction'] = '[mock:retry]'
    tools.run_paper_step()
    assert len(batches) == 1
    path.write_text('{}')
    ctx.params = {'step_id': 'prepare', 'remote_inputs': {'paper_input': str(path)}}
    with pytest.raises(ValueError):
        tools.run_paper_step()
    assert len(batches) == 1


@pytest.mark.parametrize('user_input', ['根据两篇论文研究证据追溯', ''])
def test_f1_direct_entry_uses_conversation_pdfs_and_publishes_pool(user_input, runtime, monkeypatch):
    ctx, batches = runtime
    papers = [{'id': 'P1', 'title': '论文一', 'text': '<!-- page:1 -->\n证据一'},
              {'id': 'P2', 'title': '论文二', 'text': '<!-- page:1 -->\n证据二'}]
    monkeypatch.setattr(tools, '_conversation_papers', lambda history: papers if history else [])
    ctx.params = {'step_id': 'research_topic', 'user_input': user_input,
                  'history_files_per_turn': {'1': ['/uploads/论文一.pdf'], '2': ['/uploads/论文二.pdf']}}
    tools.run_paper_step()
    outputs = {item['key']: item['value'] for item in batches[-1]}
    pool = tools._read_bound(outputs['research_topic_pool'])
    assert pool['topic'] == '证据可追溯性'
    extracted = tools.material(pool, 'papers')['content']
    assert [p['id'] for p in extracted] == [p['id'] for p in papers]
    assert all(p['text'] in e['text'] and e['extraction_version'] == 1 for p, e in zip(papers, extracted))
    assert pool['history'][0]['node'] == 'F1'
    assert '## 研究问题' in tools._read_bound(outputs['research_topic_document'])


def test_f1_direct_entry_without_pdfs_creates_unverified_question(runtime):
    ctx, batches = runtime
    ctx.params = {'step_id': 'research_topic', 'user_input': '研究生成答案的证据可追溯性'}
    tools.run_paper_step()
    outputs = {item['key']: item['value'] for item in batches[-1]}
    pool = tools._read_bound(outputs['research_topic_pool'])
    assert pool['topic'] == '证据可追溯性'
    assert tools.material(pool, 'papers') is None
    assert '## 研究问题' in tools._read_bound(outputs['research_topic_document'])


def test_f1_direct_demo_without_pdfs_keeps_upstream_demo(runtime, monkeypatch):
    ctx, batches = runtime
    monkeypatch.setattr(tools.research_topic, 'infer_topic', lambda *_args: pytest.fail('demo topic was inferred'))
    ctx.params = {'step_id': 'research_topic', 'user_input': '[paper:demo]'}
    tools.run_paper_step()
    outputs = {item['key']: item['value'] for item in batches[-1]}
    pool = tools._read_bound(outputs['research_topic_pool'])
    assert tools.material(pool, 'papers')['mock'] is True
    assert '演示数据' in tools._read_bound(outputs['research_topic_document'])


def test_f1_real_pdf_overrides_demo_marker(runtime, monkeypatch):
    ctx, batches = runtime
    monkeypatch.setattr(tools, '_conversation_papers', lambda _history: [
        {'id': 'P1', 'title': '论文一', 'text': '真实论文正文'},
    ])
    ctx.params = {'step_id': 'research_topic', 'user_input': '[paper:demo] 根据上传论文选题',
                  'history_files_per_turn': {'1': ['/uploads/论文一.pdf']}}
    tools.run_paper_step()
    outputs = {item['key']: item['value'] for item in batches[-1]}
    pool = tools._read_bound(outputs['research_topic_pool'])
    assert tools.material(pool, 'papers')['mock'] is False
    assert tools.material(pool, 'papers')['content'][0]['id'] == 'P1'
    assert '演示数据' not in tools._read_bound(outputs['research_topic_document'])


@pytest.fixture
def reflection_calls(monkeypatch):
    calls = []

    def model(prompt):
        inputs = json.loads(prompt.split('Input materials (JSON):\n', 1)[1])
        calls.append(inputs)
        return '# 阅读心得\n\n' + '、'.join(paper['id'] for paper in inputs['papers'])

    monkeypatch.setattr(api.ALGORITHMS['reading_reflection'], 'AutoModel', lambda **_: model)
    return calls


@pytest.mark.parametrize('with_notes', [False, True])
@pytest.mark.parametrize('topic', ['', '用户指定方向'])
@pytest.mark.parametrize('original_request,current_input,expected_request', [
    ('比较实验设置', None, '比较实验设置'),
    ('比较实验设置', '重点比较实验局限', '比较实验设置\n\n本轮补充要求（优先于上述要求）：\n重点比较实验局限'),
    ('比较实验设置', '重试', '比较实验设置'),
    ('', None, ''),
    ('', '重点比较实验局限', '重点比较实验局限'),
])
def test_f7_library_entry_uses_frozen_selection_and_current_request(
    with_notes, topic, original_request, current_input, expected_request,
    runtime, reflection_calls, monkeypatch, tmp_path,
):
    ctx, batches = runtime
    refs = [{'paper_id': 'kb-1', 'title': '第一篇', 'abstract': None, 'doi': '10.1/test'},
            {'paper_id': 'kb-2', 'title': '第二篇', 'abstract': '原始摘要'}]
    handoff = {'kind': 'reflection', 'input': {'papers': refs, 'user_instruction': original_request}}
    notes = [{'id': 'n-1', 'paper_id': 'kb-1', 'title': '第一篇笔记', 'content': '需要核实实验对照。'},
             {'id': 'n-2', 'paper_id': 'kb-2', 'title': '第二篇笔记', 'content': '关注方法的适用条件。'}]
    if with_notes:
        handoff['input']['notes'] = notes
    before = copy.deepcopy(handoff)
    monkeypatch.setattr(tools, '_load_f7_handoff', lambda: handoff)
    monkeypatch.setattr(tools, '_conversation_papers', lambda *_: pytest.fail('history was scanned'))
    reads = []

    def read(paper_id):
        reads.append(paper_id)
        return {'status': 'ready', 'text': '完整正文'} if paper_id == 'kb-1' else {'status': 'unavailable'}

    monkeypatch.setattr(tools, '_read_f7_paper', read)
    bound = {}
    if topic:
        path = tmp_path / 'topic.txt'
        path.write_text(topic)
        bound['paper_topic'] = str(path)
    ctx.params = {'step_id': 'reading_reflection', 'remote_inputs': bound,
                  'user_input': 'Injected launch context: use paper-writing',
                  'launch_user_input': 'Injected launch context: use paper-writing',
                  'current_user_input': 'Injected launch context: use paper-writing' if current_input is None
                  else current_input,
                  'history_files_per_turn': {'1': ['/uploads/unselected.pdf']}}
    params_before = copy.deepcopy(ctx.params)
    tools.run_paper_step()
    outputs = {item['key']: item['value'] for item in batches[-1]}
    pool = tools._read_bound(outputs['reading_reflection_pool'])
    selected = tools.material(pool, 'papers')['content']
    assert reads == ['kb-1', 'kb-2']
    expected_papers = [{**refs[0], 'id': 'kb-1', 'text': '完整正文'}, {**refs[1], 'id': 'kb-2'}]
    if with_notes:
        expected_papers[0]['notes'] = [notes[0]]
        expected_papers[1]['notes'] = [notes[1]]
    assert selected == expected_papers
    assert pool['topic'] == (topic or '第一篇；第二篇')
    assert pool['request'] == original_request
    assert pool['history'][-1]['request'] == reflection_calls[0]['request'] == expected_request
    assert reflection_calls[0]['papers'] == [
        {'id': 'kb-1', 'title': '第一篇', 'text': '完整正文', 'source_kind': 'full_text',
         **({'notes': [notes[0]]} if with_notes else {})},
        {'id': 'kb-2', 'title': '第二篇', 'abstract': '原始摘要', 'source_kind': 'abstract',
         **({'notes': [notes[1]]} if with_notes else {})},
    ]
    assert tools._read_bound(outputs['reading_reflection_document']) == '# 阅读心得\n\nkb-1、kb-2'
    assert handoff == before
    assert ctx.params == params_before


@pytest.mark.parametrize('source', ['seed_pool', 'paper_input'])
def test_f7_explicit_source_does_not_query_library(source, runtime, reflection_calls, monkeypatch, tmp_path):
    ctx, _ = runtime
    value = {'topic': '明确主题', 'papers': [{'id': 'P1', 'title': '明确选文', 'abstract': '摘要'}]}
    path = tmp_path / 'input.json'
    path.write_text(json.dumps(tools.pool_from_business(value) if source == 'seed_pool' else value))
    monkeypatch.setattr(tools, '_load_f7_handoff', lambda: pytest.fail('library was queried'))
    ctx.params = {'step_id': 'reading_reflection', 'remote_inputs': {source: str(path)}}
    tools.run_paper_step()
    assert reflection_calls[0]['papers'] == [{**value['papers'][0], 'source_kind': 'abstract'}]


@pytest.mark.parametrize('reference', [
    {'paper_id': '', 'title': '标题'}, {'paper_id': 'kb-1', 'title': ''},
    {'paper_id': 'invalid/id', 'title': '标题'},
])
def test_f7_invalid_library_identity_stops_before_reading(reference, runtime, reflection_calls, monkeypatch):
    ctx, batches = runtime
    monkeypatch.setattr(tools, '_load_f7_handoff', lambda: {
        'kind': 'reflection', 'input': {'papers': [reference], 'user_instruction': ''},
    })
    monkeypatch.setattr(tools, '_read_f7_paper', lambda *_: pytest.fail('invalid identity was read'))
    ctx.params = {'step_id': 'reading_reflection'}
    with pytest.raises(ValueError, match='paper\\.'):
        tools.run_paper_step()
    assert not batches and not reflection_calls


@pytest.mark.parametrize('status, code, absent', [
    (404, 'PAPER_TASK_NOT_FOUND', True), (404, 'NOT_FOUND', False),
    (403, 'FORBIDDEN', False), (500, 'READ_FAILED', False),
])
def test_f7_optional_handoff_only_accepts_explicit_absence(status, code, absent, monkeypatch):
    import httpx

    response = httpx.Response(status, json={'code': code}, request=httpx.Request('GET', 'http://core/context'))

    def context():
        response.raise_for_status()

    monkeypatch.setitem(sys.modules, 'lazymind.chat.service.academic_handoff',
                        SimpleNamespace(academic_handoff_context=context))
    if absent:
        assert tools._load_f7_handoff() is None
    else:
        with pytest.raises(httpx.HTTPStatusError):
            tools._load_f7_handoff()


@pytest.mark.parametrize('kind', ['reflection', 'expansion', 'review', 'ppt'])
def test_f7_handoff_lookup_only_selects_reflection(kind, monkeypatch):
    handoff = {'kind': kind}
    monkeypatch.setitem(sys.modules, 'lazymind.chat.service.academic_handoff',
                        SimpleNamespace(academic_handoff_context=lambda: handoff))
    assert tools._load_f7_handoff() == (handoff if kind == 'reflection' else None)


@pytest.mark.parametrize('failure', ['', 'network', 'processing', 'empty'])
def test_f7_library_fulltext_reads_all_pages_or_fails(failure, monkeypatch):
    calls = []

    def read(**kwargs):
        calls.append(kwargs)
        if len(calls) == 1:
            return {'status': 'ready', 'pages': [{'page': 0, 'text': '无页码正文'}] + [
                {'page': i, 'text': f'正文{i}'} for i in range(1, 5)], 'next_cursor': 'next'}
        if failure == 'network':
            raise OSError('pagination failed')
        if failure == 'processing':
            return {'status': 'processing'}
        return {'status': 'ready', 'pages': [{'page': 5, 'text': '正文5'}, {'page': 6, 'text': '正文6'}]}

    if failure == 'empty':
        read = lambda **_: {'status': 'ready', 'pages': []}
    monkeypatch.setitem(sys.modules, 'lazymind.chat.service.academic_handoff',
                        SimpleNamespace(academic_read_paper=read))
    if failure:
        with pytest.raises((ValueError, OSError)):
            tools._read_f7_paper('kb-1')
    else:
        result = tools._read_f7_paper('kb-1')
        assert result == {'status': 'ready', 'text': '无页码正文\n\n' + '\n\n'.join(
            f'<!-- page:{i} -->\n正文{i}' for i in range(1, 7))}
        assert calls == [{'paper_id': 'kb-1'}, {'cursor': 'next'}]


def test_f7_library_read_failure_publishes_nothing(runtime, reflection_calls, monkeypatch):
    ctx, batches = runtime
    monkeypatch.setattr(tools, '_load_f7_handoff', lambda: {
        'kind': 'reflection', 'input': {'papers': [
            {'paper_id': 'kb-1', 'title': '标题', 'abstract': '不可用于掩盖网络错误的摘要'},
        ], 'user_instruction': '生成心得'},
    })

    def read(_):
        raise OSError('permission or pagination failed')

    monkeypatch.setattr(tools, '_read_f7_paper', read)
    ctx.params = {'step_id': 'reading_reflection'}
    with pytest.raises(OSError, match='failed'):
        tools.run_paper_step()
    assert not reflection_calls and not batches


@pytest.mark.parametrize('count', [1, 2])
@pytest.mark.parametrize('user_input', ['', '生成阅读心得', '[paper:demo] 读取真实论文'])
def test_f7_attachment_entry_publishes_papers_and_reflection(count, user_input, runtime, reflection_calls, monkeypatch):
    ctx, batches = runtime
    papers = [{'id': f'P{i}', 'title': f'论文{i}', 'text': f'<!-- page:1 -->\n正文{i}'} for i in range(count)]
    before = copy.deepcopy(papers)
    history = {'1': ['/uploads/论文.pdf'], '2': ['/uploads/另一篇.pdf']}

    def attachments(value):
        assert value == history
        return papers

    monkeypatch.setattr(tools, '_conversation_papers', attachments)
    monkeypatch.setattr(tools.research_topic, 'prepare_papers', lambda *_: pytest.fail('F7 prepared F1 abstracts'))
    monkeypatch.setattr(tools.research_topic, 'infer_topic', lambda *_: pytest.fail('F7 inferred a topic'))
    ctx.params = {'step_id': 'reading_reflection', 'user_input': user_input, 'history_files_per_turn': history}
    tools.run_paper_step()
    outputs = {item['key']: item['value'] for item in batches[-1]}
    assert set(outputs) == {'reading_reflection_pool', 'reading_reflection_document'}
    pool = tools._read_bound(outputs['reading_reflection_pool'])
    assert pool['topic'] == (user_input or '；'.join(paper['title'] for paper in papers))
    extracted = tools.material(pool, 'papers')['content']
    assert papers == before
    assert [p['id'] for p in extracted] == [p['id'] for p in papers]
    assert all(p['text'] in e['text'] and e['extraction_version'] == 1 for p, e in zip(papers, extracted))
    assert pool['history'][-1]['node'] == 'F7'
    assert pool['current']['papers'] in pool['history'][-1]['inputs']
    assert [p['id'] for p in reflection_calls[0]['papers']] == [p['id'] for p in papers]
    assert all(p['source_kind'] == 'partial_text' for p in reflection_calls[0]['papers'])
    assert reflection_calls[0]['request'] == user_input
    assert tools._read_bound(outputs['reading_reflection_document']) == '# 阅读心得\n\n' + '、'.join(
        paper['id'] for paper in papers
    )
    assert all(not item['mock'] for item in pool['materials'])


@pytest.mark.parametrize('source', ['paper_input', 'seed_pool'])
def test_f7_explicit_empty_sources_do_not_fallback(source, runtime, reflection_calls, tmp_path, monkeypatch):
    ctx, batches = runtime
    path = tmp_path / 'input.md'
    value = tools.pool_from_business({'topic': '明确主题'}) if source == 'seed_pool' else {'topic': '明确主题'}
    if source == 'seed_pool':
        value['imported_attachments'] = [tools.text_digest('/uploads/无关论文.pdf')]
    path.write_text(json.dumps(value))
    monkeypatch.setattr(tools, '_conversation_papers', lambda *_: pytest.fail('history was scanned'))
    ctx.params = {'step_id': 'reading_reflection', 'remote_inputs': {source: str(path)},
                  'history_files_per_turn': {'1': ['/uploads/无关论文.pdf']}}
    with pytest.raises(tools.MissingMaterials, match='请先选择至少一篇论文'):
        tools.run_paper_step()
    assert not reflection_calls and not batches


@pytest.mark.parametrize('papers,error', [
    ([], '请先选择至少一篇论文'),
    ([{'id': 'P1', 'title': '缺内容'}], '摘要或全文'),
])
def test_f7_missing_materials_stop_before_model(papers, error, runtime, reflection_calls, monkeypatch):
    ctx, batches = runtime
    monkeypatch.setattr(tools, '_conversation_papers', lambda *_: papers)
    ctx.params = {'step_id': 'reading_reflection', 'history_files_per_turn': {'1': ['/uploads/paper.pdf']}}
    with pytest.raises(tools.MissingMaterials, match=error):
        tools.run_paper_step()
    assert not reflection_calls and not batches


@pytest.mark.parametrize('binding', ['paper_input', 'seed_pool', 'paper_input_pool'])
def test_f7_bound_papers_preserve_manuscript(binding, runtime, reflection_calls, tmp_path, monkeypatch):
    ctx, batches = runtime
    papers = [{'id': 'P1', 'title': '参考论文', 'abstract': '摘要依据'}]
    value = {'topic': '已确认主题', 'papers': papers, 'manuscript': '# 待操作稿件\n正文'}
    if binding != 'paper_input':
        value = tools.pool_from_business(value)
    path = tmp_path / 'input.json'
    path.write_text(json.dumps(value))
    monkeypatch.setattr(tools, '_conversation_papers', lambda *_: pytest.fail('history was scanned'))
    ctx.params = {'step_id': 'reading_reflection', 'current_user_input': '重点看实验设计',
                  'remote_inputs': {'paper_input' if binding == 'paper_input_pool' else binding: str(path)}}
    if binding == 'paper_input_pool':
        with pytest.raises(ValueError, match='paper_input.*材料池'):
            tools.run_paper_step()
        assert not reflection_calls and not batches
        return
    tools.run_paper_step()
    outputs = {item['key']: item['value'] for item in batches[-1]}
    pool = tools._read_bound(outputs['reading_reflection_pool'])
    assert tools.material(pool, 'manuscript')['content'] == '# 待操作稿件\n正文'
    assert tools.material(pool, 'papers')['content'] == papers
    assert reflection_calls[0]['papers'] == [{**paper, 'source_kind': 'abstract'} for paper in papers]
    assert reflection_calls[0]['topic'] == '已确认主题'
    assert reflection_calls[0]['request'] == '重点看实验设计'
    assert tools._read_bound(str(path)) == value


def test_f7_uses_edited_f2_evidence_and_current_request(runtime, reflection_calls, tmp_path, monkeypatch):
    ctx, batches = runtime
    monkeypatch.setattr(api.ALGORITHMS['literature_review'], 'run', lambda inputs: {
        'evidence': '# 原始综述', 'papers': inputs['papers'], 'is_demo': False,
    })
    seed = tools.pool_from_business({'topic': '既有主题', 'papers': [
        {'id': 'P1', 'title': '参考论文', 'abstract': '摘要', 'citation': '参考论文（测试材料）'},
    ]})
    pool = tools.execute_step('literature_review', seed, '旧的综述要求')['pool']
    pool_path, document_path = tmp_path / 'pool.json', tmp_path / 'evidence.md'
    pool_path.write_text(json.dumps(pool))
    document_path.write_text('# 人工修改后的综述\n补充比较依据')
    monkeypatch.setattr(tools, '_conversation_papers', lambda *_: pytest.fail('history was scanned'))
    ctx.params = {'step_id': 'reading_reflection', 'user_input': '旧的启动请求', 'current_user_input': '比较实验设计',
                  'remote_inputs': {'literature_review_pool': str(pool_path),
                                    'literature_review_document': str(document_path)}}
    tools.run_paper_step()
    outputs = {item['key']: item['value'] for item in batches[-1]}
    result = tools._read_bound(outputs['reading_reflection_pool'])
    assert reflection_calls[0]['evidence'] == document_path.read_text()
    assert reflection_calls[0]['request'] == '比较实验设计'
    assert result['sequence'] == pool['sequence'] + 1
    assert result['current']['papers'] == pool['current']['papers']
    assert result['current']['evidence']['version'] == pool['current']['evidence']['version'] + 1
    assert result['current']['evidence'] in result['history'][-1]['inputs']
    assert tools._read_bound(str(pool_path)) == pool


@pytest.mark.parametrize('existing_question', [False, True])
def test_bound_experiment_question_only_fills_missing_pool_material(existing_question, tmp_path):
    value = {'topic': '既有主题'}
    if existing_question:
        value['question'] = '已经确认的研究问题'
    original = tools.pool_from_business(value)
    source, question = tmp_path / 'pool.json', tmp_path / 'question.txt'
    source.write_text(json.dumps(original))
    question.write_text('补充的研究问题')
    pool = tools.initialize_pool({'seed_pool': str(source), 'experiment_question': str(question)})
    expected = value['question'] if existing_question else question.read_text()
    assert tools.material(pool, 'question')['content'] == expected
    assert tools._read_bound(str(source)) == original
    assert pool['sequence'] == original['sequence']


@pytest.mark.parametrize('use_pool', [False, True])
def test_f2_uses_explicit_selection_or_merges_attachments_into_saved_pool(use_pool, runtime, tmp_path, monkeypatch):
    ctx, batches = runtime
    papers = [{'id': 'P1', 'title': '原论文', 'abstract': '原摘要', 'citation': '原论文（测试材料）'}]
    value = {'topic': '既有主题', 'papers': papers}
    if use_pool:
        value = tools.pool_from_business(value)
    source = tmp_path / 'input.json'
    source.write_text(json.dumps(value))
    uploaded = [{'id': 'P1', 'title': '重复论文', 'text': '不覆盖原摘要'},
                {'id': 'P2', 'title': '新论文', 'text': '新正文', 'citation': '新论文（测试材料）'}]

    def attachments(_history):
        assert use_pool
        return uploaded

    monkeypatch.setattr(tools, '_conversation_papers', attachments)
    monkeypatch.setattr(api.ALGORITHMS['literature_review'], 'run', lambda inputs: {
        'evidence': '# 文献综述', 'papers': inputs['papers'], 'is_demo': False,
    })
    ctx.params = {'step_id': 'literature_review',
                  'remote_inputs': {'seed_pool' if use_pool else 'paper_input': str(source)},
                  'history_files_per_turn': {'1': ['/uploads/new.pdf']}}
    tools.run_paper_step()
    outputs = {item['key']: item['value'] for item in batches[-1]}
    assert set(outputs) == {'literature_review_pool', 'literature_review_document'}
    result = tools._read_bound(outputs['literature_review_pool'])
    from lazymind.document_tools.paper.source_materials import extract_paper
    expected = papers if not use_pool else [papers[0], {k: v for k, v in extract_paper(uploaded[1], '').items()
                                                      if k != 'source_tables'}]
    assert tools.material(result, 'papers')['content'] == expected
    assert tools._read_bound(str(source)) == value


@pytest.mark.parametrize('source', ['request', 'paper_input'])
def test_f7_explicit_demo_keeps_demo_status(source, runtime, reflection_calls, tmp_path):
    ctx, batches = runtime
    ctx.params = {'step_id': 'reading_reflection', 'user_input': '[paper:demo]'}
    if source == 'paper_input':
        path = tmp_path / 'demo.json'
        path.write_text(json.dumps({'demo': True}))
        ctx.params = {'step_id': 'reading_reflection', 'remote_inputs': {'paper_input': str(path)}}
    tools.run_paper_step()
    outputs = {item['key']: item['value'] for item in batches[-1]}
    assert reflection_calls
    assert '演示数据' in tools._read_bound(outputs['reading_reflection_document'])


def test_f7_business_papers_without_topic_or_request_use_titles(runtime, reflection_calls, tmp_path, monkeypatch):
    ctx, batches = runtime
    path = tmp_path / 'papers.json'
    path.write_text(json.dumps({'papers': [{'id': 'P1', 'title': '论文主题', 'text': '完整正文'}]}))
    monkeypatch.setattr(tools, '_conversation_papers', lambda *_: pytest.fail('history was scanned'))
    ctx.params = {'step_id': 'reading_reflection', 'remote_inputs': {'paper_input': str(path)}}
    tools.run_paper_step()
    assert reflection_calls[0]['topic'] == '论文主题'
    assert reflection_calls[0]['request'] == ''
    assert len(batches) == 1


@pytest.mark.parametrize('step', ['research_topic', 'reading_reflection'])
def test_paper_entry_explicit_topic_without_request_uses_attachments(
    step, runtime, reflection_calls, tmp_path, monkeypatch,
):
    ctx, batches = runtime
    topic = tmp_path / 'topic.txt'
    topic.write_text('明确阅读主题')
    monkeypatch.setattr(tools, '_conversation_papers', lambda *_: [
        {'id': 'P1', 'title': '论文', 'text': '正文'},
    ])
    monkeypatch.setattr(tools.research_topic, 'infer_topic', lambda *_: pytest.fail('explicit topic was inferred'))
    ctx.params = {'step_id': step, 'remote_inputs': {'paper_topic': str(topic)},
                  'history_files_per_turn': {'1': ['/uploads/paper.pdf']}}
    tools.run_paper_step()
    outputs = {item['key']: item['value'] for item in batches[-1]}
    assert tools._read_bound(outputs[step + '_pool'])['topic'] == '明确阅读主题'


@pytest.mark.parametrize('step', ['research_topic', 'reading_reflection'])
@pytest.mark.parametrize('binding', ['manuscript_file', 'manuscript_text'])
def test_bound_manuscript_does_not_block_conversation_papers(
    step, binding, runtime, reflection_calls, tmp_path, monkeypatch,
):
    ctx, batches = runtime
    manuscript = tmp_path / 'manuscript.md'
    manuscript.write_text('# 目标稿件\n稿件正文')
    papers = [{
        'id': 'P1',
        'title': '参考论文',
        'text': '附件论文正文',
        'abstract': '附件论文摘要',
    }]
    history = {'1': ['/uploads/参考论文.pdf']}

    def attachments(value):
        assert value == history
        return copy.deepcopy(papers)

    monkeypatch.setattr(tools, '_conversation_papers', attachments)
    ctx.params = {
        'step_id': step,
        'remote_inputs': {binding: str(manuscript)},
        'history_files_per_turn': history,
    }
    tools.run_paper_step()
    outputs = {item['key']: item['value'] for item in batches[-1]}
    pool = tools._read_bound(outputs[step + '_pool'])
    assert tools.material(pool, 'manuscript')['content'] == manuscript.read_text()
    extracted = tools.material(pool, 'papers')['content']
    assert extracted[0]['id'] == papers[0]['id']
    assert papers[0]['text'] in extracted[0]['text']
    assert extracted[0]['extraction_version'] == 1
    assert step + '_document' in outputs
    if step == 'reading_reflection':
        assert reflection_calls[0]['papers'][0]['id'] == 'P1'
        assert '附件论文正文' in reflection_calls[0]['papers'][0]['text']


@pytest.mark.parametrize('binding', ['manuscript_file', 'manuscript_text'])
def test_f7_bound_manuscript_without_attachments_fails(
    binding, runtime, reflection_calls, tmp_path,
):
    ctx, batches = runtime
    manuscript = tmp_path / 'manuscript.md'
    manuscript.write_text('# 目标稿件\n正文')
    ctx.params = {
        'step_id': 'reading_reflection',
        'remote_inputs': {binding: str(manuscript)},
        'history_files_per_turn': {},
    }
    with pytest.raises(tools.MissingMaterials, match='请先选择至少一篇论文'):
        tools.run_paper_step()
    assert not reflection_calls and not batches


def test_f1_pdf_resources_are_scoped_deduplicated_and_parsed(tmp_path, monkeypatch):
    from lazymind.chat.engine.tools.file_resources import ingest, resolver, store

    monkeypatch.setattr(store, 'workspace_for_request', lambda: str(tmp_path))
    resolved = {}
    for file_id, filename, body in [('P1', '论文一.pdf', '证据一'), ('P2', '论文二.pdf', '证据二')]:
        path = tmp_path / (file_id + '.md')
        path.write_text(body)
        resolved[file_id] = SimpleNamespace(kind='file_resource', file_id=file_id,
                                            display_name=filename, path=str(path))
    calls = []

    def cached_pdf(path, *, turn_seq, store):
        calls.append((Path(path).name, turn_seq))
        return {'file_id': Path(path).stem.replace('论文一', 'P1').replace('论文二', 'P2'),
                'parse_status': 'ready'}

    monkeypatch.setattr(ingest, 'ingest_pdf_file', cached_pdf)
    monkeypatch.setattr(resolver, 'resolve_text_target', lambda file_id, **_kwargs: resolved[file_id])
    papers = tools._conversation_papers({
        '2': ['/uploads/论文二.pdf', '/uploads/图片.png'],
        '1': ['/uploads/论文一.pdf'],
        '3': ['/uploads/论文一.pdf'],
    })
    assert [(paper['id'], paper['title'], paper['text']) for paper in papers] == [
        ('P1', '论文一', '证据一'), ('P2', '论文二', '证据二'),
    ]
    assert calls == [('论文一.pdf', 1), ('论文二.pdf', 2)]
    assert '/uploads/' not in json.dumps(papers, ensure_ascii=False)


@pytest.mark.parametrize('suffix', ['.doc', '.docx', '.pptx', '.txt', '.md', '.markdown', '.tex', '.rst', '.html', '.htm'])
def test_paper_attachments_reuse_text_resolver_and_content_identity(suffix, tmp_path, monkeypatch):
    from lazymind.chat.engine.tools.file_resources import ingest, resolver, store

    monkeypatch.setattr(store, 'workspace_for_request', lambda: str(tmp_path))
    monkeypatch.setattr(ingest, 'ingest_pdf_file', lambda *a, **kw: pytest.fail('non-PDF entered PDF ingestion'))
    parsed = tmp_path / 'parsed.txt'
    parsed.write_text('完整论文正文', encoding='utf-8')
    calls = []
    def resolve(target, *, turn, resources_only):
        calls.append((target, turn, resources_only))
        return SimpleNamespace(file_id=None, path=str(parsed), display_name=Path(target).name)
    monkeypatch.setattr(resolver, 'resolve_text_target', resolve)
    path = '/uploads/论文' + suffix
    papers = tools._conversation_papers({'1': [path], '2': [path]})
    assert calls == [(path, 1, True)]
    assert papers == [{'id': 'upload-' + tools.text_digest('完整论文正文'), 'title': '论文', 'text': '完整论文正文',
                       'attachment_key': tools.text_digest(path)}]


@pytest.mark.parametrize('body,error', [
    ('', '没有可用的解析正文'),
    ('部分正文\n\n[Attachment truncated after 200000 characters.]', '解析正文被截断'),
    (None, '无法读取或尚未解析完成'),
])
@pytest.mark.parametrize('step', ['research_topic', 'reading_reflection'])
def test_invalid_parsed_attachment_stops_before_publication(body, error, step, runtime, tmp_path, monkeypatch):
    from lazymind.chat.engine.tools.file_resources import resolver, store

    ctx, batches = runtime
    monkeypatch.setattr(store, 'workspace_for_request', lambda: str(tmp_path))
    parsed = tmp_path / 'parsed.txt'
    if body is not None:
        parsed.write_text(body, encoding='utf-8')
    monkeypatch.setattr(resolver, 'resolve_text_target', lambda *a, **kw: SimpleNamespace(
        file_id=None, path=str(parsed), display_name='论文.docx',
    ))
    ctx.params = {'step_id': step, 'user_input': '研究主题',
                  'history_files_per_turn': {'1': ['/uploads/论文.docx']}}
    with pytest.raises(tools.MissingMaterials, match=error):
        tools.run_paper_step()
    assert not batches


def test_f1_explicit_and_saved_inputs_do_not_rescan_conversation(runtime, tmp_path, monkeypatch):
    ctx, batches = runtime
    monkeypatch.setattr(tools, '_conversation_papers', lambda _history: pytest.fail('PDF history was rescanned'))
    paper_input = tmp_path / 'paper-input.json'
    paper_input.write_text(json.dumps({'topic': '显式主题', 'papers': [
        {'id': 'P1', 'title': '指定论文', 'text': '指定正文'},
    ]}, ensure_ascii=False))
    ctx.params = {'step_id': 'research_topic', 'user_input': '研究选题',
                  'remote_inputs': {'paper_input': str(paper_input)},
                  'history_files_per_turn': {'1': ['/uploads/其他论文.pdf']}}
    tools.run_paper_step()
    first = {item['key']: item['value'] for item in batches[-1]}
    assert tools.material(tools._read_bound(first['research_topic_pool']), 'papers')['content'][0]['id'] == 'P1'

    seed = tmp_path / 'seed.json'
    saved = tools.pool_from_business({'topic': '已确认主题'})
    saved['imported_attachments'] = [tools.text_digest('/uploads/其他论文.pdf')]
    seed.write_text(json.dumps(saved, ensure_ascii=False))
    ctx.params['remote_inputs'] = {'seed_pool': str(seed)}
    tools.run_paper_step()
    second = {item['key']: item['value'] for item in batches[-1]}
    assert tools._read_bound(second['research_topic_pool'])['topic'] == '已确认主题'


def test_f1_explicit_topic_wins_while_using_conversation_pdfs(runtime, tmp_path, monkeypatch):
    ctx, batches = runtime
    topic = tmp_path / 'topic.txt'
    topic.write_text('用户指定的研究主题')
    monkeypatch.setattr(tools, '_conversation_papers', lambda _history: [
        {'id': 'P1', 'title': '论文一', 'text': '论文正文'},
    ])
    monkeypatch.setattr(tools.research_topic, 'infer_topic', lambda *_args: pytest.fail('主题不应重新推断'))
    ctx.params = {'step_id': 'research_topic', 'user_input': '根据附件选题',
                  'remote_inputs': {'paper_topic': str(topic)},
                  'history_files_per_turn': {'1': ['/uploads/论文一.pdf']}}
    tools.run_paper_step()
    outputs = {item['key']: item['value'] for item in batches[-1]}
    pool = tools._read_bound(outputs['research_topic_pool'])
    assert pool['topic'] == '用户指定的研究主题'
    assert tools.material(pool, 'papers')['content'][0]['id'] == 'P1'


@pytest.mark.parametrize('binding', [None, 'research_topic_pool', 'recovery_literature_pool'])
def test_f2_merges_uploaded_papers_with_saved_materials(binding, runtime, tmp_path, monkeypatch):
    ctx, batches = runtime
    existing = {'id': 'P1', 'title': '已有论文', 'text': '原文', 'abstract': '已提炼摘要', 'citation': '原有引用'}
    uploaded = {'id': 'P2', 'title': '补充论文', 'text': '新上传的完整正文'}
    calls = []

    def review(inputs):
        calls.append(copy.deepcopy(inputs))
        return {'evidence': '# 文献综述', 'papers': [
            {**paper, 'citation': paper.get('citation', paper['title'])} for paper in inputs['papers']
        ], 'is_demo': False}

    monkeypatch.setattr(api.ALGORITHMS['literature_review'], 'run', review)
    pool = tools.pool_from_business({'topic': '已确认主题', 'papers': [existing], 'question': '已确认问题'})
    if binding:
        pool = tools.execute_step('research_topic', pool)['pool']
        if binding == 'recovery_literature_pool':
            pool = tools.execute_step('literature_review', pool)['pool']
        path = tmp_path / 'saved-pool.json'
        path.write_text(json.dumps(pool, ensure_ascii=False))
    calls.clear()
    before = copy.deepcopy(pool)
    history = {'1': ['/uploads/已有论文.pdf'], '3': ['/uploads/补充论文.pdf']}

    def attachments(received):
        assert received == history
        # The resolver returns the same stable ID on repeated uploads.
        return [{key: value for key, value in existing.items() if key != 'abstract'}, uploaded]

    monkeypatch.setattr(tools, '_conversation_papers', attachments)
    ctx.params = {'step_id': 'literature_review', 'current_user_input': '结合新上传的论文更新综述',
                  'history_files_per_turn': history,
                  'remote_inputs': {binding: str(path)} if binding else {}}
    tools.run_paper_step()
    outputs = {item['key']: item['value'] for item in batches[-1]}
    result = tools._read_bound(outputs['literature_review_pool'])
    expected = [existing, uploaded] if binding else [
        {key: value for key, value in existing.items() if key != 'abstract'}, uploaded,
    ]
    from lazymind.document_tools.paper.source_materials import extract_paper
    expected = [paper if binding and paper['id'] == existing['id'] else {
        k: v for k, v in extract_paper(paper, '').items() if k != 'source_tables'} for paper in expected]
    assert calls[-1]['papers'] == expected
    published = [{**paper, 'citation': paper.get('citation', paper['title'])} for paper in expected]
    assert tools.material(result, 'papers')['content'] == published
    assert calls[-1]['request'] == '结合新上传的论文更新综述'
    if binding:
        assert calls[-1]['question'] == tools.material(pool, 'question')['content']
        assert calls[-1]['topic'] == pool['topic']
        assert result['materials'][:len(before['materials'])] == before['materials']
        assert tools._read_bound(str(path)) == before
        merged = result['materials'][len(before['materials'])]
        assert merged['kind'] == 'papers'
        assert merged['derived_from'] == [pool['current']['papers']]
        assert merged['content'] == expected

    # A retry with the published pool must not append the same attachments again.
    ctx.params['remote_inputs'] = {'literature_review_pool': outputs['literature_review_pool']}
    tools.run_paper_step()
    retried = tools._read_bound(next(item['value'] for item in batches[-1]
                                    if item['key'] == 'literature_review_pool'))
    assert calls[-1]['papers'] == published
    assert result['current']['papers'] in retried['history'][-1]['inputs']


def test_f2_unreadable_new_attachment_does_not_publish_stale_review(runtime, tmp_path, monkeypatch):
    ctx, batches = runtime
    pool = tools.pool_from_business({'topic': '既有主题', 'question': '已确认问题'})
    path = tmp_path / 'saved-pool.json'
    path.write_text(json.dumps(pool))

    def unreadable(_history):
        raise tools.MissingMaterials('论文附件无法解析')

    monkeypatch.setattr(tools, '_conversation_papers', unreadable)
    monkeypatch.setattr(api.ALGORITHMS['literature_review'], 'run',
                        lambda *_: pytest.fail('missing attachment reached algorithm'))
    ctx.params = {'step_id': 'literature_review', 'remote_inputs': {'research_topic_pool': str(path)},
                  'history_files_per_turn': {'2': ['/uploads/坏论文.pdf']}}
    with pytest.raises(tools.MissingMaterials, match='论文附件无法解析'):
        tools.run_paper_step()
    assert not batches
    assert tools._read_bound(str(path)) == pool


def test_f1_unreadable_pdf_stops_before_publication(runtime, monkeypatch):
    ctx, batches = runtime
    def unreadable(_history):
        raise tools.MissingMaterials('论文附件无法解析')
    monkeypatch.setattr(tools, '_conversation_papers', unreadable)
    ctx.params = {'step_id': 'research_topic', 'user_input': '研究证据追溯',
                  'history_files_per_turn': {'1': ['/uploads/坏论文.pdf']}}
    with pytest.raises(tools.MissingMaterials, match='论文附件无法解析'):
        tools.run_paper_step()
    assert not batches


def test_prepare_without_input_does_not_invent_materials(runtime):
    ctx, batches = runtime
    ctx.params = {'step_id': 'prepare', 'user_input': '帮我审稿'}
    with pytest.raises(tools.MissingMaterials, match='请提供'):
        tools.run_paper_step()
    assert not batches


def test_business_manuscript_entry_publishes_visible_preparation_then_review(runtime, tmp_path):
    ctx, batches = runtime
    path = tmp_path / 'business.json'
    path.write_text(json.dumps({'topic': '我的研究', 'manuscript': '# 已有稿件\n检查方法'}))
    ctx.params = {'step_id': 'prepare', 'remote_inputs': {'paper_input': str(path)}}
    tools.run_paper_step()
    outputs = {item['key']: item['value'] for item in batches[-1]}
    summary = Path(outputs['prepare_document']).read_text()
    assert '我的研究' in summary and '稿件' in summary
    assert '演示数据' not in summary and 'pool' not in summary
    pool = tools._read_bound(outputs['seed_pool'])
    assert set(pool['current']) == {'manuscript'}
    ctx.params = {'step_id': 'peer_review', 'remote_inputs': {'seed_pool': outputs['seed_pool']}}
    tools.run_paper_step()
    assert {item['key'] for item in batches[-1]} == {'peer_review_pool', 'peer_review_document'}


def test_config_logical_edges_and_no_real_external_calls():
    state = yaml.safe_load((PACKAGE / 'scenario/state.yml').read_text())
    # Core uses these capabilities to forward KB and Sciverse credentials to F2.
    assert {'kb', 'academic_search'} <= set(state['steps']['literature_review']['capabilities'])
    for step in state['steps'].values():
        # The wrapper owns runtime parameters. Do not let a focused UI tab or
        # old launch request redefine a deterministic Mock tool invocation.
        assert '{{runtime_instruction}}' not in step['prompt']
        assert '{{user_input}}' not in step['prompt']
        assert 'Do not substitute chat text for the tool call.' in step['prompt']
        assert 'The currently viewed tab is not the execution node.' in step['prompt']
        assert step['prompt'].isascii()
        assert step['acceptance_criteria'].isascii()
    assert all(edge['when'].isascii() for edges in state['transitions'].values() for edge in edges)
    config = yaml.safe_load((PACKAGE / 'workflow.yaml').read_text())
    assert 'data_analysis' in config['when_to_use']
    assert config['name'] == '论文写作'
    for step, node in tools.STEP_NODE.items():
        actual = {tools.STEP_NODE[e['to']] for e in state['transitions'][step] if e['to'] != 'finish'}
        if step == 'refine_topic':
            actual.add('F2')  # Native rewind, not a cyclic control edge.
            assert actual == {'F2', 'F3'}
            continue
        if step == 'revise_document':
            actual.add('F6')
        if step == 'data_analysis':
            actual.add('F3')  # Native rewind carries prepared-data recovery snapshots.
        if step in {'write_outline', 'write_document', 'data_analysis', 'literature_review'}:
            actual.discard(node)  # Outline/draft/direct revision are distinct F5 entries.
        if step == 'write_outline':
            assert actual == set()
        else:
            assert actual == set(tools.ROUTES[node])
        assert state['steps'][step]['mode'] == 'human'
        assert state['steps'][step]['tools'] == ['run_paper_step']


@pytest.mark.parametrize('content', [
    'x' * 490 + '<a id="block-user-long-id"></a>\\n## Edited',
    '\u0060\u0060\u0060text\\nE2E-EDIT\\n\u0060\u0060\u0060\\n<a id="anchor"></a>' * 20,
])
def test_feedback_text_reaches_algorithm_unchanged(content, monkeypatch):
    pool = tools.execute_mock('F2', tools.seed_pool())['pool']
    pool = tools.pool_from_inputs({'literature_review_pool': pool, 'literature_review_document': content})
    seen = []
    module = api.ALGORITHMS['research_topic']
    original = module.run
    def capture(inputs):
        seen.append(copy.deepcopy(inputs))
        return original(inputs)
    monkeypatch.setattr(module, 'run', capture)
    tools.execute_step('refine_topic', pool)
    assert seen[0]['evidence'] == content
    assert seen[0]['mode'] == 'refine'


@pytest.mark.parametrize('update', [
    {'current_user_input': '只检查引用和局限'},
    {'current_user_input': '', 'retry_hint': "User is currently viewing Workflow tab 'data_analysis'. "
     'Recovery request for this rerun only: 只检查引用和局限'},
])
def test_real_core_request_fields_override_launch_intent(runtime, tmp_path, update):
    ctx, batches = runtime
    pool = tools.execute_mock('F5', draft_pool())['pool']
    path = tmp_path / 'pool.json'
    path.write_text(json.dumps(pool))
    ctx.params = {'step_id': 'peer_review', 'remote_inputs': {'write_document_pool': str(path)},
                  'user_input': '旧要求：检查全文', 'launch_user_input': '旧要求：检查全文', **update}
    tools.run_paper_step()
    assert tools._read_bound(batches[-1][0]['value'])['history'][-1]['request'] == '只检查引用和局限'
    assert '旧要求' not in tools._read_bound(batches[-1][1]['value'])


def test_public_workflow_presentation_and_named_outputs():
    workflow = yaml.safe_load((PACKAGE / 'workflow.yaml').read_text())
    assert workflow['id'] == 'paper-writing'
    assert workflow['name'] == '论文写作'
    assert {step['id'] for step in workflow['steps']} == {
        'research_topic', 'literature_review', 'refine_topic', 'experiment',
        'data_analysis', 'write_outline', 'write_document', 'peer_review', 'revise_document',
        'reading_reflection', 'finish',
    }
    assert not any(re.match(r'f\d(?:_|$)', slot['id']) for slot in workflow['slots'])
    ui = workflow['ui']
    assert not ui['task_presentation'].get('grouped')
    assert set(ui['task_presentation']['hidden_steps']) == {'finish'}
    assert len(ui['tabs']) == 10
    assert ui['tabs'][0]['step_id'] == 'research_topic'
    assert 'prepare_document' not in ui['slots']
    assert 'prepare' not in yaml.safe_load((PACKAGE / 'scenario/state.yml').read_text())['steps']
    for tab in ui['tabs']:
        assert tab['id'] not in ui['task_presentation']['hidden_steps']
        assert len(tab['label']) <= 6
        assert not re.search(r'F\d|mock', tab['label'], re.I)
        assert tab['allow_download'] is True
    for slot in workflow['slots']:
        if slot['id'].endswith('_pool'):
            assert slot['exposed'] is False
            assert slot['type'] == 'file'
        if slot['id'].endswith('_document') and not slot.get('external'):
            assert slot['type'] == 'file'
    for step in tools.STEP_NODE:
        assert ui['slots'][step + '_document'] == {'widgetType': 'text-markdown', 'readOnly': False}


def test_no_ppt_node_artifact_or_prompt_in_paper_package():
    for relative in ('workflow.yaml', 'scenario/state.yml', 'scenario/scenario.md', 'scripts/tools.py'):
        assert 'ppt' not in (PACKAGE / relative).read_text().lower(), relative
    assert 'presentation' not in tools.KINDS
    pool = tools.seed_pool()
    before = copy.deepcopy(pool)
    with pytest.raises(ValueError, match='未知节点'):
        tools.execute_mock('PPT', pool)
    assert pool == before


def test_removed_ppt_step_cannot_publish_artifacts(runtime):
    ctx, batches = runtime
    ctx.params = {'step_id': 'ppt'}
    with pytest.raises(ValueError, match='未知 Workflow 步骤'):
        tools.run_paper_step()
    assert not batches


def test_recovery_failure_and_retry_use_core_retry_hint(runtime, tmp_path):
    ctx, batches = runtime
    path = tmp_path / 'pool.json'
    path.write_text(json.dumps(tools.seed_pool()))
    ctx.params = {'step_id': 'experiment', 'remote_inputs': {'seed_pool': str(path)},
                  'user_input': '旧的启动请求', 'current_user_input': '',
                  'retry_hint': 'Recovery request for this rerun only: 重跑 experiment [mock:fail]'}
    with pytest.raises(RuntimeError, match='模拟执行失败'):
        tools.run_paper_step()
    assert not batches
    ctx.params['user_input'] = '[mock:fail]'
    ctx.params['retry_hint'] = 'Recovery request for this rerun only: 重试 experiment [mock:retry]'
    tools.run_paper_step()
    assert len(batches) == 1
    assert tools._read_bound(batches[0][0]['value'])['history'][-1]['request'] == '重试 experiment [mock:retry]'

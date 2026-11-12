"""Uploaded template routing survives missing or contradictory model context."""
import importlib.util
from pathlib import Path
from types import SimpleNamespace

import pytest
import yaml

ROOT = Path(__file__).resolve().parents[4]


@pytest.fixture
def tools(monkeypatch):
    spec = importlib.util.spec_from_file_location(
        '_ppt_output_contract_tools', ROOT / 'workflows/ppt-workflow/scripts/tools.py')
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    monkeypatch.setattr(module, '_workflow_session_id', lambda: 'session')
    return module


def bind(monkeypatch, tools, mode):
    artifacts = [] if mode is None else [
        {'slot': 'template_mode', 'selected': True, 'revision': 2, 'value': {'text': mode}},
        {'slot': 'template_mode', 'selected': False, 'revision': 3, 'value': {'text': 'none'}},
    ]
    monkeypatch.setattr(tools, '_workflow_client', lambda: SimpleNamespace(
        list_artifacts=lambda session: SimpleNamespace(result={'artifacts': artifacts})))


@pytest.mark.parametrize('entry,args', [
    ('ppt_generate_pages', {}),
    ('ppt_run_stage', {'deck_dir': '/not-used', 'stage': 'page-html'}),
    ('ppt_run_stage', {'deck_dir': '/not-used', 'stage': 'batch-page-html'}),
    ('ppt_run_stage', {'deck_dir': '/not-used', 'stage': 'refine-page'}),
    ('ppt_run_stage', {'deck_dir': '/not-used', 'stage': 'batch-refine-page'}),
    ('ppt_publish_pages', {'deck_dir': '/not-used'}),
])
def test_template_rejects_html_before_any_render_or_file_access(monkeypatch, tools, entry, args):
    bind(monkeypatch, tools, 'upload')
    monkeypatch.setattr(tools, '_resolve_deck_dir', lambda *a: pytest.fail('must not touch deck'))
    with pytest.raises(tools.ToolExecutionError, match='PPT_OUTPUT_FORMAT_MISMATCH'):
        getattr(tools, entry)(**args)


@pytest.mark.parametrize('output_format,prompt', [
    ('auto', '原生 PPTX 模板标题替换'),
    ('html', '生成 HTML 文档'),
    ('native_pptx', '生成一个完整 HTML 文档，用于幻灯片'),
    ('native_pptx', 'Return one complete HTML document'),
])
def test_prompt_publisher_rejects_wrong_route_before_publish(monkeypatch, tools, output_format, prompt):
    bind(monkeypatch, tools, 'upload')
    monkeypatch.setattr(tools, '_bound_workflow_deck', lambda: pytest.fail('must validate first'))
    with pytest.raises(tools.ToolExecutionError, match='PPT_OUTPUT_FORMAT_MISMATCH'):
        tools.ppt_publish_outline(output_format=output_format, page_prompts=[prompt])


@pytest.mark.parametrize('mode,output_format,prompt', [
    ('upload', 'native_pptx', '复用源页1，替换标题，移除示例；原生图表。内容：HTML项目性能提升。'),
    ('none', 'html', '生成一个完整 HTML 文档，蓝色标题，保留来源。'),
])
def test_publish_correct_route_preserves_authored_prompt(monkeypatch, tmp_path, tools, mode, output_format, prompt):
    bind(monkeypatch, tools, mode)
    (tmp_path / 'outline_draft.md').write_text('## 封面', encoding='utf-8')
    monkeypatch.setattr(tools, '_bound_workflow_deck', lambda: tmp_path)
    monkeypatch.setattr(tools, 'require_context', lambda: SimpleNamespace())
    monkeypatch.setattr(tools, '_resolve_artifact_text',
                        lambda _ctx, key: ('## 封面\n标题' if key == 'deck_outline' else None, 'text'))
    monkeypatch.setattr(tools, '_ui_slot_order_list', lambda *a: [])
    published = []
    monkeypatch.setattr(tools, '_publish_ordered_ppt_artifact',
                        lambda **kw: (published.append(kw), {'ok': True})[1])
    tools.ppt_publish_outline(output_format=output_format, page_prompts=[prompt])
    assert len(published) == 1 and published[0]['value'] == prompt


@pytest.mark.parametrize('mode', [None, 'unknown'])
def test_missing_contract_cannot_default_to_html(monkeypatch, tools, mode):
    bind(monkeypatch, tools, mode)
    with pytest.raises(tools.ToolExecutionError, match='PPT_OUTPUT_CONTRACT_'):
        tools.ppt_generate_pages()


def test_contract_read_failure_cannot_default_to_html(monkeypatch, tools):
    def unavailable():
        raise ConnectionError('offline')
    monkeypatch.setattr(tools, '_workflow_client', unavailable)
    with pytest.raises(tools.ToolExecutionError, match='PPT_OUTPUT_CONTRACT_UNAVAILABLE'):
        tools.ppt_generate_pages()


def test_prompt_stage_receives_durable_template_mode():
    state = yaml.safe_load((ROOT / 'workflows/ppt-workflow/scenario/state.yml').read_text(encoding='utf-8'))
    step = state['steps']['plan_page_prompts']
    assert {'material': 'template_mode', 'required': True} in step['inputs']
    assert 'output_format="native_pptx"' in step['prompt']
    assert 'HTML ONLY (presentation_output_format=html' in step['prompt']


@pytest.mark.parametrize('output_format', ['html', 'native_pptx'])
def test_uploaded_template_respects_saved_choice(monkeypatch, tools, output_format):
    artifacts = [
        {'slot': 'template_mode', 'value': 'upload'},
        {'slot': 'presentation_output_format', 'revision': 1, 'value': output_format},
        {'slot': 'presentation_output_format', 'revision': 2, 'selected': False, 'value': 'invalid'},
    ]
    monkeypatch.setattr(tools, '_workflow_client', lambda: SimpleNamespace(
        list_artifacts=lambda session: SimpleNamespace(result={'artifacts': artifacts})))
    prompt = 'Recreate template style with HTML' if output_format == 'html' else '复用源页2并替换标题'
    assert tools._validate_prompt_output_format(output_format, [prompt]) == output_format
    if output_format == 'html':
        tools._require_html_output()
        with pytest.raises(tools.ToolExecutionError, match='PPT_OUTPUT_FORMAT_MISMATCH'):
            tools.ppt_fill_template('not-read.pptx', 'hash', pages=[{}])


def test_template_startup_asks_only_missing_output_choice():
    from lazymind.chat.workflow.clarification import resolve_fields, startup_card
    policy = yaml.safe_load((ROOT / 'workflows/ppt-workflow/workflow.yaml').read_text(encoding='utf-8'))['runtime']
    values, missing = resolve_fields(policy, '主题：研究报告，8页，使用附件 company.pptx 作为 PPTX 模板',
                                     supplied={'topic': '研究报告', 'template_pptx': 'company.pptx'},
                                     extractions={'slide_count': [{'value': '8', 'evidence': '8页'}]})
    assert values['template_mode'] == 'upload'
    assert [field['id'] for field in missing] == ['presentation_output_format']
    card = startup_card(missing, {})
    assert len(card['questions']) == 1
    values, missing = resolve_fields(policy, '主题：研究报告，8页',
                                     previous={**values, 'presentation_output_format': 'HTML｜参考模板样式生成'})
    assert values['presentation_output_format'] == 'html' and missing == []


def test_plain_startup_never_asks_template_format_or_generation_mode():
    from lazymind.chat.workflow.clarification import resolve_fields
    policy = yaml.safe_load((ROOT / 'workflows/ppt-workflow/workflow.yaml').read_text(encoding='utf-8'))['runtime']
    values, missing = resolve_fields(policy, '主题：城市介绍，8页，不生成AI底图',
                                     supplied={'topic': '城市介绍'},
                                     extractions={'slide_count': [{'value': '8', 'evidence': '8页'}]})
    assert values['template_mode'] == 'none' and missing == []
    assert 'presentation_output_format' not in values
    assert 'generation_mode' not in {field['id'] for field in policy['clarification_fields']}


@pytest.mark.parametrize('text,expected', [
    ('输出 HTML', 'html'), ('输出PPTX', 'native_pptx'), ('用html输出', 'html'),
    ('HTML 格式', 'html'), ('generate PPTX', 'native_pptx'),
])
def test_explicit_template_output_does_not_ask_again(text, expected):
    from lazymind.chat.workflow.clarification import resolve_fields
    policy = yaml.safe_load((ROOT / 'workflows/ppt-workflow/workflow.yaml').read_text(encoding='utf-8'))['runtime']
    values, missing = resolve_fields(policy, '主题：研究报告，8页，' + text,
                                     supplied={'topic': '研究报告', 'template_pptx': 'template.pptx'},
                                     extractions={'slide_count': [{'value': '8', 'evidence': '8页'}]})
    assert values['presentation_output_format'] == expected and missing == []


@pytest.mark.parametrize('brief,output_format,count_evidence', [
    ('转正答辩，生成12 页pptx，十五分钟演讲、五分钟问答', 'native_pptx', '12 页pptx'),
    ('请按模板制作十二页可编辑的 PowerPoint 文件', 'native_pptx', '十二页'),
    ('沿用模板风格做12页网页幻灯片', 'html', '12页'),
])
def test_template_output_accepts_model_extraction_from_user_request(brief, output_format, count_evidence):
    from lazymind.chat.workflow.clarification import resolve_fields
    policy = yaml.safe_load((ROOT / 'workflows/ppt-workflow/workflow.yaml').read_text())['runtime']
    values, missing = resolve_fields(policy, brief, supplied={
        'topic': '转正答辩', 'template_pptx': 'template.pptx',
        'presentation_output_format': output_format,
    }, extractions={'slide_count': [{'value': '12', 'evidence': count_evidence}]})
    assert missing == []
    assert values['presentation_output_format'] == output_format


@pytest.mark.parametrize('previous', [None, {'presentation_output_format': 'HTML｜参考模板样式生成'}])
def test_user_output_choice_takes_precedence_over_model_binding(previous):
    from lazymind.chat.workflow.clarification import resolve_fields
    policy = yaml.safe_load((ROOT / 'workflows/ppt-workflow/workflow.yaml').read_text())['runtime']
    values, missing = resolve_fields(policy, '主题：研究报告，8页，输出 HTML', supplied={
        'topic': '研究报告', 'template_pptx': 'template.pptx', 'presentation_output_format': 'native_pptx',
    }, previous=previous, extractions={'slide_count': [{'value': '8', 'evidence': '8页'}]})
    assert missing == []
    assert values['presentation_output_format'] == 'html'


@pytest.mark.parametrize('mode,slot,kind,value,expected', [
    ('none', None, None, None, False),
    ('upload', None, None, None, False),
    ('none', 'preview_notes', 'text', {'text': 'Notes only'}, False),
    ('none', 'preview_html', 'text', {'text': ''}, False),
    ('none', 'preview_html', 'text', {'text': '<!-- lazymind-ppt-placeholder -->Not generated'}, False),
    ('none', 'preview_html', 'text', {'text': '<html>Slide</html>'}, True),
    ('none', 'preview_html', 'file', {'type': 'text', 'path': '/core/slide.html'}, True),
    ('none', 'native_pptx', 'file', {'path': '/core/deck.pptx'}, False),
    ('upload', 'preview_html', 'text', {'text': '<html>Slide</html>'}, False),
    ('upload', 'native_pptx', 'text', {'text': 'I generated the file'}, False),
    ('upload', 'native_pptx', 'file', {'path': '/core/deck.pptx'}, True),
])
def test_delivery_requires_this_attempts_selected_output(monkeypatch, tools, mode, slot, kind, value, expected):
    bind(monkeypatch, tools, mode)
    artifacts = [] if slot is None else [{'slot': slot, 'content_type': kind, 'value': value}]
    if expected:
        assert tools.check_ppt_delivery(artifacts)['status'] == 'ok'
    else:
        with pytest.raises(tools.ToolExecutionError, match='PPT_DELIVERY_MISSING'):
            tools.check_ppt_delivery(artifacts)


def test_template_html_delivery_uses_saved_choice_and_does_not_accept_old_outputs(monkeypatch, tools):
    html = {'slot': 'preview_html', 'content_type': 'text', 'value': {'text': '<html>Saved slide</html>'}}
    monkeypatch.setattr(tools, '_workflow_client', lambda: SimpleNamespace(
        list_artifacts=lambda session: SimpleNamespace(result={'artifacts': [
            {'slot': 'template_mode', 'value': {'text': 'upload'}},
            {'slot': 'presentation_output_format', 'value': {'text': 'html'}},
            html,
        ]})))
    with pytest.raises(tools.ToolExecutionError, match='PPT_DELIVERY_MISSING'):
        tools.check_ppt_delivery([])
    assert tools.check_ppt_delivery([html])['output_format'] == 'html'


@pytest.mark.asyncio
@pytest.mark.parametrize('output_format', ['html', 'native_pptx'])
@pytest.mark.parametrize('delivered,status', [(False, 'succeeded'), (True, 'succeeded'), (False, 'interrupted')])
async def test_ppt_completion_gate_runs_before_runtime_success(
        monkeypatch, tmp_path, request, delivered, status, output_format):
    import base64
    import json
    from unittest.mock import AsyncMock, Mock
    import lazyllm
    from lazymind.workflow_sdk import WorkflowClient
    from lazymind.chat.engine.subagent import runner
    from lazymind.chat.workflow.remote_executor import RemoteWorkflowExecutor

    package_root = ROOT / 'workflows/ppt-workflow'
    files = {path: base64.b64encode((package_root / path).read_bytes()).decode()
             for path in ('workflow.yaml', 'scripts/tools.py')}
    revision = tmp_path.name
    package = {'revision_id': revision, 'tree_hash': 'test-tree', 'files': files}
    # Fake only Core's package/artifact reads; retain the real declaration,
    # revision validation, script materialization and callable loading chain.
    package_read = Mock(return_value=SimpleNamespace(result=package))
    monkeypatch.setattr(WorkflowClient, 'get_workflow', package_read)
    monkeypatch.setattr(WorkflowClient, 'list_artifacts', lambda *_: SimpleNamespace(result={'artifacts': [
        {'slot': 'template_mode', 'value': {'text': 'upload' if output_format == 'native_pptx' else 'none'}},
        {'slot': 'presentation_output_format', 'value': {'text': output_format}},
    ]}))
    missing = object()
    previous_config = lazyllm.globals.get('agentic_config', missing)

    def restore_config():
        if previous_config is missing:
            lazyllm.globals.pop('agentic_config', None)
        else:
            lazyllm.globals['agentic_config'] = previous_config

    request.addfinalizer(restore_config)
    lazyllm.globals['agentic_config'] = {'workflow_session_id': 'session'}
    policy = yaml.safe_load((package_root / 'workflow.yaml').read_text(encoding='utf-8'))['runtime']
    worker = RemoteWorkflowExecutor()
    runtime = AsyncMock()
    runtime.upload_artifact_file.return_value = '/static-files/deck.pptx'
    if delivered and output_format == 'native_pptx':
        (tmp_path / 'deck.pptx').write_bytes(b'presentation fixture')
    runtime.context.return_value = {'metadata': {'task_id': 'task'}, 'inputs': {}}
    runtime.execution_spec.return_value = {
        'task': {'input_slots': [], 'output_slots': ['preview_html', 'preview_notes', 'native_pptx']},
        'workspace_path': str(tmp_path), 'steps': [], 'llm_config': {},
        'params': {'step_id': 'generate_ppt', 'workflow_runtime': policy,
                   'workflow_id': 'ppt-workflow', 'revision_id': revision, 'tree_hash': 'test-tree'},
    }
    worker.runtime = runtime

    async def stream(**_kwargs):
        if delivered:
            artifact = ({'slot': 'native_pptx', 'content_type': 'file', 'value': {'path': str(tmp_path / 'deck.pptx')}}
                        if output_format == 'native_pptx' else
                        {'slot': 'preview_html', 'content_type': 'text', 'value': {'text': '<html>Slide</html>'}})
            yield 'data: ' + json.dumps({'type': 'artifact', 'seq': 1, **artifact}) + '\n\n'
        yield 'data: ' + json.dumps({'type': 'done', 'status': status,
                                    'summary': '未重新生成幻灯片' if not delivered else 'Saved'}) + '\n\n'

    monkeypatch.setattr(runner, 'run_subagent_stream', stream)
    await worker._run_claim(object(), {'attempt_id': 'attempt', 'lease_token': 'lease'})
    if status == 'interrupted':
        package_read.assert_not_called()
        runtime.cancel.assert_awaited_once()
        runtime.complete.assert_not_awaited()
        runtime.fail.assert_not_awaited()
    elif delivered:
        package_read.assert_called_once_with('ppt-workflow', revision)
        runtime.fail.assert_not_awaited()
        runtime.complete.assert_awaited_once()
        if output_format == 'native_pptx':
            runtime.upload_artifact_file.assert_awaited_once()
    else:
        runtime.complete.assert_not_awaited()
        runtime.fail.assert_awaited_once()
        assert 'PPT_DELIVERY_MISSING' in runtime.fail.call_args.args[3]
        assert runtime.task_event.call_args.args[-1]['status'] == 'failed'

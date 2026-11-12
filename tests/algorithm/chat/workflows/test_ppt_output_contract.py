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
        {'slot': 'presentation_output_format', 'selected': True, 'revision': 1,
         'value': 'native_pptx' if mode == 'upload' else 'html'},
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
    monkeypatch.setattr(tools, '_resolve_artifact_text', lambda _ctx, key: ('## 封面\n标题' if key == 'deck_outline' else None, 'text'))
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
    assert 'native PPTX' in step['prompt']
    assert 'HTML uses 1600×900' in step['prompt']


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
                                     supplied={'topic': '研究报告', 'slide_count': '8', 'template_pptx': 'company.pptx'})
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
    values, missing = resolve_fields(policy, '主题：城市介绍，8页，不生成AI底图', supplied={
        'topic': '城市介绍', 'slide_count': '8', 'generate_background_images': 'disabled',
    })
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
                                     supplied={'topic': '研究报告', 'slide_count': '8',
                                               'template_pptx': 'template.pptx',
                                               'presentation_output_format': expected})
    assert values['presentation_output_format'] == expected and missing == []


@pytest.mark.parametrize('brief,output_format', [
    ('转正答辩，生成12 页pptx，十五分钟演讲、五分钟问答', 'native_pptx'),
    ('请按模板制作十二页可编辑的 PowerPoint 文件', 'native_pptx'),
    ('沿用模板风格做12页网页幻灯片', 'html'),
])
def test_template_output_accepts_model_extraction_from_user_request(brief, output_format):
    from lazymind.chat.workflow.clarification import resolve_fields
    policy = yaml.safe_load((ROOT / 'workflows/ppt-workflow/workflow.yaml').read_text())['runtime']
    values, missing = resolve_fields(policy, brief, supplied={
        'topic': '转正答辩', 'slide_count': '12', 'template_pptx': 'template.pptx',
        'presentation_output_format': output_format,
    })
    assert missing == []
    assert values['presentation_output_format'] == output_format


def test_confirmed_user_output_choice_takes_precedence_over_model_binding():
    from lazymind.chat.workflow.clarification import resolve_fields
    policy = yaml.safe_load((ROOT / 'workflows/ppt-workflow/workflow.yaml').read_text())['runtime']
    values, missing = resolve_fields(policy, '主题：研究报告，8页，输出 HTML', supplied={
        'topic': '研究报告', 'slide_count': '8', 'template_pptx': 'template.pptx',
        'presentation_output_format': 'native_pptx',
    }, previous={'presentation_output_format': 'HTML｜参考模板样式生成'})
    assert missing == []
    assert values['presentation_output_format'] == 'html'


def test_missing_saved_output_format_never_infers_a_legacy_route(monkeypatch, tools):
    monkeypatch.setattr(tools, '_workflow_client', lambda: SimpleNamespace(
        list_artifacts=lambda _: SimpleNamespace(result={'artifacts': [
            {'slot': 'template_mode', 'value': 'upload'},
        ]})))
    with pytest.raises(tools.ToolExecutionError, match='missing presentation_output_format'):
        tools._required_output_format()

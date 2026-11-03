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
    monkeypatch.setattr(tools, '_resolve_artifact_text', lambda *a: ('## 封面\n标题', 'text'))
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
    state = yaml.safe_load((ROOT / 'workflows/ppt-workflow/scenario/state.yml').read_text())
    step = state['steps']['plan_page_prompts']
    assert {'material': 'template_mode', 'required': True} in step['inputs']
    assert 'output_format="native_pptx"' in step['prompt']
    assert 'HTML ONLY (template_mode=none)' in step['prompt']

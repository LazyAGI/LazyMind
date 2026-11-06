"""Semantic font requests use installed families without rewriting slide text."""
import importlib.util
import json
from pathlib import Path

import pytest


@pytest.fixture
def tools(monkeypatch):
    path = Path(__file__).resolve().parents[4] / 'workflows/ppt-workflow/scripts/tools.py'
    spec = importlib.util.spec_from_file_location('ppt_font_selection_test', path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    from lazymind.chat.engine.tools.file_resources import presentation_edit
    monkeypatch.setattr(module, 'available_font_families', lambda: {'Songti SC', '宋体-简', 'Arial'})
    monkeypatch.setattr(presentation_edit, 'available_font_families', module.available_font_families)
    return module


@pytest.mark.parametrize('command', [
    '换成宋体', '改成宋体', '标题改成宋体', '换个正式点的字体', '字体正式点，18pt', '换成宋体并加粗',
])
def test_semantic_font_choice_preserves_text_and_neighbor(tools, monkeypatch, command):
    def model(system, prompt, **kwargs):
        payload = json.loads(prompt)
        assert payload['instruction'] == command
        assert set(payload['available_font_families']) == {'Songti SC', '宋体-简', 'Arial'}
        assert payload['selected_element']['text'] == 'Original'
        return json.dumps({'op': 'set_style', 'styles': {'font-family': '宋体-简', 'font-size': '18pt'}})
    monkeypatch.setattr(tools, '_agent_llm_call', model)
    html = '<h1 data-el="title"><span>Original</span></h1><p>Keep</p>'
    tree = tools._HtmlTree(html)
    ops, old, new = tools._single_selection_edit_ops(command, {'el': 'title'}, tree)
    assert old == new == 'Original'
    assert ops[0]['op'] == 'set_style'
    assert ops[0]['styles']['font-family'] == '"宋体-简"'
    index = tools._resolve_el(tree, 'title', {})[0]
    result = tools._set_inline_styles(html, tree.nodes[index], ops[0]['styles'])
    assert '<span style=' in result and '18pt' in result
    assert '>Original</span>' in result and '<p>Keep</p>' in result


@pytest.mark.parametrize('plan', [
    {'op': 'font_unavailable', 'family': 'SimSun'},
    {'op': 'set_style', 'styles': {'font-family': 'Invented Font'}},
    {'op': 'set_style', 'styles': {'font-family': 'Arial, serif'}},
])
def test_missing_or_invented_family_is_rejected(tools, monkeypatch, plan):
    from lazymind.chat.engine.tools.file_resources.font_library import FontUnavailableError
    monkeypatch.setattr(tools, '_agent_llm_call', lambda *a, **kw: json.dumps(plan))
    with pytest.raises(FontUnavailableError):
        tools._single_selection_edit_ops('字体换成 SimSun', {'el': 'title'},
                                         tools._HtmlTree('<h1 data-el="title">Original</h1>'))


def test_plain_text_rename_uses_semantic_planner(tools, monkeypatch):
    monkeypatch.setattr(tools, '_agent_llm_call', lambda *a, **kw:
                        json.dumps({'op': 'replace_text', 'value': '新标题'}))
    ops, _, new = tools._single_selection_edit_ops(
        '改成新标题', {'el': 'title'}, tools._HtmlTree('<h1 data-el="title">Original</h1>'),
    )
    assert ops[0]['op'] == 'replace_text'
    assert new == '新标题'

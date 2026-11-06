"""Short font requests must not become text replacement or model guesses."""
import importlib.util
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
    monkeypatch.setattr(module, '_agent_llm_call', lambda *a, **kw: pytest.fail('font edits must not call a model'))
    return module


@pytest.mark.parametrize('command', ['换成宋体', '改成宋体', '字体换成宋体'])
def test_missing_short_font_returns_explicit_error(tools, command):
    from lazymind.chat.engine.tools.file_resources.font_library import FontUnavailableError
    with pytest.raises(FontUnavailableError):
        tools._single_selection_edit_ops(command, {'el': 'title'},
                                         tools._HtmlTree('<h1 data-el="title">Original</h1>'))


@pytest.mark.parametrize('command', ['换成宋体-简', '改成“宋体-简”', '改用 Songti SC 字体', '换成Arial'])
def test_short_installed_font_edits_style_without_changing_text(tools, command):
    html = '<h1 data-el="title"><span>Original</span></h1><p>Keep</p>'
    tree = tools._HtmlTree(html)
    ops, old, new = tools._single_selection_edit_ops(command, {'el': 'title'}, tree)
    assert old == new == 'Original'
    assert ops[0]['op'] == 'set_style'
    index = tools._resolve_el(tree, 'title', {})[0]
    result = tools._set_inline_styles(html, tree.nodes[index], ops[0]['styles'])
    assert '<span style=' in result
    assert '>Original</span>' in result and '<p>Keep</p>' in result


def test_plain_text_rename_is_not_misclassified_as_font(tools):
    ops, _, new = tools._single_selection_edit_ops(
        '改成新标题', {'el': 'title'}, tools._HtmlTree('<h1 data-el="title">Original</h1>'),
    )
    assert ops[0]['op'] == 'replace_text'
    assert new == '新标题'

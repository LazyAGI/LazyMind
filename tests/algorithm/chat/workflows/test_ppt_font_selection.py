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


def test_whole_deck_generic_font_uses_available_variant(tools, monkeypatch):
    from lazymind.chat.engine.tools.file_resources import font_library

    def model(_system, prompt, **_kwargs):
        assert json.loads(prompt)['requested_font'] == '宋体'
        return '{"op":"set_font","family":"宋体-简"}'
    monkeypatch.setattr(tools, '_agent_llm_call', model)
    monkeypatch.setattr(font_library, 'ensure_font', lambda family, allow: family)
    assert tools._resolve_requested_font('宋体', False) == '宋体-简'


def test_whole_deck_missing_exact_font_is_rejected(tools, monkeypatch):
    from lazymind.chat.engine.tools.file_resources.font_library import FontUnavailableError
    monkeypatch.setattr(tools, '_agent_llm_call', lambda *a, **kw: '{"op":"font_unavailable"}')
    with pytest.raises(FontUnavailableError):
        tools._resolve_requested_font('SimSun', False)


def test_whole_deck_request_persists_and_publishes_resolved_font(tools, monkeypatch, tmp_path):
    from lazymind.chat.engine.tools.file_resources import font_library
    pages = tmp_path / 'pages'
    pages.mkdir()
    for page in (1, 2):
        (pages / f'page_{page:03}.html').write_text('<html><body><h1>Title</h1><p>Body</p></body></html>')
    monkeypatch.setattr(tools, '_resolve_deck_dir', lambda _: tmp_path)
    monkeypatch.setattr(tools, '_agent_llm_call', lambda *a, **kw: '{"op":"set_font","family":"宋体-简"}')
    monkeypatch.setattr(font_library, 'ensure_font', lambda family, allow: family)
    # Font binaries and artifact publication are external boundaries in this routing test.
    monkeypatch.setattr(tools, '_prepare_portable_font', lambda family: (family, []))
    monkeypatch.setattr(tools, '_package_edited_html_fonts', lambda html, *a: html)
    published = []

    def publish(deck, numbers, **kwargs):
        published.extend(numbers)
        return {'published_count': len(numbers), 'failed_count': 0}
    monkeypatch.setattr(tools, '_publish_pages_from_disk', publish)
    result = tools.ppt_replace_deck_fonts(str(tmp_path), target_font='宋体', allow_font_download=False)
    assert result['target_font'] == '宋体-简'
    assert published == [1, 2]
    for page in pages.glob('*.html'):
        html = page.read_text()
        assert '宋体-简' in html and '>Title<' in html and '>Body<' in html
    assert '宋体-简' in json.loads((tmp_path / 'style_spec.json').read_text())['typography']['body_font']


@pytest.mark.parametrize('stage', ['page-html', 'batch-page-html'])
def test_redraw_preserves_deck_and_page_font_choices(tools, monkeypatch, tmp_path, stage):
    from types import SimpleNamespace
    from lazymind.chat.engine.tools.file_resources import font_library

    pages = tmp_path / 'pages'
    pages.mkdir()
    document = '<html><head></head><body><h1>Title</h1><p>Body</p></body></html>'
    for page in (1, 2):
        (pages / f'page_{page:03d}.html').write_text(document)
    (tmp_path / 'complete_page_prompts.json').write_text('{}')
    (tmp_path / 'outline.json').write_text(json.dumps({'pages': [{'page_no': 1}, {'page_no': 2}]}))
    monkeypatch.setattr(tools, '_resolve_deck_dir', lambda _: tmp_path)
    monkeypatch.setattr(font_library, 'ensure_font', lambda family, _allow: family)
    monkeypatch.setattr(tools, '_prepare_portable_font', lambda family: (family, []))
    monkeypatch.setattr(tools, '_package_edited_html_fonts', lambda html, *_args: html)
    monkeypatch.setattr(tools, '_publish_pages_from_disk', lambda _deck, numbers, **_kwargs: {
        'published_count': len(numbers), 'failed_count': 0,
    })
    monkeypatch.setattr(tools, '_publish_one_page', lambda *_args, **_kwargs: {'ok': True})
    monkeypatch.setattr(tools, '_ui_slot_order_list', lambda _slot: [])
    monkeypatch.setattr(tools, '_load_slide_outline_briefs',
                        lambda numbers: {number: 'Approved page brief' for number in numbers})

    tools.ppt_replace_deck_fonts(str(tmp_path), target_font='Arial', allow_font_download=False)
    tools.ppt_replace_deck_fonts(
        str(tmp_path), target_font='Songti SC', pages=2, text_scope='titles', allow_font_download=False,
    )
    original_first_page = (pages / 'page_001.html').read_text()

    def render(_command, _deck, number, *_args, **_kwargs):
        (pages / f'page_{number:03d}.html').write_text(document.replace('Title', 'Updated title'))
        return 0, {'status': 'ok'}

    runtime = SimpleNamespace(_capture_cmd=render, cmd_page_html_from_brief=object())
    monkeypatch.setattr(tools, '_load_sn_ppt_modules', lambda: (None, runtime))
    result = tools._run_stage_inprocess(stage, tmp_path, page=2)

    assert result['status'] == 'ok'
    second = (pages / 'page_002.html').read_text()
    assert 'Updated title' in second
    assert tools._read_page_font_overrides(second) == {'all': 'Arial', 'titles': 'Songti SC'}
    title, body = (next(n for n in tools._HtmlTree(second).nodes if n['tag'] == tag) for tag in ('h1', 'p'))
    assert 'Songti SC' in title['attrs']['style']
    assert 'Arial' in body['attrs']['style']
    first = (pages / 'page_001.html').read_text()
    if stage == 'page-html':
        assert first == original_first_page
    else:
        assert 'Updated title' in first and tools._read_page_font_overrides(first) == {'all': 'Arial'}

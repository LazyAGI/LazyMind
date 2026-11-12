"""Real HTML patch paths: scoped batches and publication recovery."""
import importlib.util
from pathlib import Path

import pytest

ROOT = Path(__file__).resolve().parents[4]
HTML = ('<!doctype html><html><head><style>.keep{color:red}</style></head><body>'
        '<div id="ct"><section data-el="content">'
        '<ul data-el="list"><li>A</li><li>B</li></ul>'
        '<table data-el="table"><tbody><tr><td>H1</td><td>H2</td></tr>'
        '<tr><td style="font-weight:bold">A</td><td>1</td></tr></tbody></table>'
        '<p data-el="wide" style="width:200px">Keep</p>'
        '</section><aside data-el="other">Untouched</aside></div></body></html>')


@pytest.fixture
def tools():
    spec = importlib.util.spec_from_file_location(
        'ppt_structure_helpers', ROOT / 'workflows/ppt-workflow/scripts/tests/test_style_flow.py')
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module.TOOLS


@pytest.fixture
def deck(tmp_path, tools, monkeypatch):
    (tmp_path / 'pages').mkdir()
    for page in range(1, 4):
        (tmp_path / 'pages' / f'page_{page:03d}.html').write_text(HTML, encoding='utf-8')
    monkeypatch.setattr(tools, '_resolve_deck_dir', lambda _: tmp_path)
    monkeypatch.setattr(tools, '_outline_still_has', lambda *_: [])
    return tmp_path


def test_html_table_and_list_add_update_delete_keep_shell_bytes(tools):
    ops = [
        {'op': 'table_set_cell', 'el': 'table', 'row': 2, 'column': 2, 'text': '2 < 3'},
        {'op': 'table_insert_row', 'el': 'table', 'index': 3, 'values': ['B', '4']},
        {'op': 'table_insert_column', 'el': 'table', 'index': 3, 'values': ['H3', 'x', 'y']},
        {'op': 'table_delete_row', 'el': 'table', 'index': 1},
        {'op': 'table_delete_column', 'el': 'table', 'index': 3},
        {'op': 'list_insert_item', 'el': 'list', 'index': 2, 'text': 'C'},
        {'op': 'list_set_item', 'el': 'list', 'index': 3, 'text': 'D'},
        {'op': 'list_delete_item', 'el': 'list', 'index': 1},
        {'op': 'adjust_style', 'el': 'wide', 'deltas': {'width': 50}},
    ]
    result, _, _, _ = tools._apply_html_ops(HTML, ops)
    assert '<ul data-el="list"><li>C</li><li>D</li></ul>' in result
    assert '<tr><td style="font-weight:bold">A</td><td>2 &lt; 3</td></tr>' in result
    assert 'width: 250px' in result
    before, after = tools._HtmlTree(HTML), tools._HtmlTree(result)
    for el in ('other',):
        a = before.nodes[tools._resolve_el(before, el, {})[0]]
        b = after.nodes[tools._resolve_el(after, el, {})[0]]
        assert HTML[a['start']:a['end']] == result[b['start']:b['end']]
    assert '<head><style>.keep{color:red}</style></head>' in result
    added, *_ = tools._apply_html_ops(result, [
        {'op': 'add_list', 'el': 'ct', 'new_el': 'new-list', 'values': ['<safe>', 'B'], 'ordered': True},
        {'op': 'add_table', 'el': 'content', 'new_el': 'new-table', 'rows': [['A', 'B']]},
        {'op': 'add_text', 'el': 'content', 'new_el': 'new-text', 'text': 'Hello'},
        {'op': 'set_style', 'els': ['new-list', 'new-text'], 'styles': {'color': '#FF0000'}},
    ])
    assert '&lt;safe&gt;' in added
    inventory = tools._element_inventory(tools._HtmlTree(added))['elements']
    assert next(e for e in inventory if e['el'] == 'new-table')['rows'] == [['A', 'B']]
    removed, *_ = tools._apply_html_ops(added, [{'op': 'delete_node', 'els': ['new-list', 'new-table', 'new-text']}])
    assert removed == result


def test_cross_page_batch_validates_before_any_write_and_publication(tools, deck, monkeypatch):
    published = []
    monkeypatch.setattr(tools, '_publish_pages_from_disk', lambda *a, **kw:
                        published.append(a) or {'published_count': 2, 'failed': []})
    edits = [{'page': p, 'expected_sha256': tools._html_sha256(HTML),
              'ops': [{'op': 'replace_text', 'el': 'wide', 'value': 'Updated'}]} for p in (1, 2)]
    edits[1]['expected_sha256'] = 'stale'
    with pytest.raises(RuntimeError, match='pages unchanged'):
        tools.ppt_edit_pages_html(str(deck), edits)
    assert published == []
    assert (deck / 'pages/page_001.html').read_text() == HTML
    edits[1]['expected_sha256'] = tools._html_sha256(HTML)
    result = tools.ppt_edit_pages_html(str(deck), edits)
    assert result['published_count'] == 2
    assert result['regenerated_pages'] == []
    assert len(published) == 1
    assert (deck / 'pages/page_003.html').read_text() == HTML
    assert 'Updated' in (deck / 'pages/page_001.html').read_text()


def test_failed_publish_restores_both_pages_and_reports_rollback_failure(tools, deck, monkeypatch):
    monkeypatch.setattr(tools, '_publish_pages_from_disk', lambda *a, **kw:
                        {'published_count': 0, 'failed': ['service unavailable']})
    with pytest.raises(RuntimeError, match='Preview restoration'):
        tools.ppt_edit_pages_html(str(deck), [
            {'page': p, 'expected_sha256': tools._html_sha256(HTML),
             'ops': [{'op': 'list_delete_item', 'el': 'list', 'index': 1}]} for p in (1, 2)])
    assert all((deck / 'pages' / f'page_{p:03d}.html').read_text() == HTML for p in (1, 2, 3))


@pytest.mark.parametrize('op', [
    {'op': 'table_insert_row', 'el': 'table', 'index': 3, 'values': ['wrong dimension']},
    {'op': 'table_delete_column', 'el': 'table', 'index': 0},
    {'op': 'list_delete_item', 'el': 'list', 'index': 99},
    {'op': 'add_text', 'el': 'ct', 'new_el': 'table', 'text': 'duplicate'},
    {'op': 'set_list', 'el': 'ct', 'values': ['destroy shell']},
])
def test_invalid_structural_operation_is_rejected(tools, op):
    with pytest.raises(ValueError):
        tools._apply_html_ops(HTML, [op])


@pytest.mark.parametrize('instruction', [
    '把第二页卡片加宽，不要重新生成', '表格加一列负责人',
    '删除列表第二项', 'add a row to this table, do not regenerate',
])
def test_local_structure_request_cannot_fall_back_to_generation(tools, deck, instruction):
    with pytest.raises(RuntimeError, match='No generation stage'):
        tools._reject_font_regeneration(deck, instruction)


def test_explicit_new_page_or_redesign_can_generate(tools, deck):
    tools._reject_font_regeneration(deck, '重新生成这页，增加一个表格')
    tools._reject_font_regeneration(deck, '新增一页，放一个表格')

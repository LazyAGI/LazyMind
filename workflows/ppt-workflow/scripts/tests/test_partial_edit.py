import importlib.util
import json
import tempfile
import unittest
from pathlib import Path
from unittest import mock


TOOLS_PATH = Path(__file__).resolve().parents[1] / 'tools.py'
SPEC = importlib.util.spec_from_file_location('ppt_workflow_tools_partial_edit_test', TOOLS_PATH)
TOOLS = importlib.util.module_from_spec(SPEC)
assert SPEC.loader is not None
SPEC.loader.exec_module(TOOLS)


PAGE_HTML = """<!DOCTYPE html>
<html><head><title>Old title</title></head><body>
<div class="wrapper"><div id="bg"></div><div id="ct">
  <h1 data-el="title">Old title</h1>
  <div data-el="bullet-1"><span>repeat</span><b>repeat</b></div>
</div></div></body></html>
"""

LEGACY_PAGE_HTML = """<!doctype html>
<html><head><title>Legacy title</title></head><body>
<main class="slide"><h1 data-el="title">Legacy title</h1></main>
</body></html>
"""

LEGACY_CARD_HTML = """<!doctype html>
<html><head><title>Cards</title><style>.cards{display:grid;grid-template-columns:repeat(2,1fr)}</style></head><body>
<main class="slide"><div class="cards">
  <div class="card"><h3 data-el="card-1-head">First</h3><p data-el="card-1-detail">First detail</p></div>
  <div class="card"><h3 data-el="card-2-head">Second</h3><p data-el="card-2-detail">Second detail</p></div>
</div></main></body></html>
"""

NESTED_TITLE_HTML = """<!doctype html>
<html><head><title>Game report</title></head><body>
<main class="slide"><section data-el="title">
  <span>2025 年度报告</span><h1>2025游戏行业趋势</h1><p>全球市场洞察</p>
</section></main></body></html>
"""

DUPLICATE_TITLE_HTML = """<!doctype html>
<html><head><title>Game report</title></head><body>
<main class="slide">
  <div class="page-label" data-el="title">CYBERPUNK 2077</div>
  <h1 class="main-title" data-el="title"><span>赛博朋克</span>2077</h1>
</main></body></html>
"""


def make_deck(root: Path) -> tuple[Path, Path]:
    deck = root / 'deck'
    pages = deck / 'pages'
    pages.mkdir(parents=True)
    (deck / 'task_pack.json').write_text('{}', encoding='utf-8')
    (deck / 'info_pack.json').write_text('{}', encoding='utf-8')
    (deck / 'outline.json').write_text(json.dumps({
        'pages': [{
            'page_no': 1,
            'title': 'Old title',
            'bullets': [{'head': 'repeat', 'detail': 'detail'}],
        }],
    }), encoding='utf-8')
    page = pages / 'page_001.html'
    page.write_text(PAGE_HTML, encoding='utf-8')
    return deck, page


class PartialEditTests(unittest.TestCase):
    def test_selection_occurrence_disambiguates_duplicate_data_el(self):
        selection = {
            'type': 'ppt_html',
            'page': 1,
            'el': 'title',
            'index': 2,
            'selected_text': '赛博朋克2077',
        }
        with mock.patch.object(TOOLS, '_agent_llm_call') as llm:
            ops, old_text, new_text = TOOLS._selection_edit_ops(
                '改为赛博朋克2077游戏介绍',
                selection,
                TOOLS._HtmlTree(DUPLICATE_TITLE_HTML),
            )
        self.assertEqual(ops, [{
            'op': 'replace_text',
            'el': 'title',
            'index': 2,
            'value': '赛博朋克2077游戏介绍',
            'scope': 'element',
        }])
        self.assertIn('赛博朋克', old_text)
        self.assertEqual(new_text, '赛博朋克2077游戏介绍')
        llm.assert_not_called()

        edited, _applied, _notes, _removed = TOOLS._apply_html_ops(
            DUPLICATE_TITLE_HTML, ops,
        )
        self.assertIn('data-el="title">CYBERPUNK 2077</div>', edited)
        self.assertIn(
            'data-el="title">赛博朋克2077游戏介绍</h1>', edited,
        )
        self.assertNotIn('赛博朋克2077游戏介绍2077', edited)

    def test_duplicate_data_el_without_occurrence_remains_rejected(self):
        with self.assertRaisesRegex(ValueError, 'is not unique'):
            TOOLS._selection_edit_ops(
                '改为赛博朋克2077游戏介绍',
                {'type': 'ppt_html', 'page': 1, 'el': 'title'},
                TOOLS._HtmlTree(DUPLICATE_TITLE_HTML),
            )

    def test_duplicate_selection_preview_preserves_occurrence_index(self):
        with tempfile.TemporaryDirectory() as tmp:
            encoded = TOOLS.base64.b64encode(
                DUPLICATE_TITLE_HTML.encode('utf-8'),
            ).decode('ascii')
            artifact = {'path': f'data:text/plain;base64,{encoded}', 'type': 'text'}
            preview = TOOLS.ppt_preview_selection_edit(
                artifact=artifact,
                instruction='改为赛博朋克2077游戏介绍',
                selection={
                    'type': 'ppt_html', 'page': 1, 'el': 'title', 'index': 2,
                    'selected_text': '赛博朋克2077',
                },
                artifact_store=tmp,
                slot='preview_html',
            )

        self.assertEqual(preview['target']['index'], 2)
        self.assertEqual(preview['target']['block_type'], 'h1')
        self.assertIn('CYBERPUNK 2077', preview['candidate_html'])
        self.assertIn('赛博朋克2077游戏介绍', preview['candidate_html'])

    def test_delete_this_item_removes_legacy_outer_card(self):
        selection = {
            'type': 'ppt_html',
            'page': 1,
            'el': 'card-1-head',
        }
        with mock.patch.object(TOOLS, '_agent_llm_call') as llm:
            ops, old_text, new_text = TOOLS._selection_edit_ops(
                '删除这一条', selection, TOOLS._HtmlTree(LEGACY_CARD_HTML),
            )
        self.assertEqual(ops, [{
            'op': 'delete_node',
            'el': 'card-1-head',
            'scope': 'item',
        }])
        self.assertIn('First detail', old_text)
        self.assertEqual(new_text, '')
        llm.assert_not_called()

        edited, _applied, notes, removed = TOOLS._apply_html_ops(LEGACY_CARD_HTML, ops)
        TOOLS._validate_local_html_edit(LEGACY_CARD_HTML, edited)
        self.assertNotIn('class="card"><h3 data-el="card-1-head"', edited)
        self.assertNotIn('First detail', edited)
        self.assertIn('card-2-head', edited)
        self.assertIn('Second detail', edited)
        self.assertIn('First detail', removed[0])
        self.assertIn('grid tracks reduced 2 -> 1', notes)

    def test_legacy_data_uri_artifact_can_preview_and_persist(self):
        with tempfile.TemporaryDirectory() as tmp:
            encoded = TOOLS.base64.b64encode(LEGACY_PAGE_HTML.encode('utf-8')).decode('ascii')
            artifact = {'path': f'data:text/plain;base64,{encoded}', 'type': 'text'}
            preview = TOOLS.ppt_preview_selection_edit(
                artifact=artifact,
                instruction='修改标题成 Legacy edited',
                selection={'type': 'ppt_html', 'page': 1, 'el': 'title'},
                artifact_store=tmp,
                slot='preview_html',
            )
            self.assertIn('Legacy edited', preview['candidate_html'])
            manifest = json.loads((
                Path(tmp) / 'ppt-selection-actions' / f"{preview['commit']['token']}.json"
            ).read_text(encoding='utf-8'))
            self.assertEqual(manifest['mode'], 'artifact_only')

            applied = TOOLS.ppt_apply_selection_edit(
                commit_token=preview['commit']['token'],
                artifact=artifact,
                artifact_store=tmp,
                slot='preview_html',
            )
            self.assertIn('Legacy edited', applied['artifact']['value'])

    def test_replacement_is_html_escaped_and_title_stays_in_sync(self):
        edited, _applied, _notes, removed = TOOLS._apply_html_ops(PAGE_HTML, [{
            'op': 'replace_text',
            'el': 'title',
            'value': 'A < B & C',
        }])
        TOOLS._validate_local_html_edit(PAGE_HTML, edited)
        self.assertIn('<h1 data-el="title">A &lt; B &amp; C</h1>', edited)
        self.assertIn('<title>A &lt; B &amp; C</title>', edited)
        self.assertNotIn('<h1 data-el="title">A < B & C</h1>', edited)
        self.assertIn('Old title', removed)

    def test_clicked_nested_text_scopes_replacement_inside_outer_data_el(self):
        selection = {
            'type': 'ppt_html',
            'page': 1,
            'el': 'title',
            'selected_text': '2025游戏行业趋势',
        }
        with mock.patch.object(TOOLS, '_agent_llm_call') as llm:
            ops, old_text, new_text = TOOLS._selection_edit_ops(
                '修改为赛博朋克2077介绍', selection, TOOLS._HtmlTree(NESTED_TITLE_HTML),
            )
        self.assertEqual(ops, [{
            'op': 'replace_text',
            'el': 'title',
            'value': '赛博朋克2077介绍',
            'match': '2025游戏行业趋势',
        }])
        self.assertEqual(old_text, '2025游戏行业趋势')
        self.assertEqual(new_text, '赛博朋克2077介绍')
        llm.assert_not_called()

        edited, _applied, _notes, _removed = TOOLS._apply_html_ops(
            NESTED_TITLE_HTML, ops,
        )
        self.assertIn('<h1>赛博朋克2077介绍</h1>', edited)
        self.assertIn('<span>2025 年度报告</span>', edited)
        self.assertIn('<p>全球市场洞察</p>', edited)

    def test_llm_expansion_uses_final_json_and_keeps_clicked_text_scope(self):
        selection = {
            'type': 'ppt_html',
            'page': 1,
            'el': 'title',
            'selected_text': '2025游戏行业趋势',
        }
        model_output = (
            '{"op":"replace_text","value":"初稿"}\n'
            '{"op":"replace_text","value":"赛博朋克2077游戏行业趋势与未来展望"}'
        )
        with mock.patch.object(TOOLS, '_agent_llm_call', return_value=model_output):
            ops, old_text, new_text = TOOLS._selection_edit_ops(
                '扩写这一句', selection, TOOLS._HtmlTree(NESTED_TITLE_HTML),
            )
        self.assertEqual(ops, [{
            'op': 'replace_text',
            'el': 'title',
            'value': '赛博朋克2077游戏行业趋势与未来展望',
            'match': '2025游戏行业趋势',
        }])
        self.assertEqual(old_text, '2025游戏行业趋势')
        self.assertEqual(new_text, '赛博朋克2077游戏行业趋势与未来展望')

    def test_nested_ambiguous_match_is_rejected(self):
        with self.assertRaisesRegex(ValueError, 'contains 2 visible matches'):
            TOOLS._apply_html_ops(PAGE_HTML, [{
                'op': 'replace_text',
                'el': 'bullet-1',
                'match': 'repeat',
                'value': 'new',
            }])

    def test_selected_element_style_is_scoped_and_sanitized(self):
        edited, applied, _notes, _removed = TOOLS._apply_html_ops(PAGE_HTML, [{
            'op': 'set_style',
            'el': 'title',
            'styles': {'font-size': '48px', 'color': '#ffffff'},
        }])
        TOOLS._validate_local_html_edit(PAGE_HTML, edited)
        self.assertIn(
            '<h1 data-el="title" style="font-size: 48px; color: #ffffff">',
            edited,
        )
        self.assertEqual(len(applied), 1)
        with self.assertRaisesRegex(ValueError, 'unsafe CSS value'):
            TOOLS._apply_html_ops(PAGE_HTML, [{
                'op': 'set_style',
                'el': 'title',
                'styles': {'background': 'url(javascript:alert(1))'},
            }])

    def test_common_relative_css_requests_do_not_call_llm(self):
        selection = {
            'type': 'ppt_html',
            'page': 1,
            'el': 'title',
            'computed_style': {
                'font_size': '40px',
                'width': '600px',
                'height': '80px',
            },
        }
        with mock.patch.object(TOOLS, '_agent_llm_call') as llm:
            ops, old_text, new_text = TOOLS._selection_edit_ops(
                '字体变大，宽度变小', selection, TOOLS._HtmlTree(PAGE_HTML),
            )
        self.assertEqual(ops, [{
            'op': 'set_style',
            'el': 'title',
            'styles': {'font-size': '46px', 'width': '510px'},
        }])
        self.assertEqual(old_text, 'Old title')
        self.assertEqual(new_text, old_text)
        llm.assert_not_called()

    def test_exact_css_sizes_and_alignment_are_deterministic(self):
        with mock.patch.object(TOOLS, '_agent_llm_call') as llm:
            ops, _old_text, _new_text = TOOLS._selection_edit_ops(
                '字号改为 32px，宽度设置为 50%，文字居中并加粗',
                {'type': 'ppt_html', 'page': 1, 'el': 'title'},
                TOOLS._HtmlTree(PAGE_HTML),
            )
        self.assertEqual(ops[0]['styles'], {
            'font-size': '32px',
            'width': '50%',
            'font-weight': '700',
            'text-align': 'center',
        })
        llm.assert_not_called()

    def test_remove_background_is_a_style_edit_not_node_deletion(self):
        with mock.patch.object(TOOLS, '_agent_llm_call') as llm:
            ops, _old_text, _new_text = TOOLS._selection_edit_ops(
                '去掉背景色',
                {'type': 'ppt_html', 'page': 1, 'el': 'title'},
                TOOLS._HtmlTree(PAGE_HTML),
            )
        self.assertEqual(ops, [{
            'op': 'set_style',
            'el': 'title',
            'styles': {'background': 'transparent'},
        }])
        llm.assert_not_called()

    def test_selection_preview_then_apply_updates_export_source(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            deck, page = make_deck(root)
            public, _ = TOOLS._inline_preview_images(
                TOOLS._sanitize_page_html(PAGE_HTML), deck, page,
            )
            artifact = TOOLS._with_ppt_source_meta(
                public, page, TOOLS._html_sha256(PAGE_HTML),
            )
            preview = TOOLS.ppt_preview_selection_edit(
                artifact=artifact,
                instruction='修改标题成 New title',
                selection={'type': 'ppt_html', 'page': 1, 'el': 'title'},
                artifact_store=str(root / 'artifacts'),
                slot='preview_html',
            )
            self.assertEqual(preview['preview']['new_text'], 'New title')
            self.assertIn('New title', preview['candidate_html'])
            self.assertEqual(page.read_text(encoding='utf-8'), PAGE_HTML)

            applied = TOOLS.ppt_apply_selection_edit(
                commit_token=preview['commit']['token'],
                artifact_store=str(root / 'artifacts'),
                slot='preview_html',
            )
            self.assertEqual(applied['representation'], 'ppt_html')
            self.assertIn('New title', page.read_text(encoding='utf-8'))

    def test_read_hash_guards_against_stale_edit(self):
        with tempfile.TemporaryDirectory() as tmp:
            deck, page = make_deck(Path(tmp))
            read = TOOLS.ppt_read_page_html(str(deck), 1)
            current_hash = read['result']['html_sha256']
            self.assertEqual(current_hash, TOOLS._html_sha256(PAGE_HTML))

            result = TOOLS.ppt_edit_page_html(
                str(deck), 1,
                [{'op': 'replace_text', 'el': 'title', 'value': 'new'}],
                expected_sha256='0' * 64,
            )
            self.assertFalse(result['success'])
            self.assertEqual(page.read_text(encoding='utf-8'), PAGE_HTML)

    def test_edit_requires_hash_from_immediately_preceding_read(self):
        with tempfile.TemporaryDirectory() as tmp:
            deck, page = make_deck(Path(tmp))
            result = TOOLS.ppt_edit_page_html(
                str(deck), 1,
                [{'op': 'replace_text', 'el': 'title', 'value': 'new'}],
            )
            self.assertFalse(result['success'])
            self.assertIn('expected_sha256 is required', result['error']['reason'])
            self.assertEqual(page.read_text(encoding='utf-8'), PAGE_HTML)

    def test_failed_publish_rolls_the_page_back(self):
        with tempfile.TemporaryDirectory() as tmp:
            deck, page = make_deck(Path(tmp))
            failed_publish = {
                'published_count': 0,
                'failed': [{'page': 1, 'error': 'simulated'}],
            }
            with mock.patch.object(
                TOOLS, '_publish_pages_from_disk', return_value=failed_publish,
            ) as publish:
                result = TOOLS.ppt_edit_page_html(
                    str(deck), 1,
                    [{'op': 'replace_text', 'el': 'title', 'value': 'new'}],
                    expected_sha256=TOOLS._html_sha256(PAGE_HTML),
                )
            self.assertFalse(result['success'])
            self.assertEqual(page.read_text(encoding='utf-8'), PAGE_HTML)
            self.assertEqual(publish.call_count, 2)

    def test_page_publisher_propagates_artifact_failure(self):
        with tempfile.TemporaryDirectory() as tmp:
            deck, _page = make_deck(Path(tmp))
            with mock.patch.object(
                TOOLS,
                '_save_artifact',
                return_value={'success': False, 'error': {'reason': 'simulated'}},
            ):
                result = TOOLS._publish_one_page(deck, 1)
            self.assertFalse(result['ok'])
            self.assertIn('preview_html publish failed', result['error'])


if __name__ == '__main__':
    unittest.main()

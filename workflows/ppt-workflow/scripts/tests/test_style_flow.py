from __future__ import annotations

import concurrent.futures
import importlib.util
import json
import sys
import tempfile
import types
import unittest
from pathlib import Path
from unittest import mock


def _import_stubs() -> dict[str, types.ModuleType]:
    lazyllm = types.ModuleType('lazyllm')
    lazyllm.ThreadPoolExecutor = concurrent.futures.ThreadPoolExecutor
    lazyllm_tools = types.ModuleType('lazyllm.tools')
    lazyllm_agent = types.ModuleType('lazyllm.tools.agent')
    lazyllm_agent.ToolExecutionError = RuntimeError
    lazyllm.tools = lazyllm_tools
    lazyllm_tools.agent = lazyllm_agent

    context = types.ModuleType('lazymind.chat.engine.subagent.context')
    context.require_context = lambda: None
    subagent_tools = types.ModuleType('lazymind.chat.engine.subagent.tools')
    subagent_tools._resolve_artifact_text = lambda value: str(value or '')
    subagent_tools._save_artifact = lambda **kwargs: {'ok': True, **kwargs}
    subagent_tools._workflow_client = lambda: None
    subagent_tools.find_user_attachment = lambda filename: {
        'status': 'ok', 'filename': filename, 'path': filename,
    }
    multimodal = types.ModuleType('lazymind.chat.engine.tools.multimodal')
    multimodal.image_generator = lambda *args, **kwargs: None
    multimodal.image_editor = lambda *args, **kwargs: None
    presentation_edit = types.ModuleType(
        'lazymind.chat.engine.tools.file_resources.presentation_edit'
    )
    presentation_edit.available_font_families = lambda: {'Arial', 'SimSun', '宋体'}
    format_spec = importlib.util.spec_from_file_location(
        'ppt_test_text_format', Path(__file__).resolve().parents[4] /
        'algorithm/lazymind/chat/engine/tools/file_resources/text_format.py')
    text_format = importlib.util.module_from_spec(format_spec)
    format_spec.loader.exec_module(text_format)
    static_file = types.ModuleType('lazymind.chat.service.utils.static_file_url')
    static_file._upload_root = lambda: Path(tempfile.gettempdir())
    static_file.file_relative_path = lambda value: value
    static_file.encode_static_file_path = lambda value: value
    static_file.local_path_from_static_file_url = lambda value: value
    model_config = types.ModuleType('lazymind.model_config')
    model_config.is_model_role_available = lambda _role: True
    requests = types.ModuleType('requests')
    requests.RequestException = RuntimeError
    requests.exceptions = types.SimpleNamespace(Timeout=TimeoutError)

    modules = {
        'lazyllm': lazyllm,
        'lazyllm.tools': lazyllm_tools,
        'lazyllm.tools.agent': lazyllm_agent,
        'lazymind': types.ModuleType('lazymind'),
        'lazymind.chat': types.ModuleType('lazymind.chat'),
        'lazymind.chat.engine': types.ModuleType('lazymind.chat.engine'),
        'lazymind.chat.engine.subagent': types.ModuleType('lazymind.chat.engine.subagent'),
        'lazymind.chat.engine.subagent.context': context,
        'lazymind.chat.engine.subagent.tools': subagent_tools,
        'lazymind.chat.engine.tools': types.ModuleType('lazymind.chat.engine.tools'),
        'lazymind.chat.engine.tools.multimodal': multimodal,
        'lazymind.chat.engine.tools.file_resources': types.ModuleType(
            'lazymind.chat.engine.tools.file_resources'
        ),
        'lazymind.chat.engine.tools.file_resources.presentation_edit': presentation_edit,
        'lazymind.chat.engine.tools.file_resources.text_format': text_format,
        'lazymind.chat.service': types.ModuleType('lazymind.chat.service'),
        'lazymind.chat.service.utils': types.ModuleType('lazymind.chat.service.utils'),
        'lazymind.chat.service.utils.static_file_url': static_file,
        'lazymind.model_config': model_config,
        'requests': requests,
    }
    return modules


TOOLS_PATH = Path(__file__).resolve().parents[1] / 'tools.py'
SPEC = importlib.util.spec_from_file_location('ppt_style_flow_tools_test', TOOLS_PATH)
TOOLS = importlib.util.module_from_spec(SPEC)
assert SPEC.loader is not None
with mock.patch.dict(sys.modules, _import_stubs()):
    SPEC.loader.exec_module(TOOLS)


class StyleFlowCombinationTest(unittest.TestCase):
    def test_uploaded_template_never_requires_image_model(self):
        with mock.patch.object(TOOLS, 'is_model_role_available') as check:
            result = TOOLS.check_ppt_workflow_capabilities(
                'AI_BACKGROUND_IMAGES: disabled', template_mode='upload',
                generate_background_images='disabled', style_flow='auto',
            )
            self.assertEqual(result['required'], [])
            with self.assertRaisesRegex(ValueError, '模板'):
                TOOLS.check_ppt_workflow_capabilities(
                    'AI_BACKGROUND_IMAGES: enabled', template_mode='upload',
                    generate_background_images='enabled',
                )
            check.assert_not_called()

    def test_papers_cache_to_eof_reuse_successes_and_retry_changed_sources(self):
        reader_module = types.ModuleType('lazymind.chat.engine.tools.file_resources.tools')
        reader_module.read_file_resource = mock.Mock(side_effect=lambda target, offset, limit: {
            'text': f'{Path(target).name}: chunk {offset}',
            'eof': offset == 2, 'next_offset': 2 if offset == 1 else None,
        })
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            first = root / 'one.pdf'
            second = root / 'two.pdf'
            first.write_bytes(b'first')
            second.write_bytes(b'second')
            with mock.patch.dict(sys.modules, {reader_module.__name__: reader_module}), mock.patch.object(
                TOOLS, '_conversation_root', return_value=root,
            ), mock.patch.object(TOOLS, 'find_user_attachment', side_effect=lambda name: {'path': str(root / name)}):
                result = TOOLS.ppt_cache_academic_sources(['one.pdf', 'two.pdf', 'missing.pdf'])
                self.assertEqual(result['success_count'], 2)
                self.assertEqual(result['sources'][2]['status'], 'error')
                self.assertEqual(reader_module.read_file_resource.call_count, 4)
                cached = TOOLS.ppt_cache_academic_sources(['one.pdf', 'two.pdf'])
                self.assertTrue(all(item['cached'] for item in cached['sources']))
                self.assertEqual(reader_module.read_file_resource.call_count, 4)
                page = TOOLS.ppt_read_academic_source(result['sources'][0]['source_id'], 2)
                self.assertIn('chunk 2', page['reader_result']['text'])
                self.assertIsNone(page['next_chunk'])
                second.write_bytes(b'changed')
                changed = TOOLS.ppt_cache_academic_sources(['one.pdf', 'two.pdf'])
                self.assertTrue(changed['sources'][0]['cached'])
                self.assertFalse(changed['sources'][1]['cached'])
                self.assertEqual(reader_module.read_file_resource.call_count, 6)

    def test_academic_selection_changes_only_selected_formula(self):
        html = '<html><body><div data-el="formula-1" data-academic-kind="formula">x<sup>2</sup></div><p>Keep</p></body></html>'
        fragment = '<div data-el="formula-1" data-academic-kind="formula">x<sup>3</sup></div>'
        with mock.patch.object(TOOLS, '_agent_llm_call', return_value=json.dumps({'op': 'replace_academic_element', 'html': fragment})):
            ops, _, _ = TOOLS._selection_edit_ops('将指数改为三', {'el': 'formula-1'}, TOOLS._HtmlTree(html))
        edited, _, _, _ = TOOLS._apply_html_ops(html, ops)
        TOOLS._validate_local_html_edit(html, edited)
        self.assertIn('x<sup>3</sup>', edited)
        self.assertIn('<p>Keep</p>', edited)

    def test_academic_markup_rejects_scripts_and_image_edit_requires_model(self):
        with self.assertRaises(ValueError):
            TOOLS._validated_academic_fragment('<svg><script>alert(1)</script></svg>')
        with mock.patch.object(TOOLS, 'is_model_role_available', return_value=False):
            with self.assertRaisesRegex(ValueError, 'image_editor'):
                TOOLS._selection_edit_ops('调整曲线颜色', {'el': 'image-1'}, TOOLS._HtmlTree('<img data-el="image-1" src="source.png">'))

    def test_image_editor_result_updates_only_selected_image(self):
        with tempfile.TemporaryDirectory() as temp:
            image_path = Path(temp) / 'edited.png'
            image_path.write_bytes(b'\x89PNG\r\n\x1a\nexample')
            html = '<html><body><img data-el="image-1" src="source.png"><p>Keep</p></body></html>'
            with mock.patch.object(TOOLS, 'is_model_role_available', return_value=True), mock.patch.object(
                TOOLS, 'image_editor', return_value={'local_path': str(image_path)},
            ) as editor:
                ops, _, _ = TOOLS._selection_edit_ops('改为灰度图', {'el': 'image-1'}, TOOLS._HtmlTree(html))
            edited, _, _, _ = TOOLS._apply_html_ops(html, ops)
            self.assertIn('data:image/png;base64,', edited)
            self.assertIn('<p>Keep</p>', edited)
            editor.assert_called_once()

    COMBINATIONS = (
        ('auto', False, 'fast'),
        ('auto', True, 'fast'),
        ('preview_choice', False, 'standard'),
        ('preview_choice', True, 'standard'),
    )

    def test_template_analysis_profiles_and_caches_ooxml(self) -> None:
        import zipfile

        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            deck = root / 'deck'
            deck.mkdir()
            (deck / 'style_spec.json').write_text(json.dumps({
                'primary_color': {'hex': '#000000'},
                'palette': {'primary': '#000000', 'accent': '#FFFFFF'},
                'typography': {'heading_font': 'Arial', 'body_font': 'Arial'},
            }), encoding='utf-8')
            source = root / 'brand-template.pptx'
            with zipfile.ZipFile(source, 'w') as archive:
                archive.writestr('[Content_Types].xml', '<Types/>')
                archive.writestr('ppt/presentation.xml', '''
                    <p:presentation xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main">
                      <p:sldSz cx="12192000" cy="6858000"/>
                    </p:presentation>
                ''')
                archive.writestr('ppt/slides/slide1.xml', '''
                    <p:sld xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main"
                      xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main">
                      <a:latin typeface="Aptos"/>
                    </p:sld>
                ''')
                archive.writestr('ppt/slideMasters/slideMaster1.xml', '<p:sldMaster xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main"/>')
                archive.writestr('ppt/slideLayouts/slideLayout1.xml', '''
                    <p:sldLayout xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main" type="title">
                      <p:cSld name="Title"><p:spTree><p:sp><p:nvSpPr><p:nvPr><p:ph type="title"/></p:nvPr></p:nvSpPr></p:sp></p:spTree></p:cSld>
                    </p:sldLayout>
                ''')
                archive.writestr('ppt/theme/theme1.xml', '''
                    <a:theme xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main"><a:themeElements>
                      <a:clrScheme name="Brand"><a:dk1><a:sysClr val="windowText" lastClr="000000"/></a:dk1><a:accent1><a:srgbClr val="123456"/></a:accent1></a:clrScheme>
                      <a:fontScheme name="Brand"><a:majorFont><a:latin typeface="Brand Display"/><a:ea typeface="Noto Sans CJK SC"/></a:majorFont><a:minorFont><a:latin typeface="Aptos"/><a:ea typeface="Microsoft YaHei"/></a:minorFont></a:fontScheme>
                    </a:themeElements></a:theme>
                ''')

            saved = []
            with mock.patch.object(TOOLS, '_resolve_deck_dir', return_value=deck), mock.patch.object(
                TOOLS, '_save_artifact', side_effect=lambda *args, **kwargs: saved.append((args, kwargs)),
            ):
                first = TOOLS.ppt_analyze_template(str(source), deck_dir=str(deck))
                second = TOOLS.ppt_analyze_template(str(source), deck_dir=str(deck))

            self.assertFalse(first['cached'])
            self.assertTrue(second['cached'])
            self.assertEqual(first['profile']['slide_count'], 1)
            self.assertEqual(first['profile']['layout_count'], 1)
            self.assertEqual(first['profile']['theme_fonts']['major_latin'], 'Brand Display')
            self.assertEqual(first['profile']['theme_colors']['accent1'], '123456')
            self.assertEqual(first['profile']['theme_colors']['dk1'], '000000')
            self.assertGreaterEqual(first['profile']['explicit_fonts']['Aptos'], 1)
            self.assertTrue((deck / 'template' / 'source-template.pptx').is_file())
            style = json.loads((deck / 'style_spec.json').read_text(encoding='utf-8'))
            self.assertEqual(style['palette']['primary'], '#123456')
            self.assertIn('Brand Display', style['typography']['heading_font'])
            self.assertEqual(len(saved), 2)

    def test_each_of_four_combinations_initializes_ten_times(self) -> None:
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            with mock.patch.object(TOOLS, '_conversation_root', return_value=root), mock.patch.object(
                TOOLS, '_attach_material_images_to_deck',
                return_value={'attached': 0, 'reference_images': []},
            ):
                for flow, backgrounds, expected_mode in self.COMBINATIONS:
                    for run in range(10):
                        result = TOOLS.ppt_init_deck(
                            user_query=f'{flow}-{backgrounds}-{run}',
                            page_count=2,
                            style_flow=flow,
                            # A conflicting legacy value must not own the branch.
                            ppt_mode='standard' if expected_mode == 'fast' else 'fast',
                            generate_background_images=backgrounds,
                        )
                        deck = Path(result['deck_dir'])
                        pack = json.loads((deck / 'task_pack.json').read_text(encoding='utf-8'))
                        self.assertEqual(result['style_flow'], flow)
                        self.assertEqual(result['ppt_mode'], expected_mode)
                        self.assertEqual(pack['params']['style_flow'], flow)
                        self.assertEqual(pack['ppt_mode'], expected_mode)
                        self.assertIs(
                            pack['params']['generate_background_images'], backgrounds,
                        )

    @staticmethod
    def _prepared_deck(root: Path, flow: str, backgrounds: bool, run: int) -> Path:
        deck = root / f'{flow}-{backgrounds}-{run}'
        deck.mkdir()
        (deck / 'task_pack.json').write_text(json.dumps({
            'deck_id': deck.name,
            'ppt_mode': 'standard' if flow == 'preview_choice' else 'fast',
            'params': {
                'page_count': 2,
                'style_flow': flow,
                'generate_background_images': backgrounds,
            },
        }), encoding='utf-8')
        (deck / 'info_pack.json').write_text(json.dumps({
            'user_query': '两页测试 PPT', 'user_assets': {},
        }), encoding='utf-8')
        if flow == 'preview_choice':
            (deck / 'style_samples.json').write_text(json.dumps({
                'samples': [
                    {'sample_id': sample, 'style_spec': {}}
                    for sample in ('A', 'B', 'C')
                ],
            }), encoding='utf-8')
        return deck

    def test_each_of_four_combinations_builds_outline_ten_times(self) -> None:
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            for flow, backgrounds, _expected_mode in self.COMBINATIONS:
                for run in range(10):
                    deck = self._prepared_deck(root, flow, backgrounds, run)
                    stage_calls: list[tuple[str, str | None]] = []

                    def run_stage(deck_dir: str, *, stage: str, sample=None, **_kwargs):
                        stage_calls.append((stage, sample))
                        if stage == 'style':
                            style = {
                                'design_style': {'id': 1},
                                'color_tone': {'id': 1},
                                'primary_color': {'id': 1},
                                'palette': {'primary': '#1976D2'},
                            }
                            if sample:
                                style['_selected_sample'] = {'sample_id': sample}
                            (Path(deck_dir) / 'style_spec.json').write_text(
                                json.dumps(style), encoding='utf-8',
                            )
                        if stage == 'content-outline':
                            (Path(deck_dir) / 'outline.json').write_text(
                                json.dumps({'pages': [
                                    {'page_no': 1, 'title': '第一页'},
                                    {'page_no': 2, 'title': '第二页'},
                                ]}),
                                encoding='utf-8',
                            )
                        return {'status': 'ok', 'pages': 2}

                    with mock.patch.object(
                        TOOLS, '_resolve_deck_dir', return_value=deck,
                    ), mock.patch.object(
                        TOOLS, '_attach_material_images_to_deck',
                        return_value={'attached': 0, 'reference_images': []},
                    ), mock.patch.object(
                        TOOLS, 'ppt_run_stage', side_effect=run_stage,
                    ), mock.patch.object(
                        TOOLS, '_publish_deck_outline',
                        return_value={'ok': True, 'page_count': 2, 'chars': 80},
                    ):
                        result = TOOLS.ppt_build_outline(
                            user_query='两页测试 PPT', page_count=2,
                            deck_dir=str(deck), style_flow=flow,
                            style_selection='B' if flow == 'preview_choice' else None,
                            generate_background_images=backgrounds,
                        )

                    self.assertEqual(result['style_flow'], flow)
                    self.assertIs(result['background_images_enabled'], backgrounds)
                    self.assertEqual(
                        stage_calls,
                        [
                            ('preflight', None),
                            ('style', 'B' if flow == 'preview_choice' else None),
                            ('preflight', None),
                            ('content-outline', None),
                        ],
                    )

    def test_skipped_previews_do_not_require_candidates_or_selection(self):
        for requested in (None, 'B'):
            with self.subTest(selection=requested), tempfile.TemporaryDirectory() as temp:
                deck = self._prepared_deck(Path(temp), 'preview_choice', False, 0)
                (deck / 'style_samples.json').unlink()
                with mock.patch.object(TOOLS, '_resolve_deck_dir', return_value=deck), \
                     mock.patch.object(TOOLS, 'ppt_prepare_style_choices', return_value={
                         'status': 'ok', 'default_selection': 'A',
                     }) as choices, \
                     mock.patch.object(TOOLS, 'ppt_run_stage', return_value={'status': 'ok'}) as stage:
                    result = TOOLS.ppt_prepare_style(
                        user_query='儿童春节，一页，不看风格，不生成背景图',
                        deck_dir=str(deck), style_flow='preview_choice',
                        style_selection=requested, generate_background_images=False,
                    )
                self.assertEqual(result['style_flow'], 'auto')
                self.assertIsNone(result['style_selection'])
                choices.assert_not_called()
                self.assertIsNone(stage.call_args.kwargs['sample'])
                pack = json.loads((deck / 'task_pack.json').read_text())
                self.assertEqual(pack['params']['style_flow'], 'auto')

    def test_publish_outline_uses_bound_deck_without_find(self):
        with tempfile.TemporaryDirectory() as temp:
            deck = Path(temp)
            (deck / 'outline_draft.md').write_text('## 春节习俗\n贴春联')
            with mock.patch.object(TOOLS, '_bound_workflow_deck', return_value=deck), \
                 mock.patch.object(TOOLS, 'ppt_find_deck') as find, \
                 mock.patch.object(TOOLS, '_publish_text_page_briefs',
                                   return_value={'published_count': 1}) as publish:
                result = TOOLS.ppt_publish_outline(page_prompts=['红金配色，标题置顶，左右习俗插画，正文大字'])
            self.assertEqual(result['published_count'], 1)
            publish.assert_called_once_with(deck, None, ['红金配色，标题置顶，左右习俗插画，正文大字'])
            find.assert_not_called()

    def test_publish_outline_reports_missing_binding_without_search(self):
        with mock.patch.object(TOOLS, '_bound_workflow_deck', return_value=None), \
             mock.patch.object(TOOLS, 'ppt_find_deck') as find:
            with self.assertRaisesRegex(RuntimeError, 'No deck is bound'):
                TOOLS.ppt_publish_outline()
            find.assert_not_called()

    def test_page_prompt_publisher_requires_authored_prompts_before_writing(self):
        outline = '## 春节习俗\n贴春联、吃年夜饭'
        prompt = '标题“春节快乐”置顶，红金配色；左侧春联插画，右侧年夜饭插画；短句大字，无 AI 底图。'
        with tempfile.TemporaryDirectory() as temp, \
             mock.patch.object(TOOLS, '_resolve_artifact_text', return_value=(outline, 'text')), \
             mock.patch.object(TOOLS, '_ui_slot_order_list', return_value=[]), \
             mock.patch.object(TOOLS, '_publish_ordered_ppt_artifact', return_value={'status': 'ok'}) as publish:
            for invalid in (None, [], [outline], ['']):
                with self.subTest(prompts=invalid), self.assertRaises(ValueError):
                    TOOLS._publish_text_page_briefs(Path(temp), None, invalid)
            publish.assert_not_called()
            result = TOOLS._publish_text_page_briefs(Path(temp), None, [prompt])
            self.assertEqual(result['published_count'], 1)
            self.assertEqual(publish.call_args.kwargs['value'], prompt)

    def test_complete_prompts_skip_structure_generation_but_quick_mode_keeps_it(self):
        for complete in (True, False):
            with self.subTest(complete=complete), tempfile.TemporaryDirectory() as temp:
                deck = Path(temp)
                (deck / 'task_pack.json').write_text('{"params":{"page_count":1}}')
                (deck / 'info_pack.json').write_text('{}')
                if complete:
                    (deck / 'complete_page_prompts.json').write_text('{"version":1}')
                def stage(_deck, *, stage):
                    if stage == 'content-outline':
                        (deck / 'outline.json').write_text('{"pages":[{"page_no":1}]}')
                    return {'status': 'ok'}
                with mock.patch.object(TOOLS, '_selected_slide_outline_items',
                                       return_value=[(0, '完整提示词，红金配色')] if complete else []), \
                     mock.patch.object(TOOLS, '_resolve_artifact_text', return_value=('## 春节\n习俗', 'text')), \
                     mock.patch.object(TOOLS, 'ppt_run_stage', side_effect=stage) as generate:
                    result = TOOLS._prepare_deferred_outline(deck)
                if complete:
                    self.assertEqual(result['status'], 'direct')
                    generate.assert_not_called()
                else:
                    self.assertEqual(result['status'], 'ok')
                    self.assertEqual([c.kwargs['stage'] for c in generate.call_args_list],
                                     ['preflight', 'content-outline'])

    def test_legacy_standard_without_samples_recovers_as_auto(self) -> None:
        with tempfile.TemporaryDirectory() as temp:
            deck = Path(temp)
            (deck / 'task_pack.json').write_text(json.dumps({
                'ppt_mode': 'standard', 'params': {'page_count': 1},
            }), encoding='utf-8')

            pack, flow = TOOLS._persist_deck_style_flow(deck)

            self.assertEqual(flow, 'auto')
            self.assertEqual(pack['ppt_mode'], 'fast')
            self.assertEqual(pack['params']['style_flow'], 'auto')

    def test_explicit_background_choice_overrides_prepared_deck_both_ways(self) -> None:
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            cases = ((True, False), (False, True))
            for index, (stored, requested) in enumerate(cases):
                deck = self._prepared_deck(root, 'auto', stored, index)

                def run_stage(deck_dir: str, *, stage: str, **_kwargs):
                    if stage == 'style':
                        (Path(deck_dir) / 'style_spec.json').write_text(
                            json.dumps({
                                'design_style': {'id': 1},
                                'color_tone': {'id': 1},
                                'primary_color': {'id': 1},
                                'palette': {'primary': '#1976D2'},
                            }),
                            encoding='utf-8',
                        )
                    if stage == 'content-outline':
                        (Path(deck_dir) / 'outline.json').write_text(
                            json.dumps({'pages': [
                                {'page_no': 1, 'title': '第一页'},
                                {'page_no': 2, 'title': '第二页'},
                            ]}),
                            encoding='utf-8',
                        )
                    return {'status': 'ok'}

                with mock.patch.object(
                    TOOLS, '_resolve_deck_dir', return_value=deck,
                ), mock.patch.object(
                    TOOLS, '_attach_material_images_to_deck',
                    return_value={'attached': 0, 'reference_images': []},
                ), mock.patch.object(
                    TOOLS, 'ppt_run_stage', side_effect=run_stage,
                ), mock.patch.object(
                    TOOLS, '_publish_deck_outline',
                    return_value={'ok': True, 'page_count': 2, 'chars': 80},
                ):
                    result = TOOLS.ppt_build_outline(
                        user_query='两页测试 PPT', deck_dir=str(deck),
                        style_flow='auto',
                        generate_background_images=requested,
                    )

                pack = json.loads(
                    (deck / 'task_pack.json').read_text(encoding='utf-8')
                )
                self.assertIs(result['background_images_enabled'], requested)
                self.assertIs(
                    pack['params']['generate_background_images'], requested,
                )

    def test_capability_gate_accepts_all_four_independent_combinations(self) -> None:
        for flow, backgrounds, _mode in self.COMBINATIONS:
            marker = (
                'AI_BACKGROUND_IMAGES: enabled'
                if backgrounds else 'AI_BACKGROUND_IMAGES: disabled'
            )
            result = TOOLS.check_ppt_workflow_capabilities(
                marker,
                style_flow=flow,
                generate_background_images='enabled' if backgrounds else 'disabled',
            )
            self.assertEqual(result['status'], 'ready')
            self.assertEqual(result['style_flow'], flow)

    def test_html_font_replacement_can_target_one_page_title_only(self) -> None:
        with tempfile.TemporaryDirectory() as temp:
            deck = Path(temp) / 'deck'
            pages = deck / 'pages'
            pages.mkdir(parents=True)
            page_html = (
                '<!doctype html><html><head></head><body>'
                '<h1 data-el="title">Title</h1><p data-el="bullet-1">Body</p>'
                '</body></html>'
            )
            (pages / 'page_001.html').write_text(page_html, encoding='utf-8')
            (pages / 'page_002.html').write_text(page_html, encoding='utf-8')
            original_style = {'typography': {'heading_font': 'Arial', 'body_font': 'Arial'}}
            (deck / 'style_spec.json').write_text(
                json.dumps(original_style), encoding='utf-8',
            )
            with mock.patch.object(TOOLS, '_resolve_deck_dir', return_value=deck), mock.patch.object(
                TOOLS, '_publish_pages_from_disk', return_value={
                    'published_count': 1, 'failed_count': 0,
                },
            ):
                result = TOOLS.ppt_replace_deck_fonts(
                    str(deck), '宋体', pages=1, text_scope='titles',
                )

            self.assertEqual(result['target_font'], '宋体')
            changed = (pages / 'page_001.html').read_text(encoding='utf-8')
            untouched = (pages / 'page_002.html').read_text(encoding='utf-8')
            self.assertIn('lazymind-font-override-titles', changed)
            self.assertIn('font-family: "宋体"', changed)
            self.assertEqual(untouched, page_html)
            self.assertEqual(
                json.loads((deck / 'style_spec.json').read_text(encoding='utf-8')),
                original_style,
            )

    def test_whole_html_deck_font_replacement_updates_style_spec(self) -> None:
        with tempfile.TemporaryDirectory() as temp:
            deck = Path(temp) / 'deck'
            pages = deck / 'pages'
            pages.mkdir(parents=True)
            (pages / 'page_001.html').write_text(
                '<html><head></head><body><h1>Title</h1><p>Body</p></body></html>',
                encoding='utf-8',
            )
            (deck / 'style_spec.json').write_text('{}', encoding='utf-8')
            with mock.patch.object(TOOLS, '_resolve_deck_dir', return_value=deck), mock.patch.object(
                TOOLS, '_publish_pages_from_disk', return_value={
                    'published_count': 1, 'failed_count': 0,
                },
            ):
                result = TOOLS.ppt_replace_deck_fonts(
                    str(deck), 'SimSun', text_scope='all',
                )

            style = json.loads((deck / 'style_spec.json').read_text(encoding='utf-8'))
            self.assertEqual(result['target_font'], 'SimSun')
            self.assertTrue(result['style_spec_updated'])
            self.assertIn('SimSun', style['typography']['heading_font'])
            self.assertIn('SimSun', style['typography']['body_font'])


if __name__ == '__main__':
    unittest.main()

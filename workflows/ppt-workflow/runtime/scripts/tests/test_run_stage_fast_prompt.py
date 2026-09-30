from __future__ import annotations

import base64
import json
import os
import sys
import tempfile
import threading
import unittest
from pathlib import Path
from unittest.mock import patch

import yaml


SCRIPT_DIR = Path(__file__).resolve().parents[1]
if str(SCRIPT_DIR) not in sys.path:
    sys.path.insert(0, str(SCRIPT_DIR))

import run_stage  # noqa: E402


class StyleRenderingRecipeTest(unittest.TestCase):
    def test_recipe_catalog_covers_every_declared_style_once(self) -> None:
        recipes = json.loads(
            run_stage._STYLE_RENDERING_RECIPES_PATH.read_text(encoding='utf-8')
        )
        ids = [
            style_id
            for family in recipes['families'].values()
            for style_id in family['style_ids']
        ]
        self.assertEqual(sorted(ids), list(range(1, 69)))
        self.assertEqual(len(ids), len(set(ids)))

    def test_cyberpunk_recipe_adds_specific_export_safe_direction(self) -> None:
        style = run_stage._attach_style_rendering_recipe({
            'design_style': {'id': 10, 'name_zh': '赛博朋克'},
            'palette': {
                'primary': '#EC407A',
                'accent': '#00FFFF',
                'neutral': '#0D0D1A',
            },
        })

        direction = style['art_direction']
        self.assertEqual(direction['family'], 'technology_cinematic')
        self.assertIn('Cyberpunk:', direction['style_signature'])
        self.assertTrue(direction['export_safe_effects'])
        self.assertTrue(direction['forbidden_effects'])
        self.assertIn('small centered banner', direction['composition'][0])

    def test_workflow_ask_uses_recipe_options_as_a_ranked_subset(self) -> None:
        recipes = json.loads(
            run_stage._STYLE_RENDERING_RECIPES_PATH.read_text(encoding='utf-8')
        )
        workflow_path = Path(__file__).resolve().parents[3] / 'workflow.yaml'
        workflow = yaml.safe_load(workflow_path.read_text(encoding='utf-8'))
        style_field = next(
            field for field in workflow['runtime']['clarification_fields']
            if field['id'] == 'visual_style'
        )

        self.assertEqual(style_field['choice_policy'], 'subset')
        self.assertEqual(
            style_field['choices'],
            [option['label'] for option in recipes['ask_options']],
        )
        self.assertIn('2-4', style_field['question'])

    def test_slide_count_choices_are_suggestions_not_a_fixed_allowlist(self) -> None:
        workflow_path = Path(__file__).resolve().parents[3] / 'workflow.yaml'
        workflow = yaml.safe_load(workflow_path.read_text(encoding='utf-8'))
        slide_count = next(
            field for field in workflow['runtime']['clarification_fields']
            if field['id'] == 'slide_count'
        )

        self.assertEqual(slide_count['choice_policy'], 'seed')
        self.assertEqual(slide_count['choices'], ['3 页', '5 页', '8 页', '10 页'])

    def test_workflow_asks_for_explicit_ai_background_opt_in(self) -> None:
        workflow_path = Path(__file__).resolve().parents[3] / 'workflow.yaml'
        workflow = yaml.safe_load(workflow_path.read_text(encoding='utf-8'))
        field = next(
            item for item in workflow['runtime']['clarification_fields']
            if item['id'] == 'generate_background_images'
        )

        self.assertEqual(field['type'], 'single')
        self.assertEqual(field['choice_policy'], 'fixed')
        self.assertFalse(field['allow_other'])
        self.assertEqual(len(field['choices']), 2)
        self.assertTrue(field['choices'][0].startswith('启用'))
        self.assertTrue(field['choices'][1].startswith('不启用'))
        self.assertIn('background_images', workflow['runtime']['publisher_owned_slots'])

    def test_only_page_prompts_and_generation_checkpoints_require_approval(self) -> None:
        state_path = Path(__file__).resolve().parents[3] / 'scenario' / 'state.yml'
        state = yaml.safe_load(state_path.read_text(encoding='utf-8'))

        self.assertEqual(state['steps']['plan_background_prompts']['mode'], 'auto')
        self.assertEqual(state['steps']['build_outline']['mode'], 'auto')
        self.assertEqual(state['steps']['choose_style']['mode'], 'human')
        self.assertEqual(state['steps']['plan_page_prompts']['mode'], 'human')
        self.assertEqual(state['steps']['generate_backgrounds']['mode'], 'human')
        self.assertEqual(state['steps']['generate_ppt']['mode'], 'human')

    def test_background_prompt_and_generation_steps_support_skip_and_targeted_rerun(self) -> None:
        workflow_path = Path(__file__).resolve().parents[3] / 'workflow.yaml'
        state_path = workflow_path.parent / 'scenario' / 'state.yml'
        workflow = yaml.safe_load(workflow_path.read_text(encoding='utf-8'))
        state = yaml.safe_load(state_path.read_text(encoding='utf-8'))

        self.assertEqual(
            state['steps']['plan_background_prompts']['skip_if'],
            {'material': 'skip_background_images'},
        )
        self.assertEqual(
            state['steps']['generate_backgrounds']['skip_if'],
            {'material': 'skip_background_images'},
        )
        self.assertIn('重新生成底图 1、2', state['steps']['generate_backgrounds']['prompt'])
        self.assertIn('shared visual world', state['steps']['plan_background_prompts']['prompt'])
        slots = {slot['id']: slot for slot in workflow['slots']}
        self.assertEqual(slots['background_prompts']['cardinality'], 'list')
        self.assertIn('background_prompts', workflow['runtime']['publisher_owned_slots'])
        self.assertEqual(
            [edge['to'] for edge in state['transitions']['collect_materials']],
            ['choose_style', 'plan_background_prompts', 'build_outline'],
        )
        self.assertEqual(
            [edge['to'] for edge in state['transitions']['choose_style']],
            ['plan_background_prompts', 'build_outline'],
        )
        self.assertEqual(state['steps']['analyze_requirements']['route'], 'choice')
        self.assertEqual(state['steps']['collect_materials']['route'], 'choice')
        self.assertNotIn('skip_if', state['steps']['collect_materials'])
        self.assertEqual(
            [edge['to'] for edge in state['transitions']['analyze_requirements']],
            ['collect_materials', 'choose_style', 'plan_background_prompts', 'build_outline'],
        )
        self.assertEqual(
            state['transitions']['generate_backgrounds'],
            [{'to': 'build_outline'}],
        )
        self.assertEqual(state['transitions']['build_outline'], [{'to': 'plan_page_prompts'}])
        self.assertEqual(state['transitions']['plan_page_prompts'], [{'to': 'generate_ppt'}])
        slots = {slot['id']: slot for slot in workflow['slots']}
        self.assertEqual(slots['deck_outline']['cardinality'], 'single')
        self.assertEqual(slots['slide_outline']['cardinality'], 'list')
        page_prompts = next(
            tab for tab in workflow['ui']['tabs'] if tab['id'] == 'page_prompts'
        )
        self.assertEqual(page_prompts['layout'], 'composite')
        for tab_id in ('background_prompts', 'background_images', 'page_prompts'):
            tab = next(tab for tab in workflow['ui']['tabs'] if tab['id'] == tab_id)
            self.assertEqual(tab['layout'], 'composite')
            self.assertEqual(tab['slot_scope'], 'step')
        self.assertEqual(
            page_prompts['composite_layout']['children'],
            [{'slot': 'slide_outline'}],
        )
        self.assertIn(
            'rewind to plan_background_prompts',
            workflow['runtime']['completed_edit_routing'],
        )
        self.assertIn(
            "ppt_publish_outline(deck_dir, pages=[N], insert_before=N)",
            state['steps']['plan_page_prompts']['prompt'],
        )

    def test_completed_html_chat_supports_incremental_media_replacement(self) -> None:
        workflow_path = Path(__file__).resolve().parents[3] / 'workflow.yaml'
        state_path = workflow_path.parent / 'scenario' / 'state.yml'
        workflow = yaml.safe_load(workflow_path.read_text(encoding='utf-8'))
        state = yaml.safe_load(state_path.read_text(encoding='utf-8'))

        registered = workflow['tool_scripts'][0]['functions']
        step_tools = state['steps']['generate_ppt']['tools']
        prompt = state['steps']['generate_ppt']['prompt']
        for tool in (
            'ppt_replace_page_material_image',
            'ppt_replace_page_background',
        ):
            self.assertIn(tool, registered)
            self.assertIn(tool, step_tools)
            self.assertIn(tool, prompt)
        self.assertIn('focused_sort_order', prompt)
        self.assertIn('updated_pages', state['steps']['generate_ppt']['acceptance_criteria'])

    def test_analyze_runs_conditional_background_capability_gate(self) -> None:
        workflow_path = Path(__file__).resolve().parents[3] / 'workflow.yaml'
        workflow = yaml.safe_load(workflow_path.read_text(encoding='utf-8'))
        checks = workflow['runtime']['post_step_checks']
        slots = {slot['id']: slot for slot in workflow['slots']}

        self.assertEqual(checks, [{
            'step_id': 'analyze_requirements',
            'tool': 'check_ppt_workflow_capabilities',
            'arguments': {
                'capability_requirements': 'ppt_capability_requirements',
                'style_flow': 'style_flow',
                'generate_background_images': 'generate_background_images',
            },
        }])
        self.assertFalse(slots['ppt_capability_requirements']['exposed'])
        self.assertFalse(slots['style_flow']['exposed'])
        self.assertFalse(slots['generate_background_images']['exposed'])


class OutlineReferenceImageRepairTest(unittest.TestCase):
    def test_empty_image_pool_clears_hallucinated_binding_without_failing(self) -> None:
        pages = [
            {"page_no": 1, "use_image": {"reference_image_index": 0}},
            {"page_no": 2, "use_image": None},
        ]

        repaired = run_stage._ensure_outline_reference_images(pages, [])

        self.assertEqual(repaired, 1)
        self.assertEqual([page["use_image"] for page in pages], [None, None])

    def test_repairs_prose_only_material_reference_and_fills_other_pages(self) -> None:
        pages = [
            {"page_no": 1, "visual_hints": "封面", "use_image": None},
            {
                "page_no": 2,
                "visual_hints": "左侧放 material_03，右侧放信息卡片",
                "use_image": None,
            },
            {"page_no": 3, "visual_hints": "结尾", "use_image": None},
        ]
        images = [
            {"reference_image_index": 0, "basename": "material_01_a.png"},
            {"reference_image_index": 1, "basename": "material_02_b.jpg"},
            {"reference_image_index": 2, "basename": "material_03_c.jpg"},
        ]

        repaired = run_stage._ensure_outline_reference_images(pages, images)

        self.assertEqual(repaired, 3)
        self.assertEqual(pages[1]["use_image"], {"reference_image_index": 2})
        self.assertEqual(pages[0]["use_image"], {"reference_image_index": 0})
        self.assertEqual(pages[2]["use_image"], {"reference_image_index": 1})

    def test_collapses_model_generated_image_arrays_to_one_image_per_page(self) -> None:
        pages = [
            {
                "page_no": 1,
                "use_image": [
                    {"reference_image_index": 0},
                    {"reference_image_index": 1},
                ],
            },
            {
                "page_no": 2,
                "use_image": [
                    {"reference_image_index": 2},
                    {"reference_image_index": 3},
                ],
            },
            {
                "page_no": 3,
                "use_image": [
                    {"reference_image_index": 4},
                    {"reference_image_index": 5},
                ],
            },
        ]
        images = [
            {"reference_image_index": index, "basename": f"material_{index + 1:02d}.jpg"}
            for index in range(6)
        ]

        repaired = run_stage._ensure_outline_reference_images(pages, images)

        self.assertEqual(repaired, 3)
        self.assertEqual(
            [page["use_image"] for page in pages],
            [
                {"reference_image_index": 0},
                {"reference_image_index": 2},
                {"reference_image_index": 4},
            ],
        )

    def test_preserves_pool_a_and_repairs_invalid_or_duplicate_pool_b(self) -> None:
        pages = [
            {"page_no": 1, "use_image": {"doc_index": 0, "image_index": 1}},
            {"page_no": 2, "use_image": {"reference_image_index": 4}},
            {"page_no": 3, "use_image": {"reference_image_index": 4}},
            {"page_no": 4, "use_image": {"reference_image_index": 99}},
        ]
        images = [
            {"reference_image_index": 4, "basename": "material_05.png"},
            {"reference_image_index": 7, "basename": "material_08.png"},
        ]

        repaired = run_stage._ensure_outline_reference_images(pages, images)

        self.assertEqual(repaired, 3)
        self.assertEqual(pages[0]["use_image"], {"doc_index": 0, "image_index": 1})
        self.assertEqual(pages[1]["use_image"], {"reference_image_index": 4})
        self.assertEqual(pages[2]["use_image"], {"reference_image_index": 7})
        self.assertIsNone(pages[3]["use_image"])


class PagePromptModeTest(unittest.TestCase):
    def _deck(self, root: Path) -> Path:
        deck = root / "deck"
        (deck / "pages").mkdir(parents=True)
        fixtures = {
            "task_pack.json": {"params": {"language": "zh-Hans", "page_count": 1}},
            "info_pack.json": {"user_query": "生成一页测试幻灯片", "user_assets": {}},
            "style_spec.json": {
                "palette": {"primary": "#2563EB", "accent": "#0EA5E9"},
                "typography": {"font_family": "Noto Sans SC"},
            },
            "outline.json": {
                "pages": [{
                    "page_no": 1,
                    "page_kind": "content",
                    "title": "快速生成",
                    "subtitle": "一次模型调用",
                    "bullets": [{"head": "目标", "detail": "减少等待时间"}],
                    "narrative": "保留结构化内容并直接生成 HTML。",
                    "data_points": [],
                    "visual_hints": "左右布局",
                    "use_table": None,
                    "use_image": None,
                }],
            },
            "asset_plan.json": {"pages": [{"page_no": 1, "slots": []}]},
        }
        for name, value in fixtures.items():
            (deck / name).write_text(json.dumps(value, ensure_ascii=False), encoding="utf-8")
        return deck

    def _attach_reference_image(self, deck: Path, root: Path) -> Path:
        source = root / 'collected_material.png'
        source.write_bytes(base64.b64decode(
            'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR4nGNgYAAAAAMAASsJTYQAAAAASUVORK5CYII='
        ))
        info_path = deck / 'info_pack.json'
        info = json.loads(info_path.read_text(encoding='utf-8'))
        info['user_assets'] = {
            'reference_images': [{'path': str(source), 'caption': '素材收集得到的图片'}],
        }
        info_path.write_text(json.dumps(info, ensure_ascii=False), encoding='utf-8')
        outline_path = deck / 'outline.json'
        outline = json.loads(outline_path.read_text(encoding='utf-8'))
        outline['pages'][0]['use_image'] = {'reference_image_index': 0}
        outline_path.write_text(json.dumps(outline, ensure_ascii=False), encoding='utf-8')
        return source

    def _attach_background_image(self, deck: Path) -> str:
        relative = 'images/page_001_background.png'
        path = deck / relative
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_bytes(base64.b64decode(
            'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR4nGNgYAAAAAMAASsJTYQAAAAASUVORK5CYII='
        ))
        (deck / 'background_images.json').write_text(json.dumps({
            'enabled': True,
            'pages': [{'page_no': 1, 'local_path': relative}],
        }), encoding='utf-8')
        return relative

    def test_generated_background_is_mandatory_in_page_css(self) -> None:
        calls: list[tuple[str, str]] = []
        html = """<!DOCTYPE html><html><head><style>
        #bg{background-image:url('../images/page_001_background.png');background-size:cover}
        </style></head><body><div class='wrapper'><div id='bg'></div><div id='ct'>完成</div></div></body></html>"""

        def fake_llm(system: str, user: str, **_kwargs) -> str:
            calls.append((system, user))
            return html

        with tempfile.TemporaryDirectory() as temp, patch.object(
            run_stage, 'llm', side_effect=fake_llm,
        ):
            deck = self._deck(Path(temp))
            relative = self._attach_background_image(deck)
            self.assertEqual(run_stage.cmd_page_html(deck, 1), 0)
            self.assertEqual(len(calls), 1)
            self.assertIn('AI BACKGROUND IMAGE', calls[0][1])
            self.assertIn('../images/page_001_background.png', calls[0][1])
            rendered = (deck / 'pages' / 'page_001.html').read_text(encoding='utf-8')
            self.assertTrue(run_stage._html_has_background_image(rendered, relative))

    def test_missing_background_is_restored_without_another_model_call(self) -> None:
        html = "<html><head></head><body><div class='wrapper'><div id='bg'></div><div id='ct'>Keep layout</div></div></body></html>"
        with tempfile.TemporaryDirectory() as temp, patch.object(run_stage, 'llm', return_value=html) as model:
            deck = self._deck(Path(temp))
            relative = self._attach_background_image(deck)
            self.assertEqual(run_stage.cmd_page_html(deck, 1), 0)
            self.assertEqual(model.call_count, 1)
            rendered = (deck / 'pages' / 'page_001.html').read_text()
            self.assertTrue(run_stage._html_has_background_image(rendered, relative))
            self.assertIn('Keep layout', rendered)
            self.assertIn('data-lazymind-background', rendered)

    def test_deterministic_mode_makes_one_model_call(self) -> None:
        html = "<!DOCTYPE html><html><head><title>快速生成</title></head><body><div class='wrapper'><div id='ct'>完成</div></div></body></html>"
        calls: list[tuple[str, str]] = []

        def fake_llm(system: str, user: str, **_kwargs) -> str:
            calls.append((system, user))
            return html

        with tempfile.TemporaryDirectory() as temp, patch.dict(
            os.environ, {"PPT_PAGE_PROMPT_MODE": "deterministic"}
        ), patch.object(run_stage, "llm", side_effect=fake_llm):
            deck = self._deck(Path(temp))
            self.assertEqual(run_stage.cmd_page_html(deck, 1), 0)
            self.assertEqual(len(calls), 1)
            self.assertIn("CONTENT BRIEF (JSON)", calls[0][1])
            self.assertEqual((deck / "pages" / "page_001.html").read_text(encoding="utf-8"), html)

    def test_legacy_mode_keeps_two_model_calls(self) -> None:
        html = "<!DOCTYPE html><html><head><title>快速生成</title></head><body><div class='wrapper'><div id='ct'>完成</div></div></body></html>"
        replies = iter(["自然语言页面要求", html])
        calls: list[tuple[str, str]] = []

        def fake_llm(system: str, user: str, **_kwargs) -> str:
            calls.append((system, user))
            return next(replies)

        with tempfile.TemporaryDirectory() as temp, patch.dict(
            os.environ, {"PPT_PAGE_PROMPT_MODE": "llm-rewrite"}
        ), patch.object(run_stage, "llm", side_effect=fake_llm):
            deck = self._deck(Path(temp))
            self.assertEqual(run_stage.cmd_page_html(deck, 1), 0)
            self.assertEqual(len(calls), 2)
            self.assertIn("自然语言页面要求", calls[1][1])

    def test_editable_brief_reuses_deck_wide_style(self) -> None:
        html = "<!DOCTYPE html><html><head><title>统一风格</title></head><body><div class='wrapper'><div id='ct'>完成</div></div></body></html>"
        calls: list[tuple[str, str]] = []

        def fake_llm(system: str, user: str, **_kwargs) -> str:
            calls.append((system, user))
            return html

        with tempfile.TemporaryDirectory() as temp, patch.object(
            run_stage, "llm", side_effect=fake_llm
        ):
            deck = self._deck(Path(temp))
            brief = "主题内容：端午节习俗。采用上下布局。"
            self.assertEqual(run_stage.cmd_page_html_from_brief(deck, 1, brief), 0)
            self.assertEqual(len(calls), 1)
            query = calls[0][1]
            self.assertIn("PAGE BRIEF", query)
            self.assertIn(brief, query)
            self.assertIn("VISUAL DESIGN CONTRACT (JSON)", query)
            self.assertIn("#2563EB", query)
            self.assertIn('"art_direction"', query)
            self.assertIn('export-safe-style-recipe-v1', query)
            self.assertIn("shared by every slide", query)

    def test_editable_brief_passes_collected_image_and_repairs_omission(self) -> None:
        missing = "<!DOCTYPE html><html><body><div class='wrapper'><div id='bg'></div><div id='ct'><div class='image-section'></div></div></div></body></html>"
        repaired = "<!DOCTYPE html><html><body><div class='wrapper'><div id='bg'></div><div id='ct'><img data-el='image-1' src='images/page_001_inherited.png'></div></div></body></html>"
        replies = iter([missing, repaired])
        calls: list[tuple[str, str]] = []

        def fake_llm(system: str, user: str, **_kwargs) -> str:
            calls.append((system, user))
            return next(replies)

        with tempfile.TemporaryDirectory() as temp, patch.object(
            run_stage, 'llm', side_effect=fake_llm
        ):
            root = Path(temp)
            deck = self._deck(root)
            self._attach_reference_image(deck, root)
            self.assertEqual(
                run_stage.cmd_page_html_from_brief(deck, 1, '使用素材图片介绍主题。'),
                0,
            )
            self.assertEqual(len(calls), 2)
            self.assertIn('INHERITED FOREGROUND IMAGE', calls[0][1])
            self.assertIn('../images/page_001_inherited.png', calls[0][1])
            self.assertIn('MANDATORY CORRECTION', calls[1][1])
            output = (deck / 'pages' / 'page_001.html').read_text(encoding='utf-8')
            self.assertIn("src='../images/page_001_inherited.png'", output)
            self.assertTrue((deck / 'images' / 'page_001_inherited.png').is_file())


if __name__ == "__main__":
    unittest.main()


class CombinedStyleOutlineTest(unittest.TestCase):
    def test_single_call_persists_style_and_retries_only_invalid_outline(self):
        with tempfile.TemporaryDirectory() as tmp:
            deck = Path(tmp)
            (deck / 'task_pack.json').write_text(json.dumps({'params': {'page_count': 2}}))
            (deck / 'info_pack.json').write_text('{}')
            style = {
                'design_style': {'id': 1}, 'palette': {'primary': '#fff'},
                'typography': {'heading_font': 'Arial'},
            }
            with patch.object(run_stage, 'llm', return_value=json.dumps({
                'style_spec': style, 'outline': {'pages': [{'page_no': 1}]},
            })) as model:
                code, result = run_stage._capture_cmd(run_stage.cmd_outline, deck, generate_style=True)
            self.assertNotEqual(code, 0)
            self.assertEqual(model.call_count, 3)
            self.assertNotIn('=== style_spec requirements ===', model.call_args.args[0])
            self.assertTrue((deck / 'style_spec.json').exists())
            self.assertFalse((deck / 'outline.json').exists())
            saved_style = (deck / 'style_spec.json').read_bytes()
            with patch.object(run_stage, 'llm', return_value=json.dumps({
                'pages': [{'page_no': 1, 'title': '封面'}, {'page_no': 2, 'title': '正文'}],
            })) as retry:
                code, result = run_stage._capture_cmd(run_stage.cmd_outline, deck)
            self.assertEqual(code, 0)
            retry.assert_called_once()
            self.assertEqual((deck / 'style_spec.json').read_bytes(), saved_style)
            self.assertEqual(len(json.loads((deck / 'outline.json').read_text())['pages']), 2)

    def test_combined_output_rejects_missing_style(self):
        with tempfile.TemporaryDirectory() as tmp:
            deck = Path(tmp)
            (deck / 'task_pack.json').write_text('{}')
            (deck / 'info_pack.json').write_text('{}')
            with patch.object(run_stage, 'llm', return_value='{"outline":{"pages":[]}}'):
                code, _ = run_stage._capture_cmd(run_stage.cmd_outline, deck, generate_style=True)
            self.assertNotEqual(code, 0)
            self.assertFalse((deck / 'style_spec.json').exists())
            self.assertFalse((deck / 'outline.json').exists())

    def test_combined_success_keeps_image_binding_and_style_recipe(self):
        with tempfile.TemporaryDirectory() as tmp:
            deck = Path(tmp)
            image = deck / 'photo.png'
            image.write_bytes(b'image')
            (deck / 'task_pack.json').write_text(json.dumps({'params': {'page_count': 1}}))
            (deck / 'info_pack.json').write_text(json.dumps({
                'user_assets': {'reference_images': [str(image)]},
            }))
            with patch.object(run_stage, 'llm', return_value=json.dumps({
                'style_spec': {
                    'design_style': {'id': 3}, 'palette': {'primary': '#008000'},
                    'typography': {'heading_font': 'Arial'},
                },
                'outline': {'pages': [{'page_no': 1, 'title': '周末'}]},
            })) as model:
                code, result = run_stage._capture_cmd(run_stage.cmd_outline, deck, generate_style=True)
            self.assertEqual(code, 0)
            model.assert_called_once()
            style = json.loads((deck / 'style_spec.json').read_text())
            self.assertIn('art_direction', style)
            page = json.loads((deck / 'outline.json').read_text())['pages'][0]
            self.assertEqual(page['use_image']['reference_image_index'], 0)

    def test_combined_stage_does_not_bypass_standard_style_selection(self):
        with tempfile.TemporaryDirectory() as tmp:
            deck = Path(tmp)
            (deck / 'task_pack.json').write_text('{"ppt_mode":"standard"}')
            with patch.object(run_stage, 'llm') as model:
                code, _ = run_stage._capture_cmd(run_stage.cmd_outline, deck, generate_style=True)
            self.assertNotEqual(code, 0)
            model.assert_not_called()
            self.assertFalse((deck / 'style_spec.json').exists())


class OutputNormalizationTest(unittest.TestCase):
    def test_json_wrapper_and_trailing_commas_preserve_literal_text(self):
        raw = '<think>example {not json}</think>说明： {"pages":[{"title":"literal ,} and ]",},],} 完成'
        self.assertEqual(run_stage._parse_json_loose(raw), {'pages': [{'title': 'literal ,} and ]'}]})
        self.assertEqual(run_stage._parse_json_loose('{"title":"<think>literal</think>"}'), {'title': '<think>literal</think>'})

    def test_truncated_json_is_not_fabricated(self):
        with self.assertRaises(json.JSONDecodeError):
            run_stage._parse_json_loose('{"pages":[{"title":"unfinished')

    def test_outline_representation_is_normalized_without_mutating_input(self):
        source = [{'title': '封面', 'page_no': 0, 'narrative': None, 'bullets': ['短句', {'head': '清单', 'detail': None}]}]
        result = run_stage._normalize_outline_pages(source)
        self.assertEqual(result[0]['page_no'], 1)
        self.assertEqual(result[0]['bullets'], [{'head': '短句', 'detail': ''}, {'head': '清单', 'detail': ''}])
        self.assertEqual(source[0]['page_no'], 0)
        with self.assertRaises(ValueError):
            run_stage._normalize_outline_pages([{'bullets': []}])

    def test_invalid_html_is_repaired_once_and_not_published_if_still_invalid(self):
        valid = '<html><head></head><body><div class="wrapper">内容</div></body></html>'
        for second, expected in [(valid, 0), ('still truncated <html>', 1)]:
            with self.subTest(second=second), tempfile.TemporaryDirectory() as tmp:
                deck = Path(tmp)
                with patch.object(run_stage, 'llm', side_effect=['not html', second]) as model:
                    code, result = run_stage._capture_cmd(
                        run_stage._write_page_html_from_query, deck, 1, '内容',
                        page_plan={}, inherited_image=None, background_image=None,
                        prompt_mode='deterministic', language='zh',
                    )
                self.assertEqual(code, expected)
                self.assertEqual(model.call_count, 2)
                self.assertEqual((deck / 'pages/page_001.html').exists(), expected == 0)


class DeferredStyleTest(unittest.TestCase):
    def test_content_outline_needs_no_style_and_preserves_image_binding(self):
        with tempfile.TemporaryDirectory() as tmp:
            deck = Path(tmp)
            image = deck / 'photo.png'
            image.write_bytes(b'image')
            (deck / 'task_pack.json').write_text('{"params":{"page_count":1}}')
            (deck / 'info_pack.json').write_text(json.dumps({
                'user_query': '米白绿色，大图少字',
                'user_assets': {'reference_images': [str(image)]},
            }))
            with patch.object(run_stage, 'llm', return_value='{"pages":[{"title":"周末"}]}') as model:
                code, _ = run_stage._capture_cmd(run_stage.cmd_outline, deck, content_only=True)
            self.assertEqual(code, 0)
            self.assertFalse((deck / 'style_spec.json').exists())
            system, query = model.call_args.args
            self.assertNotIn('=== style_spec requirements ===', system)
            self.assertEqual(json.loads(query)['style_spec'], {})
            self.assertIn('米白绿色', query)
            self.assertEqual(json.loads((deck / 'outline.json').read_text())['pages'][0]['use_image'],
                             {'reference_image_index': 0})

    def test_style_failure_preserves_outline_then_retry_reuses_success(self):
        with tempfile.TemporaryDirectory() as tmp:
            deck = Path(tmp)
            (deck / 'task_pack.json').write_text('{"ppt_mode":"fast"}')
            (deck / 'info_pack.json').write_text('{}')
            outline = b'{"pages":[{"title":"Keep me"}]}'
            (deck / 'outline.json').write_bytes(outline)
            style = {'design_style': {'id': 3}, 'palette': {'primary': '#fff'},
                     'typography': {'heading_font': 'Arial'}}
            with patch.object(run_stage, 'llm', side_effect=['{}', json.dumps(style)]) as model:
                code, _ = run_stage._capture_cmd(run_stage.cmd_ensure_style, deck)
                self.assertNotEqual(code, 0)
                self.assertFalse((deck / 'style_spec.json').exists())
                code, _ = run_stage._capture_cmd(run_stage.cmd_ensure_style, deck)
                self.assertEqual(code, 0)
                saved = (deck / 'style_spec.json').read_bytes()
                code, payload = run_stage._capture_cmd(run_stage.cmd_ensure_style, deck)
                self.assertEqual(code, 0)
                self.assertTrue(payload['reused'])
                self.assertEqual(model.call_count, 2)
            self.assertEqual((deck / 'style_spec.json').read_bytes(), saved)
            self.assertEqual((deck / 'outline.json').read_bytes(), outline)

    def test_standard_deck_still_requires_manual_style_selection(self):
        with tempfile.TemporaryDirectory() as tmp:
            deck = Path(tmp)
            (deck / 'task_pack.json').write_text('{"ppt_mode":"standard"}')
            with patch.object(run_stage, 'llm') as model:
                code, _ = run_stage._capture_cmd(run_stage.cmd_ensure_style, deck)
            self.assertNotEqual(code, 0)
            model.assert_not_called()


class OutlineLocalCorrectionTest(unittest.TestCase):
    def test_count_then_field_error_corrected_in_one_call(self):
        with tempfile.TemporaryDirectory() as tmp:
            deck = Path(tmp)
            (deck / 'task_pack.json').write_text('{"params":{"page_count":2}}')
            (deck / 'info_pack.json').write_text('{}')
            style = b'{"existing":"unchanged"}'
            (deck / 'style_spec.json').write_bytes(style)
            replies = [
                '{"pages":[{"title":"Keep"}]}',
                '{"pages":[{"title":"Keep"},{"bullets":[]}]}',
                '{"pages":[{"title":"Keep","page_no":9},{"title":"Added","page_no":9}]}',
            ]
            with patch.object(run_stage, 'llm', side_effect=replies) as model:
                code, result = run_stage._capture_cmd(run_stage.cmd_outline, deck, content_only=True)
            self.assertEqual(code, 0)
            self.assertEqual(result['attempts'], 3)
            calls = model.call_args_list
            self.assertIn('page_count mismatch', calls[1].args[1])
            self.assertIn('non-empty title', calls[2].args[1])
            self.assertTrue(all(c.kwargs['retries'] == 0 for c in calls))
            self.assertLessEqual(calls[2].kwargs['timeout'], calls[0].kwargs['timeout'])
            self.assertEqual((deck / 'style_spec.json').read_bytes(), style)
            self.assertEqual([p['page_no'] for p in json.loads((deck / 'outline.json').read_text())['pages']], [1, 2])

    def test_invalid_outputs_stop_at_three_without_overwriting_previous_outline(self):
        with tempfile.TemporaryDirectory() as tmp:
            deck = Path(tmp)
            (deck / 'task_pack.json').write_text('{}')
            (deck / 'info_pack.json').write_text('{}')
            (deck / 'outline.json').write_text('previous version')
            with patch.object(run_stage, 'llm', return_value='not JSON') as model:
                code, result = run_stage._capture_cmd(run_stage.cmd_outline, deck, content_only=True)
            self.assertNotEqual(code, 0)
            self.assertEqual(model.call_count, 3)
            self.assertEqual(result['attempts'], 3)
            self.assertEqual((deck / 'outline.json').read_text(), 'previous version')

    def test_provider_rejection_is_not_retried_as_content_correction(self):
        with tempfile.TemporaryDirectory() as tmp:
            deck = Path(tmp)
            (deck / 'task_pack.json').write_text('{}')
            (deck / 'info_pack.json').write_text('{}')
            with patch.object(run_stage, 'llm', side_effect=run_stage.ModelClientError('provider error')) as model:
                code, _ = run_stage._capture_cmd(run_stage.cmd_outline, deck, content_only=True)
            self.assertNotEqual(code, 0)
            model.assert_called_once()


class ApprovedOutlineInputTest(unittest.TestCase):
    def test_generate_uses_approved_content_as_authoritative_input(self):
        with tempfile.TemporaryDirectory() as tmp:
            deck = Path(tmp)
            (deck / 'task_pack.json').write_text(json.dumps({'params': {'page_count': 1}}))
            (deck / 'info_pack.json').write_text('{}')
            (deck / 'approved_outline.md').write_text('## 修改后的标题\n保留用户新增条目')
            with patch.object(run_stage, 'llm', return_value=json.dumps({
                'pages': [{'page_no': 1, 'title': '修改后的标题'}],
            })) as model:
                code, _ = run_stage._capture_cmd(run_stage.cmd_outline, deck, content_only=True)
            self.assertEqual(code, 0)
            self.assertIn('authoritative', model.call_args.args[0])
            self.assertIn('保留用户新增条目', json.loads(model.call_args.args[1])['approved_page_briefs'])


class TaskModelIsolationAndMetaCopyTest(unittest.TestCase):
    @staticmethod
    def _outline(title: str = '结构化大纲') -> dict:
        return {
            'pages': [{
                'page_no': 1,
                'page_kind': 'cover',
                'title': title,
                'subtitle': '可靠提交',
                'bullets': [{'head': '目标', 'detail': '验证任务级模型隔离'}],
                'narrative': '面向读者呈现核心结论。',
                'data_points': [],
                'visual_hints': '居中标题配合简洁卡片',
                'use_table': None,
                'use_image': None,
            }],
        }

    @staticmethod
    def _deck(root: Path, name: str) -> Path:
        deck = root / name
        deck.mkdir()
        fixtures = {
            'task_pack.json': {'params': {'language': 'zh-Hans', 'page_count': 1}},
            'info_pack.json': {'user_query': '生成一页测试幻灯片', 'user_assets': {}},
            'style_spec.json': {
                'palette': {'primary': '#2563EB'},
                'typography': {'font_family': 'Noto Sans SC'},
            },
        }
        for filename, payload in fixtures.items():
            (deck / filename).write_text(json.dumps(payload, ensure_ascii=False), encoding='utf-8')
        return deck

    def test_two_concurrent_outline_tasks_keep_their_own_model(self):
        barrier = threading.Barrier(2)
        errors: list[Exception] = []

        def generate(deck: Path, title: str) -> None:
            def task_llm(*_args, **kwargs):
                self.assertEqual(kwargs['request_name'], 'outline')
                barrier.wait(2)
                return json.dumps(self._outline(title), ensure_ascii=False)

            try:
                self.assertEqual(run_stage.cmd_outline(deck, llm_call=task_llm), 0)
            except Exception as exc:  # pragma: no cover - assertion aid
                errors.append(exc)

        with tempfile.TemporaryDirectory() as temp, patch.object(run_stage, '_ok', return_value=0):
            root = Path(temp)
            first_deck = self._deck(root, 'first')
            second_deck = self._deck(root, 'second')
            first = threading.Thread(target=generate, args=(first_deck, '任务 A'))
            second = threading.Thread(target=generate, args=(second_deck, '任务 B'))
            first.start()
            second.start()
            first.join(3)
            second.join(3)

            self.assertFalse(errors)
            self.assertEqual(json.loads((first_deck / 'outline.json').read_text())['pages'][0]['title'], '任务 A')
            self.assertEqual(json.loads((second_deck / 'outline.json').read_text())['pages'][0]['title'], '任务 B')

    def test_reader_visible_fields_reject_production_meta_copy(self):
        outline = self._outline()
        outline['pages'][0]['narrative'] = '本页作为开场，用于商务定位，并预告后续内容。'
        outline['pages'][0]['bullets'][0]['detail'] = '画面采用左右布局，营造专业感。'

        self.assertTrue(run_stage._contains_production_meta_copy(
            outline['pages'][0]['narrative'],
        ))
        self.assertEqual(
            run_stage._production_meta_copy_paths(outline['pages'][0]),
            ['narrative', 'bullets[0].detail'],
        )

    def test_page_query_drops_internal_slide_intent(self):
        query = run_stage._build_deterministic_page_query({
            'page_no': 1,
            'page_outline': {
                'title': '商汤科技',
                'slide_intent': '本页用于商务开场定位',
                'narrative': '坚持原创，让人工智能引领人类进步。',
                'visual_hints': '左侧标题，右侧品牌主视觉',
            },
            'style_spec': {},
        })

        self.assertNotIn('本页用于商务开场定位', query)
        self.assertIn('坚持原创，让人工智能引领人类进步。', query)
        self.assertIn('visual_hints only as internal layout guidance', query)

    def test_page_query_strips_legacy_meta_copy_from_all_visible_fields(self):
        query = run_stage._build_deterministic_page_query({
            'page_no': 1,
            'page_outline': {
                'title': '本页用于介绍产品定位',
                'subtitle': '可见副标题',
                'bullets': [
                    {'head': '核心能力', 'detail': '画面采用三栏布局进行展示'},
                ],
                'data_points': [
                    {'label': '覆盖率', 'value': '90%', 'context': '向观众说明增长趋势'},
                ],
                'visual_hints': '三栏布局',
            },
            'style_spec': {},
        })

        self.assertNotIn('本页用于介绍产品定位', query)
        self.assertNotIn('画面采用三栏布局进行展示', query)
        self.assertNotIn('向观众说明增长趋势', query)
        self.assertIn('可见副标题', query)
        self.assertIn('核心能力', query)
        self.assertIn('90%', query)
        self.assertIn('三栏布局', query)

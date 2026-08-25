from __future__ import annotations

import base64
import json
import os
import sys
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch


SCRIPT_DIR = Path(__file__).resolve().parents[1]
if str(SCRIPT_DIR) not in sys.path:
    sys.path.insert(0, str(SCRIPT_DIR))

import run_stage  # noqa: E402


class OutlineReferenceImageRepairTest(unittest.TestCase):
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

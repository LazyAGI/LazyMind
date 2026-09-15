import importlib.util
import json
import tempfile
import unittest
from pathlib import Path
from unittest import mock

from lazyllm.tools.agent import ToolExecutionError
from PIL import Image


TOOLS_PATH = Path(__file__).resolve().parents[1] / 'tools.py'
SPEC = importlib.util.spec_from_file_location('ppt_workflow_background_test', TOOLS_PATH)
TOOLS = importlib.util.module_from_spec(SPEC)
assert SPEC.loader is not None
SPEC.loader.exec_module(TOOLS)


class BackgroundImageGenerationTest(unittest.TestCase):
    @staticmethod
    def _write_generated_image(path: Path, size: tuple[int, int] = (1024, 1024)) -> None:
        Image.new('RGB', size, '#d34a24').save(path)

    def _deck(self, root: Path) -> Path:
        deck = root / 'deck'
        (deck / 'images').mkdir(parents=True)
        (deck / 'outline.json').write_text(json.dumps({
            'pages': [
                {'page_no': 1, 'page_kind': 'cover', 'title': '年度总结'},
                {'page_no': 2, 'page_kind': 'content', 'title': '关键进展'},
            ],
        }, ensure_ascii=False), encoding='utf-8')
        (deck / 'style_spec.json').write_text(json.dumps({
            'design_style': {'name_zh': '科技感'},
            'primary_color': '#126BFF',
        }, ensure_ascii=False), encoding='utf-8')
        return deck

    def test_generates_and_publishes_one_background_per_outline_page(self) -> None:
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            deck = self._deck(root)
            generated = []
            for page in (1, 2):
                path = root / f'generated_{page}.png'
                self._write_generated_image(path)
                generated.append(path)

            calls = []

            def fake_image_generator(**kwargs):
                calls.append(kwargs)
                return {'local_path': str(generated[len(calls) - 1])}

            with mock.patch.object(
                TOOLS, '_resolve_deck_dir', return_value=deck,
            ), mock.patch.object(
                TOOLS, 'image_generator', side_effect=fake_image_generator,
            ), mock.patch.object(
                TOOLS, '_save_artifact', return_value={'stored': True},
            ) as save:
                result = TOOLS.ppt_generate_background_images(str(deck))

            self.assertEqual(result['count'], 2)
            self.assertEqual(result['published_count'], 2)
            self.assertEqual(len(calls), 2)
            self.assertIn('No words, no letters, no numbers', calls[0]['prompt'])
            self.assertIn('年度总结', calls[0]['prompt'])
            self.assertIn('关键进展', calls[1]['prompt'])
            self.assertTrue((deck / 'images/page_001_background.png').is_file())
            self.assertTrue((deck / 'images/page_002_background.png').is_file())
            with Image.open(deck / 'images/page_001_background.png') as image:
                self.assertEqual(image.size, (1280, 720))
            manifest = json.loads(
                (deck / 'background_images.json').read_text(encoding='utf-8'),
            )
            self.assertTrue(manifest['enabled'])
            self.assertEqual([item['page_no'] for item in manifest['pages']], [1, 2])
            self.assertEqual(manifest['pages'][0]['original_size'], {
                'width': 1024,
                'height': 1024,
            })
            self.assertEqual(manifest['pages'][0]['size'], {
                'width': 1280,
                'height': 720,
                'aspect': '16:9',
            })
            self.assertEqual(save.call_count, 2)
            self.assertEqual(save.call_args_list[0].kwargs['key'], 'background_images')
            self.assertTrue(save.call_args_list[0].kwargs['internal_publish'])

    def test_generates_from_approved_prompts_before_outline_exists(self) -> None:
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            deck = root / 'deck'
            (deck / 'images').mkdir(parents=True)
            (deck / 'task_pack.json').write_text(json.dumps({
                'params': {'page_count': 2},
            }), encoding='utf-8')
            generated = []
            for page in (1, 2):
                path = root / f'prepared_{page}.png'
                self._write_generated_image(path, (1600, 900))
                generated.append(path)
            calls = []

            def fake_image_generator(**kwargs):
                calls.append(kwargs)
                return {'local_path': str(generated[len(calls) - 1])}

            with mock.patch.object(
                TOOLS, '_resolve_deck_dir', return_value=deck,
            ), mock.patch.object(
                TOOLS, '_ui_slot_order_list', return_value=[],
            ), mock.patch.object(
                TOOLS, 'image_generator', side_effect=fake_image_generator,
            ), mock.patch.object(
                TOOLS, '_save_artifact', return_value={'stored': True},
            ):
                result = TOOLS.ppt_generate_background_images(str(deck), [
                    {'page_no': 1, 'prompt': 'series anchor, opening variation'},
                    {'page_no': 2, 'prompt': 'series anchor, closing variation'},
                ])

            self.assertEqual(result['generated_count'], 2)
            self.assertEqual([call['prompt'] for call in calls], [
                'series anchor, opening variation',
                'series anchor, closing variation',
            ])
            self.assertFalse((deck / 'outline.json').exists())

    def test_targeted_regeneration_uses_approved_prompts_and_overwrites_positions(self) -> None:
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            deck = self._deck(root)
            outline = json.loads((deck / 'outline.json').read_text(encoding='utf-8'))
            outline['pages'].append({'page_no': 3, 'title': '未来规划'})
            (deck / 'outline.json').write_text(
                json.dumps(outline, ensure_ascii=False), encoding='utf-8',
            )
            previous = []
            for page in (1, 2, 3):
                rel = f'images/page_{page:03d}_background.png'
                (deck / rel).write_bytes(f'old-{page}'.encode())
                previous.append({
                    'page_no': page,
                    'local_path': rel,
                    'prompt': f'old prompt {page}',
                })
            (deck / 'background_images.json').write_text(json.dumps({
                'enabled': True,
                'pages': previous,
            }), encoding='utf-8')
            generated = []
            for page in (1, 2):
                path = root / f'new_{page}.png'
                self._write_generated_image(path)
                generated.append(path)
            calls = []

            def fake_image_generator(**kwargs):
                calls.append(kwargs)
                return {'local_path': str(generated[len(calls) - 1])}

            with mock.patch.object(
                TOOLS, '_resolve_deck_dir', return_value=deck,
            ), mock.patch.object(
                TOOLS, '_ui_slot_order_list', return_value=[10, 11, 12],
            ), mock.patch.object(
                TOOLS, 'image_generator', side_effect=fake_image_generator,
            ), mock.patch.object(
                TOOLS, '_save_artifact', return_value={'stored': True},
            ) as save:
                result = TOOLS.ppt_generate_background_images(
                    str(deck),
                    prompts_json=[
                        {'page_no': 1, 'prompt': 'approved connected prompt one'},
                        {'page_no': 2, 'prompt': 'approved connected prompt two'},
                    ],
                    pages_json=[1, 2],
                    replace=True,
                )

            self.assertEqual(result['updated_pages'], [1, 2])
            self.assertEqual(result['generated_count'], 2)
            self.assertEqual([call['prompt'] for call in calls], [
                'approved connected prompt one',
                'approved connected prompt two',
            ])
            self.assertEqual([
                call.kwargs['publisher_list_index'] for call in save.call_args_list
            ], [10, 11])
            manifest = json.loads(
                (deck / 'background_images.json').read_text(encoding='utf-8'),
            )
            self.assertEqual(len(manifest['pages']), 3)
            self.assertEqual(manifest['pages'][2]['prompt'], 'old prompt 3')
            self.assertEqual((deck / 'images/page_003_background.png').read_bytes(), b'old-3')

    def test_inserted_background_adds_one_ui_position_without_republishing_old_images(self) -> None:
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            deck = self._deck(root)
            outline = json.loads((deck / 'outline.json').read_text(encoding='utf-8'))
            outline['pages'] = [
                outline['pages'][0],
                {'page_no': 2, 'page_kind': 'content', 'title': '插入页'},
                {**outline['pages'][1], 'page_no': 3},
            ]
            (deck / 'outline.json').write_text(
                json.dumps(outline, ensure_ascii=False), encoding='utf-8',
            )
            existing = []
            for page in (1, 3):
                rel = f'images/page_{page:03d}_background.png'
                (deck / rel).write_bytes(f'old-{page}'.encode())
                existing.append({
                    'page_no': page,
                    'local_path': rel,
                    'prompt': f'old prompt {page}',
                })
            (deck / 'background_images.json').write_text(json.dumps({
                'enabled': True,
                'pages': existing,
            }), encoding='utf-8')
            generated = root / 'inserted.png'
            self._write_generated_image(generated, (1600, 900))
            order = [10, 11]

            with mock.patch.object(
                TOOLS, '_resolve_deck_dir', return_value=deck,
            ), mock.patch.object(
                TOOLS, '_ui_slot_order_list', return_value=order,
            ), mock.patch.object(
                TOOLS, '_reserve_ui_slot_position', return_value=99,
            ) as reserve, mock.patch.object(
                TOOLS, 'image_generator', return_value={'local_path': str(generated)},
            ) as generate, mock.patch.object(
                TOOLS, '_save_artifact', return_value={'stored': True},
            ) as save:
                result = TOOLS.ppt_generate_background_images(
                    str(deck),
                    prompts_json=[{'page_no': 2, 'prompt': 'new approved prompt'}],
                    pages_json=[2],
                    replace=False,
                    insert_before=2,
                )

            generate.assert_called_once()
            reserve.assert_called_once()
            save.assert_called_once()
            self.assertEqual(save.call_args.kwargs['publisher_list_index'], 99)
            self.assertEqual(order, [10, 99, 11])
            self.assertEqual(result['updated_pages'], [2])
            self.assertEqual(result['generated_count'], 1)
            self.assertEqual(result['published_count'], 1)
            self.assertEqual(
                (deck / 'images/page_001_background.png').read_bytes(), b'old-1',
            )
            self.assertEqual(
                (deck / 'images/page_003_background.png').read_bytes(), b'old-3',
            )
            manifest = json.loads(
                (deck / 'background_images.json').read_text(encoding='utf-8'),
            )
            self.assertEqual(
                [entry['page_no'] for entry in manifest['pages']], [1, 2, 3],
            )

    def test_surfaces_image_model_failure_reason(self) -> None:
        with tempfile.TemporaryDirectory() as temp:
            deck = self._deck(Path(temp))
            with mock.patch.object(
                TOOLS, '_resolve_deck_dir', return_value=deck,
            ), mock.patch.object(
                TOOLS,
                'image_generator',
                return_value={
                    'success': False,
                    'error': {'reason': 'image_generator model is not configured'},
                },
            ):
                with self.assertRaisesRegex(
                    ToolExecutionError,
                    'page 1 background generation failed: image_generator model is not configured',
                ):
                    TOOLS.ppt_generate_background_images(str(deck))

    def test_checkpoints_completed_pages_and_resumes_after_failure(self) -> None:
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            deck = self._deck(root)
            first = root / 'first.png'
            second = root / 'second.png'
            self._write_generated_image(first)
            self._write_generated_image(second)

            with mock.patch.object(
                TOOLS, '_resolve_deck_dir', return_value=deck,
            ), mock.patch.object(
                TOOLS, 'image_generator', side_effect=[
                    {'local_path': str(first)},
                    TimeoutError('provider connect timeout'),
                ],
            ), self.assertRaisesRegex(
                ToolExecutionError, 'page 2 background generation failed',
            ):
                TOOLS.ppt_generate_background_images(str(deck))

            checkpoint = json.loads(
                (deck / 'background_images.json').read_text(encoding='utf-8'),
            )
            self.assertEqual(
                [item['page_no'] for item in checkpoint['pages']], [1],
            )

            with mock.patch.object(
                TOOLS, '_resolve_deck_dir', return_value=deck,
            ), mock.patch.object(
                TOOLS, '_ui_slot_order_list', return_value=[],
            ), mock.patch.object(
                TOOLS, 'image_generator', return_value={'local_path': str(second)},
            ) as generate, mock.patch.object(
                TOOLS, '_save_artifact', return_value={'stored': True},
            ) as save:
                result = TOOLS.ppt_generate_background_images(str(deck))

            generate.assert_called_once()
            self.assertEqual(result['generated_count'], 1)
            self.assertEqual(result['reused_count'], 1)
            self.assertEqual(result['published_count'], 2)
            self.assertEqual(save.call_count, 2)

    def test_targeted_recovery_backfills_predecessors_before_ui_publication(self) -> None:
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            deck = self._deck(root)
            previous = []
            for page in (1, 2):
                rel = f'images/page_{page:03d}_background.png'
                self._write_generated_image(deck / rel)
                previous.append({
                    'page_no': page,
                    'local_path': rel,
                    'prompt': f'prompt {page}',
                })
            (deck / 'background_images.json').write_text(json.dumps({
                'enabled': True,
                'pages': previous,
            }), encoding='utf-8')

            with mock.patch.object(
                TOOLS, '_resolve_deck_dir', return_value=deck,
            ), mock.patch.object(
                TOOLS, '_ui_slot_order_list', return_value=[],
            ), mock.patch.object(
                TOOLS, 'image_generator', side_effect=AssertionError(
                    'existing page must be reused',
                ),
            ), mock.patch.object(
                TOOLS, '_save_artifact', return_value={'stored': True},
            ) as save:
                result = TOOLS.ppt_generate_background_images(
                    str(deck), pages_json=[2], replace=False,
                )

            self.assertEqual(result['published_count'], 2)
            self.assertEqual([
                call.kwargs['caption'] for call in save.call_args_list
            ], ['第 1 页 AI 底图', '第 2 页 AI 底图'])

    def test_ui_publication_failure_preserves_exact_reason(self) -> None:
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            deck = self._deck(root)
            generated = []
            for page in (1, 2):
                path = root / f'generated_{page}.png'
                self._write_generated_image(path)
                generated.append(path)

            with mock.patch.object(
                TOOLS, '_resolve_deck_dir', return_value=deck,
            ), mock.patch.object(
                TOOLS, 'image_generator', side_effect=[
                    {'local_path': str(path)} for path in generated
                ],
            ), mock.patch.object(
                TOOLS, '_save_artifact', return_value={
                    'ok': False,
                    'error': 'workflow publisher unavailable',
                },
            ), self.assertRaisesRegex(
                ToolExecutionError, 'workflow publisher unavailable',
            ):
                TOOLS.ppt_generate_background_images(str(deck))

    def test_preview_inlines_local_css_background_for_srcdoc_and_export(self) -> None:
        with tempfile.TemporaryDirectory() as temp:
            deck = self._deck(Path(temp))
            page = deck / 'pages/page_001.html'
            page.parent.mkdir()
            background = deck / 'images/page_001_background.png'
            self._write_generated_image(background, (1280, 720))
            html = (
                '<html><head><style>'
                '#bg{background-image:url("../images/page_001_background.png")}'
                '</style></head><body><div id="bg"></div></body></html>'
            )

            public, inlined = TOOLS._inline_preview_images(html, deck, page)

            self.assertEqual(inlined, 1)
            self.assertIn('background-image:url("data:image/png;base64,', public)
            self.assertNotIn('../images/page_001_background.png', public)


class PptCapabilityCheckTest(unittest.TestCase):
    def test_disabled_backgrounds_skip_image_model_check(self) -> None:
        with mock.patch.object(
            TOOLS,
            'is_model_role_available',
            side_effect=AssertionError('disabled backgrounds must not inspect models'),
        ):
            result = TOOLS.check_ppt_workflow_capabilities(
                'AI_BACKGROUND_IMAGES: disabled',
            )

        self.assertEqual(result['status'], 'ready')
        self.assertEqual(result['required'], [])
        self.assertEqual(result['checks'], [])

    def test_enabled_backgrounds_return_settings_card_when_model_is_missing(self) -> None:
        with mock.patch.object(
            TOOLS, 'is_model_role_available', return_value=False,
        ), self.assertRaisesRegex(
            ToolExecutionError, 'MEDIA_CAPABILITY_DEPENDENCY_MISSING',
        ) as captured:
            TOOLS.check_ppt_workflow_capabilities(
                'AI_BACKGROUND_IMAGES: enabled',
            )

        message = str(captured.exception)
        self.assertIn('image_generator', message)
        self.assertIn('/settings?section=models', message)
        self.assertIn('尚未配置可用的文生图模型', message)


class BackgroundPromptPublicationTest(unittest.TestCase):
    def test_first_publication_uses_prepared_deck_page_count_before_outline(self) -> None:
        with tempfile.TemporaryDirectory() as temp:
            deck = Path(temp) / 'deck'
            deck.mkdir()
            (deck / 'task_pack.json').write_text(json.dumps({
                'params': {'page_count': 2},
            }), encoding='utf-8')
            with mock.patch.object(
                TOOLS, '_resolve_deck_dir', return_value=deck,
            ), mock.patch.object(
                TOOLS, '_ui_slot_order_list', return_value=[],
            ), mock.patch.object(
                TOOLS, '_save_artifact', return_value={'stored': True},
            ):
                result = TOOLS.ppt_publish_background_prompts(str(deck), [
                    {'page_no': 1, 'prompt': 'shared anchor, opening scene'},
                    {'page_no': 2, 'prompt': 'shared anchor, closing scene'},
                ])

            self.assertEqual(result['count'], 2)
            self.assertFalse((deck / 'outline.json').exists())

    def test_full_publish_then_targeted_replacement_preserves_other_prompts(self) -> None:
        with tempfile.TemporaryDirectory() as temp:
            deck = BackgroundImageGenerationTest()._deck(Path(temp))
            with mock.patch.object(
                TOOLS, '_resolve_deck_dir', return_value=deck,
            ), mock.patch.object(
                TOOLS, '_ui_slot_order_list', return_value=[],
            ), mock.patch.object(
                TOOLS, '_save_artifact', return_value={'stored': True},
            ) as first_save:
                first = TOOLS.ppt_publish_background_prompts(str(deck), [
                    {'page_no': 1, 'prompt': 'shared series anchor, cover variation'},
                    {'page_no': 2, 'prompt': 'shared series anchor, progress variation'},
                ])

            self.assertEqual(first['count'], 2)
            self.assertEqual([
                call.kwargs['publisher_list_index'] for call in first_save.call_args_list
            ], [0, 1])

            with mock.patch.object(
                TOOLS, '_resolve_deck_dir', return_value=deck,
            ), mock.patch.object(
                TOOLS, '_ui_slot_order_list', return_value=[4, 7],
            ), mock.patch.object(
                TOOLS, '_save_artifact', return_value={'stored': True},
            ) as replace_save:
                replaced = TOOLS.ppt_publish_background_prompts(str(deck), [
                    {'page_no': 2, 'prompt': 'shared series anchor, revised page two'},
                ])

            self.assertEqual(replaced['updated_pages'], [2])
            self.assertEqual(replace_save.call_args.kwargs['publisher_list_index'], 7)
            manifest = json.loads(
                (deck / 'background_prompts.json').read_text(encoding='utf-8'),
            )
            self.assertEqual(manifest['pages'][0]['prompt'], 'shared series anchor, cover variation')
            self.assertEqual(manifest['pages'][1]['prompt'], 'shared series anchor, revised page two')

    def test_page_deletion_compacts_approved_prompt_positions(self) -> None:
        with tempfile.TemporaryDirectory() as temp:
            deck = Path(temp)
            (deck / 'background_prompts.json').write_text(json.dumps({
                'pages': [
                    {'page_no': 1, 'prompt': 'one'},
                    {'page_no': 2, 'prompt': 'two'},
                    {'page_no': 3, 'prompt': 'three'},
                ],
            }), encoding='utf-8')

            result = TOOLS._remove_background_prompt_page(deck, 2)

            self.assertTrue(result['found'])
            manifest = json.loads(
                (deck / 'background_prompts.json').read_text(encoding='utf-8'),
            )
            self.assertEqual(manifest['pages'], [
                {'page_no': 1, 'prompt': 'one'},
                {'page_no': 2, 'prompt': 'three'},
            ])


if __name__ == '__main__':
    unittest.main()

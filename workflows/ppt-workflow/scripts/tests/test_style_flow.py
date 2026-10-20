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
    multimodal = types.ModuleType('lazymind.chat.engine.tools.multimodal')
    multimodal.image_generator = lambda *args, **kwargs: None
    static_file = types.ModuleType('lazymind.chat.service.utils.static_file_url')
    static_file._upload_root = lambda: Path(tempfile.gettempdir())
    static_file.file_relative_path = lambda value: value
    static_file.encode_static_file_path = lambda value: value
    static_file.local_path_from_static_file_url = lambda value: value
    model_config = types.ModuleType('lazymind.model_config')
    model_config.is_model_role_available = lambda _role: True

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
        'lazymind.chat.service': types.ModuleType('lazymind.chat.service'),
        'lazymind.chat.service.utils': types.ModuleType('lazymind.chat.service.utils'),
        'lazymind.chat.service.utils.static_file_url': static_file,
        'lazymind.model_config': model_config,
    }
    return modules


TOOLS_PATH = Path(__file__).resolve().parents[1] / 'tools.py'
SPEC = importlib.util.spec_from_file_location('ppt_style_flow_tools_test', TOOLS_PATH)
TOOLS = importlib.util.module_from_spec(SPEC)
assert SPEC.loader is not None
with mock.patch.dict(sys.modules, _import_stubs()):
    SPEC.loader.exec_module(TOOLS)


class StyleFlowCombinationTest(unittest.TestCase):
    COMBINATIONS = (
        ('auto', False, 'fast'),
        ('auto', True, 'fast'),
        ('preview_choice', False, 'standard'),
        ('preview_choice', True, 'standard'),
    )

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


if __name__ == '__main__':
    unittest.main()

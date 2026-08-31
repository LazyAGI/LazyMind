from __future__ import annotations

import importlib.util
from pathlib import Path
import unittest
from unittest import mock

import yaml


def _repo_root() -> Path:
    return Path(__file__).resolve().parents[4]


def _load_tools():
    path = _repo_root() / 'workflows' / 'image-workflow' / 'scripts' / 'tools.py'
    spec = importlib.util.spec_from_file_location('image_workflow_search_tools', path)
    assert spec and spec.loader
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


class _ProbeResponse:
    status_code = 206
    headers = {'Content-Type': 'image/jpeg'}
    url = 'https://cdn.example.com/final/photo.jpg?token=kept'

    def __init__(self, data: bytes):
        self._data = data
        self.closed = False

    def raise_for_status(self):
        return None

    def iter_content(self, chunk_size: int):
        for offset in range(0, len(self._data), chunk_size):
            yield self._data[offset:offset + chunk_size]

    def close(self):
        self.closed = True


class ImageWorkflowSearchValidationTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.tools = _load_tools()

    def test_direct_image_filter_rejects_search_pages_and_keeps_signed_files(self):
        self.assertFalse(
            self.tools._is_image_url('https://www.gettyimages.com/photos/erling-haaland')
        )
        self.assertTrue(
            self.tools._is_image_url(
                'https://media.example.com/haaland.jpg?s=612x612&token=do-not-drop'
            )
        )

    def test_remote_probe_reads_past_first_eight_kib_when_metadata_is_large(self):
        response = _ProbeResponse(b'\xff\xd8' + b'0' * (40 * 1024))
        inspected_sizes = []

        def dimensions(data: bytes):
            inspected_sizes.append(len(data))
            if len(data) <= self.tools._PROBE_BYTES:
                raise OSError('Truncated File Read')
            return 1600, 1200, 'JPEG'

        with mock.patch.object(self.tools.requests, 'get', return_value=response), mock.patch.object(
            self.tools, '_probe_image_dimensions', side_effect=dimensions,
        ):
            result = self.tools._probe_remote_image(
                'https://cdn.example.com/original/photo.jpg?token=kept'
            )

        self.assertEqual(result, (response.url, 1600, 1200, 'JPEG'))
        self.assertGreater(max(inspected_sizes), self.tools._PROBE_BYTES)
        self.assertTrue(response.closed)

    def test_batch_search_validates_every_exact_url_and_reports_factual_counts(self):
        urls = [
            'https://cdn.example.com/a.jpg?signature=one',
            'https://cdn.example.com/b.jpg?signature=two',
            'https://cdn.example.com/c.jpg?signature=three',
            'https://cdn.example.com/d.jpg?signature=four',
        ]
        results = {
            urls[0]: {
                'status': 'invalid', 'original_url': urls[0], 'url': urls[0],
                'reason': 'not an image',
            },
            urls[1]: {
                'status': 'ok', 'original_url': urls[1], 'url': urls[1],
                'width': 1600, 'height': 1200, 'format': 'JPEG',
            },
            urls[2]: {
                'status': 'ok', 'original_url': urls[2], 'url': urls[2],
                'width': 270, 'height': 380, 'format': 'JPEG',
            },
            urls[3]: {
                'status': 'ok', 'original_url': urls[3], 'url': urls[3],
                'width': 1000, 'height': 1000, 'format': 'JPEG',
            },
        }

        with mock.patch.object(
            self.tools, '_search_image_candidates', side_effect=AssertionError('must not search'),
        ), mock.patch.object(
            self.tools, '_validate_image_candidate', side_effect=lambda url: dict(results[url]),
        ) as validate:
            result = self.tools.image_search_and_validate(
                'Erling Haaland Manchester City jersey portrait',
                target_valid=2,
                max_candidates=15,
                candidate_urls=urls,
            )

        self.assertEqual(result['status'], 'ok')
        self.assertEqual(result['candidate_count'], 4)
        self.assertEqual(result['validated_count'], 4)
        self.assertEqual(result['valid_count'], 3)
        self.assertEqual(result['invalid_count'], 1)
        self.assertEqual(result['selected_count'], 2)
        self.assertEqual(result['candidate_source'], 'provided')
        self.assertEqual(
            [item['url'] for item in result['selected']],
            [urls[1], urls[3]],
        )
        self.assertEqual({call.args[0] for call in validate.call_args_list}, set(urls))

    def test_candidate_input_accepts_web_search_payload_and_rejects_result_pages(self):
        signed = 'https://cdn.example.com/photo.jpg?token=must-stay'
        payload = [{
            'title': 'summary',
            'url': 'https://example.com/photos/person',
            'extra': {
                'images': [signed],
            },
        }]

        self.assertEqual(
            self.tools._normalize_candidate_urls(payload, 15),
            [signed],
        )

    def test_workflow_exposes_batch_tool_and_requires_exact_report_counters(self):
        root = _repo_root() / 'workflows' / 'image-workflow'
        workflow = yaml.safe_load((root / 'workflow.yaml').read_text(encoding='utf-8'))
        state = yaml.safe_load((root / 'scenario' / 'state.yml').read_text(encoding='utf-8'))
        functions = {
            name
            for script in workflow['tool_scripts']
            for name in script['functions']
        }
        collect = state['steps']['collect_materials']

        self.assertIn('image_search_and_validate', functions)
        self.assertIn('image_search_and_validate', collect['tools'])
        self.assertIn('copy its counters exactly', collect['prompt'])
        self.assertIn('including every query/signature parameter', collect['prompt'])
        self.assertIn('as candidate_urls', collect['prompt'])

    def test_analyze_requires_search_then_edit_to_collect_a_source(self):
        root = _repo_root() / 'workflows' / 'image-workflow'
        state = yaml.safe_load((root / 'scenario' / 'state.yml').read_text(encoding='utf-8'))
        analyze = state['steps']['analyze_subject']

        self.assertIn('HIGHEST-PRIORITY SEARCH-THEN-EDIT OVERRIDE', analyze['prompt'])
        self.assertIn('classify it as FIND_AND_EDIT', analyze['prompt'])
        self.assertIn('An explicit request to find/search an image is always COLLECT', analyze['prompt'])
        self.assertIn('must use WORKFLOW: FIND_AND_EDIT', analyze['acceptance_criteria'])

    def test_route_selector_sends_integrated_text_edits_to_enhance(self):
        self.assertEqual(
            self.tools.select_image_route('WORKFLOW: FIND_AND_EDIT\nSKIP_STEPS: generate_image'),
            {
                'status': 'ok',
                'workflow': 'FIND_AND_EDIT',
                'next_step': 'enhance_image',
                'control': {'next_step': 'enhance_image'},
            },
        )
        self.assertEqual(
            self.tools.select_image_route('WORKFLOW: CREATE_STATIC_MEME')['next_step'],
            'generate_image',
        )
        with self.assertRaisesRegex(ValueError, 'exactly one WORKFLOW'):
            self.tools.select_image_route('NEXT_STEPS: enhance_image')


if __name__ == '__main__':
    unittest.main()

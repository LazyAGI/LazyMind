import importlib.util
import http.client
import io
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch


SCRIPT = Path(__file__).resolve().parents[2] / 'frontend/scripts/generate_upstream_model_catalog.py'
SPEC = importlib.util.spec_from_file_location('upstream_catalog_generator', SCRIPT)
catalog = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(catalog)


class UpstreamCatalogTests(unittest.TestCase):
    def test_specialized_type_and_exact_input_limit_survive_normalization(self):
        model = catalog.from_models_dev({
            'id': 'mini_lm_l12_v2', 'type': 'embedding',
            'modalities': {'input': ['text'], 'output': ['text']},
            'limit': {'context': 8192, 'input': 8000, 'output': 384},
        }, 'fallback')
        self.assertEqual(model['model_type'], 'embed')
        self.assertEqual(model['max_input'], 8000)
        image = catalog.from_models_dev({
            'id': 'image-model', 'type': 'image',
            'modalities': {'input': ['text', 'image'], 'output': ['text', 'image']},
        }, 'fallback')
        self.assertEqual(image['model_type'], 'image_editing')

    def test_regional_fallback_does_not_merge_models_from_another_endpoint(self):
        data = {
            'alibaba-cn': {'models': {'china': {'id': 'china'}}},
            'alibaba': {'models': {'global': {'id': 'global'}}},
        }
        self.assertEqual(list(catalog.collect_models_dev(data, ['alibaba-cn', 'alibaba'])), ['china'])

    def test_openrouter_api_is_authoritative_for_available_models_and_limits(self):
        dev = {
            'openai': {'models': {'direct': {'id': 'direct'}}},
            'openrouter': {'models': {
                'retired': {'id': 'retired'},
                'current': {'id': 'current', 'limit': {'context': 1000000}},
            }},
        }
        router = {'data': [{
            'id': 'current', 'context_length': 8192,
            'architecture': {'input_modalities': ['text'], 'output_modalities': ['text']},
        }]}
        result = catalog.build_catalog(dev, router)
        self.assertEqual([m['name'] for m in result['providers']['OpenRouter']], ['current'])
        self.assertEqual(result['providers']['OpenRouter'][0]['context'], 8192)

    def test_empty_or_malformed_sources_do_not_produce_a_successful_snapshot(self):
        for dev, router in [({}, {'data': []}), ([], {'data': []}), ({'openai': {}}, {'error': 'bad'})]:
            with self.subTest(dev=dev, router=router), self.assertRaises(ValueError):
                catalog.build_catalog(dev, router)

    def test_fresh_snapshot_is_reused_for_both_outputs_without_network(self):
        with tempfile.TemporaryDirectory() as directory:
            first = Path(directory) / 'backend.json'
            second = Path(directory) / 'frontend.json'
            snapshot = {
                'schema_version': catalog.SCHEMA_VERSION, 'generated_at': catalog.utc_now(),
                'providers': {'OpenAI': [catalog.blank_model('example')]},
            }
            first.write_text(json.dumps(snapshot), encoding='utf-8')
            with patch.object(catalog, 'fetch_json', side_effect=AssertionError('network must not be used')):
                catalog.generate([str(first), str(second)])
            self.assertEqual(json.loads(first.read_text()), json.loads(second.read_text()))

    def test_network_failure_preserves_valid_previous_snapshot(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / 'catalog.json'
            snapshot = {
                'schema_version': catalog.SCHEMA_VERSION, 'generated_at': '2020-01-01T00:00:00Z',
                'providers': {'OpenAI': [catalog.blank_model('example')]},
            }
            path.write_text(json.dumps(snapshot), encoding='utf-8')
            with patch.object(catalog, 'fetch_json', side_effect=OSError('offline')):
                catalog.generate([str(path)])
            self.assertEqual(json.loads(path.read_text()), snapshot)

    def test_network_failure_without_a_snapshot_fails(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / 'catalog.json'
            with patch.object(catalog, 'fetch_json', side_effect=OSError('offline')):
                with self.assertRaises(OSError):
                    catalog.generate([str(path)])
            self.assertFalse(path.exists())

    def test_previous_generator_snapshot_remains_usable_during_an_offline_upgrade(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / 'catalog.json'
            model = catalog.blank_model('old-catalog-model')
            del model['max_input']
            del model['model_type']
            snapshot = {'generated_at': catalog.utc_now(), 'providers': {'OpenAI': [model]}}
            path.write_text(json.dumps(snapshot), encoding='utf-8')
            with patch.object(catalog, 'fetch_json', side_effect=OSError('offline')) as fetch:
                catalog.generate([str(path)])
            fetch.assert_called_once()
            result = json.loads(path.read_text())
            self.assertEqual(result['providers']['OpenAI'][0]['name'], 'old-catalog-model')
            self.assertEqual(result['providers']['OpenAI'][0]['max_input'], 0)
            self.assertEqual(result['schema_version'], 0)
            self.assertEqual(result['generated_at'], snapshot['generated_at'])

    def test_fetch_recovers_from_a_transient_network_failure(self):
        responses = [OSError('retry'), io.StringIO('{"ok": true}')]
        with patch.object(catalog.urllib.request, 'urlopen', side_effect=responses):
            with patch.object(catalog.time, 'sleep'):
                self.assertEqual(catalog.fetch_json('https://models.dev/api.json?type=all'), {'ok': True})

    def test_truncated_http_response_keeps_previous_snapshot(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / 'catalog.json'
            snapshot = {
                'schema_version': catalog.SCHEMA_VERSION, 'generated_at': '2020-01-01T00:00:00Z',
                'providers': {'OpenAI': [catalog.blank_model('example')]},
            }
            path.write_text(json.dumps(snapshot), encoding='utf-8')
            with patch.object(catalog.urllib.request, 'urlopen', side_effect=http.client.IncompleteRead(b'{', 2)):
                with patch.object(catalog.time, 'sleep'):
                    catalog.generate([str(path)])
            self.assertEqual(json.loads(path.read_text()), snapshot)


if __name__ == '__main__':
    unittest.main()

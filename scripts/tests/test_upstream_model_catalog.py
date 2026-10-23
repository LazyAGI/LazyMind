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
        self.assertEqual(image['model_type'], 'text2image')

    def test_openrouter_keeps_raw_modalities_and_classifies_dedicated_tasks(self):
        cases = [
            (['text'], ['text'], 'llm'),
            (['text'], ['embeddings'], 'embed'),
            (['text', 'image'], ['embeddings'], 'multimodal_embedding'),
            (['text', 'pdf'], ['embeddings'], 'multimodal_embedding'),
            (['text', 'future-input'], ['embeddings'], 'multimodal_embedding'),
            (['text'], ['image'], 'text2image'),
            (['image', 'text'], ['text', 'image'], 'text2image'),
            (['text', 'image'], ['video'], 'text2video'),
            (['text'], ['speech'], 'tts'),
            (['audio'], ['transcription'], 'stt'),
            (['audio'], ['text'], 'stt'),
            (['text', 'image'], ['rerank'], 'rerank'),
            (['text'], ['decisions'], ''),
            (['text'], ['text', 'audio'], ''),
            (['text', 'future-input'], ['future-output'], ''),
            (['text'], ['image', 'future-output'], ''),
            ([], [], ''),
        ]
        for inputs, outputs, expected in cases:
            with self.subTest(inputs=inputs, outputs=outputs):
                model = catalog.finalize(catalog.from_openrouter({
                    'id': 'opaque-model',
                    'architecture': {'input_modalities': inputs, 'output_modalities': outputs},
                }))
                self.assertEqual(model['input'], inputs)
                self.assertEqual(model['output'], outputs)
                self.assertEqual(model['model_type'], expected)

    def test_models_dev_explicit_editing_is_distinct_from_image_generation(self):
        for kind, expected in [('image', 'text2image'), ('image_editing', 'image_editing')]:
            with self.subTest(kind=kind):
                model = catalog.from_models_dev({
                    'id': 'opaque-image', 'type': kind,
                    'modalities': {'input': ['text', 'image'], 'output': ['image']},
                }, 'fallback')
                self.assertEqual(model['model_type'], expected)

    def test_models_dev_does_not_rename_unsupported_tasks_as_chat(self):
        for kind in ['decision', 'realtime', 'future-task']:
            with self.subTest(kind=kind):
                model = catalog.from_models_dev({
                    'id': 'opaque', 'type': kind,
                    'modalities': {'input': ['text'], 'output': ['text']},
                }, 'fallback')
                self.assertEqual(model['model_type'], '')

    def test_live_openrouter_modalities_override_stale_type_enrichment(self):
        dev = {
            'openai': {'models': {'direct': {'id': 'direct'}}},
            'openrouter': {'models': {
                'render': {'id': 'render', 'type': 'chat'},
                'unknown': {'id': 'unknown', 'type': 'chat'},
            }},
        }
        router = {'data': [
            {'id': 'render', 'architecture': {
                'input_modalities': ['text', 'image'], 'output_modalities': ['image', 'text'],
            }},
            {'id': 'unknown', 'architecture': {'input_modalities': ['text'], 'output_modalities': ['decisions']}},
        ]}
        models = {m['name']: m for m in catalog.build_catalog(dev, router)['providers']['OpenRouter']}
        self.assertEqual(models['render']['model_type'], 'text2image')
        self.assertEqual(models['unknown']['model_type'], '')
        self.assertEqual(models['unknown']['output'], ['decisions'])

    def test_legacy_snapshot_forces_refresh_to_request_all_modalities(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / 'catalog.json'
            path.write_text(json.dumps({
                'schema_version': 1, 'generated_at': catalog.utc_now(),
                'providers': {'OpenAI': [catalog.blank_model('previous')]},
            }), encoding='utf-8')
            dev = {'openai': {'models': {'direct': {'id': 'direct'}}}}
            router = {'data': [{'id': 'opaque', 'architecture': {
                'input_modalities': ['text'], 'output_modalities': ['embeddings'],
            }}]}
            with patch.object(catalog, 'fetch_json', side_effect=[dev, router]) as fetch:
                catalog.generate([str(path)])
            self.assertEqual(
                fetch.call_args_list[1].args[0], 'https://openrouter.ai/api/v1/models?output_modalities=all',
            )
            result = catalog.read_snapshot(str(path))
            self.assertEqual(result['schema_version'], catalog.SCHEMA_VERSION)
            self.assertEqual(result['providers']['OpenRouter'][0]['output'], ['embeddings'])

    def test_previous_schema_snapshot_remains_usable_offline_but_is_not_marked_fresh(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / 'catalog.json'
            snapshot = {
                'schema_version': 1, 'generated_at': catalog.utc_now(),
                'providers': {'OpenAI': [catalog.blank_model('old-catalog-model')]},
            }
            path.write_text(json.dumps(snapshot), encoding='utf-8')
            with patch.object(catalog, 'fetch_json', side_effect=OSError('offline')) as fetch:
                catalog.generate([str(path)])
            fetch.assert_called_once()
            self.assertEqual(json.loads(path.read_text()), snapshot)

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

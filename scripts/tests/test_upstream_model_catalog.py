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
    def test_models_dev_keeps_explicit_feature_booleans_and_missing_is_unknown(self):
        base = {'id': 'opaque', 'modalities': {'input': ['text', 'image'], 'output': ['text']}}
        model = catalog.from_models_dev(dict(base, tool_call=False, reasoning=True), 'fallback')
        self.assertEqual(model.get('feature_support'), {'vision': True, 'tool_calling': False, 'reasoning': True})
        missing = catalog.from_models_dev({'id': 'not-a-capability-name', 'type': 'chat'}, 'fallback')
        self.assertNotIn('feature_support', missing)
        invalid = catalog.from_models_dev(dict(base, tool_call='false', reasoning=1), 'fallback')
        self.assertEqual(invalid.get('feature_support'), {'vision': True})

    def test_openrouter_supported_parameters_require_a_real_list(self):
        base = {'id': 'opaque', 'architecture': {'input_modalities': ['text'], 'output_modalities': ['text']}}
        for params, expected in [(['tools', 'reasoning'], {'vision': False, 'tool_calling': True, 'reasoning': True}),
                                 (['include_reasoning'], {'vision': False, 'tool_calling': False, 'reasoning': True}),
                                 ([], {'vision': False, 'tool_calling': False, 'reasoning': False})]:
            with self.subTest(params=params):
                support = catalog.from_openrouter(dict(base, supported_parameters=params)).get('feature_support')
                self.assertEqual(support, expected)
        for value in [None, 'tools', [None]]:
            support = catalog.from_openrouter(dict(base, supported_parameters=value)).get('feature_support')
            self.assertEqual(support, {'vision': False})
        self.assertEqual(catalog.from_openrouter(base).get('feature_support'), {'vision': False})

    def test_recognition_keeps_conflicting_feature_evidence_for_consumers(self):
        data = {'openai': {'models': {'one': {'id': 'same', 'type': 'chat', 'tool_call': True}}},
                'other': {'models': {'two': {'id': 'same', 'type': 'chat', 'tool_call': False}}}}
        records = catalog.collect_recognition_models(data, {})
        self.assertEqual(len(records), 2)
        self.assertEqual({record['feature_support']['tool_calling'] for record in records}, {True, False})

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

    def test_recognition_includes_other_suppliers_without_expanding_available_models(self):
        # These task declarations match the public catalog; their suppliers are
        # recognition evidence, not newly configured LazyMind service endpoints.
        dev = {
            'openai': {'models': {'gpt': {'id': 'gpt'}}},
            'alibaba-cn': {'models': {'regional': {'id': 'regional'}}},
            'alibaba': {'models': {'global-only': {'id': 'global-only'}}},
            'stackit': {'models': {'Qwen/Qwen3-VL-Embedding-8B': {
                'id': 'Qwen/Qwen3-VL-Embedding-8B', 'type': 'embedding',
                'modalities': {'input': ['text', 'image'], 'output': ['text']},
            }}},
            'nano-gpt': {'models': {'THUDM/GLM-Z1-9B-0414': {
                'id': 'THUDM/GLM-Z1-9B-0414',
                'modalities': {'input': ['text'], 'output': ['text']},
            }}},
            'novita-ai': {'models': {'qwen/qwen3-reranker-8b': {
                'id': 'qwen/qwen3-reranker-8b', 'type': 'reranking',
                'modalities': {'input': ['text'], 'output': ['text']},
            }}},
        }
        router = {'data': [{'id': 'live', 'architecture': {
            'input_modalities': ['text'], 'output_modalities': ['text'],
        }}]}
        result = catalog.build_catalog(dev, router)
        recognition = {model['name']: model for model in result.get('recognition_models', [])}
        self.assertIn('Qwen/Qwen3-VL-Embedding-8B', recognition)
        self.assertEqual(recognition['Qwen/Qwen3-VL-Embedding-8B']['model_type'], 'multimodal_embedding')
        self.assertEqual(recognition['THUDM/GLM-Z1-9B-0414']['model_type'], 'llm')
        self.assertEqual(recognition['qwen/qwen3-reranker-8b']['model_type'], 'rerank')
        self.assertIn('global-only', recognition)
        self.assertEqual(set(result['providers']), {
            'Claude', 'DeepSeek', 'Doubao', 'GLM', 'Kimi', 'Minimax',
            'OpenAI', 'Qwen', 'SenseNova', 'SiliconFlow', 'OpenRouter',
        })
        self.assertEqual([model['name'] for model in result['providers']['Qwen']], ['regional'])
        available = {model['name'] for models in result['providers'].values() for model in models}
        self.assertTrue(available.isdisjoint({
            'Qwen/Qwen3-VL-Embedding-8B', 'THUDM/GLM-Z1-9B-0414',
            'qwen/qwen3-reranker-8b', 'global-only',
        }))

    def test_recognition_keeps_same_name_conflicts_and_uses_live_openrouter_modalities(self):
        text = {'id': 'conflicting', 'modalities': {'input': ['text'], 'output': ['text']}}
        dev = {
            'openai': {'models': {'conflicting': text}},
            'unconfigured-a': {'models': {'conflicting': text}},
            'unconfigured-b': {'models': {'conflicting': {
                'id': 'conflicting', 'type': 'embedding',
                'modalities': {'input': ['text'], 'output': ['text']},
            }}},
            'openrouter': {'models': {'render': {
                'id': 'render', 'type': 'chat',
                'modalities': {'input': ['text'], 'output': ['text']},
            }}},
        }
        router = {'data': [{'id': 'render', 'architecture': {
            'input_modalities': ['text', 'image'], 'output_modalities': ['image', 'text'],
        }}]}
        recognition = catalog.build_catalog(dev, router).get('recognition_models', [])
        conflicting = [model for model in recognition if model['name'] == 'conflicting']
        self.assertEqual(sorted(model['model_type'] for model in conflicting), ['embed', 'llm'])
        render = [model for model in recognition if model['name'] == 'render']
        self.assertEqual(len(render), 1)
        self.assertEqual(render[0]['model_type'], 'text2image')
        self.assertEqual(render[0]['output'], ['image', 'text'])

    def test_schema_three_validates_the_recognition_index_before_reusing_a_snapshot(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / 'catalog.json'
            snapshot = {
                'schema_version': 3, 'generated_at': catalog.utc_now(),
                'providers': {'OpenAI': [catalog.blank_model('example')]},
                'recognition_models': [catalog.blank_model('outside-provider')],
            }
            path.write_text(json.dumps(snapshot), encoding='utf-8')
            self.assertIsNotNone(catalog.read_snapshot(str(path)))
            invalid_model = dict(snapshot['recognition_models'][0], model_type=5)
            for invalid in [None, {}, [], [{'name': 'missing-fields'}], [invalid_model]]:
                with self.subTest(invalid=invalid):
                    snapshot['recognition_models'] = invalid
                    path.write_text(json.dumps(snapshot), encoding='utf-8')
                    self.assertIsNone(catalog.read_snapshot(str(path)))
            del snapshot['recognition_models']
            path.write_text(json.dumps(snapshot), encoding='utf-8')
            self.assertIsNone(catalog.read_snapshot(str(path)))

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
            for version in [1, 2]:
                with self.subTest(version=version):
                    snapshot = {
                        'schema_version': version, 'generated_at': catalog.utc_now(),
                        'providers': {'OpenAI': [catalog.blank_model('old-catalog-model')]},
                    }
                    path.write_text(json.dumps(snapshot), encoding='utf-8')
                    with patch.object(catalog, 'fetch_json', side_effect=OSError('offline')) as fetch:
                        catalog.generate([str(path)])
                    fetch.assert_called_once()
                    self.assertEqual(json.loads(path.read_text()), snapshot)

    def test_schema_two_is_refreshed_to_add_missing_recognition_models(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / 'catalog.json'
            path.write_text(json.dumps({
                'schema_version': 2, 'generated_at': catalog.utc_now(),
                'providers': {'OpenAI': [catalog.blank_model('old-catalog-model')]},
            }), encoding='utf-8')
            dev = {'openai': {'models': {'fresh': {
                'id': 'fresh', 'modalities': {'input': ['text'], 'output': ['text']},
            }}}}
            router = {'data': [{'id': 'live', 'architecture': {
                'input_modalities': ['text'], 'output_modalities': ['embeddings'],
            }}]}
            with patch.object(catalog, 'fetch_json', side_effect=[dev, router]):
                catalog.generate([str(path)])
            result = catalog.read_snapshot(str(path))
            self.assertEqual(result['schema_version'], catalog.SCHEMA_VERSION)
            self.assertEqual({model['name'] for model in result['recognition_models']}, {'fresh', 'live'})

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
                'recognition_models': [catalog.blank_model('outside-provider')],
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
                'recognition_models': [catalog.blank_model('outside-provider')],
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
                'recognition_models': [catalog.blank_model('outside-provider')],
            }
            path.write_text(json.dumps(snapshot), encoding='utf-8')
            with patch.object(catalog.urllib.request, 'urlopen', side_effect=http.client.IncompleteRead(b'{', 2)):
                with patch.object(catalog.time, 'sleep'):
                    catalog.generate([str(path)])
            self.assertEqual(json.loads(path.read_text()), snapshot)


if __name__ == '__main__':
    unittest.main()

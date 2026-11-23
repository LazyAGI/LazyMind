"""Provider credentials stay private; real HTTP forwarding is verified locally."""
import base64
import io
import json
import os
import sys
import tempfile
import threading
import unittest
import urllib.error
import urllib.request
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from unittest.mock import patch

from PIL import Image

sys.path.insert(0, str(Path(__file__).parents[1]))
import agent_e2e_providers as providers


def configuration():
    return {'version': 1, 'llm': {'api_key': 'fixture-private-llm-key', 'base_url': 'https://model.example/v1',
            'model': 'fixture-model', 'api': 'openai-compatible'},
            'image': {'api_key': 'fixture-private-image-key', 'base_url': 'https://image.example/v1',
                      'model': 'fixture-image', 'api': 'siliconflow-images'}}


class PrivateProviderTests(unittest.TestCase):
    def write(self, path, data):
        path.write_text(json.dumps(data))
        path.chmod(0o600)

    def test_first_run_reports_the_template_and_secret_configuration_steps(self):
        with tempfile.TemporaryDirectory() as tmp, patch.dict(os.environ, {}, clear=True):
            with self.assertRaises(providers.ProviderConfigurationError) as raised:
                providers.load(Path(tmp) / 'missing.json', required=True)
            self.assertIn('agent_e2e.providers.example.json', str(raised.exception))
            self.assertIn('Secrets', str(raised.exception))

    def test_private_file_loads_but_public_metadata_and_model_files_omit_keys(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / 'private.json'
            value = configuration()
            self.write(path, value)
            actual = providers.load(path, required=True)
            public = json.dumps(providers.public(actual)) + json.dumps(providers.workbuddy_models(actual, proxy=True))
            for name in ('llm', 'image'):
                self.assertNotIn(value[name]['api_key'], public)
            self.assertIn('${CODEBUDDY_API_KEY}', public)
            self.assertIn('${CODEBUDDY_BASE_URL}', public)

    def test_inline_keys_in_the_repository_are_rejected(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp).resolve()
            path = root / 'providers.json'
            self.write(path, configuration())
            with patch.object(providers, 'ROOT', root), self.assertRaisesRegex(providers.ProviderConfigurationError, '仓库外'):
                providers.load(path, required=True)

    @unittest.skipUnless(os.name == 'posix', 'POSIX private file permissions')
    def test_world_readable_private_configuration_is_rejected(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / 'providers.json'
            self.write(path, configuration())
            path.chmod(0o644)
            with self.assertRaisesRegex(providers.ProviderConfigurationError, 'chmod 600'):
                providers.load(path, required=True)

    def test_image_key_is_optional_for_text_cases_and_required_for_meme_cases(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / 'providers.json'
            value = configuration()
            value['image']['api_key'] = ''
            self.write(path, value)
            with patch.dict(os.environ, {}, clear=True):
                self.assertNotIn('image', providers.load(path, required=True))
                with self.assertRaisesRegex(providers.ProviderConfigurationError, 'image 缺少配置字段：api_key'):
                    providers.load(path, required=True, require_image=True)

    def test_ci_secrets_override_file_values_without_being_written_to_disk(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / 'providers.json'
            self.write(path, configuration())
            before = path.read_bytes()
            with patch.dict(os.environ, {'LAZYMIND_CI_LLM_API_KEY': 'ci-runtime-secret', 'LAZYMIND_CI_LLM_API': 'openai-responses'}):
                actual = providers.load(path, required=True)
            self.assertEqual(actual['llm']['api_key'], 'ci-runtime-secret')
            self.assertEqual(actual['llm']['api'], 'openai-responses')
            self.assertEqual(path.read_bytes(), before)

    def test_malformed_configuration_does_not_echo_its_credentials(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / 'providers.json'
            path.write_text('{"api_key":"fixture-private-value",')
            with self.assertRaises(providers.ProviderConfigurationError) as raised:
                providers.load(path, required=True)
            self.assertNotIn('fixture-private-value', str(raised.exception))

    def test_endpoint_does_not_allow_query_credentials_or_embedded_passwords(self):
        for value in ('https://user:secret@example.com/v1', 'https://example.com/v1?key=secret', 'http://remote.example/v1'):
            with self.subTest(value=value), self.assertRaises(providers.ProviderConfigurationError):
                providers.endpoint(value)

    def test_responses_protocol_is_explicit_and_uses_a_base_url(self):
        value = configuration()
        value['llm']['api'] = 'openai-responses'
        result = providers.workbuddy_models(value, proxy=True)['models'][0]
        self.assertEqual(result['api'], 'openai-responses')
        self.assertEqual(result['url'], '${CODEBUDDY_BASE_URL}')

    def test_workbuddy_usage_compatibility_preserves_counts_and_content(self):
        value = {'response': {'output': [{'text': 'actual response'}], 'usage': {
            'input_tokens': 20, 'input_tokens_details': {'cached_tokens': 10, 'cache_write_tokens': 0,
                'cache_write_tokens_details': {'ttl_12h_tokens': 0}},
            'output_tokens': 5}}}
        actual = providers.workbuddy_usage(value)
        self.assertEqual(actual['response']['output'], [{'text': 'actual response'}])
        self.assertEqual(actual['response']['usage']['input_tokens_details'], {'cached_tokens': 10, 'cache_write_tokens': 0})
        self.assertEqual(actual['response']['usage']['output_tokens'], 5)

    def test_image_helper_decodes_and_resizes_the_actual_returned_bytes(self):
        stream = io.BytesIO()
        Image.new('RGB', (1024, 1024), 'blue').save(stream, format='PNG')
        payload = {'images': [{'b64_json': base64.b64encode(stream.getvalue()).decode()}]}
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            with patch.object(providers.Path, 'cwd', return_value=root), patch.object(providers, 'request_json', return_value=payload) as request:
                output = providers.generate_image(configuration(), 'a white cat', root / 'image.png')
            with Image.open(output['local_path']) as image:
                self.assertEqual(image.size, (512, 512))
            self.assertEqual(output['original_size'], [1024, 1024])
            self.assertEqual(request.call_args.args[2]['model'], 'fixture-image')
            self.assertEqual(request.call_args.args[2]['image_size'], '1024x1024')

    def test_image_helper_does_not_call_the_provider_for_an_outside_output_path(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            with (patch.object(providers.Path, 'cwd', return_value=root), patch.object(providers, 'request_json') as request,
                  self.assertRaisesRegex(providers.ProviderConfigurationError, '工作目录')):
                providers.generate_image(configuration(), 'cat', root.parent / 'outside.png')
            request.assert_not_called()


class ModelProxyTests(unittest.TestCase):
    def setUp(self):
        self.received = []
        self.reply_error = False
        self.error_param = 'max_tokens'
        self.reply_body = b'data: {"text":"OK"}\n\ndata: [DONE]\n\n'
        self.content_type = 'text/event-stream'
        test = self
        class Handler(BaseHTTPRequestHandler):
            def log_message(self, *args):
                pass

            def do_POST(self):
                body = json.loads(self.rfile.read(int(self.headers['Content-Length'])))
                test.received.append({'authorization': self.headers.get('Authorization'), 'body': body, 'path': self.path})
                if test.reply_error:
                    raw = json.dumps({'error': {'message': 'echo fixture-private-llm-key', 'code': 'unsupported_parameter',
                                               'param': test.error_param}}).encode()
                    self.send_response(400)
                else:
                    raw = test.reply_body
                    self.send_response(200)
                self.send_header('Content-Type', 'application/json' if test.reply_error else test.content_type)
                self.send_header('Content-Length', str(len(raw)))
                self.end_headers()
                self.wfile.write(raw)
        self.server = ThreadingHTTPServer(('127.0.0.1', 0), Handler)
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        self.thread.start()
        self.config = configuration()
        self.config['llm']['base_url'] = f'http://127.0.0.1:{self.server.server_port}/v1'

    def tearDown(self):
        self.server.shutdown()
        self.server.server_close()
        self.thread.join(timeout=2)

    def request(self, proxy, token=None, model='fixture-model'):
        return urllib.request.Request(proxy.base_url + '/chat/completions',
                   data=json.dumps({'model': model, 'messages': []}).encode(),
                   headers={'Authorization': 'Bearer ' + (token or proxy.token), 'Content-Type': 'application/json'})

    def test_stream_is_forwarded_and_only_the_real_provider_receives_its_key(self):
        with providers.ModelProxy(self.config) as proxy, urllib.request.urlopen(self.request(proxy), timeout=5) as response:
            self.assertIn(b'[DONE]', response.read())
            self.assertNotEqual(proxy.token, self.config['llm']['api_key'])
        self.assertEqual(self.received[0]['authorization'], 'Bearer fixture-private-llm-key')
        self.assertEqual(self.received[0]['path'], '/v1/chat/completions')

    def test_wrong_token_or_model_is_rejected_before_any_provider_request(self):
        with providers.ModelProxy(self.config) as proxy:
            for request, status in ((self.request(proxy, token='wrong'), 401), (self.request(proxy, model='wrong-model'), 400)):
                with self.assertRaises(urllib.error.HTTPError) as raised:
                    urllib.request.urlopen(request, timeout=5)
                self.assertEqual(raised.exception.code, status)
        self.assertEqual(self.received, [])

    def test_native_export_requires_current_token_and_is_not_forwarded_to_model_provider(self):
        seen = []
        def export(body):
            seen.append(body)
            return {'status': 'ok', 'slides': 3}
        for enabled in (False, True):
            with providers.ModelProxy(self.config, ppt_export=export if enabled else None) as proxy:
                for token, expected in (('wrong', 401), (proxy.token, 200 if enabled else 404)):
                    request = urllib.request.Request(proxy.base_url + '/ci/ppt-export',
                        data=json.dumps({'deck_dir': 'fixture/deck'}).encode(),
                        headers={'Authorization': 'Bearer ' + token, 'Content-Type': 'application/json'})
                    if expected == 200:
                        with urllib.request.urlopen(request, timeout=5) as response:
                            self.assertEqual(json.load(response), {'status': 'ok', 'slides': 3})
                    else:
                        with self.assertRaises(urllib.error.HTTPError) as raised:
                            urllib.request.urlopen(request, timeout=5)
                        self.assertEqual(raised.exception.code, expected)
        self.assertEqual(seen, [{'deck_dir': 'fixture/deck'}])
        self.assertEqual(self.received, [])

    def test_responses_sse_and_json_keep_output_and_adapt_only_usage_breakdown(self):
        value = {'output': [{'text': 'actual response'}], 'usage': {'input_tokens': 10,
            'input_tokens_details': {'cached_tokens': 5, 'cache_write_tokens_details': {'ttl_12h_tokens': 0}}}}
        for stream in (False, True):
            with self.subTest(stream=stream):
                event = {'type': 'response.completed', 'response': value} if stream else value
                self.reply_body = b'data: ' + json.dumps(event).encode() + b'\n\n' if stream else json.dumps(event).encode()
                self.content_type = 'text/event-stream' if stream else 'application/json'
                with providers.ModelProxy(self.config) as proxy:
                    request = self.request(proxy)
                    request.full_url = proxy.base_url + '/responses'
                    with urllib.request.urlopen(request, timeout=5) as response:
                        raw = response.read()
                actual = json.loads(raw.removeprefix(b'data: ').strip())
                actual = actual['response'] if stream else actual
                self.assertEqual(actual['output'], value['output'])
                self.assertEqual(actual['usage']['input_tokens_details'], {'cached_tokens': 5})
                self.assertEqual(actual['usage']['input_tokens'], 10)

    def test_provider_error_body_is_not_exposed_but_safe_parameter_diagnostics_remain(self):
        self.reply_error = True
        with providers.ModelProxy(self.config) as proxy:
            with self.assertRaises(urllib.error.HTTPError) as raised:
                urllib.request.urlopen(self.request(proxy), timeout=5)
            self.assertNotIn('fixture-private-llm-key', raised.exception.read().decode())
            self.assertEqual(proxy.diagnostics[0]['param'], 'max_tokens')

    def test_provider_key_echoed_in_error_metadata_is_also_omitted(self):
        self.reply_error = True
        self.error_param = self.config['llm']['api_key']
        with providers.ModelProxy(self.config) as proxy:
            with self.assertRaises(urllib.error.HTTPError) as raised:
                urllib.request.urlopen(self.request(proxy), timeout=5)
            body = raised.exception.read().decode()
            self.assertNotIn(self.error_param, body + json.dumps(proxy.diagnostics))


if __name__ == '__main__':
    unittest.main()

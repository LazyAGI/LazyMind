import importlib.util
import io
import json
from pathlib import Path
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('client', Path(__file__).parents[1]/'skill/scripts/tender_client.py')
client = importlib.util.module_from_spec(spec)
spec.loader.exec_module(client)


class ClientTests(unittest.TestCase):
    def test_missing_key(self):
        with patch.dict('os.environ', {}, clear=True):
            self.assertEqual(client.request('search', {})['error'], 'missing_api_key')

    def invoke(self, body):
        with patch.object(client.urllib.request, 'build_opener') as build:
            build.return_value.open.return_value = io.BytesIO(json.dumps(body).encode())
            result = client.request('files', {'projectId': 123, 'publishTime': '2026-09-15'}, 'test-key')
            req = build.return_value.open.call_args.args[0]
            self.assertEqual(req.method, 'POST')
            self.assertEqual(json.loads(req.data)['projectId'], 123)
            return result

    def test_business_failure_is_failure(self):
        self.assertFalse(self.invoke({'code':403, 'msg':'invalid key'})['ok'])

    def test_empty_files_are_success(self):
        self.assertTrue(self.invoke({'code':200, 'data':[]})['ok'])

    def test_redaction(self):
        self.assertNotIn('test-key', json.dumps(self.invoke({'code':403,'msg':'test-key'})))

    def test_redirect_not_followed(self):
        self.assertIsNone(client.NoRedirect().redirect_request(None,None,302,'',{},'https://example.com'))


if __name__ == '__main__':
    unittest.main()

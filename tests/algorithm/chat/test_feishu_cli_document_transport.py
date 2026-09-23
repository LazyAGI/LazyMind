"""Contract tests through the real Feishu FS and product credential injection."""

import json
import unittest
from concurrent.futures import ThreadPoolExecutor
from threading import Barrier
from unittest.mock import patch
from uuid import uuid4
from urllib.parse import urlsplit

import lazyllm
import requests
from lazyllm.common.globals import new_session
from lazyllm.tools.fs import FeishuFS, FSReadLimitError, fs_read_limits

from lazymind.chat.engine.tool_auth import inject_tool_config
from lazymind.config import config
from lazymind.document_tools.reading import DocumentReadRequest, read_document


def response(data, status=200):
    result = requests.Response()
    result.status_code = status
    result._content = json.dumps(data).encode()
    result._content_consumed = True
    return result


class FeishuDocumentTransportTest(unittest.TestCase):
    def setUp(self):
        for key, value in {
            'core_api_url': 'https://core.fixture.invalid',
            'core_internal_token': 'fixture-internal-token',
        }.items():
            old = config[key]
            config[key] = value
            self.addCleanup(config.__setitem__, key, old)
        self.session = new_session('fixture-feishu-' + uuid4().hex)
        self.session.__enter__()
        self.addCleanup(self.session.__exit__, None, None, None)
        lazyllm.globals.config['dynamic_fs_auth'] = {}
        self.fs = FeishuFS(dynamic_auth=True, skip_instance_cache=True)
        self.addCleanup(self.fs.close)

    def assert_cli_request(self, method, url, kwargs, handle):
        self.assertEqual(method.upper(), 'POST')
        self.assertEqual(url, 'https://core.fixture.invalid/v1/internal/provider-connections/feishu-cli:execute')
        self.assertEqual(kwargs['headers']['X-LazyMind-Internal-Token'], 'fixture-internal-token')
        self.assertNotIn('Authorization', kwargs['headers'])
        self.assertFalse(kwargs.get('allow_redirects', True))
        timeout = kwargs.get('timeout', 0)
        for value in timeout if isinstance(timeout, tuple) else (timeout,):
            self.assertGreater(value, 0)
        envelope = kwargs['json']
        self.assertEqual(envelope['handle'], handle)
        self.assertEqual(envelope['operation'], 'document_request')
        self.assertEqual(envelope['identity'], 'user')
        return envelope['params']

    def test_cli_create_and_edit_reuse_existing_feishu_fs(self):
        handle = 'lmc_fcli_fixture_owner'
        inject_tool_config({'feishu': handle})
        calls = []

        def send(_session, method, url, **kwargs):
            params = self.assert_cli_request(method, url, kwargs, handle)
            calls.append(params)
            if params['method'] == 'POST':
                self.assertEqual(params['path'], '/open-apis/docx/v1/documents')
                self.assertEqual(json.loads(params['body']), {'title': 'Fixture title'})
                return response({'data': {'document': {'document_id': 'doc_fixture', 'title': 'Fixture title'}}})
            self.assertEqual(params['method'], 'PATCH')
            self.assertEqual(params['path'], '/open-apis/docx/v1/documents/doc_fixture/blocks/batch_update')
            self.assertEqual(json.loads(params['query']), {'document_revision_id': 3})
            self.assertEqual(json.loads(params['body']), {'requests': [{'block_id': 'block_fixture'}]})
            return response({'data': {'document_revision_id': 4}})

        with patch.object(requests.Session, 'request', autospec=True, side_effect=send):
            created = self.fs.create_document('Fixture title')
            edited = self.fs.update_block(created['document_id'], [{'block_id': 'block_fixture'}], document_revision_id=3)
        self.assertEqual(created['document_id'], 'doc_fixture')
        self.assertEqual(edited['document_revision_id'], 4)
        self.assertEqual(len(calls), 2)

    def test_drive_and_wiki_round_trip_through_real_document_reader(self):
        for container in ('drive', 'wiki'):
            for credential in ('fixture-original-oauth-token', 'lmc_fcli_fixture_roundtrip'):
                with self.subTest(container=container, credential=credential):
                    inject_tool_config({'feishu': credential})
                    fs = FeishuFS(dynamic_auth=True, skip_instance_cache=True,
                                  space_id='space_fixture' if container == 'wiki' else None)
                    self.addCleanup(fs.close)
                    state = {'title': 'Fixture title', 'text': 'Initial body', 'revision': 3}
                    seen = []

                    def send(_session, method, url, **kwargs):
                        if credential.startswith('lmc_fcli_'):
                            params = self.assert_cli_request(method, url, kwargs, credential)
                            verb, path = params['method'], params['path']
                            body = json.loads(params.get('body') or '{}')
                        else:
                            self.assertEqual(kwargs['headers']['Authorization'], 'Bearer ' + credential)
                            self.assertNotIn('X-LazyMind-Internal-Token', kwargs['headers'])
                            verb, path, body = method.upper(), urlsplit(url).path, kwargs.get('json') or {}
                        seen.append((verb, path))
                        node = {'obj_token': 'doc_fixture', 'node_token': 'node_fixture', 'obj_type': 'docx',
                                'space_id': 'space_fixture', 'title': 'Fixture title', 'has_child': False}
                        document = {'document_id': 'doc_fixture', 'title': state['title'],
                                    'revision_id': state['revision']}
                        if verb == 'POST' and path == '/open-apis/docx/v1/documents':
                            self.assertEqual(body, {'title': 'Fixture title'})
                            return response({'data': {'document': document}})
                        if verb == 'POST' and path == '/open-apis/wiki/v2/spaces/space_fixture/nodes':
                            self.assertEqual(body, {'obj_type': 'docx', 'node_type': 'origin', 'title': 'Fixture title'})
                            return response({'data': {'node': node}})
                        if verb == 'PATCH' and path.endswith('/blocks/batch_update'):
                            state['text'] = body['requests'][0]['update_text_elements']['elements'][0]['text_run']['content']
                            state['revision'] += 1
                            return response({'data': {'document_revision_id': state['revision']}})
                        if verb == 'PATCH' and path.endswith('/blocks/doc_fixture'):
                            state['title'] = body['update_text_elements']['elements'][0]['text_run']['content']
                            state['revision'] += 1
                            return response({'data': {'document_revision_id': state['revision']}})
                        if verb == 'GET' and path == '/open-apis/wiki/v2/spaces/get_node':
                            return response({'data': {'node': node}})
                        if verb == 'GET' and path == '/open-apis/docx/v1/documents/doc_fixture':
                            return response({'data': {'document': document}})
                        if verb == 'GET' and path.endswith('/blocks/doc_fixture/children'):
                            return response({'data': {'items': [{'block_id': 'block_fixture', 'block_type': 2,
                                'parent_id': 'doc_fixture', 'text': {'elements': [
                                    {'text_run': {'content': state['text']}}]}}], 'has_more': False}})
                        self.fail('unexpected request: ' + verb + ' ' + path)

                    with patch.object(requests.Session, 'request', autospec=True, side_effect=send):
                        created = fs.create_document('Fixture title')
                        fs.update_block(created['document_id'], [{'block_id': 'block_fixture',
                            'update_text_elements': {'elements': [{'text_run': {'content': 'Edited fixture body'}}]}}],
                            document_revision_id=3)
                        fs.update_document_title(created['document_id'], 'Renamed fixture', document_revision_id=4)
                        result = read_document(DocumentReadRequest(
                            user_id='fixture-owner', tenant_id='', source_id='fixture-connection', provider='feishu',
                            locator=created['browser_url'], tool_config={'feishu': credential}))
                    self.assertEqual(result.title, 'Renamed fixture')
                    self.assertIn('Edited fixture body', result.content)
                    self.assertNotIn('Initial body', result.content)
                    self.assertTrue(any(verb == 'GET' for verb, _ in seen))
                    if container == 'wiki':
                        self.assertIn(('GET', '/open-apis/wiki/v2/spaces/get_node'), seen)

    def test_remote_write_failure_classes_do_not_leak_or_switch_accounts(self):
        handles = ['lmc_fcli_fixture_first', 'lmc_fcli_fixture_second']
        for status, code, error_type in (
            (401, 'AUTH_TOKEN_EXPIRED', PermissionError),
            (403, 'AUTH_SCOPE_MISSING', PermissionError),
            (422, 'CLI_EXECUTION_FAILED', requests.HTTPError),
            (504, 'CLI_WRITE_OUTCOME_UNKNOWN', requests.Timeout),
        ):
            with self.subTest(code=code):
                inject_tool_config({'feishu': handles})

                def send(_session, method, url, **kwargs):
                    handle = kwargs.get('json', {}).get('handle')
                    self.assertIn(handle, handles)
                    self.assert_cli_request(method, url, kwargs, handle)
                    return response({'code': code, 'message': 'fixture-private-document-body'}, status)

                with patch.object(requests.Session, 'request', autospec=True, side_effect=send) as send_mock:
                    with self.assertRaises(error_type) as caught:
                        self.fs.create_document('Fixture error')
                self.assertEqual(getattr(caught.exception, 'code', None), code)
                self.assertNotIn('fixture-private-document-body', str(caught.exception))
                self.assertEqual(send_mock.call_count, 1)

    def test_old_oauth_stays_direct_even_after_cli_credentials_were_injected(self):
        inject_tool_config({'feishu': 'lmc_fcli_previous_fixture'})
        inject_tool_config({'feishu': 'fixture-original-oauth-token'})

        def send(_session, method, url, **kwargs):
            self.assertEqual(method, 'POST')
            self.assertEqual(url, 'https://open.feishu.cn/open-apis/docx/v1/documents')
            self.assertEqual(kwargs['headers']['Authorization'], 'Bearer fixture-original-oauth-token')
            self.assertNotIn('X-LazyMind-Internal-Token', kwargs['headers'])
            return response({'data': {'document': {'document_id': 'doc_legacy'}}})

        with patch.object(requests.Session, 'request', autospec=True, side_effect=send) as send_mock:
            self.assertEqual(self.fs.create_document('Legacy')['document_id'], 'doc_legacy')
        self.assertEqual(send_mock.call_count, 1)

    def test_cli_write_timeout_does_not_replay_or_switch_account(self):
        handles = ['lmc_fcli_fixture_first', 'lmc_fcli_fixture_second']
        inject_tool_config({'feishu': handles})

        def send(_session, method, url, **kwargs):
            handle = kwargs.get('json', {}).get('handle')
            self.assertIn(handle, handles)
            self.assert_cli_request(method, url, kwargs, handle)
            raise requests.Timeout('fixture write result unknown')

        with patch.object(requests.Session, 'request', autospec=True, side_effect=send) as send_mock:
            with self.assertRaises(requests.Timeout):
                self.fs.create_document('Fixture timeout')
        self.assertEqual(send_mock.call_count, 1)

    def test_shared_fs_uses_each_concurrent_request_handle(self):
        barrier = Barrier(2)
        handles = ['lmc_fcli_fixture_a', 'lmc_fcli_fixture_b']

        def send(_session, method, url, **kwargs):
            handle = kwargs.get('json', {}).get('handle')
            self.assertIn(handle, handles)
            self.assert_cli_request(method, url, kwargs, handle)
            barrier.wait(timeout=5)
            return response({'data': {'document': {'document_id': handle}}})

        def create(handle):
            with new_session('fixture-' + handle):
                inject_tool_config({'feishu': handle})
                return self.fs.create_document('Fixture')['document_id']

        with patch.object(requests.Session, 'request', autospec=True, side_effect=send):
            with ThreadPoolExecutor(max_workers=2) as executor:
                results = list(executor.map(create, handles))
        self.assertEqual(results, handles)

    def test_cli_transport_preserves_framework_transfer_limits(self):
        handle = 'lmc_fcli_fixture_bounded'
        inject_tool_config({'feishu': handle})

        def send(_session, method, url, **kwargs):
            self.assert_cli_request(method, url, kwargs, handle)
            return response({'data': {'document_revision_id': 4, 'content': 'x' * 1024}})

        with patch.object(requests.Session, 'request', autospec=True, side_effect=send) as send_mock:
            with fs_read_limits(max_bytes=64):
                with self.assertRaises(FSReadLimitError):
                    self.fs.update_document_title('doc_fixture', 'Fixture')
        self.assertEqual(send_mock.call_count, 1)


if __name__ == '__main__':
    unittest.main()

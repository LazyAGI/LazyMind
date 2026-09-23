import multiprocessing
import os
import tempfile
import time
import unittest
from concurrent.futures import ThreadPoolExecutor
from unittest.mock import patch
from urllib.parse import parse_qs, urlsplit

from sqlalchemy import create_engine
from sqlalchemy.orm import sessionmaker


def _process_token(database_url, args, counter, queue):
    from services.mcp_oauth import MCPOAuthService
    engine = create_engine(database_url, connect_args={
                           'check_same_thread': False} if database_url.startswith('sqlite') else {})

    def request(url, data=None, json_body=None):
        with counter.get_lock():
            counter.value += 1
        time.sleep(.2)
        return {'access_token': 'process-token', 'refresh_token': 'rotated', 'token_type': 'Bearer', 'expires_in': 20}
    service = MCPOAuthService(sessionmaker(bind=engine), request=request)
    try:
        queue.put(service.token(**args))
    except Exception as exc:  # noqa: BLE001 - marshal worker failures back to parent
        queue.put({'error': type(exc).__name__})
    finally:
        engine.dispose()


class MCPOAuthTests(unittest.TestCase):
    def setUp(self):
        environment = patch.dict(os.environ, {
            'LAZYMIND_AUTH_CLOUD_SECRET_KEY': 'test-key',
            'LAZYMIND_MCP_OAUTH_PUBLIC_BASE_URL': 'http://localhost:5173',
            'LAZYMIND_AUTH_OPENAPI_EXPORT_ENABLED': '0',
        })
        environment.start()
        self.addCleanup(environment.stop)
        from models import Base
        from services.mcp_oauth import MCPOAuthService
        self.tmp = tempfile.TemporaryDirectory()
        database_url = os.getenv('MCP_OAUTH_TEST_DATABASE_URL') or 'sqlite:///' + self.tmp.name + '/auth.db'
        self.engine = create_engine(database_url, connect_args={
                                    'check_same_thread': False} if database_url.startswith('sqlite') else {})
        Base.metadata.create_all(self.engine)
        self.sessions = sessionmaker(bind=self.engine, expire_on_commit=False)
        self.refreshes = 0
        self.hook = None

        def request(url, data=None, json_body=None):
            if 'oauth-protected-resource' in url:
                return {'resource': 'https://mcp.example/mcp', 'authorization_servers': ['https://auth.example']}
            if 'well-known' in url:
                return {'issuer': 'https://auth.example',
                        'authorization_endpoint': 'https://auth.example/authorize',
                        'token_endpoint': 'https://auth.example/token',
                        'registration_endpoint': 'https://auth.example/register',
                        'code_challenge_methods_supported': ['S256']}
            if url.endswith('/register'):
                return {'client_id': 'client', 'token_endpoint_auth_method': 'none'}
            if data.get('grant_type') == 'refresh_token':
                self.refreshes += 1
                time.sleep(.1)
                if self.hook:
                    self.hook()
            return {'access_token': 'token-' + str(self.refreshes), 'refresh_token': 'refresh',
                    'token_type': 'Bearer', 'expires_in': 3600}
        self.service = MCPOAuthService(self.sessions, request=request)
        self.identity = {'user_id': 'alice', 'server_id': 'server', 'server_url': 'https://mcp.example/mcp'}

    def tearDown(self):
        from models.mcp_oauth import MCPOAuthGrant, MCPOAuthState
        from sqlalchemy import delete
        with self.sessions() as db:
            db.execute(delete(MCPOAuthState))
            db.execute(delete(MCPOAuthGrant))
            db.commit()
        self.engine.dispose()
        self.tmp.cleanup()

    def authorize(self):
        result = self.service.authorize(**self.identity)
        query = parse_qs(urlsplit(result['authorization_url']).query)
        self.assertEqual(query['code_challenge_method'], ['S256'])
        self.assertEqual(query['resource'], [self.identity['server_url']])
        self.state = query['state'][0]
        return self.service.callback(**self.identity, code='code', state=self.state)

    def test_replay_and_owner_isolation(self):
        from services.mcp_oauth import OAuthError
        grant = self.authorize()
        with self.assertRaises(OAuthError):
            self.service.callback(**self.identity, code='code', state=self.state)
        with self.assertRaises(OAuthError):
            self.service.token(**dict(self.identity, user_id='bob'),
                               grant_id=grant['grant_id'], grant_version=grant['grant_version'])
        self.assertEqual(self.service.status(**dict(self.identity, user_id='bob'))['status'], 'needs_authorization')

    def test_parallel_refresh_one_exchange_and_stale_grants(self):
        from services.mcp_oauth import OAuthError
        grant = self.authorize()
        args = dict(self.identity, grant_id=grant['grant_id'],
                    grant_version=grant['grant_version'], rejected_token_version=1)
        with ThreadPoolExecutor(max_workers=4) as pool:
            results = list(pool.map(lambda _: self.service.token(**args), range(4)))
        self.assertEqual(self.refreshes, 1)
        self.assertEqual({r['token_version'] for r in results}, {2})
        self.service.disconnect(**self.identity)
        with self.assertRaises(OAuthError):
            self.service.token(**args)
        self.authorize()
        with self.assertRaises(OAuthError):
            self.service.token(**args)

    def test_mcp_errors_use_central_catalog(self):
        from core.errors import ErrorCodes
        expected = {
            'MCP_OAUTH_AUTHORIZATION_REQUIRED': (401, 1001101),
            'MCP_OAUTH_INVALID_REQUEST': (400, 1001102),
            'MCP_OAUTH_PROVIDER_FAILED': (502, 1001103),
            'MCP_OAUTH_BUSY': (503, 1001104),
            'MCP_OAUTH_CONFIGURATION_INVALID': (503, 1001105),
            'MCP_OAUTH_STORAGE_UNAVAILABLE': (503, 1001106),
        }
        for name, value in expected.items():
            self.assertEqual(getattr(ErrorCodes, name, None)[:2] if hasattr(ErrorCodes, name) else None, value)

    def test_short_lived_refresh_returns_after_one_exchange(self):
        grant = self.authorize()
        original_request = self.service.request

        def short_lived_request(url, data=None, json_body=None):
            self.assertEqual(self.refreshes, 0, 'A newly refreshed token must not trigger another refresh')
            payload = original_request(url, data=data, json_body=json_body)
            return dict(payload, expires_in=20)
        self.service.request = short_lived_request
        token = self.service.token(**self.identity, grant_id=grant['grant_id'],
                                   grant_version=grant['grant_version'], rejected_token_version=1)
        self.assertEqual(self.refreshes, 1)
        self.assertEqual(token['access_token'], 'token-1')
        self.assertEqual(token['token_version'], 2)
        self.assertGreater(token['expires_at'], time.time())

    def test_valid_short_token_without_refresh_is_preserved(self):
        from models.mcp_oauth import MCPOAuthGrant
        from services.mcp_oauth import decrypt_json, encrypt_json
        from sqlalchemy import select
        grant = self.authorize()
        with self.sessions() as db:
            row = db.scalar(select(MCPOAuthGrant))
            secret = decrypt_json(row.ciphertext)
            secret.pop('refresh_token')
            row.ciphertext = encrypt_json(secret)
            row.expires_at = time.time() + 20
            db.commit()
        token = self.service.token(**self.identity, grant_id=grant['grant_id'],
                                   grant_version=grant['grant_version'])
        self.assertEqual(token['access_token'], 'token-0')
        self.assertEqual(self.service.status(**self.identity)['status'], 'authorized')
        self.assertEqual(self.refreshes, 0)

    def test_parallel_short_refresh_reuses_new_version(self):
        grant = self.authorize()
        original_request = self.service.request

        def request(url, data=None, json_body=None):
            return dict(original_request(url, data=data, json_body=json_body), expires_in=20)

        self.service.request = request
        args = dict(self.identity, grant_id=grant['grant_id'],
                    grant_version=grant['grant_version'], rejected_token_version=1)
        with ThreadPoolExecutor(max_workers=4) as pool:
            results = list(pool.map(lambda _: self.service.token(**args), range(4)))
        self.assertEqual(self.refreshes, 1)
        self.assertEqual({r['token_version'] for r in results}, {2})
        args.pop('rejected_token_version')
        self.assertEqual(self.service.token(**args)['token_version'], 2)
        self.assertEqual(self.refreshes, 1)

    def test_parallel_expiry_refresh_reuses_short_token(self):
        from models.mcp_oauth import MCPOAuthGrant
        from sqlalchemy import update
        grant = self.authorize()
        with self.sessions() as db:
            db.execute(update(MCPOAuthGrant).values(expires_at=0))
            db.commit()
        original_request = self.service.request

        def request(url, data=None, json_body=None):
            return dict(original_request(url, data=data, json_body=json_body), expires_in=20)

        self.service.request = request
        args = dict(self.identity, grant_id=grant['grant_id'], grant_version=grant['grant_version'])
        with ThreadPoolExecutor(max_workers=4) as pool:
            results = list(pool.map(lambda _: self.service.token(**args), range(4)))
        self.assertEqual(self.refreshes, 1)
        self.assertEqual({r['token_version'] for r in results}, {2})

    def test_oidc_discovery_fallbacks(self):
        from services.mcp_oauth import OAuthError
        original_request = self.service.request
        for path, success_path, expected in [
            ('', '/.well-known/openid-configuration', [
                '/.well-known/oauth-authorization-server', '/.well-known/openid-configuration']),
            ('/tenant', '/.well-known/openid-configuration/tenant', [
                '/.well-known/oauth-authorization-server/tenant', '/.well-known/openid-configuration/tenant']),
            ('/tenant', '/tenant/.well-known/openid-configuration', [
                '/.well-known/oauth-authorization-server/tenant',
                '/.well-known/openid-configuration/tenant', '/tenant/.well-known/openid-configuration']),
        ]:
            with self.subTest(path=path, endpoint=success_path):
                calls = []
                issuer = 'https://auth.example' + path

                def request(url, data=None, json_body=None, issuer=issuer, calls=calls, success_path=success_path):
                    if 'oauth-protected-resource' in url:
                        return {'resource': self.identity['server_url'], 'authorization_servers': [issuer]}
                    calls.append(urlsplit(url).path)
                    if urlsplit(url).path != success_path:
                        raise OAuthError('not_found')
                    return dict(original_request(url), issuer=issuer)

                self.service.request = request
                metadata, _ = self.service._discover(self.identity['server_url'])
                self.assertEqual(metadata['issuer'], issuer)
                self.assertEqual(calls, expected)

    def test_expired_or_rejected_token_without_refresh_requires_authorization(self):
        from models.mcp_oauth import MCPOAuthGrant
        from services.mcp_oauth import OAuthError, decrypt_json, encrypt_json
        from sqlalchemy import select
        for rejected in (None, 1):
            with self.subTest(rejected=rejected):
                grant = self.authorize()
                with self.sessions() as db:
                    row = db.scalar(select(MCPOAuthGrant))
                    secret = decrypt_json(row.ciphertext)
                    secret.pop('refresh_token')
                    row.ciphertext = encrypt_json(secret)
                    row.expires_at = time.time() + (20 if rejected else -1)
                    db.commit()
                with self.assertRaises(OAuthError):
                    self.service.token(**self.identity, grant_id=grant['grant_id'],
                                       grant_version=grant['grant_version'], rejected_token_version=rejected)
                self.assertEqual(self.service.status(**self.identity)['status'], 'needs_authorization')

    def test_discovery_security_errors_never_fall_back(self):
        from services.mcp_oauth import OAuthError
        original_request = self.service.request
        for failure in ('issuer', 'private_endpoint', 'missing_dcr', 'transport'):
            with self.subTest(failure=failure):
                calls = []

                def request(url, data=None, json_body=None, calls=calls, failure=failure):
                    if 'oauth-protected-resource' in url:
                        return original_request(url)
                    calls.append(url)
                    if failure == 'transport':
                        raise OAuthError('invalid')
                    result = original_request(url)
                    if failure == 'issuer':
                        result['issuer'] = 'https://unrelated.example'
                    elif failure == 'private_endpoint':
                        result['token_endpoint'] = 'http://127.0.0.1/token'
                    else:
                        result.pop('registration_endpoint')
                    return result

                self.service.request = request
                with self.assertRaises(OAuthError):
                    self.service._discover(self.identity['server_url'])
                self.assertEqual(len(calls), 1)

    def test_disconnect_wins_refresh_race(self):
        from services.mcp_oauth import OAuthError
        grant = self.authorize()
        self.hook = lambda: self.service.disconnect(**self.identity)
        with self.assertRaises(OAuthError):
            self.service.token(**self.identity, grant_id=grant['grant_id'],
                               grant_version=grant['grant_version'], rejected_token_version=1)
        self.assertEqual(self.service.status(**self.identity)['status'], 'disconnected')

    def test_private_and_mixed_dns_rejected(self):
        from services.mcp_oauth import OAuthError
        from services.mcp_oauth_http import public_target
        for url in ['http://example.com', 'https://127.0.0.1', 'https://user:pass@example.com', 'https://[::1]']:
            with self.assertRaises(OAuthError):
                public_target(url)
        addresses = [(2, 1, 6, '', ('8.8.8.8', 443)), (2, 1, 6, '', ('127.0.0.1', 443))]
        with (patch('socket.getaddrinfo', return_value=addresses),
              self.assertRaises(OAuthError)):
            public_target('https://example.com')

    def test_callback_config_rejects_path_and_malformed_host(self):
        from services.mcp_oauth import OAuthError, _callback_url
        for value in ['https://[bad', 'https://example.com/path', 'http://example.com']:
            with (patch.dict(os.environ, {'LAZYMIND_MCP_OAUTH_PUBLIC_BASE_URL': value}),
                  self.assertRaises(OAuthError)):
                _callback_url()

    def test_state_expiry_url_binding_and_encryption(self):
        from models.mcp_oauth import MCPOAuthGrant, MCPOAuthState
        from services.mcp_oauth import OAuthError
        from sqlalchemy import select, update
        result = self.service.authorize(**self.identity)
        state = parse_qs(urlsplit(result['authorization_url']).query)['state'][0]
        with self.sessions() as db:
            transaction = db.scalar(select(MCPOAuthState))
            self.assertNotIn('verifier', transaction.ciphertext)
            self.assertNotEqual(transaction.state_hash, state)
            db.execute(update(MCPOAuthState).values(expires_at=0))
            db.commit()
        with self.assertRaises(OAuthError):
            self.service.callback(**self.identity, code='code', state=state)
        grant = self.authorize()
        with self.assertRaises(OAuthError):
            self.service.token(**dict(self.identity, server_url='https://other.example/mcp'),
                               grant_id=grant['grant_id'], grant_version=grant['grant_version'])
        with self.sessions() as db:
            row = db.scalar(select(MCPOAuthGrant))
            self.assertNotIn('token-0', row.ciphertext)
            self.assertNotIn('refresh', row.ciphertext)

    def test_reauthorize_wins_callback_and_refresh(self):
        from services.mcp_oauth import OAuthError
        grant = self.authorize()
        self.hook = lambda: self.service.authorize(**self.identity)
        with self.assertRaises(OAuthError):
            self.service.token(**self.identity, grant_id=grant['grant_id'],
                               grant_version=grant['grant_version'], rejected_token_version=1)
        self.assertEqual(self.service.status(**self.identity)['status'], 'pending')

    def test_multiprocess_refresh_uses_database_lease(self):
        grant = self.authorize()
        args = dict(self.identity, grant_id=grant['grant_id'],
                    grant_version=grant['grant_version'], rejected_token_version=1)
        ctx = multiprocessing.get_context('spawn')
        counter, queue = ctx.Value('i', 0), ctx.Queue()
        processes = [ctx.Process(target=_process_token, args=(self.engine.url.render_as_string(
            hide_password=False), args, counter, queue)) for _ in range(4)]
        for process in processes:
            process.start()
        results = [queue.get(timeout=15) for _ in processes]
        for process in processes:
            process.join(15)
            self.assertEqual(process.exitcode, 0)
        self.assertEqual(counter.value, 1)
        self.assertEqual({result.get('access_token') for result in results}, {'process-token'})
        self.assertEqual({result.get('token_version') for result in results}, {2})

    def test_pending_callback_is_single_use_across_parallel_calls(self):
        from services.mcp_oauth import OAuthError
        result = self.service.authorize(**self.identity)
        state = parse_qs(urlsplit(result['authorization_url']).query)['state'][0]

        def callback(_):
            try:
                return self.service.callback(**self.identity, code='code', state=state)['status']
            except OAuthError:
                return 'rejected'
        with ThreadPoolExecutor(max_workers=4) as pool:
            results = list(pool.map(callback, range(4)))
        self.assertEqual(results.count('authorized'), 1)
        self.assertEqual(results.count('rejected'), 3)

    def test_abandoned_refresh_requires_reauthorization(self):
        from models.mcp_oauth import MCPOAuthGrant
        from services.mcp_oauth import OAuthError
        from sqlalchemy import update
        grant = self.authorize()
        with self.sessions() as db:
            db.execute(update(MCPOAuthGrant).values(lease_id='crashed-worker', lease_until=0))
            db.commit()
        with self.assertRaises(OAuthError):
            self.service.token(**self.identity, grant_id=grant['grant_id'],
                               grant_version=grant['grant_version'], rejected_token_version=1)
        self.assertEqual(self.refreshes, 0)
        self.assertEqual(self.service.status(**self.identity)['status'], 'needs_authorization')

    def test_api_internal_auth_envelope_and_secret_redaction(self):
        import api.mcp_oauth as api
        from core.deps import require_internal_service_token
        from fastapi.testclient import TestClient
        from main import app
        client = TestClient(app)
        url = '/api/authservice/v1/mcp-oauth/status'
        self.assertIn(client.post(url, json=self.identity).status_code, {401, 403})
        app.dependency_overrides[require_internal_service_token] = lambda: None
        try:
            with patch.object(api, 'mcp_oauth_service', self.service):
                result = client.post(url, json=self.identity)
                self.assertEqual(result.status_code, 200)
                self.assertEqual(result.json()['data']['status'], 'needs_authorization')
                result = client.post('/api/authservice/v1/mcp-oauth/callback',
                                     json=dict(self.identity, code={'secret': 'do-not-leak'}, state='state'))
                self.assertEqual(result.status_code, 400)
                self.assertNotIn('do-not-leak', result.text)
                result = client.post('/api/authservice/v1/mcp-oauth/token',
                                     json=dict(self.identity, grant_id='missing', grant_version=1))
                self.assertEqual(result.status_code, 401)
                self.assertEqual(result.json()['code'], 1001101)
        finally:
            app.dependency_overrides.pop(require_internal_service_token, None)


if __name__ == '__main__':
    unittest.main()

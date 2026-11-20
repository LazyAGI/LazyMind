"""Real App entry points in fresh processes; no manual background execution."""
import os
import subprocess
import sys
from pathlib import Path

import pytest
from test_github_star_deferred import KEY, isolated_database_engine

AUTH_ROOT = Path(__file__).resolve().parents[3] / 'backend' / 'auth-service'
LIFECYCLE = r'''
import importlib
import importlib.util
import io
import json
import os
import sys
import threading
import time
from datetime import datetime, timedelta, timezone
from unittest.mock import patch
from urllib import request
from urllib.error import HTTPError
from uuid import UUID

from fastapi.testclient import TestClient

mode, platform, expected_text = sys.argv[1:]
expected = expected_text == 'true'
repo = 'LazyAGI/LazyMind'
root = '/api/authservice/v1/cloud/github/star'
owner = UUID('d390cf35-74db-47f1-a603-1b36b1815325')
token = 'fixture-star-token-not-a-real-credential'
start_time = datetime(2026, 11, 17, tzinfo=timezone.utc)
clock = [start_time + timedelta(hours={'before_due': 2, 'due': 6}.get(mode, 0))]
calls, threads, probes, lifecycle = [], set(), [], {'start': 0, 'shutdown': 0}
starred = [False]
offline = mode in {'seed', 'automatic'}

def forbidden_network(*args, **kwargs):
    raise AssertionError('unexpected real HTTP outside controlled GitHub boundary')

request.OpenerDirector.open = forbidden_network

def response(status, body, headers=None):
    result = io.BytesIO(json.dumps(body).encode())
    result.status = status
    result.headers = headers or {'X-OAuth-Scopes': 'repo'}
    return result

def github(req, *args, **kwargs):
    assert req.full_url.startswith('https://api.github.com/')
    assert req.get_header('Authorization') == 'Bearer ' + token
    method, path = req.get_method(), req.full_url.removeprefix('https://api.github.com')
    calls.append((method, path))
    threads.add(threading.current_thread())
    if offline and len(calls) == 1:
        if mode == 'automatic':
            # Advance only external time, not production retry intervals or methods.
            clock[0] = start_time + timedelta(hours=6)
        raise TimeoutError('fixture home network cannot reach GitHub')
    if method == 'GET' and path == '/user':
        return response(200, {'id': 10001, 'login': 'fixture-user'})
    if method == 'GET' and path == '/repos/' + repo:
        return response(200, {'full_name': repo})
    if method == 'PUT' and path == '/user/starred/' + repo:
        starred[0] = True
        return response(204, {})
    assert method == 'GET' and path == '/user/starred/' + repo
    if not starred[0]:
        raise HTTPError(req.full_url, 404, 'fixture', {}, io.BytesIO(b'{}'))
    return response(204, {})

cli_module = importlib.import_module('services.github_cli_auth')
with patch('sys.platform', platform):
    cli = importlib.reload(cli_module).github_cli_auth_service
assert cli.host_credentials_enabled is expected

def probe():
    probes.append(True)
    return {'access_token': token}

cli.runner.probe = probe
cli.start_recovery = lambda: None
cli.shutdown = lambda: None
health = importlib.import_module('services.cloud_oauth_health')
health.start = lambda: None

async def stop_health():
    return None

health.stop = stop_health
star_module = importlib.import_module('services.github_star_service')
star_module.request.urlopen = github

if mode != 'control':
    name = 'services.github_star_deferred'
    assert importlib.util.find_spec(name) is not None, 'production deferred service assembly is missing'
    service_type = importlib.import_module(name).GitHubStarDeferredService
    original_init = service_type.__init__
    original_start, original_shutdown = service_type.start, service_type.shutdown

    def initialized(self, *args, **kwargs):
        # Observe the real factory's decision; never substitute its enabled value.
        assert kwargs.get('enabled') is expected, 'production factory ignored runtime capability gate'
        kwargs['clock'] = lambda: clock[0]
        if kwargs.get('probe') is not None:
            kwargs['probe'] = probe
        original_init(self, *args, **kwargs)

    def started(self, *args, **kwargs):
        lifecycle['start'] += 1
        return original_start(self, *args, **kwargs)

    def stopped(self, *args, **kwargs):
        lifecycle['shutdown'] += 1
        return original_shutdown(self, *args, **kwargs)

    service_type.__init__, service_type.start, service_type.shutdown = initialized, started, stopped

main = importlib.import_module('main')
api = importlib.import_module('api.cloud_oauth')
from core.database import SessionLocal
from core.cloud_crypto import encrypt_json
from core.deps import current_user_id
from models import CloudAuthConnection, PermissionGroup, Role, User

main.app.dependency_overrides[current_user_id] = lambda: owner

def seed_user():
    with SessionLocal() as db:
        if db.get(User, owner) is None:
            permissions = []
            for code in ('model.read', 'model.write'):
                permission = db.query(PermissionGroup).filter_by(code=code).first()
                if permission is None:
                    permission = PermissionGroup(code=code, module='model', action=code.split('.')[1])
                    db.add(permission)
                permissions.append(permission)
            role = Role(name='lifecycle-fixture-writer', permission_groups=permissions)
            db.add(User(id=owner, username='lifecycle-owner', password_hash='fixture', role=role))
            db.commit()

def snapshot(client):
    result = client.get(root + '/deferred')
    assert result.status_code == 200, result.text
    return result.json()['data']

def wait_for(client, predicate):
    limit = time.monotonic() + 5
    while time.monotonic() < limit:
        data = snapshot(client)
        if predicate(data):
            return data
        threading.Event().wait(0.01)
    raise AssertionError('automatic worker did not produce expected state: ' + repr(snapshot(client)))

with TestClient(main.app, raise_server_exceptions=False) as client:
    seed_user()
    if mode == 'control':
        with SessionLocal() as db:
            db.add(CloudAuthConnection(
                connection_id='lifecycle-control', owner_user_id=str(owner), provider='github',
                auth_mode='oauth_user', provider_account_id='10001', display_name='fixture-user',
                credential_ciphertext=encrypt_json({'provider_options': {}}),
                auth_state_ciphertext=encrypt_json({'token_source': 'github_cli', 'access_token': token}),
            ))
            db.commit()
        result = client.get(root, params={'connection_id': 'lifecycle-control'})
        assert result.status_code == 200, result.text
        assert result.json()['data']['accounts'][0]['state'] == 'not_starred'
        assert not probes
    elif mode == 'gate':
        data = snapshot(client)
        assert data['can_receive'] is expected and data['local_supported'] is expected
        assert lifecycle['start'] == 1
        if not expected:
            result = client.post(root + '/deferred', json={'source': 'local', 'confirmed': True})
            assert result.status_code in (403, 409, 503), result.text
            assert not probes and not calls
    elif mode in {'seed', 'automatic'}:
        result = client.post(root + '/deferred', json={'source': 'local', 'confirmed': True})
        assert result.status_code == 200, result.text
        assert result.json()['data']['accepted'] is True
        if mode == 'seed':
            data = wait_for(client, lambda item: item['intent']['attempt_count'] == 1 and bool(calls))
            assert data['intent']['status'] == 'pending'
            assert data['intent']['next_attempt_at'].startswith('2026-11-17T06:00:00')
        else:
            data = wait_for(client, lambda item: item['intent']['status'] == 'succeeded')
            assert data['intent']['attempt_count'] == 2
            assert data['intent']['remote_state'] == 'starred'
            assert len([call for call in calls if call[0] == 'PUT']) == 1
        assert lifecycle['start'] == 1 and threads
    elif mode == 'before_due':
        data = snapshot(client)
        assert data['intent']['attempt_count'] == 1
        assert data['intent']['created_at'].startswith('2026-11-17T00:00:00')
        assert data['intent']['expires_at'].startswith('2026-11-24T00:00:00')
        threading.Event().wait(0.1)
        assert not probes and not calls
        assert lifecycle['start'] == 1
    else:
        assert mode == 'due'
        data = wait_for(client, lambda item: item['intent']['status'] == 'succeeded')
        assert data['intent']['attempt_count'] == 2
        assert data['intent']['remote_state'] == 'starred'
        assert lifecycle['start'] == 1 and threads

if mode != 'control':
    assert lifecycle['shutdown'] == 1, 'real App shutdown did not stop deferred service'
    assert all(not thread.is_alive() for thread in threads), 'HTTP execution thread survived App shutdown'
print('LIFECYCLE_RESULT ' + json.dumps({'mode': mode, 'lifecycle': lifecycle, 'http_count': len(calls)}))
'''


def run_app_process(engine, tmp_path, mode, platform='darwin', runtime='local', workers='1', expected=True):
    env = dict(os.environ,
               LAZYMIND_DATABASE_URL=engine.url.render_as_string(hide_password=False),
               LAZYMIND_AUTH_OPENAPI_EXPORT_ENABLED='0', LAZYMIND_AUTH_CLOUD_SECRET_KEY=KEY,
               LAZYMIND_RUNTIME_MODE=runtime, WEB_CONCURRENCY=workers,
               LAZYMIND_STATE_BACKEND='sqlite', LAZYMIND_STATE_SQLITE_DIR=str(tmp_path / 'auth-state'),
               LAZYMIND_GITHUB_CLI_CONTAINER_ENABLED='1' if platform == 'linux' else '0',
               GH_TOKEN='fixture-star-token-not-a-real-credential', GITHUB_TOKEN='')
    result = subprocess.run([sys.executable, '-c', LIFECYCLE, mode, platform, str(expected).lower()],
                            cwd=AUTH_ROOT, env=env, capture_output=True, text=True, timeout=30, check=False)
    assert result.returncode == 0, result.stdout + result.stderr
    assert 'LIFECYCLE_RESULT ' in result.stdout


def test_isolated_real_app_bootstrap_user_permissions_and_existing_star_control(tmp_path):
    with isolated_database_engine(tmp_path) as engine:
        run_app_process(engine, tmp_path, 'control')


def test_real_app_starts_continuous_worker_and_stops_actual_execution_thread(tmp_path):
    with isolated_database_engine(tmp_path) as engine:
        run_app_process(engine, tmp_path, 'automatic')


def test_fresh_process_restart_respects_future_due_time_then_automatically_restores_due_intent(tmp_path):
    with isolated_database_engine(tmp_path) as engine:
        for mode in ('seed', 'before_due', 'due'):
            run_app_process(engine, tmp_path, mode)


@pytest.mark.parametrize('platform,runtime,workers,expected', [
    ('darwin', 'local', '1', True), ('win32', 'local', '1', True),
    ('linux', 'local', '1', False), ('darwin', 'server', '1', False),
    ('darwin', 'local', '2', False),
])
def test_production_factory_uses_real_platform_runtime_and_worker_gate(tmp_path, platform, runtime, workers, expected):
    with isolated_database_engine(tmp_path) as engine:
        run_app_process(engine, tmp_path, 'gate', platform, runtime, workers, expected)

"""Notification claims share Core's service identity, including file-only deployments."""
from pathlib import Path

import httpx
import pytest
import yaml

from channel_gateway.common.errors import GatewayError
from channel_gateway.common.infrastructure.lazymind import LazyMindClient


@pytest.mark.parametrize('source', ['file', 'direct'])
def test_claim_uses_shared_internal_credential(tmp_path, monkeypatch, source):
    token = 'synthetic-shared-service-token'
    path = tmp_path / 'token'
    path.write_text('  ' + token + '\n')
    path.chmod(0o600)
    monkeypatch.setenv('LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN_FILE', str(path))
    monkeypatch.setenv('LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN', token if source == 'direct' else '')
    if source == 'direct':
        path.unlink()  # Explicit values have the same precedence as Core.
    calls = []

    def request(method, url, **kwargs):
        calls.append(kwargs['headers']['X-LazyMind-Internal-Token'])
        assert method == 'POST' and url.endswith('/notification-events/notice:claim')
        return httpx.Response(200, json={'data': {'granted': True}})

    monkeypatch.setattr(httpx, 'request', request)
    LazyMindClient('http://core.invalid', 10).claim_notification('owner', 'notice', 'outbox', False)
    assert calls == [token]


@pytest.mark.parametrize('invalid', ['missing', 'empty', 'short', 'large', 'directory', 'relative', 'public'])
def test_claim_fails_closed_for_invalid_credential_file(tmp_path, monkeypatch, invalid):
    path = tmp_path / 'token'
    path.write_text('synthetic-shared-service-token')
    path.chmod(0o600)
    if invalid == 'missing':
        path.unlink()
    elif invalid in ('empty', 'short', 'large'):
        path.write_text({'empty': '', 'short': 'short', 'large': 'x' * 4097}[invalid])
    elif invalid == 'directory':
        path = tmp_path
    elif invalid == 'relative':
        path = Path('relative-token')
    elif invalid == 'public':
        path.chmod(0o644)
    monkeypatch.setenv('LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN', '')
    monkeypatch.setenv('LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN_FILE', str(path))
    monkeypatch.setattr(httpx, 'request', lambda *a, **kw: pytest.fail('must not send unauthenticated claim'))
    with pytest.raises(GatewayError) as error:
        LazyMindClient('http://core.invalid', 10).claim_notification('owner', 'notice', 'outbox', False)
    assert error.value.code == 'NOTIFICATION_CORE_UNAVAILABLE'


def test_compose_gateway_uses_core_shared_token_volume():
    compose = yaml.safe_load((Path(__file__).resolve().parents[3] / 'docker-compose.yml').read_text())
    gateway = compose['services']['channel-gateway']
    assert gateway['environment']['LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN_FILE'] == '/run/secrets/internal-service/token'
    assert 'LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN' not in gateway['environment']
    assert 'internal-service-credentials:/run/secrets/internal-service:ro' in gateway['volumes']
    assert gateway['depends_on']['internal-service-token-init']['condition'] == 'service_completed_successfully'

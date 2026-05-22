from fastapi.testclient import TestClient


def _authorize_payload() -> dict:
    return {
        'tenant_id': 'tenant-test',
        'auth_mode': 'oauth_user',
        'client_id': 'cli_test',
        'client_secret': 'sec_test',
        'redirect_uri': 'http://localhost/callback',
    }


def test_oauth_authorize_url_requires_secret_key(client: TestClient, monkeypatch):
    monkeypatch.delenv('LAZYMIND_AUTH_CLOUD_SECRET_KEY', raising=False)

    resp = client.post('/api/authservice/v1/cloud/feishu/oauth/authorize-url', json=_authorize_payload())
    assert resp.status_code == 500
    payload = resp.json()
    assert payload['code'] == 1000708
    assert payload['message'] == 'cloud oauth encryption key is not configured'


def test_oauth_authorize_url_success_when_secret_key_present(client: TestClient, monkeypatch):
    monkeypatch.setenv('LAZYMIND_AUTH_CLOUD_SECRET_KEY', 'test-ragscan-secret')

    resp = client.post('/api/authservice/v1/cloud/feishu/oauth/authorize-url', json=_authorize_payload())
    assert resp.status_code == 200
    payload = resp.json()
    assert payload['code'] == 200
    data = payload['data']
    assert data['provider'] == 'feishu'
    assert data['auth_mode'] == 'oauth_user'
    assert data['connection_id'].startswith('conn_')
    assert 'accounts.feishu.cn' in data['authorize_url']

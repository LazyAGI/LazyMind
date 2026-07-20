import pytest
from fastapi.testclient import TestClient


@pytest.fixture(autouse=True)
def _stub_redis_dependencies(monkeypatch):
    import api.auth as auth_api
    from services.auth_service import login_rate_limiter

    store = {}

    monkeypatch.setattr(login_rate_limiter, 'is_limited', lambda user_id: False)
    monkeypatch.setattr(login_rate_limiter, 'record_failure', lambda user_id: None)
    monkeypatch.setattr(
        auth_api,
        'set_refresh_token',
        lambda token_hash, user_id: store.__setitem__(token_hash, user_id),
    )
    monkeypatch.setattr(auth_api, 'get_user_id_by_token', lambda token_hash: store.get(token_hash))
    monkeypatch.setattr(auth_api, 'delete_refresh_token', lambda token_hash: store.pop(token_hash, None))


def _data(response):
    return response.json()['data']


def _auth_headers(client: TestClient, username: str = 'clouduser') -> dict[str, str]:
    client.post('/api/authservice/auth/register', json={
        'username': username,
        'password': 'Aa1!aaaa',
        'confirm_password': 'Aa1!aaaa',
    })
    login = client.post('/api/authservice/auth/login', json={'username': username, 'password': 'Aa1!aaaa'})
    token = _data(login)['access_token']
    return {'Authorization': f'Bearer {token}'}


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

    resp = client.post(
        '/api/authservice/v1/cloud/feishu/oauth/authorize-url',
        json=_authorize_payload(),
        headers=_auth_headers(client, 'cloudnosecret'),
    )
    assert resp.status_code == 500
    payload = resp.json()
    assert payload['code'] == 1000708
    assert payload['message'] == 'cloud oauth encryption key is not configured'


def test_oauth_authorize_url_success_when_secret_key_present(client: TestClient, monkeypatch):
    monkeypatch.setenv('LAZYMIND_AUTH_CLOUD_SECRET_KEY', 'test-ragscan-secret')

    headers = _auth_headers(client, 'cloudok')
    resp = client.post(
        '/api/authservice/v1/cloud/feishu/oauth/authorize-url',
        json=_authorize_payload(),
        headers=headers,
    )
    assert resp.status_code == 200
    payload = resp.json()
    assert payload['code'] == 200
    data = payload['data']
    assert data['provider'] == 'feishu'
    assert data['auth_mode'] == 'oauth_user'
    assert data['tenant_id'] == ''
    assert data['owner_user_id']
    assert data['connection_id'].startswith('conn_')
    assert 'accounts.feishu.cn' in data['authorize_url']

    listed = client.get('/api/authservice/v1/cloud/connections?provider=feishu', headers=headers)
    assert listed.status_code == 200
    items = _data(listed)['items']
    assert items == []

    pending = client.get('/api/authservice/v1/cloud/connections?provider=feishu&status=PENDING', headers=headers)
    assert pending.status_code == 200
    pending_items = _data(pending)['items']
    assert len(pending_items) == 1
    assert pending_items[0]['connection_id'] == data['connection_id']

    status = client.post(
        '/api/authservice/v1/cloud/connections/status:batch',
        json={'connection_ids': [data['connection_id'], data['connection_id']]},
        headers={'X-LazyMind-Internal-Token': 'test-internal-token'},
    )
    assert status.status_code == 200
    status_items = _data(status)['items']
    assert len(status_items) == 1
    assert status_items[0]['connection_id'] == data['connection_id']
    assert status_items[0]['status'] == 'PENDING'


def test_google_drive_oauth_provider_is_registered(client: TestClient, monkeypatch):
    monkeypatch.setenv('LAZYMIND_AUTH_CLOUD_SECRET_KEY', 'test-ragscan-secret')

    response = client.post(
        '/api/authservice/v1/cloud/googledrive/oauth/authorize-url',
        json=_authorize_payload(),
        headers=_auth_headers(client, 'googledriveuser'),
    )

    assert response.status_code == 200
    data = _data(response)
    assert data['provider'] == 'googledrive'
    assert data['scope'] == 'https://www.googleapis.com/auth/drive.readonly'
    assert 'accounts.google.com/o/oauth2/v2/auth' in data['authorize_url']
    assert 'access_type=offline' in data['authorize_url']

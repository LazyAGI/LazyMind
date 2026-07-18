from urllib.parse import parse_qs, urlparse

from services.providers import google_drive_oauth_provider as provider_module
from services.providers.google_drive_oauth_provider import GoogleDriveOAuthProvider


class _Response:
    def __init__(self, body: bytes):
        self._body = body

    def __enter__(self):
        return self

    def __exit__(self, *_args):
        return False

    def read(self):
        return self._body


def test_authorize_url_requests_offline_readonly_access():
    provider = GoogleDriveOAuthProvider()
    url = provider.build_authorize_url(
        client_id='client-id',
        redirect_uri='http://localhost/oauth/googledrive/callback',
        scope='',
        state='state-1',
    )
    parsed = urlparse(url)
    query = parse_qs(parsed.query)

    assert parsed.netloc == 'accounts.google.com'
    assert query['scope'] == ['https://www.googleapis.com/auth/drive.readonly']
    assert query['access_type'] == ['offline']
    assert query['include_granted_scopes'] == ['true']
    assert query['prompt'] == ['consent']
    assert query['state'] == ['state-1']


def test_exchange_and_refresh_tokens(monkeypatch):
    calls = []

    def post_form(payload):
        calls.append(payload)
        if payload['grant_type'] == 'authorization_code':
            return {
                'access_token': 'access-1',
                'expires_in': 3600,
                'refresh_token': 'refresh-1',
                'token_type': 'Bearer',
            }
        return {
            'access_token': 'access-2',
            'expires_in': 3600,
            'token_type': 'Bearer',
        }

    monkeypatch.setattr(provider_module, '_post_form', post_form)
    provider = GoogleDriveOAuthProvider()
    exchanged = provider.exchange_code(
        client_id='client-id',
        client_secret='client-secret',
        code='code-1',
        redirect_uri='http://localhost/callback',
    )
    refreshed = provider.refresh_access_token(
        client_id='client-id',
        client_secret='client-secret',
        refresh_token='refresh-1',
    )

    assert exchanged.access_token == 'access-1'
    assert exchanged.refresh_token == 'refresh-1'
    assert refreshed.access_token == 'access-2'
    assert refreshed.refresh_token == 'refresh-1'
    assert calls[0]['grant_type'] == 'authorization_code'
    assert calls[1]['grant_type'] == 'refresh_token'


def test_refresh_uses_rotated_refresh_token_when_returned(monkeypatch):
    monkeypatch.setattr(provider_module, '_post_form', lambda _payload: {
        'access_token': 'access-2',
        'expires_in': 3600,
        'refresh_token': 'refresh-2',
        'token_type': 'Bearer',
    })

    refreshed = GoogleDriveOAuthProvider().refresh_access_token(
        client_id='client-id',
        client_secret='client-secret',
        refresh_token='refresh-1',
    )

    assert refreshed.refresh_token == 'refresh-2'


def test_fetch_account_profile_uses_drive_about(monkeypatch):
    monkeypatch.setattr(
        provider_module,
        '_get_json',
        lambda url, access_token: {
            'user': {
                'permissionId': 'permission-1',
                'displayName': 'Drive User',
                'emailAddress': 'drive@example.com',
                'photoLink': 'https://example.com/avatar.png',
            }
        },
    )

    profile = GoogleDriveOAuthProvider().fetch_account_profile(access_token='access-token')

    assert profile.provider_account_id == 'permission-1'
    assert profile.display_name == 'Drive User'
    assert profile.meta['email'] == 'drive@example.com'


def test_tenant_mode_is_rejected():
    provider = GoogleDriveOAuthProvider()
    try:
        provider.acquire_tenant_access_token(client_id='client-id', client_secret='client-secret')
    except RuntimeError as exc:
        assert 'oauth_user' in str(exc)
    else:
        raise AssertionError('expected tenant mode to be rejected')


def test_post_form_reports_invalid_json(monkeypatch):
    monkeypatch.setattr(provider_module.request, 'urlopen', lambda *_args, **_kwargs: _Response(b'not-json'))

    try:
        provider_module._post_form({'grant_type': 'authorization_code'})
    except RuntimeError as exc:
        assert 'invalid json' in str(exc)
    else:
        raise AssertionError('expected invalid JSON to raise RuntimeError')


def test_get_json_reports_invalid_json(monkeypatch):
    monkeypatch.setattr(provider_module.request, 'urlopen', lambda *_args, **_kwargs: _Response(b'not-json'))

    try:
        provider_module._get_json('https://example.com', 'token')
    except RuntimeError as exc:
        assert 'invalid json' in str(exc)
    else:
        raise AssertionError('expected invalid JSON to raise RuntimeError')

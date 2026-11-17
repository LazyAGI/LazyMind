import ssl

import pytest
import requests

from lazymind.common.mineru_transport import official_api_request


def mismatch():
    error = ssl.SSLCertVerificationError(1, 'hostname mismatch')
    error.verify_code = 62
    return requests.exceptions.SSLError(error)


@pytest.mark.parametrize('host', ['mineru.net', 'mineru.org.cn'])
def test_official_api_keeps_domain_and_payload(host):
    calls = []
    url = f'https://{host}/api/v4/file-urls/batch'

    def send(method, target, **kwargs):
        calls.append(target)
        assert method == 'POST'
        assert kwargs['verify'] is False
        assert kwargs['allow_redirects'] is False
        assert kwargs['json'] == {'files': []}
        assert kwargs['headers'] == {'Authorization': 'Bearer test'}
        return 'ok'

    assert official_api_request(send, 'POST', url, json={'files': []},
                                headers={'Authorization': 'Bearer test'}) == 'ok'
    assert calls == [url]


@pytest.mark.parametrize('url', [
    'https://mineru.net/artifact',
    'https://mineru.org.cn/artifact',
    'https://example.com/api/v4/file-urls/batch',
    'https://mineru.net.evil.example/api/v4/file-urls/batch',
    'https://mineru.oss-cn-shanghai.aliyuncs.com/signed',
    'http://mineru.net/api/v4/file-urls/batch',
    'https://mineru.net:8443/api/v4/file-urls/batch',
])
def test_unrelated_origins_are_never_rewritten(url):
    calls = []

    def send(method, target, **kwargs):
        calls.append(target)
        assert kwargs['verify'] is True
        raise mismatch()

    with pytest.raises(requests.exceptions.SSLError):
        official_api_request(send, 'GET', url, verify=True)
    assert calls == [url]


@pytest.mark.parametrize('error', [
    requests.Timeout('timeout'), requests.ConnectionError('reset'), requests.exceptions.SSLError('unknown CA'), mismatch(),
])
def test_other_failures_never_resubmit(error):
    calls = []

    def send(method, url, **kwargs):
        calls.append(url)
        raise error

    with pytest.raises(type(error)):
        official_api_request(send, 'POST', 'https://mineru.net/api/v4/file-urls/batch')
    assert len(calls) == 1


def test_official_api_compatibility_overrides_verification():
    def send(method, url, **kwargs):
        assert kwargs['verify'] is False
        return 'ok'
    assert official_api_request(send, 'GET', 'https://mineru.net/api/v4/x', verify=False) == 'ok'

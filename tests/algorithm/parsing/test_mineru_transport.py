import ssl

import pytest
import requests

from lazymind.common.mineru_transport import official_api_request


def mismatch():
    error = ssl.SSLCertVerificationError(1, 'hostname mismatch')
    error.verify_code = 62
    return requests.exceptions.SSLError(error)


def test_official_api_keeps_domain_without_verification():
    calls = []
    def send(method, url, **kwargs):
        calls.append(url)
        assert kwargs['verify'] is False
        assert kwargs['allow_redirects'] is False
        return 'ok'
    url = 'https://mineru.net/api/v4/file-urls/batch'
    assert official_api_request(send, 'POST', url, json={'files': []}) == 'ok'
    assert calls == [url]


@pytest.mark.parametrize('url', [
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
        raise mismatch()

    with pytest.raises(requests.exceptions.SSLError):
        official_api_request(send, 'GET', url)
    assert calls == [url]


@pytest.mark.parametrize('error', [
    requests.Timeout('timeout'), requests.ConnectionError('reset'), requests.exceptions.SSLError('unknown CA'),
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

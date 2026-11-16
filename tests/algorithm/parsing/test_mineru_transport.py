import ssl

import pytest
import requests

from lazymind.common.mineru_transport import official_api_request


def mismatch():
    error = ssl.SSLCertVerificationError(1, 'hostname mismatch')
    error.verify_code = 62
    return requests.exceptions.SSLError(error)


def test_alias_retry_preserves_payload_and_certificate_verification():
    calls = []

    def send(method, url, **kwargs):
        calls.append((method, url, kwargs))
        if len(calls) == 1:
            raise mismatch()
        return 'ok'

    assert official_api_request(send, 'POST', 'https://mineru.net/api/v4/file-urls/batch',
                                json={'files': []}, headers={'Authorization': 'Bearer test'}) == 'ok'
    assert [c[1] for c in calls] == [
        'https://mineru.net/api/v4/file-urls/batch', 'https://mineru.org.cn/api/v4/file-urls/batch']
    assert all(c[2]['verify'] is True and c[2]['allow_redirects'] is False for c in calls)
    assert calls[0][2] == calls[1][2]


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


def test_official_api_cannot_inherit_disabled_verification():
    def send(method, url, **kwargs):
        assert kwargs['verify'] is True
        return 'ok'
    assert official_api_request(send, 'GET', 'https://mineru.net/api/v4/x', verify=False) == 'ok'

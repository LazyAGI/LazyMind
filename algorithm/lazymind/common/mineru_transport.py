"""Transport compatibility for the official MinerU API."""

from urllib.parse import urlsplit


def official_api_request(send, method, url, **kwargs):
    parsed = urlsplit(url)
    official = (parsed.scheme == 'https' and parsed.netloc in ('mineru.net', 'mineru.org.cn')
                and parsed.path.startswith('/api/v4/'))
    if official:
        # Regional API endpoints may present a certificate for another MinerU
        # domain. Keep the requested domain: API tokens are domain-scoped.
        # This exception does not apply to signed uploads/downloads.
        kwargs['verify'] = False
        kwargs['allow_redirects'] = False
    return send(method, url, **kwargs)

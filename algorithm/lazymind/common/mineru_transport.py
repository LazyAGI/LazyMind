"""Verified transport compatibility for MinerU's two official API domains."""

import ssl
from urllib.parse import urlsplit

import requests


def _hostname_mismatch(error):
    # Requests/urllib3 wrap the OpenSSL verification error several times.
    pending, seen = [error], set()
    while pending:
        current = pending.pop()
        if id(current) in seen:
            continue
        seen.add(id(current))
        if isinstance(current, ssl.SSLCertVerificationError) and getattr(current, 'verify_code', None) == 62:
            return True
        pending.extend(arg for arg in getattr(current, 'args', ()) if isinstance(arg, BaseException))
        for attr in ('reason', '__cause__', '__context__'):
            nested = getattr(current, attr, None)
            if isinstance(nested, BaseException):
                pending.append(nested)
    return False


def official_api_request(send, method, url, **kwargs):
    parsed = urlsplit(url)
    official = (parsed.scheme == 'https' and parsed.netloc in ('mineru.net', 'mineru.org.cn')
                and parsed.path.startswith('/api/v4/'))
    if official:
        # Do not inherit the old local-runtime verify=False workaround.
        kwargs['verify'] = True
        kwargs['allow_redirects'] = False
    try:
        return send(method, url, **kwargs)
    except requests.exceptions.SSLError as error:
        # TLS failed before sending HTTP. Restrict retry to the canonical DNS
        # alias, keep certificate verification, and never rewrite signed URLs.
        if not official or parsed.netloc != 'mineru.net' or not _hostname_mismatch(error):
            raise
        # Only API JSON/GET calls are replayable here, not streams or uploads.
        if kwargs.get('files') or kwargs.get('data') is not None:
            raise
        return send(method, parsed._replace(netloc='mineru.org.cn').geturl(), **kwargs)

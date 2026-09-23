"""Small HTTPS-only OAuth transport with DNS pinning and no redirects/proxies."""
import http.client
import ipaddress
import json
import socket
import ssl
from urllib.parse import urlencode, urlsplit


class OAuthError(Exception):
    def __init__(self, kind='upstream'):
        self.kind = kind
        super().__init__('MCP OAuth ' + kind)


def public_target(url):
    try:
        parsed = urlsplit(url)
        if (parsed.scheme != 'https' or not parsed.hostname or parsed.username or parsed.password
                or parsed.fragment or any(c.isspace() for c in url)):
            raise ValueError()
        addresses = socket.getaddrinfo(parsed.hostname, parsed.port or 443, type=socket.SOCK_STREAM)
        if not addresses or any(not ipaddress.ip_address(a[4][0]).is_global for a in addresses):
            raise ValueError()
        return parsed, addresses[0]
    except (ValueError, OSError):
        raise OAuthError('invalid') from None


class _PinnedHTTPS(http.client.HTTPSConnection):
    def __init__(self, parsed, address):
        super().__init__(parsed.hostname, parsed.port or 443, timeout=10, context=ssl.create_default_context())
        self.address = address

    def connect(self):
        family, socktype, proto, _, sockaddr = self.address
        raw = socket.socket(family, socktype, proto)
        raw.settimeout(self.timeout)
        try:
            raw.connect(sockaddr)
            self.sock = self._context.wrap_socket(raw, server_hostname=self.host)
        except BaseException:
            raw.close()
            raise


def request_json(url, data=None, json_body=None):
    parsed, address = public_target(url)
    connection = _PinnedHTTPS(parsed, address)
    headers = {'Accept': 'application/json'}
    body = None
    if data is not None:
        body = urlencode(data).encode()
        headers['Content-Type'] = 'application/x-www-form-urlencoded'
    elif json_body is not None:
        body = json.dumps(json_body).encode()
        headers['Content-Type'] = 'application/json'
    try:
        connection.request('POST' if body is not None else 'GET', parsed.path
                           + ('?' + parsed.query if parsed.query else '') or '/', body, headers)
        response = connection.getresponse()
        raw = response.read(1_048_577)
        if len(raw) > 1_048_576:
            raise OAuthError()
        if response.status in {404, 405} and body is None:
            raise OAuthError('not_found')
        if 200 <= response.status < 300 and not raw:
            return {}
        payload = json.loads(raw)
        if not isinstance(payload, dict):
            raise OAuthError()
        if response.status in {400, 401} and payload.get('error') == 'invalid_grant':
            raise OAuthError('authorization')
        if not 200 <= response.status < 300:
            raise OAuthError()
        return payload
    except OAuthError:
        raise
    except (OSError, ValueError, http.client.HTTPException):
        raise OAuthError() from None
    finally:
        connection.close()

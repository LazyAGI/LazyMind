from __future__ import annotations

import argparse
import base64
import binascii
import ipaddress
import json
import os
import select
import socket
import socketserver
import threading
import time
from pathlib import Path
from urllib.parse import urlsplit

from .protocol import normalize_domains


MAX_CONTROL_BYTES = 64 * 1024
MAX_HEADER_BYTES = 64 * 1024


class EgressPolicyStore:
    def __init__(self) -> None:
        self._lock = threading.Lock()
        self._policies: dict[str, tuple[float, tuple[str, ...]]] = {}
        self._violations: dict[str, dict[str, str]] = {}

    def register(self, token: str, domains: list[str], ttl_seconds: int) -> None:
        if not token or len(token) > 256:
            raise ValueError('egress token is invalid')
        normalized = tuple(normalize_domains(domains))
        expires = time.time() + min(600, max(1, int(ttl_seconds)))
        with self._lock:
            self._policies[token] = (expires, normalized)
            self._violations.pop(token, None)

    def unregister(self, token: str) -> None:
        with self._lock:
            self._policies.pop(token, None)
            self._violations.pop(token, None)

    def domains(self, token: str) -> tuple[str, ...]:
        with self._lock:
            record = self._policies.get(token)
            if record is None:
                return ()
            expires, domains = record
            if expires <= time.time():
                self._policies.pop(token, None)
                return ()
            return domains

    def registered(self, token: str) -> bool:
        with self._lock:
            record = self._policies.get(token)
            if record is None:
                return False
            if record[0] <= time.time():
                self._policies.pop(token, None)
                self._violations.pop(token, None)
                return False
            return True

    def record_violation(self, token: str, resource: str, reason: str) -> None:
        with self._lock:
            if token in self._policies and token not in self._violations:
                self._violations[token] = {
                    'blocked_resource': resource,
                    'reason': reason,
                }

    def violation(self, token: str) -> dict[str, str] | None:
        with self._lock:
            violation = self._violations.get(token)
            return dict(violation) if violation else None


def _domain_allowed(host: str, domains: tuple[str, ...]) -> bool:
    normalized = host.rstrip('.').lower()
    try:
        ipaddress.ip_address(normalized)
        return False
    except ValueError:
        pass
    for domain in domains:
        if domain.startswith('*.'):
            suffix = domain[1:]
            if normalized.endswith(suffix) and normalized != domain[2:]:
                return True
        elif normalized == domain:
            return True
    return False


def _public_addresses(host: str, port: int) -> list[tuple[int, int, int, tuple]]:
    result = []
    for family, socktype, proto, _canonname, sockaddr in socket.getaddrinfo(
        host, port, type=socket.SOCK_STREAM,
    ):
        address = ipaddress.ip_address(sockaddr[0])
        if not address.is_global:
            raise PermissionError(f'egress destination resolved to a non-public address: {address}')
        item = (family, socktype, proto, sockaddr)
        if item not in result:
            result.append(item)
    if not result:
        raise OSError(f'no address found for {host}')
    return result


def _connect_public(host: str, port: int, timeout: float = 10.0) -> socket.socket:
    last_error: OSError | None = None
    for family, socktype, proto, sockaddr in _public_addresses(host, port):
        connection = socket.socket(family, socktype, proto)
        connection.settimeout(timeout)
        try:
            connection.connect(sockaddr)
            return connection
        except OSError as exc:
            last_error = exc
            connection.close()
    raise last_error or OSError(f'could not connect to {host}')


def _proxy_token(header: str) -> str:
    scheme, _, payload = str(header or '').partition(' ')
    if scheme.lower() != 'basic' or not payload:
        return ''
    try:
        decoded = base64.b64decode(payload, validate=True).decode('utf-8')
    except (binascii.Error, ValueError, UnicodeDecodeError):
        return ''
    return decoded.partition(':')[0]


class EgressProxyHandler(socketserver.StreamRequestHandler):
    timeout = 15

    def handle(self) -> None:
        token = ''
        target = ''
        host = ''
        try:
            request_line = self.rfile.readline(MAX_HEADER_BYTES + 1)
            if not request_line or len(request_line) > MAX_HEADER_BYTES:
                raise ValueError('invalid proxy request line')
            method, target, version = request_line.decode('latin-1').strip().split(' ', 2)
            headers = self._read_headers()
            token = _proxy_token(headers.get('proxy-authorization', ''))
            domains = self.server.policy_store.domains(token)
            if not self.server.policy_store.registered(token):
                self._reject(407, 'Proxy authentication required')
                return
            host, port, origin = self._destination(method, target)
            if not _domain_allowed(host, domains):
                self.server.policy_store.record_violation(
                    token, host, f'Destination is not allowed: {host}',
                )
                self._reject(403, f'Destination is not allowed: {host}')
                return
            upstream = _connect_public(host, port)
            try:
                if method.upper() == 'CONNECT':
                    self.wfile.write(b'HTTP/1.1 200 Connection Established\r\n\r\n')
                    self.wfile.flush()
                    first_record = _read_tls_record(self.connection)
                    server_name = _tls_server_name(first_record)
                    if server_name.rstrip('.').lower() != host.rstrip('.').lower():
                        raise PermissionError(
                            f'TLS server name does not match the allowed destination: {server_name}'
                        )
                    upstream.sendall(first_record)
                    self._relay(self.connection, upstream)
                    return
                self._forward_http(upstream, method, origin, version, headers, host, port)
            finally:
                upstream.close()
        except PermissionError as exc:
            self.server.policy_store.record_violation(token, host or target, str(exc))
            try:
                self._reject(403, str(exc))
            except OSError:
                pass
        except (OSError, ValueError) as exc:
            try:
                self._reject(502, str(exc))
            except OSError:
                pass

    def _read_headers(self) -> dict[str, str]:
        headers: dict[str, str] = {}
        total = 0
        while True:
            line = self.rfile.readline(MAX_HEADER_BYTES + 1)
            total += len(line)
            if total > MAX_HEADER_BYTES or not line:
                raise ValueError('proxy request headers are too large')
            if line in (b'\r\n', b'\n'):
                return headers
            name, separator, value = line.decode('latin-1').partition(':')
            if not separator:
                raise ValueError('malformed proxy request header')
            headers[name.strip().lower()] = value.strip()

    @staticmethod
    def _destination(method: str, target: str) -> tuple[str, int, str]:
        if method.upper() == 'CONNECT':
            host, separator, raw_port = target.rpartition(':')
            if not separator or int(raw_port) != 443:
                raise PermissionError('CONNECT is limited to HTTPS port 443')
            return host.strip('[]'), 443, ''
        parsed = urlsplit(target)
        if parsed.scheme not in {'http', 'https'} or not parsed.hostname:
            raise PermissionError('Only absolute HTTP/HTTPS proxy requests are supported')
        port = parsed.port or (443 if parsed.scheme == 'https' else 80)
        if port not in {80, 443}:
            raise PermissionError('HTTP/HTTPS egress is limited to ports 80 and 443')
        origin = parsed.path or '/'
        if parsed.query:
            origin += '?' + parsed.query
        return parsed.hostname, port, origin

    def _forward_http(
        self,
        upstream: socket.socket,
        method: str,
        origin: str,
        version: str,
        headers: dict[str, str],
        host: str,
        port: int,
    ) -> None:
        excluded = {'proxy-authorization', 'proxy-connection', 'connection', 'host'}
        lines = [f'{method} {origin} {version}']
        lines.extend(f'{name}: {value}' for name, value in headers.items() if name not in excluded)
        authority = host if port == 80 else f'{host}:{port}'
        lines.append(f'Host: {authority}')
        lines.append('Connection: close')
        upstream.sendall(('\r\n'.join(lines) + '\r\n\r\n').encode('latin-1'))
        content_length = int(headers.get('content-length') or 0)
        if content_length < 0 or content_length > 16 * 1024 * 1024:
            raise ValueError('proxy request body is too large')
        if content_length:
            upstream.sendall(self.rfile.read(content_length))
        while True:
            chunk = upstream.recv(65536)
            if not chunk:
                break
            self.connection.sendall(chunk)

    @staticmethod
    def _relay(left: socket.socket, right: socket.socket) -> None:
        sockets = [left, right]
        while sockets:
            readable, _, _ = select.select(sockets, [], [], 30)
            if not readable:
                return
            for source in readable:
                data = source.recv(65536)
                if not data:
                    return
                (right if source is left else left).sendall(data)

    def _reject(self, status: int, message: str) -> None:
        body = (message or 'Proxy request rejected').encode('utf-8')[:4096]
        response = (
            f'HTTP/1.1 {status} Rejected\r\nContent-Type: text/plain; charset=utf-8\r\n'
            f'Content-Length: {len(body)}\r\nConnection: close\r\n\r\n'
        ).encode('ascii') + body
        self.connection.sendall(response)


def _recv_exact(connection: socket.socket, size: int) -> bytes:
    result = bytearray()
    while len(result) < size:
        chunk = connection.recv(size - len(result))
        if not chunk:
            raise PermissionError('TLS client hello ended unexpectedly')
        result.extend(chunk)
    return bytes(result)


def _read_tls_record(connection: socket.socket) -> bytes:
    header = _recv_exact(connection, 5)
    if header[0] != 22:
        raise PermissionError('HTTPS CONNECT requires a TLS client hello')
    size = int.from_bytes(header[3:5], 'big')
    if size <= 0 or size > 18432:
        raise PermissionError('TLS client hello record has an invalid size')
    return header + _recv_exact(connection, size)


def _tls_server_name(record: bytes) -> str:
    data = memoryview(record)
    if len(data) < 9 or data[5] != 1:
        raise PermissionError('HTTPS CONNECT requires a TLS client hello')
    handshake_size = int.from_bytes(data[6:9], 'big')
    body = data[9:9 + handshake_size]
    if len(body) != handshake_size or len(body) < 38:
        raise PermissionError('TLS client hello is incomplete')
    offset = 34
    session_size = body[offset]
    offset += 1 + session_size
    if offset + 2 > len(body):
        raise PermissionError('TLS client hello has no cipher suites')
    cipher_size = int.from_bytes(body[offset:offset + 2], 'big')
    offset += 2 + cipher_size
    if offset >= len(body):
        raise PermissionError('TLS client hello has no compression methods')
    compression_size = body[offset]
    offset += 1 + compression_size
    if offset + 2 > len(body):
        raise PermissionError('TLS client hello has no extensions')
    extensions_size = int.from_bytes(body[offset:offset + 2], 'big')
    offset += 2
    extensions_end = offset + extensions_size
    if extensions_end > len(body):
        raise PermissionError('TLS client hello extensions are incomplete')
    while offset + 4 <= extensions_end:
        extension_type = int.from_bytes(body[offset:offset + 2], 'big')
        extension_size = int.from_bytes(body[offset + 2:offset + 4], 'big')
        offset += 4
        extension = body[offset:offset + extension_size]
        offset += extension_size
        if len(extension) != extension_size:
            raise PermissionError('TLS client hello extension is incomplete')
        if extension_type != 0 or len(extension) < 5:
            continue
        names_size = int.from_bytes(extension[0:2], 'big')
        if names_size + 2 != len(extension) or extension[2] != 0:
            raise PermissionError('TLS client hello server name is invalid')
        name_size = int.from_bytes(extension[3:5], 'big')
        if name_size + 5 > len(extension):
            raise PermissionError('TLS client hello server name is incomplete')
        try:
            return bytes(extension[5:5 + name_size]).decode('ascii')
        except UnicodeDecodeError as exc:
            raise PermissionError('TLS server name must be ASCII') from exc
    raise PermissionError('TLS client hello does not contain a server name')


class ThreadingTCPProxy(socketserver.ThreadingMixIn, socketserver.TCPServer):
    daemon_threads = True
    allow_reuse_address = True

    def __init__(self, address: tuple[str, int], policy_store: EgressPolicyStore):
        self.policy_store = policy_store
        super().__init__(address, EgressProxyHandler)


class EgressControlHandler(socketserver.StreamRequestHandler):
    def handle(self) -> None:
        raw = self.rfile.read(MAX_CONTROL_BYTES + 1)
        try:
            if len(raw) > MAX_CONTROL_BYTES:
                raise ValueError('egress control request is too large')
            request = json.loads(raw.decode('utf-8'))
            action = request.get('action')
            if action == 'register':
                self.server.policy_store.register(
                    str(request.get('token') or ''),
                    request.get('domains') or [],
                    int(request.get('ttl_seconds') or 60),
                )
            elif action == 'unregister':
                self.server.policy_store.unregister(str(request.get('token') or ''))
            elif action == 'status':
                response = {
                    'status': 'ok',
                    'violation': self.server.policy_store.violation(
                        str(request.get('token') or ''),
                    ),
                }
                self.wfile.write(json.dumps(response).encode('utf-8'))
                return
            elif action != 'ping':
                raise ValueError('unknown egress control action')
            response = {'status': 'ok'}
        except (TypeError, ValueError) as exc:
            response = {'status': 'error', 'error': str(exc)}
        self.wfile.write(json.dumps(response).encode('utf-8'))


class ThreadingUnixControl(socketserver.ThreadingMixIn, socketserver.UnixStreamServer):
    daemon_threads = True

    def __init__(self, socket_path: str, policy_store: EgressPolicyStore):
        self.policy_store = policy_store
        super().__init__(socket_path, EgressControlHandler)


def control_request(socket_path: str, request: dict) -> dict:
    payload = json.dumps(request, separators=(',', ':')).encode('utf-8')
    with socket.socket(socket.AF_UNIX, socket.SOCK_STREAM) as connection:
        connection.settimeout(3)
        connection.connect(socket_path)
        connection.sendall(payload)
        connection.shutdown(socket.SHUT_WR)
        response = connection.recv(MAX_CONTROL_BYTES)
    result = json.loads(response.decode('utf-8'))
    if not isinstance(result, dict):
        raise ValueError('invalid egress control response')
    return result


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument('--listen', default='0.0.0.0')
    parser.add_argument('--port', type=int, default=18081)
    parser.add_argument('--control-socket', default='/run/lazymind-egress/control.sock')
    parser.add_argument('--healthcheck', action='store_true')
    args = parser.parse_args()
    if args.healthcheck:
        result = control_request(args.control_socket, {'action': 'ping'})
        raise SystemExit(0 if result.get('status') == 'ok' else 1)

    control_path = Path(args.control_socket)
    control_path.parent.mkdir(parents=True, exist_ok=True)
    if control_path.exists() or control_path.is_socket():
        control_path.unlink()
    store = EgressPolicyStore()
    control = ThreadingUnixControl(str(control_path), store)
    os.chmod(control_path, 0o600)
    proxy = ThreadingTCPProxy((args.listen, args.port), store)
    thread = threading.Thread(target=control.serve_forever, daemon=True)
    thread.start()
    try:
        proxy.serve_forever(poll_interval=0.2)
    finally:
        proxy.server_close()
        control.shutdown()
        control.server_close()
        thread.join(timeout=5)
        try:
            control_path.unlink()
        except FileNotFoundError:
            pass


if __name__ == '__main__':
    main()

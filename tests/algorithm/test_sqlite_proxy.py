import json
import http.server
import threading

import pytest

from lazymind.common.database import sqlite_proxy


def test_connection_reuses_http_socket_for_sequential_requests(monkeypatch, tmp_path):
    ports = []

    class Handler(http.server.BaseHTTPRequestHandler):
        protocol_version = 'HTTP/1.1'

        def do_POST(self):
            self.rfile.read(int(self.headers['Content-Length']))
            ports.append(self.client_address[1])
            body = b'{}'
            self.send_response(200)
            self.send_header('Content-Length', str(len(body)))
            self.end_headers()
            self.wfile.write(body)

        def log_message(self, *_args):
            pass

    server = http.server.ThreadingHTTPServer(('127.0.0.1', 0), Handler)
    server.daemon_threads = True
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    token = tmp_path / 'token'
    token.write_text('secret')
    monkeypatch.setenv('LAZYMIND_SQLITE_SERVER_URL', f'http://127.0.0.1:{server.server_port}')
    monkeypatch.setenv('LAZYMIND_SQLITE_SERVER_TOKEN_FILE', str(token))
    try:
        connection = sqlite_proxy.Connection('segments')
        for _ in range(12):
            assert connection._call('/v1/query') == {}
        connection.close()
    finally:
        server.shutdown()
        server.server_close()
        thread.join()
    assert len(ports) == 12
    assert len(set(ports)) == 1


def _connection_stub():
    connection = object.__new__(sqlite_proxy.Connection)
    connection._database = 'segments'
    connection._tx_id = 'transaction-1'
    connection._closed = False
    connection._lock = threading.RLock()
    connection._isolation_level = ''
    return connection


def test_executemany_chunks_by_encoded_request_size(monkeypatch):
    connection = _connection_stub()
    requests = []

    def call(path, values=None):
        encoded = connection._encoded_payload(values)
        assert len(encoded) <= sqlite_proxy._MAX_REQUEST_BODY_BYTES
        requests.append(json.loads(encoded))
        return {'rowsAffected': len(values['batches']), 'lastInsertId': len(requests)}

    connection._call = call
    monkeypatch.setattr(sqlite_proxy, '_MAX_REQUEST_BODY_BYTES', 800)

    cursor = sqlite_proxy.Cursor(connection).executemany(
        'INSERT INTO chunks(text, position) VALUES (?, ?)',
        [('中文内容' * 20, index) for index in range(5)],
    )

    assert len(requests) > 1
    assert sum(len(request['batches']) for request in requests) == 5
    assert cursor.rowcount == 5


def test_executemany_rejects_one_row_larger_than_request_limit(monkeypatch):
    connection = _connection_stub()
    connection._call = lambda *_args, **_kwargs: pytest.fail('oversized request was sent')
    monkeypatch.setattr(sqlite_proxy, '_MAX_REQUEST_BODY_BYTES', 256)

    with pytest.raises(sqlite_proxy.sqlite3.DataError, match='parameter row'):
        sqlite_proxy.Cursor(connection).executemany(
            'INSERT INTO chunks(text) VALUES (?)', [('中文' * 100,)],
        )

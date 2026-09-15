import json
import threading

import pytest

from lazymind.common.database import sqlite_proxy


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

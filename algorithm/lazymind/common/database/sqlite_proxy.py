import base64
import datetime
import json
import os
import sqlite3
import threading
import urllib.error
import urllib.request
from collections import defaultdict


_SERVER_URL_ENV = 'LAZYMIND_SQLITE_SERVER_URL'
_TOKEN_FILE_ENV = 'LAZYMIND_SQLITE_SERVER_TOKEN_FILE'
_WRITE_KEYWORDS = frozenset({'INSERT', 'UPDATE', 'DELETE', 'REPLACE'})
# The Go server accepts 16 MiB. Keep headroom for protocol evolution while
# sizing the actual JSON bytes (including ensure_ascii expansion for CJK).
_MAX_REQUEST_BODY_BYTES = 15 << 20


def _database_alias(value):
    alias = str(value or '').strip()
    if alias.startswith('sqliteproxy://'):
        alias = alias[len('sqliteproxy://'):]
    alias = alias.strip('/')
    if not alias:
        raise sqlite3.OperationalError('sqlite proxy database alias is empty')
    return alias


def _encode_value(value):
    if value is None:
        return {'type': 'null'}
    if isinstance(value, bool):
        return {'type': 'bool', 'bool': value}
    if isinstance(value, int):
        return {'type': 'int', 'int': str(value)}
    if isinstance(value, float):
        return {'type': 'float', 'float': value}
    if isinstance(value, str):
        return {'type': 'text', 'text': value}
    if isinstance(value, (bytes, bytearray, memoryview)):
        encoded = base64.b64encode(bytes(value)).decode('ascii')
        return {'type': 'bytes', 'text': encoded}
    if isinstance(value, datetime.datetime):
        return {'type': 'text', 'text': str(value)}
    if isinstance(value, (datetime.date, datetime.time)):
        return {'type': 'text', 'text': value.isoformat()}
    raise sqlite3.InterfaceError(f'unsupported sqlite proxy value type: {type(value).__name__}')


def _decode_value(value):
    value_type = value.get('type')
    if value_type == 'null':
        return None
    if value_type == 'bool':
        return bool(value.get('bool'))
    if value_type == 'int':
        return int(value.get('int', '0'))
    if value_type == 'float':
        return float(value.get('float', 0))
    if value_type == 'text':
        return value.get('text', '')
    if value_type == 'bytes':
        return base64.b64decode(value.get('text', ''))
    if value_type == 'time':
        return value.get('text', '')
    raise sqlite3.InterfaceError(f'unsupported sqlite proxy response type: {value_type!r}')


def _read_token():
    path = os.environ.get(_TOKEN_FILE_ENV, '').strip()
    if not path:
        raise sqlite3.OperationalError(f'{_TOKEN_FILE_ENV} is empty')
    try:
        with open(path, 'r', encoding='utf-8') as token_file:
            token = token_file.read().strip()
    except OSError as exc:
        raise sqlite3.OperationalError(f'cannot read sqlite proxy token: {exc}') from exc
    if not token:
        raise sqlite3.OperationalError('sqlite proxy token is empty')
    return token


class Cursor:
    arraysize = 1

    def __init__(self, connection):
        self.connection = connection
        self.description = None
        self.rowcount = -1
        self.lastrowid = None
        self._rows = []
        self._index = 0
        self._closed = False

    def execute(self, operation, parameters=()):
        self._check_open()
        parameters = () if parameters is None else parameters
        if isinstance(parameters, dict):
            raise sqlite3.NotSupportedError('sqlite proxy supports positional parameters only')
        keyword = str(operation or '').lstrip().split(None, 1)
        keyword = keyword[0].upper() if keyword else ''
        if keyword in {'BEGIN', 'START'}:
            self.connection._begin()
            self._apply_result({})
            return self
        if keyword in {'COMMIT', 'END'}:
            self.connection.commit()
            self._apply_result({})
            return self
        if keyword == 'ROLLBACK':
            self.connection.rollback()
            self._apply_result({})
            return self
        self.connection._begin_if_needed(operation)
        result = self.connection._call('/v1/query' if self._is_query(operation) else '/v1/execute', {
            'sql': operation,
            'args': [_encode_value(value) for value in parameters],
        })
        self._apply_result(result)
        return self

    def executemany(self, operation, seq_of_parameters):
        self._check_open()
        with self.connection._lock:
            self.connection._begin_if_needed(operation)
            empty_values = {'sql': operation, 'batches': []}
            empty_size = len(self.connection._encoded_payload(empty_values))
            if empty_size > _MAX_REQUEST_BODY_BYTES:
                raise sqlite3.DataError('sqlite proxy SQL exceeds request body limit')

            batches = []
            batches_size = 0
            total_rows_affected = 0
            last_insert_id = 0

            def flush():
                nonlocal batches, batches_size, total_rows_affected, last_insert_id
                if not batches:
                    return
                result = self.connection._call(
                    '/v1/executemany', {'sql': operation, 'batches': batches},
                )
                total_rows_affected += int(result.get('rowsAffected', 0))
                last_insert_id = result.get('lastInsertId', last_insert_id)
                batches = []
                batches_size = 0

            for parameters in seq_of_parameters:
                if isinstance(parameters, dict):
                    raise sqlite3.NotSupportedError(
                        'sqlite proxy supports positional parameters only',
                    )
                batch = [_encode_value(value) for value in parameters]
                batch_size = len(json.dumps(batch, separators=(',', ':')).encode('utf-8'))
                separator_size = 1 if batches else 0
                if empty_size + batch_size > _MAX_REQUEST_BODY_BYTES:
                    raise sqlite3.DataError('sqlite proxy parameter row exceeds request body limit')
                if empty_size + batches_size + separator_size + batch_size > _MAX_REQUEST_BODY_BYTES:
                    flush()
                    separator_size = 0
                batches.append(batch)
                batches_size += separator_size + batch_size
            flush()
            self._apply_result({
                'rowsAffected': total_rows_affected,
                'lastInsertId': last_insert_id,
            })
        return self

    def executescript(self, script):
        self._check_open()
        for statement in script.split(';'):
            if statement.strip():
                self.execute(statement)
        return self

    def fetchone(self):
        self._check_open()
        if self._index >= len(self._rows):
            return None
        row = self._rows[self._index]
        self._index += 1
        return row

    def fetchmany(self, size=None):
        self._check_open()
        size = self.arraysize if size is None else size
        start = self._index
        self._index = min(len(self._rows), start + size)
        return self._rows[start:self._index]

    def fetchall(self):
        self._check_open()
        rows = self._rows[self._index:]
        self._index = len(self._rows)
        return rows

    def close(self):
        self._closed = True
        self._rows = []

    def setinputsizes(self, sizes):
        return None

    def setoutputsize(self, size, column=None):
        return None

    def __iter__(self):
        return self

    def __next__(self):
        row = self.fetchone()
        if row is None:
            raise StopIteration
        return row

    def _apply_result(self, result):
        columns = result.get('columns')
        self.description = None if columns is None else [
            (column, None, None, None, None, None, None) for column in columns
        ]
        self._rows = [tuple(_decode_value(value) for value in row) for row in result.get('rows', [])]
        self._index = 0
        self.rowcount = -1 if columns is not None else int(result.get('rowsAffected', 0))
        self.lastrowid = None if columns is not None else result.get('lastInsertId')

    @staticmethod
    def _is_query(operation):
        statement = str(operation or '').lstrip()
        keyword = statement.split(None, 1)
        if not keyword:
            return False
        keyword = keyword[0].upper()
        return keyword in {'SELECT', 'PRAGMA', 'EXPLAIN', 'WITH'} or (
            keyword in _WRITE_KEYWORDS and ' RETURNING ' in f' {statement.upper()} '
        )

    def _check_open(self):
        if self._closed:
            raise sqlite3.ProgrammingError('cannot operate on a closed cursor')
        self.connection._check_open()


class Connection:
    def __init__(self, database):
        self._database = _database_alias(database)
        self._base_url = os.environ.get(_SERVER_URL_ENV, '').strip().rstrip('/')
        if not self._base_url:
            raise sqlite3.OperationalError(f'{_SERVER_URL_ENV} is empty')
        self._token = _read_token()
        self._tx_id = None
        self._closed = False
        self._lock = threading.RLock()
        self._isolation_level = ''

    @property
    def isolation_level(self):
        return self._isolation_level

    @isolation_level.setter
    def isolation_level(self, value):
        self._isolation_level = value

    @property
    def in_transaction(self):
        return self._tx_id is not None

    def cursor(self):
        self._check_open()
        return Cursor(self)

    def execute(self, operation, parameters=()):
        return self.cursor().execute(operation, parameters)

    def executemany(self, operation, seq_of_parameters):
        return self.cursor().executemany(operation, seq_of_parameters)

    def executescript(self, script):
        return self.cursor().executescript(script)

    def commit(self):
        with self._lock:
            self._check_open()
            if self._tx_id is not None:
                self._call('/v1/commit')
                self._tx_id = None

    def rollback(self):
        with self._lock:
            self._check_open()
            if self._tx_id is not None:
                self._call('/v1/rollback')
                self._tx_id = None

    def close(self):
        with self._lock:
            if self._closed:
                return
            if self._tx_id is not None:
                try:
                    self._call('/v1/rollback')
                finally:
                    self._tx_id = None
            self._closed = True

    def create_function(self, name, num_params, func, deterministic=False):
        return None

    def create_aggregate(self, name, num_params, aggregate_class):
        raise sqlite3.NotSupportedError('remote aggregate registration is not supported')

    def set_trace_callback(self, trace_callback):
        return None

    def __enter__(self):
        self._check_open()
        return self

    def __exit__(self, exc_type, exc_value, traceback):
        if exc_type is None:
            self.commit()
        else:
            self.rollback()

    def _begin_if_needed(self, operation):
        keyword = str(operation or '').lstrip().split(None, 1)
        if self._isolation_level is None or not keyword or keyword[0].upper() not in _WRITE_KEYWORDS:
            return
        with self._lock:
            if self._tx_id is None:
                self._begin()

    def _begin(self):
        with self._lock:
            self._check_open()
            if self._tx_id is not None:
                raise sqlite3.OperationalError('cannot start a transaction within a transaction')
            result = self._call('/v1/begin')
            self._tx_id = result['txId']

    def _call(self, path, values=None):
        with self._lock:
            self._check_open()
            request = urllib.request.Request(
                self._base_url + path,
                data=self._encoded_payload(values),
                headers={
                    'Authorization': 'Bearer ' + self._token,
                    'Content-Type': 'application/json',
                },
                method='POST',
            )
            try:
                with urllib.request.urlopen(request, timeout=300) as response:
                    result = json.loads(response.read().decode('utf-8'))
            except urllib.error.HTTPError as exc:
                try:
                    detail = json.loads(exc.read().decode('utf-8')).get('error')
                except Exception:
                    detail = None
                raise sqlite3.OperationalError(detail or str(exc)) from exc
            except (OSError, ValueError) as exc:
                raise sqlite3.OperationalError(f'sqlite proxy request failed: {exc}') from exc
            if result.get('error'):
                raise sqlite3.OperationalError(result['error'])
            return result

    def _encoded_payload(self, values=None):
        payload = {'db': self._database}
        if self._tx_id is not None:
            payload['txId'] = self._tx_id
        if values:
            payload.update(values)
        return json.dumps(payload, separators=(',', ':')).encode('utf-8')

    def _check_open(self):
        if self._closed:
            raise sqlite3.ProgrammingError('cannot operate on a closed database')


def connect(database, timeout=5.0, detect_types=0, isolation_level='', check_same_thread=True,
            factory=Connection, cached_statements=128, uri=False, **kwargs):
    del timeout, detect_types, check_same_thread, cached_statements, uri, kwargs
    connection = factory(database)
    connection.isolation_level = isolation_level
    return connection


_adapter_lock = threading.Lock()
_adapter_installed = False


def install_lazyllm_sqlite_proxy():
    global _adapter_installed
    if _adapter_installed:
        return
    with _adapter_lock:
        if _adapter_installed:
            return

        import sqlalchemy
        from lazyllm.tools.rag.store.hybrid.map_store import MapStore
        from lazyllm.tools.rag.store.segment.sqlite_store import SQLiteStore
        from lazyllm.tools.sql.sql_manager import SqlManager

        manager_engine = SqlManager.engine.fget
        sqlite_store_open = SQLiteStore._open_conn
        sqlite_store_dir = SQLiteStore.dir.fget
        map_store_open = MapStore._open_conn
        map_store_connect = MapStore.connect
        map_store_dir = MapStore.dir.fget

        def proxied_manager_engine(manager):
            if not manager._db_name.startswith('sqliteproxy://'):
                return manager_engine(manager)
            if manager._engine is None:
                manager._engine = sqlalchemy.create_engine(
                    'sqlite://',
                    creator=lambda: connect(manager._db_name, check_same_thread=False),
                    poolclass=sqlalchemy.pool.QueuePool,
                    echo=False,
                )
            return manager._engine

        def proxied_sqlite_store_open(store):
            if not store._db_path.startswith('sqliteproxy://'):
                return sqlite_store_open(store)
            if connection := getattr(store._local, 'conn', None):
                return connection
            connection = connect(store._db_path, timeout=5.0)
            store._local.conn = connection
            return connection

        def proxied_sqlite_store_dir(store):
            if store._db_path.startswith('sqliteproxy://'):
                return ''
            return sqlite_store_dir(store)

        def proxied_map_store_open(store):
            if not store._uri or not store._uri.startswith('sqliteproxy://'):
                return map_store_open(store)
            if store._conn:
                return store._conn
            connection = connect(store._uri, timeout=5.0, check_same_thread=False)
            store._conn = connection
            return connection

        def proxied_map_store_connect(store, collections=None, **kwargs):
            if not store._uri or not store._uri.startswith('sqliteproxy://'):
                return map_store_connect(store, collections=collections, **kwargs)
            store._uid2data = {}
            store._collection2uids = defaultdict(set)
            store._col_doc_uids = defaultdict(lambda: defaultdict(set))
            store._col_kb_doc_uids = defaultdict(lambda: defaultdict(lambda: defaultdict(set)))
            store._col_parent_uids = defaultdict(lambda: defaultdict(set))
            store._col_number_uids = defaultdict(lambda: defaultdict(set))
            store._lock = threading.Lock()
            with store._lock:
                connection = store._open_conn()
                if collections:
                    cursor = connection.cursor()
                    for collection in collections:
                        store._ensure_table(cursor, collection)
                    connection.commit()

        def proxied_map_store_dir(store):
            if store._uri and store._uri.startswith('sqliteproxy://'):
                return ''
            return map_store_dir(store)

        SqlManager.engine = property(proxied_manager_engine)
        SQLiteStore._open_conn = proxied_sqlite_store_open
        SQLiteStore.dir = property(proxied_sqlite_store_dir)
        MapStore._open_conn = proxied_map_store_open
        MapStore.connect = proxied_map_store_connect
        MapStore.dir = property(proxied_map_store_dir)
        _adapter_installed = True

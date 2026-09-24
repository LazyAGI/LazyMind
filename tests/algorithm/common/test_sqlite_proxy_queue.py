from datetime import datetime, timedelta, timezone

import pytest
import sqlalchemy

from lazymind.common.database import sqlite_proxy
from lazyllm.tools.rag.parsing_service.base import FINISHED_TASK_QUEUE_TABLE_INFO
from lazyllm.tools.rag.parsing_service.queue import _SQLBasedQueue
from lazyllm.tools.rag.parsing_service.server import DocumentProcessor
from lazyllm.tools.rag.store.hybrid.map_store import MapStore
from lazyllm.tools.rag.store.segment.sqlite_store import SQLiteStore
from lazyllm.tools.sql.sql_manager import SqlManager


@pytest.fixture
def proxy_adapter(monkeypatch):
    for cls, names in [(SqlManager, ['engine']), (SQLiteStore, ['_open_conn', 'dir']),
                       (MapStore, ['_open_conn', 'connect', 'dir']), (_SQLBasedQueue, ['peek'])]:
        for name in names:
            monkeypatch.setattr(cls, name, getattr(cls, name))
    monkeypatch.setattr(sqlite_proxy, '_adapter_installed', False)
    sqlite_proxy.install_lazyllm_sqlite_proxy()


@pytest.mark.parametrize('tz', [None, timezone.utc, timezone(timedelta(hours=8))])
def test_proxy_queue_preserves_stored_time_and_callback_deadline(tmp_path, proxy_adapter, tz):
    queue = _SQLBasedQueue(
        table_name='finished_time_test', columns=FINISHED_TASK_QUEUE_TABLE_INFO['columns'],
        db_config={'db_type': 'sqlite', 'db_name': str(tmp_path / 'queue.db'),
                   'user': None, 'password': None, 'host': None, 'port': None},
    )
    engine = queue._sql_manager.engine
    # Reuse a real SQLite engine while exercising the proxy-only adapter.
    queue._sql_manager._db_name = 'sqliteproxy://test'
    table = queue._sql_manager.get_table_orm_class(queue._table_name)

    def attach_driver_timezone(record, _context):
        if record.finished_at.tzinfo is None:
            record.finished_at = record.finished_at.replace(tzinfo=timezone.utc)

    sqlalchemy.event.listen(table, 'load', attach_driver_timezone)
    try:
        queue.enqueue(task_id='task', task_type='DOC_ADD', task_status='SUCCESS', finished_at=datetime.now())
        impl = object.__new__(DocumentProcessor._Impl)
        for delta, expected in [(-60, True), (60, False)]:
            stored = (datetime.now(tz) + timedelta(seconds=delta)).isoformat(' ')
            with engine.begin() as conn:
                conn.execute(sqlalchemy.text('UPDATE finished_time_test SET finished_at=:value'), {'value': stored})
            result = queue.peek({'task_id': 'task'})
            expected_time = datetime.fromisoformat(stored)
            if expected_time.tzinfo is not None:
                expected_time = expected_time.astimezone().replace(tzinfo=None)
            assert result['finished_at'] == expected_time
            assert impl._is_callback_due(result) is expected
        assert queue.peek({'task_id': 'missing'}) is None
    finally:
        engine.dispose()


def test_regular_sqlite_queue_keeps_original_datetime_result(tmp_path, proxy_adapter):
    queue = _SQLBasedQueue(
        table_name='finished_time_test', columns=FINISHED_TASK_QUEUE_TABLE_INFO['columns'],
        db_config={'db_type': 'sqlite', 'db_name': str(tmp_path / 'queue.db'),
                   'user': None, 'password': None, 'host': None, 'port': None},
    )
    try:
        now = datetime.now()
        queue.enqueue(task_id='task', task_type='DOC_ADD', task_status='SUCCESS', finished_at=now)
        assert queue.peek()['finished_at'] == now
    finally:
        queue._sql_manager.engine.dispose()

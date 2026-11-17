from datetime import datetime, timedelta
import os
from uuid import uuid4

import pytest

from lazyllm.tools.rag.parsing_service.base import WAITING_TASK_QUEUE_TABLE_INFO, _calculate_task_score
from lazyllm.tools.rag.parsing_service.queue import _SQLBasedQueue
from lazymind.processor.service.scheduling import claim_ingestion_first
from lazymind.processor.service.db import parse_db_url


@pytest.fixture
def queue(tmp_path):
    # Optional integration run against a disposable PostgreSQL database. Each
    # test owns its table, so parallel runs never share task records.
    url = os.environ.get('LAZYMIND_TEST_POSTGRES_URL')
    db_config = parse_db_url(url) if url else {
        'db_type': 'sqlite', 'db_name': str(tmp_path / 'queue.db'),
        'user': None, 'password': None, 'host': None, 'port': None,
    }
    queue = _SQLBasedQueue(
        table_name='test_ingestion_priority_' + uuid4().hex, columns=WAITING_TASK_QUEUE_TABLE_INFO['columns'],
        db_config=db_config, order_by='task_score',
    )
    try:
        yield queue
    finally:
        table = queue._sql_manager.get_table_orm_class(queue._table_name).__table__
        table.drop(queue._sql_manager.engine)
        queue._sql_manager.engine.dispose()


def enqueue(queue, task_id, task_type, **overrides):
    now = datetime.now()
    queue.enqueue(**dict(dict(task_id=task_id, task_type=task_type, user_priority=0,
                              task_score=_calculate_task_score(task_type, 0), message='{}',
                              status='WAITING', created_at=now, updated_at=now), **overrides))


def claim(queue, worker='worker-1'):
    return claim_ingestion_first(queue, worker_id=worker, lease_duration=300)


@pytest.mark.parametrize('foreground', ['DOC_ADD', 'DOC_REPARSE', 'DOC_TRANSFER', 'DOC_UPDATE_META'])
def test_foreground_precedes_existing_delete_backlog_and_cleanup_resumes(queue, foreground):
    for index in range(20):
        enqueue(queue, f'delete-{index}', 'DOC_DELETE')
    enqueue(queue, 'import-other-kb', foreground)
    task = claim(queue)
    assert task['task_id'] == 'import-other-kb'
    assert task['status'] == 'WORKING' and task['worker_id'] == 'worker-1'
    queue.delete({'task_id': task['task_id'], 'worker_id': 'worker-1'})
    first_delete = claim(queue)
    assert first_delete['task_type'] == 'DOC_DELETE'
    queue.delete({'task_id': first_delete['task_id'], 'worker_id': 'worker-1'})
    # New ingestion gets the next turn, even during a multi-document deletion.
    enqueue(queue, 'new-import', 'DOC_ADD')
    assert claim(queue)['task_id'] == 'new-import'
    queue.delete({'task_id': 'new-import'})
    for _ in range(19):
        task = claim(queue)
        assert task['task_type'] == 'DOC_DELETE'
        queue.delete({'task_id': task['task_id']})
    assert claim(queue) is None


def test_claim_preserves_active_leases_and_recovers_expired_ingestion_before_deletion(queue):
    enqueue(queue, 'active', 'DOC_ADD', status='WORKING', worker_id='other',
            lease_expires_at=datetime.now() + timedelta(minutes=5))
    enqueue(queue, 'delete', 'DOC_DELETE')
    enqueue(queue, 'interrupted', 'DOC_ADD', status='WORKING', worker_id='old',
            lease_expires_at=datetime.now() - timedelta(seconds=1))
    assert claim(queue)['task_id'] == 'interrupted'
    assert claim(queue, 'worker-2')['task_id'] == 'delete'
    assert claim(queue, 'worker-3') is None
    assert queue.peek({'task_id': 'active'})['worker_id'] == 'other'

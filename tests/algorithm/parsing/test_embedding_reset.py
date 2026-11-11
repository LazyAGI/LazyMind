import importlib
import sqlite3
from contextlib import nullcontext
from types import SimpleNamespace

import pytest
from sqlalchemy import create_engine, text

from lazymind.config import config
from lazymind.parsing.service import embedding_reset
from test_embedding_vector_maintenance import _core_engine


@pytest.fixture
def reset_index(monkeypatch, tmp_path):
    core = _core_engine()
    # Use the shared fixture schema on disk so the real HTTP worker thread can
    # access the same Core database without replacing the handler or service.
    core_path = tmp_path / 'core.db'
    with core.connect() as conn, sqlite3.connect(core_path) as snapshot:
        conn.connection.driver_connection.backup(snapshot)
    core.dispose()
    core = create_engine(f'sqlite:///{core_path}')
    with core.begin() as conn:
        for column in ('parse_error_code', 'parse_error_message', 'chunk_error_code',
                       'chunk_error_message', 'parse_fingerprint', 'chunk_fingerprint'):
            conn.execute(text(f"ALTER TABLE document_processing_states ADD COLUMN {column} TEXT DEFAULT ''"))
        conn.execute(text("UPDATE embedding_index_states SET phase='clearing'"))
    tasks = create_engine(f'sqlite:///{tmp_path}/tasks.db')
    with tasks.begin() as conn:
        for table in ('lazyllm_waiting_task_queue', 'lazyllm_finished_task_queue'):
            conn.execute(text(f'CREATE TABLE {table} (task_id TEXT)'))
    monkeypatch.setitem(config._impl, 'core_database_url', 'sqlite:///test-core')
    monkeypatch.setitem(config._impl, 'database_url', str(tasks.url))
    monkeypatch.setitem(config._impl, 'shared_upload_dir', str(tmp_path))
    monkeypatch.setattr(embedding_reset, '_engine', lambda url: core)
    original = tmp_path / 'original.txt'
    original.write_text('keep the source', encoding='utf-8')
    artifact = tmp_path / 'original.txt.lazymind-base.json'
    artifact.write_text('{"chunks": ["old chunk"]}', encoding='utf-8')
    with core.begin() as conn:
        conn.execute(text('UPDATE document_processing_states SET chunk_artifact_ref=:ref'), {'ref': str(artifact)})

    # Real SQLite segment storage; the vector service is isolated at its boundary.
    import lazyllm.tools.rag.store as stores
    from lazymind.parsing.service import build_document
    segment_path = tmp_path / 'segments.db'
    segment = stores.SQLiteStore(db_path=str(segment_path))
    segment.connect()
    assert segment.upsert('col_block', [{'uid': 'old-segment', 'content': 'remove this chunk',
                                         'doc_id': 'doc-indexed', 'kb_id': 'indexed'}])
    deleted = []
    fail = []

    class VectorStore:
        def __init__(self, **kwargs):
            pass

        def connect(self):
            pass

        def delete(self, collection):
            if fail:
                raise RuntimeError('vector service unavailable')
            deleted.append(collection)
            return True

    monkeypatch.setattr(stores, 'MilvusStore', VectorStore)
    monkeypatch.setattr(build_document, '_build_store_config', lambda _: {
        'vector_store': {'type': 'milvus', 'kwargs': {}},
        'segment_store': {'type': 'SQLiteStore', 'kwargs': {'db_path': str(segment_path)}},
    })
    yield SimpleNamespace(core=core, tasks=tasks, original=original, artifact=artifact,
                          segment_path=segment_path, deleted=deleted, fail=fail)
    core.dispose()
    tasks.dispose()


def test_clear_keeps_originals_and_marks_every_document_for_manual_parsing(reset_index):
    f = reset_index
    assert embedding_reset.clear_chunks(7) == {'cleared': True, 'generation': 7}
    assert f.original.read_text(encoding='utf-8') == 'keep the source'
    assert not f.artifact.exists()
    assert 'col_block' in f.deleted
    assert 'col_image' in f.deleted
    with sqlite3.connect(f.segment_path) as conn:
        tables = conn.execute("SELECT name FROM sqlite_master WHERE name IN ('col_block', 'col_block_fts')").fetchall()
        assert tables == []
    with f.core.connect() as conn:
        documents = conn.execute(text('SELECT COUNT(*) FROM documents')).scalar_one()
        states = conn.execute(text('SELECT * FROM document_processing_states')).mappings().all()
    assert len(states) == documents == 5
    for state in states:
        assert state['parse_status'] == state['chunk_status'] == state['index_status'] == 'pending'
        assert state['embedding_version'] == 'reembed:manual'
        assert state['chunk_artifact_ref'] == state['index_artifact_ref'] == ''
    with f.tasks.connect() as conn:
        assert conn.execute(text('SELECT COUNT(*) FROM lazyllm_waiting_task_queue')).scalar_one() == 0
    # Retrying the same cleanup does not touch the original or schedule work.
    assert embedding_reset.clear_chunks(7)['cleared']
    assert f.original.exists()


@pytest.mark.parametrize(('document_type', 'empty_only', 'expected_status'), [
    ('FOLDER', True, 200), ('PDF', True, 409), ('', True, 409), (None, True, 409),
    ('PDF', False, 200),
])
def test_empty_only_endpoint_ignores_folders_but_protects_documents(
        reset_index, monkeypatch, document_type, empty_only, expected_status):
    from fastapi import FastAPI
    from fastapi.testclient import TestClient
    from lazymind.chat.api.base_ingestion_routes import router

    f = reset_index
    with f.core.begin() as conn:
        conn.execute(text('UPDATE documents SET deleted_at=CURRENT_TIMESTAMP'))
        conn.execute(text("""INSERT INTO documents (id, dataset_id, document_type)
            VALUES ('folder', 'indexed', 'FOLDER')"""))
        conn.execute(text("""INSERT INTO documents (id, dataset_id, document_type, p_id)
            VALUES ('nested', 'indexed', :document_type, 'folder')"""), {'document_type': document_type})
    with f.core.connect() as conn:
        documents_before = conn.execute(text('SELECT * FROM documents ORDER BY id')).mappings().all()
        states_before = conn.execute(text(
            'SELECT * FROM document_processing_states ORDER BY document_id')).mappings().all()
    monkeypatch.setitem(config._impl, 'core_internal_token', 'internal-test-token')
    app = FastAPI()
    app.include_router(router)
    with TestClient(app) as client:
        response = client.post('/internal/knowledge:clear-chunks',
                               headers={'X-LazyMind-Internal-Token': 'internal-test-token'},
                               json={'generation': 7, 'empty_only': empty_only})
    assert response.status_code == expected_status, response.text
    with f.core.connect() as conn:
        assert conn.execute(text('SELECT * FROM documents ORDER BY id')).mappings().all() == documents_before
        states_after = conn.execute(text(
            'SELECT * FROM document_processing_states ORDER BY document_id')).mappings().all()
    if expected_status == 200:
        assert response.json() == {'cleared': True, 'generation': 7}
        assert 'col_block' in f.deleted
        assert not f.artifact.exists()
        assert all(state['embedding_version'] == 'reembed:manual' for state in states_after)
        with f.tasks.connect() as conn:
            assert conn.execute(text('SELECT COUNT(*) FROM lazyllm_waiting_task_queue')).scalar_one() == 0
    else:
        assert response.json() == {'detail': {'code': 'INDEX_BUSY'}}
        assert not f.deleted
        assert f.artifact.exists()
        assert states_after == states_before
        with sqlite3.connect(f.segment_path) as conn:
            assert conn.execute('SELECT uid FROM col_block').fetchall() == [('old-segment',)]
    assert f.original.read_text(encoding='utf-8') == 'keep the source'


@pytest.mark.parametrize('reason', ['stale_generation', 'queued_task', 'callback_pending', 'new_document'])
def test_clear_rechecks_conditions_before_deleting(reset_index, reason):
    f = reset_index
    if reason in ('queued_task', 'callback_pending'):
        table = 'lazyllm_waiting_task_queue' if reason == 'queued_task' else 'lazyllm_finished_task_queue'
        with f.tasks.begin() as conn:
            conn.execute(text(f"INSERT INTO {table} VALUES ('pending')"))
    with pytest.raises(embedding_reset.IndexBusy):
        embedding_reset.clear_chunks(6 if reason == 'stale_generation' else 7, empty_only=reason == 'new_document')
    assert not f.deleted
    assert f.artifact.exists()
    assert f.original.exists()


def test_failed_store_cleanup_never_marks_documents_ready(reset_index):
    reset_index.fail.append(True)
    with pytest.raises(RuntimeError, match='vector service unavailable'):
        embedding_reset.clear_chunks(7)
    assert reset_index.artifact.exists()
    assert reset_index.original.exists()
    with reset_index.core.connect() as conn:
        assert conn.execute(text('SELECT phase FROM embedding_index_states')).scalar_one() == 'clearing'
    with pytest.raises(embedding_reset.IndexBusy):
        with embedding_reset.index_operation():
            pytest.fail('partial cleanup allowed access')


def test_worker_rejects_late_submission_using_queued_llm_config(monkeypatch):
    monkeypatch.setitem(config._impl, 'database_url', 'sqlite:///:memory:')
    module = importlib.import_module('lazymind.processor.service.worker')
    monkeypatch.setattr(embedding_reset, 'index_lock', nullcontext)
    monkeypatch.setattr(embedding_reset, 'index_state', lambda: {'generation': 8, 'phase': 'ready'})
    base = module.DocumentProcessorWorker._Impl.__mro__[1]
    monkeypatch.setattr(base, '_run_task', lambda self, *args: self._get_processor())
    worker = object.__new__(module.DocumentProcessorWorker._Impl)
    with pytest.raises(embedding_reset.IndexBusy):
        worker._run_task('old-task', 'doc_add', {'llm_config': {'_embedding_generation': 7}}, True)

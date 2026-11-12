import pytest
from sqlalchemy import create_engine, text
from sqlalchemy.exc import DBAPIError

from lazymind.parsing.service import embedding_maintenance


class FakeMilvusClient:
    def __init__(self, schemas):
        self.schemas = schemas
        self.dropped = []
        self.database = None
        self.closed = False
        self.fail_on_drop = None

    def using_database(self, name):
        self.database = name

    def has_collection(self, name):
        return name in self.schemas

    def describe_collection(self, collection_name):
        return {'fields': [{'name': name} for name in self.schemas[collection_name]]}

    def drop_collection(self, collection_name):
        if collection_name == self.fail_on_drop:
            raise RuntimeError('Milvus unavailable')
        self.dropped.append(collection_name)
        del self.schemas[collection_name]

    def close(self):
        self.closed = True


def _config():
    return {'vector_store': {'type': 'milvus', 'kwargs': {'uri': 'http://milvus.test', 'db_name': 'lazyllm'}},
            'segment_store': {'type': 'SQLiteStore', 'kwargs': {'db_path': '/must/not/be/touched'}}}


def test_clear_text_vectors_drops_only_text_collections_and_preserves_image_and_segments(monkeypatch):
    names = embedding_maintenance.text_collection_names()
    schemas = {name: {'uid', 'embedding_embed_main'} for name in names}
    schemas['col_image'] = {'uid', 'embedding_embed_image'}
    client = FakeMilvusClient(schemas)
    monkeypatch.setenv('LAZYMIND_EMBEDDING_MAINTENANCE_MODE', 'true')

    cleared = embedding_maintenance.clear_text_vectors(
        writers_stopped=True, store_config=_config(), client_factory=lambda **kwargs: client)

    assert cleared == list(names)
    assert set(client.dropped) == set(names)
    assert 'col_image' in schemas
    assert client.database == 'lazyllm'
    assert client.closed


def test_clear_text_vectors_rejects_mixed_embedding_collection_before_any_drop(monkeypatch):
    names = embedding_maintenance.text_collection_names()
    schemas = {name: {'uid', 'embedding_embed_main'} for name in names}
    schemas[names[1]].add('embedding_embed_image')
    client = FakeMilvusClient(schemas)
    monkeypatch.setenv('LAZYMIND_EMBEDDING_MAINTENANCE_MODE', 'true')

    with pytest.raises(RuntimeError, match='mixed embedding'):
        embedding_maintenance.clear_text_vectors(
            writers_stopped=True, store_config=_config(), client_factory=lambda **kwargs: client)

    assert client.dropped == []
    assert client.closed


def test_clear_text_vectors_refuses_without_explicit_offline_marker():
    client = FakeMilvusClient({})
    with pytest.raises(RuntimeError, match='maintenance mode'):
        embedding_maintenance.clear_text_vectors(store_config=_config(), client_factory=lambda **kwargs: client)
    assert client.dropped == []


def test_clear_text_vectors_refuses_without_writers_stopped_attestation(monkeypatch):
    monkeypatch.setenv('LAZYMIND_EMBEDDING_MAINTENANCE_MODE', 'true')
    client = FakeMilvusClient({})
    with pytest.raises(RuntimeError, match='writers must be stopped'):
        embedding_maintenance.clear_text_vectors(
            store_config=_config(), client_factory=lambda **kwargs: client)
    assert client.dropped == []


def test_text_collection_name_matches_lazyllm_normalization():
    assert embedding_maintenance.text_collection_names() == (
        'col_block', 'col_line', 'col_code', 'col_doc_summary_6f95dd387857',
    )


def test_offline_database_engine_uses_available_postgres_driver():
    engine = embedding_maintenance._database_engine(
        'postgresql://user:pass@localhost/core', 'core', required=True)
    try:
        assert engine.url.drivername == 'postgresql+psycopg'
    finally:
        engine.dispose()


def _core_engine():
    engine = create_engine('sqlite:///:memory:')
    with engine.begin() as conn:
        conn.execute(text('''CREATE TABLE embedding_index_states (
            scope TEXT PRIMARY KEY, active_model_id TEXT NOT NULL DEFAULT '',
            active_source TEXT NOT NULL DEFAULT '', active_model_name TEXT NOT NULL DEFAULT '',
            active_provider_name TEXT NOT NULL DEFAULT '', signature TEXT NOT NULL DEFAULT '',
            phase TEXT NOT NULL DEFAULT 'ready', generation BIGINT NOT NULL DEFAULT 1,
            target_model_id TEXT NOT NULL DEFAULT '', target_source TEXT NOT NULL DEFAULT '',
            target_signature TEXT NOT NULL DEFAULT '', updated_at TIMESTAMP NOT NULL)'''))
        conn.execute(text('''CREATE TABLE datasets (
            id TEXT PRIMARY KEY, processing_level TEXT NOT NULL, deleted_at TIMESTAMP)'''))
        conn.execute(text('''CREATE TABLE documents (
            id TEXT PRIMARY KEY, dataset_id TEXT NOT NULL, deleted_at TIMESTAMP)'''))
        conn.execute(text('''CREATE TABLE document_processing_states (
            dataset_id TEXT NOT NULL, document_id TEXT NOT NULL,
            parse_status TEXT NOT NULL DEFAULT 'pending',
            chunk_status TEXT NOT NULL DEFAULT 'pending',
            index_status TEXT NOT NULL DEFAULT 'pending',
            index_error_code TEXT NOT NULL DEFAULT '',
            index_error_message TEXT NOT NULL DEFAULT '',
            parse_artifact_ref TEXT NOT NULL DEFAULT '',
            chunk_artifact_ref TEXT NOT NULL DEFAULT '',
            index_artifact_ref TEXT NOT NULL DEFAULT '',
            index_fingerprint TEXT NOT NULL DEFAULT '',
            embedding_version TEXT NOT NULL DEFAULT '',
            revision BIGINT NOT NULL DEFAULT 1,
            updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
            PRIMARY KEY(dataset_id, document_id))'''))
        conn.execute(text('''INSERT INTO embedding_index_states (
            scope, active_model_id, active_source, active_model_name,
            active_provider_name, signature, phase, generation,
            target_model_id, target_source, target_signature, updated_at)
            VALUES ('global', 'model-a', 'user', 'Vector A', 'Vendor', 'sig-a',
                    'ready', 7, 'model-b', 'user', 'sig-b', CURRENT_TIMESTAMP)'''))
        for dataset_id, level, deleted in (
                ('indexed', 'indexed', None), ('draft', 'parsed', None),
                ('deleted', 'indexed', '2026-01-01')):
            conn.execute(text('''INSERT INTO datasets (id, processing_level, deleted_at)
                VALUES (:dataset_id, :level, :deleted)'''),
                         {'dataset_id': dataset_id, 'level': level, 'deleted': deleted})
            conn.execute(text('''INSERT INTO documents (id, dataset_id, deleted_at)
                VALUES (:document_id, :dataset_id, NULL)'''),
                         {'dataset_id': dataset_id, 'document_id': f'doc-{dataset_id}'})
            conn.execute(text('''INSERT INTO document_processing_states (
                dataset_id, document_id, parse_status, chunk_status, index_status,
                parse_artifact_ref, chunk_artifact_ref, index_artifact_ref,
                index_fingerprint, embedding_version, updated_at)
                VALUES (:dataset_id, :document_id, 'succeeded', 'succeeded', 'succeeded',
                        'parse-ref', 'chunk-ref', 'index-ref', 'old-fingerprint',
                        'old-version', CURRENT_TIMESTAMP)'''),
                         {'dataset_id': dataset_id, 'document_id': f'doc-{dataset_id}'})
        conn.execute(text("INSERT INTO documents (id, dataset_id) VALUES ('legacy-doc', 'indexed')"))
        conn.execute(text(
            'INSERT INTO documents (id, dataset_id, deleted_at) '
            "VALUES ('removed-doc', 'indexed', '2026-01-01')"))
    return engine


def _task_engine(*, queued=False, finished=False):
    engine = create_engine('sqlite:///:memory:')
    with engine.begin() as conn:
        conn.execute(text('CREATE TABLE lazyllm_waiting_task_queue (id INTEGER PRIMARY KEY, status TEXT)'))
        conn.execute(text('CREATE TABLE lazyllm_finished_task_queue (id INTEGER PRIMARY KEY)'))
        if queued:
            conn.execute(text("INSERT INTO lazyllm_waiting_task_queue (id,status) VALUES (1,'WORKING')"))
        if finished:
            conn.execute(text('INSERT INTO lazyllm_finished_task_queue (id) VALUES (1)'))
    return engine


def _row(engine, table, where):
    with engine.connect() as conn:
        return conn.execute(text(f'SELECT * FROM {table} WHERE {where}')).mappings().one()


@pytest.mark.parametrize('phase', ['ready', 'detached'])
def test_offline_handoff_preserves_parse_chunks_and_image_vectors(monkeypatch, phase):
    monkeypatch.setenv('LAZYMIND_EMBEDDING_MAINTENANCE_MODE', 'true')
    names = embedding_maintenance.text_collection_names()
    schemas = {name: {'uid', 'embedding_embed_main'} for name in names}
    schemas['col_image'] = {'uid', 'embedding_embed_image'}
    client = FakeMilvusClient(schemas)
    core, tasks = _core_engine(), _task_engine()

    with core.begin() as conn:
        conn.execute(text("UPDATE embedding_index_states SET phase=:phase"), {'phase': phase})

    result = embedding_maintenance.clear_text_vectors_with_core_handoff(
        writers_stopped=True, core_engine=core, task_engine=tasks,
        store_config=_config(), client_factory=lambda **kwargs: client)

    assert result['dropped_collections'] == list(names)
    assert result['phase'] == 'cleared'
    state = _row(core, 'embedding_index_states', "scope='global'")
    for field in ('active_model_id', 'active_source', 'active_model_name',
                  'active_provider_name', 'signature', 'target_model_id',
                  'target_source', 'target_signature'):
        assert state[field] == ''
    assert state['phase'] == 'cleared'
    assert state['generation'] == 8
    indexed = _row(core, 'document_processing_states', "document_id='doc-indexed'")
    assert indexed['index_status'] == 'pending'
    assert indexed['index_artifact_ref'] == ''
    assert indexed['index_fingerprint'] == ''
    assert indexed['embedding_version'] == 'reembed:pending'
    assert indexed['parse_status'] == indexed['chunk_status'] == 'succeeded'
    assert indexed['parse_artifact_ref'] == 'parse-ref'
    assert indexed['chunk_artifact_ref'] == 'chunk-ref'
    legacy = _row(core, 'document_processing_states', "document_id='legacy-doc'")
    assert legacy['index_status'] == 'pending'
    assert legacy['embedding_version'] == 'reembed:pending'
    with core.connect() as conn:
        count = conn.execute(text(
            "SELECT COUNT(*) FROM document_processing_states WHERE document_id='removed-doc'"
        )).scalar_one()
        assert count == 0
    assert _row(core, 'document_processing_states', "dataset_id='draft'")['index_status'] == 'pending'
    assert _row(core, 'document_processing_states', "dataset_id='draft'")['embedding_version'] == 'reembed:pending'
    assert _row(core, 'document_processing_states', "dataset_id='deleted'")['index_status'] == 'succeeded'
    assert 'col_image' in schemas


def test_offline_handoff_refuses_running_writer_or_queued_task(monkeypatch):
    monkeypatch.setenv('LAZYMIND_EMBEDDING_MAINTENANCE_MODE', 'true')
    names = embedding_maintenance.text_collection_names()
    client = FakeMilvusClient({names[0]: {'uid', 'embedding_embed_main'}})
    core, tasks = _core_engine(), _task_engine(queued=True)

    with pytest.raises(RuntimeError, match='writers must be stopped'):
        embedding_maintenance.clear_text_vectors_with_core_handoff(
            writers_stopped=False, core_engine=core, task_engine=tasks,
            store_config=_config(), client_factory=lambda **kwargs: client)
    with pytest.raises(RuntimeError, match='waiting task queue'):
        embedding_maintenance.clear_text_vectors_with_core_handoff(
            writers_stopped=True, core_engine=core, task_engine=tasks,
            store_config=_config(), client_factory=lambda **kwargs: client)
    assert client.dropped == []
    assert _row(core, 'embedding_index_states', "scope='global'")['phase'] == 'ready'


def test_offline_handoff_refuses_unprocessed_callback(monkeypatch):
    monkeypatch.setenv('LAZYMIND_EMBEDDING_MAINTENANCE_MODE', 'true')
    name = embedding_maintenance.text_collection_names()[0]
    client = FakeMilvusClient({name: {'uid', 'embedding_embed_main'}})
    core, tasks = _core_engine(), _task_engine(finished=True)

    with pytest.raises(RuntimeError, match='finished task queue'):
        embedding_maintenance.clear_text_vectors_with_core_handoff(
            writers_stopped=True, core_engine=core, task_engine=tasks,
            store_config=_config(), client_factory=lambda **kwargs: client)
    assert name in client.schemas
    assert _row(core, 'embedding_index_states', "scope='global'")['phase'] == 'ready'


def test_offline_handoff_recovers_after_partial_milvus_failure(monkeypatch):
    monkeypatch.setenv('LAZYMIND_EMBEDDING_MAINTENANCE_MODE', 'true')
    names = embedding_maintenance.text_collection_names()
    client = FakeMilvusClient({name: {'uid', 'embedding_embed_main'} for name in names})
    client.fail_on_drop = names[1]
    core, tasks = _core_engine(), _task_engine()

    with pytest.raises(RuntimeError, match='Milvus unavailable'):
        embedding_maintenance.clear_text_vectors_with_core_handoff(
            writers_stopped=True, core_engine=core, task_engine=tasks,
            store_config=_config(), client_factory=lambda **kwargs: client)
    assert _row(core, 'embedding_index_states', "scope='global'")['phase'] == 'clearing'
    assert _row(core, 'embedding_index_states', "scope='global'")['generation'] == 7

    client.fail_on_drop = None
    result = embedding_maintenance.clear_text_vectors_with_core_handoff(
        writers_stopped=True, core_engine=core, task_engine=tasks,
        store_config=_config(), client_factory=lambda **kwargs: client)
    assert result['dropped_collections'] == list(names[1:])
    assert _row(core, 'embedding_index_states', "scope='global'")['generation'] == 8
    assert _row(core, 'embedding_index_states', "scope='global'")['phase'] == 'cleared'

    again = embedding_maintenance.clear_text_vectors_with_core_handoff(
        writers_stopped=True, core_engine=core, task_engine=tasks,
        store_config=_config(), client_factory=lambda **kwargs: client)
    assert again['dropped_collections'] == []
    assert _row(core, 'embedding_index_states', "scope='global'")['generation'] == 8


def test_offline_handoff_recovers_when_core_commit_fails_after_drop(monkeypatch):
    monkeypatch.setenv('LAZYMIND_EMBEDDING_MAINTENANCE_MODE', 'true')
    name = embedding_maintenance.text_collection_names()[0]
    client = FakeMilvusClient({name: {'uid', 'embedding_embed_main'}})
    core, tasks = _core_engine(), _task_engine()
    with core.begin() as conn:
        conn.execute(text('''CREATE TRIGGER fail_finalization
            BEFORE UPDATE ON embedding_index_states
            WHEN NEW.phase='cleared'
            BEGIN SELECT RAISE(ABORT, 'finalization blocked'); END'''))

    with pytest.raises(DBAPIError, match='finalization blocked'):
        embedding_maintenance.clear_text_vectors_with_core_handoff(
            writers_stopped=True, core_engine=core, task_engine=tasks,
            store_config=_config(), client_factory=lambda **kwargs: client)
    assert name not in client.schemas
    assert _row(core, 'embedding_index_states', "scope='global'")['phase'] == 'clearing'
    assert _row(core, 'embedding_index_states', "scope='global'")['generation'] == 7
    assert _row(core, 'document_processing_states', "document_id='doc-indexed'")['index_status'] == 'succeeded'

    with core.begin() as conn:
        conn.execute(text('DROP TRIGGER fail_finalization'))
    result = embedding_maintenance.clear_text_vectors_with_core_handoff(
        writers_stopped=True, core_engine=core, task_engine=tasks,
        store_config=_config(), client_factory=lambda **kwargs: client)
    assert result['dropped_collections'] == []
    assert _row(core, 'embedding_index_states', "scope='global'")['phase'] == 'cleared'
    assert _row(core, 'embedding_index_states', "scope='global'")['generation'] == 8
    assert _row(core, 'document_processing_states', "document_id='doc-indexed'")['index_status'] == 'pending'


def test_offline_handoff_creates_missing_core_state_row(monkeypatch):
    monkeypatch.setenv('LAZYMIND_EMBEDDING_MAINTENANCE_MODE', 'true')
    client = FakeMilvusClient({})
    core, tasks = _core_engine(), _task_engine()
    with core.begin() as conn:
        conn.execute(text("DELETE FROM embedding_index_states WHERE scope='global'"))

    embedding_maintenance.clear_text_vectors_with_core_handoff(
        writers_stopped=True, core_engine=core, task_engine=tasks,
        store_config=_config(), client_factory=lambda **kwargs: client)
    state = _row(core, 'embedding_index_states', "scope='global'")
    assert state['phase'] == 'cleared'
    assert state['generation'] == 2


def test_offline_handoff_rejects_vectors_reappearing_after_cleared_phase(monkeypatch):
    monkeypatch.setenv('LAZYMIND_EMBEDDING_MAINTENANCE_MODE', 'true')
    name = embedding_maintenance.text_collection_names()[0]
    client = FakeMilvusClient({name: {'uid', 'embedding_embed_main'}})
    core, tasks = _core_engine(), _task_engine()
    with core.begin() as conn:
        conn.execute(text("UPDATE embedding_index_states SET phase='cleared' WHERE scope='global'"))

    with pytest.raises(RuntimeError, match='text vectors still exist'):
        embedding_maintenance.clear_text_vectors_with_core_handoff(
            writers_stopped=True, core_engine=core, task_engine=tasks,
            store_config=_config(), client_factory=lambda **kwargs: client)
    assert name in client.schemas
    assert _row(core, 'embedding_index_states', "scope='global'")['generation'] == 7

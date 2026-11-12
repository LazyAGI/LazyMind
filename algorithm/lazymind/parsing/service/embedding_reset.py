"""Online text-vector reset; originals, parsed chunks and image vectors survive."""
from contextlib import contextmanager
from functools import lru_cache
from pathlib import Path
import os

from sqlalchemy import text
from sqlalchemy.pool import NullPool

from lazymind.config import config
from lazymind.common.database.postgres import (
    create_database_engine, postgres_dsn_to_sqlalchemy_url, sqlalchemy_engine_options,
)


class IndexBusy(RuntimeError):
    pass


@lru_cache(maxsize=4)
def _engine(url):
    url = url.strip()
    if url.startswith(('sqlite:', 'sqliteproxy:')):
        return create_database_engine(url, **sqlalchemy_engine_options(url))
    # Readers hold an advisory-lock connection while querying state separately.
    # A bounded pool would deadlock when all its connections hold reader locks.
    return create_database_engine(postgres_dsn_to_sqlalchemy_url(url, driver='psycopg'), poolclass=NullPool)


def index_state():
    url = config['core_database_url']
    if not url:
        return {'generation': 0, 'phase': 'ready'}
    with _engine(url).connect() as conn:
        row = conn.execute(text("SELECT generation, phase FROM embedding_index_states WHERE scope='global'"))
        return dict(row.mappings().first() or {'generation': 0, 'phase': 'ready'})


@contextmanager
def index_lock(*, exclusive=False):
    """Fence in-flight readers/writers, across processes and cloud replicas.

    PostgreSQL advisory locks do not block ordinary queue transactions. Desktop
    SQLite deployments share the upload directory and use an OS file lock.
    """
    url = config['core_database_url']
    if not url:  # Legacy deployments have no online reset endpoint available.
        if exclusive:
            raise RuntimeError('Core database is required')
        yield
        return
    engine = _engine(url)
    if engine.dialect.name == 'postgresql':
        suffix = '' if exclusive else '_shared'
        with engine.connect() as conn:
            acquired = conn.execute(text('SELECT pg_try_advisory_lock(781631953)' if exclusive
                                         else 'SELECT pg_advisory_lock_shared(781631953)')).scalar_one()
            if exclusive and not acquired:
                raise IndexBusy('knowledge index is in use')
            try:
                yield
            finally:
                conn.execute(text(f'SELECT pg_advisory_unlock{suffix}(781631953)'))
    else:
        path = Path(config['shared_upload_dir']) / '.embedding-index.lock'
        path.parent.mkdir(parents=True, exist_ok=True)
        if os.name == 'nt':
            from filelock import FileLock, Timeout
            try:
                with FileLock(str(path), timeout=0 if exclusive else -1):
                    yield
            except Timeout as exc:
                raise IndexBusy('knowledge index is in use') from exc
        else:
            import fcntl
            with path.open('a') as handle:
                try:
                    fcntl.flock(handle, (fcntl.LOCK_EX | fcntl.LOCK_NB) if exclusive else fcntl.LOCK_SH)
                except BlockingIOError as exc:
                    raise IndexBusy('knowledge index is in use') from exc
                try:
                    yield
                finally:
                    fcntl.flock(handle, fcntl.LOCK_UN)


@contextmanager
def index_operation(expected_generation=None):
    with index_lock():
        state = index_state()
        if state['phase'] in ('clearing', 'cleanup_failed', 'cleared', 'detached'):
            raise IndexBusy('knowledge index is being cleared')
        if expected_generation is not None and expected_generation != state['generation']:
            raise IndexBusy('document task belongs to an earlier index generation')
        yield state['generation']


def clear_chunks(generation, empty_only=False):
    from lazymind.parsing.service.embedding_maintenance import _database_engine, _check_task_queues
    # Keep the fence until Core metadata is reset too. Core owns model probing,
    # user confirmation, target selection and the clearing generation.
    with index_lock(exclusive=True):
        state = index_state()
        if state != {'generation': generation, 'phase': 'clearing'}:
            raise IndexBusy('index generation changed')
        task_engine = _database_engine(config['database_url'], 'document database', required=True)
        try:
            try:
                _check_task_queues(task_engine)
            except RuntimeError as exc:
                raise IndexBusy('document tasks are still running') from exc
        finally:
            task_engine.dispose()
        if empty_only:
            with _engine(config['core_database_url']).connect() as conn:
                count = conn.execute(text("""SELECT COUNT(*) FROM documents d JOIN datasets ds ON ds.id=d.dataset_id
                    LEFT JOIN document_processing_states s ON s.dataset_id=d.dataset_id AND s.document_id=d.id
                    WHERE d.deleted_at IS NULL AND ds.deleted_at IS NULL
                      AND (d.document_type IS NULL OR d.document_type <> 'FOLDER')
                      AND (ds.processing_level NOT IN ('stored','parsed','chunked') OR s.index_status='succeeded')
                    """)).scalar_one()
                if count:
                    raise IndexBusy('knowledge documents changed; confirmation is required')
        from lazyllm.tools.rag.store import MilvusStore
        from lazyllm.tools.rag.store.document_store import _DocumentStore
        from lazymind.parsing.service.build_document import _build_store_config
        from lazymind.config import EMBED_INDEX_KWARGS
        conf = _build_store_config(EMBED_INDEX_KWARGS)
        vector = dict(conf['vector_store']['kwargs'])
        vector.pop('index_kwargs', None)
        stores = [MilvusStore(**vector)]
        for store in stores:
            store.connect()
        groups = ('block', 'line', 'code', 'doc-summary')
        for store in stores:
            for group in groups:
                name = _DocumentStore._normalize_collection_raw_name(f'col_{group}'.lower())
                if not store.delete(name):
                    raise RuntimeError('chunk cleanup failed')
        # Chunk and parse artifacts do not depend on the embedding identity.
        # Retain them so manual reindex can reuse content without OCR again.
        engine = _engine(config['core_database_url'])
        with engine.begin() as conn:
            conn.execute(text("""INSERT INTO document_processing_states (dataset_id, document_id, updated_at)
                SELECT d.dataset_id, d.id, CURRENT_TIMESTAMP FROM documents d JOIN datasets ds ON ds.id=d.dataset_id
                WHERE d.deleted_at IS NULL AND ds.deleted_at IS NULL
                AND (d.document_type IS NULL OR d.document_type <> 'FOLDER')
                AND ds.processing_level NOT IN ('stored','parsed','chunked') AND NOT EXISTS (
                SELECT 1 FROM document_processing_states s WHERE s.document_id=d.id AND s.dataset_id=d.dataset_id)"""))
            conn.execute(text("""UPDATE document_processing_states SET
                index_status='pending', index_error_code='', index_error_message='',
                index_artifact_ref='', index_fingerprint='',
                embedding_version='reembed:manual', revision=revision+1, updated_at=CURRENT_TIMESTAMP
                WHERE EXISTS (SELECT 1 FROM documents d JOIN datasets ds ON ds.id=d.dataset_id
                    WHERE d.id=document_processing_states.document_id
                      AND d.dataset_id=document_processing_states.dataset_id
                      AND d.deleted_at IS NULL AND ds.deleted_at IS NULL
                      AND (d.document_type IS NULL OR d.document_type <> 'FOLDER')
                      AND (ds.processing_level NOT IN ('stored','parsed','chunked')
                           OR document_processing_states.index_status='succeeded'))"""))
        return {'cleared': True, 'generation': generation}

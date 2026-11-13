"""Offline maintenance for the shared text-vector space.

This command must run only after the Core API, parser, document workers,
algorithm search, and all other application services have stopped. Keep only
the databases and Milvus running. The maintenance marker prevents accidental
calls, but it cannot itself prove that another process has stopped writing.
No HTTP route invokes this module.

Dry run::

    python -m lazymind.parsing.service.embedding_maintenance

Apply after stopping all writers/readers::

    LAZYMIND_EMBEDDING_MAINTENANCE_MODE=true python -m \
      lazymind.parsing.service.embedding_maintenance --apply \
      --writers-stopped \
      --confirm 'CLEAR TEXT VECTORS'

The apply path requires LAZYMIND_CORE_DATABASE_URL. It checks the LazyLLM
waiting and finished-callback queues when LAZYMIND_DATABASE_URL is configured.
Queue emptiness is only a diagnostic; the operator must stop all services
externally.
"""

from __future__ import annotations

import argparse
import json
import os
from collections.abc import Callable
from urllib.parse import urlparse

from sqlalchemy import inspect, text
from sqlalchemy.engine import Engine

from lazyllm.tools.rag.store.document_store import _DocumentStore
from lazyllm.tools.rag.store.store_base import EMBED_PREFIX

from lazymind.config import EMBED_INDEX_KWARGS, EMBED_MAIN, config
from lazymind.common.database.postgres import (create_database_engine, postgres_dsn_to_sqlalchemy_url,
                                               sqlalchemy_engine_options)

TEXT_VECTOR_GROUPS = ('block', 'line', 'code', 'doc-summary')
_REMOTE_SCHEMES = frozenset({'unix', 'http', 'https', 'tcp', 'grpc'})
_MAINTENANCE_MARKER = 'LAZYMIND_EMBEDDING_MAINTENANCE_MODE'
_GLOBAL_SCOPE = 'global'
_CORE_COLUMNS = {
    'embedding_index_states': {'scope', 'active_model_id', 'active_source', 'active_model_name',
                               'active_provider_name', 'signature', 'phase', 'generation',
                               'target_model_id', 'target_source', 'target_signature', 'updated_at'},
    'datasets': {'id', 'processing_level', 'deleted_at'},
    'documents': {'id', 'dataset_id', 'deleted_at'},
    'document_processing_states': {'dataset_id', 'document_id', 'index_status', 'index_error_code',
                                   'index_error_message', 'index_artifact_ref', 'index_fingerprint',
                                   'embedding_version', 'revision', 'updated_at'},
}


def text_collection_names() -> tuple[str, ...]:
    """Use the exact collection-name normalization used by LazyLLM."""
    return tuple(_DocumentStore._normalize_collection_raw_name(f'col_{group}'.lower())
                 for group in TEXT_VECTOR_GROUPS)


def _store_config():
    # Import lazily so a dry-run import does not initialize the document server.
    from lazymind.parsing.service.build_document import _build_store_config
    return _build_store_config(EMBED_INDEX_KWARGS)


def _open_vector_client(store_config: dict, client_factory: Callable | None):
    vector = store_config.get('vector_store') or {}
    if str(vector.get('type') or '').lower() != 'milvus':
        raise RuntimeError('text-vector maintenance requires a separate Milvus vector store')
    kwargs = vector.get('kwargs') or {}
    uri = str(kwargs.get('uri') or '').strip()
    if not uri:
        raise RuntimeError('Milvus URI is required')
    client_kwargs = dict(kwargs.get('client_kwargs') or {})
    client_kwargs.pop('max_pool_size', None)
    if client_factory is None:
        from lazyllm.thirdparty import pymilvus
        client_factory = pymilvus.MilvusClient
    client = client_factory(uri=uri, **client_kwargs)
    db_name = kwargs.get('db_name', 'lazyllm')
    if urlparse(uri).scheme.lower() in _REMOTE_SCHEMES and db_name:
        try:
            client.using_database(db_name)
        except Exception:
            client.close()
            raise
    return client


def _inspect_client(client) -> tuple[str, ...]:
    existing = []
    for name in text_collection_names():
        if not client.has_collection(name):
            continue
        description = client.describe_collection(collection_name=name)
        fields = description.get('fields') or []
        embedding_keys = {
            str(field.get('name') or '')[len(EMBED_PREFIX):]
            for field in fields if str(field.get('name') or '').startswith(EMBED_PREFIX)
        }
        if embedding_keys != {EMBED_MAIN}:
            raise RuntimeError(f'mixed embedding or unknown vector schema in collection {name}')
        existing.append(name)
    return tuple(existing)


def inspect_text_vectors(*, store_config: dict | None = None,
                         client_factory: Callable | None = None) -> tuple[str, ...]:
    """Return existing text-only Milvus collections after validating all schemas."""
    client = _open_vector_client(store_config or _store_config(), client_factory)
    try:
        return _inspect_client(client)
    finally:
        client.close()


def clear_text_vectors(*, writers_stopped: bool = False, store_config: dict | None = None,
                       client_factory: Callable | None = None) -> list[str]:
    """Drop text vector collections; preserve all source, chunks and image vectors.

    The caller must stop all application services before invoking this method.
    A partial Milvus failure raises so the maintenance window cannot be treated
    as complete. The operation is idempotent and can be retried offline.
    """
    _require_maintenance_mode()
    if not writers_stopped:
        raise RuntimeError('all writers must be stopped before offline embedding maintenance')
    client = _open_vector_client(store_config or _store_config(), client_factory)
    try:
        names = _inspect_client(client)  # validate every target before any drop
        for name in names:
            client.drop_collection(collection_name=name)
        return list(names)
    finally:
        client.close()


def _require_maintenance_mode() -> None:
    if os.getenv(_MAINTENANCE_MARKER, '').strip().lower() not in {'1', 'true', 'yes'}:
        raise RuntimeError('embedding maintenance mode is required')


def _database_engine(raw_url: str | None, label: str, *, required: bool) -> Engine | None:
    url = str(raw_url or '').strip()
    if not url:
        if required:
            raise RuntimeError(f'{label} is required for offline embedding maintenance')
        return None
    if not url.startswith(('sqlite:', 'sqliteproxy:')):
        url = postgres_dsn_to_sqlalchemy_url(url, driver='psycopg')
    return create_database_engine(url, **sqlalchemy_engine_options(url))


def _check_core_schema(core_engine: Engine) -> None:
    schema = inspect(core_engine)
    for table, columns in _CORE_COLUMNS.items():
        if not schema.has_table(table):
            raise RuntimeError(f'Core database table {table} is required before vector clearing')
        present = {column['name'] for column in schema.get_columns(table)}
        missing = columns - present
        if missing:
            raise RuntimeError(f'Core database table {table} lacks columns: {sorted(missing)}')


def _check_task_queues(task_engine: Engine | None) -> dict[str, str]:
    if task_engine is None:
        return {'waiting': 'not_configured', 'finished': 'not_configured'}
    schema = inspect(task_engine)
    status = {}
    for kind, table in (('waiting', 'lazyllm_waiting_task_queue'),
                        ('finished', 'lazyllm_finished_task_queue')):
        if not schema.has_table(table):
            status[kind] = 'table_absent'
            continue
        with task_engine.connect() as connection:
            count = connection.execute(text(f'SELECT COUNT(*) FROM {table}')).scalar_one()
        if count:
            raise RuntimeError(f'LazyLLM {kind} task queue has {count} rows; drain tasks before clearing')
        status[kind] = 'empty'
    return status


def _core_phase(core_engine: Engine) -> str | None:
    with core_engine.connect() as connection:
        return connection.execute(text('SELECT phase FROM embedding_index_states WHERE scope=:scope'),
                                  {'scope': _GLOBAL_SCOPE}).scalar_one_or_none()


def _mark_clearing(core_engine: Engine) -> None:
    with core_engine.begin() as connection:
        phase = connection.execute(text('SELECT phase FROM embedding_index_states WHERE scope=:scope'),
                                   {'scope': _GLOBAL_SCOPE}).scalar_one_or_none()
        if phase is None:
            connection.execute(text('''INSERT INTO embedding_index_states
                (scope, phase, generation, updated_at)
                VALUES (:scope, 'clearing', 1, CURRENT_TIMESTAMP)'''), {'scope': _GLOBAL_SCOPE})
        else:
            connection.execute(text('''UPDATE embedding_index_states
                SET phase='clearing', updated_at=CURRENT_TIMESTAMP WHERE scope=:scope'''),
                               {'scope': _GLOBAL_SCOPE})


def _finalize_core(core_engine: Engine) -> int:
    # One transaction makes a failed handoff retriable. Milvus collections have
    # already been dropped; until this commit the state remains "clearing".
    with core_engine.begin() as connection:
        result = connection.execute(text('''UPDATE document_processing_states
            SET index_status='pending', index_error_code='', index_error_message='',
                index_artifact_ref='', index_fingerprint='',
                embedding_version=CASE WHEN embedding_version='rebuild:manual' THEN 'rebuild:manual'
                    ELSE 'reembed:pending' END,
                revision=revision+1, updated_at=CURRENT_TIMESTAMP
            WHERE EXISTS (
                SELECT 1 FROM documents d JOIN datasets ds ON ds.id=d.dataset_id
                WHERE d.dataset_id=document_processing_states.dataset_id
                  AND d.id=document_processing_states.document_id
                  AND d.deleted_at IS NULL AND ds.deleted_at IS NULL
            )'''))
        inserted = connection.execute(text('''INSERT INTO document_processing_states
            (dataset_id, document_id, index_status, index_artifact_ref,
             index_fingerprint, embedding_version, updated_at)
            SELECT d.dataset_id, d.id, 'pending', '', '', 'reembed:pending', CURRENT_TIMESTAMP
            FROM documents d JOIN datasets ds ON ds.id=d.dataset_id
            WHERE d.deleted_at IS NULL AND ds.deleted_at IS NULL
              AND ds.processing_level='indexed'
              AND NOT EXISTS (
                SELECT 1 FROM document_processing_states s
                WHERE s.dataset_id=d.dataset_id AND s.document_id=d.id
              )'''))
        finalized = connection.execute(text('''UPDATE embedding_index_states
            SET active_model_id='', active_source='', active_model_name='', active_provider_name='',
                signature='', target_model_id='', target_source='', target_signature='',
                phase='cleared', generation=generation+1, updated_at=CURRENT_TIMESTAMP
            WHERE scope=:scope AND phase='clearing' '''), {'scope': _GLOBAL_SCOPE})
        if finalized.rowcount != 1:
            raise RuntimeError('Core embedding state was not in clearing phase; handoff rolled back')
        return result.rowcount + inserted.rowcount


def clear_text_vectors_with_core_handoff(
    *, writers_stopped: bool, core_engine: Engine | None = None,
    task_engine: Engine | None = None, store_config: dict | None = None,
    client_factory: Callable | None = None,
) -> dict:
    """Complete the offline purge and Core-state handoff; retry after failures.

    ``writers_stopped`` is an operator attestation, not a distributed fence.
    Core, algorithm, parser, and document worker processes must all be stopped
    separately. The Core schema and queues are checked before any write or
    Milvus drop.
    """
    _require_maintenance_mode()
    if not writers_stopped:
        raise RuntimeError('all writers must be stopped before offline embedding maintenance')

    owned_core = core_engine is None
    owned_task = task_engine is None
    core_engine = core_engine or _database_engine(
        config['core_database_url'], 'LAZYMIND_CORE_DATABASE_URL', required=True)
    try:
        task_engine = task_engine or _database_engine(
            config['database_url'], 'LAZYMIND_DATABASE_URL', required=False)
        _check_core_schema(core_engine)
        queue_check = _check_task_queues(task_engine)
        client = _open_vector_client(store_config or _store_config(), client_factory)
        try:
            names = _inspect_client(client)
            phase = _core_phase(core_engine)
            if phase == 'cleared':
                if names:
                    raise RuntimeError('Core phase is cleared but text vectors still exist; inspect writers')
                return {'dropped_collections': [], 'phase': 'cleared',
                        'reset_document_states': 0, 'queue_check': queue_check}
            if phase not in {None, 'ready', 'clearing', 'cleanup_failed', 'detached'}:
                raise RuntimeError(f'cannot clear text vectors from Core phase {phase!r}')
            _mark_clearing(core_engine)
            for name in names:
                client.drop_collection(collection_name=name)
            reset_count = _finalize_core(core_engine)
            return {'dropped_collections': list(names), 'phase': 'cleared',
                    'reset_document_states': reset_count, 'queue_check': queue_check}
        finally:
            client.close()
    finally:
        if owned_task and task_engine is not None:
            task_engine.dispose()
        if owned_core and core_engine is not None:
            core_engine.dispose()


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description='Inspect or clear only LazyMind text vector collections offline')
    parser.add_argument('--apply', action='store_true', help='drop the listed text-vector collections')
    parser.add_argument('--writers-stopped', action='store_true',
                        help='attest Core API, parser, worker, search and all other app services are stopped')
    parser.add_argument('--confirm', default='', help='required exact phrase: CLEAR TEXT VECTORS')
    args = parser.parse_args(argv)
    if args.apply:
        if args.confirm != 'CLEAR TEXT VECTORS':
            parser.error("--apply requires --confirm 'CLEAR TEXT VECTORS'")
        result = clear_text_vectors_with_core_handoff(writers_stopped=args.writers_stopped)
    else:
        names = list(inspect_text_vectors())
        result = {'text_vector_collections': names}
    print(json.dumps({**result, 'applied': args.apply}))
    return 0


if __name__ == '__main__':
    raise SystemExit(main())

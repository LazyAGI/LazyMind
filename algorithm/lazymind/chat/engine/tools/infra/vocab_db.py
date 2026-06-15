"""PostgreSQL helpers for backend-managed vocabulary and chat tables.

Vocabulary rows are now read from ``core.public.words`` and filtered by
``deleted_at IS NULL`` so soft-deleted words are excluded from both vocab
manager reloads and vocabulary evolution planning.

Connection priority for vocab reads:

1. explicit ``db_url`` argument
2. ``LAZYMIND_CORE_DATABASE_URL``
3. ``ACL_DB_DSN``
4. ``LAZYMIND_DATABASE_URL``
"""
from __future__ import annotations

import shlex
import threading
from datetime import datetime
from typing import Any, Dict, List, Optional
from urllib.parse import urlsplit, urlunsplit

from lazyllm import LOG
from sqlalchemy import create_engine, text
from sqlalchemy.engine import URL, Engine

from lazymind.config import config as _cfg

VOCAB_SCHEMA = 'public'
VOCAB_TABLE = 'words'
VOCAB_TABLE_QUALIFIED = f'{VOCAB_SCHEMA}.{VOCAB_TABLE}'
VOCAB_REFERENCE_COLUMN = 'reference_info'
_DB_URL_ENV = 'LAZYMIND_DATABASE_URL'
_CORE_DB_DSN_ENV = 'LAZYMIND_ACL_DB_DSN'
_CORE_DB_URL_ENV = 'LAZYMIND_CORE_DATABASE_URL'
_VOCAB_DB_ENV_HINT = f'{_CORE_DB_URL_ENV}, {_CORE_DB_DSN_ENV}, or {_DB_URL_ENV}'

_table_ensured = False
_table_ensure_lock = threading.Lock()
_engine_cache: Dict[str, Engine] = {}
_engine_cache_lock = threading.Lock()


# ---------------------------------------------------------------------------
# Internal helpers
# ---------------------------------------------------------------------------

def _get_db_url() -> Optional[str]:
    value = _cfg['database_url']
    return value if value and value.strip() else None


def _ensure_postgres_driver(url: str) -> str:
    normalized = url.strip()
    parts = urlsplit(normalized)
    scheme = (parts.scheme or '').lower()
    if scheme in {'postgresql', 'postgres'}:
        return urlunsplit((f'{scheme}+psycopg2', parts.netloc, parts.path, parts.query, parts.fragment))
    return normalized


def _dsn_to_sqlalchemy_url(dsn: str) -> str:
    if '://' in dsn:
        return _ensure_postgres_driver(dsn)
    parts: Dict[str, str] = {}
    for token in shlex.split(dsn):
        if '=' not in token:
            continue
        key, value = token.split('=', 1)
        parts[key.strip()] = value.strip()
    if not parts:
        raise ValueError('invalid database dsn')
    if not (parts.get('host') or '').strip():
        raise ValueError('database host is required')
    database = (parts.get('dbname') or parts.get('database') or '').strip()
    if not database:
        raise ValueError('database name is required')
    try:
        port = int(parts['port']) if parts.get('port') else 5432
    except ValueError as exc:
        raise ValueError('invalid database port') from exc
    return URL.create(
        'postgresql+psycopg2',
        username=parts.get('user') or None,
        password=parts.get('password') or None,
        host=parts['host'],
        port=port,
        database=database,
    ).render_as_string(hide_password=False)


def _normalize_pg_url(url: Optional[str] = None, dsn: Optional[str] = None) -> str:
    if dsn and dsn.strip():
        return _dsn_to_sqlalchemy_url(dsn)
    if url and url.strip():
        return _ensure_postgres_driver(url)
    raise RuntimeError('postgres connection config is required')


def _get_engine(*, url: Optional[str] = None, dsn: Optional[str] = None) -> Engine:
    engine_url = _normalize_pg_url(url=url, dsn=dsn)
    engine = _engine_cache.get(engine_url)
    if engine is not None:
        return engine
    with _engine_cache_lock:
        engine = _engine_cache.get(engine_url)
        if engine is None:
            engine = create_engine(engine_url, future=True, pool_pre_ping=True)
            _engine_cache[engine_url] = engine
    return engine


def _get_core_db_dsn() -> Optional[str]:
    value = _cfg['acl_db_dsn']
    return value if value and value.strip() else None


def _get_core_db_url() -> Optional[str]:
    value = _cfg['core_database_url']
    return value if value and value.strip() else None


def _resolve_vocab_conn_target(db_url: Optional[str] = None) -> tuple[Optional[str], Optional[str]]:
    if db_url and db_url.strip():
        return db_url.strip(), None
    core_db_url = _get_core_db_url()
    if core_db_url:
        return core_db_url, None
    core_db_dsn = _get_core_db_dsn()
    if core_db_dsn:
        return None, core_db_dsn
    vocab_db_url = _get_db_url()
    if vocab_db_url:
        return vocab_db_url, None
    return None, None


def _has_vocab_conn_target(db_url: Optional[str] = None) -> bool:
    url, dsn = _resolve_vocab_conn_target(db_url=db_url)
    return bool(url or dsn)


def _get_vocab_conn(db_url: Optional[str] = None) -> Engine:
    url, dsn = _resolve_vocab_conn_target(db_url=db_url)
    if not (url or dsn):
        raise RuntimeError(
            f'[VocabDB] {_VOCAB_DB_ENV_HINT} is not set; cannot connect to vocab database.'
        )
    return _get_engine(url=url, dsn=dsn)


def _get_core_conn(*, db_dsn: Optional[str] = None, db_url: Optional[str] = None) -> Engine:
    return _get_engine(
        url=db_url or _get_core_db_url(),
        dsn=db_dsn or _get_core_db_dsn(),
    )


# ---------------------------------------------------------------------------
# Table bootstrap
# ---------------------------------------------------------------------------

def ensure_vocab_table(db_url: Optional[str] = None) -> None:
    """Verify backend-managed vocab table is reachable in the configured database."""
    if not _has_vocab_conn_target(db_url=db_url):
        LOG.warning(f'[VocabDB] {_VOCAB_DB_ENV_HINT} not set; skipping vocab table check.')
        return
    try:
        engine = _get_vocab_conn(db_url=db_url)
        with engine.connect() as conn:
            exists = conn.execute(
                text(
                    """SELECT 1
                           FROM information_schema.tables
                          WHERE table_schema = :table_schema
                            AND table_name = :table_name
                          LIMIT 1"""
                ),
                {'table_schema': VOCAB_SCHEMA, 'table_name': VOCAB_TABLE},
            ).scalar()
        if exists:
            LOG.info(f'[VocabDB] verified table {VOCAB_TABLE_QUALIFIED} is available.')
            return
        LOG.warning(f'[VocabDB] table {VOCAB_TABLE_QUALIFIED} not found in configured vocab database.')
    except Exception as exc:
        LOG.error(f'[VocabDB] ensure_vocab_table failed: {exc}')


def _ensure_table_once(db_url: Optional[str] = None) -> None:
    """Verify the vocab table exactly once per process."""
    global _table_ensured
    if not _table_ensured:
        with _table_ensure_lock:
            if not _table_ensured:
                ensure_vocab_table(db_url=db_url)
                _table_ensured = True


# ---------------------------------------------------------------------------
# Public query API
# ---------------------------------------------------------------------------

def fetch_vocab_for_user_id(user_id: str) -> List[Dict[str, Any]]:
    """Return all vocab rows for *user_id* as a list of ``{'word': ..., 'cluster_id': ...}`` dicts.

    Returns an empty list when the DB is unavailable or user has no entries.
    The ``cluster_id`` key matches the default ``cluster_key`` of
    :class:`lazyllm.tools.rag.QueryEnhACProcessor`.
    """
    _ensure_table_once()
    if not _has_vocab_conn_target():
        LOG.warning(
            f'[VocabDB] {_VOCAB_DB_ENV_HINT} not set; returning empty vocab for user_id={user_id!r}.'
        )
        return []
    try:
        engine = _get_vocab_conn()
        with engine.connect() as conn:
            rows = conn.execute(
                text(
                    f"""SELECT word, group_id
                          FROM {VOCAB_TABLE_QUALIFIED}
                         WHERE create_user_id = :user_id
                           AND deleted_at IS NULL"""
                ),
                {'user_id': user_id},
            ).mappings().all()
        result = [{'word': row['word'], 'cluster_id': row['group_id']} for row in rows]
        LOG.info(f'[VocabDB] fetched {len(result)} vocab entries for user_id={user_id!r}.')
        return result
    except Exception as exc:
        LOG.error(f'[VocabDB] fetch_vocab_for_user_id({user_id!r}) failed: {exc}')
        return []


def fetch_vocab_groups_for_user_id(
        user_id: str, *, db_url: Optional[str] = None) -> Dict[str, Dict[str, Any]]:
    """Return existing vocab groups for a user keyed by ``group_id``."""
    _ensure_table_once(db_url=db_url)
    if not _has_vocab_conn_target(db_url=db_url):
        LOG.warning(
            f'[VocabDB] {_VOCAB_DB_ENV_HINT} not set; returning empty vocab groups '
            f'for user_id={user_id!r}.'
        )
        return {}
    try:
        engine = _get_vocab_conn(db_url=db_url)
        with engine.connect() as conn:
            rows = conn.execute(
                text(
                    f"""SELECT group_id,
                               word,
                               COALESCE(description, '') AS description,
                               COALESCE({VOCAB_REFERENCE_COLUMN}, '') AS reference
                          FROM {VOCAB_TABLE_QUALIFIED}
                                                 WHERE create_user_id = :user_id
                           AND deleted_at IS NULL
                         ORDER BY group_id, id"""
                ),
                {'user_id': user_id},
            ).mappings().all()
    except Exception as exc:
        LOG.error(f'[VocabDB] fetch_vocab_groups_for_user_id({user_id!r}) failed: {exc}')
        return {}

    groups: Dict[str, Dict[str, Any]] = {}
    for row in rows:
        group_id = row['group_id']
        word = row['word']
        description = row['description']
        reference = row['reference']
        item = groups.setdefault(group_id, {
            'group_id': group_id,
            'description': description or '',
            'words': [],
            'references': [],
        })
        if word and word not in item['words']:
            item['words'].append(word)
        if reference and reference not in item['references']:
            item['references'].append(reference)
        if not item['description'] and description:
            item['description'] = description
    return groups


def list_chat_users(
    *,
    start_time: Optional[datetime] = None,
    end_time: Optional[datetime] = None,
    db_dsn: Optional[str] = None,
    db_url: Optional[str] = None,
) -> List[str]:
    """Return distinct users who have chat history in the given time range."""
    where = ['c.deleted_at IS NULL']
    params: Dict[str, Any] = {}
    if start_time is not None:
        where.append('h.create_time >= :start_time')
        params['start_time'] = start_time
    if end_time is not None:
        where.append('h.create_time <= :end_time')
        params['end_time'] = end_time
    sql = f"""
        SELECT DISTINCT c.create_user_id AS user_id
        FROM conversations c
        JOIN chat_histories h ON h.conversation_id = c.id
        WHERE {' AND '.join(where)}
        ORDER BY user_id
    """
    try:
        engine = _get_core_conn(db_dsn=db_dsn, db_url=db_url)
        with engine.connect() as conn:
            rows = [row for row in conn.execute(text(sql), params).scalars().all() if row]
        return rows
    except Exception as exc:
        LOG.error(f'[VocabDB] list_chat_users failed: {exc}')
        return []


def fetch_chat_histories_for_user_id(
    user_id: str,
    *,
    start_time: Optional[datetime] = None,
    end_time: Optional[datetime] = None,
    db_dsn: Optional[str] = None,
    db_url: Optional[str] = None,
) -> List[Dict[str, Any]]:
    """Return chat histories for one user ordered by time and sequence."""
    params: Dict[str, Any] = {'user_id': user_id}
    where = ['c.create_user_id = :user_id', 'c.deleted_at IS NULL']
    if start_time is not None:
        where.append('h.create_time >= :start_time')
        params['start_time'] = start_time
    if end_time is not None:
        where.append('h.create_time <= :end_time')
        params['end_time'] = end_time
    sql = f"""
        SELECT c.create_user_id AS user_id,
               c.id AS conversation_id,
               h.id AS message_id,
               h.seq,
             COALESCE(h.raw_content, '') AS raw_content,
             COALESCE(h.content, '') AS content,
             COALESCE(h.result, '') AS result,
               h.create_time
        FROM conversations c
        JOIN chat_histories h ON h.conversation_id = c.id
        WHERE {' AND '.join(where)}
        ORDER BY h.create_time ASC, h.seq ASC, h.id ASC
    """
    try:
        engine = _get_core_conn(db_dsn=db_dsn, db_url=db_url)
        with engine.connect() as conn:
            rows = conn.execute(text(sql), params).mappings().all()
    except Exception as exc:
        LOG.error(f'[VocabDB] fetch_chat_histories_for_user_id({user_id!r}) failed: {exc}')
        return []

    return [
        {
            'user_id': row['user_id'],
            'conversation_id': row['conversation_id'],
            'message_id': row['message_id'],
            'seq': row['seq'],
            'raw_content': row['raw_content'],
            'content': row['content'],
            'result': row['result'],
            'create_time': row['create_time'],
        }
        for row in rows
    ]


def fetch_chat_histories_for_session(
    session_id: str,
    *,
    db_dsn: Optional[str] = None,
    db_url: Optional[str] = None,
) -> List[Dict[str, Any]]:
    """Return chat histories for the conversation identified by a session id."""
    session_id = str(session_id or '').strip()
    if not session_id:
        return []

    conversation_id = session_id.rsplit('_', 1)[0].strip() if '_' in session_id else session_id
    if not conversation_id:
        return []

    sql = """
        SELECT c.create_user_id AS user_id,
               c.id AS conversation_id,
               h.id AS message_id,
               h.seq,
               COALESCE(h.raw_content, '') AS raw_content,
               COALESCE(h.content, '') AS content,
               COALESCE(h.result, '') AS result,
               h.create_time
        FROM conversations c
        JOIN chat_histories h ON h.conversation_id = c.id
        WHERE c.id = :conversation_id
          AND c.deleted_at IS NULL
        ORDER BY h.create_time ASC, h.seq ASC, h.id ASC
    """
    try:
        engine = _get_core_conn(db_dsn=db_dsn, db_url=db_url)
        with engine.connect() as conn:
            rows = conn.execute(text(sql), {'conversation_id': conversation_id}).mappings().all()
    except Exception as exc:
        LOG.error(f'[VocabDB] fetch_chat_histories_for_session({session_id!r}) failed: {exc}')
        return []

    return [
        {
            'user_id': row['user_id'],
            'conversation_id': row['conversation_id'],
            'message_id': row['message_id'],
            'seq': row['seq'],
            'raw_content': row['raw_content'],
            'content': row['content'],
            'result': row['result'],
            'create_time': row['create_time'],
        }
        for row in rows
    ]

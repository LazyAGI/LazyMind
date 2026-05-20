from __future__ import annotations

import json
from datetime import date, datetime
from decimal import Decimal
from typing import Any
from uuid import UUID

from sqlalchemy import text

from collections import defaultdict
from chat.components.agentic.history import _normalize_history_for_agent
from chat.components.skill_review.schemas import SessionData, SessionMessage
# from vocab.db import _get_core_db_dsn, _get_core_db_url, _get_db_url, _get_engine


# from __future__ import annotations

import shlex
import threading
from datetime import datetime
from typing import Any, Dict, List, Optional
from urllib.parse import urlsplit, urlunsplit

from lazyllm import LOG
from sqlalchemy import create_engine, text
from sqlalchemy.engine import URL, Engine

from config import config as _cfg

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
    return str(URL.create(
        'postgresql+psycopg2',
        username=parts.get('user') or None,
        password=parts.get('password') or None,
        host=parts['host'],
        port=port,
        database=database,
    ))


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


def _convert_history(rows):
    grouped = defaultdict(list)
    for row in rows:
        cid = row.get('conversation_id')
        if not cid:
            continue
        grouped[cid].append(row)

    sessions = []
    for cid, items in grouped.items():
        items = sorted(items, key=lambda x: x.get('seq', 0))
        messages = []
        for item in items:
            user_content = item.get('content') or ''
            assistant_content = item.get('result') or ''
            if user_content:
                messages.append({'role': 'user', 'content': user_content})
            if assistant_content:
                messages.append({'role': 'assistant', 'content': assistant_content})
        normalized = _normalize_history_for_agent(messages)
        create_user_id = item.get('create_user_id')
        sessions.append({'conversation_id': cid,
                         'messages': normalized,
                         'create_user_id': create_user_id})
    return sessions



def read_session():
    """Read all rows from the configured core database ``conversations`` table."""

    def jsonable(value: Any) -> Any:
        if isinstance(value, (datetime, date)):
            return value.isoformat()
        if isinstance(value, Decimal):
            return float(value)
        if isinstance(value, bytes):
            try:
                return value.decode('utf-8')
            except UnicodeDecodeError:
                return value.hex()
        if isinstance(value, UUID):
            return str(value)
        return value

    core_db_url = _get_core_db_url()
    core_db_dsn = _get_core_db_dsn()
    db_url = _get_db_url()
    if core_db_url:
        engine = _get_engine(url=core_db_url)
        source_db = 'core_database_url'
    elif core_db_dsn:
        engine = _get_engine(dsn=core_db_dsn)
        source_db = 'acl_db_dsn'
    elif db_url:
        engine = _get_engine(url=db_url)
        source_db = 'database_url'
    else:
        raise RuntimeError(
            '[SessionReader] LAZYMIND_CORE_DATABASE_URL, LAZYMIND_ACL_DB_DSN, '
            'or LAZYMIND_DATABASE_URL is not set; cannot connect to database.'
        )

    _sql = text(
        'SELECT ch.*, c.create_user_id'
        ' FROM chat_histories ch'
        ' LEFT JOIN conversations c ON ch.conversation_id = c.id'
    )
    with engine.connect() as conn:
        rows = conn.execute(_sql).mappings().all()
    chat_histories = []
    for row in rows:
        row = {key: jsonable(value) for key, value in dict(row).items()}
        chat_histories.append(row)

    # return _convert_history(chat_histories)
    # mock data for test
    with open('tmp/message_data_demo.json', 'r', encoding='utf-8') as f:
        return json.load(f)

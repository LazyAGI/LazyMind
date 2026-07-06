from __future__ import annotations

import json
import threading
from datetime import datetime, timezone
from typing import Any, Dict, List, Optional
from uuid import uuid4

from sqlalchemy import create_engine, text
from sqlalchemy.engine import Engine

from lazymind.common.postgres import normalize_postgres_connection_url
from lazymind.config import config as _cfg

MEMORY_REVIEW_TABLE = 'memory_review'

_DB_URL_ENV_HINT = (
    'LAZYMIND_CORE_DATABASE_URL, LAZYMIND_ACL_DB_DSN, or LAZYMIND_DATABASE_URL'
)
_engine_cache: Dict[str, Engine] = {}
_engine_cache_lock = threading.Lock()


def _get_engine(*, url: Optional[str] = None, dsn: Optional[str] = None) -> Engine:
    engine_url = normalize_postgres_connection_url(url=url, dsn=dsn)
    engine = _engine_cache.get(engine_url)
    if engine is not None:
        return engine
    with _engine_cache_lock:
        engine = _engine_cache.get(engine_url)
        if engine is None:
            engine = create_engine(engine_url, future=True, pool_pre_ping=True)
            _engine_cache[engine_url] = engine
    return engine


def _resolve_memory_review_conn_target() -> tuple[Optional[str], Optional[str]]:
    core_db_url = _cfg['core_database_url']
    if core_db_url and core_db_url.strip():
        return core_db_url.strip(), None

    core_db_dsn = _cfg['acl_db_dsn']
    if core_db_dsn and core_db_dsn.strip():
        return None, core_db_dsn.strip()

    db_url = _cfg['database_url']
    if db_url and db_url.strip():
        return db_url.strip(), None
    return None, None


def _get_memory_review_conn() -> Engine:
    url, dsn = _resolve_memory_review_conn_target()
    if not (url or dsn):
        raise RuntimeError(
            f'[MemoryReviewDB] {_DB_URL_ENV_HINT} is not set; cannot write memory_review.'
        )
    return _get_engine(url=url, dsn=dsn)


def _decode_operations(value: Any) -> List[Dict[str, Any]]:
    if value is None:
        return []
    if isinstance(value, list):
        return [dict(item) for item in value if isinstance(item, dict)]
    if isinstance(value, str):
        try:
            decoded = json.loads(value)
        except json.JSONDecodeError:
            return []
        if isinstance(decoded, list):
            return [dict(item) for item in decoded if isinstance(item, dict)]
    return []


def find_pending_memory_review_record(
    *,
    target: str,
    user_id: str,
) -> Optional[Dict[str, Any]]:
    if target not in {'memory', 'user_preference'}:
        raise ValueError("target must be one of 'memory' or 'user_preference'.")
    if not user_id.strip():
        raise ValueError('user_id is required.')

    engine = _get_memory_review_conn()
    with engine.begin() as conn:
        row = conn.execute(
            text(
                f"""
                SELECT
                    id,
                    target,
                    user_id,
                    session_id,
                    source_content,
                    content,
                    operations,
                    state,
                    review_status,
                    time
                FROM {MEMORY_REVIEW_TABLE}
                WHERE user_id = :user_id
                    AND target = :target
                    AND state = 'success'
                    AND review_status = 'pending'
                ORDER BY time DESC, id DESC
                LIMIT 1
                """
            ),
            {
                'user_id': user_id.strip(),
                'target': target,
            },
        ).mappings().first()

    if row is None:
        return None
    record = dict(row)
    record['operations'] = _decode_operations(record.get('operations'))
    return record


def update_memory_review_record(
    *,
    record_id: str,
    session_id: str = '',
    content: str,
    source_content: str = '',
    operations: Optional[List[Dict[str, Any]]] = None,
) -> Dict[str, Any]:
    if not record_id.strip():
        raise ValueError('record_id is required.')
    if not isinstance(content, str) or not content.strip():
        raise ValueError('content must be a non-empty string.')

    operation_payload = json.dumps(operations or [], ensure_ascii=False)
    updated_at = datetime.now(timezone.utc).isoformat()

    engine = _get_memory_review_conn()
    with engine.begin() as conn:
        result = conn.execute(
            text(
                f"""
                UPDATE {MEMORY_REVIEW_TABLE}
                SET
                    session_id = :session_id,
                    source_content = :source_content,
                    content = :content,
                    operations = CAST(:operations AS JSONB),
                    time = :time
                WHERE id = :id
                    AND review_status = 'pending'
                """
            ),
            {
                'id': record_id.strip(),
                'session_id': session_id.strip(),
                'source_content': source_content,
                'content': content,
                'operations': operation_payload,
                'time': updated_at,
            },
        )
        if result.rowcount == 0:
            raise RuntimeError('pending memory review record not found.')

    return {
        'id': record_id.strip(),
        'state': 'success',
        'review_status': 'pending',
        'time': updated_at,
    }


def insert_memory_review_record(
    *,
    target: str,
    user_id: str,
    session_id: str = '',
    content: str,
    source_content: str = '',
    operations: Optional[List[Dict[str, Any]]] = None,
) -> Dict[str, Any]:
    if target not in {'memory', 'user_preference'}:
        raise ValueError("target must be one of 'memory' or 'user_preference'.")
    if not user_id.strip():
        raise ValueError('user_id is required.')
    if not isinstance(content, str) or not content.strip():
        raise ValueError('content must be a non-empty string.')

    record_id = str(uuid4())
    operation_payload = json.dumps(operations or [], ensure_ascii=False)
    created_at = datetime.now(timezone.utc).isoformat()

    engine = _get_memory_review_conn()
    with engine.begin() as conn:
        conn.execute(
            text(
                f"""
                INSERT INTO {MEMORY_REVIEW_TABLE} (
                    id,
                    target,
                    user_id,
                    session_id,
                    source_content,
                    content,
                    operations,
                    state,
                    review_status,
                    time
                )
                VALUES (
                    :id,
                    :target,
                    :user_id,
                    :session_id,
                    :source_content,
                    :content,
                    CAST(:operations AS JSONB),
                    'success',
                    'pending',
                    :time
                )
                """
            ),
            {
                'id': record_id,
                'target': target,
                'user_id': user_id.strip(),
                'session_id': session_id.strip(),
                'source_content': source_content,
                'content': content,
                'operations': operation_payload,
                'time': created_at,
            },
        )

    return {
        'id': record_id,
        'target': target,
        'user_id': user_id.strip(),
        'state': 'success',
        'review_status': 'pending',
        'time': created_at,
    }

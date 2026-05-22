from __future__ import annotations

import json
import threading
from collections import defaultdict
from datetime import date, datetime
from decimal import Decimal
from typing import Any, Dict, Iterable, Optional
from uuid import UUID
from urllib.parse import urlsplit, urlunsplit

from lazyllm import LOG
from sqlalchemy import create_engine, text
from sqlalchemy.engine import Engine

from chat.components.agentic.history import _normalize_history_for_agent
from chat.components.skill_review.schemas import SkillReviewResolution
from config import config as _cfg

SKILL_REVIEW_TABLE = 'chat_skill_review'
_DB_URL_ENV = 'LAZYMIND_DATABASE_URL'
_CORE_DB_URL_ENV = 'LAZYMIND_CORE_DATABASE_URL'
_DB_ENV_HINT = f'{_CORE_DB_URL_ENV} or {_DB_URL_ENV}'

_table_ensured = False
_table_ensure_lock = threading.Lock()
_engine_cache: Dict[str, Engine] = {}
_engine_cache_lock = threading.Lock()

_HISTORY_SQL = text(
    'SELECT ch.*, c.create_user_id'
    ' FROM chat_histories ch'
    ' LEFT JOIN conversations c ON ch.conversation_id = c.id'
    ' WHERE ch.update_time >= :start_time AND ch.update_time <= :end_time'
    ' ORDER BY ch.update_time ASC'
)


def read_session(start_time: datetime, end_time: datetime) -> list[dict[str, Any]]:
    """Read chat history rows in [start_time, end_time] by chat_histories.update_time."""
    with _get_app_conn().connect() as conn:
        rows = conn.execute(
            _HISTORY_SQL,
            {'start_time': start_time, 'end_time': end_time},
        ).mappings().all()
    return _convert_history([_jsonable_value(dict(row)) for row in rows])


def insert_skill_review_records(
    records: SkillReviewResolution | Iterable[SkillReviewResolution],
) -> int:
    """Insert one or many skill review resolutions into ``chat_skill_review``."""
    normalized = _normalize_records(records)
    if not normalized:
        return 0
    _ensure_table_once()
    payload = [
        {
            'id': item.id,
            'skill_name': item.skill_name,
            'type': item.type,
            'skill_content': json.dumps(item.skill_content, ensure_ascii=False),
            'suggestion': item.suggestion,
        }
        for item in normalized
    ]
    with _get_app_conn().begin() as conn:
        conn.execute(
            text(
                f"""INSERT INTO {SKILL_REVIEW_TABLE}
                       (id, skill_name, "type", skill_content, suggestion)
                    VALUES
                       (:id, :skill_name, :type, CAST(:skill_content AS JSONB), :suggestion)
                    ON CONFLICT (id) DO UPDATE SET
                       skill_name = EXCLUDED.skill_name,
                       "type" = EXCLUDED."type",
                       skill_content = EXCLUDED.skill_content,
                       suggestion = EXCLUDED.suggestion"""
            ),
            payload,
        )
    return len(payload)


def add_skill_review_records(
    records: SkillReviewResolution | Iterable[SkillReviewResolution],
) -> int:
    return insert_skill_review_records(records)


def fetch_all_skill_review_records() -> list[dict[str, Any]]:
    """Return all rows from ``chat_skill_review`` ordered by insertion time."""
    _ensure_table_once()
    with _get_app_conn().connect() as conn:
        rows = conn.execute(
            text(
                f"""SELECT id, skill_name, "type", skill_content, suggestion
                       FROM {SKILL_REVIEW_TABLE}
                      ORDER BY id ASC"""
            )
        ).mappings().all()
    return [_jsonable_row(dict(row)) for row in rows]


def read_all_skill_review_records() -> list[dict[str, Any]]:
    return fetch_all_skill_review_records()


def ensure_skill_review_table() -> None:
    with _get_app_conn().begin() as conn:
        conn.execute(
            text(
                f"""CREATE TABLE IF NOT EXISTS {SKILL_REVIEW_TABLE} (
                    id TEXT PRIMARY KEY,
                    skill_name TEXT NOT NULL,
                    "type" TEXT NOT NULL CHECK ("type" IN ('new', 'patch')),
                    skill_content JSONB NOT NULL,
                    suggestion TEXT
                )"""
            )
        )
    LOG.info(f'[SkillReviewDB] ensured table {SKILL_REVIEW_TABLE}.')


def _convert_history(rows: list[dict[str, Any]]) -> list[dict[str, Any]]:
    grouped: dict[Any, list[dict[str, Any]]] = defaultdict(list)
    for row in rows:
        conversation_id = row.get('conversation_id')
        if conversation_id:
            grouped[conversation_id].append(row)

    sessions: list[dict[str, Any]] = []
    for conversation_id, items in grouped.items():
        items.sort(key=lambda row: row.get('seq', 0))
        messages: list[dict[str, str]] = []
        for item in items:
            if item.get('content'):
                messages.append({'role': 'user', 'content': item['content']})
            if item.get('result'):
                messages.append({'role': 'assistant', 'content': item['result']})
        sessions.append({
            'conversation_id': conversation_id,
            'messages': _normalize_history_for_agent(messages),
            'create_user_id': items[-1].get('create_user_id'),
        })
    return sessions


def _normalize_records(
    records: SkillReviewResolution | Iterable[SkillReviewResolution],
) -> list[SkillReviewResolution]:
    if isinstance(records, SkillReviewResolution):
        return [records]
    return [SkillReviewResolution.model_validate(item) for item in records]


def _ensure_table_once() -> None:
    global _table_ensured
    if _table_ensured:
        return
    with _table_ensure_lock:
        if not _table_ensured:
            ensure_skill_review_table()
            _table_ensured = True


def _get_app_conn() -> Engine:
    core_db_url = _get_core_db_url()
    if core_db_url:
        return _get_engine(core_db_url)
    db_url = _get_db_url()
    if db_url:
        return _get_engine(db_url)
    raise RuntimeError(f'[SkillReviewDB] {_DB_ENV_HINT} is not set; cannot connect to app database.')


def _get_db_url() -> Optional[str]:
    value = _cfg['database_url']
    return value if value and value.strip() else None


def _get_core_db_url() -> Optional[str]:
    value = _cfg['core_database_url']
    return value if value and value.strip() else None


def _get_engine(url: str) -> Engine:
    engine_url = _postgres_url(url)
    engine = _engine_cache.get(engine_url)
    if engine is not None:
        return engine
    with _engine_cache_lock:
        engine = _engine_cache.get(engine_url)
        if engine is None:
            engine = create_engine(engine_url, future=True, pool_pre_ping=True)
            _engine_cache[engine_url] = engine
    return engine


def _postgres_url(url: str) -> str:
    normalized = url.strip()
    if not normalized:
        raise RuntimeError('postgres connection url is required')
    parts = urlsplit(normalized)
    scheme = (parts.scheme or '').lower()
    if scheme in {'postgresql', 'postgres'}:
        return urlunsplit((f'{scheme}+psycopg2', parts.netloc, parts.path, parts.query, parts.fragment))
    if scheme.startswith('postgresql+') or scheme.startswith('postgres+'):
        return normalized
    raise RuntimeError(f'[SkillReviewDB] unsupported database scheme for postgres connection: {parts.scheme}')


def _jsonable_row(row: dict[str, Any]) -> dict[str, Any]:
    content = row.get('skill_content')
    if isinstance(content, str):
        try:
            row['skill_content'] = json.loads(content)
        except json.JSONDecodeError:
            pass
    return row


def _jsonable_value(value: Any) -> Any:
    if isinstance(value, dict):
        return {key: _jsonable_value(item) for key, item in value.items()}
    if isinstance(value, list):
        return [_jsonable_value(item) for item in value]
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

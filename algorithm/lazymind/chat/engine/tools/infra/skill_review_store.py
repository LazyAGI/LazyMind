from __future__ import annotations

import threading
from datetime import date, datetime
from decimal import Decimal
from typing import Any, Optional
from uuid import UUID, uuid4

from sqlalchemy import create_engine, text
from sqlalchemy.engine import Engine

from lazymind.common.postgres import normalize_postgres_sqlalchemy_url
from lazymind.config import config as _cfg

SKILL_REVIEW_TABLE = 'skill_review_results'
SKILL_REVIEW_TYPE_PATCH = 'patch'
_DB_URL_ENV = 'LAZYMIND_DATABASE_URL'
_CORE_DB_URL_ENV = 'LAZYMIND_CORE_DATABASE_URL'
_DB_ENV_HINT = f'{_CORE_DB_URL_ENV} or {_DB_URL_ENV}'

_engine_cache: dict[str, Engine] = {}
_engine_cache_lock = threading.Lock()


def find_pending_skill_review(category: str, skill_name: str, user_id: str) -> Optional[dict[str, Any]]:
    with _get_app_conn().connect() as conn:
        row = conn.execute(
            text(
                f"""SELECT id, category, skill_name, "type", review_status, userid,
                          requestid, summary, "time"
                       FROM {SKILL_REVIEW_TABLE}
                      WHERE userid = :userid
                        AND category = :category
                        AND skill_name = :skill_name
                        AND review_status = :review_status
                      ORDER BY "time" DESC, id DESC
                      LIMIT 1"""
            ),
            {
                'userid': user_id,
                'category': category,
                'skill_name': skill_name,
                'review_status': 'pending',
            },
        ).mappings().first()
    return _jsonable_row(dict(row)) if row else None


def insert_skill_review_result(
    *,
    category: str,
    skill_name: str,
    review_type: str,
    skill_content: str,
    user_id: str = '',
    requestid: str = '',
    summary: Optional[str] = None,
) -> dict[str, Any]:
    record_id = str(uuid4())
    requestid = requestid or uuid4().hex
    with _get_app_conn().begin() as conn:
        row = conn.execute(
            text(
                f"""INSERT INTO {SKILL_REVIEW_TABLE}
                       (id, category, skill_name, "type", review_status, userid,
                        requestid, skill_content, summary, "time")
                    VALUES
                       (:id, :category, :skill_name, :type, :review_status,
                        :userid, :requestid, :skill_content, :summary,
                        CURRENT_TIMESTAMP)
                    ON CONFLICT (id) DO UPDATE SET
                       category = EXCLUDED.category,
                       skill_name = EXCLUDED.skill_name,
                       "type" = EXCLUDED."type",
                       review_status = EXCLUDED.review_status,
                       userid = EXCLUDED.userid,
                       requestid = EXCLUDED.requestid,
                       skill_content = EXCLUDED.skill_content,
                       summary = EXCLUDED.summary,
                       "time" = EXCLUDED."time"
                 RETURNING id, category, skill_name, "type", review_status,
                           userid, requestid, skill_content, summary, "time" """
            ),
            {
                'id': record_id,
                'category': category,
                'skill_name': skill_name,
                'type': review_type,
                'review_status': 'pending',
                'userid': user_id,
                'requestid': requestid,
                'skill_content': skill_content,
                'summary': summary,
            },
        ).mappings().one()
    return _jsonable_row(dict(row))


def _get_app_conn() -> Engine:
    core_db_url = _get_core_db_url()
    if core_db_url:
        return _get_engine(core_db_url)
    db_url = _get_db_url()
    if db_url:
        return _get_engine(db_url)
    raise RuntimeError(f'[SkillReviewStore] {_DB_ENV_HINT} is not set; cannot connect to app database.')


def _get_db_url() -> Optional[str]:
    value = _cfg['database_url']
    return value if value and value.strip() else None


def _get_core_db_url() -> Optional[str]:
    value = _cfg['core_database_url']
    return value if value and value.strip() else None


def _get_engine(url: str) -> Engine:
    engine_url = normalize_postgres_sqlalchemy_url(url)
    engine = _engine_cache.get(engine_url)
    if engine is not None:
        return engine
    with _engine_cache_lock:
        engine = _engine_cache.get(engine_url)
        if engine is None:
            engine = create_engine(engine_url, future=True, pool_pre_ping=True)
            _engine_cache[engine_url] = engine
    return engine


def _jsonable_row(row: dict[str, Any]) -> dict[str, Any]:
    return {key: _jsonable_value(value) for key, value in row.items()}


def _jsonable_value(value: Any) -> Any:
    if isinstance(value, (datetime, date)):
        return value.isoformat()
    if isinstance(value, Decimal):
        return float(value)
    if isinstance(value, UUID):
        return str(value)
    return value

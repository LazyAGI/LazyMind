from __future__ import annotations

import json
import threading
from datetime import date, datetime
from decimal import Decimal
from typing import Any, Optional
from uuid import UUID

from sqlalchemy import create_engine, text
from sqlalchemy.engine import Engine

from lazymind.common.postgres import normalize_postgres_sqlalchemy_url
from lazymind.config import config as _cfg
from lazymind.review.skill_review.db import SKILL_REVIEW_RUN_STATS_TABLE

_DB_URL_ENV = 'LAZYMIND_DATABASE_URL'
_CORE_DB_URL_ENV = 'LAZYMIND_CORE_DATABASE_URL'
_DB_ENV_HINT = f'{_CORE_DB_URL_ENV} or {_DB_URL_ENV}'
_engine_cache: dict[str, Engine] = {}
_engine_cache_lock = threading.Lock()


def insert_skill_organize_result(
    *,
    record_id: str,
    requestid: str,
    user_id: str,
    organize_result: dict[str, Any],
) -> int:
    summary = json.dumps(_jsonable_value(organize_result), ensure_ascii=False)
    status = str(organize_result.get('status') or 'completed').strip()
    if not status:
        status = 'completed'
    with _get_app_conn().begin() as conn:
        conn.execute(
            text(
                f"""INSERT INTO {SKILL_REVIEW_RUN_STATS_TABLE}
                       (id, requestid, userid, status, started_at, duration_ms,
                        summary)
                    VALUES
                       (:id, :requestid, :userid, :status, :started_at,
                        :duration_ms, CAST(:summary AS JSONB))
                    ON CONFLICT (id) DO UPDATE SET
                       requestid = EXCLUDED.requestid,
                       userid = EXCLUDED.userid,
                       status = EXCLUDED.status,
                       started_at = EXCLUDED.started_at,
                       duration_ms = EXCLUDED.duration_ms,
                       summary = EXCLUDED.summary"""
            ),
            {
                'id': record_id,
                'userid': user_id,
                'requestid': requestid,
                'status': status,
                'started_at': organize_result.get('started_at') or organize_result.get('created_at') or None,
                'duration_ms': int(organize_result.get('duration_ms') or 0),
                'summary': summary,
            },
        )
    return 1


def _get_app_conn() -> Engine:
    core_db_url = _get_core_db_url()
    if core_db_url:
        return _get_engine(core_db_url)
    db_url = _get_db_url()
    if db_url:
        return _get_engine(db_url)
    raise RuntimeError(f'[SkillOrganize] {_DB_ENV_HINT} is not set; cannot connect to app database.')


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


def _jsonable_value(value: Any) -> Any:
    if isinstance(value, dict):
        return {key: _jsonable_value(item) for key, item in value.items()}
    if isinstance(value, list):
        return [_jsonable_value(item) for item in value]
    if isinstance(value, (datetime, date)):
        return value.isoformat()
    if isinstance(value, Decimal):
        return float(value)
    if isinstance(value, UUID):
        return str(value)
    return value

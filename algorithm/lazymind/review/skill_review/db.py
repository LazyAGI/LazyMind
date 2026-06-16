from __future__ import annotations

import json
import threading
from collections import defaultdict
from datetime import date, datetime
from decimal import Decimal
from typing import Any, Dict, Iterable, Optional
from uuid import UUID

from lazyllm import LOG
from sqlalchemy import create_engine, text
from sqlalchemy.engine import Engine

from lazymind.chat.service.component.history import normalize_history_for_agent
from lazymind.chat.engine.tools.infra.skill_validation import parse_skill_frontmatter
from lazymind.common.postgres import normalize_postgres_sqlalchemy_url
from lazymind.review.skill_review.schemas import SkillReviewResolution, SkillReviewRunStat
from lazymind.config import config as _cfg

SKILL_REVIEW_TABLE = 'skill_review_results'
SKILL_REVIEW_RUN_STATS_TABLE = 'skill_review_run_stats'
_DB_URL_ENV = 'LAZYMIND_DATABASE_URL'
_CORE_DB_URL_ENV = 'LAZYMIND_CORE_DATABASE_URL'
_DB_ENV_HINT = f'{_CORE_DB_URL_ENV} or {_DB_URL_ENV}'

_engine_cache: Dict[str, Engine] = {}
_engine_cache_lock = threading.Lock()


def read_session(
    start_time: datetime,
    end_time: datetime,
    user_ids: Optional[list[str]] = None,
) -> list[dict[str, Any]]:
    normalized_user_ids = [str(item).strip() for item in (user_ids or []) if str(item).strip()]

    params: dict[str, Any] = {
        'start_time': start_time,
        'end_time': end_time,
    }

    user_filter = ''
    if normalized_user_ids:
        user_filter = 'AND c.create_user_id = ANY(:user_ids)'
        params['user_ids'] = normalized_user_ids

    query = text(
        f"""
        WITH updated_sessions AS (
            SELECT c.id AS conversation_id, c.create_user_id
            FROM conversations c
            WHERE c.updated_at >= :start_time
              AND c.updated_at < :end_time
              {user_filter}
        )
        SELECT ch.*, us.create_user_id
        FROM chat_histories ch
        JOIN updated_sessions us ON ch.conversation_id = us.conversation_id
        ORDER BY ch.conversation_id ASC, ch.create_time ASC
        """
    )

    with _get_app_conn().connect() as conn:
        rows = conn.execute(query, params).mappings().all()

    return _convert_history([_jsonable_value(dict(row)) for row in rows])


def insert_skill_review_records(
    records: SkillReviewResolution | Iterable[SkillReviewResolution],
) -> int:
    """Insert one or many skill review resolutions into ``skill_review``."""
    normalized = _normalize_records(records)
    if not normalized:
        return 0
    payload = [
        {
            'id': item.id,
            'skill_name': item.skill_name,
            'category': _parse_skill_category(item.skill_content),
            'type': item.type,
            'review_status': item.review_status,
            'userid': item.userid,
            'requestid': item.requestid,
            'skill_content': item.skill_content,
            'summary': item.summary if item.type == 'patch' else None,
            'time': item.time,
        }
        for item in normalized
    ]
    with _get_app_conn().begin() as conn:
        conn.execute(
            text(
                f"""INSERT INTO {SKILL_REVIEW_TABLE}
                       (id, skill_name, category, "type", review_status, userid, requestid,
                        skill_content, summary, "time")
                    VALUES
                       (:id, :skill_name, :category, :type, :review_status, :userid, :requestid,
                        :skill_content, :summary,
                        COALESCE(CAST(NULLIF(:time, '') AS TIMESTAMPTZ), CURRENT_TIMESTAMP))
                    ON CONFLICT (id) DO UPDATE SET
                       skill_name = EXCLUDED.skill_name,
                       category = EXCLUDED.category,
                       "type" = EXCLUDED."type",
                       userid = EXCLUDED.userid,
                       requestid = EXCLUDED.requestid,
                       skill_content = EXCLUDED.skill_content,
                       summary = EXCLUDED.summary,
                       "time" = EXCLUDED."time" """
            ),
            payload,
        )
    return len(payload)


def insert_skill_review_run_stats(
    records: SkillReviewRunStat | Iterable[SkillReviewRunStat],
) -> int:
    normalized = _normalize_run_stats(records)
    if not normalized:
        return 0
    payload = [
        {
            'id': item.id,
            'requestid': item.requestid,
            'userid': item.userid,
            'status': item.status,
            'started_at': item.started_at,
            'duration_ms': item.duration_ms,
            'summary': json.dumps(item.summary, ensure_ascii=False),
        }
        for item in normalized
    ]
    with _get_app_conn().begin() as conn:
        conn.execute(
            text(
                f"""INSERT INTO {SKILL_REVIEW_RUN_STATS_TABLE}
                       (id, requestid, userid, status, started_at, duration_ms,
                        summary)
                    VALUES
                       (:id, :requestid, :userid, :status, :started_at, :duration_ms,
                        CAST(:summary AS JSONB))
                    ON CONFLICT (id) DO UPDATE SET
                       requestid = EXCLUDED.requestid,
                       userid = EXCLUDED.userid,
                       status = EXCLUDED.status,
                       started_at = EXCLUDED.started_at,
                       duration_ms = EXCLUDED.duration_ms,
                       summary = EXCLUDED.summary"""
            ),
            payload,
        )
    return len(payload)


def read_skill_review_records_by_ids(ids: list[str]) -> list[dict[str, Any]]:
    normalized_ids = [str(item).strip() for item in ids or [] if str(item).strip()]
    if not normalized_ids:
        return []
    with _get_app_conn().connect() as conn:
        rows = conn.execute(
            text(
                f"""SELECT id, skill_name, "type", review_status, userid, requestid,
                          skill_content, summary, "time"
                       FROM {SKILL_REVIEW_TABLE}
                      WHERE id = ANY(:ids)"""
            ),
            {'ids': normalized_ids},
        ).mappings().all()
    by_id = {str(row.get('id') or '').strip(): _jsonable_row(dict(row)) for row in rows}
    return [by_id[item_id] for item_id in normalized_ids if item_id in by_id]


def add_skill_review_records(
    records: SkillReviewResolution | Iterable[SkillReviewResolution],
) -> int:
    return insert_skill_review_records(records)


def fetch_all_skill_review_records() -> list[dict[str, Any]]:
    """Return all rows from ``skill_review`` ordered by insertion time."""
    with _get_app_conn().connect() as conn:
        rows = conn.execute(
            text(
                f"""SELECT id, skill_name, "type", review_status, userid, requestid,
                          skill_content, summary, "time"
                       FROM {SKILL_REVIEW_TABLE}
                      ORDER BY "time" ASC, id ASC"""
            )
        ).mappings().all()
    return [_jsonable_row(dict(row)) for row in rows]


def delete_all_skill_review_records() -> int:
    """Delete all rows from ``skill_review``."""
    with _get_app_conn().begin() as conn:
        result = conn.execute(
            text(f'DELETE FROM {SKILL_REVIEW_TABLE}')
        )
    deleted_count = int(result.rowcount or 0)
    LOG.info(f'[SkillReview] deleted {deleted_count} rows from {SKILL_REVIEW_TABLE}.')
    return deleted_count


def read_all_skill_review_records() -> list[dict[str, Any]]:
    return fetch_all_skill_review_records()


def _convert_history(rows: list[dict[str, Any]]) -> list[dict[str, Any]]:
    grouped: dict[Any, list[dict[str, Any]]] = defaultdict(list)
    for row in rows:
        conversation_id = row.get('conversation_id')
        if conversation_id:
            grouped[conversation_id].append(row)

    sessions: list[dict[str, Any]] = []
    for conversation_id, items in grouped.items():
        items.sort(key=lambda row: row.get('seq', 0))
        messages: list[dict[str, Any]] = []
        for item in items:
            messages.extend(_history_messages_from_row(item))
        sessions.append({
            'conversation_id': conversation_id,
            'messages': normalize_history_for_agent(messages),
            'create_user_id': items[-1].get('create_user_id'),
        })
    return sessions


def _history_messages_from_row(item: dict[str, Any]) -> list[dict[str, Any]]:
    for key in ('messages', 'history'):
        raw_messages = item.get(key)
        if isinstance(raw_messages, str):
            try:
                raw_messages = json.loads(raw_messages)
            except Exception:
                raw_messages = None
        if isinstance(raw_messages, list):
            return [
                message
                for message in (_normalize_raw_message(raw) for raw in raw_messages)
                if message
            ]

    role = str(item.get('role') or '').strip()
    if role:
        message = _normalize_raw_message(item)
        return [message] if message else []

    messages: list[dict[str, Any]] = []
    if item.get('content'):
        messages.append({'role': 'user', 'content': item['content']})
    if item.get('result'):
        messages.append({'role': 'assistant', 'content': item['result']})
    return messages


def _normalize_raw_message(raw: Any) -> dict[str, Any] | None:
    if not isinstance(raw, dict):
        return None

    role = str(raw.get('role') or '').strip()
    content = raw.get('content')
    if content is None:
        content = raw.get('result')
    if content is None:
        content = ''

    if role == 'tool':
        return {
            'role': 'tool',
            'tool_call_id': str(raw.get('tool_call_id') or raw.get('id') or ''),
            'name': str(raw.get('name') or raw.get('tool_name') or ''),
            'content': content,
        }

    if role == 'assistant':
        message: dict[str, Any] = {'role': 'assistant', 'content': content}
        tool_calls = raw.get('tool_calls')
        if isinstance(tool_calls, list):
            message['tool_calls'] = tool_calls
        return message

    if role == 'user':
        return {'role': 'user', 'content': content}

    if role:
        return {'role': role, 'content': content}

    return None


def _parse_skill_category(skill_content: str) -> str:
    frontmatter, _ = parse_skill_frontmatter(skill_content)
    return str(frontmatter.get('category') or '').strip()


def _normalize_records(
    records: SkillReviewResolution | Iterable[SkillReviewResolution],
) -> list[SkillReviewResolution]:
    if isinstance(records, SkillReviewResolution):
        return [records]
    return [SkillReviewResolution.model_validate(item) for item in records]


def _normalize_run_stats(
    records: SkillReviewRunStat | Iterable[SkillReviewRunStat],
) -> list[SkillReviewRunStat]:
    if isinstance(records, SkillReviewRunStat):
        return [records]
    return [SkillReviewRunStat.model_validate(item) for item in records]


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
    try:
        return normalize_postgres_sqlalchemy_url(url)
    except RuntimeError as exc:
        raise RuntimeError(f'[SkillReviewDB] {exc}') from exc


def _jsonable_row(row: dict[str, Any]) -> dict[str, Any]:
    content = row.get('skill_content')
    if isinstance(content, str):
        try:
            row['skill_content'] = json.loads(content)
        except json.JSONDecodeError:
            pass
    return {key: _jsonable_value(value) for key, value in row.items()}


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

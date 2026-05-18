from __future__ import annotations

import json
import sqlite3
from pathlib import Path
from typing import Any

from chat.components.skill_review.schemas import SessionData, SessionMessage

_MESSAGE_TABLE_HINTS = ('message', 'history', 'conversation', 'trace', 'span', 'event')
_ROLE_KEYS = ('role', 'message_role', 'type', 'kind')
_CONTENT_KEYS = ('content', 'message', 'text', 'input', 'output', 'raw_content', 'result')
_SESSION_KEYS = ('session_id', 'sid', 'session', 'conversation_id', 'trace_id')
_TIME_KEYS = ('created_at', 'create_time', 'updated_at', 'timestamp', 'time', 'start_time')
_TOOL_KEYS = ('tool_name', 'tool', 'function_name', 'name')
_SKILL_KEYS = ('skill_name', 'skill', 'called_skill')


class SessionReadError(RuntimeError):
    pass


def read_session(session_db_path: str, session_id: str) -> SessionData:
    path = Path(session_db_path).expanduser()
    if not path.exists():
        raise SessionReadError(f'session db not found: {path}')
    if not path.is_file():
        raise SessionReadError(f'session db path is not a file: {path}')

    conn = sqlite3.connect(str(path))
    conn.row_factory = sqlite3.Row
    try:
        tables = _list_tables(conn)
        messages: list[SessionMessage] = []
        for table in tables:
            if not _looks_like_message_table(table):
                continue
            messages.extend(_read_table_messages(conn, table, session_id))
        messages.sort(key=lambda item: item.created_at or '')
        return SessionData(
            session_id=session_id,
            source_db=str(path),
            tables=tables,
            messages=messages,
            metadata={'message_count': len(messages)},
        )
    finally:
        conn.close()


def _list_tables(conn: sqlite3.Connection) -> list[str]:
    rows = conn.execute(
        "SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name"
    ).fetchall()
    return [str(row['name']) for row in rows]


def _looks_like_message_table(table: str) -> bool:
    lowered = table.lower()
    return any(hint in lowered for hint in _MESSAGE_TABLE_HINTS)


def _read_table_messages(
    conn: sqlite3.Connection,
    table: str,
    session_id: str,
) -> list[SessionMessage]:
    columns = _table_columns(conn, table)
    if not columns:
        return []
    where, args = _session_filter(columns, session_id)
    order = _order_clause(columns)
    sql = f'SELECT * FROM "{table}"{where}{order} LIMIT 2000'
    try:
        rows = conn.execute(sql, args).fetchall()
    except sqlite3.Error:
        return []

    messages: list[SessionMessage] = []
    for row in rows:
        raw = {key: _decode_value(row[key]) for key in row.keys()}
        msg = _row_to_message(raw)
        if msg is not None:
            messages.append(msg)
    return messages


def _table_columns(conn: sqlite3.Connection, table: str) -> list[str]:
    try:
        rows = conn.execute(f'PRAGMA table_info("{table}")').fetchall()
    except sqlite3.Error:
        return []
    return [str(row['name']) for row in rows]


def _session_filter(columns: list[str], session_id: str) -> tuple[str, list[Any]]:
    keys = [key for key in _SESSION_KEYS if key in columns]
    if not keys:
        return '', []
    clauses = [f'"{key}" = ?' for key in keys]
    return ' WHERE (' + ' OR '.join(clauses) + ')', [session_id] * len(keys)


def _order_clause(columns: list[str]) -> str:
    for key in _TIME_KEYS:
        if key in columns:
            return f' ORDER BY "{key}" ASC'
    if 'id' in columns:
        return ' ORDER BY "id" ASC'
    return ''


def _row_to_message(raw: dict[str, Any]) -> SessionMessage | None:
    role = _first_text(raw, _ROLE_KEYS) or _infer_role(raw)
    content = _first_text(raw, _CONTENT_KEYS)
    tool_name = _first_text(raw, _TOOL_KEYS)
    skill_name = _first_text(raw, _SKILL_KEYS)

    if not content:
        content = _compact_json(raw)
    if not content:
        return None
    return SessionMessage(
        role=role,
        content=content,
        created_at=_first_text(raw, _TIME_KEYS),
        tool_name=tool_name,
        skill_name=skill_name,
        raw=raw,
    )


def _infer_role(raw: dict[str, Any]) -> str:
    text = _compact_json(raw).lower()
    if 'tool' in text or 'function' in text:
        return 'tool'
    if 'user' in text:
        return 'user'
    if 'assistant' in text or 'agent' in text:
        return 'assistant'
    return 'unknown'


def _first_text(raw: dict[str, Any], keys: tuple[str, ...]) -> str:
    for key in keys:
        value = raw.get(key)
        if value is None:
            continue
        text = str(value).strip()
        if text:
            return text
    return ''


def _decode_value(value: Any) -> Any:
    if isinstance(value, bytes):
        try:
            return value.decode('utf-8')
        except UnicodeDecodeError:
            return value.hex()
    if isinstance(value, str):
        text = value.strip()
        if text.startswith(('{', '[')):
            try:
                return json.loads(text)
            except json.JSONDecodeError:
                return value
    return value


def _compact_json(value: Any) -> str:
    try:
        return json.dumps(value, ensure_ascii=False, sort_keys=True, default=str)
    except TypeError:
        return str(value)

from __future__ import annotations

import json
import uuid
from contextlib import contextmanager
from datetime import datetime, timezone
from typing import Any, Dict, List, Optional

from sqlalchemy import create_engine, text
from sqlalchemy.sql import bindparam
from sqlalchemy.engine import Engine

from lazymind.common.postgres import normalize_postgres_connection_url
from lazymind.config import config as _cfg


def _utcnow() -> datetime:
    return datetime.now(timezone.utc)


def _new_id(prefix: str) -> str:
    return f'{prefix}{uuid.uuid4().hex}'


class SubAgentDB:
    """Thin DB accessor over the down-passed core DSN.

    The connection is created from the DSN provided per request, used for the
    lifetime of one SubAgent run, and disposed afterwards. No global caching.
    """

    def __init__(self, dsn: str) -> None:
        url = normalize_postgres_connection_url(dsn=dsn)
        self._engine: Engine = create_engine(url, pool_pre_ping=True, future=True)

    def dispose(self) -> None:
        try:
            self._engine.dispose()
        except Exception:
            pass

    @contextmanager
    def _conn(self):
        with self._engine.begin() as conn:
            yield conn

    # ----- tasks -----

    def load_task(self, task_id: str) -> Optional[Dict[str, Any]]:
        with self._conn() as conn:
            row = conn.execute(
                text(
                    'SELECT id, conversation_id, agent_type, title, objective, params, mode, '
                    'status, workspace_path, input_artifact_keys, output_artifact_keys '
                    'FROM sub_agent_tasks WHERE id = :id'
                ),
                {'id': task_id},
            ).mappings().first()
            return dict(row) if row else None

    # ----- steps -----

    def append_step(self, task_id: str, seq: int, role: str, content: Dict[str, Any]) -> None:
        with self._conn() as conn:
            conn.execute(
                text(
                    'INSERT INTO sub_agent_steps (id, task_id, seq, role, content, created_at) '
                    'VALUES (:id, :task_id, :seq, :role, :content, :created_at)'
                ),
                {
                    'id': _new_id('sas_'),
                    'task_id': task_id,
                    'seq': seq,
                    'role': role,
                    'content': json.dumps(content, ensure_ascii=False, default=str),
                    'created_at': _utcnow(),
                },
            )

    def load_steps(self, task_id: str) -> List[Dict[str, Any]]:
        with self._conn() as conn:
            rows = conn.execute(
                text('SELECT seq, role, content FROM sub_agent_steps WHERE task_id = :task_id ORDER BY seq ASC'),
                {'task_id': task_id},
            ).mappings().all()
        out: List[Dict[str, Any]] = []
        for r in rows:
            content = r['content']
            if isinstance(content, str):
                try:
                    content = json.loads(content)
                except ValueError:
                    content = {}
            out.append({'seq': r['seq'], 'role': r['role'], 'content': content})
        return out

    def max_step_seq(self, task_id: str) -> int:
        with self._conn() as conn:
            row = conn.execute(
                text('SELECT COALESCE(MAX(seq), -1) AS m FROM sub_agent_steps WHERE task_id = :task_id'),
                {'task_id': task_id},
            ).mappings().first()
        return int(row['m']) if row else -1

    # ----- artifacts -----

    def next_artifact_seq(self, task_id: str, key: str) -> int:
        with self._conn() as conn:
            row = conn.execute(
                text(
                    'SELECT COALESCE(MAX(seq), 0) AS m FROM sub_agent_artifacts '
                    'WHERE task_id = :task_id AND artifact_key = :key'
                ),
                {'task_id': task_id, 'key': key},
            ).mappings().first()
        return (int(row['m']) if row else 0) + 1

    def save_artifact(self, task_id: str, key: str, content_type: str, value: Dict[str, Any], seq: int) -> None:
        with self._conn() as conn:
            conn.execute(
                text(
                    'INSERT INTO sub_agent_artifacts (id, task_id, artifact_key, content_type, value, seq, created_at) '
                    'VALUES (:id, :task_id, :key, :ct, :value, :seq, :created_at)'
                ),
                {
                    'id': _new_id('saa_'),
                    'task_id': task_id,
                    'key': key,
                    'ct': content_type,
                    'value': json.dumps(value, ensure_ascii=False, default=str),
                    'seq': seq,
                    'created_at': _utcnow(),
                },
            )

    def load_artifacts(self, task_id: str, keys: Optional[List[str]] = None) -> List[Dict[str, Any]]:
        sql = (
            'SELECT artifact_key, content_type, value, seq FROM sub_agent_artifacts '
            'WHERE task_id = :task_id'
        )
        params: Dict[str, Any] = {'task_id': task_id}
        if keys:
            sql += ' AND artifact_key IN :keys'
            params['keys'] = tuple(keys)
        sql += ' ORDER BY artifact_key ASC, seq ASC'
        with self._conn() as conn:
            stmt = text(sql)
            if keys:
                stmt = stmt.bindparams(bindparam('keys', expanding=True))
            rows = conn.execute(stmt, params).mappings().all()
        out: List[Dict[str, Any]] = []
        for r in rows:
            value = r['value']
            if isinstance(value, str):
                try:
                    value = json.loads(value)
                except ValueError:
                    value = {}
            out.append({
                'artifact_key': r['artifact_key'],
                'content_type': r['content_type'],
                'value': value,
                'seq': r['seq'],
            })
        return out

    def saved_artifact_keys(self, task_id: str) -> List[str]:
        with self._conn() as conn:
            rows = conn.execute(
                text('SELECT DISTINCT artifact_key FROM sub_agent_artifacts WHERE task_id = :task_id'),
                {'task_id': task_id},
            ).mappings().all()
        return [r['artifact_key'] for r in rows]


# ---------------------------------------------------------------------------
# TaskQueryDB — read-only DB accessor for ChatAgent tool context.
#
# Unlike SubAgentDB (which receives a db_dsn per request), this class derives
# the connection string from environment config so it can be used inside
# ChatAgent tool functions that have no per-request DSN available.
#
# Connection priority (mirrors vocab_db.py):
#   1. LAZYMIND_CORE_DATABASE_URL
#   2. ACL_DB_DSN  (libpq key=value or URL)
# ---------------------------------------------------------------------------

_task_query_engine: Optional[Engine] = None


def _get_task_query_engine() -> Engine:
    global _task_query_engine
    if _task_query_engine is not None:
        return _task_query_engine
    core_url = str(_cfg['core_database_url'] or '').strip()
    acl_dsn = str(_cfg['acl_db_dsn'] or '').strip()
    conn_url = normalize_postgres_connection_url(url=core_url or None, dsn=acl_dsn or None)
    _task_query_engine = create_engine(conn_url, pool_pre_ping=True, future=True)
    return _task_query_engine


class TaskQueryDB:
    """Read-only accessor for sub_agent_tasks / sub_agent_artifacts used by ChatAgent tools.

    All methods return plain dicts and swallow DB errors (returning empty fallbacks),
    so callers never need to handle database exceptions at the tool level.
    """

    @contextmanager
    def _conn(self):
        with _get_task_query_engine().connect() as conn:
            yield conn

    def get_task_status(self, task_id: str) -> Optional[Dict[str, Any]]:
        """Return status snapshot for one task (status, progress_pct, current_phase, summary).

        Returns None when the task does not exist or the DB is unavailable.
        """
        try:
            with self._conn() as conn:
                row = conn.execute(
                    text(
                        'SELECT id, status, progress_pct, current_phase, summary '
                        'FROM sub_agent_tasks WHERE id = :id'
                    ),
                    {'id': task_id},
                ).mappings().first()
            if row is None:
                return None
            return {
                'task_id': row['id'],
                'status': row['status'],
                'progress': row['progress_pct'],
                'current_phase': row['current_phase'],
                'summary': row['summary'],
            }
        except Exception:
            return None

    def list_tasks_by_conversation(self, conv_id: str) -> List[Dict[str, Any]]:
        """Return all tasks for a conversation with their latest artifacts.

        Returns the same shape expected by _list_conversation_tasks / _resolve_task:
        task_id, id, title, agent_type, status, progress_pct, current_phase, summary,
        seq_in_conversation, output_artifact_keys, artifacts (list of artifact dicts).
        """
        try:
            with self._conn() as conn:
                task_rows = conn.execute(
                    text(
                        'SELECT id, title, agent_type, status, progress_pct, current_phase, '
                        '       summary, seq_in_conversation, output_artifact_keys '
                        'FROM sub_agent_tasks '
                        'WHERE conversation_id = :conv_id '
                        'ORDER BY seq_in_conversation ASC'
                    ),
                    {'conv_id': conv_id},
                ).mappings().all()
        except Exception:
            return []

        if not task_rows:
            return []

        task_ids = [r['id'] for r in task_rows]
        try:
            with self._conn() as conn:
                art_rows = conn.execute(
                    text(
                        'SELECT task_id, artifact_key, content_type, value, seq '
                        'FROM sub_agent_artifacts '
                        'WHERE task_id IN :ids '
                        'ORDER BY task_id, artifact_key, seq ASC'
                    ).bindparams(bindparam('ids', expanding=True)),
                    {'ids': task_ids},
                ).mappings().all()
        except Exception:
            art_rows = []

        arts_by_task: Dict[str, List[Dict[str, Any]]] = {}
        for ar in art_rows:
            value = ar['value']
            if isinstance(value, str):
                try:
                    value = json.loads(value)
                except ValueError:
                    value = {}
            arts_by_task.setdefault(ar['task_id'], []).append({
                'artifact_key': ar['artifact_key'],
                'content_type': ar['content_type'],
                'value': value,
                'seq': ar['seq'],
            })

        tasks = []
        for r in task_rows:
            out_keys = r['output_artifact_keys']
            if isinstance(out_keys, str):
                try:
                    out_keys = json.loads(out_keys)
                except ValueError:
                    out_keys = []
            tasks.append({
                'task_id': r['id'],
                'id': r['id'],
                'title': r['title'],
                'agent_type': r['agent_type'],
                'status': r['status'],
                'progress_pct': r['progress_pct'],
                'current_phase': r['current_phase'],
                'summary': r['summary'],
                'seq_in_conversation': r['seq_in_conversation'],
                'output_artifact_keys': out_keys or [],
                'artifacts': arts_by_task.get(r['id'], []),
            })
        return tasks

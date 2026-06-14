from __future__ import annotations

import json
import uuid
from contextlib import contextmanager
from datetime import datetime, timezone
from typing import Any, Dict, List, Optional

from sqlalchemy import create_engine, text
from sqlalchemy.engine import Engine

from lazymind.common.postgres import normalize_postgres_connection_url


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
            sql += ' AND artifact_key = ANY(:keys)'
            params['keys'] = list(keys)
        sql += ' ORDER BY artifact_key ASC, seq ASC'
        with self._conn() as conn:
            rows = conn.execute(text(sql), params).mappings().all()
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

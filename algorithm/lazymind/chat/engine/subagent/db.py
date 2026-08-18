"""Persistence for ordinary LazyMind SubAgent tasks.

Workflow state and Artifacts are intentionally absent; Workflow-aware callers
must use the public Workflow SDK.
"""
from __future__ import annotations

import json
import uuid
from contextlib import contextmanager
from datetime import datetime, timezone
from typing import Any, Dict, List, Optional

from sqlalchemy import create_engine, text
from sqlalchemy.engine import Engine
from sqlalchemy.sql import bindparam

from lazymind.common.database.postgres import normalize_postgres_connection_url
from lazymind.config import config


def _decode(raw: Any, default: Any = None) -> Any:
    if raw is None:
        return default
    if isinstance(raw, (bytes, bytearray)):
        raw = raw.decode('utf-8')
    if isinstance(raw, str):
        try:
            return json.loads(raw)
        except ValueError:
            return default
    return raw


class SubAgentDB:
    def __init__(self, dsn: str) -> None:
        self._engine: Engine = create_engine(
            normalize_postgres_connection_url(dsn=dsn), pool_pre_ping=True, future=True,
        )

    def dispose(self) -> None:
        self._engine.dispose()

    @contextmanager
    def _conn(self):
        with self._engine.begin() as conn:
            yield conn

    def load_task(self, task_id: str) -> Optional[Dict[str, Any]]:
        with self._conn() as conn:
            row = conn.execute(text(
                'SELECT id, conversation_id, agent_type, title, objective, params, mode, '
                'status, workspace_path, input_slots, output_slots, sources, create_user_id '
                'FROM sub_agent_tasks WHERE id = :id'
            ), {'id': task_id}).mappings().first()
        if not row:
            return None
        task = dict(row)
        task['sources'] = _decode(task.get('sources'), [])
        return task

    def append_step(self, task_id: str, seq: int, role: str, content: Dict[str, Any]) -> None:
        with self._conn() as conn:
            conn.execute(text(
                'INSERT INTO sub_agent_steps (id, task_id, seq, role, content, created_at) '
                'VALUES (:id, :task_id, :seq, :role, :content, :created_at)'
            ), {'id': 'sas_' + uuid.uuid4().hex, 'task_id': task_id, 'seq': seq, 'role': role,
                'content': json.dumps(content, ensure_ascii=False, default=str),
                'created_at': datetime.now(timezone.utc)})

    def load_steps(self, task_id: str) -> List[Dict[str, Any]]:
        with self._conn() as conn:
            rows = conn.execute(text(
                'SELECT seq, role, content FROM sub_agent_steps '
                'WHERE task_id = :task_id ORDER BY seq ASC'
            ), {'task_id': task_id}).mappings().all()
        return [{'seq': row['seq'], 'role': row['role'],
                 'content': _decode(row['content'], {})} for row in rows]

    def max_step_seq(self, task_id: str) -> int:
        with self._conn() as conn:
            row = conn.execute(text(
                'SELECT COALESCE(MAX(seq), -1) AS value FROM sub_agent_steps WHERE task_id = :id'
            ), {'id': task_id}).mappings().first()
        return int(row['value']) if row else -1

    def next_artifact_seq(self, task_id: str, key: str) -> int:
        with self._conn() as conn:
            row = conn.execute(text(
                'SELECT COALESCE(MAX(seq), 0) AS value FROM sub_agent_artifacts '
                'WHERE task_id = :id AND slot = :slot'
            ), {'id': task_id, 'slot': key}).mappings().first()
        return int(row['value'] if row else 0) + 1

    def load_artifacts(self, task_id: str, keys: Optional[List[str]] = None) -> List[Dict[str, Any]]:
        statement = 'SELECT slot, content_type, value, seq FROM sub_agent_artifacts WHERE task_id = :id'
        params: Dict[str, Any] = {'id': task_id}
        query = text(statement)
        if keys:
            query = text(statement + ' AND slot IN :keys').bindparams(bindparam('keys', expanding=True))
            params['keys'] = tuple(keys)
        with self._conn() as conn:
            rows = conn.execute(query, params).mappings().all()
        return [{'slot': row['slot'], 'content_type': row['content_type'],
                 'value': _decode(row['value'], {}), 'seq': row['seq']} for row in rows]

    def load_artifacts_for_tasks(self, task_ids: List[str]) -> List[Dict[str, Any]]:
        if not task_ids:
            return []
        with self._conn() as conn:
            rows = conn.execute(text(
                'SELECT task_id, slot, content_type, value, seq FROM sub_agent_artifacts '
                'WHERE task_id IN :ids AND hidden = FALSE ORDER BY task_id, slot, seq'
            ).bindparams(bindparam('ids', expanding=True)), {'ids': task_ids}).mappings().all()
        return [{'task_id': row['task_id'], 'slot': row['slot'],
                 'content_type': row['content_type'], 'value': _decode(row['value'], {}),
                 'seq': row['seq']} for row in rows]

    def format_task_artifacts(self, task_ids: List[str]) -> List[str]:
        rows = self.load_artifacts_for_tasks(task_ids)
        return [json.dumps(row, ensure_ascii=False, default=str) for row in rows]


class MemorySubAgentStore:
    """Per-execution state for remote hosts that must not connect to Core DB."""

    def __init__(self, task: Dict[str, Any], steps: Optional[List[Dict[str, Any]]] = None) -> None:
        self._task = dict(task)
        self._steps = [dict(step) for step in (steps or [])]

    def load_task(self, task_id: str) -> Optional[Dict[str, Any]]:
        return dict(self._task) if str(self._task.get('id')) == task_id else None

    def append_step(self, task_id: str, seq: int, role: str, content: Dict[str, Any]) -> None:
        self._steps.append({'seq': seq, 'role': role, 'content': dict(content)})

    def load_steps(self, task_id: str) -> List[Dict[str, Any]]:
        return [dict(step) for step in sorted(self._steps, key=lambda value: value['seq'])]

    def max_step_seq(self, task_id: str) -> int:
        return max((int(step['seq']) for step in self._steps), default=-1)

    def next_artifact_seq(self, task_id: str, key: str) -> int:
        return 1

    def load_artifacts(self, task_id: str, keys: Optional[List[str]] = None) -> List[Dict[str, Any]]:
        return []

    def dispose(self) -> None:
        pass


_query_engine: Optional[Engine] = None


def _engine() -> Engine:
    global _query_engine
    if _query_engine is None:
        core_url = str(config['core_database_url'] or '').strip()
        acl_dsn = str(config['acl_db_dsn'] or '').strip()
        _query_engine = create_engine(
            normalize_postgres_connection_url(url=core_url or None, dsn=acl_dsn or None),
            pool_pre_ping=True, future=True,
        )
    return _query_engine


class TaskQueryDB:
    @contextmanager
    def _conn(self):
        with _engine().connect() as conn:
            yield conn

    def get_task_status(self, task_id: str) -> Optional[Dict[str, Any]]:
        try:
            with self._conn() as conn:
                row = conn.execute(text(
                    'SELECT id, status, progress_pct, current_phase, summary '
                    'FROM sub_agent_tasks WHERE id = :id'
                ), {'id': task_id}).mappings().first()
            return dict(row) if row else None
        except Exception:
            return None

    def list_tasks_by_conversation(self, conversation_id: str) -> List[Dict[str, Any]]:
        try:
            with self._conn() as conn:
                rows = conn.execute(text(
                    'SELECT id, title, agent_type, status, progress_pct, current_phase, summary, '
                    'seq_in_conversation FROM sub_agent_tasks WHERE conversation_id = :id '
                    'ORDER BY seq_in_conversation'
                ), {'id': conversation_id}).mappings().all()
            return [dict(row) for row in rows]
        except Exception:
            return []

    def load_artifacts_for_tasks(self, task_ids: List[str]) -> List[Dict[str, Any]]:
        """Read visible artifacts for ordinary LazyMind tasks."""
        if not task_ids:
            return []
        with self._conn() as conn:
            rows = conn.execute(text(
                'SELECT task_id, slot, content_type, value, seq FROM sub_agent_artifacts '
                'WHERE task_id IN :ids AND hidden = FALSE ORDER BY task_id, slot, seq'
            ).bindparams(bindparam('ids', expanding=True)), {'ids': task_ids}).mappings().all()
        return [{'task_id': row['task_id'], 'slot': row['slot'],
                 'content_type': row['content_type'], 'value': _decode(row['value'], {}),
                 'seq': row['seq']} for row in rows]

    def format_task_artifacts(self, task_ids: List[str]) -> List[str]:
        """Render ordinary task artifacts for the parent ChatAgent context."""
        return [json.dumps(row, ensure_ascii=False, default=str)
                for row in self.load_artifacts_for_tasks(task_ids)]

    def build_chat_agent_task_context(self, conversation_id: str) -> str:
        tasks = self.list_tasks_by_conversation(conversation_id)
        visible = [task for task in tasks if task.get('status') in {
            'running', 'pending', 'succeeded', 'failed', 'interrupted',
        }]
        if not visible:
            return ''
        status_labels = {'succeeded': 'done', 'failed': 'failed',
                         'interrupted': 'interrupted', 'pending': 'pending',
                         'running': 'running'}
        lines = [
            f'Task {index}. {task.get("title") or task.get("id")} '
            f'[{status_labels.get(str(task.get("status")), task.get("status"))}]'
            + (f': {task.get("summary")}' if task.get('summary') else '')
            for index, task in enumerate(visible, 1)
        ]
        task_ids = [str(task.get('id') or task.get('task_id') or '') for task in visible]
        lines.extend(self.format_task_artifacts([task_id for task_id in task_ids if task_id]))
        return '## LazyMind Tasks\n' + '\n'.join(lines)

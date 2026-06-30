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

    def load_plugin_session_steps(self, session_id: str) -> List[Dict[str, Any]]:
        """Return plugin_session_steps rows for a session, ordered by attempt ASC.

        Used by _enrich_objective_with_artifacts to find succeeded step task_ids.
        Returns empty list on any error.
        """
        try:
            with self._conn() as conn:
                rows = conn.execute(
                    text(
                        'SELECT step_id, task_id, status, attempt '
                        'FROM plugin_session_steps '
                        'WHERE session_id = :session_id '
                        'ORDER BY attempt ASC'
                    ),
                    {'session_id': session_id},
                ).mappings().all()
            return [dict(r) for r in rows]
        except Exception:
            return []

    def load_artifacts_for_tasks(self, task_ids: List[str]) -> List[Dict[str, Any]]:
        """Return non-hidden artifacts for a list of task_ids, ordered by task_id / key / seq ASC.

        Returns empty list on any error or if task_ids is empty.
        """
        if not task_ids:
            return []
        try:
            with self._conn() as conn:
                rows = conn.execute(
                    text(
                        'SELECT task_id, artifact_key, content_type, value, seq '
                        'FROM sub_agent_artifacts '
                        'WHERE task_id IN :ids AND hidden = FALSE '
                        'ORDER BY task_id, artifact_key, seq ASC'
                    ).bindparams(bindparam('ids', expanding=True)),
                    {'ids': task_ids},
                ).mappings().all()
            out: List[Dict[str, Any]] = []
            for r in rows:
                value = r['value']
                if isinstance(value, str):
                    try:
                        value = json.loads(value)
                    except ValueError:
                        value = {}
                out.append({
                    'task_id': r['task_id'],
                    'artifact_key': r['artifact_key'],
                    'content_type': r['content_type'],
                    'value': value,
                    'seq': r['seq'],
                })
            return out
        except Exception:
            return []

    def load_selected_slot_artifacts_with_order(self, session_id: str) -> List[Dict[str, Any]]:
        """Return selected slot revisions with sort_order derived from plugin_slot_order.

        sort_order is the 1-based position in the order_list JSON array for the slot.
        Falls back to list_index + 1 when no order row exists for the slot.

        Returns a list of dicts with keys:
          artifact_key, list_index, artifact_seq, human_artifact_id,
          content_snapshot, change_source, task_id, sort_order
        Returns empty list on any error.
        """
        try:
            with self._conn() as conn:
                rows = conn.execute(
                    text(
                        'SELECT '
                        '  psr.artifact_key, '
                        '  psr.list_index, '
                        '  psr.artifact_seq, '
                        '  psr.human_artifact_id, '
                        '  psr.content_snapshot, '
                        '  psr.change_source, '
                        '  pss.task_id, '
                        '  COALESCE(pos.sort_order, psr.list_index + 1) AS sort_order '
                        'FROM plugin_slot_revisions psr '
                        'LEFT JOIN plugin_session_steps pss '
                        '  ON pss.session_id = psr.session_id '
                        '  AND pss.step_id   = psr.step_id '
                        '  AND pss.attempt   = psr.attempt '
                        'LEFT JOIN ( '
                        '  SELECT slot_id, val::int AS list_index, '
                        '         (ord - 1 + 1) AS sort_order '
                        '  FROM plugin_slot_order, '
                        '       jsonb_array_elements_text(order_list) '
                        '       WITH ORDINALITY AS t(val, ord) '
                        '  WHERE session_id = :session_id '
                        ') pos ON pos.slot_id = psr.slot_id '
                        '      AND pos.list_index = psr.list_index '
                        'WHERE psr.session_id = :session_id '
                        '  AND psr.selected = TRUE '
                        'ORDER BY psr.artifact_key ASC, '
                        '         COALESCE(pos.sort_order, psr.list_index + 1) ASC'
                    ),
                    {'session_id': session_id},
                ).mappings().all()
            return [dict(r) for r in rows]
        except Exception:
            return []

    def load_slot_artifact_by_sort_order(
        self, session_id: str, artifact_key: str, sort_order: int
    ) -> Optional[Dict[str, Any]]:
        """Resolve sort_order → list_index for a plugin session slot, then return the
        selected revision metadata (artifact_seq, human_artifact_id, content_snapshot,
        task_id, list_index).

        Returns None when not found or on any error.
        """
        try:
            with self._conn() as conn:
                row = conn.execute(
                    text(
                        'SELECT '
                        '  psr.artifact_key, '
                        '  psr.list_index, '
                        '  psr.artifact_seq, '
                        '  psr.human_artifact_id, '
                        '  psr.content_snapshot, '
                        '  psr.change_source, '
                        '  pss.task_id '
                        'FROM plugin_slot_revisions psr '
                        'LEFT JOIN plugin_session_steps pss '
                        '  ON pss.session_id = psr.session_id '
                        '  AND pss.step_id   = psr.step_id '
                        '  AND pss.attempt   = psr.attempt '
                        'INNER JOIN ( '
                        '  SELECT slot_id, val::int AS list_index '
                        '  FROM plugin_slot_order, '
                        '       jsonb_array_elements_text(order_list) '
                        '       WITH ORDINALITY AS t(val, ord) '
                        '  WHERE session_id = :session_id '
                        '    AND (ord - 1 + 1) = :sort_order '
                        ') pos ON pos.slot_id = psr.slot_id '
                        '      AND pos.list_index = psr.list_index '
                        'WHERE psr.session_id = :session_id '
                        '  AND psr.artifact_key = :artifact_key '
                        '  AND psr.selected = TRUE '
                        'ORDER BY psr.list_index ASC '
                        'LIMIT 1'
                    ),
                    {
                        'session_id': session_id,
                        'artifact_key': artifact_key,
                        'sort_order': sort_order,
                    },
                ).mappings().first()
            return dict(row) if row else None
        except Exception:
            return None

    def resolve_slot_revision_value(
        self, row: Dict[str, Any]
    ) -> tuple:
        """Resolve value and content_type from a plugin_slot_revisions row dict.

        Returns (value, content_type) where value may be None if unresolvable.
        """
        human_artifact_id = row.get('human_artifact_id')
        artifact_seq = row.get('artifact_seq')
        task_id = row.get('task_id')
        content_snapshot = row.get('content_snapshot')

        value: Any = None
        content_type: Optional[str] = None

        try:
            if human_artifact_id:
                with self._conn() as conn:
                    ha = conn.execute(
                        text(
                            'SELECT value, content_type FROM plugin_human_artifacts '
                            'WHERE id = :id'
                        ),
                        {'id': human_artifact_id},
                    ).mappings().first()
                if ha is not None:
                    raw = ha['value']
                    content_type = ha['content_type']
                    value = json.loads(raw) if isinstance(raw, str) else (raw or {})
            elif artifact_seq is not None and task_id:
                with self._conn() as conn:
                    ar = conn.execute(
                        text(
                            'SELECT value, content_type FROM sub_agent_artifacts '
                            'WHERE task_id = :tid AND artifact_key = :key AND seq = :seq'
                        ),
                        {'tid': task_id, 'key': row.get('artifact_key', ''), 'seq': artifact_seq},
                    ).mappings().first()
                if ar is not None:
                    raw = ar['value']
                    content_type = ar['content_type']
                    value = json.loads(raw) if isinstance(raw, str) else (raw or {})
            elif content_snapshot is not None:
                if isinstance(content_snapshot, str):
                    try:
                        value = json.loads(content_snapshot)
                    except ValueError:
                        value = {}
                else:
                    value = content_snapshot or {}
        except Exception:
            pass

        return value, content_type

    def load_selected_slot_artifacts_resolved_with_order(self, session_id: str) -> List[Dict[str, Any]]:
        """Return selected slot artifacts with resolved values and sort_order.

        Combines load_selected_slot_artifacts_with_order (raw rows + sort_order) with the
        value-resolution logic from resolve_slot_revision_value.

        Returns a list of dicts with keys:
          artifact_key, sort_order, content_type, value, is_human (bool)
        Returns empty list on any error.
        """
        try:
            raw_rows = self.load_selected_slot_artifacts_with_order(session_id)
            if not raw_rows:
                return []
            out: List[Dict[str, Any]] = []
            for r in raw_rows:
                artifact_key = r.get('artifact_key', '')
                sort_order = r.get('sort_order') or 1
                is_human = bool(r.get('human_artifact_id'))
                value, content_type = self.resolve_slot_revision_value(r)
                if value is None:
                    continue
                out.append({
                    'artifact_key': artifact_key,
                    'sort_order': sort_order,
                    'content_type': content_type,
                    'value': value,
                    'is_human': is_human,
                })
            return out
        except Exception:
            return []

    def load_selected_slot_artifacts(self, session_id: str) -> List[Dict[str, Any]]:
        """Return the currently-selected slot values for a plugin session.

        Value resolution priority (mirrors enrichSlots in Go):
          1. human_artifact_id IS NOT NULL → read from plugin_human_artifacts.
          2. artifact_seq IS NOT NULL      → read from sub_agent_artifacts by exact seq.
          3. content_snapshot IS NOT NULL  → legacy fallback for pre-migration rows.

        Only revisions that resolve to a non-NULL value are returned.
        Returns empty list on any error.
        """
        try:
            with self._conn() as conn:
                rows = conn.execute(
                    text(
                        'SELECT '
                        '  psr.artifact_key, '
                        '  psr.list_index, '
                        '  psr.artifact_seq, '
                        '  psr.human_artifact_id, '
                        '  psr.content_snapshot, '
                        '  pss.task_id '
                        'FROM plugin_slot_revisions psr '
                        'LEFT JOIN plugin_session_steps pss '
                        '  ON pss.session_id = psr.session_id '
                        '  AND pss.step_id   = psr.step_id '
                        '  AND pss.attempt   = psr.attempt '
                        'WHERE psr.session_id = :session_id '
                        '  AND psr.selected = TRUE '
                        'ORDER BY psr.artifact_key ASC, COALESCE(psr.list_index, -1) ASC'
                    ),
                    {'session_id': session_id},
                ).mappings().all()
            out: List[Dict[str, Any]] = []
            for r in rows:
                value: Any = None
                content_type: Optional[str] = None

                human_artifact_id = r['human_artifact_id']
                artifact_seq = r['artifact_seq']
                task_id = r['task_id']

                if human_artifact_id:
                    # Human revision: read from plugin_human_artifacts.
                    with self._conn() as conn2:
                        ha_row = conn2.execute(
                            text(
                                'SELECT value, content_type FROM plugin_human_artifacts '
                                'WHERE id = :id'
                            ),
                            {'id': human_artifact_id},
                        ).mappings().first()
                    if ha_row is not None:
                        raw = ha_row['value']
                        content_type = ha_row['content_type']
                        if isinstance(raw, str):
                            try:
                                value = json.loads(raw)
                            except ValueError:
                                value = {}
                        else:
                            value = raw or {}
                elif artifact_seq is not None and task_id:
                    # AI revision: load from sub_agent_artifacts by exact seq.
                    with self._conn() as conn2:
                        art_row = conn2.execute(
                            text(
                                'SELECT value, content_type FROM sub_agent_artifacts '
                                'WHERE task_id = :tid AND artifact_key = :key AND seq = :seq'
                            ),
                            {'tid': task_id, 'key': r['artifact_key'], 'seq': artifact_seq},
                        ).mappings().first()
                    if art_row is not None:
                        raw = art_row['value']
                        content_type = art_row['content_type']
                        if isinstance(raw, str):
                            try:
                                value = json.loads(raw)
                            except ValueError:
                                value = {}
                        else:
                            value = raw or {}
                else:
                    # Legacy fallback: content_snapshot for pre-migration rows.
                    snapshot = r['content_snapshot']
                    if snapshot is None:
                        continue
                    if isinstance(snapshot, str):
                        try:
                            value = json.loads(snapshot)
                        except ValueError:
                            value = {}
                    else:
                        value = snapshot or {}

                if value is None:
                    continue
                out.append({
                    'artifact_key': r['artifact_key'],
                    'content_type': content_type,
                    'value': value,
                    'list_index': r['list_index'],
                })
            return out
        except Exception:
            return []

    def format_plugin_session_artifacts(self, session_id: str) -> List[str]:
        rows = self.load_selected_slot_artifacts_resolved_with_order(session_id)
        return _rows_to_artifact_summary(rows) if rows else []

    def format_task_artifacts(self, task_ids: List[str]) -> List[str]:
        rows = self.load_artifacts_for_tasks(task_ids)
        return _rows_to_artifact_summary(rows, order_field='seq', is_human_field=None) if rows else []


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
    """Accessor for sub_agent_tasks / sub_agent_artifacts used by ChatAgent tools.

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
                        '       summary, seq_in_conversation, output_artifact_keys, params '
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
                'params': r['params'],
            })
        return tasks

    def format_plugin_session_artifacts(self, session_id: str) -> List[str]:
        rows = self.load_plugin_session_slot_summary(session_id)
        return _rows_to_artifact_summary(rows) if rows else []

    def load_artifacts_for_tasks(self, task_ids: List[str]) -> List[Dict[str, Any]]:
        """Return non-hidden artifacts for a list of task_ids, ordered by task_id / key / seq ASC.

        Returns empty list on any error or if task_ids is empty.
        """
        if not task_ids:
            return []
        try:
            with self._conn() as conn:
                rows = conn.execute(
                    text(
                        'SELECT task_id, artifact_key, content_type, value, seq '
                        'FROM sub_agent_artifacts '
                        'WHERE task_id IN :ids AND hidden = FALSE '
                        'ORDER BY task_id, artifact_key, seq ASC'
                    ).bindparams(bindparam('ids', expanding=True)),
                    {'ids': task_ids},
                ).mappings().all()
            out: List[Dict[str, Any]] = []
            for r in rows:
                value = r['value']
                if isinstance(value, str):
                    try:
                        value = json.loads(value)
                    except ValueError:
                        value = {}
                out.append({
                    'task_id': r['task_id'],
                    'artifact_key': r['artifact_key'],
                    'content_type': r['content_type'],
                    'value': value,
                    'seq': r['seq'],
                })
            return out
        except Exception:
            return []

    def format_task_artifacts(self, task_ids: List[str]) -> List[str]:
        rows = self.load_artifacts_for_tasks(task_ids)
        return _rows_to_artifact_summary(rows, order_field='seq', is_human_field=None) if rows else []

    def build_chat_agent_task_context(self, conv_id: str) -> str:
        """Build the ## Tasks system-prompt section for ChatAgent.

        For each task in the conversation (plugin_step regardless of status,
        ordinary tasks only when terminal):
        - plugin_step → format_plugin_session_artifacts (plugin_slot_revisions)
        - ordinary    → format_task_artifacts (sub_agent_artifacts)
        Returns '' on any error or when there is nothing to show.
        """
        try:
            tasks = self.list_tasks_by_conversation(conv_id)
        except Exception:
            return ''
        if not tasks:
            return ''
        terminal = {'succeeded', 'failed', 'interrupted'}
        lines = ['## Tasks (real-time state — user may have added or removed items since earlier '
                 'in this conversation; treat this list as the single source of truth)']
        for t in tasks:
            status = str(t.get('status') or '')
            agent_type = str(t.get('agent_type') or '')
            # plugin_step tasks may still be running but have partial artifacts — always include.
            # Ordinary tasks only matter once they've reached a terminal state.
            if agent_type != 'plugin_step' and status not in terminal:
                continue
            seq = t.get('seq_in_conversation', '')
            title = str(t.get('title') or '')
            task_ref = f'{seq}. {title}' if seq else title
            summary = str(t.get('summary') or '').strip()
            status_label = {'succeeded': 'done', 'failed': 'failed',
                            'interrupted': 'interrupted',
                            'running': 'in progress', 'pending': 'pending'}.get(status, status)
            header = f'- Task {task_ref} [{status_label}]'
            if summary:
                header += f': {summary}'
            lines.append(header)

            agent_type = str(t.get('agent_type') or '')
            if agent_type == 'plugin_step':
                # Plugin step artifacts are already injected via _build_session_artifact_section.
                # Only show progress summary here to avoid duplicate / misleading context.
                art_lines = []
            else:
                art_lines = self.format_task_artifacts([t['id']])
            lines.extend(f'  {ln}' for ln in art_lines)

        if len(lines) == 1:
            return ''
        return '\n'.join(lines)

    def load_plugin_session_slot_summary(self, session_id: str) -> List[Dict[str, Any]]:
        """Return selected slot artifacts for a plugin session, resolved with sort_order.

        Returns a list of dicts with keys:
          artifact_key, sort_order, content_type, value, is_human (bool)
        Returns empty list on any error or when session has no selected artifacts.

        Uses the same resolution logic as SubAgentDB.load_selected_slot_artifacts_resolved_with_order
        but runs on the shared TaskQueryDB engine (no per-request DSN needed).
        """
        try:
            with self._conn() as conn:
                rows = conn.execute(
                    text(
                        'SELECT '
                        '  psr.artifact_key, '
                        '  psr.list_index, '
                        '  psr.artifact_seq, '
                        '  psr.human_artifact_id, '
                        '  psr.content_snapshot, '
                        '  psr.change_source, '
                        '  pss.task_id, '
                        '  COALESCE(pos.sort_order, psr.list_index + 1) AS sort_order '
                        'FROM plugin_slot_revisions psr '
                        'LEFT JOIN plugin_session_steps pss '
                        '  ON pss.session_id = psr.session_id '
                        '  AND pss.step_id   = psr.step_id '
                        '  AND pss.attempt   = psr.attempt '
                        'LEFT JOIN ( '
                        '  SELECT slot_id, val::int AS list_index, '
                        '         (ord - 1 + 1) AS sort_order '
                        '  FROM plugin_slot_order, '
                        '       jsonb_array_elements_text(order_list) '
                        '       WITH ORDINALITY AS t(val, ord) '
                        '  WHERE session_id = :session_id '
                        ') pos ON pos.slot_id = psr.slot_id '
                        '      AND pos.list_index = psr.list_index '
                        'WHERE psr.session_id = :session_id '
                        '  AND psr.selected = TRUE '
                        'ORDER BY psr.artifact_key ASC, '
                        '         COALESCE(pos.sort_order, psr.list_index + 1) ASC'
                    ),
                    {'session_id': session_id},
                ).mappings().all()
        except Exception:
            return []

        out: List[Dict[str, Any]] = []
        for r in rows:
            artifact_key = r.get('artifact_key', '')
            sort_order = r.get('sort_order') or 1
            is_human = bool(r.get('human_artifact_id'))

            value: Any = None
            content_type: Optional[str] = None
            human_artifact_id = r.get('human_artifact_id')
            artifact_seq = r.get('artifact_seq')
            task_id = r.get('task_id')

            try:
                if human_artifact_id:
                    with self._conn() as conn2:
                        ha = conn2.execute(
                            text(
                                'SELECT value, content_type FROM plugin_human_artifacts '
                                'WHERE id = :id'
                            ),
                            {'id': human_artifact_id},
                        ).mappings().first()
                    if ha is not None:
                        raw = ha['value']
                        content_type = ha['content_type']
                        value = json.loads(raw) if isinstance(raw, str) else (raw or {})
                elif artifact_seq is not None and task_id:
                    with self._conn() as conn2:
                        ar = conn2.execute(
                            text(
                                'SELECT value, content_type FROM sub_agent_artifacts '
                                'WHERE task_id = :tid AND artifact_key = :key AND seq = :seq'
                            ),
                            {'tid': task_id, 'key': artifact_key, 'seq': artifact_seq},
                        ).mappings().first()
                    if ar is not None:
                        raw = ar['value']
                        content_type = ar['content_type']
                        value = json.loads(raw) if isinstance(raw, str) else (raw or {})
                elif r.get('content_snapshot') is not None:
                    snap = r['content_snapshot']
                    value = json.loads(snap) if isinstance(snap, str) else (snap or {})
            except Exception:
                pass

            if value is None:
                continue
            out.append({
                'artifact_key': artifact_key,
                'sort_order': sort_order,
                'content_type': content_type,
                'value': value,
                'is_human': is_human,
            })
        return out

    # ----- intent / constraint helpers -----

    def get_session_intent(self, session_id: str) -> Optional[str]:
        """Return the global intent_context text for a session, or None if not set."""
        try:
            with self._conn() as conn:
                row = conn.execute(
                    text('SELECT intent_context FROM plugin_sessions WHERE id = :sid'),
                    {'sid': session_id},
                ).mappings().first()
            if row is None:
                return None
            raw = row['intent_context']
            if raw is None:
                return None
            data = json.loads(raw) if isinstance(raw, str) else raw
            return data.get('text') or data.get('content') if isinstance(data, dict) else str(data) or None
        except Exception:
            return None

    def get_step_intent(self, session_id: str, step_id: str) -> Optional[str]:
        """Return the step-level intent_context text, or None if not set."""
        try:
            with self._conn() as conn:
                row = conn.execute(
                    text(
                        'SELECT intent_context FROM plugin_step_intents '
                        'WHERE session_id = :sid AND step_id = :step'
                    ),
                    {'sid': session_id, 'step': step_id},
                ).mappings().first()
            if row is None:
                return None
            raw = row['intent_context']
            if raw is None:
                return None
            data = json.loads(raw) if isinstance(raw, str) else raw
            return data.get('text') or data.get('content') if isinstance(data, dict) else str(data) or None
        except Exception:
            return None

    def list_step_intents(self, session_id: str) -> Dict[str, str]:
        """Return all step-level intent texts for a session as {step_id: text}."""
        try:
            with self._conn() as conn:
                rows = conn.execute(
                    text(
                        'SELECT step_id, intent_context FROM plugin_step_intents '
                        'WHERE session_id = :sid'
                    ),
                    {'sid': session_id},
                ).mappings().all()
            result: Dict[str, str] = {}
            for row in rows:
                raw = row['intent_context']
                if raw is None:
                    continue
                data = json.loads(raw) if isinstance(raw, str) else raw
                text_val = data.get('text') or data.get('content') if isinstance(data, dict) else str(data)
                if text_val:
                    result[row['step_id']] = text_val
            return result
        except Exception:
            return {}

    def get_step_artifacts(self, session_id: str, step_id: str) -> Dict[str, Any]:
        """Return artifact key→value dict for a step (latest seq per key, non-hidden)."""
        try:
            with self._conn() as conn:
                rows = conn.execute(
                    text(
                        'SELECT sa.artifact_key, sa.content_type, sa.value '
                        'FROM sub_agent_artifacts sa '
                        'JOIN plugin_session_steps pss ON pss.task_id = sa.task_id '
                        'WHERE pss.session_id = :sid AND pss.step_id = :step '
                        '  AND sa.hidden = FALSE '
                        'ORDER BY sa.artifact_key, sa.seq DESC'
                    ),
                    {'sid': session_id, 'step': step_id},
                ).mappings().all()
            seen: set = set()
            out: Dict[str, Any] = {}
            for r in rows:
                key = r['artifact_key']
                if key in seen:
                    continue
                seen.add(key)
                raw = r['value']
                val = json.loads(raw) if isinstance(raw, str) else (raw or {})
                ct = r['content_type'] or 'text'
                out[key] = artifact_summary_line(val, ct, is_human=False)
            return out
        except Exception:
            return {}


# ---------------------------------------------------------------------------
# Shared artifact formatting utilities
# Used by both SubAgent (runner.py) and ChatAgent (plugin_manager.py).
# ---------------------------------------------------------------------------

_ARTIFACT_SUMMARY_LIMIT = 200  # chars for inline text/json preview


def artifact_summary_line(value: Any, content_type: Optional[str], is_human: bool) -> str:
    """Return a one-line summary for a single artifact value."""
    suffix = ' (by user)' if is_human else ''
    ct = (content_type or '').lower()
    if ct in ('image', 'file', 'file_list'):
        if isinstance(value, dict):
            name = value.get('filename') or value.get('path', '').split('/')[-1] or '(file)'
            caption = value.get('caption') or ''
            label = f'{name} — {caption}' if caption else name
        else:
            label = str(value)
        return f'{label}{suffix}'
    # text / json — inline preview
    if isinstance(value, dict):
        text_val = value.get('text') or ''
        if not text_val and 'data' in value:
            text_val = json.dumps(value['data'], ensure_ascii=False)
        if not text_val:
            text_val = json.dumps(value, ensure_ascii=False)
    else:
        text_val = str(value) if value else ''
    if len(text_val) <= _ARTIFACT_SUMMARY_LIMIT:
        return f'{text_val}{suffix}'
    return f'{text_val[:_ARTIFACT_SUMMARY_LIMIT]}...{suffix} (use get_artifact to read full content)'


def format_artifact_summary(
    key_items: Dict[str, List[tuple]],
    key_content_type: Dict[str, str],
) -> List[str]:
    """Format collected artifact items into summary block lines.

    Each tuple in key_items[key] is (sort_order, content_type, value, is_human).
    Returns a list of lines starting with an 'Available artifacts' header.
    """
    lines = ['Available artifacts (use get_artifact to retrieve content):']
    for key in sorted(key_items.keys()):
        items = sorted(key_items[key], key=lambda t: t[0])
        ct_label = key_content_type.get(key, 'unknown')
        count = len(items)
        if count > 1:
            header = f'- "{key}" [{ct_label}, {count} items]:'
        else:
            header = f'- "{key}" [{ct_label}]:'
        lines.append(header)
        for sort_order, ct, value, is_human in items:
            summary = artifact_summary_line(value, ct, is_human)
            if count > 1:
                lines.append(f'    [{sort_order}] {summary}')
            else:
                lines.append(f'    {summary}')
    return lines


def _rows_to_artifact_summary(
    rows: List[Dict[str, Any]],
    order_field: str = 'sort_order',
    is_human_field: Optional[str] = 'is_human',
) -> List[str]:
    from collections import defaultdict
    key_items: Dict[str, List[tuple]] = defaultdict(list)
    key_content_type: Dict[str, str] = {}
    for r in rows:
        key = r.get('artifact_key', '')
        if not key:
            continue
        ct = r.get('content_type') or ''
        value = r.get('value') or {}
        order = r.get(order_field) or 1
        is_human = bool(r.get(is_human_field)) if is_human_field else False
        key_items[key].append((order, ct, value, is_human))
        if ct and key not in key_content_type:
            key_content_type[key] = ct
    return format_artifact_summary(key_items, key_content_type)

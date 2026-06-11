from __future__ import annotations

import logging
import threading
from contextlib import contextmanager
from typing import Any, Generator

from lazymind.config import config

logger = logging.getLogger(__name__)

PLUGIN_DIR: str = config['plugin_dir']
PLUGIN_WORKSPACE_BASE: str = config['plugin_workspace_base']

_engine_lock = threading.Lock()
_engine_cache: dict[str, Any] = {}


def _get_plugin_db_engine():
    from sqlalchemy import create_engine
    from lazymind.common.postgres import normalize_postgres_sqlalchemy_url

    db_url: str = (config['core_database_url'] or config['database_url'] or '').strip()
    if not db_url:
        return None

    try:
        db_url = normalize_postgres_sqlalchemy_url(db_url)
    except Exception:
        pass

    engine = _engine_cache.get(db_url)
    if engine is not None:
        return engine
    with _engine_lock:
        engine = _engine_cache.get(db_url)
        if engine is None:
            engine = create_engine(db_url, future=True, pool_pre_ping=True)
            _engine_cache[db_url] = engine
    return engine


@contextmanager
def _plugin_db_session() -> Generator[Any, None, None]:
    """Context manager that yields a SQLAlchemy connection for plugin dependency checks."""
    engine = _get_plugin_db_engine()
    if engine is None:
        yield None
        return
    with engine.connect() as conn:
        yield conn


def get_db_session_factory():
    """Return a callable context manager factory for use in plugin trigger tools.

    Returns None when no database URL is configured (tests / local dev without DB).
    """
    engine = _get_plugin_db_engine()
    if engine is None:
        return None
    return _plugin_db_session


def load_execution_path(session_id: str) -> list[dict]:
    """Query plugin_session_steps chronologically and return the full execution path.

    Each entry: {"step_id": str, "status": str, "summary": str}.
    Returns [] when the DB is unavailable or the session has no records.

    The summary is taken from the step_summary artifact for the execution.
    If no artifact exists and the step is interrupted/running, a checkpoint
    fallback string is built instead.
    """
    engine = _get_plugin_db_engine()
    if engine is None:
        return []

    try:
        from sqlalchemy import text
        with engine.connect() as conn:
            # 1. Fetch all executions in order.
            step_rows = conn.execute(
                text(
                    'SELECT id, step, step_status FROM plugin_session_steps '
                    'WHERE session_id = :sid ORDER BY created_at ASC'
                ),
                {'sid': session_id},
            ).fetchall()

            if not step_rows:
                return []

            exec_ids = [r[0] for r in step_rows]

            # 2. Batch-fetch step_summary artifacts for all executions.
            placeholders = ', '.join(f':eid{i}' for i in range(len(exec_ids)))
            params: dict = {'sid': session_id}
            params.update({f'eid{i}': eid for i, eid in enumerate(exec_ids)})
            artifact_rows = conn.execute(
                text(
                    'SELECT step_exec_id, value FROM plugin_session_artifacts '
                    "WHERE session_id = :sid AND artifact_id = 'step_summary' "
                    f'AND step_exec_id IN ({placeholders}) '
                    'ORDER BY created_at DESC'
                ),
                params,
            ).fetchall()

            # Keep only the first (newest) summary per exec_id.
            summary_by_exec: dict[str, str] = {}
            for row in artifact_rows:
                exec_id, raw_val = row[0], row[1]
                if exec_id not in summary_by_exec:
                    # raw_val is already a Python object from SQLAlchemy JSONB
                    summary_by_exec[exec_id] = str(raw_val) if raw_val is not None else ''

            # 3. Build entries; fallback to checkpoint for interrupted/running.
            entries = []
            for exec_id, step, status in step_rows:
                summary = summary_by_exec.get(exec_id, '')
                if not summary and status in ('interrupted', 'running'):
                    cp_row = conn.execute(
                        text(
                            'SELECT completed_count, total_count, phase_note '
                            'FROM plugin_session_step_checkpoints '
                            'WHERE step_exec_id = :eid ORDER BY sequence DESC LIMIT 1'
                        ),
                        {'eid': exec_id},
                    ).fetchone()
                    if cp_row and (cp_row[0] or cp_row[1]):
                        summary = f'interrupted, completed {cp_row[0]}/{cp_row[1]}'
                        if cp_row[2]:
                            summary += f', phase: {cp_row[2]}'
                    else:
                        summary = 'interrupted'

                entries.append({'step_id': step, 'status': status, 'summary': summary})

            return entries

    except Exception as exc:
        logger.warning('load_execution_path: DB query failed (%s), returning []', exc)
        return []


def load_step_artifacts(session_id: str) -> dict:
    """Return the latest value for every artifact_id in the session.

    Returns {} when the DB is unavailable or no artifacts exist.
    """
    engine = _get_plugin_db_engine()
    if not engine or not session_id:
        return {}
    try:
        from sqlalchemy import text
        with engine.connect() as conn:
            rows = conn.execute(
                text(
                    'SELECT artifact_id, value FROM plugin_session_artifacts '
                    'WHERE session_id = :sid ORDER BY created_at DESC'
                ),
                {'sid': session_id},
            ).fetchall()
        result: dict = {}
        for artifact_id, raw_val in rows:
            if artifact_id not in result:
                result[artifact_id] = raw_val  # JSONB already decoded by SQLAlchemy
        return result
    except Exception as exc:
        logger.warning('load_step_artifacts: DB query failed (%s), returning {}', exc)
        return {}


def load_step_checkpoint(session_id: str, step_id: str) -> dict:
    """Return the latest checkpoint for the most recent *interrupted* execution
    of the given step. Returns {} when none exists (first run or step never interrupted).

    Note: intentionally excludes 'running' status. Go inserts a new 'running' record
    before calling /api/plugin/step, so querying 'running' would find the brand-new
    empty record rather than the previous interrupted one with actual checkpoint data.
    """
    engine = _get_plugin_db_engine()
    if not engine or not session_id or not step_id:
        return {}
    try:
        from sqlalchemy import text
        with engine.connect() as conn:
            step_row = conn.execute(
                text(
                    'SELECT id FROM plugin_session_steps '
                    'WHERE session_id = :sid AND step = :step '
                    "AND step_status = 'interrupted' "
                    'ORDER BY created_at DESC LIMIT 1'
                ),
                {'sid': session_id, 'step': step_id},
            ).fetchone()
            if not step_row:
                return {}
            exec_id = step_row[0]
            cp_row = conn.execute(
                text(
                    'SELECT completed_count, total_count, phase_note, partial_results '
                    'FROM plugin_session_step_checkpoints '
                    'WHERE step_exec_id = :eid ORDER BY sequence DESC LIMIT 1'
                ),
                {'eid': exec_id},
            ).fetchone()
            if not cp_row:
                return {}
            result: dict = {
                'completed_count': cp_row[0] or 0,
                'total_count': cp_row[1] or 0,
                'phase_note': cp_row[2] or '',
            }
            if cp_row[3] is not None:
                result['partial_results'] = cp_row[3]  # already decoded by SQLAlchemy
            return result
    except Exception as exc:
        logger.warning('load_step_checkpoint: DB query failed (%s), returning {}', exc)
        return {}


def load_previous_step_summary(session_id: str, step_id: str) -> str:
    """Return the step_summary artifact from the most recent *done* execution of the step.

    Returns '' when no completed execution exists (first run).
    """
    engine = _get_plugin_db_engine()
    if not engine or not session_id or not step_id:
        return ''
    try:
        from sqlalchemy import text
        with engine.connect() as conn:
            step_row = conn.execute(
                text(
                    'SELECT id FROM plugin_session_steps '
                    "WHERE session_id = :sid AND step = :step AND step_status = 'done' "
                    'ORDER BY created_at DESC LIMIT 1'
                ),
                {'sid': session_id, 'step': step_id},
            ).fetchone()
            if not step_row:
                return ''
            exec_id = step_row[0]
            art_row = conn.execute(
                text(
                    'SELECT value FROM plugin_session_artifacts '
                    'WHERE session_id = :sid AND step_exec_id = :eid '
                    "AND artifact_id = 'step_summary' "
                    'ORDER BY created_at DESC LIMIT 1'
                ),
                {'sid': session_id, 'eid': exec_id},
            ).fetchone()
            if not art_row or art_row[0] is None:
                return ''
            return str(art_row[0])
    except Exception as exc:
        logger.warning('load_previous_step_summary: DB query failed (%s), returning ""', exc)
        return ''


def load_current_step(session_id: str) -> str:
    """Return plugin_sessions.current_step_id for the given session.

    Returns '' when the DB is unavailable or the session does not exist.
    """
    engine = _get_plugin_db_engine()
    if not engine or not session_id:
        return ''
    try:
        from sqlalchemy import text
        with engine.connect() as conn:
            row = conn.execute(
                text('SELECT current_step_id FROM plugin_sessions WHERE id = :sid'),
                {'sid': session_id},
            ).fetchone()
        return (row[0] or '') if row else ''
    except Exception as exc:
        logger.warning('load_current_step: DB query failed (%s), returning ""', exc)
        return ''


def load_plugin_info(session_id: str) -> dict:
    """Return {'plugin_id': str, 'step_id': str} for the given session.

    step_id is current_step_id from plugin_sessions.
    Returns {'plugin_id': '', 'step_id': ''} when unavailable.
    """
    engine = _get_plugin_db_engine()
    if not engine or not session_id:
        return {'plugin_id': '', 'step_id': ''}
    try:
        from sqlalchemy import text
        with engine.connect() as conn:
            row = conn.execute(
                text('SELECT plugin_id, current_step_id FROM plugin_sessions WHERE id = :sid'),
                {'sid': session_id},
            ).fetchone()
        if not row:
            return {'plugin_id': '', 'step_id': ''}
        return {'plugin_id': row[0] or '', 'step_id': row[1] or ''}
    except Exception as exc:
        logger.warning('load_plugin_info: DB query failed (%s), returning empty', exc)
        return {'plugin_id': '', 'step_id': ''}


def load_attempt_count(session_id: str, step_id: str) -> int:
    """Return the number of times the given step has been attempted in this session.

    Counts all execution records (done + failed + interrupted + running).
    Returns 1 as a safe default when the DB is unavailable.
    """
    engine = _get_plugin_db_engine()
    if not engine or not session_id or not step_id:
        return 1
    try:
        from sqlalchemy import text
        with engine.connect() as conn:
            row = conn.execute(
                text(
                    'SELECT COUNT(*) FROM plugin_session_steps '
                    'WHERE session_id = :sid AND step = :step'
                ),
                {'sid': session_id, 'step': step_id},
            ).fetchone()
        return max(1, int(row[0])) if row else 1
    except Exception as exc:
        logger.warning('load_attempt_count: DB query failed (%s), returning 1', exc)
        return 1

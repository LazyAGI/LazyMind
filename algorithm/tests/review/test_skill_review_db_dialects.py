from __future__ import annotations

import json
import importlib.util
import sys
import types
from pathlib import Path
from typing import Any

from sqlalchemy import create_engine, text

_ROOT = Path(__file__).parents[3]


class SkillReviewRunStat:
    def __init__(
        self,
        *,
        id: str,
        requestid: str = '',
        userid: str = '',
        status: str,
        started_at: str,
        duration_ms: int = 0,
        summary: dict[str, Any] | None = None,
    ) -> None:
        self.id = id
        self.requestid = requestid
        self.userid = userid
        self.status = status
        self.started_at = started_at
        self.duration_ms = duration_ms
        self.summary = summary or {}

    @classmethod
    def model_validate(cls, item: Any) -> 'SkillReviewRunStat':
        if isinstance(item, cls):
            return item
        return cls(**item)


def _load_review_modules(monkeypatch):
    monkeypatch.setitem(sys.modules, 'lazymind', types.ModuleType('lazymind'))
    monkeypatch.setitem(sys.modules, 'lazymind.chat', types.ModuleType('lazymind.chat'))
    monkeypatch.setitem(sys.modules, 'lazymind.chat.service', types.ModuleType('lazymind.chat.service'))
    monkeypatch.setitem(
        sys.modules,
        'lazymind.chat.service.component',
        types.ModuleType('lazymind.chat.service.component'),
    )
    history = types.ModuleType('lazymind.chat.service.component.history')
    history.normalize_history_for_agent = lambda messages: messages
    monkeypatch.setitem(sys.modules, 'lazymind.chat.service.component.history', history)

    common_database_postgres = types.ModuleType('lazymind.common.database.postgres')
    common_database_postgres.normalize_postgres_sqlalchemy_url = lambda url: url
    common_database_postgres.sqlalchemy_engine_options = lambda _url: {}
    monkeypatch.setitem(sys.modules, 'lazymind.common', types.ModuleType('lazymind.common'))
    monkeypatch.setitem(sys.modules, 'lazymind.common.database', types.ModuleType('lazymind.common.database'))
    monkeypatch.setitem(sys.modules, 'lazymind.common.database.postgres', common_database_postgres)

    config_module = types.ModuleType('lazymind.config')
    config_module.config = {'core_database_url': '', 'database_url': ''}
    monkeypatch.setitem(sys.modules, 'lazymind.config', config_module)

    monkeypatch.setitem(sys.modules, 'lazymind.review', types.ModuleType('lazymind.review'))
    monkeypatch.setitem(sys.modules, 'lazymind.review.skill_review', types.ModuleType('lazymind.review.skill_review'))
    schema_module = types.ModuleType('lazymind.review.skill_review.schemas')
    schema_module.SkillReviewRunStat = SkillReviewRunStat
    monkeypatch.setitem(sys.modules, 'lazymind.review.skill_review.schemas', schema_module)

    review_db = _load_module(
        'lazymind.review.skill_review.db',
        _ROOT / 'algorithm/lazymind/review/skill_review/db.py',
    )
    monkeypatch.setitem(sys.modules, 'lazymind.review.skill_review.db', review_db)
    organize_db = _load_module(
        'lazymind.review.skill_organize.db',
        _ROOT / 'algorithm/lazymind/review/skill_organize/db.py',
    )
    return review_db, organize_db


def _load_module(name: str, path: Path):
    spec = importlib.util.spec_from_file_location(name, path)
    module = importlib.util.module_from_spec(spec)
    assert spec and spec.loader
    spec.loader.exec_module(module)
    return module


def _sqlite_engine():
    engine = create_engine('sqlite:///:memory:', future=True)
    with engine.begin() as conn:
        conn.execute(text(
            """
            CREATE TABLE conversations (
                id TEXT PRIMARY KEY,
                create_user_id TEXT NOT NULL
            )
            """
        ))
        conn.execute(text(
            """
            CREATE TABLE chat_histories (
                id TEXT PRIMARY KEY,
                conversation_id TEXT NOT NULL,
                seq INTEGER NOT NULL,
                create_time TEXT NOT NULL,
                role TEXT,
                content TEXT,
                result TEXT,
                messages TEXT
            )
            """
        ))
        conn.execute(text(
            """
            CREATE TABLE skill_review_stats (
                id TEXT PRIMARY KEY,
                requestid TEXT NOT NULL,
                userid TEXT NOT NULL,
                status TEXT NOT NULL,
                started_at TEXT,
                duration_ms INTEGER NOT NULL,
                summary TEXT
            )
            """
        ))
    return engine


def test_skill_review_db_reads_sessions_with_sqlite(monkeypatch):
    review_db, _ = _load_review_modules(monkeypatch)
    engine = _sqlite_engine()
    monkeypatch.setattr(review_db, '_get_app_conn', lambda: engine)
    with engine.begin() as conn:
        conn.execute(
            text('INSERT INTO conversations(id, create_user_id) VALUES (:id, :user_id)'),
            [{'id': 'c1', 'user_id': 'u1'}, {'id': 'c2', 'user_id': 'u2'}],
        )
        conn.execute(
            text(
                """
                INSERT INTO chat_histories
                    (id, conversation_id, seq, create_time, role, content, result, messages)
                VALUES
                    (:id, :conversation_id, :seq, :create_time, :role, :content, :result, :messages)
                """
            ),
            {
                'id': 'h1',
                'conversation_id': 'c1',
                'seq': 1,
                'create_time': '2026-08-07T00:00:00Z',
                'role': 'user',
                'content': 'hello',
                'result': '',
                'messages': None,
            },
        )

    sessions = review_db.read_session(['c1', 'c2'], ['u1'])

    assert [item['conversation_id'] for item in sessions] == ['c1']
    assert sessions[0]['create_user_id'] == 'u1'
    assert sessions[0]['messages'][0]['content'] == 'hello'


def test_skill_review_run_stats_upsert_with_sqlite(monkeypatch):
    review_db, _ = _load_review_modules(monkeypatch)
    engine = _sqlite_engine()
    monkeypatch.setattr(review_db, '_get_app_conn', lambda: engine)

    review_db.insert_skill_review_run_stats(SkillReviewRunStat(
        id='stat-1',
        requestid='req-1',
        userid='u1',
        status='completed',
        started_at='2026-08-07T00:00:00Z',
        duration_ms=12,
        summary={'count': 1},
    ))
    review_db.insert_skill_review_run_stats(SkillReviewRunStat(
        id='stat-1',
        requestid='req-2',
        userid='u1',
        status='failed',
        started_at='2026-08-07T00:00:01Z',
        duration_ms=34,
        summary={'count': 2},
    ))

    with engine.connect() as conn:
        row = conn.execute(
            text("SELECT requestid, status, duration_ms, summary FROM skill_review_stats WHERE id = 'stat-1'")
        ).mappings().one()

    assert row['requestid'] == 'req-2'
    assert row['status'] == 'failed'
    assert row['duration_ms'] == 34
    assert json.loads(row['summary']) == {'count': 2}


def test_skill_organize_result_upsert_with_sqlite(monkeypatch):
    _, organize_db = _load_review_modules(monkeypatch)
    engine = _sqlite_engine()
    monkeypatch.setattr(organize_db, '_get_app_conn', lambda: engine)

    organize_db.insert_skill_organize_result(
        record_id='organize-1',
        requestid='req-1',
        user_id='u1',
        organize_result={'status': 'organize_apply', 'created_at': '2026-08-07T00:00:00Z', 'items': [1]},
    )
    organize_db.insert_skill_organize_result(
        record_id='organize-1',
        requestid='req-2',
        user_id='u2',
        organize_result={'status': 'completed', 'created_at': '2026-08-07T00:00:01Z', 'items': [2]},
    )

    with engine.connect() as conn:
        row = conn.execute(
            text("SELECT requestid, userid, status, summary FROM skill_review_stats WHERE id = 'organize-1'")
        ).mappings().one()

    assert row['requestid'] == 'req-2'
    assert row['userid'] == 'u2'
    assert row['status'] == 'completed'
    assert json.loads(row['summary'])['items'] == [2]

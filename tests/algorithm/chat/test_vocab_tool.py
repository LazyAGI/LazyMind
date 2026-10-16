from __future__ import annotations

import os
import sys
import importlib


_ALGO = os.path.join(os.path.dirname(__file__), '..', '..', '..', 'algorithm')
_LAZYLLM_ROOT = os.path.join(_ALGO, 'lazyllm')
if _ALGO not in sys.path:
    sys.path.insert(0, _ALGO)
if _LAZYLLM_ROOT not in sys.path:
    sys.path.insert(0, _LAZYLLM_ROOT)

from lazymind.chat.engine.tools.infra import resolve_vocab_user_id  # noqa: E402
from lazymind.chat.engine.tools.infra import vocab_db  # noqa: E402

vocab_tool = importlib.import_module('lazymind.chat.engine.tools.vocab_learn')


def test_sqlite_vocab_reads_core_table_and_filters_users_and_deleted_rows(monkeypatch):
    from sqlalchemy import create_engine, text

    engine = create_engine('sqlite://')
    errors = []
    monkeypatch.setattr(vocab_db, '_has_vocab_conn_target', lambda db_url=None: True)
    monkeypatch.setattr(vocab_db, '_get_vocab_conn', lambda db_url=None: engine)
    monkeypatch.setattr(vocab_db, '_table_ensured', False)
    monkeypatch.setattr(vocab_db.LOG, 'error', errors.append)
    with engine.begin() as conn:
        conn.execute(text('CREATE TABLE words (id TEXT, word TEXT, group_id TEXT, '
                          'create_user_id TEXT, deleted_at TEXT, description TEXT, reference_info TEXT)'))
        conn.execute(text("INSERT INTO words VALUES "
                          "('1','ICOCO','g1','u1',NULL,'conference','source'),"
                          "('2','deleted','g1','u1','2026-01-01','',''),"
                          "('3','private','g2','u2',NULL,'','')"))
    try:
        assert vocab_db.fetch_vocab_for_user_id('u1') == [{'word': 'ICOCO', 'cluster_id': 'g1'}]
        groups = vocab_db.fetch_vocab_groups_for_user_id('u1')
        assert groups == {'g1': {'group_id': 'g1', 'description': 'conference',
                                 'words': ['ICOCO'], 'references': ['source']}}
        assert not errors
    finally:
        engine.dispose()


def test_vocab_postgres_keeps_schema_qualification():
    from types import SimpleNamespace
    engine = SimpleNamespace(dialect=SimpleNamespace(name='postgresql'))
    assert vocab_db._vocab_table_name(engine) == 'public.words'


def test_fetch_chat_histories_for_timestamped_session(monkeypatch):
    seen_conversation_ids = []

    class _FakeResult:
        def mappings(self):
            return self

        def all(self):
            return [{
                'user_id': 'user-1',
                'conversation_id': 'conv-1',
                'message_id': 'm1',
                'seq': 1,
                'raw_content': '',
                'content': '请记住苹果就是apple',
                'result': '',
                'create_time': None,
            }]

    class _FakeConn:
        def __enter__(self):
            return self

        def __exit__(self, exc_type, exc, tb):
            return False

        def execute(self, _sql, params):
            seen_conversation_ids.append(params['conversation_id'])
            return _FakeResult()

    class _FakeEngine:
        def connect(self):
            return _FakeConn()

    monkeypatch.setattr(vocab_db, '_get_core_conn', lambda db_dsn=None, db_url=None: _FakeEngine())

    rows = vocab_db.fetch_chat_histories_for_session('conv-1_1778221345821')

    assert seen_conversation_ids == ['conv-1']
    assert rows == [{
        'user_id': 'user-1',
        'conversation_id': 'conv-1',
        'message_id': 'm1',
        'seq': 1,
        'raw_content': '',
        'content': '请记住苹果就是apple',
        'result': '',
        'create_time': None,
    }]


def test_resolve_user_id_reads_agentic_config(monkeypatch):
    monkeypatch.setattr(vocab_tool.lazyllm, 'globals', {'agentic_config': {'user_id': 'user-9'}})

    assert resolve_vocab_user_id(None) == 'user-9'


def test_vocab_learn_creates_group_for_new_pair(monkeypatch):
    captured = {}

    monkeypatch.setattr(vocab_tool.lazyllm, 'globals', {'agentic_config': {
        'session_id': 'sid-1',
        'user_id': 'user-1',
    }})
    monkeypatch.setattr(vocab_tool, 'fetch_vocab_groups_for_user_id', lambda user_id: {})
    monkeypatch.setattr(vocab_tool, 'fetch_chat_histories_for_session', lambda session_id: [
        {
            'user_id': 'user-1',
            'conversation_id': 'sid-1',
            'message_id': 'm1',
            'seq': 1,
            'raw_content': '',
            'content': '请记住苹果就是apple',
            'result': '',
            'create_time': None,
        },
    ])

    def _fake_post(path, payload):
        captured['path'] = path
        captured['payload'] = payload
        return {'persisted': 'core_api'}

    monkeypatch.setattr(vocab_tool, 'post_core_api', _fake_post)

    result = vocab_tool.vocab_learn([
        {'word': '苹果', 'synonym': 'apple', 'reason': 'user explicitly asked to remember it'},
    ])

    assert result['persisted'] == 'core_api'
    assert captured['path'] == '/inner/word_group:apply'
    assert captured['payload']['action_list'] == [{
        'reason': 'user explicitly asked to remember it',
        'words': ['苹果', 'apple'],
        'description': '',
        'group_ids': '[]',
        'user_id': 'user-1',
        'message_ids': '["m1"]',
        'action': 'create_new_group',
    }]


def test_vocab_learn_adds_to_group(monkeypatch):
    captured = {}

    monkeypatch.setattr(vocab_tool.lazyllm, 'globals', {'agentic_config': {'session_id': 'sid-2', 'user_id': 'user-2'}})
    monkeypatch.setattr(vocab_tool, 'fetch_vocab_groups_for_user_id', lambda user_id: {
        'g1': {'group_id': 'g1', 'words': ['民法'], 'description': '', 'references': []},
    })
    monkeypatch.setattr(vocab_tool, 'fetch_chat_histories_for_session', lambda session_id: [
        {
            'user_id': 'user-2',
            'conversation_id': 'sid-2',
            'message_id': 'm2',
            'seq': 1,
            'raw_content': '',
            'content': '这里的民法就是民事法律',
            'result': '',
            'create_time': None,
        },
    ])

    def _fake_post(path, payload):
        captured['path'] = path
        captured['payload'] = payload
        return {'persisted': 'core_api'}

    monkeypatch.setattr(vocab_tool, 'post_core_api', _fake_post)

    result = vocab_tool.vocab_learn([
        {'word': '民法', 'synonym': '民事法律', 'reason': 'user used the terms as the same concept'},
    ])

    assert result['persisted'] == 'core_api'
    assert captured['payload']['action_list'] == [{
        'reason': 'user used the terms as the same concept',
        'words': ['民事法律'],
        'description': '',
        'group_ids': '["g1"]',
        'user_id': 'user-2',
        'message_ids': '["m2"]',
        'action': 'add_to_group',
    }]


def test_vocab_learn_creates_new_group_when_domain_description_changes(monkeypatch):
    captured = {}

    monkeypatch.setattr(vocab_tool.lazyllm, 'globals', {'agentic_config': {
        'session_id': 'sid-3',
        'user_id': 'user-3',
    }})
    monkeypatch.setattr(vocab_tool, 'fetch_vocab_groups_for_user_id', lambda user_id: {
        'g-med': {'group_id': 'g-med', 'words': ['变白质'], 'description': '医学领域术语', 'references': ['["m-old"]']},
    })
    monkeypatch.setattr(vocab_tool, 'fetch_chat_histories_for_session', lambda session_id: [
        {
            'user_id': 'user-3',
            'conversation_id': 'sid-3',
            'message_id': 'm3',
            'seq': 1,
            'raw_content': '',
            'content': '请记住变白质在体育领域就是铅球垫子。',
            'result': '',
            'create_time': None,
        },
    ])

    def _fake_post(path, payload):
        captured['path'] = path
        captured['payload'] = payload
        return {'persisted': 'core_api'}

    monkeypatch.setattr(vocab_tool, 'post_core_api', _fake_post)

    result = vocab_tool.vocab_learn([
        {'word': '变白质', 'synonym': '铅球垫子', 'description': '体育领域术语', 'reason': '用户指定体育领域术语映射'},
    ])

    assert result['persisted'] == 'core_api'
    assert captured['payload']['action_list'] == [{
        'reason': '用户指定体育领域术语映射',
        'words': ['变白质', '铅球垫子'],
        'description': '体育领域术语',
        'group_ids': '[]',
        'user_id': 'user-3',
        'message_ids': '["m3"]',
        'action': 'create_new_group',
    }]

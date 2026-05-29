"""Tests for multi-user VocabManager and vocab hot-reload API.

Test categories
---------------
- TestVocabManagerBasic        : single-user VocabManager with injected data_source
- TestVocabManagerReload       : reload() refreshes the AC automaton
- TestVocabRegistry            : get_vocab_manager() per-user isolation
- TestVocabManagerThreadSafety : concurrent reload + call
- TestVocabReloadRoute         : FastAPI POST /api/vocab/reload
- TestVocabDBQueryLayer        : SQL generation for backend-managed words table
- TestVocabDBIntegration       : real PostgreSQL queries (requires core DB env)

Run (from repo root, with lazyllm env activated):
    source activate lazyllm
    cd LazyLLM && export PYTHONPATH=$PWD:$PYTHONPATH && cd ../LazyMind
    python -m pytest tests/algorithm/test_vocab_manager.py -v

Integration tests only:
    LAZYMIND_CORE_DATABASE_URL=postgresql://root:123456@10.119.24.129:5432/core \
        python -m pytest tests/algorithm/test_vocab_manager.py -v -m integration
"""
from __future__ import annotations

import importlib
import os as _os
import sys
import threading
from unittest.mock import MagicMock, patch

# ---------------------------------------------------------------------------
# Ensure algorithm/ and local lazyllm source are on sys.path before importing lazyllm
# ---------------------------------------------------------------------------
_ALGO = _os.path.join(_os.path.dirname(__file__), '..', '..', 'algorithm')
_LAZYLLM_ROOT = _os.path.join(_ALGO, 'lazyllm')
if _ALGO not in sys.path:
    sys.path.insert(0, _ALGO)
if _LAZYLLM_ROOT not in sys.path:
    sys.path.insert(0, _LAZYLLM_ROOT)

import pytest
from sqlalchemy import text
from lazyllm.module import LLMBase

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

_SAMPLE_ROWS_USER1 = [
    {'word': '苹果',  'cluster_id': '01'},
    {'word': 'apple', 'cluster_id': '01'},
    {'word': '苹果',  'cluster_id': '02'},   # same word, different cluster
    {'word': 'apple', 'cluster_id': '02'},
]

_SAMPLE_ROWS_USER2 = [
    {'word': '民法',    'cluster_id': 'g1'},
    {'word': '民事法律','cluster_id': 'g1'},
]


def _mock_llm_discriminator(*call_returns):
    model = MagicMock(spec=LLMBase)
    terminal = MagicMock()
    if len(call_returns) == 1:
        only = call_returns[0]
        if isinstance(only, list) and only and all(isinstance(x, bool) for x in only):
            terminal.return_value = only
        elif isinstance(only, list):
            terminal.side_effect = only
        else:
            terminal.return_value = only
    else:
        terminal.side_effect = list(call_returns)
    model.share.return_value.prompt.return_value.formatter.return_value = terminal
    return model, terminal


@pytest.fixture(autouse=True)
def _patch_vocab_discriminator():
    model, _ = _mock_llm_discriminator([True])
    with patch('lazymind.review.vocab.vocab_manager.AutoModel', return_value=model):
        yield


def _make_manager(rows: list, user_id: str = 'test_user'):
    """Create an isolated VocabManager using an in-memory data_source (no DB)."""
    from lazymind.review.vocab.vocab_manager import VocabManager
    return VocabManager(user_id=user_id, data_source=rows)


def _reset_registry():
    """Clear the global registry between tests."""
    from lazymind.review.service.registry import clear_registry
    clear_registry()


class _FakeMappingsResult:
    def __init__(self, *, rows=None, scalar_value=None):
        self._rows = list(rows or [])
        self._scalar_value = scalar_value

    def mappings(self):
        return self

    def all(self):
        return list(self._rows)

    def scalar(self):
        return self._scalar_value


class _FakeConnection:
    def __init__(self, *results):
        self._results = list(results)
        self.executed: list[tuple[str, dict | None]] = []

    def __enter__(self):
        return self

    def __exit__(self, exc_type, exc, tb):
        return False

    def execute(self, statement, params=None):
        self.executed.append((str(statement), params))
        if not self._results:
            raise AssertionError('unexpected execute call in fake DB connection')
        return self._results.pop(0)


class _FakeEngine:
    def __init__(self, connection: _FakeConnection):
        self._connection = connection

    def connect(self):
        return self._connection


# ---------------------------------------------------------------------------
# TestVocabManagerBasic
# ---------------------------------------------------------------------------

class TestVocabManagerBasic:

    def test_empty_vocab_query_unchanged(self):
        mgr = _make_manager([])
        assert mgr('任意查询') == '任意查询'
        assert mgr(['a', 'b']) == ['a', 'b']

    def test_vocab_size_reflects_loaded_entries(self):
        mgr = _make_manager(_SAMPLE_ROWS_USER1)
        # word_to_cluster key is the word string; 苹果/apple appear in two clusters
        # but QueryEnhACProcessor deduplicates by word (last wins)
        assert mgr.vocab_size == 2  # unique words: '苹果', 'apple'

    def test_user_id_property(self):
        mgr = _make_manager([], user_id='alice')
        assert mgr.user_id == 'alice'

    def test_call_with_string_enhances_query(self):
        mgr = _make_manager(_SAMPLE_ROWS_USER2)

        result = mgr('关于民法的问题')

        assert isinstance(result, str)
        assert result == '关于民法（民事法律）的问题'

    def test_call_with_list(self):
        mgr = _make_manager([])
        result = mgr(['query1', 'query2'])
        assert result == ['query1', 'query2']

    def test_call_with_invalid_list_item_returns_original_query(self):
        mgr = _make_manager([])
        query = ['query1', 123]

        with patch('lazymind.review.vocab.vocab_manager.LOG') as mock_log:
            result = mgr(query)

        assert result is query
        mock_log.error.assert_called_once()

    def test_call_when_processor_raises_returns_original_query(self):
        mgr = _make_manager([])

        with patch.object(mgr, '_proc', side_effect=RuntimeError('boom')), \
             patch('lazymind.review.vocab.vocab_manager.LOG') as mock_log:
            result = mgr('关于民法的问题')

        assert result == '关于民法的问题'
        mock_log.error.assert_called_once()


# ---------------------------------------------------------------------------
# TestVocabManagerReload
# ---------------------------------------------------------------------------

class TestVocabManagerReload:

    def test_reload_updates_vocab(self):
        mgr = _make_manager([], user_id='u_reload')
        assert mgr.vocab_size == 0

        new_rows = [
            {'word': 'alpha', 'cluster_id': 'c1'},
            {'word': 'beta',  'cluster_id': 'c1'},
        ]
        # Patch _load_vocab so reload() reads new_rows
        with patch.object(mgr, '_load_vocab', return_value=new_rows):
            count = mgr.reload()
        assert count == 2
        assert mgr.vocab_size == 2

    def test_reload_clears_stale_vocab(self):
        old_rows = [{'word': 'stale', 'cluster_id': 'x'}]
        mgr = _make_manager(old_rows)
        assert mgr.vocab_size == 1

        with patch.object(mgr, '_load_vocab', return_value=[]):
            mgr.reload()
        assert mgr.vocab_size == 0

    def test_reload_without_db_returns_zero(self):
        """When DB returns empty, reload gives vocab_size=0."""
        mgr = _make_manager([{'word': 'existing', 'cluster_id': 'x'}])
        assert mgr.vocab_size == 1
        mgr._data_source = lambda: []
        with patch.object(mgr, '_load_vocab', return_value=[]):
            mgr.reload()
        assert mgr.vocab_size == 0


# ---------------------------------------------------------------------------
# TestVocabRegistry
# ---------------------------------------------------------------------------

class TestVocabRegistry:

    def setup_method(self):
        _reset_registry()

    def teardown_method(self):
        _reset_registry()

    def test_different_users_get_different_managers(self):
        from lazymind.review.service.registry import get_vocab_manager
        with patch('lazymind.review.service.registry.fetch_vocab_for_user_id', return_value=[]):
            mgr_a = get_vocab_manager('alice')
            mgr_b = get_vocab_manager('bob')
        assert mgr_a is not mgr_b
        assert mgr_a.user_id == 'alice'
        assert mgr_b.user_id == 'bob'

    def test_same_user_gets_same_manager_instance(self):
        from lazymind.review.service.registry import get_vocab_manager
        with patch('lazymind.review.service.registry.fetch_vocab_for_user_id', return_value=[]):
            mgr1 = get_vocab_manager('charlie')
            mgr2 = get_vocab_manager('charlie')
        assert mgr1 is mgr2

    def test_user_isolation_vocab_does_not_bleed(self):
        """user_001's vocab should not affect user_002's query."""
        from lazymind.review.service.registry import get_vocab_manager

        def _side_effect(user_id):
            return _SAMPLE_ROWS_USER1 if user_id == 'user_001' else _SAMPLE_ROWS_USER2

        with patch('lazymind.review.service.registry.fetch_vocab_for_user_id', side_effect=_side_effect):
            mgr1 = get_vocab_manager('user_001')
            mgr2 = get_vocab_manager('user_002')

        # user_001 has '苹果'/'apple' — user_002 should NOT
        assert '苹果' in mgr1._proc.word_to_cluster
        assert '苹果' not in mgr2._proc.word_to_cluster

        # user_002 has '民法' — user_001 should NOT
        assert '民法' in mgr2._proc.word_to_cluster
        assert '民法' not in mgr1._proc.word_to_cluster

    def test_empty_user_id_allowed(self):
        from lazymind.review.service.registry import get_vocab_manager
        with patch('lazymind.review.service.registry.fetch_vocab_for_user_id', return_value=[]):
            mgr = get_vocab_manager('')
        assert mgr.user_id == ''
        assert mgr.vocab_size == 0


# ---------------------------------------------------------------------------
# TestVocabManagerThreadSafety
# ---------------------------------------------------------------------------

class TestVocabManagerThreadSafety:

    def test_concurrent_reload_and_call_no_exception(self):  # noqa: D401
        rows = [
            {'word': 'threadtok', 'cluster_id': 'th'},
            {'word': 'tok2',      'cluster_id': 'th'},
        ]
        mgr = _make_manager(rows, user_id='thread_user')
        errors: list = []

        def _reload():
            try:
                for _ in range(20):
                    with patch.object(mgr, '_load_vocab', return_value=rows):
                        mgr.reload()
            except Exception as exc:  # pragma: no cover
                errors.append(exc)

        def _call():
            try:
                for _ in range(20):
                    mgr('threadtok test')
            except Exception as exc:  # pragma: no cover
                errors.append(exc)

        threads = [threading.Thread(target=_reload) for _ in range(3)]
        threads += [threading.Thread(target=_call) for _ in range(3)]
        for t in threads:
            t.start()
        for t in threads:
            t.join(timeout=10)

        assert errors == [], f'Thread errors: {errors}'


class TestVocabDBQueryLayer:

    def test_get_vocab_conn_prefers_core_db_url(self):
        import lazymind.review.service.db as vocab_db

        fake_engine = object()
        with patch.dict(_os.environ, {
            'LAZYMIND_DATABASE_URL': 'postgresql://legacy-app-db',
            'LAZYMIND_CORE_DATABASE_URL': 'postgresql://core-db',
            'ACL_DB_DSN': '',
        }, clear=False), patch('lazymind.review.service.db._get_engine', return_value=fake_engine) as mock_get_engine:
            assert vocab_db._get_vocab_conn() is fake_engine

        assert mock_get_engine.call_args.kwargs == {'url': 'postgresql://core-db', 'dsn': None}

    def test_fetch_vocab_for_user_id_queries_public_words_and_filters_deleted(self):
        from lazymind.review.service.db import fetch_vocab_for_user_id

        conn = _FakeConnection(
            _FakeMappingsResult(rows=[{'word': '苹果', 'group_id': 'g1'}]),
        )
        engine = _FakeEngine(conn)
        with patch('lazymind.review.service.db._ensure_table_once', return_value=None), \
             patch('lazymind.review.service.db._has_vocab_conn_target', return_value=True), \
             patch('lazymind.review.service.db._get_vocab_conn', return_value=engine):
            rows = fetch_vocab_for_user_id('user-x')

        assert rows == [{'word': '苹果', 'cluster_id': 'g1'}]
        sql, params = conn.executed[0]
        assert 'FROM public.words' in sql
        assert 'deleted_at IS NULL' in sql
        assert params == {'user_id': 'user-x'}

    def test_fetch_vocab_groups_queries_reference_info_and_filters_deleted(self):
        from lazymind.review.service.db import fetch_vocab_groups_for_user_id

        conn = _FakeConnection(
            _FakeMappingsResult(rows=[
                {'group_id': 'g1', 'word': '苹果', 'description': '水果', 'reference': 'r1'},
                {'group_id': 'g1', 'word': 'apple', 'description': '', 'reference': 'r2'},
            ]),
        )
        engine = _FakeEngine(conn)
        with patch('lazymind.review.service.db._ensure_table_once', return_value=None), \
             patch('lazymind.review.service.db._has_vocab_conn_target', return_value=True), \
             patch('lazymind.review.service.db._get_vocab_conn', return_value=engine):
            groups = fetch_vocab_groups_for_user_id('user-y')

        assert groups == {
            'g1': {
                'group_id': 'g1',
                'description': '水果',
                'words': ['苹果', 'apple'],
                'references': ['r1', 'r2'],
            }
        }
        sql, params = conn.executed[0]
        assert 'COALESCE(reference_info, \'\') AS reference' in sql
        assert 'FROM public.words' in sql
        assert 'deleted_at IS NULL' in sql
        assert params == {'user_id': 'user-y'}


# ---------------------------------------------------------------------------
# TestVocabReloadRoute
# ---------------------------------------------------------------------------

class TestVocabReloadRoute:

    @pytest.fixture()
    def client(self, tmp_path):
        """Build a minimal FastAPI test app with vocab_routes registered."""
        from fastapi import FastAPI
        from fastapi.testclient import TestClient

        test_app = FastAPI()

        # Load vocab_routes without triggering ChatServer (which needs model files)
        _routes_file = _os.path.join(_ALGO, 'lazymind', 'chat', 'service', 'api', 'vocab_routes.py')
        spec = importlib.util.spec_from_file_location('_vocab_routes_test', _routes_file)
        vocab_routes_mod = importlib.util.module_from_spec(spec)

        # Patch get_vocab_manager inside the routes module
        mock_mgr = MagicMock()
        mock_mgr.reload.return_value = 3
        mock_extract = MagicMock(return_value=[{
            'reason': '用户明确要求记住苹果就是 apple',
            'words': ['苹果', 'apple'],
            'description': '水果语境',
            'group_ids': '[]',
            'user_id': 'user_001',
            'message_ids': '["m1"]',
            'action': 'create_new_group',
        }])

        with patch('lazymind.review.service.registry.get_vocab_manager', return_value=mock_mgr):
            spec.loader.exec_module(vocab_routes_mod)
            test_app.include_router(vocab_routes_mod.router)

        yield TestClient(test_app), mock_mgr, mock_extract

    def test_reload_returns_ok_with_user_id(self, client):
        tc, mock_mgr, _ = client
        resp = tc.post('/api/vocab/reload', json={'user_id': 'user_001'})
        assert resp.status_code == 200
        body = resp.json()
        assert body['status'] == 'ok'
        assert body['user_id'] == 'user_001'
        assert isinstance(body['vocab_size'], int)

    def test_reload_default_empty_user_id(self, client):
        tc, _, _ = client
        resp = tc.post('/api/vocab/reload')
        assert resp.status_code == 200
        assert resp.json()['user_id'] == ''

    def test_extract_returns_no_content_with_user_id(self, client):
        tc, _, mock_extract = client
        resp = tc.post('/api/vocab/extract', json={'user_id': 'user_001'})

        assert resp.status_code == 204
        assert resp.content == b''
        mock_extract.assert_not_called()

    def test_extract_without_user_id_is_noop(self, client):
        tc, _, mock_extract = client
        resp = tc.post('/api/vocab/extract')

        assert resp.status_code == 204
        assert resp.content == b''
        mock_extract.assert_not_called()

    def test_extract_is_noop_and_does_not_change_response(self, client):
        tc, _, mock_extract = client
        resp = tc.post('/api/vocab/extract', json={'user_id': 'user_001'})

        assert resp.status_code == 204
        assert resp.content == b''
        mock_extract.assert_not_called()

    def test_extract_failure_does_not_break_response(self, tmp_path):
        from fastapi import FastAPI
        from fastapi.testclient import TestClient

        test_app = FastAPI()
        _routes_file = _os.path.join(_ALGO, 'lazymind', 'chat', 'service', 'api', 'vocab_routes.py')
        spec = importlib.util.spec_from_file_location('_vocab_routes_extract_fail', _routes_file)
        vocab_routes_mod = importlib.util.module_from_spec(spec)

        mock_mgr = MagicMock()
        mock_extract = MagicMock()

        with patch('lazymind.review.service.registry.get_vocab_manager', return_value=mock_mgr):
            spec.loader.exec_module(vocab_routes_mod)
            test_app.include_router(vocab_routes_mod.router)
            tc = TestClient(test_app)
            resp = tc.post('/api/vocab/extract', json={'user_id': 'user_001'})

        assert resp.status_code == 204
        assert resp.content == b''
        mock_extract.assert_not_called()

    def test_reload_failure_returns_503(self, tmp_path):
        from fastapi import FastAPI
        from fastapi.testclient import TestClient

        test_app = FastAPI()
        _routes_file = _os.path.join(_ALGO, 'lazymind', 'chat', 'service', 'api', 'vocab_routes.py')
        spec = importlib.util.spec_from_file_location('_vocab_routes_reload_fail', _routes_file)
        vocab_routes_mod = importlib.util.module_from_spec(spec)

        mock_mgr = MagicMock()
        mock_mgr.reload.side_effect = RuntimeError('db down')

        with patch('lazymind.review.service.registry.get_vocab_manager', return_value=mock_mgr):
            spec.loader.exec_module(vocab_routes_mod)
            test_app.include_router(vocab_routes_mod.router)
            tc = TestClient(test_app)
            resp = tc.post('/api/vocab/reload', json={'user_id': 'user_001'})

        assert resp.status_code == 503
        assert resp.json() == {'detail': 'vocab reload failed'}

    def test_reload_different_user_ids_call_respective_manager(self, tmp_path):
        """Each user_id triggers reload on its own VocabManager instance."""
        from fastapi import FastAPI
        from fastapi.testclient import TestClient
        from lazymind.review.service.registry import clear_registry

        clear_registry()

        test_app = FastAPI()
        _routes_file = _os.path.join(_ALGO, 'lazymind', 'chat', 'service', 'api', 'vocab_routes.py')
        spec = importlib.util.spec_from_file_location('_vocab_routes_multi', _routes_file)
        vocab_routes_mod = importlib.util.module_from_spec(spec)

        called_users: list = []

        def fake_get_manager(uid=''):
            called_users.append(uid)
            m = MagicMock()
            m.reload.return_value = 0
            return m

        with patch('lazymind.review.service.registry.get_vocab_manager', side_effect=fake_get_manager):
            spec.loader.exec_module(vocab_routes_mod)
            test_app.include_router(vocab_routes_mod.router)
            tc = TestClient(test_app)
            tc.post('/api/vocab/reload', json={'user_id': 'alice'})
            tc.post('/api/vocab/reload', json={'user_id': 'bob'})

        assert 'alice' in called_users
        assert 'bob' in called_users
        clear_registry()



# ---------------------------------------------------------------------------
# TestVocabDBIntegration  (requires real DB — skipped when env var absent)
# ---------------------------------------------------------------------------

_REAL_VOCAB_DB_URL = _os.getenv('LAZYMIND_CORE_DATABASE_URL', '') or _os.getenv('LAZYMIND_DATABASE_URL', '')


def _real_vocab_users(limit: int = 2) -> list[str]:
    from lazymind.review.service.db import _get_vocab_conn

    engine = _get_vocab_conn()
    with engine.connect() as conn:
        return [
            row for row in conn.execute(
                text(
                    """SELECT create_user_id
                           FROM public.words
                          WHERE deleted_at IS NULL
                            AND COALESCE(create_user_id, '') <> ''
                          GROUP BY create_user_id
                          ORDER BY COUNT(*) DESC, create_user_id
                          LIMIT :limit"""
                ),
                {'limit': limit},
            ).scalars().all()
            if row
        ]


def _real_deleted_vocab_entry() -> dict | None:
    from lazymind.review.service.db import _get_vocab_conn

    engine = _get_vocab_conn()
    with engine.connect() as conn:
        rows = conn.execute(
            text(
                """SELECT create_user_id, word, group_id
                       FROM public.words
                      WHERE deleted_at IS NOT NULL
                        AND COALESCE(create_user_id, '') <> ''
                      ORDER BY updated_at DESC NULLS LAST, created_at DESC NULLS LAST
                      LIMIT 1"""
            )
        ).mappings().all()
    return dict(rows[0]) if rows else None


@pytest.mark.integration
@pytest.mark.skipif(not _REAL_VOCAB_DB_URL, reason='LAZYMIND_CORE_DATABASE_URL / LAZYMIND_DATABASE_URL not set')
class TestVocabDBIntegration:
    """Integration tests that hit the real core.public.words table."""

    def test_fetch_vocab_for_active_user(self):
        from lazymind.review.service.db import fetch_vocab_for_user_id
        users = _real_vocab_users(limit=1)
        if not users:
            pytest.skip('no active vocab users in real core.words table')

        rows = fetch_vocab_for_user_id(users[0])
        assert rows, f'expected active vocab rows for {users[0]!r}'
        assert all(row['word'] for row in rows)
        assert all(row['cluster_id'] for row in rows)

    def test_deleted_vocab_rows_are_excluded(self):
        from lazymind.review.service.db import fetch_vocab_for_user_id

        deleted = _real_deleted_vocab_entry()
        if not deleted:
            pytest.skip('no deleted vocab rows available in real core.words table')

        rows = fetch_vocab_for_user_id(deleted['create_user_id'])
        assert {
            'word': deleted['word'],
            'cluster_id': deleted['group_id'],
        } not in rows

    def test_fetch_vocab_unknown_user_returns_empty(self):
        from lazymind.review.service.db import fetch_vocab_for_user_id
        rows = fetch_vocab_for_user_id('__nonexistent_user_xyz__')
        assert rows == []

    def test_vocab_manager_loads_from_db(self):
        _reset_registry()
        from lazymind.review.service.registry import get_vocab_manager
        users = _real_vocab_users(limit=1)
        if not users:
            pytest.skip('no active vocab users in real core.words table')

        mgr = get_vocab_manager(users[0])
        assert mgr.vocab_size >= 1
        _reset_registry()

    def test_reload_reads_db(self):
        _reset_registry()
        from lazymind.review.service.registry import get_vocab_manager
        users = _real_vocab_users(limit=1)
        if not users:
            pytest.skip('no active vocab users in real core.words table')

        mgr = get_vocab_manager(users[0])
        count = mgr.reload()
        assert count >= 1
        _reset_registry()

    def test_user_isolation_in_full_stack(self):
        """Two active users should load independent vocab snapshots."""
        _reset_registry()
        from lazymind.review.service.registry import get_vocab_manager
        users = _real_vocab_users(limit=2)
        if len(users) < 2:
            pytest.skip('need at least two active vocab users for isolation test')

        mgr1 = get_vocab_manager(users[0])
        mgr2 = get_vocab_manager(users[1])

        assert mgr1.user_id != mgr2.user_id
        assert mgr1 is not mgr2
        assert mgr1._proc.word_to_cluster != mgr2._proc.word_to_cluster
        _reset_registry()


class TestVocabDbDsnParsing:

    def test_dsn_to_sqlalchemy_url_requires_host(self):
        from lazymind.review.service.db import _dsn_to_sqlalchemy_url

        with pytest.raises(ValueError, match='database host is required'):
            _dsn_to_sqlalchemy_url('user=app password=app dbname=core port=5432')

    def test_dsn_to_sqlalchemy_url_requires_database_name(self):
        from lazymind.review.service.db import _dsn_to_sqlalchemy_url

        with pytest.raises(ValueError, match='database name is required'):
            _dsn_to_sqlalchemy_url('host=db user=app password=app port=5432')

    def test_dsn_to_sqlalchemy_url_rejects_invalid_port(self):
        from lazymind.review.service.db import _dsn_to_sqlalchemy_url

        with pytest.raises(ValueError, match='invalid database port'):
            _dsn_to_sqlalchemy_url('host=db user=app password=app dbname=core port=abc')

import concurrent.futures
import sys
import time
import types

import core.state_store as state_store_module
from core.state_store import SQLiteStateStore


def test_sqlite_state_store_set_get_delete_and_expiry(monkeypatch, tmp_path):
    store = SQLiteStateStore(str(tmp_path / 'state.db'))

    store.set('k', 'v', ex=60)
    assert store.get('k') == 'v'

    store.delete('k')
    assert store.get('k') is None

    store.set('expired', 'v', ex=1)
    now = time.time()
    monkeypatch.setattr(state_store_module.time, 'time', lambda: now + 2)
    assert store.get('expired') is None


def test_sqlite_state_store_zset_window(tmp_path):
    store = SQLiteStateStore(str(tmp_path / 'state.db'))

    store.zadd('login:alice', {'1': 1, '2': 2, '3': 3})
    store.zremrangebyscore('login:alice', float('-inf'), 1)

    assert store.zcard('login:alice') == 2


def test_state_backend_prefers_lazymind_config(monkeypatch):
    package = types.ModuleType('lazymind')
    config_module = types.ModuleType('lazymind.config')
    config_module.config = {'state_backend': 'sqlite'}
    monkeypatch.setitem(sys.modules, 'lazymind', package)
    monkeypatch.setitem(sys.modules, 'lazymind.config', config_module)
    monkeypatch.setenv(state_store_module.STATE_BACKEND_ENV, 'redis')

    assert state_store_module.state_backend() == 'sqlite'


def test_state_backend_falls_back_to_env_when_lazymind_config_unavailable(monkeypatch):
    monkeypatch.delitem(sys.modules, 'lazymind.config', raising=False)
    monkeypatch.setenv(state_store_module.STATE_BACKEND_ENV, ' sqlite ')

    assert state_store_module.state_backend() == 'sqlite'


def test_state_store_initializes_once_under_concurrent_calls(monkeypatch, tmp_path):
    created_paths = []

    class FakeSQLiteStateStore:
        def __init__(self, path):
            created_paths.append(path)
            time.sleep(0.01)

    monkeypatch.setattr(state_store_module, '_STORE', None)
    monkeypatch.setattr(state_store_module, 'state_backend', lambda: 'sqlite')
    monkeypatch.setattr(state_store_module, '_sqlite_path', lambda: str(tmp_path / 'state.db'))
    monkeypatch.setattr(state_store_module, 'SQLiteStateStore', FakeSQLiteStateStore)

    with concurrent.futures.ThreadPoolExecutor(max_workers=8) as executor:
        stores = list(executor.map(lambda _: state_store_module.state_store(), range(16)))

    assert len(created_paths) == 1
    assert all(store is stores[0] for store in stores)

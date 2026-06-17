import time

from core.state_store import SQLiteStateStore


def test_sqlite_state_store_set_get_delete_and_expiry(tmp_path):
    store = SQLiteStateStore(str(tmp_path / 'state.db'))

    store.set('k', 'v', ex=60)
    assert store.get('k') == 'v'

    store.delete('k')
    assert store.get('k') is None

    store.set('expired', 'v', ex=-1)
    assert store.get('expired') is None


def test_sqlite_state_store_zset_window(tmp_path):
    store = SQLiteStateStore(str(tmp_path / 'state.db'))

    store.zadd('login:alice', {'1': 1, '2': 2, '3': 3})
    store.zremrangebyscore('login:alice', float('-inf'), 1)

    assert store.zcard('login:alice') == 2

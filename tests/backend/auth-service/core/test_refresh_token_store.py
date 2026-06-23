import json
import uuid

import core.refresh_token_store as store


class _StateStore:
    def __init__(self):
        self.values = {}
        self.set_calls = []
        self.deleted = []
        self.fail_delete = False

    def set(self, key, value, ex):
        self.values[key] = value
        self.set_calls.append((key, value, ex))

    def get(self, key):
        return self.values.get(key)

    def delete(self, key):
        if self.fail_delete:
            raise RuntimeError('delete failed')
        self.deleted.append(key)


def test_key_adds_refresh_token_prefix():
    assert store._key('abc') == 'auth:rt:abc'


def test_set_refresh_token_stores_user_id_with_ttl(monkeypatch):
    state = _StateStore()
    user_id = uuid.uuid4()
    monkeypatch.setattr(store, 'state_store', lambda: state)
    monkeypatch.setattr(store, 'refresh_token_ttl_seconds', lambda: 60)
    monkeypatch.setattr(store.time, 'time', lambda: 100)

    store.set_refresh_token('hash', user_id)

    key, value, ex = state.set_calls[0]
    assert key == 'auth:rt:hash'
    assert isinstance(value, str)
    assert json.loads(value) == {'user_id': str(user_id), 'expires_at': 160}
    assert ex == 60


def test_get_user_id_by_token_returns_none_for_missing_or_invalid_values(monkeypatch):
    state = _StateStore()
    monkeypatch.setattr(store, 'state_store', lambda: state)

    assert store.get_user_id_by_token('missing') is None

    state.values['auth:rt:bad-json'] = '{bad'
    assert store.get_user_id_by_token('bad-json') is None

    state.values['auth:rt:not-dict'] = json.dumps(['x'])
    assert store.get_user_id_by_token('not-dict') is None

    state.values['auth:rt:no-expiry'] = json.dumps({'user_id': str(uuid.uuid4())})
    assert store.get_user_id_by_token('no-expiry') is None

    state.values['auth:rt:bad-expiry'] = json.dumps({'user_id': str(uuid.uuid4()), 'expires_at': 'soon'})
    assert store.get_user_id_by_token('bad-expiry') is None

    state.values['auth:rt:bad-user'] = json.dumps({'user_id': 'bad', 'expires_at': 999})
    assert store.get_user_id_by_token('bad-user') is None


def test_get_user_id_by_token_returns_user_id_and_deletes_expired_token(monkeypatch):
    state = _StateStore()
    user_id = uuid.uuid4()
    monkeypatch.setattr(store, 'state_store', lambda: state)
    monkeypatch.setattr(store.time, 'time', lambda: 100)

    state.values['auth:rt:valid'] = json.dumps({'user_id': str(user_id), 'expires_at': 101})
    result = store.get_user_id_by_token('valid')
    assert isinstance(result, uuid.UUID)
    assert result == user_id

    state.values['auth:rt:expired'] = json.dumps({'user_id': str(user_id), 'expires_at': 99})
    assert store.get_user_id_by_token('expired') is None
    assert state.deleted == ['auth:rt:expired']


def test_delete_refresh_token_ignores_delete_errors(monkeypatch):
    state = _StateStore()
    state.fail_delete = True
    monkeypatch.setattr(store, 'state_store', lambda: state)

    store.delete_refresh_token('hash')

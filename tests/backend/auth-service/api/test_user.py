import uuid

import pytest

import api.user as user_api
from core.errors import AppException
from schemas.user import (
    CreateUserBody,
    DisableUserBody,
    UserRoleBatchBody,
    UserRoleBody,
)


def _call(fn, *args, **kwargs):
    return getattr(fn, '__wrapped__', fn)(*args, **kwargs)


def test_parse_user_id_returns_uuid_and_rejects_invalid_value():
    value = str(uuid.uuid4())

    assert user_api._parse_user_id(value) == uuid.UUID(value)
    with pytest.raises(AppException) as exc:
        user_api._parse_user_id('not-a-uuid')
    assert exc.value.code == 1000401


def test_parse_user_ids_reports_bad_value_as_extra_message():
    valid = str(uuid.uuid4())

    with pytest.raises(AppException) as exc:
        user_api._parse_user_ids([valid, 'bad-user-id'])

    assert exc.value.code == 1000401
    assert exc.value.extra == 'bad-user-id'


def test_create_user_converts_role_id_and_passes_defaults(monkeypatch):
    role_id = uuid.uuid4()
    calls = []
    monkeypatch.setattr(user_api.user_service, 'create_user', lambda **kwargs: calls.append(kwargs) or {'ok': True})

    result = _call(
        user_api.create_user,
        CreateUserBody(
            username='alice',
            password='secret',
            role_id=str(role_id),
            email='alice@example.test',
            tenant_id='',
            disabled=True,
        ),
        object(),
    )

    assert isinstance(result, dict)
    assert result == {'ok': True}
    assert calls == [{
        'username': 'alice',
        'password': 'secret',
        'role_id': role_id,
        'email': 'alice@example.test',
        'tenant_id': '',
        'disabled': True,
    }]


def test_create_user_rejects_invalid_role_id():
    with pytest.raises(AppException) as exc:
        _call(user_api.create_user, CreateUserBody(username='alice', password='secret', role_id='bad'), object())

    assert exc.value.code == 1000403


def test_list_users_returns_pagination_payload(monkeypatch):
    calls = []
    monkeypatch.setattr(user_api.user_service, 'list_users', lambda **kwargs: calls.append(kwargs) or (['u1'], 7))

    result = _call(
        user_api.list_users,
        object(),
        page=2,
        page_size=5,
        search='ali',
        tenant_id='tenant-a',
        active_only=True,
    )

    assert isinstance(result, dict)
    assert result == {'users': ['u1'], 'total': 7, 'page': 2, 'page_size': 5}
    assert calls == [{
        'page': 2,
        'page_size': 5,
        'search': 'ali',
        'tenant_id': 'tenant-a',
        'active_only': True,
    }]


def test_create_user_passes_none_role_id_when_absent(monkeypatch):
    calls = []
    monkeypatch.setattr(user_api.user_service, 'create_user', lambda **kwargs: calls.append(kwargs) or {'ok': True})

    result = _call(
        user_api.create_user,
        CreateUserBody(username='alice', password='secret', role_id=None, email=None, tenant_id='', disabled=False),
        object(),
    )

    assert result == {'ok': True}
    assert calls == [{
        'username': 'alice',
        'password': 'secret',
        'role_id': None,
        'email': None,
        'tenant_id': '',
        'disabled': False,
    }]


def test_set_user_roles_batch_converts_all_ids(monkeypatch):
    user_ids = [uuid.uuid4(), uuid.uuid4()]
    role_id = uuid.uuid4()
    calls = []
    monkeypatch.setattr(user_api.user_service, 'set_user_roles_batch', lambda uids, rid: calls.append((uids, rid)))

    result = _call(
        user_api.set_user_roles_batch,
        UserRoleBatchBody(user_ids=[str(item) for item in user_ids], role_id=str(role_id)),
        object(),
    )

    assert result == {'ok': True}
    assert calls == [(user_ids, role_id)]


def test_set_user_roles_batch_rejects_invalid_role_id():
    with pytest.raises(AppException) as exc:
        _call(
            user_api.set_user_roles_batch,
            UserRoleBatchBody(user_ids=[str(uuid.uuid4())], role_id='bad-role-id'),
            object(),
        )

    assert exc.value.code == 1000403


def test_list_user_groups_internal_returns_groups_payload(monkeypatch):
    user_id = uuid.uuid4()
    monkeypatch.setattr(user_api.group_service, 'list_user_groups', lambda uid: [{'group_id': str(uid)}])

    result = user_api.list_user_groups_internal(str(user_id), None)

    assert isinstance(result, dict)
    assert result == {'groups': [{'group_id': str(user_id)}]}


def test_user_mutation_endpoints_delegate_to_service(monkeypatch):
    user_id = uuid.uuid4()
    role_id = uuid.uuid4()
    calls = []
    monkeypatch.setattr(user_api.user_service, 'get_user', lambda uid: calls.append(('get', uid)) or {'user_id': str(uid)})
    monkeypatch.setattr(user_api.user_service, 'set_user_role', lambda uid, rid: calls.append(('role', uid, rid)))
    monkeypatch.setattr(user_api.user_service, 'disable_user', lambda uid, disabled: calls.append(('disable', uid, disabled)))

    assert _call(user_api.get_user, str(user_id), object()) == {'user_id': str(user_id)}
    assert _call(user_api.set_user_role, str(user_id), UserRoleBody(role_id=str(role_id)), object()) == {'ok': True}
    assert _call(user_api.disable_user, str(user_id), DisableUserBody(disabled=False), object()) == {'ok': True}
    assert calls == [
        ('get', user_id),
        ('role', user_id, role_id),
        ('disable', user_id, False),
    ]

import uuid
from contextlib import contextmanager
import importlib
import sys
import types

import pytest
from sqlalchemy import create_engine
from sqlalchemy.orm import sessionmaker
from sqlalchemy.pool import StaticPool

from core.errors import AppException, AuthError
from models import Base
from repositories import GroupRepository, PermissionGroupRepository, RoleRepository, UserRepository

try:
    from passlib.context import CryptContext as _CryptContext
except ModuleNotFoundError:
    passlib_module = types.ModuleType('passlib')
    context_module = types.ModuleType('passlib.context')

    class _CryptContext:
        def __init__(self, *args, **kwargs):
            pass

        def hash(self, password):
            return f'fake-hash::{password}'

        def verify(self, password, password_hash):
            return password_hash == f'fake-hash::{password}'

    context_module.CryptContext = _CryptContext
    passlib_module.context = context_module
    sys.modules['passlib'] = passlib_module
    sys.modules['passlib.context'] = context_module

from services.auth_service import AuthService
from services.group_service import GroupService
from services.role_service import RoleService
from services.user_service import UserService


@pytest.fixture
def db_session():
    engine = create_engine(
        'sqlite:///:memory:',
        connect_args={'check_same_thread': False},
        poolclass=StaticPool,
    )
    Base.metadata.create_all(engine)
    session = sessionmaker(bind=engine)()
    yield session
    session.close()


@pytest.fixture
def service_session_local(db_session):
    @contextmanager
    def _session_local():
        yield db_session

    return _session_local


@pytest.fixture
def service_modules(service_session_local, monkeypatch):
    group_service_module = importlib.import_module('services.group_service')
    role_service_module = importlib.import_module('services.role_service')
    user_service_module = importlib.import_module('services.user_service')

    monkeypatch.setattr(group_service_module, 'SessionLocal', service_session_local)
    monkeypatch.setattr(role_service_module, 'SessionLocal', service_session_local)
    monkeypatch.setattr(user_service_module, 'SessionLocal', service_session_local)
    return group_service_module, role_service_module, user_service_module


@pytest.fixture
def seeded_data(db_session):
    system_admin = RoleRepository.create(db_session, 'system-admin', built_in=True)
    user_role = RoleRepository.create(db_session, 'user', built_in=True)
    editor_role = RoleRepository.create(db_session, 'editor', built_in=False)
    read_pg = PermissionGroupRepository.create(
        db_session,
        code='group.read',
        description='Read group',
        module='group',
        action='read',
    )
    write_pg = PermissionGroupRepository.create(
        db_session,
        code='group.write',
        description='Write group',
        module='group',
        action='write',
    )
    admin = UserRepository.create(
        db_session,
        username='admin',
        password_hash='hashed-admin',
        role_id=system_admin.id,
        tenant_id='tenant-a',
        source='admin',
    )
    bootstrap = UserRepository.create(
        db_session,
        username='bootstrap',
        password_hash='hashed-bootstrap',
        role_id=system_admin.id,
        tenant_id='tenant-a',
        source='init',
    )
    normal = UserRepository.create(
        db_session,
        username='normal',
        password_hash='hashed-normal',
        role_id=user_role.id,
        tenant_id='tenant-a',
        source='platform',
    )
    outsider = UserRepository.create(
        db_session,
        username='outsider',
        password_hash='hashed-outsider',
        role_id=user_role.id,
        tenant_id='tenant-b',
        source='platform',
    )
    team = GroupRepository.create(db_session, 'tenant-a', 'alpha-team', 'Alpha', admin.id)
    return {
        'system_admin': system_admin,
        'user_role': user_role,
        'editor_role': editor_role,
        'read_pg': read_pg,
        'write_pg': write_pg,
        'admin': admin,
        'bootstrap': bootstrap,
        'normal': normal,
        'outsider': outsider,
        'team': team,
    }


def test_services_package_exports_instances():
    services_pkg = importlib.import_module('services')

    assert services_pkg.__all__ == [
        'auth_service',
        'cloud_oauth_service',
        'group_service',
        'role_service',
        'user_service',
    ]
    assert services_pkg.auth_service is not None
    assert services_pkg.cloud_oauth_service is not None
    assert services_pkg.group_service is not None
    assert services_pkg.role_service is not None
    assert services_pkg.user_service is not None


def test_auth_service_validation_and_registration(db_session, seeded_data, monkeypatch):
    service = AuthService()
    user_role = seeded_data['user_role']

    assert service.validate_username('user.name-1') is True
    assert service.validate_username('a') is False
    assert service.validate_username('.bad') is False
    assert service.validate_password('Aa1!aaaa') is True
    assert service.validate_password('weakpass') is False

    created = service.register_user(
        db=db_session,
        username='  new_user  ',
        password='Aa1!aaaa',
        role_id=user_role.id,
        tenant_id='tenant-a',
        email='new@example.com',
    )
    assert created.username == 'new_user'
    assert created.email == 'new@example.com'
    assert service.verify_password('Aa1!aaaa', created.password_hash) is True

    with pytest.raises(AuthError) as invalid_username:
        service.register_user(db=db_session, username='*bad*', password='Aa1!aaaa', role_id=user_role.id)
    assert invalid_username.value.code == 1000101

    with pytest.raises(AuthError) as username_required:
        service.register_user(db=db_session, username='   ', password='Aa1!aaaa', role_id=user_role.id)
    assert username_required.value.code == 1000201

    with pytest.raises(AuthError) as password_required:
        service.register_user(db=db_session, username='valid_user2', password='', role_id=user_role.id)
    assert password_required.value.code == 1000202

    with pytest.raises(AuthError) as invalid_password:
        service.register_user(db=db_session, username='valid_user', password='short', role_id=user_role.id)
    assert invalid_password.value.code == 1000103

    with pytest.raises(AuthError) as duplicate:
        service.register_user(
            db=db_session,
            username='new_user',
            password='Aa1!aaaa',
            role_id=user_role.id,
        )
    assert duplicate.value.code == 1000102

    attempts = []

    class DummyLimiter:
        def is_limited(self, user_id):
            return user_id == created.id and len(attempts) >= 1

        def record_failure(self, user_id):
            attempts.append(user_id)

    auth_service_module = importlib.import_module('services.auth_service')

    monkeypatch.setattr(auth_service_module, 'login_rate_limiter', DummyLimiter())

    authenticated = service.authenticate_user(db=db_session, username='new_user', password='Aa1!aaaa')
    assert authenticated.id == created.id

    with pytest.raises(AuthError) as wrong_password:
        service.authenticate_user(db=db_session, username='new_user', password='wrong')
    assert wrong_password.value.code == 1000105
    assert attempts == [created.id]

    with pytest.raises(AuthError) as missing_user:
        service.authenticate_user(db=db_session, username='ghost', password='Aa1!aaaa')
    assert missing_user.value.code == 1000105

    with pytest.raises(AuthError) as locked:
        service.authenticate_user(db=db_session, username='new_user', password='Aa1!aaaa')
    assert locked.value.code == 1000104

    created.disabled = True
    db_session.commit()

    monkeypatch.setattr(auth_service_module, 'login_rate_limiter', DummyLimiter())
    attempts.clear()
    with pytest.raises(AuthError) as disabled:
        service.authenticate_user(db=db_session, username='new_user', password='Aa1!aaaa')
    assert disabled.value.code == 1000106


def test_role_service_crud_and_permission_management(service_modules, db_session, seeded_data):
    _, _, _ = service_modules
    service = RoleService()
    editor_role = seeded_data['editor_role']
    system_admin = seeded_data['system_admin']

    listed_permissions = service.list_permission_groups()
    assert [item['code'] for item in listed_permissions] == ['group.read', 'group.write']

    listed_roles = service.list_roles()
    assert [item['name'] for item in listed_roles] == ['editor', 'system-admin', 'user']

    created = service.create_role(' auditor ')
    assert created['name'] == 'auditor'
    assert created['built_in'] is False

    with pytest.raises(AppException) as empty_name:
        service.create_role('   ')
    assert empty_name.value.code == 1000408

    with pytest.raises(AppException) as duplicate:
        service.create_role('auditor')
    assert duplicate.value.code == 1000409

    service.set_role_permissions(editor_role.id, ['group.read', 'missing', 'group.write'])
    assert set(service.get_role_permissions(editor_role.id)) == {'group.read', 'group.write'}

    service.set_role_permissions(editor_role.id, [])
    assert service.get_role_permissions(editor_role.id) == []

    with pytest.raises(AppException) as role_not_found_permissions:
        service.get_role_permissions(uuid.uuid4())
    assert role_not_found_permissions.value.code == 1000403

    with pytest.raises(AppException) as cannot_change_admin:
        service.set_role_permissions(system_admin.id, ['group.read'])
    assert cannot_change_admin.value.code == 1000411

    with pytest.raises(AppException) as set_missing_role:
        service.set_role_permissions(uuid.uuid4(), ['group.read'])
    assert set_missing_role.value.code == 1000403

    service.delete_role(editor_role.id)
    assert RoleRepository.get_by_id(db_session, editor_role.id) is None

    with pytest.raises(AppException) as cannot_delete_builtin:
        service.delete_role(system_admin.id)
    assert cannot_delete_builtin.value.code == 1000410

    with pytest.raises(AppException) as delete_missing_role:
        service.delete_role(uuid.uuid4())
    assert delete_missing_role.value.code == 1000403


def test_group_service_group_membership_and_permissions(service_modules, db_session, seeded_data):
    _, _, _ = service_modules
    service = GroupService()
    admin = seeded_data['admin']
    normal = seeded_data['normal']
    outsider = seeded_data['outsider']
    team = seeded_data['team']

    created_group_id = service.create_group(
        '  beta-team  ',
        tenant_id='tenant-a',
        remark='Beta',
        creator_user_id=admin.id,
    )
    created_group = GroupRepository.get_by_id(db_session, uuid.UUID(created_group_id))
    assert created_group.group_name == 'beta-team'

    with pytest.raises(AppException) as empty_group_name:
        service.create_group('   ', tenant_id='tenant-a')
    assert empty_group_name.value.code == 1000404

    with pytest.raises(AppException) as duplicate:
        service.create_group('beta-team', tenant_id='tenant-a')
    assert duplicate.value.code == 1000413

    assert service.get_group(team.id)['group_name'] == 'alpha-team'
    assert service.get_group(uuid.uuid4()) is None

    assert service.list_groups(current_user_id=None, is_system_admin=False) == ([], 0)

    with pytest.raises(AppException) as user_not_found:
        service.list_groups(current_user_id=uuid.uuid4(), is_system_admin=False)
    assert user_not_found.value.code == 1000401

    service.add_group_users(team.id, [admin.id, normal.id], role='member', operator_id=admin.id)
    members = service.list_group_users(team.id)
    assert {row['username'] for row in members} == {'admin', 'normal'}

    normal.disabled = True
    db_session.commit()
    active_members = service.list_group_users(team.id, active_only=True)
    assert [row['username'] for row in active_members] == ['admin']
    normal.disabled = False
    db_session.commit()

    service.add_group_users(team.id, [normal.id], role='member', operator_id=admin.id)
    assert len(service.list_group_users(team.id)) == 2

    with pytest.raises(AppException) as add_missing_group:
        service.add_group_users(uuid.uuid4(), [normal.id])
    assert add_missing_group.value.code == 1000402

    with pytest.raises(AppException) as add_missing_user:
        service.add_group_users(team.id, [uuid.uuid4()])
    assert add_missing_user.value.code == 1000401

    user_groups = service.list_user_groups(normal.id)
    assert user_groups[0]['group_name'] == 'alpha-team'

    with pytest.raises(AppException) as missing_user_groups:
        service.list_user_groups(uuid.uuid4())
    assert missing_user_groups.value.code == 1000401

    admin_groups, admin_total = service.list_groups(is_system_admin=True, tenant_id='tenant-a', search='team')
    assert admin_total == 2
    assert {item['group_name'] for item in admin_groups} == {'alpha-team', 'beta-team'}

    active_admin_groups, active_admin_total = service.list_groups(
        is_system_admin=True,
        tenant_id='tenant-a',
        search='team',
        active_members_only=True,
    )
    assert active_admin_total == 1
    assert [item['group_name'] for item in active_admin_groups] == ['alpha-team']

    member_groups, member_total = service.list_groups(
        current_user_id=normal.id,
        search='alpha',
        tenant_id='tenant-a',
        is_system_admin=False,
    )
    assert member_total == 1
    assert member_groups[0]['group_name'] == 'alpha-team'

    service.set_member_role(team.id, normal.id, ' owner ')
    role_by_username = {row['username']: row['role'] for row in service.list_group_users(team.id)}
    assert role_by_username['normal'] == 'owner'

    with pytest.raises(AppException) as role_required:
        service.set_member_role(team.id, normal.id, '   ')
    assert role_required.value.code == 1000406

    with pytest.raises(AppException) as membership_missing:
        service.set_member_role(team.id, outsider.id, 'owner')
    assert membership_missing.value.code == 1000407

    service.set_member_roles_batch(team.id, [admin.id, normal.id], 'maintainer')
    assert {row['role'] for row in service.list_group_users(team.id)} == {'maintainer'}

    service.set_member_roles_batch(team.id, [], 'maintainer')

    with pytest.raises(AppException) as batch_role_required:
        service.set_member_roles_batch(team.id, [admin.id], '   ')
    assert batch_role_required.value.code == 1000406

    with pytest.raises(AppException) as missing_member:
        service.set_member_roles_batch(team.id, [outsider.id], 'member')
    assert missing_member.value.code == 1000407

    service.set_group_permissions(team.id, ['group.read', 'group.write', 'missing'])
    assert set(service.get_group_permissions(team.id)) == {'group.read', 'group.write'}

    service.set_group_permissions(team.id, [])
    assert service.get_group_permissions(team.id) == []

    with pytest.raises(AppException) as group_permissions_missing:
        service.get_group_permissions(uuid.uuid4())
    assert group_permissions_missing.value.code == 1000402

    with pytest.raises(AppException) as set_group_permissions_missing:
        service.set_group_permissions(uuid.uuid4(), ['group.read'])
    assert set_group_permissions_missing.value.code == 1000402

    service.remove_group_users(team.id, [normal.id])
    assert [row['username'] for row in service.list_group_users(team.id)] == ['admin']

    service.update_group(team.id, group_name='alpha-renamed', remark='Renamed', tenant_id='tenant-x')
    assert service.get_group(team.id) == {
        'group_id': str(team.id),
        'group_name': 'alpha-renamed',
        'remark': 'Renamed',
        'tenant_id': 'tenant-x',
    }

    service.create_group('gamma-team', tenant_id='tenant-x')
    with pytest.raises(AppException) as group_name_empty:
        service.update_group(team.id, group_name='   ')
    assert group_name_empty.value.code == 1000405

    with pytest.raises(AppException) as group_name_exists:
        service.update_group(team.id, group_name='gamma-team')
    assert group_name_exists.value.code == 1000413

    with pytest.raises(AppException) as update_missing_group:
        service.update_group(uuid.uuid4(), group_name='missing')
    assert update_missing_group.value.code == 1000402

    service.delete_group(created_group.id)
    assert GroupRepository.get_by_id(db_session, created_group.id) is None

    with pytest.raises(AppException) as delete_missing_group:
        service.delete_group(uuid.uuid4())
    assert delete_missing_group.value.code == 1000402


def test_user_service_crud_role_assignment_and_password_reset(service_modules, db_session, seeded_data, monkeypatch):
    _, _, user_service_module = service_modules
    service = UserService()
    user_role = seeded_data['user_role']
    editor_role = seeded_data['editor_role']
    bootstrap = seeded_data['bootstrap']
    normal = seeded_data['normal']

    monkeypatch.setattr(user_service_module.auth_service, 'hash_password', lambda password: f'hashed::{password}')
    monkeypatch.setattr(
        user_service_module.auth_service,
        'validate_password',
        lambda password: password.startswith('Aa1!'),
    )

    created = service.create_user(
        username='  svc-user  ',
        password='Aa1!service',
        tenant_id='tenant-a',
        email='svc@example.com',
    )
    assert created['username'] == 'svc-user'
    assert created['role_name'] == 'user'

    explicit = service.create_user(
        username='editor-user',
        password='Aa1!editor',
        role_id=editor_role.id,
        disabled=True,
    )
    explicit_user = UserRepository.get_by_id(db_session, uuid.UUID(explicit['user_id']))
    assert explicit_user.disabled is True
    assert explicit['role_name'] == 'editor'

    assert service._is_bootstrap_admin(None) is False
    assert service._is_bootstrap_admin(bootstrap) is True

    with pytest.raises(AppException) as username_required:
        service.create_user(username='   ', password='Aa1!service')
    assert username_required.value.code == 1000201

    with pytest.raises(AppException) as password_required:
        service.create_user(username='missing-password', password='   ')
    assert password_required.value.code == 1000202

    with pytest.raises(AppException) as invalid_password:
        service.create_user(username='bad-password', password='weak')
    assert invalid_password.value.code == 1000103

    with pytest.raises(AppException) as missing_role:
        service.create_user(username='missing-role', password='Aa1!service', role_id=uuid.uuid4())
    assert missing_role.value.code == 1000403

    with pytest.raises(AppException) as duplicate:
        service.create_user(username='svc-user', password='Aa1!service')
    assert duplicate.value.code == 1000102

    users, total = service.list_users(search='user')
    assert total >= 2
    assert any(item['username'] == 'svc-user' for item in users)

    active_users, active_total = service.list_users(search='user', active_only=True)
    assert active_total == 1
    assert [item['username'] for item in active_users] == ['svc-user']

    detail = service.get_user(normal.id)
    assert detail['username'] == 'normal'
    assert detail['role_name'] == 'user'
    assert detail['is_bootstrap_admin'] is False

    bootstrap_detail = service.get_user(bootstrap.id)
    assert bootstrap_detail['is_bootstrap_admin'] is True

    with pytest.raises(AppException) as get_missing_user:
        service.get_user(uuid.uuid4())
    assert get_missing_user.value.code == 1000401

    service.set_user_role(normal.id, editor_role.id)
    assert UserRepository.get_by_id(db_session, normal.id).role_id == editor_role.id

    with pytest.raises(AppException) as set_missing_user:
        service.set_user_role(uuid.uuid4(), editor_role.id)
    assert set_missing_user.value.code == 1000401

    with pytest.raises(AppException) as set_missing_role:
        service.set_user_role(normal.id, uuid.uuid4())
    assert set_missing_role.value.code == 1000403

    with pytest.raises(AppException) as bootstrap_forbidden:
        service.set_user_role(bootstrap.id, user_role.id)
    assert bootstrap_forbidden.value.code == 1000412

    service.set_user_roles_batch([normal.id, explicit_user.id], user_role.id)
    assert UserRepository.get_by_id(db_session, normal.id).role_id == user_role.id
    assert UserRepository.get_by_id(db_session, explicit_user.id).role_id == user_role.id

    service.set_user_roles_batch([], user_role.id)

    with pytest.raises(AppException) as batch_missing_role:
        service.set_user_roles_batch([normal.id], uuid.uuid4())
    assert batch_missing_role.value.code == 1000403

    with pytest.raises(AppException) as batch_missing_user:
        service.set_user_roles_batch([uuid.uuid4()], user_role.id)
    assert batch_missing_user.value.code == 1000401

    with pytest.raises(AppException) as batch_bootstrap_forbidden:
        service.set_user_roles_batch([bootstrap.id], user_role.id)
    assert batch_bootstrap_forbidden.value.code == 1000412

    service.disable_user(normal.id, disabled=True)
    assert UserRepository.get_by_id(db_session, normal.id).disabled is True

    service.disable_user(normal.id, disabled=False)
    assert UserRepository.get_by_id(db_session, normal.id).disabled is False

    with pytest.raises(AppException) as disable_missing_user:
        service.disable_user(uuid.uuid4())
    assert disable_missing_user.value.code == 1000401

    service.reset_password(normal.id, 'Aa1!reset')
    refreshed = UserRepository.get_by_id(db_session, normal.id)
    assert refreshed.password_hash == 'hashed::Aa1!reset'
    assert refreshed.updated_pwd_time is not None

    with pytest.raises(AppException) as reset_password_required:
        service.reset_password(normal.id, '   ')
    assert reset_password_required.value.code == 1000206

    with pytest.raises(AppException) as invalid_password_after_reset:
        service.reset_password(normal.id, 'bad')
    assert invalid_password_after_reset.value.code == 1000103

    with pytest.raises(AppException) as reset_missing_user:
        service.reset_password(uuid.uuid4(), 'Aa1!reset')
    assert reset_missing_user.value.code == 1000401


def test_user_service_default_role_missing_raises(service_modules, db_session, monkeypatch):
    _, _, user_service_module = service_modules
    service = UserService()

    monkeypatch.setattr(user_service_module.auth_service, 'hash_password', lambda password: f'hashed::{password}')
    monkeypatch.setattr(user_service_module.auth_service, 'validate_password', lambda password: True)

    RoleRepository.create(db_session, 'user', built_in=True)
    role = RoleRepository.create(db_session, 'editor', built_in=False)
    UserRepository.create(
        db_session,
        username='seed',
        password_hash='hashed',
        role_id=role.id,
    )

    user_role = RoleRepository.get_by_name(db_session, 'user')
    db_session.delete(user_role)
    db_session.commit()

    with pytest.raises(AppException) as default_role_missing:
        service.create_user(username='svc-user', password='Aa1!service')
    assert default_role_missing.value.code == 1000501

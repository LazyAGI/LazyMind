import uuid
import importlib

import pytest
from sqlalchemy import create_engine
from sqlalchemy.orm import sessionmaker
from sqlalchemy.pool import StaticPool

from models import Base
from repositories import (
    GroupPermissionRepository,
    GroupRepository,
    PermissionGroupRepository,
    RoleRepository,
    UserGroupRepository,
    UserRepository,
)


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
def seeded_entities(db_session):
    admin_role = RoleRepository.create(db_session, 'admin', built_in=True)
    user_role = RoleRepository.create(db_session, 'user', built_in=False)
    read_pg = PermissionGroupRepository.create(
        db_session,
        code='user.read',
        description='Read user',
        module='user',
        action='read',
    )
    write_pg = PermissionGroupRepository.create(
        db_session,
        code='user.write',
        description='Write user',
        module='user',
        action='write',
    )
    alice = UserRepository.create(
        db_session,
        username='alice',
        password_hash='hash-a',
        role_id=admin_role.id,
        tenant_id='tenant-a',
        email='alice@example.com',
        display_name='Alice',
    )
    bob = UserRepository.create(
        db_session,
        username='bob',
        password_hash='hash-b',
        role_id=user_role.id,
        tenant_id='tenant-b',
        display_name='Bobby',
    )
    group_a = GroupRepository.create(db_session, 'tenant-a', 'alpha', 'Alpha team', alice.id)
    group_b = GroupRepository.create(db_session, 'tenant-b', 'beta', 'Beta team', bob.id)
    return {
        'admin_role': admin_role,
        'user_role': user_role,
        'read_pg': read_pg,
        'write_pg': write_pg,
        'alice': alice,
        'bob': bob,
        'group_a': group_a,
        'group_b': group_b,
    }


def test_repositories_package_exports_expected_symbols():
    repositories_pkg = importlib.import_module('repositories')

    assert set(repositories_pkg.__all__) == {
        'CloudAuthConnectionRepository',
        'GroupPermissionRepository',
        'GroupRepository',
        'PermissionGroupRepository',
        'RoleRepository',
        'UserGroupRepository',
        'UserRepository',
    }


def test_permission_group_repository_create_get_and_list(db_session):
    alpha = PermissionGroupRepository.create(
        db_session,
        code='document.read',
        description='Read docs',
        module='document',
        action='read',
    )
    PermissionGroupRepository.create(
        db_session,
        code='auth.login',
        description='Login',
        module='auth',
        action='write',
    )

    listed = PermissionGroupRepository.list_all_ordered(db_session)

    assert PermissionGroupRepository.get_by_code(db_session, 'document.read').id == alpha.id
    assert [item.code for item in listed] == ['auth.login', 'document.read']


def test_role_repository_methods_cover_lookup_and_permission_replacement(db_session, seeded_entities):
    admin_role = seeded_entities['admin_role']
    read_pg = seeded_entities['read_pg']
    write_pg = seeded_entities['write_pg']

    assert RoleRepository.get_by_name(db_session, 'admin').id == admin_role.id
    assert RoleRepository.get_by_id(db_session, admin_role.id).name == 'admin'
    assert RoleRepository.get_names_in(db_session, ['admin', 'missing']) == {'admin'}
    assert RoleRepository.count(db_session) == 2
    assert [item.name for item in RoleRepository.list_all_ordered(db_session)] == ['admin', 'user']

    RoleRepository.replace_permissions(db_session, admin_role.id, {read_pg.id, write_pg.id})
    loaded = RoleRepository.get_with_permission_groups(db_session, admin_role.id)

    assert {item.code for item in loaded.permission_groups} == {'user.read', 'user.write'}

    RoleRepository.replace_permissions(db_session, admin_role.id, set())
    loaded = RoleRepository.get_with_permission_groups(db_session, admin_role.id)
    assert loaded.permission_groups == []


def test_group_repository_list_create_get_and_delete(db_session, seeded_entities):
    group_a = seeded_entities['group_a']
    group_b = seeded_entities['group_b']

    assert GroupRepository.get_by_id(db_session, group_a.id).group_name == 'alpha'
    assert GroupRepository.get_by_tenant_and_name(db_session, 'tenant-b', 'beta').id == group_b.id

    groups, total = GroupRepository.list_paginated(
        db_session,
        page=1,
        page_size=10,
        search='a',
        tenant_id='tenant-a',
    )
    assert total == 1
    assert [item.group_name for item in groups] == ['alpha']

    active_groups, active_total = GroupRepository.list_paginated(
        db_session,
        page=1,
        page_size=10,
        search='a',
        tenant_id='tenant-a',
        active_members_only=True,
    )
    assert active_total == 0
    assert active_groups == []

    UserGroupRepository.add(
        db_session,
        tenant_id='tenant-a',
        user_id=seeded_entities['alice'].id,
        group_id=group_a.id,
        role='member',
        creator_user_id=seeded_entities['alice'].id,
    )
    active_groups, active_total = GroupRepository.list_paginated(
        db_session,
        page=1,
        page_size=10,
        search='a',
        tenant_id='tenant-a',
        active_members_only=True,
    )
    assert active_total == 1
    assert [item.group_name for item in active_groups] == ['alpha']

    GroupRepository.delete(db_session, group_b)
    assert GroupRepository.get_by_id(db_session, group_b.id) is None


def test_user_group_repository_add_list_update_and_remove(db_session, seeded_entities):
    group_a = seeded_entities['group_a']
    alice = seeded_entities['alice']
    bob = seeded_entities['bob']

    owner = UserGroupRepository.add(
        db_session,
        tenant_id='tenant-a',
        user_id=alice.id,
        group_id=group_a.id,
        role='owner',
        creator_user_id=alice.id,
    )
    member = UserGroupRepository.add(
        db_session,
        tenant_id='tenant-a',
        user_id=bob.id,
        group_id=group_a.id,
        role='member',
        creator_user_id=alice.id,
    )

    listed = UserGroupRepository.list_by_group_id(db_session, group_a.id)
    pair = UserGroupRepository.get_by_group_and_user(db_session, group_a.id, bob.id, 'tenant-a')
    batch = UserGroupRepository.get_by_group_and_users(db_session, group_a.id, [alice.id, bob.id])
    no_rows = UserGroupRepository.get_by_group_and_users(db_session, group_a.id, [])

    assert owner.role == 'owner'
    assert member.role == 'member'
    assert {row.user.username for row in listed} == {'alice', 'bob'}
    assert pair.id == member.id
    assert {row.user_id for row in batch} == {alice.id, bob.id}
    assert no_rows == []

    UserGroupRepository.set_member_role(db_session, member, 'maintainer')
    assert UserGroupRepository.get_by_group_and_user(db_session, group_a.id, bob.id).role == 'maintainer'

    UserGroupRepository.remove_by_group_and_users(db_session, group_a.id, [bob.id])
    remaining = UserGroupRepository.list_by_group_id(db_session, group_a.id)
    assert [row.user_id for row in remaining] == [alice.id]


def test_group_permission_repository_replace_and_read_codes(db_session, seeded_entities):
    group_a = seeded_entities['group_a']
    read_pg = seeded_entities['read_pg']
    write_pg = seeded_entities['write_pg']

    GroupPermissionRepository.replace_permissions(db_session, group_a.id, {read_pg.id, write_pg.id})
    assert set(GroupPermissionRepository.get_permission_codes(db_session, group_a.id)) == {
        'user.read',
        'user.write',
    }

    GroupPermissionRepository.replace_permissions(db_session, group_a.id, {write_pg.id})
    assert GroupPermissionRepository.get_permission_codes(db_session, group_a.id) == ['user.write']

    GroupPermissionRepository.replace_permissions(db_session, group_a.id, set())
    assert GroupPermissionRepository.get_permission_codes(db_session, group_a.id) == []


def test_user_repository_load_options_pagination_and_profile_update(db_session, seeded_entities):
    admin_role = seeded_entities['admin_role']
    read_pg = seeded_entities['read_pg']
    alice = seeded_entities['alice']
    bob = seeded_entities['bob']
    group_a = seeded_entities['group_a']

    RoleRepository.replace_permissions(db_session, admin_role.id, {read_pg.id})
    UserGroupRepository.add(
        db_session,
        tenant_id='tenant-a',
        user_id=alice.id,
        group_id=group_a.id,
        role='owner',
        creator_user_id=alice.id,
    )
    GroupPermissionRepository.replace_permissions(db_session, group_a.id, {read_pg.id})

    bob.disabled = True
    db_session.commit()

    loaded = UserRepository.get_by_id(
        db_session,
        alice.id,
        load_role=True,
        load_permission_groups=True,
        load_groups=True,
        load_group_permission_groups=True,
    )

    assert loaded.role.name == 'admin'
    assert loaded.role.permission_groups[0].code == 'user.read'
    assert loaded.groups[0].group.permission_groups[0].code == 'user.read'

    page_items, total = UserRepository.list_paginated(
        db_session,
        page=1,
        page_size=1,
        search='bo',
        tenant_id='tenant-b',
    )
    assert total == 1
    assert [item.username for item in page_items] == ['bob']

    active_page_items, active_total = UserRepository.list_paginated(
        db_session,
        page=1,
        page_size=1,
        search='bo',
        tenant_id='tenant-b',
        active_only=True,
    )
    assert active_total == 0
    assert active_page_items == []

    updated = UserRepository.update_profile(
        db_session,
        bob.id,
        display_name='Bob',
        email='bob@example.com',
        phone='13800000000',
        remark='Updated',
    )
    assert UserRepository.count(db_session) == 2
    assert updated.display_name == 'Bob'
    assert updated.email == 'bob@example.com'
    assert updated.phone == '13800000000'
    assert updated.remark == 'Updated'
    assert UserRepository.update_profile(db_session, uuid.uuid4(), display_name='ghost') is None

    assert UserRepository.get_by_username(db_session, 'alice').id == alice.id
    assert UserRepository.get_by_id(db_session, uuid.uuid4()) is None

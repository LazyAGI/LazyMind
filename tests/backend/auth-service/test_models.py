import uuid
import importlib

import pytest
from sqlalchemy.exc import IntegrityError
from sqlalchemy import create_engine
from sqlalchemy import event
from sqlalchemy import text
from sqlalchemy.orm import joinedload, sessionmaker
from sqlalchemy.pool import StaticPool

from models import (
    Base,
    Group,
    GroupPermission,
    PermissionGroup,
    Role,
    RolePermission,
    User,
    UserGroup,
)


def create_session():
    engine = create_engine(
        'sqlite:///:memory:',
        connect_args={'check_same_thread': False},
        poolclass=StaticPool,
    )
    event.listen(engine, 'connect', lambda conn, _: conn.execute('PRAGMA foreign_keys=ON'))
    Base.metadata.create_all(engine)
    session = sessionmaker(bind=engine)()
    return session


def test_models_package_exports_expected_symbols():
    models_pkg = importlib.import_module('models')

    assert set(models_pkg.__all__) == {
        'Base',
        'CloudAuthConnection',
        'Group',
        'GroupPermission',
        'PermissionGroup',
        'Role',
        'RolePermission',
        'User',
        'UserGroup',
    }
    assert models_pkg.User is User
    assert models_pkg.Group is Group


def test_base_to_json_excludes_sqlalchemy_internal_state():
    session = create_session()
    try:
        role = Role(name='reader', built_in=False)
        session.add(role)
        session.commit()
        session.refresh(role)

        payload = role.to_json()

        assert payload['name'] == 'reader'
        assert payload['built_in'] is False
        assert '_sa_instance_state' not in payload
    finally:
        session.close()


def test_model_relationships_round_trip():
    session = create_session()
    try:
        role = Role(name='member', built_in=False)
        permission = PermissionGroup(
            code='group.read',
            description='Read groups',
            module='group',
            action='read',
        )
        session.add_all([role, permission])
        session.commit()

        role_permission = RolePermission(role_id=role.id, permission_group_id=permission.id)
        session.add(role_permission)
        session.commit()

        user = User(
            username='alice',
            display_name='Alice',
            password_hash='hashed',
            role_id=role.id,
            tenant_id='tenant-a',
        )
        session.add(user)
        session.commit()

        group = Group(
            tenant_id='tenant-a',
            group_name='dev-team',
            remark='Developers',
            creator_user_id=user.id,
        )
        session.add(group)
        session.commit()

        membership = UserGroup(
            tenant_id='tenant-a',
            user_id=user.id,
            group_id=group.id,
            role='owner',
            creator_user_id=user.id,
        )
        group_permission = GroupPermission(group_id=group.id, permission_group_id=permission.id)
        session.add_all([membership, group_permission])
        session.commit()

        loaded_user = (
            session.query(User)
            .options(joinedload(User.role), joinedload(User.groups).joinedload(UserGroup.group))
            .filter_by(id=user.id)
            .first()
        )
        loaded_role = (
            session.query(Role)
            .options(joinedload(Role.permission_groups))
            .filter_by(id=role.id)
            .first()
        )
        loaded_group = (
            session.query(Group)
            .options(
                joinedload(Group.members).joinedload(UserGroup.user),
                joinedload(Group.permission_groups),
            )
            .filter_by(id=group.id)
            .first()
        )
        loaded_permission = (
            session.query(PermissionGroup)
            .options(joinedload(PermissionGroup.roles), joinedload(PermissionGroup.groups))
            .filter_by(id=permission.id)
            .first()
        )

        assert isinstance(loaded_user.id, uuid.UUID)
        assert loaded_user.display_name == 'Alice'
        assert loaded_user.disabled is False
        assert loaded_user.source == 'platform'

        assert loaded_user.role.id == role.id
        assert loaded_user.groups[0].group.id == group.id
        assert loaded_user.groups[0].role == 'owner'

        assert loaded_role.permission_groups[0].code == 'group.read'
        assert loaded_group.members[0].user.id == user.id
        assert loaded_group.permission_groups[0].id == permission.id
        assert loaded_permission.roles[0].id == role.id
        assert loaded_permission.groups[0].id == group.id
    finally:
        session.close()


def test_model_unique_constraints_are_enforced():
    session = create_session()
    try:
        role = Role(name='user', built_in=True)
        permission = PermissionGroup(code='user.read')
        user = User(username='alice', password_hash='hashed', role=role, tenant_id='tenant-a')
        group = Group(tenant_id='tenant-a', group_name='ops')
        session.add_all([role, permission, user, group])
        session.commit()

        session.add(Role(name='user', built_in=False))
        with pytest.raises(IntegrityError):
            session.commit()
        session.rollback()

        session.add(PermissionGroup(code='user.read'))
        with pytest.raises(IntegrityError):
            session.commit()
        session.rollback()

        session.add(User(username='alice', password_hash='hashed-2', role_id=role.id, tenant_id='tenant-a'))
        with pytest.raises(IntegrityError):
            session.commit()
        session.rollback()

        session.add(Group(tenant_id='tenant-a', group_name='ops'))
        with pytest.raises(IntegrityError):
            session.commit()
        session.rollback()

        membership = UserGroup(tenant_id='tenant-a', user_id=user.id, group_id=group.id)
        session.add(membership)
        session.commit()

        session.add(UserGroup(tenant_id='tenant-a', user_id=user.id, group_id=group.id))
        with pytest.raises(IntegrityError):
            session.commit()
        session.rollback()

        binding = GroupPermission(group_id=group.id, permission_group_id=permission.id)
        session.add(binding)
        session.commit()

        session.add(GroupPermission(group_id=group.id, permission_group_id=permission.id))
        with pytest.raises(IntegrityError):
            session.commit()
        session.rollback()

        session.add(RolePermission(role_id=role.id, permission_group_id=permission.id))
        session.commit()
        session.add(RolePermission(role_id=role.id, permission_group_id=permission.id))
        with pytest.raises(IntegrityError):
            session.commit()
    finally:
        session.close()


def test_model_foreign_key_delete_behaviors():
    session = create_session()
    try:
        role_restrict = Role(name='restrict-role', built_in=False)
        role_cascade = Role(name='cascade-role', built_in=False)
        permission = PermissionGroup(code='group.manage')
        session.add_all([role_restrict, role_cascade, permission])
        session.commit()

        user = User(username='carol', password_hash='hashed', role_id=role_restrict.id, tenant_id='tenant-a')
        session.add(user)
        session.commit()

        group = Group(
            tenant_id='tenant-a',
            group_name='owners',
            creator_user_id=user.id,
        )
        session.add(group)
        session.commit()

        session.add(
            UserGroup(
                tenant_id='tenant-a',
                user_id=user.id,
                group_id=group.id,
                role='owner',
                creator_user_id=user.id,
            )
        )
        session.add(RolePermission(role_id=role_cascade.id, permission_group_id=permission.id))
        session.add(GroupPermission(group_id=group.id, permission_group_id=permission.id))
        session.commit()

        session.delete(role_restrict)
        with pytest.raises(IntegrityError):
            session.commit()
        session.rollback()

        session.delete(role_cascade)
        session.commit()
        assert session.query(RolePermission).filter_by(role_id=role_cascade.id).count() == 0

        session.delete(user)
        session.commit()
        refreshed_group = session.get(Group, group.id)
        assert refreshed_group.creator_user_id is None
        assert session.query(UserGroup).filter_by(group_id=group.id).count() == 0

        session.delete(group)
        session.commit()
        assert session.query(GroupPermission).filter_by(group_id=group.id).count() == 0

        fk_state = session.execute(text('PRAGMA foreign_keys')).scalar()
        assert fk_state == 1
    finally:
        session.close()

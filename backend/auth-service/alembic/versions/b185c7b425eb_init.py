"""init

Revision ID: b185c7b425eb
Revises:
Create Date: 2026-03-12 03:10:06.051234

"""

from alembic import op
import sqlalchemy as sa


revision = 'b185c7b425eb'
down_revision = None
branch_labels = None
depends_on = None


def upgrade() -> None:
    op.create_table(
        'permission_groups',
        sa.Column('id', sa.Uuid(), nullable=False, comment='Primary key UUID'),
        sa.Column(
            'code',
            sa.String(length=128),
            nullable=False,
            comment='Permission code, e.g. user.read / document.add',
        ),
        sa.Column(
            'description',
            sa.String(length=255),
            nullable=False,
            comment='Description text, e.g. query user / create document',
        ),
        sa.Column('module', sa.String(length=64), nullable=False, comment='Module: document / user / app / qa'),
        sa.Column('action', sa.String(length=16), nullable=False, comment='Action type: read / write / admin'),
        sa.Column(
            'created_at',
            sa.DateTime(timezone=True),
            server_default=sa.text('CURRENT_TIMESTAMP'),
            nullable=False,
            comment='Created at',
        ),
        sa.Column('updated_at', sa.DateTime(timezone=True), nullable=True, comment='Updated at'),
        sa.PrimaryKeyConstraint('id'),
    )
    op.create_index(op.f('ix_permission_groups_code'), 'permission_groups', ['code'], unique=True)
    op.create_index(op.f('ix_permission_groups_module'), 'permission_groups', ['module'], unique=False)
    op.create_table(
        'roles',
        sa.Column('id', sa.Uuid(), nullable=False, comment='Primary key UUID'),
        sa.Column('name', sa.String(length=64), nullable=False, comment='Role name'),
        sa.Column('built_in', sa.Boolean(), nullable=False, comment='Built-in role, not deletable'),
        sa.Column(
            'created_at',
            sa.DateTime(timezone=True),
            server_default=sa.text('CURRENT_TIMESTAMP'),
            nullable=False,
            comment='Created at',
        ),
        sa.Column('updated_at', sa.DateTime(timezone=True), nullable=True, comment='Updated at'),
        sa.PrimaryKeyConstraint('id'),
    )
    op.create_index(op.f('ix_roles_name'), 'roles', ['name'], unique=True)
    op.create_table(
        'role_permissions',
        sa.Column('id', sa.Uuid(), nullable=False, comment='Primary key UUID'),
        sa.Column('role_id', sa.Uuid(), nullable=False, comment='Role id'),
        sa.Column('permission_group_id', sa.Uuid(), nullable=False, comment='Permission group id'),
        sa.Column(
            'created_at',
            sa.DateTime(timezone=True),
            server_default=sa.text('CURRENT_TIMESTAMP'),
            nullable=False,
            comment='Created at',
        ),
        sa.Column('updated_at', sa.DateTime(timezone=True), nullable=True, comment='Updated at'),
        sa.ForeignKeyConstraint(['permission_group_id'], ['permission_groups.id'], ondelete='CASCADE'),
        sa.ForeignKeyConstraint(['role_id'], ['roles.id'], ondelete='CASCADE'),
        sa.PrimaryKeyConstraint('id'),
        sa.UniqueConstraint('role_id', 'permission_group_id', name='uq_role_permission'),
    )
    op.create_index(
        op.f('ix_role_permissions_permission_group_id'),
        'role_permissions',
        ['permission_group_id'],
        unique=False,
    )
    op.create_index(op.f('ix_role_permissions_role_id'), 'role_permissions', ['role_id'], unique=False)
    op.create_table(
        'users',
        sa.Column('id', sa.Uuid(), nullable=False, comment='Primary key UUID'),
        sa.Column('username', sa.String(length=128), nullable=False, comment='Username'),
        sa.Column('display_name', sa.String(length=255), nullable=False, comment='Display name'),
        sa.Column('password_hash', sa.String(length=255), nullable=False, comment='Password hash'),
        sa.Column('role_id', sa.Uuid(), nullable=False, comment='Role id, FK'),
        sa.Column('tenant_id', sa.String(length=64), nullable=False, comment='Tenant ID'),
        sa.Column('email', sa.String(length=255), nullable=True, comment='Email'),
        sa.Column('phone', sa.String(length=64), nullable=False, comment='Phone number'),
        sa.Column('remark', sa.String(length=255), nullable=False, comment='Remark'),
        sa.Column('creator', sa.String(length=128), nullable=False, comment='Creator'),
        sa.Column(
            'created_at',
            sa.DateTime(timezone=True),
            server_default=sa.text('CURRENT_TIMESTAMP'),
            nullable=False,
            comment='Created at',
        ),
        sa.Column('updated_at', sa.DateTime(timezone=True), nullable=True, comment='Updated at'),
        sa.Column('last_login_time', sa.DateTime(timezone=True), nullable=True, comment='Last login time'),
        sa.Column('updated_pwd_time', sa.DateTime(timezone=True), nullable=True, comment='Password updated at'),
        sa.Column('disabled', sa.Boolean(), nullable=False, comment='Disabled'),
        sa.Column('source', sa.String(length=32), nullable=False, comment='User source'),
        sa.ForeignKeyConstraint(['role_id'], ['roles.id'], ondelete='RESTRICT'),
        sa.PrimaryKeyConstraint('id'),
    )
    op.create_index(op.f('ix_users_disabled'), 'users', ['disabled'], unique=False)
    op.create_index(op.f('ix_users_email'), 'users', ['email'], unique=False)
    op.create_index(op.f('ix_users_role_id'), 'users', ['role_id'], unique=False)
    op.create_index(op.f('ix_users_tenant_id'), 'users', ['tenant_id'], unique=False)
    op.create_index(op.f('ix_users_username'), 'users', ['username'], unique=True)
    op.create_table(
        'groups',
        sa.Column('id', sa.Uuid(), nullable=False, comment='Primary key UUID'),
        sa.Column('tenant_id', sa.String(length=64), nullable=False, comment='Tenant id'),
        sa.Column('group_name', sa.String(length=255), nullable=False, comment='Group name'),
        sa.Column('remark', sa.String(length=255), nullable=False, comment='Remark'),
        sa.Column('creator_user_id', sa.Uuid(), nullable=True, comment='Creator user id'),
        sa.Column(
            'created_at',
            sa.DateTime(timezone=True),
            server_default=sa.text('CURRENT_TIMESTAMP'),
            nullable=False,
            comment='Created at',
        ),
        sa.Column('updated_at', sa.DateTime(timezone=True), nullable=True, comment='Updated at'),
        sa.ForeignKeyConstraint(['creator_user_id'], ['users.id'], ondelete='SET NULL'),
        sa.PrimaryKeyConstraint('id'),
        sa.UniqueConstraint('tenant_id', 'group_name', name='uq_tenant_group_name'),
    )
    op.create_index(op.f('ix_groups_creator_user_id'), 'groups', ['creator_user_id'], unique=False)
    op.create_index(op.f('ix_groups_group_name'), 'groups', ['group_name'], unique=False)
    op.create_index(op.f('ix_groups_tenant_id'), 'groups', ['tenant_id'], unique=False)
    op.create_table(
        'group_permissions',
        sa.Column('id', sa.Uuid(), nullable=False, comment='Primary key UUID'),
        sa.Column('group_id', sa.Uuid(), nullable=False, comment='Group id'),
        sa.Column('permission_group_id', sa.Uuid(), nullable=False, comment='Permission group id'),
        sa.Column(
            'created_at',
            sa.DateTime(timezone=True),
            server_default=sa.text('CURRENT_TIMESTAMP'),
            nullable=False,
            comment='Created at',
        ),
        sa.Column('updated_at', sa.DateTime(timezone=True), nullable=True, comment='Updated at'),
        sa.ForeignKeyConstraint(['group_id'], ['groups.id'], ondelete='CASCADE'),
        sa.ForeignKeyConstraint(['permission_group_id'], ['permission_groups.id'], ondelete='CASCADE'),
        sa.PrimaryKeyConstraint('id'),
        sa.UniqueConstraint('group_id', 'permission_group_id', name='uq_group_permission'),
    )
    op.create_index(op.f('ix_group_permissions_group_id'), 'group_permissions', ['group_id'], unique=False)
    op.create_index(
        op.f('ix_group_permissions_permission_group_id'),
        'group_permissions',
        ['permission_group_id'],
        unique=False,
    )
    op.create_table(
        'user_groups',
        sa.Column('id', sa.Uuid(), nullable=False, comment='Primary key UUID'),
        sa.Column('tenant_id', sa.String(length=64), nullable=False, comment='Tenant id'),
        sa.Column('user_id', sa.Uuid(), nullable=False, comment='User id'),
        sa.Column('group_id', sa.Uuid(), nullable=False, comment='Group id'),
        sa.Column('role', sa.String(length=16), nullable=False, comment='Role in group, e.g. member'),
        sa.Column('creator_user_id', sa.Uuid(), nullable=True, comment='Creator user id who added this member'),
        sa.Column(
            'created_at',
            sa.DateTime(timezone=True),
            server_default=sa.text('CURRENT_TIMESTAMP'),
            nullable=False,
            comment='Created at',
        ),
        sa.Column('updated_at', sa.DateTime(timezone=True), nullable=True, comment='Updated at'),
        sa.ForeignKeyConstraint(['creator_user_id'], ['users.id'], ondelete='SET NULL'),
        sa.ForeignKeyConstraint(['group_id'], ['groups.id'], ondelete='CASCADE'),
        sa.ForeignKeyConstraint(['user_id'], ['users.id'], ondelete='CASCADE'),
        sa.PrimaryKeyConstraint('id'),
        sa.UniqueConstraint('tenant_id', 'user_id', 'group_id', name='uq_tenant_user_group'),
    )
    op.create_index(op.f('ix_user_groups_creator_user_id'), 'user_groups', ['creator_user_id'], unique=False)
    op.create_index(op.f('ix_user_groups_group_id'), 'user_groups', ['group_id'], unique=False)
    op.create_index(op.f('ix_user_groups_tenant_id'), 'user_groups', ['tenant_id'], unique=False)
    op.create_index(op.f('ix_user_groups_user_id'), 'user_groups', ['user_id'], unique=False)


def downgrade() -> None:
    op.drop_index(op.f('ix_user_groups_user_id'), table_name='user_groups')
    op.drop_index(op.f('ix_user_groups_tenant_id'), table_name='user_groups')
    op.drop_index(op.f('ix_user_groups_group_id'), table_name='user_groups')
    op.drop_index(op.f('ix_user_groups_creator_user_id'), table_name='user_groups')
    op.drop_table('user_groups')
    op.drop_index(op.f('ix_group_permissions_permission_group_id'), table_name='group_permissions')
    op.drop_index(op.f('ix_group_permissions_group_id'), table_name='group_permissions')
    op.drop_table('group_permissions')
    op.drop_index(op.f('ix_groups_tenant_id'), table_name='groups')
    op.drop_index(op.f('ix_groups_group_name'), table_name='groups')
    op.drop_index(op.f('ix_groups_creator_user_id'), table_name='groups')
    op.drop_table('groups')
    op.drop_index(op.f('ix_users_username'), table_name='users')
    op.drop_index(op.f('ix_users_tenant_id'), table_name='users')
    op.drop_index(op.f('ix_users_role_id'), table_name='users')
    op.drop_index(op.f('ix_users_email'), table_name='users')
    op.drop_index(op.f('ix_users_disabled'), table_name='users')
    op.drop_table('users')
    op.drop_index(op.f('ix_role_permissions_role_id'), table_name='role_permissions')
    op.drop_index(op.f('ix_role_permissions_permission_group_id'), table_name='role_permissions')
    op.drop_table('role_permissions')
    op.drop_index(op.f('ix_roles_name'), table_name='roles')
    op.drop_table('roles')
    op.drop_index(op.f('ix_permission_groups_module'), table_name='permission_groups')
    op.drop_index(op.f('ix_permission_groups_code'), table_name='permission_groups')
    op.drop_table('permission_groups')

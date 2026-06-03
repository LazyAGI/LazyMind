"""add cloud auth connection owner

Revision ID: d8b8d6142f1a
Revises: c9d0d3d31d10
Create Date: 2026-05-30 00:00:00.000000

"""

from alembic import op
import sqlalchemy as sa


revision = 'd8b8d6142f1a'
down_revision = 'c9d0d3d31d10'
branch_labels = None
depends_on = None


def upgrade() -> None:
    op.add_column(
        'cloud_auth_connections',
        sa.Column('owner_user_id', sa.String(length=64), nullable=False, server_default=''),
    )
    op.add_column(
        'cloud_auth_connections',
        sa.Column('provider_account_id', sa.String(length=255), nullable=False, server_default=''),
    )
    op.add_column(
        'cloud_auth_connections',
        sa.Column('display_name', sa.String(length=255), nullable=False, server_default=''),
    )
    op.add_column(
        'cloud_auth_connections',
        sa.Column('provider_tenant_key', sa.String(length=255), nullable=False, server_default=''),
    )
    op.add_column(
        'cloud_auth_connections',
        sa.Column('provider_account_meta', sa.Text(), nullable=False, server_default=''),
    )
    op.add_column(
        'cloud_auth_connections',
        sa.Column('scope', sa.Text(), nullable=False, server_default=''),
    )
    op.add_column(
        'cloud_auth_connections',
        sa.Column('last_used_at', sa.DateTime(timezone=True), nullable=True),
    )
    op.create_index(
        op.f('ix_cloud_auth_connections_owner_user_id'),
        'cloud_auth_connections',
        ['owner_user_id'],
        unique=False,
    )
    op.create_index(
        op.f('ix_cloud_auth_connections_provider_account_id'),
        'cloud_auth_connections',
        ['provider_account_id'],
        unique=False,
    )


def downgrade() -> None:
    op.drop_index(op.f('ix_cloud_auth_connections_provider_account_id'), table_name='cloud_auth_connections')
    op.drop_index(op.f('ix_cloud_auth_connections_owner_user_id'), table_name='cloud_auth_connections')
    op.drop_column('cloud_auth_connections', 'last_used_at')
    op.drop_column('cloud_auth_connections', 'scope')
    op.drop_column('cloud_auth_connections', 'provider_account_meta')
    op.drop_column('cloud_auth_connections', 'provider_tenant_key')
    op.drop_column('cloud_auth_connections', 'display_name')
    op.drop_column('cloud_auth_connections', 'provider_account_id')
    op.drop_column('cloud_auth_connections', 'owner_user_id')

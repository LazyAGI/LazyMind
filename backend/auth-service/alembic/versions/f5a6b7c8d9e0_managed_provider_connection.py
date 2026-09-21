"""add managed provider connection mirror fields

Revision ID: f5a6b7c8d9e0
Revises: e4f6a7b8c9d0
Create Date: 2026-09-01 00:00:00.000000
"""

from alembic import op
import sqlalchemy as sa


revision = 'f5a6b7c8d9e0'
down_revision = 'e4f6a7b8c9d0'
branch_labels = None
depends_on = None


def upgrade() -> None:
    op.add_column(
        'cloud_auth_connections',
        sa.Column('connection_method', sa.String(length=32), nullable=False, server_default='legacy_byo'),
    )
    op.add_column(
        'cloud_auth_connections',
        sa.Column('credential_location', sa.String(length=16), nullable=False, server_default='local'),
    )
    op.add_column('cloud_auth_connections', sa.Column('cloud_connection_id', sa.String(length=64), nullable=True))
    op.add_column(
        'cloud_auth_connections',
        sa.Column('cloud_owner_user_id', sa.String(length=64), nullable=False, server_default=''),
    )
    op.add_column(
        'cloud_auth_connections',
        sa.Column('provider_workspace_id', sa.String(length=255), nullable=False, server_default=''),
    )
    op.add_column(
        'cloud_auth_connections',
        sa.Column('capability_contract_version', sa.String(length=64), nullable=False, server_default=''),
    )
    op.create_index('ix_cloud_auth_connections_cloud_connection_id', 'cloud_auth_connections', ['cloud_connection_id'])
    op.create_index('ix_cloud_auth_connections_cloud_owner_user_id', 'cloud_auth_connections', ['cloud_owner_user_id'])


def downgrade() -> None:
    op.drop_index('ix_cloud_auth_connections_cloud_owner_user_id', table_name='cloud_auth_connections')
    op.drop_index('ix_cloud_auth_connections_cloud_connection_id', table_name='cloud_auth_connections')
    op.drop_column('cloud_auth_connections', 'capability_contract_version')
    op.drop_column('cloud_auth_connections', 'provider_workspace_id')
    op.drop_column('cloud_auth_connections', 'cloud_owner_user_id')
    op.drop_column('cloud_auth_connections', 'cloud_connection_id')
    op.drop_column('cloud_auth_connections', 'credential_location')
    op.drop_column('cloud_auth_connections', 'connection_method')

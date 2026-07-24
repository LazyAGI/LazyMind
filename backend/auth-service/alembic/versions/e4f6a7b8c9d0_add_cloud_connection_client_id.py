"""add cloud connection client identity

Revision ID: e4f6a7b8c9d0
Revises: d8b8d6142f1a
Create Date: 2026-07-21 00:00:00.000000

"""

from alembic import op
import sqlalchemy as sa


revision = 'e4f6a7b8c9d0'
down_revision = 'd8b8d6142f1a'
branch_labels = None
depends_on = None


_IDENTITY_PREDICATE = "client_id IS NOT NULL AND auth_mode IN ('tenant', 'service_account')"
_UNIQUE_INDEX = 'uq_cloud_auth_connections_owner_provider_mode_client'


def upgrade() -> None:
    op.add_column(
        'cloud_auth_connections',
        sa.Column(
            'client_id',
            sa.String(length=255),
            nullable=True,
            comment='Normalized cloud app/integration id',
        ),
    )
    op.create_index(
        _UNIQUE_INDEX,
        'cloud_auth_connections',
        ['owner_user_id', 'provider', 'auth_mode', 'client_id'],
        unique=True,
        postgresql_where=sa.text(_IDENTITY_PREDICATE),
        sqlite_where=sa.text(_IDENTITY_PREDICATE),
    )


def downgrade() -> None:
    op.drop_index(_UNIQUE_INDEX, table_name='cloud_auth_connections')
    op.drop_column('cloud_auth_connections', 'client_id')

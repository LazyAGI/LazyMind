"""add cloud_auth_connections

Revision ID: c9d0d3d31d10
Revises: b185c7b425eb
Create Date: 2026-04-18 18:30:00.000000

"""

from alembic import op
import sqlalchemy as sa


revision = 'c9d0d3d31d10'
down_revision = 'b185c7b425eb'
branch_labels = None
depends_on = None


def upgrade() -> None:
    op.create_table(
        'cloud_auth_connections',
        sa.Column('connection_id', sa.String(length=64), nullable=False),
        sa.Column('tenant_id', sa.String(length=64), nullable=False),
        sa.Column('provider', sa.String(length=64), nullable=False),
        sa.Column('auth_mode', sa.String(length=32), nullable=False, server_default='oauth_user'),
        sa.Column('credential_ciphertext', sa.Text(), nullable=False),
        sa.Column('auth_state_ciphertext', sa.Text(), nullable=False, server_default=''),
        sa.Column('status', sa.String(length=32), nullable=False, server_default='ACTIVE'),
        sa.Column('last_error', sa.Text(), nullable=False, server_default=''),
        sa.Column('created_at', sa.DateTime(timezone=True), nullable=False, server_default=sa.text('CURRENT_TIMESTAMP')),
        sa.Column('updated_at', sa.DateTime(timezone=True), nullable=True),
        sa.PrimaryKeyConstraint('connection_id'),
    )
    op.create_index(op.f('ix_cloud_auth_connections_tenant_id'), 'cloud_auth_connections', ['tenant_id'], unique=False)
    op.create_index(op.f('ix_cloud_auth_connections_provider'), 'cloud_auth_connections', ['provider'], unique=False)
    op.create_index(op.f('ix_cloud_auth_connections_status'), 'cloud_auth_connections', ['status'], unique=False)


def downgrade() -> None:
    op.drop_index(op.f('ix_cloud_auth_connections_status'), table_name='cloud_auth_connections')
    op.drop_index(op.f('ix_cloud_auth_connections_provider'), table_name='cloud_auth_connections')
    op.drop_index(op.f('ix_cloud_auth_connections_tenant_id'), table_name='cloud_auth_connections')
    op.drop_table('cloud_auth_connections')

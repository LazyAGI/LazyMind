"""add Feishu CLI profile reference

Revision ID: a6b7c8d9e0f1
Revises: f5a6b7c8d9e0
Create Date: 2026-09-04 00:00:00.000000
"""

from alembic import op
import sqlalchemy as sa


revision = 'a6b7c8d9e0f1'
down_revision = 'f5a6b7c8d9e0'
branch_labels = None
depends_on = None


def upgrade() -> None:
    op.add_column(
        'cloud_auth_connections',
        sa.Column('profile_ref', sa.String(length=255), nullable=False, server_default=''),
    )


def downgrade() -> None:
    op.drop_column('cloud_auth_connections', 'profile_ref')

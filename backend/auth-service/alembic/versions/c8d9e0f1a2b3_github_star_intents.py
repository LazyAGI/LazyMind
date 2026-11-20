"""Persist fixed-project GitHub support intents and finite execution budgets."""
import sqlalchemy as sa
from alembic import op

revision = 'c8d9e0f1a2b3'
down_revision = 'b7c8d9e0f1a2'
branch_labels = None
depends_on = None


def upgrade():
    op.create_table(
        'github_star_intents',
        sa.Column('intent_id', sa.String(64), primary_key=True),
        sa.Column('owner_user_id', sa.String(64), nullable=False),
        sa.Column('repository', sa.String(255), nullable=False, server_default='LazyAGI/LazyMind'),
        sa.Column('source', sa.String(16), nullable=False),
        sa.Column('connection_id', sa.String(64), nullable=True),
        sa.Column('account_id', sa.String(255), nullable=True),
        sa.Column('credential_fingerprint', sa.String(64), nullable=True),
        sa.Column('status', sa.String(32), nullable=False, server_default='needs_attention'),
        sa.Column('reason', sa.String(64), nullable=False, server_default='binding_not_captured'),
        sa.Column('remote_state', sa.String(16), nullable=False, server_default='unknown'),
        sa.Column('created_at', sa.DateTime(timezone=True), nullable=False, server_default=sa.func.now()),
        sa.Column('expires_at', sa.DateTime(timezone=True), nullable=False),
        sa.Column('attempt_count', sa.Integer(), nullable=False, server_default=sa.text('0')),
        sa.Column('next_attempt_at', sa.DateTime(timezone=True), nullable=True),
        sa.Column('rate_limit_not_before', sa.DateTime(timezone=True), nullable=True),
        sa.Column('updated_at', sa.DateTime(timezone=True), nullable=False, server_default=sa.func.now()),
        sa.UniqueConstraint('owner_user_id', 'repository', name='uq_github_star_intents_owner_repository'),
    )
    op.create_index('ix_github_star_intents_status_next_attempt', 'github_star_intents', ['status', 'next_attempt_at'])


def downgrade():
    op.drop_index('ix_github_star_intents_status_next_attempt', table_name='github_star_intents')
    op.drop_table('github_star_intents')

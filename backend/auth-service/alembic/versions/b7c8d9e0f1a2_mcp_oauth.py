"""Personal MCP OAuth grants and single-use transactions."""
import sqlalchemy as sa
from alembic import op

revision = 'b7c8d9e0f1a2'
down_revision = 'a6b7c8d9e0f1'
branch_labels = None
depends_on = None


def upgrade():
    op.create_table('mcp_oauth_grants',
                    sa.Column('grant_id', sa.String(64), primary_key=True),
                    sa.Column('user_id', sa.String(64), nullable=False),
                    sa.Column('server_id', sa.String(128), nullable=False),
                    sa.Column('server_url', sa.Text(), nullable=False),
                    sa.Column('grant_version', sa.Integer(), nullable=False),
                    sa.Column('token_version', sa.Integer(), nullable=False),
                    sa.Column('status', sa.String(32), nullable=False),
                    sa.Column('ciphertext', sa.Text(), nullable=False),
                    sa.Column('expires_at', sa.Float(), nullable=False),
                    sa.Column('lease_id', sa.String(64), nullable=False),
                    sa.Column('lease_until', sa.Float(), nullable=False),
                    sa.UniqueConstraint('user_id', 'server_id', name='uq_mcp_oauth_owner_server'))
    op.create_table('mcp_oauth_states',
                    sa.Column('state_hash', sa.String(64), primary_key=True),
                    sa.Column('grant_id', sa.String(64), nullable=False),
                    sa.Column('grant_version', sa.Integer(), nullable=False),
                    sa.Column('expires_at', sa.Float(), nullable=False),
                    sa.Column('ciphertext', sa.Text(), nullable=False))
    op.create_index('ix_mcp_oauth_states_grant_id', 'mcp_oauth_states', ['grant_id'])


def downgrade():
    op.drop_table('mcp_oauth_states')
    op.drop_table('mcp_oauth_grants')

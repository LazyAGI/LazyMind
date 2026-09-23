from sqlalchemy import Float, Integer, String, Text, UniqueConstraint
from sqlalchemy.orm import mapped_column

from .base import Base


class MCPOAuthGrant(Base):
    __tablename__ = 'mcp_oauth_grants'
    __table_args__ = (UniqueConstraint('user_id', 'server_id', name='uq_mcp_oauth_owner_server'),)
    grant_id = mapped_column(String(64), primary_key=True)
    user_id = mapped_column(String(64), nullable=False)
    server_id = mapped_column(String(128), nullable=False)
    server_url = mapped_column(Text, nullable=False)
    grant_version = mapped_column(Integer, nullable=False)
    token_version = mapped_column(Integer, nullable=False, default=0)
    status = mapped_column(String(32), nullable=False)
    ciphertext = mapped_column(Text, nullable=False, default='')
    expires_at = mapped_column(Float, nullable=False, default=0)
    lease_id = mapped_column(String(64), nullable=False, default='')
    lease_until = mapped_column(Float, nullable=False, default=0)


class MCPOAuthState(Base):
    __tablename__ = 'mcp_oauth_states'
    state_hash = mapped_column(String(64), primary_key=True)
    grant_id = mapped_column(String(64), nullable=False, index=True)
    grant_version = mapped_column(Integer, nullable=False)
    expires_at = mapped_column(Float, nullable=False)
    ciphertext = mapped_column(Text, nullable=False)

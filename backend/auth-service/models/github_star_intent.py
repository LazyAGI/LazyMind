import uuid

from sqlalchemy import DateTime, Index, Integer, String, UniqueConstraint, func
from sqlalchemy.orm import mapped_column

from .base import Base


class GitHubStarIntent(Base):
    __tablename__ = 'github_star_intents'
    __table_args__ = (
        UniqueConstraint('owner_user_id', 'repository', name='uq_github_star_intents_owner_repository'),
        Index('ix_github_star_intents_status_next_attempt', 'status', 'next_attempt_at'),
    )

    intent_id = mapped_column(String(64), primary_key=True, default=lambda: uuid.uuid4().hex)
    owner_user_id = mapped_column(String(64), nullable=False)
    repository = mapped_column(String(255), nullable=False, default='LazyAGI/LazyMind')
    source = mapped_column(String(16), nullable=False)
    connection_id = mapped_column(String(64), nullable=True)
    account_id = mapped_column(String(255), nullable=True)
    credential_fingerprint = mapped_column(String(64), nullable=True)
    status = mapped_column(String(32), nullable=False, default='needs_attention')
    reason = mapped_column(String(64), nullable=False, default='binding_not_captured')
    remote_state = mapped_column(String(16), nullable=False, default='unknown')
    created_at = mapped_column(DateTime(timezone=True), nullable=False, server_default=func.now())
    expires_at = mapped_column(DateTime(timezone=True), nullable=False)
    attempt_count = mapped_column(Integer, nullable=False, default=0)
    next_attempt_at = mapped_column(DateTime(timezone=True), nullable=True)
    rate_limit_not_before = mapped_column(DateTime(timezone=True), nullable=True)
    updated_at = mapped_column(DateTime(timezone=True), nullable=False,
                               server_default=func.now(), onupdate=func.now())

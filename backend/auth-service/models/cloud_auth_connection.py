import uuid

from sqlalchemy import DateTime, Index, String, Text, func, text
from sqlalchemy.orm import mapped_column

from .base import Base


class CloudAuthConnection(Base):
    __tablename__ = 'cloud_auth_connections'
    __table_args__ = (
        Index(
            'uq_cloud_auth_connections_owner_provider_mode_client',
            'owner_user_id',
            'provider',
            'auth_mode',
            'client_id',
            unique=True,
            postgresql_where=text("client_id IS NOT NULL AND auth_mode IN ('tenant', 'service_account')"),
            sqlite_where=text("client_id IS NOT NULL AND auth_mode IN ('tenant', 'service_account')"),
        ),
    )

    connection_id = mapped_column(String(64), primary_key=True, default=lambda: f'conn_{uuid.uuid4().hex}')
    tenant_id = mapped_column(String(64), nullable=False, index=True, default='', comment='Tenant id')
    owner_user_id = mapped_column(String(64), nullable=False, index=True, default='', comment='Owner user id')
    provider = mapped_column(String(64), nullable=False, index=True, comment='Cloud provider name')
    auth_mode = mapped_column(
        String(32),
        nullable=False,
        default='oauth_user',
        comment='tenant/oauth_user/service_account',
    )
    client_id = mapped_column(String(255), nullable=True, comment='Normalized cloud app/integration id')
    credential_ciphertext = mapped_column(Text, nullable=False, comment='Encrypted app credential payload')
    auth_state_ciphertext = mapped_column(Text, nullable=False, default='', comment='Encrypted token/auth state')
    provider_account_id = mapped_column(
        String(255),
        nullable=False,
        default='',
        index=True,
        comment='Provider account id',
    )
    display_name = mapped_column(String(255), nullable=False, default='', comment='Display name')
    provider_tenant_key = mapped_column(String(255), nullable=False, default='', comment='Provider tenant key')
    provider_account_meta = mapped_column(Text, nullable=False, default='', comment='Provider account metadata JSON')
    scope = mapped_column(Text, nullable=False, default='', comment='Granted or requested scope')
    last_used_at = mapped_column(DateTime(timezone=True), nullable=True, comment='Last used at')
    status = mapped_column(
        String(32),
        nullable=False,
        default='ACTIVE',
        index=True,
        comment='PENDING/ACTIVE/EXPIRED/ERROR/REVOKED',
    )
    last_error = mapped_column(Text, nullable=False, default='', comment='Last error message')
    created_at = mapped_column(
        DateTime(timezone=True),
        nullable=False,
        server_default=func.now(),
        comment='Created at',
    )
    updated_at = mapped_column(
        DateTime(timezone=True),
        nullable=True,
        onupdate=func.now(),
        comment='Updated at',
    )

from sqlalchemy.orm import Session

from models import CloudAuthConnection


class CloudAuthConnectionRepository:
    @classmethod
    def get_by_id(cls, session: Session, connection_id: str) -> CloudAuthConnection | None:
        return (
            session.query(CloudAuthConnection)
            .filter(CloudAuthConnection.connection_id == (connection_id or '').strip())
            .first()
        )

    @classmethod
    def create(
        cls,
        session: Session,
        *,
        connection_id: str,
        tenant_id: str,
        owner_user_id: str = '',
        provider: str,
        auth_mode: str,
        credential_ciphertext: str,
        auth_state_ciphertext: str,
        provider_account_id: str = '',
        display_name: str = '',
        provider_tenant_key: str = '',
        provider_account_meta: str = '',
        scope: str = '',
        status: str = 'ACTIVE',
        last_error: str = '',
    ) -> CloudAuthConnection:
        row = CloudAuthConnection(
            connection_id=(connection_id or '').strip(),
            tenant_id=(tenant_id or '').strip(),
            owner_user_id=(owner_user_id or '').strip(),
            provider=(provider or '').strip().lower(),
            auth_mode=(auth_mode or '').strip().lower(),
            credential_ciphertext=credential_ciphertext,
            auth_state_ciphertext=auth_state_ciphertext,
            provider_account_id=(provider_account_id or '').strip(),
            display_name=(display_name or '').strip(),
            provider_tenant_key=(provider_tenant_key or '').strip(),
            provider_account_meta=provider_account_meta or '',
            scope=(scope or '').strip(),
            status=(status or '').strip().upper() or 'ACTIVE',
            last_error=last_error or '',
        )
        session.add(row)
        session.commit()
        session.refresh(row)
        return row

    @classmethod
    def save(cls, session: Session, row: CloudAuthConnection) -> CloudAuthConnection:
        session.add(row)
        session.commit()
        session.refresh(row)
        return row

    @classmethod
    def list_for_owner(
        cls,
        session: Session,
        *,
        owner_user_id: str,
        provider: str | None = None,
        auth_mode: str | None = None,
        status: str | None = None,
        exclude_auth_modes: tuple[str, ...] | None = None,
    ) -> list[CloudAuthConnection]:
        q = session.query(CloudAuthConnection).filter(
            CloudAuthConnection.tenant_id == '',
            CloudAuthConnection.owner_user_id == (owner_user_id or '').strip(),
        )
        if provider:
            q = q.filter(CloudAuthConnection.provider == provider.strip().lower())
        if auth_mode:
            q = q.filter(CloudAuthConnection.auth_mode == auth_mode.strip().lower())
        elif exclude_auth_modes:
            normalized_modes = tuple(
                mode.strip().lower()
                for mode in exclude_auth_modes
                if mode and mode.strip()
            )
            if normalized_modes:
                q = q.filter(~CloudAuthConnection.auth_mode.in_(normalized_modes))
        if status:
            q = q.filter(CloudAuthConnection.status == status.strip().upper())
        return q.order_by(CloudAuthConnection.updated_at.desc(), CloudAuthConnection.created_at.desc()).all()

    @classmethod
    def find_latest_for_owner(
        cls,
        session: Session,
        *,
        owner_user_id: str,
        provider: str,
        auth_mode: str,
        status: str | None = None,
    ) -> CloudAuthConnection | None:
        q = session.query(CloudAuthConnection).filter(
            CloudAuthConnection.tenant_id == '',
            CloudAuthConnection.owner_user_id == (owner_user_id or '').strip(),
            CloudAuthConnection.provider == (provider or '').strip().lower(),
            CloudAuthConnection.auth_mode == (auth_mode or '').strip().lower(),
        )
        if status:
            q = q.filter(CloudAuthConnection.status == status.strip().upper())
        return q.order_by(CloudAuthConnection.updated_at.desc(), CloudAuthConnection.created_at.desc()).first()

    @classmethod
    def find_by_provider_account(
        cls,
        session: Session,
        *,
        owner_user_id: str,
        provider: str,
        auth_mode: str,
        provider_account_id: str,
        status: str | None = None,
        exclude_statuses: tuple[str, ...] | None = None,
    ) -> CloudAuthConnection | None:
        account_id = (provider_account_id or '').strip()
        if not account_id:
            return None
        q = session.query(CloudAuthConnection).filter(
            CloudAuthConnection.tenant_id == '',
            CloudAuthConnection.owner_user_id == (owner_user_id or '').strip(),
            CloudAuthConnection.provider == (provider or '').strip().lower(),
            CloudAuthConnection.auth_mode == (auth_mode or '').strip().lower(),
            CloudAuthConnection.provider_account_id == account_id,
        )
        if status:
            q = q.filter(CloudAuthConnection.status == status.strip().upper())
        elif exclude_statuses:
            normalized_statuses = tuple(
                item.strip().upper()
                for item in exclude_statuses
                if item and item.strip()
            )
            if normalized_statuses:
                q = q.filter(~CloudAuthConnection.status.in_(normalized_statuses))
        return q.order_by(CloudAuthConnection.updated_at.desc(), CloudAuthConnection.created_at.desc()).first()

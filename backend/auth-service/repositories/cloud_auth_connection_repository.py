import json

from sqlalchemy.orm import Session

from models import CloudAuthConnection


class CloudAuthConnectionRepository:
    @staticmethod
    def _preserve_feishu_chat_preference(row: CloudAuthConnection, incoming: str) -> str:
        if row.provider != 'feishu':
            return incoming
        previous = json.loads(row.provider_account_meta or '{}')
        meta = json.loads(incoming)
        enabled = previous.get('chat_enabled', previous.get('chatEnabled', False))
        meta.update(chat_enabled=enabled, chatEnabled=enabled)
        return json.dumps(meta, ensure_ascii=False)

    @classmethod
    def get_for_owner(cls, session: Session, connection_id: str, owner_user_id: str) -> CloudAuthConnection | None:
        return (
            session.query(CloudAuthConnection)
            .filter(
                CloudAuthConnection.connection_id == (connection_id or '').strip(),
                CloudAuthConnection.owner_user_id == (owner_user_id or '').strip(),
            )
            .first()
        )

    @classmethod
    def upsert_managed(
        cls,
        session: Session,
        *,
        connection_id: str,
        owner_user_id: str,
        cloud_owner_user_id: str,
        provider: str,
        display_name: str,
        provider_tenant_key: str,
        provider_workspace_id: str,
        provider_account_meta: str,
        status: str,
        capability_contract_version: str,
    ) -> CloudAuthConnection:
        normalized_id = (connection_id or '').strip()
        normalized_owner = (owner_user_id or '').strip()
        row = cls.get_for_owner(session, normalized_id, normalized_owner)
        if row is None:
            row = CloudAuthConnection(
                connection_id=normalized_id,
                tenant_id='',
                owner_user_id=normalized_owner,
                provider=(provider or '').strip().lower(),
                auth_mode='oauth_user',
                client_id=None,
                connection_method='managed_oauth',
                credential_location='cloud',
                cloud_connection_id=normalized_id,
                cloud_owner_user_id=(cloud_owner_user_id or '').strip(),
                credential_ciphertext='managed-reference-v1',
                auth_state_ciphertext='managed-reference-v1',
                provider_account_id='',
                display_name=(display_name or '').strip(),
                provider_tenant_key=(provider_tenant_key or '').strip(),
                provider_workspace_id=(provider_workspace_id or '').strip(),
                provider_account_meta=provider_account_meta,
                scope='',
                capability_contract_version=(capability_contract_version or '').strip(),
                status=(status or '').strip().upper(),
                last_error='',
            )
            session.add(row)
        else:
            if row.connection_method != 'managed_oauth' or row.provider != (provider or '').strip().lower():
                raise ValueError('managed connection identity conflict')
            row.display_name = (display_name or '').strip()
            row.provider_tenant_key = (provider_tenant_key or '').strip()
            row.provider_workspace_id = (provider_workspace_id or '').strip()
            row.provider_account_meta = cls._preserve_feishu_chat_preference(row, provider_account_meta)
            row.capability_contract_version = (capability_contract_version or '').strip()
            row.cloud_owner_user_id = (cloud_owner_user_id or '').strip()
            row.status = (status or '').strip().upper()
            row.last_error = ''
        session.commit()
        session.refresh(row)
        return row

    @classmethod
    def upsert_feishu_cli(
        cls,
        session: Session,
        *,
        connection_id: str,
        owner_user_id: str,
        display_name: str,
        provider_account_id: str,
        provider_tenant_key: str,
        provider_workspace_id: str,
        provider_account_meta: str,
        profile_ref: str,
        granted_scopes: str,
        credential_location: str,
        status: str,
        capability_contract_version: str,
    ) -> CloudAuthConnection:
        normalized_id = (connection_id or '').strip()
        normalized_owner = (owner_user_id or '').strip()
        row = cls.get_for_owner(session, normalized_id, normalized_owner)
        if row is None:
            row = CloudAuthConnection(
                connection_id=normalized_id,
                tenant_id='',
                owner_user_id=normalized_owner,
                provider='feishu',
                auth_mode='oauth_user',
                client_id=None,
                connection_method='cli_personal_app',
                credential_location=credential_location,
                profile_ref=(profile_ref or '').strip(),
                cloud_connection_id=None,
                cloud_owner_user_id='',
                credential_ciphertext='cli-profile-reference-v1',
                auth_state_ciphertext='cli-profile-reference-v1',
                provider_account_id=(provider_account_id or '').strip(),
                display_name=(display_name or '').strip(),
                provider_tenant_key=(provider_tenant_key or '').strip(),
                provider_workspace_id=(provider_workspace_id or '').strip(),
                provider_account_meta=provider_account_meta,
                scope=(granted_scopes or '').strip(),
                capability_contract_version=(capability_contract_version or '').strip(),
                status=(status or '').strip().upper(),
                last_error='',
            )
            session.add(row)
        else:
            if row.connection_method != 'cli_personal_app' or row.provider != 'feishu':
                raise ValueError('Feishu CLI connection identity conflict')
            if row.profile_ref != (profile_ref or '').strip():
                raise ValueError('Feishu CLI profile identity conflict')
            if row.provider_account_id and row.provider_account_id != (provider_account_id or '').strip():
                raise ValueError('Feishu CLI account identity conflict')
            if row.provider_tenant_key and row.provider_tenant_key != (provider_tenant_key or '').strip():
                raise ValueError('Feishu CLI tenant identity conflict')
            row.display_name = (display_name or '').strip()
            row.provider_account_id = (provider_account_id or '').strip()
            row.provider_tenant_key = (provider_tenant_key or '').strip()
            row.provider_workspace_id = (provider_workspace_id or '').strip()
            row.provider_account_meta = cls._preserve_feishu_chat_preference(row, provider_account_meta)
            row.scope = (granted_scopes or '').strip()
            row.credential_location = credential_location
            row.capability_contract_version = (capability_contract_version or '').strip()
            row.status = (status or '').strip().upper()
            row.last_error = ''
        session.commit()
        session.refresh(row)
        return row

    @classmethod
    def get_by_id(cls, session: Session, connection_id: str) -> CloudAuthConnection | None:
        return (
            session.query(CloudAuthConnection)
            .filter(CloudAuthConnection.connection_id == (connection_id or '').strip())
            .first()
        )

    @classmethod
    def list_by_ids(cls, session: Session, connection_ids: list[str]) -> list[CloudAuthConnection]:
        ids = []
        seen = set()
        for connection_id in connection_ids or []:
            normalized = (connection_id or '').strip()
            if normalized and normalized not in seen:
                ids.append(normalized)
                seen.add(normalized)
        if not ids:
            return []
        return (
            session.query(CloudAuthConnection)
            .filter(CloudAuthConnection.connection_id.in_(ids))
            .all()
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
        client_id: str | None = None,
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
            client_id=(client_id or '').strip() or None,
            connection_method='legacy_byo',
            credential_location='local',
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
    def find_by_client_identity(
        cls,
        session: Session,
        *,
        owner_user_id: str,
        provider: str,
        auth_mode: str,
        client_id: str,
    ) -> CloudAuthConnection | None:
        return (
            session.query(CloudAuthConnection)
            .filter(
                CloudAuthConnection.tenant_id == '',
                CloudAuthConnection.owner_user_id == (owner_user_id or '').strip(),
                CloudAuthConnection.provider == (provider or '').strip().lower(),
                CloudAuthConnection.auth_mode == (auth_mode or '').strip().lower(),
                CloudAuthConnection.client_id == (client_id or '').strip(),
            )
            .first()
        )

    @classmethod
    def list_without_client_identity(
        cls,
        session: Session,
        *,
        owner_user_id: str,
        provider: str,
        auth_mode: str,
    ) -> list[CloudAuthConnection]:
        return (
            session.query(CloudAuthConnection)
            .filter(
                CloudAuthConnection.tenant_id == '',
                CloudAuthConnection.owner_user_id == (owner_user_id or '').strip(),
                CloudAuthConnection.provider == (provider or '').strip().lower(),
                CloudAuthConnection.auth_mode == (auth_mode or '').strip().lower(),
                CloudAuthConnection.client_id.is_(None),
            )
            .order_by(CloudAuthConnection.created_at.desc())
            .all()
        )

    @classmethod
    def save(cls, session: Session, row: CloudAuthConnection) -> CloudAuthConnection:
        session.add(row)
        session.commit()
        session.refresh(row)
        return row

    @classmethod
    def delete(cls, session: Session, row: CloudAuthConnection) -> None:
        session.delete(row)
        session.commit()

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
            statuses = tuple(
                value.strip().upper()
                for value in status.split(',')
                if value and value.strip()
            )
            if statuses:
                q = q.filter(CloudAuthConnection.status.in_(statuses))
        return q.order_by(CloudAuthConnection.updated_at.desc(), CloudAuthConnection.created_at.desc()).all()

    @classmethod
    def list_health_check_candidates(
        cls,
        session: Session,
        *,
        provider: str | None = None,
        auth_mode: str = 'oauth_user',
        statuses: tuple[str, ...] = ('ACTIVE', 'EXPIRED', 'ERROR'),
        limit: int = 100,
    ) -> list[CloudAuthConnection]:
        normalized_statuses = tuple(
            status.strip().upper()
            for status in statuses
            if status and status.strip()
        )
        if not normalized_statuses:
            return []
        q = session.query(CloudAuthConnection).filter(
            CloudAuthConnection.tenant_id == '',
            CloudAuthConnection.auth_mode == (auth_mode or '').strip().lower(),
            CloudAuthConnection.status.in_(normalized_statuses),
        )
        if provider:
            q = q.filter(CloudAuthConnection.provider == provider.strip().lower())
        return (
            q.order_by(CloudAuthConnection.updated_at.asc(), CloudAuthConnection.created_at.asc())
            .limit(max(1, int(limit or 100)))
            .all()
        )

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
        provider_tenant_key: str | None = None,
        status: str | None = None,
        exclude_statuses: tuple[str, ...] | None = None,
        exclude_connection_id: str | None = None,
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
        tenant_key = (provider_tenant_key or '').strip()
        if tenant_key:
            q = q.filter(CloudAuthConnection.provider_tenant_key == tenant_key)
        excluded_connection_id = (exclude_connection_id or '').strip()
        if excluded_connection_id:
            q = q.filter(CloudAuthConnection.connection_id != excluded_connection_id)
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

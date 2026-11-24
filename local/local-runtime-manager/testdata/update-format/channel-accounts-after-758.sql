CREATE TABLE channel_accounts (
                id TEXT PRIMARY KEY,
                owner_user_id TEXT NOT NULL,
                provider VARCHAR(32) NOT NULL,
                external_id_hash VARCHAR(64) NOT NULL,
                label TEXT NOT NULL,
                status VARCHAR(32) NOT NULL,
                runtime_status VARCHAR(32) NOT NULL DEFAULT 'stopped',
                last_poll_at TIMESTAMPTZ,
                last_message_at TIMESTAMPTZ,
                last_error TEXT,
                credentials_ciphertext TEXT NOT NULL,
                credential_revision INTEGER NOT NULL DEFAULT 1,
                welcome_pending BOOLEAN NOT NULL DEFAULT FALSE,
                connected_at TIMESTAMPTZ,
                created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
                updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP, identity_metadata TEXT NOT NULL DEFAULT '{}', archived_at TIMESTAMPTZ, default_recipient_id TEXT NOT NULL DEFAULT '', label_custom BOOLEAN NOT NULL DEFAULT FALSE, connection_check_status TEXT NOT NULL DEFAULT 'unknown', connection_checked_at TIMESTAMPTZ, connection_check_started_at TIMESTAMPTZ, connection_check_id TEXT NOT NULL DEFAULT '', connection_error_code TEXT NOT NULL DEFAULT '', connection_context_revision INTEGER NOT NULL DEFAULT 0,
                UNIQUE (owner_user_id, provider, external_id_hash)
            )

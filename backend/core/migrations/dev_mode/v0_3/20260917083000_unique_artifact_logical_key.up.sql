-- Conversation-scoped dual-write keys reuse tenant+owner+logical_key as identity.
-- +migrate Dialect postgres
CREATE UNIQUE INDEX IF NOT EXISTS uk_artifacts_owner_logical_key
ON artifacts (tenant_id, owner_user_id, logical_key)
WHERE deleted_at IS NULL AND logical_key IS NOT NULL AND logical_key <> '';

-- +migrate Dialect sqlite
CREATE UNIQUE INDEX IF NOT EXISTS uk_artifacts_owner_logical_key
ON artifacts (tenant_id, owner_user_id, logical_key)
WHERE deleted_at IS NULL AND logical_key IS NOT NULL AND logical_key <> '';

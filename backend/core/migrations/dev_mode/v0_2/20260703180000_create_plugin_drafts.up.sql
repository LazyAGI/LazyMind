CREATE TABLE plugin_drafts (
    id          VARCHAR(36)     NOT NULL PRIMARY KEY,
    name        VARCHAR(255)    NOT NULL DEFAULT '',
    content     TEXT            NOT NULL DEFAULT '',
    created_by  VARCHAR(255)    NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ     NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ     NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_plugin_drafts_created_by ON plugin_drafts (created_by);

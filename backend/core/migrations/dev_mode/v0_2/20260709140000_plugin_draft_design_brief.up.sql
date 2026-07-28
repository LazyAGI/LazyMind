-- +migrate Dialect postgres
-- Add design_brief_content column and brief_done generate_status to plugin_drafts.
-- design_brief_content stores the Phase 0 design brief (Markdown) produced before
-- Phase 1 skeleton generation.  NULL / empty means Phase 0 was skipped (old drafts).
ALTER TABLE plugin_drafts ADD COLUMN IF NOT EXISTS design_brief_content TEXT NOT NULL DEFAULT '';

-- +migrate Dialect sqlite
SELECT 1; -- Historical SQLite change is included by the first v0.2 dev migration.

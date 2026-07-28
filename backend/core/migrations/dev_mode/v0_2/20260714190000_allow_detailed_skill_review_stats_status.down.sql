-- 20260714190000_allow_detailed_skill_review_stats_status
-- +migrate Down
-- +migrate Dialect postgres
-- The status column remains TEXT NOT NULL. The removed enum constraint is not
-- restored because rows may already contain detailed non-terminal statuses.

SELECT 1;

-- +migrate Dialect sqlite
SELECT 1; -- Historical SQLite change is included by the first v0.2 dev migration.

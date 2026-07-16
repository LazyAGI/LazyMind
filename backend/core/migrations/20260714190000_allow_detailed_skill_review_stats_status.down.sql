-- 20260714190000_allow_detailed_skill_review_stats_status
-- +migrate Down
-- The status column remains TEXT NOT NULL. The removed enum constraint is not
-- restored because rows may already contain detailed non-terminal statuses.

SELECT 1;

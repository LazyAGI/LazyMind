-- +migrate Dialect postgres,sqlite
-- The prior migration already has the complete archive, so no schema changes
-- are needed before it backs up the graph for its own rollback.
SELECT 1;

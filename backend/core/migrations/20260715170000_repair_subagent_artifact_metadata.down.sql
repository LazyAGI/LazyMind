-- Intentionally keep the repaired columns on rollback: older migrations and
-- the current ORM may already own and depend on them.
SELECT 1;

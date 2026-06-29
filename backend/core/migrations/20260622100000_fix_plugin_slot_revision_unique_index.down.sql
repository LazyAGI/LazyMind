-- Rollback: restore the original (session_id, slot_id, revision) unique index.

DROP INDEX IF EXISTS idx_psr_slot_rev;

CREATE UNIQUE INDEX idx_psr_slot_rev
    ON plugin_slot_revisions(session_id, slot_id, revision);

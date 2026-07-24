-- Fix plugin_slot_revisions unique index to scope revision per (session, slot, list_index).
-- Previously the index was (session_id, slot_id, revision), which caused conflicts when
-- multiple list items each start their own version counter from 1.
-- The correct constraint is: revision is unique within one (session, slot, list_index) scope.
-- For single-cardinality slots list_index IS NULL; COALESCE maps NULL to -1 for index purposes.

DROP INDEX IF EXISTS idx_psr_slot_rev;

CREATE UNIQUE INDEX idx_psr_slot_rev
    ON plugin_slot_revisions(session_id, slot_id, COALESCE(list_index, -1), revision);

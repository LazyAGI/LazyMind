-- +migrate Dialect postgres,sqlite
-- The immutable archive remains available after reverting normalized storage.
DROP TABLE paper_library_legacy_states;

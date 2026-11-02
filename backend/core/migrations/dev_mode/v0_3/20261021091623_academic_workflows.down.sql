-- +migrate Dialect *
DROP TABLE IF EXISTS academic_paper_contents;
DROP TABLE IF EXISTS academic_handoffs;
DROP TABLE IF EXISTS academic_expansion_rounds;
DROP TABLE IF EXISTS academic_expansions;
DROP TABLE IF EXISTS academic_user_paper_states;
DROP TABLE IF EXISTS academic_document_states;
ALTER TABLE datasets DROP COLUMN scenario_key;

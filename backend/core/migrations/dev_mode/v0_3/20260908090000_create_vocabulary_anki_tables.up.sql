-- +migrate Dialect postgres
CREATE TABLE IF NOT EXISTS vocabulary_provider_settings (
    owner_id VARCHAR(64) PRIMARY KEY,
    selected_provider VARCHAR(16) NOT NULL DEFAULT 'anki',
    anki_endpoint TEXT NOT NULL DEFAULT 'http://127.0.0.1:8765',
    anki_deck_name TEXT NOT NULL DEFAULT 'LazyMind Vocabulary',
    anki_model_version INTEGER NOT NULL DEFAULT 1,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE IF NOT EXISTS vocabulary_words (
    id VARCHAR(36) PRIMARY KEY,
    owner_id VARCHAR(64) NOT NULL,
    provider VARCHAR(16) NOT NULL,
    provider_note_id VARCHAR(64) NOT NULL DEFAULT '',
    normalized_term TEXT NOT NULL,
    term TEXT NOT NULL,
    language VARCHAR(16) NOT NULL DEFAULT 'en',
    phonetic TEXT NOT NULL DEFAULT '',
    part_of_speech TEXT NOT NULL DEFAULT '',
    meaning TEXT NOT NULL DEFAULT '',
    definition TEXT NOT NULL DEFAULT '',
    user_note TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX IF NOT EXISTS uk_vocabulary_word_owner_provider_term ON vocabulary_words(owner_id, provider, language, normalized_term);
CREATE TABLE IF NOT EXISTS vocabulary_examples (
    id VARCHAR(36) PRIMARY KEY,
    owner_id VARCHAR(64) NOT NULL,
    word_id VARCHAR(36) NOT NULL,
    provider_note_id VARCHAR(64) NOT NULL DEFAULT '',
    sentence TEXT NOT NULL,
    translation TEXT NOT NULL DEFAULT '',
    content_origin VARCHAR(32) NOT NULL DEFAULT 'user',
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_vocabulary_examples_word ON vocabulary_examples(owner_id, word_id);
CREATE TABLE IF NOT EXISTS vocabulary_source_refs (
    id VARCHAR(36) PRIMARY KEY,
    owner_id VARCHAR(64) NOT NULL,
    word_id VARCHAR(36) NOT NULL,
    example_id VARCHAR(36),
    dataset_id VARCHAR(64) NOT NULL,
    document_id VARCHAR(64) NOT NULL,
    segment_id VARCHAR(64) NOT NULL DEFAULT '',
    page INTEGER,
    bbox_json TEXT NOT NULL DEFAULT '',
    selected_text TEXT NOT NULL DEFAULT '',
    context_sentence TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX IF NOT EXISTS uk_vocabulary_source_ref ON vocabulary_source_refs(owner_id, word_id, document_id, segment_id, context_sentence);
CREATE INDEX IF NOT EXISTS idx_vocabulary_source_document ON vocabulary_source_refs(owner_id, document_id);
CREATE TABLE IF NOT EXISTS vocabulary_provider_operations (
    id VARCHAR(36) PRIMARY KEY,
    owner_id VARCHAR(64) NOT NULL,
    provider VARCHAR(16) NOT NULL,
    operation_type VARCHAR(32) NOT NULL,
    payload_json TEXT NOT NULL,
    idempotency_key VARCHAR(128) NOT NULL,
    status VARCHAR(24) NOT NULL DEFAULT 'pending',
    retry_count INTEGER NOT NULL DEFAULT 0,
    last_error TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX IF NOT EXISTS uk_vocabulary_operation_idempotency ON vocabulary_provider_operations(owner_id, idempotency_key);
CREATE INDEX IF NOT EXISTS idx_vocabulary_operations_pending ON vocabulary_provider_operations(owner_id, provider, status, created_at);

-- +migrate Dialect sqlite
CREATE TABLE IF NOT EXISTS vocabulary_provider_settings (
    owner_id TEXT PRIMARY KEY,
    selected_provider TEXT NOT NULL DEFAULT 'anki',
    anki_endpoint TEXT NOT NULL DEFAULT 'http://127.0.0.1:8765',
    anki_deck_name TEXT NOT NULL DEFAULT 'LazyMind Vocabulary',
    anki_model_version INTEGER NOT NULL DEFAULT 1,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE IF NOT EXISTS vocabulary_words (
    id TEXT PRIMARY KEY,
    owner_id TEXT NOT NULL,
    provider TEXT NOT NULL,
    provider_note_id TEXT NOT NULL DEFAULT '',
    normalized_term TEXT NOT NULL,
    term TEXT NOT NULL,
    language TEXT NOT NULL DEFAULT 'en',
    phonetic TEXT NOT NULL DEFAULT '',
    part_of_speech TEXT NOT NULL DEFAULT '',
    meaning TEXT NOT NULL DEFAULT '',
    definition TEXT NOT NULL DEFAULT '',
    user_note TEXT NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX IF NOT EXISTS uk_vocabulary_word_owner_provider_term ON vocabulary_words(owner_id, provider, language, normalized_term);
CREATE TABLE IF NOT EXISTS vocabulary_examples (
    id TEXT PRIMARY KEY,
    owner_id TEXT NOT NULL,
    word_id TEXT NOT NULL,
    provider_note_id TEXT NOT NULL DEFAULT '',
    sentence TEXT NOT NULL,
    translation TEXT NOT NULL DEFAULT '',
    content_origin TEXT NOT NULL DEFAULT 'user',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_vocabulary_examples_word ON vocabulary_examples(owner_id, word_id);
CREATE TABLE IF NOT EXISTS vocabulary_source_refs (
    id TEXT PRIMARY KEY,
    owner_id TEXT NOT NULL,
    word_id TEXT NOT NULL,
    example_id TEXT,
    dataset_id TEXT NOT NULL,
    document_id TEXT NOT NULL,
    segment_id TEXT NOT NULL DEFAULT '',
    page INTEGER,
    bbox_json TEXT NOT NULL DEFAULT '',
    selected_text TEXT NOT NULL DEFAULT '',
    context_sentence TEXT NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX IF NOT EXISTS uk_vocabulary_source_ref ON vocabulary_source_refs(owner_id, word_id, document_id, segment_id, context_sentence);
CREATE INDEX IF NOT EXISTS idx_vocabulary_source_document ON vocabulary_source_refs(owner_id, document_id);
CREATE TABLE IF NOT EXISTS vocabulary_provider_operations (
    id TEXT PRIMARY KEY,
    owner_id TEXT NOT NULL,
    provider TEXT NOT NULL,
    operation_type TEXT NOT NULL,
    payload_json TEXT NOT NULL,
    idempotency_key TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending',
    retry_count INTEGER NOT NULL DEFAULT 0,
    last_error TEXT NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX IF NOT EXISTS uk_vocabulary_operation_idempotency ON vocabulary_provider_operations(owner_id, idempotency_key);
CREATE INDEX IF NOT EXISTS idx_vocabulary_operations_pending ON vocabulary_provider_operations(owner_id, provider, status, created_at);

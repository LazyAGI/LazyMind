-- Bounded reading status/enrollment queries; resource_id stays the user concurrency key.
CREATE INDEX idx_reading_job_source ON async_jobs
    (create_user_id, (payload_json ->> 'dataset_id'), (payload_json ->> 'document_id'), (payload_json ->> 'source_sha256'), created_at DESC, id DESC)
    WHERE job_type = 'document_reading_preparation';
CREATE INDEX idx_reading_job_enrollment ON async_jobs
    (resource_type, create_user_id, (payload_json ->> 'dataset_id'), (payload_json ->> 'document_id'))
    WHERE job_type = 'document_reading_preparation';
CREATE INDEX idx_reading_job_daily ON async_jobs (job_type, resource_type, create_user_id, created_at);
CREATE INDEX idx_reading_document_candidates ON documents (create_user_id, created_at, id) WHERE deleted_at IS NULL;

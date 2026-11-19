CREATE TABLE reading_usage (
 user_id VARCHAR(255) NOT NULL,
 category VARCHAR(32) NOT NULL,
 source_sha256 VARCHAR(64) NOT NULL,
 charged_at TIMESTAMP NOT NULL,
 PRIMARY KEY (user_id, category, source_sha256)
);
CREATE INDEX idx_automatic_reading_usage_day ON reading_usage (user_id, category, charged_at);

-- Keep prior automatic work idempotent after upgrade. Read-only cache hits
-- without a parser attempt or model request do not create study reservations.
INSERT INTO reading_usage (user_id, category, source_sha256, charged_at)
SELECT create_user_id, 'ai_study', payload_json ->> 'source_sha256', MIN(started_at)
FROM async_jobs
WHERE job_type = 'document_reading_preparation' AND resource_type = 'reading_automatic'
  AND started_at IS NOT NULL AND COALESCE(payload_json ->> 'source_sha256', '') <> ''
  AND CAST(COALESCE(result_json ->> 'model_requests', '0') AS INTEGER) > 0
GROUP BY create_user_id, payload_json ->> 'source_sha256';

INSERT INTO reading_usage (user_id, category, source_sha256, charged_at)
SELECT create_user_id, 'pdf_parse', payload_json ->> 'source_sha256', MIN(started_at)
FROM async_jobs
WHERE job_type = 'document_pdf_ir' AND started_at IS NOT NULL
  AND CAST(payload_json ->> 'automatic' AS TEXT) IN ('true', '1')
  AND COALESCE(payload_json ->> 'source_sha256', '') <> ''
GROUP BY create_user_id, payload_json ->> 'source_sha256';

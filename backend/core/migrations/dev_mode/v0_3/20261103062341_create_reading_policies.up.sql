CREATE TABLE user_reading_policies (
 user_id VARCHAR(255) PRIMARY KEY,
 policy_json TEXT NOT NULL,
 updated_at TIMESTAMP NOT NULL
);

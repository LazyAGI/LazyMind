ALTER TABLE default_models
    ALTER COLUMN max_input_tokens TYPE BIGINT
    USING CASE
        WHEN max_input_tokens IS NULL OR max_input_tokens = '' THEN NULL
        WHEN max_input_tokens ~ '^[0-9]+K$' THEN SUBSTRING(max_input_tokens FROM '^[0-9]+')::BIGINT * 1024
        WHEN max_input_tokens ~ '^[0-9]+M$' THEN SUBSTRING(max_input_tokens FROM '^[0-9]+')::BIGINT * 1000000
        ELSE max_input_tokens::BIGINT
    END;

ALTER TABLE user_model_provider_group_models
    ALTER COLUMN max_input_tokens TYPE BIGINT
    USING CASE
        WHEN max_input_tokens IS NULL OR max_input_tokens = '' THEN NULL
        WHEN max_input_tokens ~ '^[0-9]+K$' THEN SUBSTRING(max_input_tokens FROM '^[0-9]+')::BIGINT * 1024
        WHEN max_input_tokens ~ '^[0-9]+M$' THEN SUBSTRING(max_input_tokens FROM '^[0-9]+')::BIGINT * 1000000
        ELSE max_input_tokens::BIGINT
    END;

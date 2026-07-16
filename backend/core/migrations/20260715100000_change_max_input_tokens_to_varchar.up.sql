ALTER TABLE default_models
    ALTER COLUMN max_input_tokens TYPE VARCHAR(16)
    USING max_input_tokens::VARCHAR(16);

ALTER TABLE user_model_provider_group_models
    ALTER COLUMN max_input_tokens TYPE VARCHAR(16)
    USING max_input_tokens::VARCHAR(16);

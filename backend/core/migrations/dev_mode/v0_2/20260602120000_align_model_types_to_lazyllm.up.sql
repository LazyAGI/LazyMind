-- +migrate Up
-- +migrate Dialect postgres

-- Align model_type values in default_models and user_model_provider_group_models
-- to match lazyllm type conventions (embed_main→embed, embed_image→cross_modal_embed, reranker→rerank).
-- These tables store the lazyllm technical type, not the runtime_models.yaml role key.

UPDATE default_models SET model_type = 'embed'             WHERE model_type = 'embed_main';
UPDATE default_models SET model_type = 'cross_modal_embed' WHERE model_type = 'embed_image';
UPDATE default_models SET model_type = 'rerank'            WHERE model_type = 'reranker';

UPDATE user_model_provider_group_models SET model_type = 'embed'             WHERE model_type = 'embed_main';
UPDATE user_model_provider_group_models SET model_type = 'cross_modal_embed' WHERE model_type = 'embed_image';
UPDATE user_model_provider_group_models SET model_type = 'rerank'            WHERE model_type = 'reranker';

-- +migrate Dialect sqlite
SELECT 1; -- Historical SQLite change is included by the first v0.2 dev migration.

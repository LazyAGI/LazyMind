-- +migrate Up

-- Align legacy model_type values with runtime_models.yaml role keys.

UPDATE default_models SET model_type = 'vlm' WHERE model_type = 'VLM';
UPDATE default_models SET model_type = 'embed_main' WHERE model_type = 'embedding';
UPDATE default_models SET model_type = 'embed_image' WHERE model_type = 'multimodal_embedding';
UPDATE default_models SET model_type = 'reranker' WHERE model_type = 'rerank';

UPDATE user_model_provider_group_models SET model_type = 'vlm' WHERE model_type = 'VLM';
UPDATE user_model_provider_group_models SET model_type = 'embed_main' WHERE model_type = 'embedding';
UPDATE user_model_provider_group_models SET model_type = 'embed_image' WHERE model_type = 'multimodal_embedding';
UPDATE user_model_provider_group_models SET model_type = 'reranker' WHERE model_type = 'rerank';

UPDATE user_selected_models SET model_type = 'llm' WHERE model_type = 'llm-chat';
UPDATE user_selected_models SET model_type = 'evo_llm' WHERE model_type IN ('llm-evo', 'llm2');
UPDATE user_selected_models SET model_type = 'vlm' WHERE model_type = 'VLM';
UPDATE user_selected_models SET model_type = 'embed_main' WHERE model_type = 'embedding';
UPDATE user_selected_models SET model_type = 'embed_image' WHERE model_type = 'multimodal_embedding';
UPDATE user_selected_models SET model_type = 'reranker' WHERE model_type = 'rerank';

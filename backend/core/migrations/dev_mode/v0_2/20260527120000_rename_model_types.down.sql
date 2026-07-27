-- +migrate Down

UPDATE default_models SET model_type = 'VLM' WHERE model_type = 'vlm';
UPDATE default_models SET model_type = 'embedding' WHERE model_type = 'embed_main';
UPDATE default_models SET model_type = 'multimodal_embedding' WHERE model_type = 'embed_image';
UPDATE default_models SET model_type = 'rerank' WHERE model_type = 'reranker';

UPDATE user_model_provider_group_models SET model_type = 'VLM' WHERE model_type = 'vlm';
UPDATE user_model_provider_group_models SET model_type = 'embedding' WHERE model_type = 'embed_main';
UPDATE user_model_provider_group_models SET model_type = 'multimodal_embedding' WHERE model_type = 'embed_image';
UPDATE user_model_provider_group_models SET model_type = 'rerank' WHERE model_type = 'reranker';

UPDATE user_selected_models SET model_type = 'llm-chat' WHERE model_type = 'llm';
UPDATE user_selected_models SET model_type = 'llm-evo' WHERE model_type = 'evo_llm';
UPDATE user_selected_models SET model_type = 'VLM' WHERE model_type = 'vlm';
UPDATE user_selected_models SET model_type = 'embedding' WHERE model_type = 'embed_main';
UPDATE user_selected_models SET model_type = 'multimodal_embedding' WHERE model_type = 'embed_image';
UPDATE user_selected_models SET model_type = 'rerank' WHERE model_type = 'reranker';

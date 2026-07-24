-- +migrate Down

UPDATE default_models SET model_type = 'embed_main' WHERE model_type = 'embed';
UPDATE default_models SET model_type = 'embed_image' WHERE model_type = 'cross_modal_embed';
UPDATE default_models SET model_type = 'reranker'    WHERE model_type = 'rerank';

UPDATE user_model_provider_group_models SET model_type = 'embed_main' WHERE model_type = 'embed';
UPDATE user_model_provider_group_models SET model_type = 'embed_image' WHERE model_type = 'cross_modal_embed';
UPDATE user_model_provider_group_models SET model_type = 'reranker'    WHERE model_type = 'rerank';

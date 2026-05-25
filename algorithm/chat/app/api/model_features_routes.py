from fastapi import APIRouter

from chat.utils.load_config import get_image_embed_key

router = APIRouter()


@router.get('/api/model/features', summary='Get model feature flags derived from runtime config')
async def get_model_features():
    '''Return feature flags based on the active runtime_models config.

    image_embed_enabled is True when a cross_modal_embed role is present in the
    config (i.e. get_image_embed_key() returns a non-None value).  Clients should
    use this flag to decide whether to show / validate the multimodal_embedding
    model slot.
    '''
    return {
        'image_embed_enabled': get_image_embed_key() is not None,
    }

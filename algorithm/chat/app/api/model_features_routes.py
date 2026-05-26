import requests
from fastapi import APIRouter

from chat.utils.load_config import get_dynamic_role_slot_map, get_image_embed_key
from config import config as _cfg
from lazyllm.tools.rag.store import LAZY_IMAGE_GROUP

router = APIRouter()


def _is_image_group_lazy() -> bool:
    '''Return True when the image node group still has lazy_mode='embed' (admin has not configured yet).'''
    processor_url = (_cfg['document_processor_url'] or '').rstrip('/')
    if not processor_url:
        return True
    try:
        resp = requests.get(f'{processor_url}/ng/{LAZY_IMAGE_GROUP}/lazy_mode', timeout=5)
        if resp.status_code == 200:
            data = resp.json().get('data') or {}
            return data.get('lazy_mode') == 'embed'
    except Exception:
        pass
    return True


@router.get('/api/model/features', summary='Get model feature flags derived from runtime config')
def get_model_features():
    '''Return feature flags based on the active runtime_models config.

    image_embed_enabled is True when a cross_modal_embed role is present in the
    config (i.e. get_image_embed_key() returns a non-None value).

    image_embed_required is True when embed_image is source=dynamic AND the admin
    has already configured it at least once (lazy_mode is no longer 'embed').
    '''
    image_embed_key = get_image_embed_key()
    image_embed_required = False
    if image_embed_key and image_embed_key in get_dynamic_role_slot_map():
        image_embed_required = not _is_image_group_lazy()
    return {
        'image_embed_enabled': image_embed_key is not None,
        'image_embed_required': image_embed_required,
    }

import requests
from fastapi import APIRouter, HTTPException

from lazymind.model_config import get_dynamic_role_slot_map, load_model_config
from lazymind.config import EMBED_IMAGE, config as _cfg
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

    image_embed_enabled is True when the fixed embed_image role is present in
    the config.

    image_embed_required is True when embed_image is source=dynamic AND the admin
    has already configured it at least once (lazy_mode is no longer 'embed').
    '''
    raw = load_model_config()
    image_embed_enabled = EMBED_IMAGE in raw
    image_embed_required = False
    if image_embed_enabled and EMBED_IMAGE in get_dynamic_role_slot_map():
        image_embed_required = not _is_image_group_lazy()
    return {
        'image_embed_enabled': image_embed_enabled,
        'image_embed_required': image_embed_required,
    }


@router.get('/api/model/role_type', summary='Get the lazyllm type and source for a runtime_models.yaml role key')
def get_role_type(role: str):
    '''Return the lazyllm technical type and source for the given runtime_models.yaml role key.

    Example: role=evo_llm   → {"role": "evo_llm",   "type": "llm",   "source": "dynamic", "is_dynamic": true}
             role=embed_main → {"role": "embed_main", "type": "embed", "source": "dynamic", "is_dynamic": true}

    source is the raw value from the yaml entry (e.g. "dynamic", "openai", a URL, etc.).
    is_dynamic is true when source == "dynamic".

    Returns 404 when the role is not defined in the active config file.
    '''
    raw = load_model_config()
    cfg = raw.get(role)
    if cfg is None:
        raise HTTPException(status_code=404, detail=f'role {role!r} not found in runtime config')
    if isinstance(cfg, list):
        cfg = cfg[0] if cfg else {}
    if not isinstance(cfg, dict):
        cfg = {}
    source = str(cfg.get('source') or '').strip()
    return {
        'role': role,
        'type': str(cfg.get('type') or 'llm').lower(),
        'source': source,
        'is_dynamic': source.lower() == 'dynamic',
    }

"""Route images using declared capabilities only; never probe model endpoints."""
from __future__ import annotations

import lazyllm
from lazymind.model_config import _role_entry, is_model_role_available, load_model_config


class VisionModelUnavailable(RuntimeError):
    pass


def _llm_configuration() -> dict:
    entry = _role_entry(load_model_config().get('llm')) or {}
    if str(entry.get('source', '')).lower() != 'dynamic':
        return entry
    config = lazyllm.globals['config']
    selected = ((config.get('dynamic_model_configs') or {}).get('llm') or {}).get('chat') or {}
    capabilities = (lazyllm.globals.get('lazymind_model_capabilities') or {}).get('llm') or {}
    return {**selected, **capabilities}


def main_model_supports_vision() -> bool:
    """Use the current request's declared capability without probing a model."""
    if not is_model_role_available('llm'):
        return False
    config = _llm_configuration()
    return config.get('vision') is True or str(config.get('type', '')).lower() == 'vlm'


def select_vision_model_role() -> str:
    if is_model_role_available('vlm'):
        return 'vlm'
    if not is_model_role_available('llm'):
        raise VisionModelUnavailable('未配置视觉模型或主模型，请在模型设置中配置后重试。')
    if main_model_supports_vision():
        return 'llm'
    raise VisionModelUnavailable(
        '主模型未声明支持多模态，请在添加 LLM 时勾选“是否支持多模态”，或配置图文模型后重试。',
    )

from __future__ import annotations

import re
from collections.abc import Mapping
from typing import Any


MODEL_NOT_CONFIGURED = 2001300
MODEL_NOT_ALLOWED = 2001301


def _models(*names: str) -> dict[str, str]:
    return {name.casefold(): name for name in names}


EVO_MODEL_ALLOWLIST = {
    'claude': _models(
        'claude-haiku-4-5',
        'claude-opus-4-7',
        'claude-sonnet-4-6',
    ),
    'deepseek': _models(
        'deepseek-v4-flash',
        'deepseek-v4-pro',
    ),
    'glm': _models(
        'GLM-4.5-Air',
        'GLM-4.5-Flash',
        'GLM-4.6',
        'GLM-4.7',
        'GLM-4.7-Flash',
        'GLM-4.7-FlashX',
        'GLM-5',
        'GLM-5.1',
        'GLM-5V-Turbo',
    ),
    'kimi': _models(
        'kimi-k2-0711-preview',
        'kimi-k2-0905-preview',
        'kimi-k2-thinking',
        'kimi-k2-thinking-turbo',
        'kimi-k2-turbo-preview',
        'kimi-k2.5',
        'kimi-k2.6',
    ),
    'minimax': _models(
        'MiniMax-M2.5',
        'MiniMax-M2.5-highspeed',
        'MiniMax-M2.7',
        'MiniMax-M2.7-highspeed',
    ),
    'openai': _models(
        'gpt-4.1',
        'gpt-4.1-mini',
        'gpt-4o-mini',
        'gpt-5',
        'gpt-5-mini',
        'gpt-5-pro',
        'gpt-5.1',
        'gpt-5.2',
        'gpt-5.2-pro',
        'gpt-5.4',
        'gpt-5.4-mini',
        'gpt-5.4-pro',
        'o3',
    ),
    'qwen': _models(
        'qwen-plus',
        'qwen-max',
        'qwen3-max',
        'qwen3.5-flash',
        'qwen3.5-plus',
        'qwen3.6-flash',
        'qwen3.6-plus',
        'qwen3.7-max',
        'qwen3.7-plus',
        'qwen3-coder-flash',
        'qwen3-coder-plus',
        'qwen3-coder-30b-a3b-instruct',
        'qwen3-coder-480b-a35b-instruct',
    ),
    'siliconflow': _models(
        'deepseek-ai/DeepSeek-V4-Flash',
        'MiniMaxAI/MiniMax-M2.5',
        'Pro/MiniMaxAI/MiniMax-M2.5',
        'Pro/deepseek-ai/DeepSeek-V3.2',
        'Pro/moonshotai/Kimi-K2.5',
        'Pro/moonshotai/Kimi-K2.6',
        'Pro/zai-org/GLM-5',
        'Pro/zai-org/GLM-5.1',
        'Qwen/Qwen3-Coder-30B-A3B-Instruct',
        'Qwen/Qwen3-Coder-480B-A35B-Instruct',
    ),
}


PROVIDER_ALIASES = {
    'alibaba': 'qwen',
    'alibabacn': 'qwen',
    'anthropic': 'claude',
    'claude': 'claude',
    'dashscope': 'qwen',
    'deepseek': 'deepseek',
    'glm': 'glm',
    'kimi': 'kimi',
    'minimax': 'minimax',
    'moonshot': 'kimi',
    'moonshotai': 'kimi',
    'openai': 'openai',
    'qwen': 'qwen',
    'siliconflow': 'siliconflow',
    'zhipu': 'glm',
    'zhipuai': 'glm',
}


# OpenCode provider IDs are the canonical Models.dev IDs. Known product
# endpoints are rewritten to those providers' supported API surfaces; custom
# endpoints are preserved verbatim.
OPENCODE_PROVIDERS = {
    'claude': ('anthropic', '@ai-sdk/anthropic', {}),
    'deepseek': ('deepseek', '@ai-sdk/openai-compatible', {
        'https://api.deepseek.com/v1': 'https://api.deepseek.com',
    }),
    'glm': ('zhipuai', '@ai-sdk/openai-compatible', {}),
    'kimi': ('moonshotai-cn', '@ai-sdk/openai-compatible', {
        'https://api.moonshot.cn': 'https://api.moonshot.cn/v1',
    }),
    'minimax': ('minimax-cn', '@ai-sdk/anthropic', {
        'https://api.minimaxi.com/v1': 'https://api.minimaxi.com/anthropic/v1',
    }),
    'openai': ('openai', '@ai-sdk/openai', {
        'https://api.openai.com': 'https://api.openai.com/v1',
    }),
    'qwen': ('alibaba-cn', '@ai-sdk/openai-compatible', {
        'https://dashscope.aliyuncs.com': 'https://dashscope.aliyuncs.com/compatible-mode/v1',
    }),
    'siliconflow': ('siliconflow-cn', '@ai-sdk/openai-compatible', {}),
}


class EvoModelConfigError(ValueError):
    def __init__(
        self,
        code: int,
        reason: str,
        provider: str = '',
        model: str = '',
        missing_fields: tuple[str, ...] = (),
    ) -> None:
        super().__init__(code, reason, provider, model, missing_fields)
        self.code = code
        self.reason = reason
        self.provider = provider
        self.model = model
        self.missing_fields = missing_fields

    def __str__(self) -> str:
        return self.reason

    def detail(self) -> dict[str, Any]:
        data: dict[str, Any] = {
            'reason': self.reason,
            'model_role': 'evo_llm',
        }
        if self.provider:
            data['provider'] = self.provider
        if self.model:
            data['model'] = self.model
        if self.missing_fields:
            data['missing_fields'] = list(self.missing_fields)
        return {
            'code': self.code,
            'message': (
                '请先完成 evo_llm 模型配置'
                if self.code == MODEL_NOT_CONFIGURED
                else '当前配置的自进化模型不支持自进化'
            ),
            'data': data,
        }


def resolve_evo_model(role: object) -> tuple[str, str]:
    if not isinstance(role, Mapping):
        raise EvoModelConfigError(MODEL_NOT_CONFIGURED, 'model_not_configured')

    raw_provider = _text(role.get('provider')) or _text(role.get('source'))
    raw_model = _text(role.get('model'))
    base_url = _text(role.get('base_url'))
    api_key = _text(role.get('api_key'))
    missing = tuple(
        field
        for field, value in (
            ('source', raw_provider),
            ('model', raw_model),
            ('base_url', base_url),
            ('api_key', api_key or ('skip_auth' if role.get('skip_auth') is True else '')),
        )
        if not value
    )
    if missing:
        raise EvoModelConfigError(
            MODEL_NOT_CONFIGURED,
            'model_config_incomplete',
            provider=raw_provider,
            model=raw_model,
            missing_fields=missing,
        )

    provider = PROVIDER_ALIASES.get(_provider_key(raw_provider), '')
    model = EVO_MODEL_ALLOWLIST.get(provider, {}).get(raw_model.casefold(), '')
    if not provider or not model:
        raise EvoModelConfigError(
            MODEL_NOT_ALLOWED,
            'evo_llm_not_allowed',
            provider=raw_provider,
            model=raw_model,
        )
    return provider, model


def opencode_settings(role: object) -> dict[str, str]:
    provider, model = resolve_evo_model(role)
    opencode_provider, npm, rewrites = OPENCODE_PROVIDERS[provider]
    raw = role if isinstance(role, Mapping) else {}
    base_url = _text(raw.get('base_url')).rstrip('/')
    base_url = rewrites.get(base_url, base_url)
    return {
        'model': f'{opencode_provider}/{model}',
        'provider': opencode_provider,
        'provider_model': model,
        'npm': npm,
        'base_url': base_url,
        'api_key': _text(raw.get('api_key')),
        'skip_auth': 'true' if raw.get('skip_auth') is True else '',
    }


def _provider_key(value: str) -> str:
    return re.sub(r'[\s_.-]+', '', value.casefold())


def _text(value: object) -> str:
    return str(value or '').strip()


assert set(EVO_MODEL_ALLOWLIST) == set(OPENCODE_PROVIDERS)

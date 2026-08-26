from __future__ import annotations

from collections.abc import Mapping
from typing import Any


MODEL_NOT_CONFIGURED = 2001300
MODEL_NOT_ALLOWED = 2001301


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


def resolve_evo_model(role: object) -> Mapping[str, Any]:
    config = _mapping(role)
    source = _text(config.get('source'))
    model = _text(config.get('model'))
    base_url = _text(config.get('base_url'))
    api_key = _text(config.get('api_key'))
    missing = tuple(
        field
        for field, value in (
            ('source', source),
            ('model', model),
            ('base_url', base_url),
            ('api_key', api_key or ('skip_auth' if config.get('skip_auth') is True else '')),
        )
        if not value
    )
    if missing:
        raise EvoModelConfigError(
            MODEL_NOT_CONFIGURED,
            'model_config_incomplete',
            provider=source,
            model=model,
            missing_fields=missing,
        )

    raw_descriptor = config.get('opencode')
    descriptor = raw_descriptor if isinstance(raw_descriptor, Mapping) else {}
    required = ('provider', 'provider_model', 'model', 'npm', 'base_url')
    descriptor_missing = tuple(name for name in required if not _text(descriptor.get(name)))
    if descriptor_missing:
        raise EvoModelConfigError(
            MODEL_NOT_ALLOWED,
            'evo_llm_not_eligible',
            provider=source,
            model=model,
            missing_fields=tuple(f'opencode.{name}' for name in descriptor_missing),
        )

    provider = _text(descriptor['provider'])
    provider_model = _text(descriptor['provider_model'])
    if provider_model != model or _text(descriptor['model']) != f'{provider}/{provider_model}':
        raise EvoModelConfigError(
            MODEL_NOT_ALLOWED,
            'invalid_opencode_descriptor',
            provider=source,
            model=model,
        )
    return descriptor


def opencode_settings(role: object) -> dict[str, str]:
    config = _mapping(role)
    descriptor = resolve_evo_model(config)
    return {
        'model': _text(descriptor['model']),
        'provider': _text(descriptor['provider']),
        'provider_model': _text(descriptor['provider_model']),
        'npm': _text(descriptor['npm']),
        'base_url': _text(descriptor['base_url']).rstrip('/'),
        'api_key': _text(config.get('api_key')),
        'skip_auth': 'true' if config.get('skip_auth') is True else '',
    }


def _mapping(value: object) -> Mapping[str, Any]:
    if not isinstance(value, Mapping):
        raise EvoModelConfigError(MODEL_NOT_CONFIGURED, 'model_not_configured')
    return value


def _text(value: object) -> str:
    return str(value or '').strip()

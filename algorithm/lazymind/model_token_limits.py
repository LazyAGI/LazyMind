"""Apply model limits at LazyMind's call boundaries without mutating shared models."""
from __future__ import annotations

import re
from typing import Any, Optional

_TOKEN_LIMIT_PATTERN = re.compile(r'^(\d+(?:\.\d+)?)\s*([KM])?$', re.IGNORECASE)


def parse_token_limit(value: Any) -> Optional[int]:
    if isinstance(value, bool):
        return None
    if isinstance(value, int) and value > 0:
        return value
    if isinstance(value, float) and value > 0:
        return int(value)
    if not isinstance(value, str):
        return None
    match = _TOKEN_LIMIT_PATTERN.fullmatch(value.strip())
    if not match:
        return None
    amount = float(match.group(1))
    suffix = (match.group(2) or '').upper()
    multiplier = {'K': 1024, 'M': 1024 * 1024}.get(suffix, 1)
    parsed = int(amount * multiplier)
    return parsed if parsed > 0 else None


def capped_output_kwargs(role_config: Any, kwargs: dict[str, Any]) -> dict[str, Any]:
    """Respect a smaller caller request; an unset model limit changes nothing."""
    limit = parse_token_limit(role_config.get('max_output_tokens')) if isinstance(role_config, dict) else None
    if limit is None:
        return kwargs
    requested = parse_token_limit(kwargs.get('max_tokens'))
    if kwargs.get('max_tokens') is not None and requested is None:
        raise ValueError('max_tokens must be a positive token limit')
    return {**kwargs, 'max_tokens': min(requested, limit) if requested else limit}


def bounded_model_for_role(model: Any, llm_config: Any) -> Any:
    """Give a named agent model its own bounded static parameters.

    The exact module name selects the configured role. Tool models with a
    different role never inherit the main dialogue model's output limit.
    """
    role = getattr(model, 'name', None)
    if not isinstance(role, str) or not isinstance(llm_config, dict):
        return model
    role_config = llm_config.get(role)
    if not isinstance(role_config, dict) or parse_token_limit(role_config.get('max_output_tokens')) is None:
        return model
    bounded = model.share(copy_static_params=True)
    bounded._static_params = capped_output_kwargs(role_config, dict(bounded._static_params))
    return bounded

from __future__ import annotations

import re
from typing import Any, Optional

from lazymind.config import config

from .models import ContextBudget

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
    multiplier = {'K': 1_000, 'M': 1_000_000}.get(suffix, 1)
    parsed = int(amount * multiplier)
    return parsed if parsed > 0 else None


def _role_max_input_tokens(role_cfg: Any) -> Optional[int]:
    if not isinstance(role_cfg, dict):
        return None
    return parse_token_limit(role_cfg.get('max_input_tokens'))


def _llm_config_max_input_tokens(llm_config: Optional[dict[str, Any]]) -> Optional[int]:
    if not isinstance(llm_config, dict):
        return None
    for role in ('llm', 'vlm', 'chat'):
        parsed = _role_max_input_tokens(llm_config.get(role))
        if parsed:
            return parsed
    return parse_token_limit(llm_config.get('max_input_tokens'))


def resolve_max_input_tokens(
    max_input_tokens: Any = None,
    llm_config: Optional[dict[str, Any]] = None,
) -> int:
    parsed = parse_token_limit(max_input_tokens)
    if parsed:
        return parsed
    parsed = _llm_config_max_input_tokens(llm_config)
    if parsed:
        return parsed
    return max(1, int(config['context_compression_default_max_input_tokens']))


def _budget_source(
    max_input_tokens: Any,
    llm_config: Optional[dict[str, Any]],
) -> str:
    if parse_token_limit(max_input_tokens):
        return 'explicit'
    if _llm_config_max_input_tokens(llm_config):
        return 'llm_config'
    return 'fallback'


def build_context_budget(
    max_input_tokens: Any = None,
    *,
    llm_config: Optional[dict[str, Any]] = None,
    trigger_ratio: Optional[float] = None,
    target_ratio: Optional[float] = None,
    reserved_output_tokens: Optional[int] = None,
) -> ContextBudget:
    resolved_max = resolve_max_input_tokens(max_input_tokens, llm_config)
    reserved = max(
        0,
        int(
            reserved_output_tokens
            if reserved_output_tokens is not None
            else config['context_compression_reserved_output_tokens']
        ),
    )
    # Cap reservation so smaller model windows still keep a usable input budget.
    reserved = min(reserved, max(0, resolved_max // 2))
    trigger = float(
        trigger_ratio if trigger_ratio is not None else config['context_compression_trigger_ratio']
    )
    target = float(
        target_ratio if target_ratio is not None else config['context_compression_target_ratio']
    )
    effective = max(1, resolved_max - reserved)
    return ContextBudget(
        max_input_tokens=resolved_max,
        reserved_output_tokens=reserved,
        effective_input_budget=effective,
        trigger_tokens=max(1, round(effective * trigger)),
        target_tokens=max(1, round(effective * target)),
        trigger_ratio=trigger,
        target_ratio=target,
        source=_budget_source(max_input_tokens, llm_config),
    )


def needs_compression(estimated_tokens: int, budget: ContextBudget) -> bool:
    return int(estimated_tokens) >= budget.trigger_tokens


def usage_ratio(estimated_tokens: int, budget: ContextBudget) -> float:
    if budget.effective_input_budget <= 0:
        return 0.0
    return float(estimated_tokens) / float(budget.effective_input_budget)

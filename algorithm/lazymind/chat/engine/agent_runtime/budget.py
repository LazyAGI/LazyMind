from __future__ import annotations

from typing import Any, Optional

from lazymind.config import config
from lazymind.model_token_limits import parse_token_limit

from .models import ContextBudget


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
    resolved = (parse_token_limit(max_input_tokens)
                or _llm_config_max_input_tokens(llm_config)
                or max(1, int(config['context_compression_default_max_input_tokens'])))
    if isinstance(llm_config, dict):
        selected = next((llm_config[role] for role in ('llm', 'vlm', 'chat')
                         if isinstance(llm_config.get(role), dict)
                         and _role_max_input_tokens(llm_config[role])), llm_config)
        limit = parse_token_limit(selected.get('input_token_limit'))
        if limit:
            resolved = min(resolved, limit)
    return resolved


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

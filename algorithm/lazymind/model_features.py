"""Permissions for optional chat features, separate from support declarations.

Disabling reasoning suppresses explicit reasoning requests. Intrinsic thinking in
an upstream model is not universally switchable and remains upstream behavior.
No name-based inference or undocumented provider parameter is added here.
"""
from copy import deepcopy

_TOOL_FIELDS = frozenset({'tools', 'tool_choice', 'functions', 'function_call', 'parallel_tool_calls'})
_REASONING_FIELDS = frozenset({'reasoning', 'thinking', 'reasoning_effort', 'enable_thinking',
                               'include_reasoning', 'thinking_budget', 'thinking_budget_tokens'})
_ENVELOPES = ('parameters', 'extra_body', 'chat_template_kwargs')


def apply_model_features(body: dict, features: dict, source: str = '') -> dict:
    """Veto disabled optional request fields at the final transport boundary."""
    result = deepcopy(body)
    forbidden = set()
    if features.get('tool_calling') is False:
        forbidden.update(_TOOL_FIELDS)
    if features.get('reasoning') is False:
        forbidden.update(_REASONING_FIELDS)

    def strip(value):
        if not isinstance(value, dict):
            return
        for field in forbidden:
            value.pop(field, None)
        for envelope in _ENVELOPES:
            strip(value.get(envelope))
    strip(result)
    return result


def feature_is_enabled(feature: str, model_config=None, role='llm') -> bool:
    """Unknown preserves legacy behavior; explicit false is authoritative."""
    entry = (model_config or {}).get(role) if isinstance(model_config, dict) else None
    if not isinstance(entry, dict):
        import lazyllm
        entry = (lazyllm.globals.get('lazymind_model_capabilities') or {}).get(role, {})
    features = entry.get('features') if isinstance(entry, dict) else None
    return not isinstance(features, dict) or features.get(feature) is not False

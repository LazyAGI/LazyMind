"""Chat engine public API with imports deferred until an attribute is used."""

from __future__ import annotations

import importlib


_EXPORTS = {
    'KBToolkit': ('lazymind.chat.engine.tools.kb', 'KBToolkit'),
    'calculator': ('lazymind.chat.engine.tools.calculator', 'calculator'),
    'kb_tmp_search': ('lazymind.chat.engine.tools.kb', 'kb_tmp_search'),
    'SkillManagementToolkit': ('lazymind.chat.engine.tools.skill_editor', 'SkillManagementToolkit'),
    'url_fetch': ('lazymind.chat.engine.tools.web_search', 'url_fetch'),
    'vision_extractor': ('lazymind.chat.engine.tools.multimodal', 'vision_extractor'),
    'vocab_learn': ('lazymind.chat.engine.tools.vocab_learn', 'vocab_learn'),
}

__all__ = list(_EXPORTS)


def __getattr__(name: str):
    try:
        module_name, attribute = _EXPORTS[name]
    except KeyError as exc:
        raise AttributeError(name) from exc
    value = getattr(importlib.import_module(module_name), attribute)
    globals()[name] = value
    return value

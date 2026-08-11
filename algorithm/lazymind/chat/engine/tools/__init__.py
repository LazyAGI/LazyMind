"""Chat tools public API with imports deferred until an attribute is used."""

from __future__ import annotations

import importlib


_EXPORTS = {
    'build_schedule_toolkit': ('.schedule', 'build_schedule_toolkit'),
    'calculator': ('.calculator', 'calculator'),
    'ExternalDatabaseToolkit': ('.external_db', 'ExternalDatabaseToolkit'),
    'image_editor': ('.multimodal', 'image_editor'),
    'image_generator': ('.multimodal', 'image_generator'),
    'video_generator': ('.multimodal', 'video_generator'),
    'video_to_gif': ('.multimodal', 'video_to_gif'),
    'LocalFileToolkit': ('.local_fs', 'LocalFileToolkit'),
    'vision_extractor': ('.multimodal', 'vision_extractor'),
    'SkillManagementToolkit': ('.skill_editor', 'SkillManagementToolkit'),
    'list_data_sources': ('.system_query', 'list_data_sources'),
    'vocab_learn': ('.vocab_learn', 'vocab_learn'),
    'url_fetch': ('.web_search', 'url_fetch'),
    'WriterCreateToolkit': ('.writer', 'WriterCreateToolkit'),
    'WriterRevisionToolkit': ('.writer', 'WriterRevisionToolkit'),
}

__all__ = list(_EXPORTS)


def __getattr__(name: str):
    try:
        module_name, attribute = _EXPORTS[name]
    except KeyError as exc:
        raise AttributeError(name) from exc
    value = getattr(importlib.import_module(module_name, __name__), attribute)
    globals()[name] = value
    return value

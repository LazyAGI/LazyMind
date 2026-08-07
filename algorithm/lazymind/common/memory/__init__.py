from __future__ import annotations

from .context import (
    MemoryContext,
    load_memory_context,
    truncate_preference_index,
)
from .editors import (
    add_preference_entry,
    apply_memory_operations,
    delete_preference_entry,
    preference_name_to_reference_name,
    validate_preference_name,
)
from .models import (
    EpisodeCreateInput,
    EpisodeCreateResult,
    EpisodeDeleteResult,
    EpisodeRecord,
    EpisodeSearchResult,
    EpisodeSource,
    EpisodeType,
    normalize_episode_summary,
)
from .paths import (
    AGENTS_ROOT,
    PREFERENCE_PATH,
    PROFILE_PATH,
    REFERENCE_ROOT,
    SOUL_PATH,
    USERS_ROOT,
    build_reference_path,
    is_reference_path,
    normalize_memory_path,
    split_reference_ref,
)
from .result import memory_err, memory_ok
from .validation import (
    PreferenceItem,
    append_preference_item,
    parse_preference_items,
    remove_preference_item,
    validate_preference_index,
    validate_reference_content,
    validate_stored_memory_content,
)
from .store import MemoryStore

_EPISODE_STORE_EXPORTS = {
    'EpisodeReadError',
    'EpisodeStore',
    'get_episode_store',
}
_EPISODE_RANKING_EXPORTS = {
    'episode_query_coverage',
    'informative_query_terms',
    'tokenize_episode_text',
}


def __getattr__(name: str):
    """Load the Core HTTP-backed Episode runtime only when it is requested.

    Soul/Profile/Preference validation and file operations should not require
    the Episode tokenizer or HTTP client dependencies.
    """
    if name in _EPISODE_STORE_EXPORTS:
        from . import episode_store

        value = getattr(episode_store, name)
        globals()[name] = value
        return value
    if name in _EPISODE_RANKING_EXPORTS:
        from . import ranking

        value = getattr(ranking, name)
        globals()[name] = value
        return value
    raise AttributeError(f'module {__name__!r} has no attribute {name!r}')


__all__ = [
    'AGENTS_ROOT',
    'MemoryContext',
    'MemoryStore',
    'PREFERENCE_PATH',
    'PROFILE_PATH',
    'PreferenceItem',
    'REFERENCE_ROOT',
    'SOUL_PATH',
    'USERS_ROOT',
    'EpisodeReadError',
    'EpisodeCreateInput',
    'EpisodeCreateResult',
    'EpisodeDeleteResult',
    'EpisodeRecord',
    'EpisodeSearchResult',
    'EpisodeSource',
    'EpisodeStore',
    'EpisodeType',
    'add_preference_entry',
    'append_preference_item',
    'apply_memory_operations',
    'build_reference_path',
    'delete_preference_entry',
    'episode_query_coverage',
    'get_episode_store',
    'informative_query_terms',
    'is_reference_path',
    'load_memory_context',
    'memory_err',
    'memory_ok',
    'normalize_episode_summary',
    'normalize_memory_path',
    'parse_preference_items',
    'preference_name_to_reference_name',
    'remove_preference_item',
    'split_reference_ref',
    'tokenize_episode_text',
    'truncate_preference_index',
    'validate_preference_index',
    'validate_preference_name',
    'validate_reference_content',
    'validate_stored_memory_content',
]

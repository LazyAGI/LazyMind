"""Infrastructure helpers loaded only when their public attribute is used."""

from __future__ import annotations

import importlib


_MODULE_EXPORTS = {
    '.core_api_client': ('get_core_api', 'post_core_api'),
    '.calculator_eval': ('safe_evaluate_expression',),
    '.tool_result_citations': ('CitationResultMiddleware',),
    '.web_search_support': ('fetch_url_content',),
    '.kb_opensearch_client': ('opensearch_search', 'resolve_index', 'term_filter'),
    '.github_skill_installer': ('GitHubSkillInstaller',),
    '.suggestion': ('Suggestion', 'dump_suggestion'),
    '.vocab_support': (
        'VocabSuggestion', 'dedupe_vocab_values_keep_order', 'dump_vocab_suggestion',
        'norm_vocab_text', 'prepare_vocab_candidates', 'resolve_vocab_user_id',
        'serialize_vocab_backend_actions', 'summarize_vocab_action_for_log',
        'summarize_vocab_candidate_for_log', 'summarize_vocab_suggestion_for_log',
    ),
    '.vocab_db': ('fetch_chat_histories_for_session', 'fetch_vocab_groups_for_user_id'),
    '.vocab_manager': ('VocabManager',),
    '.vocab_planning': ('ActionPlanningModule', 'ChatHistoryRecord', 'SynonymCandidate', 'VocabEvolutionRequest'),
    '.vocab_registry': ('clear_vocab_registry', 'get_vocab_manager'),
    '.tool_runtime': ('handle_tool_errors', 'tool_error', 'tool_failure', 'tool_success'),
}
_EXPORTS = {
    name: (module_name, name)
    for module_name, names in _MODULE_EXPORTS.items()
    for name in names
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

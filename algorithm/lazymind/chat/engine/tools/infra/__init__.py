"""Infrastructure helpers for chat engine tools."""

from .core_api_client import (
    post_core_api,
)
from .calculator_eval import (
    safe_evaluate_expression,
)
from .web_search_support import (
    fetch_url_content,
)
from .kb_opensearch_client import (
    opensearch_search,
    resolve_index,
    term_filter,
)
from .skill_registry import (
    build_skill_identity,
    is_writable_skill_source,
    list_all_skill_entries,
    list_all_skills_with_category,
)
from .skill_remote_store import (
    create_remote_skill,
    remove_remote_skill,
)
from .skill_validation import (
    normalize_skill_category,
    parse_skill_frontmatter,
    validate_skill_content,
    validate_skill_name,
)
from .user_preference_validation import (
    parse_user_preference_frontmatter,
    validate_user_preference_content,
)
from .suggestion import (
    Suggestion,
    dump_suggestion,
)
from .vocab_support import (
    VocabSuggestion,
    dedupe_vocab_values_keep_order,
    dump_vocab_suggestion,
    norm_vocab_text,
    prepare_vocab_candidates,
    resolve_vocab_user_id,
    serialize_vocab_backend_actions,
    summarize_vocab_action_for_log,
    summarize_vocab_candidate_for_log,
    summarize_vocab_suggestion_for_log,
)
from .vocab_db import (
    fetch_chat_histories_for_session,
    fetch_vocab_groups_for_user_id,
)
from .vocab_manager import (
    VocabManager,
)
from .vocab_planning import (
    ActionPlanningModule,
    ChatHistoryRecord,
    SynonymCandidate,
    VocabEvolutionRequest,
)
from .vocab_registry import (
    clear_vocab_registry,
    get_vocab_manager,
)
from .tool_runtime import (
    handle_tool_errors,
    tool_error,
    tool_failure,
    tool_success,
)

__all__ = [
    'Suggestion',
    'ActionPlanningModule',
    'ChatHistoryRecord',
    'SynonymCandidate',
    'VocabSuggestion',
    'VocabEvolutionRequest',
    'VocabManager',
    'build_skill_identity',
    'clear_vocab_registry',
    'create_remote_skill',
    'dedupe_vocab_values_keep_order',
    'dump_suggestion',
    'dump_vocab_suggestion',
    'fetch_chat_histories_for_session',
    'fetch_url_content',
    'fetch_vocab_groups_for_user_id',
    'get_vocab_manager',
    'handle_tool_errors',
    'is_writable_skill_source',
    'list_all_skill_entries',
    'list_all_skills_with_category',
    'norm_vocab_text',
    'normalize_skill_category',
    'opensearch_search',
    'parse_skill_frontmatter',
    'parse_user_preference_frontmatter',
    'post_core_api',
    'prepare_vocab_candidates',
    'remove_remote_skill',
    'resolve_index',
    'resolve_vocab_user_id',
    'safe_evaluate_expression',
    'serialize_vocab_backend_actions',
    'summarize_vocab_action_for_log',
    'summarize_vocab_candidate_for_log',
    'summarize_vocab_suggestion_for_log',
    'term_filter',
    'tool_error',
    'tool_failure',
    'tool_success',
    'validate_skill_content',
    'validate_skill_name',
    'validate_user_preference_content',
]

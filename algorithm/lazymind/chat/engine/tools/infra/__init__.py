"""Infrastructure helpers for chat engine tools."""

from .core_api_client import (
    get_core_api,
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
from .github_skill_installer import (
    GitHubSkillInstaller,
)
from .memory_remote_store import (
    MEMORY_TARGET_PATHS,
    MemoryRemoteStore,
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
    'MEMORY_TARGET_PATHS',
    'MemoryRemoteStore',
    'SynonymCandidate',
    'VocabSuggestion',
    'VocabEvolutionRequest',
    'VocabManager',
    'clear_vocab_registry',
    'dedupe_vocab_values_keep_order',
    'dump_suggestion',
    'dump_vocab_suggestion',
    'fetch_chat_histories_for_session',
    'fetch_url_content',
    'fetch_vocab_groups_for_user_id',
    'get_core_api',
    'get_vocab_manager',
    'GitHubSkillInstaller',
    'handle_tool_errors',
    'norm_vocab_text',
    'opensearch_search',
    'parse_user_preference_frontmatter',
    'post_core_api',
    'prepare_vocab_candidates',
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
    'validate_user_preference_content',
]

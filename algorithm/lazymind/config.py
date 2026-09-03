import os
from pathlib import Path

import lazyllm
from lazyllm.configs import Config

_COMMON_DIR = Path(__file__).resolve().parent / 'common'
EMBED_MAIN = 'embed_main'
EMBED_IMAGE = 'embed_image'
EMBED_KEYS = [EMBED_MAIN, EMBED_IMAGE]
EMBED_INDEX_KWARGS = [
    {
        'embed_key': EMBED_MAIN,
        'index_type': 'IVF_FLAT',
        'metric_type': 'COSINE',
        'params': {'nlist': 128},
    },
    {
        'embed_key': EMBED_IMAGE,
        'index_type': 'IVF_FLAT',
        'metric_type': 'COSINE',
        'params': {'nlist': 128},
    },
]


def apply_local_model_config_override(resolved_path):
    """Prefer gitignored runtime_models.local.yaml next to an inner config."""
    if not resolved_path:
        return resolved_path
    path = Path(resolved_path)
    if path.name != 'runtime_models.inner.yaml':
        return str(path)
    local = path.with_name('runtime_models.local.yaml')
    return str(local) if local.is_file() else str(path)


def _model_config_path_post_action(resolved_path):
    path = apply_local_model_config_override(resolved_path)
    if not path:
        return
    lazyllm.config['auto_model_config_map_path'] = str(path)


def _require_positive_config_value(env_name):
    def validate(value):
        if not isinstance(value, int) or value <= 0:
            raise ValueError(f'{env_name} must be a positive integer')

    return validate


def _parse_positive_integer_env(env_name, raw):
    if raw is None:
        return None
    try:
        value = int(raw.strip())
    except (AttributeError, TypeError, ValueError) as exc:
        raise ValueError(f'{env_name} must be a positive integer') from exc
    if value <= 0:
        raise ValueError(f'{env_name} must be a positive integer')
    return value


def _validate_positive_integer_env(env_name):
    _parse_positive_integer_env(env_name, os.environ.get(env_name))


def _require_integer_range_config_value(env_name, minimum, maximum):
    def validate(value):
        if (
            not isinstance(value, int)
            or isinstance(value, bool)
            or value < minimum
            or value > maximum
        ):
            raise ValueError(
                f'{env_name} must be an integer between {minimum} and {maximum}'
            )

    return validate


def _validate_integer_range_env(env_name, minimum, maximum):
    raw = os.environ.get(env_name)
    if raw is None:
        return
    try:
        value = int(raw.strip())
    except (AttributeError, TypeError, ValueError) as exc:
        raise ValueError(
            f'{env_name} must be an integer between {minimum} and {maximum}'
        ) from exc
    if value < minimum or value > maximum:
        raise ValueError(
            f'{env_name} must be an integer between {minimum} and {maximum}'
        )


# Single Config instance for the entire algorithm package.
# All LAZYMIND_* environment variables are registered here.
config = Config(prefix='LAZYMIND', home='~/.lazyllm_rag')
_LAZYMIND_ROOT = os.path.dirname(__file__)
config.add('runtime_mode', str, 'cloud', 'RUNTIME_MODE',
           description='LazyMind runtime mode profile: cloud or local.')

# ---------------------------------------------------------------------------
# Chat
# ---------------------------------------------------------------------------
config.add('mount_base_dir', str, '/data', 'MOUNT_BASE_DIR', description='Base directory for mounted files.')
config.add(
    'sensitive_red_words_path',
    str,
    os.path.join(_LAZYMIND_ROOT, 'chat', 'resources', 'sensitive_red.txt'),
    'SENSITIVE_RED_WORDS_PATH',
    description='Path to sensitive red words file.',
)
config.add(
    'sensitive_gray_words_path',
    str,
    os.path.join(_LAZYMIND_ROOT, 'chat', 'resources', 'sensitive_gray.txt'),
    'SENSITIVE_GRAY_WORDS_PATH',
    description='Path to sensitive gray words file.',
)
config.add(
    'sensitive_whitelist_path',
    str,
    os.path.join(_LAZYMIND_ROOT, 'chat', 'resources', 'sensitive_whitelist.txt'),
    'SENSITIVE_WHITELIST_PATH',
    description='Path to sensitive whitelist file.',
)
config.add('llm_priority', int, 0, 'LLM_PRIORITY', description='LLM priority level.')
config.add('max_concurrency', int, 10, 'MAX_CONCURRENCY', description='Max concurrent requests.')
config.add('rag_mode', bool, True, 'RAG_MODE', description='Enable RAG mode.')
config.add('shared_upload_dir', str, '/var/lib/lazymind/uploads', 'SHARED_UPLOAD_DIR',
           description='Shared upload dir for normalized images and frames.')
config.add('whisper_model', str, 'base', 'WHISPER_MODEL',
           description='OpenAI whisper model version for video/audio transcription.')
config.add('video_frame_interval', int, 20, 'VIDEO_FRAME_INTERVAL',
           description='Interval (seconds) between extracted video frames.')
config.add('audio_segment_interval', int, 15, 'AUDIO_SEGMENT_INTERVAL',
           description='Audio transcript segment merge interval in seconds.')
config.add('default_chat_dataset', str, 'algo', 'DEFAULT_CHAT_DATASET', description='Default chat dataset.')
config.add(
    'workflows_dir',
    str,
    str(Path(__file__).resolve().parent.parent.parent / 'workflows'),
    'WORKFLOWS_DIR',
    description='Directory containing workflow packages. Each sub-directory is one workflow.',
)
config.add('model_config_path', str, 'dynamic', 'MODEL_CONFIG_PATH',
           description='Runtime model config YAML path. Shorthand aliases are auto-resolved to absolute paths.',
           alias={
               'inner': str(_COMMON_DIR / 'runtime_models.inner.yaml'),
               'online': str(_COMMON_DIR / 'runtime_models.online.yaml'),
               'dynamic': str(_COMMON_DIR / 'runtime_models.yaml'),
           },
           post_action=_model_config_path_post_action)
config.add('algo_id', str, 'general_algo', 'ALGO_ID', description='LazyMind algorithm ID.')
# Global router toggle. Registered here (not in router/config.py) so that both the chat
# entrypoint and the router entrypoint can read it without cross-importing router config.
config.add('enable_router', bool, False, 'ENABLE_ROUTER',
           description='Enable router mode. When false, app.py falls back to the original chat service.')
config.add('background_jobs_enabled', bool, True, 'BACKGROUND_JOBS_ENABLED',
           description='Enable non-request background maintenance jobs for this service.')
config.add('state_backend', str, 'redis', 'STATE_BACKEND',
           description='Short-lived state backend: redis or sqlite.')
# Marks a process as a router-spawned child that only serves proxied request types
# (chat / subagent). Set automatically by ProcessManager when spawning children.
config.add('router_child_proxied_only', bool, False, 'ROUTER_CHILD_PROXIED_ONLY',
           description='When true, skip stateless shared endpoints (rewrite/review/model_*) that the '
                       'main router process serves directly. Set on router-spawned child processes.')

# ---------------------------------------------------------------------------
# Tracing / observability
# ---------------------------------------------------------------------------
config.add('langfuse_force_flush_timeout_ms', int, 5000, 'LANGFUSE_FORCE_FLUSH_TIMEOUT_MS',
           description='Langfuse flush timeout in ms.')
config.add('document_server_url', str, 'http://localhost:8000', 'DOCUMENT_SERVER_URL',
           description='Document server URL for health checks.')

# ---------------------------------------------------------------------------
# Agentic
# ---------------------------------------------------------------------------
config.add('agentic_kb_url', str, 'http://lazyllm-algo:8000', 'AGENTIC_KB_URL',
           description='Knowledge base service URL for agentic tools.')
config.add('core_api_url', str, 'http://core:8000', 'CORE_API_URL', description='Core API service URL.')
config.add('core_api_timeout', int, 30, 'CORE_API_TIMEOUT', description='Core API request timeout in seconds.')
config.add('core_internal_token', str, '', 'AUTH_SERVICE_INTERNAL_TOKEN',
           description='Internal service token for privileged Core API calls.')
config.add('agentic_kb_name', str, 'general_algo', 'AGENTIC_KB_NAME',
           description='Default knowledge base name for agentic.')
config.add('skill_fs_url', str, 'remote://skills', 'SKILL_FS_URL', description='Skill filesystem URL.')
_validate_positive_integer_env('LAZYMIND_PREFERENCE_INDEX_MAX_ITEMS')
config.add(
    'preference_index_max_items',
    int,
    100,
    'PREFERENCE_INDEX_MAX_ITEMS',
    description='Maximum number of Preference index items eligible for resident prompt projection.',
    post_action=_require_positive_config_value('LAZYMIND_PREFERENCE_INDEX_MAX_ITEMS'),
)
_validate_positive_integer_env('LAZYMIND_PREFERENCE_CONTEXT_MAX_CHARS')
config.add(
    'preference_context_max_chars',
    int,
    5000,
    'PREFERENCE_CONTEXT_MAX_CHARS',
    description='Maximum rendered Preference index characters injected into a prompt.',
    post_action=_require_positive_config_value('LAZYMIND_PREFERENCE_CONTEXT_MAX_CHARS'),
)
config.add('segment_store_type', str, 'opensearch', 'SEGMENT_STORE_TYPE',
           description='Segment store type: opensearch, elasticsearch, or SQLiteStore.')
config.add('segment_store_uri_or_path', str, 'https://opensearch:9200', 'SEGMENT_STORE_URI_OR_PATH',
           description='Segment store URI (OpenSearch/Elasticsearch) or file path (SQLite).')
config.add('segment_store_user', str, 'admin', 'SEGMENT_STORE_USER',
           description='Segment store username (OpenSearch/Elasticsearch only).')
config.add('segment_store_password', str, 'LazyRAG_OpenSearch123!', 'SEGMENT_STORE_PASSWORD',
           description='Segment store password (OpenSearch/Elasticsearch only).')
config.add('episode_candidate_topk', int, 20, 'EPISODE_CANDIDATE_TOPK',
           description='Episode FTS candidate count.')
config.add('episode_inject_topk', int, 5, 'EPISODE_INJECT_TOPK',
           description='Maximum Episode snapshots injected per chat request.')
_validate_integer_range_env('LAZYMIND_EPISODE_RECENT_PROGRESS_INJECT_TOPK', 0, 3)
config.add(
    'episode_recent_progress_inject_topk',
    int,
    3,
    'EPISODE_RECENT_PROGRESS_INJECT_TOPK',
    description='Maximum recent progress Episodes injected on a first-turn semantic miss.',
    post_action=_require_integer_range_config_value(
        'LAZYMIND_EPISODE_RECENT_PROGRESS_INJECT_TOPK',
        0,
        3,
    ),
)
config.add('episode_context_max_chars', int, 4000, 'EPISODE_CONTEXT_MAX_CHARS',
           description='Episode prompt character budget.')
config.add('episode_relevance_weight', float, 0.75, 'EPISODE_RELEVANCE_WEIGHT',
           description='Episode hard-filter term coverage ranking weight.')
config.add('episode_recency_weight', float, 0.20, 'EPISODE_RECENCY_WEIGHT')
config.add('episode_hit_weight', float, 0.05, 'EPISODE_HIT_WEIGHT')
config.add('episode_half_life_days', float, 30.0, 'EPISODE_HALF_LIFE_DAYS')
config.add('episode_hit_saturation', int, 10, 'EPISODE_HIT_SATURATION')
config.add('web_search_timeout', int, 10, 'WEB_SEARCH_TIMEOUT', description='Web search request timeout in seconds.')
config.add('url_fetch_max_length', int, 4000, 'URL_FETCH_MAX_LENGTH',
           description='Maximum readable text length returned by url_fetch.')
config.add('url_fetch_pdf_max_bytes', int, 100 * 1024 * 1024, 'URL_FETCH_PDF_MAX_BYTES',
           description='Maximum PDF download size for url_fetch ingestion.')
config.add('max_retries', int, 20, 'MAX_RETRIES', description='Max retries for agentic function call loop.')
config.add('agentic_max_rounds_low', int, 6, 'AGENTIC_MAX_ROUNDS_LOW',
           description='Maximum ChatAgent ReAct rounds in low thinking-depth mode.')
config.add('agentic_max_rounds_medium', int, 20, 'AGENTIC_MAX_ROUNDS_MEDIUM',
           description='Maximum ChatAgent ReAct rounds in medium thinking-depth mode.')
config.add('agentic_max_rounds_high', int, 60, 'AGENTIC_MAX_ROUNDS_HIGH',
           description='Maximum ChatAgent ReAct rounds in high thinking-depth mode.')
config.add('agentic_tool_limit_wait_timeout', float, 120, 'AGENTIC_TOOL_LIMIT_WAIT_TIMEOUT',
           description='Seconds ChatAgent waits for a user decision after reaching its initial round limit.')
config.add('agentic_expanded_max_rounds', int, 200, 'AGENTIC_EXPANDED_MAX_ROUNDS',
           description='Maximum ReAct rounds for one ChatAgent invocation after the user continues.')
config.add('agentic_workspace', str, './workspace', 'AGENTIC_WORKSPACE',
           description=(
               'Root for the main-agent conversation workspace '
               '(chat-artifacts/<user>/<conv>/). Deployments should set this to the same '
               'value as LAZYMIND_SUBAGENT_WORKSPACE so unpublished working files and '
               'tool spills persist next to published artifacts.'
           ))
config.add('trusted_local_mode', bool, False, 'TRUSTED_LOCAL_MODE',
           description='Allow agents to access host paths outside their workspace and use local command tools.')
config.add('agentic_keep_full_turns', int, 2, 'AGENTIC_KEEP_FULL_TURNS',
           description='Number of recent tool results kept intact during context compression.')
# Context compression knobs (process-level via LAZYMIND_*). Master switch gates all
# strategies. Code defaults are ON; local .env may set them false until validated.
# Summary strategy gates with context_compression_enabled AND
# context_summary_compression_enabled. Do not put flags in request payload,
# llm_config, or runtime_models YAML.
config.add(
    'context_compression_enabled',
    bool,
    True,
    'CONTEXT_COMPRESSION_ENABLED',
    description=(
        'Master switch for ChatAgent context compression '
        '(deterministic tool-result prune/compact and summary).'
    ),
)
config.add('context_compression_default_max_input_tokens', int, 64000,
           'CONTEXT_COMPRESSION_DEFAULT_MAX_INPUT_TOKENS',
           description=(
               'Fallback max input tokens when llm_config/catalog does not provide one.'
           ))
config.add('context_compression_trigger_ratio', float, 0.9, 'CONTEXT_COMPRESSION_TRIGGER_RATIO',
           description='Compress when estimated tokens reach this fraction of the effective input budget.')
config.add('context_compression_target_ratio', float, 0.45, 'CONTEXT_COMPRESSION_TARGET_RATIO',
           description='Target fraction of the effective input budget after compression.')
config.add('context_compression_reserved_output_tokens', int, 8192,
           'CONTEXT_COMPRESSION_RESERVED_OUTPUT_TOKENS',
           description='Tokens reserved for model output when computing the effective input budget.')
config.add(
    'context_prune_cache_amortization_calls',
    int,
    2,
    'CONTEXT_PRUNE_CACHE_AMORTIZATION_CALLS',
    description='Maximum future model calls used to amortize deterministic prune cache disruption.',
)
config.add(
    'context_prune_cached_token_cost_ratio',
    float,
    0.25,
    'CONTEXT_PRUNE_CACHED_TOKEN_COST_RATIO',
    description='Estimated relative cost of invalidating one previously cached prompt token.',
)
config.add(
    'context_prune_min_reclaim_ratio',
    float,
    0.05,
    'CONTEXT_PRUNE_MIN_RECLAIM_RATIO',
    description=(
        'Minimum tokens a non-spill prune must reclaim, as a fraction of the '
        'effective input budget. Ignored when the result is already at or below '
        'target_tokens. Otherwise the required floor is min(ratio * budget, '
        'remaining gap to target, context_prune_min_reclaim_tokens_cap). '
        '0 disables the proportional floor.'
    ),
)
config.add(
    'context_prune_min_reclaim_tokens_cap',
    int,
    20000,
    'CONTEXT_PRUNE_MIN_RECLAIM_TOKENS_CAP',
    description=(
        'Upper bound on the proportional min-reclaim floor, applied before the '
        'remaining-target-gap cap.'
    ),
)
config.add(
    'context_compression_spill_bytes',
    int,
    16384,
    'CONTEXT_COMPRESSION_SPILL_BYTES',
    description=(
        'Offload a tool result to the chat workspace when its UTF-8 size exceeds this '
        'many bytes, even if it is inside the keep_recent window.'
    ),
)
config.add(
    'context_summary_compression_enabled',
    bool,
    True,
    'CONTEXT_SUMMARY_COMPRESSION_ENABLED',
    description=(
        'Enable LLM summary compression after deterministic prune when still over '
        'target. Requires context_compression_enabled.'
    ),
)
config.add(
    'context_summary_keep_recent_ratio',
    float,
    0.10,
    'CONTEXT_SUMMARY_KEEP_RECENT_RATIO',
    description='Max fraction of effective input budget kept as uncompressed recent Tail.',
)
config.add(
    'context_summary_min_recent_user_turns',
    int,
    1,
    'CONTEXT_SUMMARY_MIN_RECENT_USER_TURNS',
    description='Minimum recent user turns (1-3) kept intact in the Tail.',
)
config.add(
    'context_summary_required_overshoot_reclaim_ratio',
    float,
    0.80,
    'CONTEXT_SUMMARY_REQUIRED_OVERSHOOT_RECLAIM_RATIO',
    description='Required fraction of target overshoot reclaimed when fixed context makes target unreachable.',
)
config.add(
    'context_summary_max_output_to_replaced_ratio',
    float,
    0.30,
    'CONTEXT_SUMMARY_MAX_OUTPUT_TO_REPLACED_RATIO',
    description='Maximum summary output tokens as a fraction of the replaced history span.',
)
config.add(
    'context_compression_event_path',
    str,
    '',
    'CONTEXT_COMPRESSION_EVENT_PATH',
    description=(
        'Deprecated alias for agent_lab_event_path. Prefer LAZYMIND_AGENT_LAB_EVENT_PATH.'
    ),
)
config.add(
    'agent_lab_event_path',
    str,
    '',
    'AGENT_LAB_EVENT_PATH',
    description=(
        'Optional JSONL path for agent-lab runtime telemetry '
        '(turns, tools, file reads, harness, prune, summary). Empty disables writing.'
    ),
)

config.add('dynamic_prompt_modules', bool, True, 'DYNAMIC_PROMPT_MODULES',
           description='Enable per-turn task profiling and progressive prompt-module disclosure.')
config.add('agentic_stream_chunk_size', int, 24, 'AGENTIC_STREAM_CHUNK_SIZE',
           description='Fallback chunk size for final streamed agentic text.')
config.add('review_max_retries', int, 5, 'REVIEW_MAX_RETRIES', description='Max retries for background review agent.')
config.add('skill_review_debug', bool, False, 'SKILL_REVIEW_DEBUG', description='Enable skill review debug logging.')
config.add('review_debug', bool, False, 'REVIEW_DEBUG', description='Enable review debug logging.')

# ---------------------------------------------------------------------------
# Parsing
# ---------------------------------------------------------------------------
config.add('milvus_uri', str, None, 'MILVUS_URI', description='Milvus vector store URI (required).')
config.add('mineru_backend', str, 'pipeline', 'MINERU_BACKEND', description='MinerU processing backend.')
config.add('mineru_server_port', int, 8000, 'MINERU_SERVER_PORT', description='MinerU server port.')
config.add(
    'ocr_server_url',
    str,
    '',
    'OCR_SERVER_URL',
    description='Local OCR endpoint when the user has not selected a frontend OCR provider.',
)
config.add('ocr_cache_dir', str, os.path.join(config['shared_upload_dir'], '.image_cache'), 'OCR_CACHE_DIR',
           description='OCR cache root for parsed results and images.')
config.add('reader_use_cache', bool, True, 'READER_USE_CACHE',
           description='Reader ModuleBase cache; forwarded to LAZYLLM_READER_USE_CACHE.')
config.add('document_parse_profile', str, 'cloud', 'DOCUMENT_PARSE_PROFILE',
           description='Document parsing profile: cloud or local.')
config.add('document_processor_url', str, 'http://localhost:8000', 'DOCUMENT_PROCESSOR_URL',
           description='Document processor service URL.')
config.add('algo_server_port', int, 8000, 'ALGO_SERVER_PORT', description='Algorithm server port.')
config.add('document_server_port', int, 8000, 'DOCUMENT_SERVER_PORT',
           description='Document server port (fallback for algo_server_port).')
config.add('startup_retry_interval', str, '2', 'STARTUP_RETRY_INTERVAL',
           description='Startup retry interval in seconds.')
config.add('startup_timeout', str, '0', 'STARTUP_TIMEOUT',
           description='Startup wait timeout in seconds (0 = no timeout).')
config.add('reset_algo_on_startup', bool, False, 'RESET_ALGO_ON_STARTUP',
           description='Drop all vector/segment data and algorithm registration on startup, then rebuild from scratch.')
config.add('rag_image_path_prefix', str, '/mnt/lustre/share_data/mineru/images/', 'RAG_IMAGE_PATH_PREFIX',
           description='Image path prefix for RAG documents.')

# ---------------------------------------------------------------------------
# Processor
# ---------------------------------------------------------------------------
config.add('database_url', str, None, 'DATABASE_URL',
           description='Shared PostgreSQL URL (required for document processor).')
config.add('document_worker_port', int, 8001, 'DOCUMENT_WORKER_PORT', description='Document processor worker port.')
config.add('document_worker_num_workers', int, 1, 'DOCUMENT_WORKER_NUM_WORKERS',
           description='Number of document processor workers.')
# float values stored as str; consumers call float(config['...'])
config.add('document_worker_lease_duration', str, '300.0', 'DOCUMENT_WORKER_LEASE_DURATION',
           description='Worker lease duration in seconds.')
config.add('document_worker_lease_renew_interval', str, '60.0', 'DOCUMENT_WORKER_LEASE_RENEW_INTERVAL',
           description='Worker lease renew interval in seconds.')
config.add('document_worker_high_priority_task_types', str, None, 'DOCUMENT_WORKER_HIGH_PRIORITY_TASK_TYPES',
           description='Comma-separated high-priority task types.')
config.add('document_worker_high_priority_only', bool, False, 'DOCUMENT_WORKER_HIGH_PRIORITY_ONLY',
           description='Process only high-priority tasks.')
config.add('document_worker_poll_mode', str, 'direct', 'DOCUMENT_WORKER_POLL_MODE', description='Worker poll mode.')
config.add('upload_dir', str, '/app/uploads', 'UPLOAD_DIR', description='Upload directory for document files.')
config.add('default_algo_id', str, 'general_algo', 'DEFAULT_ALGO_ID', description='Default algorithm ID for uploads.')
config.add('default_group', str, 'block', 'DEFAULT_GROUP', description='Default group name for uploads.')
config.add('document_processor_port', int, 8000, 'DOCUMENT_PROCESSOR_PORT', description='Document processor HTTP port.')
config.add('upload_server_port', int, 8001, 'UPLOAD_SERVER_PORT', description='Upload server port.')

# ---------------------------------------------------------------------------
# Vocab
# ---------------------------------------------------------------------------
config.add('core_database_url', str, None, 'CORE_DATABASE_URL', description='Core service PostgreSQL URL.')
config.add('word_group_apply_url', str, None, 'WORD_GROUP_APPLY_URL', description='Word group apply endpoint URL.')
config.add('core_service_url', str, None, 'CORE_SERVICE_URL', description='Core service base URL.')
# ACL_DB_DSN: now requires LAZYMIND_ACL_DB_DSN prefix.
config.add('acl_db_dsn', str, None, 'ACL_DB_DSN', description='ACL database DSN (PostgreSQL connection string).')

# ---------------------------------------------------------------------------
# Evo
# ---------------------------------------------------------------------------
config.add('evo_code_provider', str, 'qwen', 'EVO_CODE_PROVIDER', description='Evo code provider.')
config.add('evo_code_model', str, 'qwen3-max', 'EVO_CODE_MODEL', description='Evo code model.')
config.add('evo_code_api_key', str, '', 'EVO_CODE_API_KEY', description='Evo code API key.')
config.add('evo_code_base_url', str, '', 'EVO_CODE_BASE_URL', description='Evo code provider base URL.')
config.add('evo_code_label', str, 'qwen', 'EVO_CODE_LABEL', description='Evo code provider display label.')
config.add('evo_code_agent', str, None, 'EVO_CODE_AGENT', description='Evo code agent.')
config.add('evo_code_variant', str, None, 'EVO_CODE_VARIANT', description='Evo code variant.')
config.add('evo_code_timeout_s', str, '600', 'EVO_CODE_TIMEOUT_S', description='Evo code timeout seconds.')
config.add('evo_code_data_dir', str, None, 'EVO_CODE_DATA_DIR', description='Evo code data directory.')
config.add('evo_code_binary', str, None, 'EVO_CODE_BINARY', description='Evo code binary.')
config.add('evo_code_skip_permissions', bool, True, 'EVO_CODE_SKIP_PERMISSIONS',
           description='Evo code skip permissions.')
config.add('evo_apply_test_command', str, 'bash tests/run-all.sh', 'EVO_APPLY_TEST_COMMAND',
           description='Evo apply test command.')
config.add('evo_apply_min_action_confidence', str, '0.5', 'EVO_APPLY_MIN_ACTION_CONFIDENCE',
           description='Evo apply minimum action confidence.')
config.add('evo_apply_min_action_validity', str, '0.5', 'EVO_APPLY_MIN_ACTION_VALIDITY',
           description='Evo apply minimum action validity.')
config.add('evo_llm_role', str, 'evo_llm', 'EVO_LLM_ROLE', description='Evo LLM AutoModel role.')
config.add('evo_auto_user_role', str, 'evo_llm', 'EVO_AUTO_USER_ROLE', description='Evo auto-user AutoModel role.')
config.add('evo_data_dir', str, None, 'EVO_DATA_DIR', description='Evo static data directory.')
config.add('evo_base_dir', str, None, 'EVO_BASE_DIR', description='Evo runtime storage directory.')
config.add('evo_code_map', str, None, 'EVO_CODE_MAP', description='Evo code map path.')
config.add('evo_chat_source', str, None, 'EVO_CHAT_SOURCE', description='Evo chat source directory.')

os.environ.setdefault('LAZYLLM_READER_USE_CACHE', str(bool(config['reader_use_cache'])).lower())

# MinerU online SSL: default verify (lazyllm mineru_ssl_verify=True).
# Skip only when LAZYMIND_RUNTIME_MODE=local or LAZYLLM_MINERU_SSL_VERIFY=false.
_runtime_mode = (config['runtime_mode'] or 'cloud').strip().lower()
_ssl_verify_env = os.environ.get('LAZYLLM_MINERU_SSL_VERIFY', '').strip().lower()
if _runtime_mode == 'local' or _ssl_verify_env in ('false', '0', 'no'):
    os.environ['LAZYLLM_MINERU_SSL_VERIFY'] = 'false'

config.add('memory_maintenance_workers', int, 2, 'MEMORY_MAINTENANCE_WORKERS',
           description='Dedicated shared Memory Review and Organizer worker capacity.')

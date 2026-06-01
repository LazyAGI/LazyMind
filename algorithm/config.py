import os
from lazyllm.configs import Config

# Single Config instance for the entire algorithm package.
# All LAZYMIND_* environment variables are registered here.
config = Config(prefix='LAZYMIND', home='~/.lazyllm_rag')

# ---------------------------------------------------------------------------
# Chat
# ---------------------------------------------------------------------------
config.add('mount_base_dir', str, '/data', 'MOUNT_BASE_DIR', description='Base directory for mounted files.')
config.add('sensitive_words_path', str, 'data/sensitive_words.txt', 'SENSITIVE_WORDS_PATH', description='Path to sensitive words file.')
config.add('llm_priority', int, 0, 'LLM_PRIORITY', description='LLM priority level.')
config.add('max_concurrency', int, 10, 'MAX_CONCURRENCY', description='Max concurrent requests.')
config.add('rag_mode', bool, True, 'RAG_MODE', description='Enable RAG mode.')
config.add('multimodal_mode', bool, True, 'MULTIMODAL_MODE', description='Enable multimodal mode.')
config.add('shared_upload_dir', str, '/var/lib/lazymind/uploads', 'SHARED_UPLOAD_DIR', description='Shared upload dir for normalized images and frames.')
config.add('whisper_model', str, 'base', 'WHISPER_MODEL', description='OpenAI whisper model version for video/audio transcription.')
config.add('video_frame_interval', int, 20, 'VIDEO_FRAME_INTERVAL', description='Interval (seconds) between extracted video frames.')
config.add('audio_segment_interval', int, 15, 'AUDIO_SEGMENT_INTERVAL', description='Audio transcript segment merge interval in seconds.')
config.add('image_topk', int, 3, 'IMAGE_TOPK', description='Top-k for image-only retriever branch.')
config.add('algo_service_url', str, 'http://lazyllm-algo:8000', 'ALGO_SERVICE_URL', description='Algorithm service URL.')
config.add('algo_dataset_name', str, 'general_algo', 'ALGO_DATASET_NAME', description='Default algorithm dataset name.')
config.add('default_chat_dataset', str, 'algo', 'DEFAULT_CHAT_DATASET', description='Default chat dataset.')
config.add('skip_startup_pipeline', bool, False, 'SKIP_STARTUP_PIPELINE', description='Skip startup pipeline initialization.')
config.add('model_config_path', str, 'dynamic', 'MODEL_CONFIG_PATH', description='Runtime model config path (inner/online/dynamic or file path).')

# ---------------------------------------------------------------------------
# Tracing / observability
# ---------------------------------------------------------------------------
config.add('langfuse_force_flush_timeout_ms', int, 5000, 'LANGFUSE_FORCE_FLUSH_TIMEOUT_MS', description='Langfuse flush timeout in ms.')
config.add('document_server_url', str, 'http://localhost:8000', 'DOCUMENT_SERVER_URL', description='Document server URL for health checks.')

# ---------------------------------------------------------------------------
# Agentic
# ---------------------------------------------------------------------------
config.add('agentic_kb_url', str, 'http://lazyllm-algo:8000', 'AGENTIC_KB_URL', description='Knowledge base service URL for agentic tools.')
config.add('core_api_url', str, 'http://core:8000', 'CORE_API_URL', description='Core API service URL.')
config.add('core_api_timeout', int, 30, 'CORE_API_TIMEOUT', description='Core API request timeout in seconds.')
config.add('agentic_kb_name', str, 'general_algo', 'AGENTIC_KB_NAME', description='Default knowledge base name for agentic.')
config.add('skill_fs_url', str, 'remote://skills', 'SKILL_FS_URL', description='Skill filesystem URL.')
config.add('opensearch_uri', str, None, 'OPENSEARCH_URI', description='OpenSearch/Elasticsearch URI.')
config.add('opensearch_user', str, 'admin', 'OPENSEARCH_USER', description='OpenSearch username.')
config.add('opensearch_password', str, '', 'OPENSEARCH_PASSWORD', description='OpenSearch password.')
config.add('web_search_timeout', int, 10, 'WEB_SEARCH_TIMEOUT', description='Web search request timeout in seconds.')
config.add('url_fetch_max_length', int, 4000, 'URL_FETCH_MAX_LENGTH', description='Maximum readable text length returned by url_fetch.')
config.add('web_search_auto_sources', str, 'bocha,google,bing,wikipedia', 'WEB_SEARCH_AUTO_SOURCES', description='Comma-separated list of auto web search sources.')
config.add('web_search_wikipedia_base_url', str, 'https://zh.wikipedia.org', 'WEB_SEARCH_WIKIPEDIA_BASE_URL', description='Wikipedia base URL for web search.')
config.add('web_search_google_api_key', str, '', 'WEB_SEARCH_GOOGLE_API_KEY', description='Google Custom Search API key.')
config.add('web_search_google_search_engine_id', str, '', 'WEB_SEARCH_GOOGLE_SEARCH_ENGINE_ID', description='Google Custom Search engine ID.')
config.add('web_search_bing_subscription_key', str, '', 'WEB_SEARCH_BING_SUBSCRIPTION_KEY', description='Bing Search subscription key.')
config.add('web_search_bing_endpoint', str, '', 'WEB_SEARCH_BING_ENDPOINT', description='Bing Search endpoint URL.')
config.add('web_search_bocha_api_key', str, '', 'WEB_SEARCH_BOCHA_API_KEY', description='Bocha search API key.')
config.add('web_search_bocha_base_url', str, 'https://api.bochaai.com', 'WEB_SEARCH_BOCHA_BASE_URL', description='Bocha search base URL.')
config.add('arxiv_search_timeout', int, 15, 'ARXIV_SEARCH_TIMEOUT', description='Arxiv search timeout in seconds.')
config.add('max_retries', int, 20, 'MAX_RETRIES', description='Max retries for agentic function call loop.')
config.add('agentic_workspace', str, './workspace', 'AGENTIC_WORKSPACE', description='Workspace directory for agentic tools.')
config.add('agentic_keep_full_turns', int, 3, 'AGENTIC_KEEP_FULL_TURNS', description='Number of full turns retained in agentic history.')
config.add('agentic_stream_chunk_size', int, 24, 'AGENTIC_STREAM_CHUNK_SIZE', description='Fallback chunk size for final streamed agentic text.')
config.add('memory_review_interval', int, 1, 'MEMORY_REVIEW_INTERVAL', description='Memory review trigger interval (turns).')
config.add('skill_review_interval', int, 5, 'SKILL_REVIEW_INTERVAL', description='Skill review trigger interval (turns).')
config.add('review_max_retries', int, 5, 'REVIEW_MAX_RETRIES', description='Max retries for background review agent.')
config.add('skill_review_debug', bool, False, 'SKILL_REVIEW_DEBUG', description='Enable skill review debug logging.')
config.add('review_debug', bool, False, 'REVIEW_DEBUG', description='Enable review debug logging.')

# ---------------------------------------------------------------------------
# Parsing
# ---------------------------------------------------------------------------
config.add('milvus_uri', str, None, 'MILVUS_URI', description='Milvus vector store URI (required).')
config.add('ocr_server_type', str, 'none', 'OCR_SERVER_TYPE', description='OCR server type (none/mineru/paddleocr).')
config.add('ocr_server_url', str, 'http://localhost:8000', 'OCR_SERVER_URL', description='OCR server URL.')
config.add('mineru_upload_mode', str, None, 'MINERU_UPLOAD_MODE', description='MinerU upload mode override (auto-detected if unset).')
config.add('mineru_backend', str, 'pipeline', 'MINERU_BACKEND', description='MinerU processing backend.')
config.add('mineru_server_port', int, 8000, 'MINERU_SERVER_PORT', description='MinerU server port.')
config.add('ocr_cache_dir', str, os.path.join(config['shared_upload_dir'], '.image_cache'), 'OCR_CACHE_DIR',
           description='OCR cache root for parsed results and images.')
config.add('document_processor_url', str, 'http://localhost:8000', 'DOCUMENT_PROCESSOR_URL', description='Document processor service URL.')
config.add('algo_server_port', int, 8000, 'ALGO_SERVER_PORT', description='Algorithm server port.')
config.add('document_server_port', int, 8000, 'DOCUMENT_SERVER_PORT', description='Document server port (fallback for algo_server_port).')
config.add('startup_retry_interval', str, '2', 'STARTUP_RETRY_INTERVAL', description='Startup retry interval in seconds.')
config.add('startup_timeout', str, '0', 'STARTUP_TIMEOUT', description='Startup wait timeout in seconds (0 = no timeout).')
config.add('reset_algo_on_startup', bool, False, 'RESET_ALGO_ON_STARTUP', description='Drop all vector/segment data and algorithm registration on startup, then rebuild from scratch.')
config.add('rag_image_path_prefix', str, '/mnt/lustre/share_data/mineru/images/', 'RAG_IMAGE_PATH_PREFIX', description='Image path prefix for RAG documents.')
config.add('ocr_patch_applied', bool, False, 'OCR_PATCH_APPLIED', description='Whether the OCR service patch has been applied.')
config.add('ocr_service_variant', str, 'online', 'OCR_SERVICE_VARIANT', description='OCR service variant (online/offline).')

# ---------------------------------------------------------------------------
# Processor
# ---------------------------------------------------------------------------
config.add('database_url', str, None, 'DATABASE_URL', description='Shared PostgreSQL URL (required for document processor).')
config.add('document_worker_port', int, 8001, 'DOCUMENT_WORKER_PORT', description='Document processor worker port.')
config.add('document_worker_num_workers', int, 1, 'DOCUMENT_WORKER_NUM_WORKERS', description='Number of document processor workers.')
# float values stored as str; consumers call float(config['...'])
config.add('document_worker_lease_duration', str, '300.0', 'DOCUMENT_WORKER_LEASE_DURATION', description='Worker lease duration in seconds.')
config.add('document_worker_lease_renew_interval', str, '60.0', 'DOCUMENT_WORKER_LEASE_RENEW_INTERVAL', description='Worker lease renew interval in seconds.')
config.add('document_worker_high_priority_task_types', str, None, 'DOCUMENT_WORKER_HIGH_PRIORITY_TASK_TYPES', description='Comma-separated high-priority task types.')
config.add('document_worker_high_priority_only', bool, False, 'DOCUMENT_WORKER_HIGH_PRIORITY_ONLY', description='Process only high-priority tasks.')
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
config.add('evo_code_skip_permissions', bool, True, 'EVO_CODE_SKIP_PERMISSIONS', description='Evo code skip permissions.')
config.add('evo_apply_test_command', str, 'bash tests/run-all.sh', 'EVO_APPLY_TEST_COMMAND', description='Evo apply test command.')
config.add('evo_apply_min_action_confidence', str, '0.5', 'EVO_APPLY_MIN_ACTION_CONFIDENCE', description='Evo apply minimum action confidence.')
config.add('evo_apply_min_action_validity', str, '0.5', 'EVO_APPLY_MIN_ACTION_VALIDITY', description='Evo apply minimum action validity.')
config.add('evo_llm_role', str, 'evo_llm', 'EVO_LLM_ROLE', description='Evo LLM AutoModel role.')
config.add('evo_auto_user_role', str, 'evo_llm', 'EVO_AUTO_USER_ROLE', description='Evo auto-user AutoModel role.')
config.add('evo_data_dir', str, None, 'EVO_DATA_DIR', description='Evo static data directory.')
config.add('evo_base_dir', str, None, 'EVO_BASE_DIR', description='Evo runtime storage directory.')
config.add('evo_code_map', str, None, 'EVO_CODE_MAP', description='Evo code map path.')
config.add('evo_chat_source', str, None, 'EVO_CHAT_SOURCE', description='Evo chat source directory.')

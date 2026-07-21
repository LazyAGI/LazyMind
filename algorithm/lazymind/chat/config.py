from lazymind.config import config

MOUNT_BASE_DIR: str = config['mount_base_dir']
SENSITIVE_WORDS_PATH: str = config['sensitive_words_path']

LAZYMIND_LLM_PRIORITY: int = config['llm_priority']

MAX_CONCURRENCY: int = config['max_concurrency']
RAG_MODE: bool = config['rag_mode']
SENSITIVE_FILTER_RESPONSE_TEXT = 'Sorry, I have not learned how to answer this question yet. If you have other questions, I am happy to help.'  # noqa: E501

IMAGE_EXTENSIONS = ('.png', '.jpg', '.jpeg')
CHAT_DOCUMENT_EXTENSIONS = ('.pdf', '.doc', '.docx', '.pptx')
# Keep this list aligned with frontend allowedTextTypes and core common textFileExtensions.
CHAT_TEXT_EXTENSIONS = (
    '.txt', '.md', '.markdown', '.csv', '.tsv', '.json', '.jsonl', '.ndjson',
    '.xml', '.yaml', '.yml', '.toml', '.ini', '.cfg', '.conf', '.log', '.sql',
    '.html', '.htm', '.css', '.scss', '.sass', '.less',
    '.py', '.pyi', '.js', '.jsx', '.mjs', '.cjs', '.ts', '.tsx',
    '.java', '.c', '.h', '.cc', '.cpp', '.cxx', '.hpp', '.cs',
    '.go', '.rs', '.rb', '.php', '.swift', '.kt', '.kts', '.scala',
    '.sh', '.bash', '.zsh', '.fish', '.ps1', '.bat', '.cmd',
    '.vue', '.svelte', '.tex', '.rst', '.properties', '.env',
    '.gradle', '.groovy', '.lua', '.r', '.dart', '.ex', '.exs', '.erl', '.hrl',
    '.clj', '.cljs', '.edn', '.fs', '.fsx', '.vb', '.asm', '.s',
)
CHAT_ATTACHMENT_EXTENSIONS = IMAGE_EXTENSIONS + CHAT_DOCUMENT_EXTENSIONS + CHAT_TEXT_EXTENSIONS
DEFAULT_TMP_BLOCK_TOPK = 20

DEFAULT_CHAT_DATASET: str = config['default_chat_dataset']

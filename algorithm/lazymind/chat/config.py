from lazymind.config import config

MOUNT_BASE_DIR: str = config['mount_base_dir']
SENSITIVE_WORDS_PATH: str = config['sensitive_words_path']

LAZYMIND_LLM_PRIORITY: int = config['llm_priority']

MAX_CONCURRENCY: int = config['max_concurrency']
RAG_MODE: bool = config['rag_mode']
SENSITIVE_FILTER_RESPONSE_TEXT = 'Sorry, I have not learned how to answer this question yet. If you have other questions, I am happy to help.'  # noqa: E501

IMAGE_EXTENSIONS = ('.png', '.jpg', '.jpeg')
DEFAULT_TMP_BLOCK_TOPK = 20

DEFAULT_CHAT_DATASET: str = config['default_chat_dataset']

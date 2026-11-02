"""Executable adapters, independent of provider catalog availability.

Mirror Core's runtimeModelTypesBySource and the frontend modelCapabilities map.
The registry contract tests check every declared type against the pinned LazyLLM
adapters; compatible HTTP shapes alone must not produce a successful probe for a
type that cannot be used by the application.
"""
import re

RUNTIME_TYPES_BY_SOURCE = {
    'aiping': {'llm', 'vlm', 'embed', 'rerank', 'text2image'},
    'claude': {'llm', 'vlm'},
    'deepseek': {'llm', 'vlm'},
    'doubao': {'llm', 'vlm', 'embed', 'multimodal_embedding', 'text2image', 'image_editing', 'text2video'},
    'glm': {'llm', 'vlm', 'embed', 'rerank', 'text2image', 'text2video', 'tts', 'stt'},
    'kimi': {'llm', 'vlm'},
    'minimax': {'llm', 'vlm', 'text2image', 'tts'},
    'openai': {'llm', 'vlm', 'embed', 'multimodal_embedding', 'rerank'},
    'openrouter': {'llm', 'vlm', 'embed', 'text2image', 'text2video', 'tts', 'stt'},
    'ppio': {'llm', 'vlm'},
    'qwen': {'llm', 'vlm', 'embed', 'multimodal_embedding', 'rerank', 'text2image', 'image_editing',
             'text2video', 'tts', 'stt'},
    'sensenova': {'llm', 'vlm', 'embed', 'text2image', 'image_editing'},
    'siliconflow': {'llm', 'vlm', 'embed', 'multimodal_embedding', 'rerank', 'text2image', 'image_editing',
                    'text2video', 'tts', 'stt'},
}

SOURCE_ALIASES = {
    'anthropic': 'claude', 'bigmodel': 'glm', 'moonshot': 'kimi', 'tongyiqianwen': 'qwen',
    'volcanoark': 'doubao', 'volcengine': 'doubao', 'zhipu': 'glm', 'zhipuai': 'glm',
    'vllm': 'openai', 'lmdeploy': 'openai',
}


def runtime_source(source: str) -> str:
    source = re.sub('[^a-z0-9]', '', source.lower())
    return SOURCE_ALIASES.get(source, source)


def supports_runtime_type(source: str, kind: str) -> bool:
    return kind in RUNTIME_TYPES_BY_SOURCE.get(runtime_source(source), set())

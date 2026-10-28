"""Request-scoped text embedding options without changing persisted document text."""
from copy import copy
import hashlib
import json
from typing import Any

import lazyllm
from lazyllm.module import ModuleBase

_OPTIONS_KEY = 'lazymind_embedding_options'
_PREFIX_LIMIT = 2048
_BATCH_LIMIT = 2048


class _EmbeddingQuery(str):
    """An in-process query marker; its string value remains the original query."""


def embedding_query(value: str) -> str:
    return _EmbeddingQuery(value)


def validate_embedding_options(value: Any) -> dict:
    if value is None:
        return {}
    if not isinstance(value, dict) or set(value) - {'query_prefix', 'passage_prefix', 'batch_size'}:
        raise ValueError('invalid embedding options')
    options = dict(value)
    for key in ('query_prefix', 'passage_prefix'):
        prefix = options.get(key, '')
        if not isinstance(prefix, str) or len(prefix) > _PREFIX_LIMIT:
            raise ValueError('embedding prefixes must contain at most 2048 characters')
    batch_size = options.get('batch_size')
    if batch_size is not None and (type(batch_size) is not int or not 1 <= batch_size <= _BATCH_LIMIT):
        raise ValueError('embedding batch size must be an integer between 1 and 2048')
    return options


def configure_embedding_options(model_config: dict | None) -> None:
    """Retain only validated embedding metadata in the current LazyLLM session."""
    options = {}
    for role, config in (model_config or {}).items():
        if not isinstance(config, dict) or 'embedding' not in config:
            continue
        value = validate_embedding_options(config['embedding'])
        kind = str(config.get('type') or ('embed' if role == 'embed_main' else '')).strip().lower()
        if value and kind not in {'embed', 'embedding'}:
            raise ValueError('embedding options require a text embedding model')
        options[role] = value
    lazyllm.globals[_OPTIONS_KEY] = options


def embedding_passage_prefix_bytes(role: str = 'embed_main') -> int:
    prefix = (lazyllm.globals.get(_OPTIONS_KEY) or {}).get(role, {}).get('passage_prefix', '')
    return len(prefix.encode('utf-8'))


def embedding_configuration_key(role: str = 'embed_main') -> str:
    """Invalidate temporary vectors for embedding identity changes, not batching."""
    configs = lazyllm.globals['config'].get('dynamic_model_configs') or {}
    route = (configs.get(role) or {}).get('embed') or {}
    options = (lazyllm.globals.get(_OPTIONS_KEY) or {}).get(role, {})
    identity = [route.get(key, '') for key in ('source', 'model', 'url')]
    identity.extend(options.get(key, '') for key in ('query_prefix', 'passage_prefix'))
    return hashlib.sha256(json.dumps(identity, ensure_ascii=False).encode('utf-8')).hexdigest()


class EmbeddingModel(ModuleBase):
    """Apply prefixes at inference and isolate mutable supplier batch settings."""

    def __init__(self, model, *, role: str):
        super().__init__(name=f'{role}_embedding_options')
        self.model = model
        self.role = role

    def _embedding_settings(self):
        return (lazyllm.globals.get(_OPTIONS_KEY) or {}).get(self.role, {})

    @property
    def batch_size(self):
        return self._embedding_settings().get('batch_size') or getattr(self.model, 'batch_size', 1)

    def forward(self, value, **kwargs):
        options = self._embedding_settings()
        prefix = options.get('query_prefix' if isinstance(value, _EmbeddingQuery) else 'passage_prefix', '')
        if isinstance(value, str):
            value = prefix + str(value)
        elif isinstance(value, list) and prefix:
            if any(not isinstance(item, str) for item in value):
                raise ValueError('text embedding prefixes require text input')
            value = [prefix + item for item in value]
        model = self.model
        if options.get('batch_size') is not None:
            # Dynamic routers reset their supplier cache on copy. Batch changes
            # therefore reach the new supplier without mutating another request.
            model = copy(model)
            model.batch_size = options['batch_size']
        return model(value, **kwargs)


def wrap_embedding_model(model, *, role: str):
    return model if isinstance(model, EmbeddingModel) else EmbeddingModel(model, role=role)


def mark_embedding_query_arguments(args, kwargs):
    """Mark only retrieve's query, leaving lazily indexed passages untouched."""
    if not args or args[0] != 'retrieve':
        return args, kwargs
    if 'query' in kwargs:
        kwargs = dict(kwargs, query=embedding_query(kwargs['query']))
    elif len(args) > 1:
        args = (args[0], embedding_query(args[1]), *args[2:])
    return args, kwargs

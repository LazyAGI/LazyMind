"""Fingerprint the two real text-embedding paths without exposing vectors."""
import hashlib
import math
import struct

import lazyllm

from lazymind.embedding_options import embedding_query, wrap_embedding_model
from lazymind.model_config import get_dynamic_role_slot_map, inject_model_config

from .contracts import ProbeInput
from .profiles import normalize_type
from .runtime_support import supports_runtime_type

_SAMPLE = 'lazymind'
_MAX_DIMENSIONS = 65536


def _fingerprint(vector) -> tuple[int, str]:
    if not isinstance(vector, list) or not 0 < len(vector) <= _MAX_DIMENSIONS:
        raise ValueError('invalid embedding vector')
    encoded = bytearray(struct.pack('<I', len(vector)))
    for value in vector:
        if isinstance(value, bool) or not isinstance(value, (int, float)) or not math.isfinite(value):
            raise ValueError('invalid embedding coordinate')
        try:
            encoded.extend(struct.pack('<f', value))
        except (OverflowError, struct.error) as exc:
            raise ValueError('invalid embedding coordinate') from exc
    return len(vector), hashlib.sha256(encoded).hexdigest()


def sign_embedding_model(request: ProbeInput) -> dict:
    """Use the same LazyLLM model and prefix wrapper as document indexing/search."""
    if normalize_type(request.model_type) != 'embed' or not supports_runtime_type(request.source, 'embed'):
        return {'status': 'unsupported', 'code': 'unsupported_type', 'model_type': 'embed'}
    # A static embed_main ignores the injected candidate and would fingerprint
    # the deployment default instead. Such a result cannot authorize reuse.
    try:
        dynamic_embed = get_dynamic_role_slot_map().get('embed_main') == 'embed'
    except Exception:
        dynamic_embed = False
    if not dynamic_embed:
        return {'status': 'unsupported', 'code': 'unsupported_provider', 'model_type': 'embed'}
    if not request.model.strip() or not request.url.strip():
        return {'status': 'failed', 'code': 'invalid_request', 'model_type': 'embed'}
    config = {
        'type': 'embed',
        'source': request.source,
        'model': request.model,
        'base_url': request.url,
        'api_key': request.api_key,
        'skip_auth': request.skip_auth,
    }
    if request.embedding:
        config['embedding'] = request.embedding
    if request.request_advanced:
        config['request_advanced'] = request.request_advanced
    try:
        with lazyllm.new_session():
            inject_model_config({'embed_main': config})
            model = wrap_embedding_model(lazyllm.AutoModel(model='embed_main'), role='embed_main')
            passage_dimensions, passage_signature = _fingerprint(model(_SAMPLE))
            query_dimensions, query_signature = _fingerprint(model(embedding_query(_SAMPLE)))
        if passage_dimensions != query_dimensions:
            raise ValueError('embedding dimensions differ')
    except (ValueError, TypeError, OverflowError):
        return {'status': 'failed', 'code': 'invalid_response', 'model_type': 'embed'}
    except Exception:
        # Provider exceptions may contain credentials, URLs, or response bodies.
        return {'status': 'failed', 'code': 'connection_error', 'model_type': 'embed'}
    return {
        'status': 'ready', 'code': 'ok', 'model_type': 'embed',
        'dimensions': passage_dimensions,
        'passage_signature': passage_signature,
        'query_signature': query_signature,
    }

"""Write retained segment indexes using their existing metadata representation.

Older LazyLLM indexes stored metadata as objects; newer defaults use JSON text.
Changing an embedding model must not require deleting those parsed segments.
"""
from contextvars import ContextVar
import json

_mapping = ContextVar('lazymind_segment_mapping', default=None)


def encode_metadata(segment, properties):
    result = dict(segment)
    for field in ('meta', 'global_meta', 'image_keys'):
        if field not in result:
            continue
        spec = properties.get(field, {})
        kind = spec.get('type')
        value = result[field]
        if (kind in ('object', 'nested') or 'properties' in spec) and isinstance(value, str):
            result[field] = json.loads(value)
        elif kind in ('text', 'keyword') and isinstance(value, (dict, list)):
            result[field] = json.dumps(value, ensure_ascii=False)
    return result


def install():
    from lazyllm.tools.rag.store.segment.opensearch_store import OpenSearchStore
    if getattr(OpenSearchStore, '_lazymind_mapping_aware', False):
        return
    original_upsert = OpenSearchStore.upsert
    original_serialize = OpenSearchStore._serialize_node

    def upsert(self, collection_name, data):
        if not data:
            return original_upsert(self, collection_name, data)
        self._ensure_index(collection_name)
        mapping = self._client.indices.get_mapping(index=collection_name)
        properties = mapping[collection_name]['mappings'].get('properties', {})
        token = _mapping.set(properties)
        try:
            return original_upsert(self, collection_name, data)
        finally:
            _mapping.reset(token)

    def serialize(self, segment):
        return encode_metadata(original_serialize(self, segment), (_mapping.get() or {}))

    OpenSearchStore.upsert = upsert
    OpenSearchStore._serialize_node = serialize
    OpenSearchStore._lazymind_mapping_aware = True

import json
from lazymind.parsing.service.opensearch_compat import encode_metadata


def test_existing_object_mapping_accepts_json_metadata_without_changing_source():
    source = {'meta': '{"page":1}', 'global_meta': '{"doc_id":"paper"}'}
    result = encode_metadata(source, {
        'meta': {'type': 'object'}, 'global_meta': {'properties': {'doc_id': {'type': 'keyword'}}}})
    assert result == {'meta': {'page': 1}, 'global_meta': {'doc_id': 'paper'}}
    assert isinstance(source['meta'], str)


def test_new_text_mapping_keeps_json_representation():
    result = encode_metadata({'meta': {'page': 1}, 'image_keys': ['a']}, {
        'meta': {'type': 'text'}, 'image_keys': {'type': 'keyword'}})
    assert json.loads(result['meta']) == {'page': 1}
    assert json.loads(result['image_keys']) == ['a']


def test_retained_json_metadata_is_not_left_double_encoded():
    value = json.dumps(json.dumps({'page': 1}))
    assert encode_metadata({'meta': value}, {'meta': {'properties': {}}})['meta'] == {'page': 1}


def test_disabled_object_mapping_still_requires_an_object():
    assert encode_metadata({'meta': '{"page":1}'}, {'meta': {'enabled': False}})['meta'] == {'page': 1}


def test_compatibility_wraps_actual_bulk_payload(monkeypatch):
    from types import SimpleNamespace
    from lazyllm.tools.rag.store.segment.opensearch_store import OpenSearchStore, BUILDIN_GLOBAL_META_DESC
    from lazymind.parsing.service.opensearch_compat import install
    install()
    store = object.__new__(OpenSearchStore)
    store._primary_key = 'uid'
    store._global_metadata_desc = BUILDIN_GLOBAL_META_DESC
    requests = []
    store._client = SimpleNamespace(
        indices=SimpleNamespace(get_mapping=lambda **kwargs: {'roots': {'mappings': {'properties': {
            'meta': {'properties': {'page': {'type': 'long'}}},
            'global_meta': {'type': 'object'}, 'image_keys': {'type': 'text'}}}}}),
        bulk=lambda **kwargs: requests.append(kwargs) or {'errors': False},
    )
    monkeypatch.setattr(store, '_ensure_index', lambda _: None)
    assert store.upsert('roots', [{'uid': 'test', 'meta': '{"page":1}', 'global_meta': {'doc_id': 'test'}}])
    assert requests[0]['body'][1]['meta'] == {'page': 1}
    assert requests[0]['body'][1]['global_meta'] == {'doc_id': 'test'}

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

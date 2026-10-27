import json

from lazymind.chat.engine.tools.artifact_output import artifact_output


def test_binary_removed_without_mutating_internal_transport():
    raw = {'artifacts': [{'id': 'file-1', 'value': {
        'content_base64': 'A' * 500000, 'filename': 'presentation.pptx',
        'size': 375000, 'url': '/files/presentation.pptx',
    }}]}
    result = artifact_output(raw)
    assert len(json.dumps(result)) < 200
    assert result['artifacts'][0]['value']['url'] == '/files/presentation.pptx'
    assert len(raw['artifacts'][0]['value']['content_base64']) == 500000


def test_nested_json_and_data_urls():
    value = {'value': json.dumps({'contentBase64': 'AAAA', 'revision': 2}),
             'image': 'data:image/png;base64,AAAA', 'notes': '保留原始素材与出处'}
    result = artifact_output(value)
    assert json.loads(result['value']) == {'revision': 2}
    assert 'AAAA' not in json.dumps(result)
    assert result['notes'] == value['notes']

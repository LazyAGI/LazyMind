from lazymind.chat.workflow.artifacts import compact_artifact_for_prompt


def test_binary_artifact_prompt_projection_omits_embedded_data():
    result = compact_artifact_for_prompt({
        'slot': 'images',
        'content_type': 'image',
        'value': {
            'path': 'data:image/png;base64,abc123',
            'filename': 'chart.png',
        },
    })

    assert result['value']['filename'] == 'chart.png'
    assert result['value']['path'] == {
        'embedded_data_omitted': True,
        'encoded_chars': len('data:image/png;base64,abc123'),
    }

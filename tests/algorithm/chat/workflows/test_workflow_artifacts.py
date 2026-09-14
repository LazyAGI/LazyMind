from lazymind.chat.workflow.artifacts import artifact_inventory


def test_artifact_inventory_omits_all_bodies():
    result = artifact_inventory({'artifacts': [{
        'artifact_id': 'artifact-1',
        'slot': 'draft',
        'content_type': 'text',
        'value': 'large document body',
    }]})

    assert result['artifacts'] == [{
        'artifact_id': 'artifact-1', 'slot': 'draft', 'content_type': 'text',
    }]
    assert result['content_omitted'] is True

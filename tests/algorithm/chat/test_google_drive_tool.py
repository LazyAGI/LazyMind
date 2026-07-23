from lazymind.chat.service.component.tool_registry import DEFAULT_TOOLS, _extract_methods


def test_google_drive_tool_group_exposes_search_and_find():
    group = next(item for item in DEFAULT_TOOLS if item.name == 'cloud_files')
    drive = next(
        item for item in group.tool['tools'] if item.__class__.__name__ == 'GoogleDriveFS'
    )

    assert drive.__public_apis__ == ['search', 'find', 'read', 'read_file']
    assert [method['name'] for method in _extract_methods(drive)] == [
        'search',
        'find',
        'read',
        'read_file',
    ]

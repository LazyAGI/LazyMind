from lazymind.chat.service.component.tool_registry import DEFAULT_TOOLS


def test_cloud_files_nests_supplier_filesystems():
    group = next(item for item in DEFAULT_TOOLS if item.name == 'cloud_files')
    nested = group.tool['tools']

    assert [type(item).__name__ for item in nested] == [
        'FeishuWikiFS',
        'NotionFS',
        'GoogleDriveFS',
    ]
    assert nested[0].__public_apis__
    assert 'search' in nested[0].__public_apis__
    assert 'find' in nested[1].__public_apis__
    assert nested[2].__public_apis__ == ['search', 'find', 'read', 'read_file']


def test_lazymind_does_not_register_a_duplicate_cloud_search_group():
    names = {item.name for item in DEFAULT_TOOLS}

    assert 'online_search' not in names
    assert not {'feishu', 'notion', 'google_drive'} & names

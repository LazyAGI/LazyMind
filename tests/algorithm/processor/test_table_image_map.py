import re

from lazymind.processor.engine.table_image_map import (
    merge_table_image_maps,
    normalize_table_image_map,
    serialize_table_image_map,
)


def test_normalize_table_image_map_handles_empty_values():
    assert normalize_table_image_map(None) == []
    assert normalize_table_image_map('') == []
    assert normalize_table_image_map([]) == []


def test_normalize_table_image_map_accepts_legacy_dict():
    result = normalize_table_image_map({'表格': '![图](table.png)'})

    assert result == [
        {'content': '表格', 'image': '![图](table.png)'}
    ]
    image_urls = re.findall(r'!\[[^\]]*\]\(([^)]+)\)', result[0]['image'])
    assert image_urls == ['table.png']


def test_normalize_table_image_map_accepts_json_string_and_bytes():
    raw = '[{"content": "表格", "image": "![图](table.png)"}]'

    expected = [{'content': '表格', 'image': '![图](table.png)'}]
    assert normalize_table_image_map(raw) == expected
    assert normalize_table_image_map(raw.encode('utf-8')) == expected
    assert isinstance(normalize_table_image_map(raw), list)
    assert all(isinstance(item, dict) for item in normalize_table_image_map(raw))


def test_normalize_table_image_map_skips_invalid_list_items():
    value = [
        {'content': 'keep', 'image': 'image'},
        {'content': '', 'image': 'missing-content'},
        {'content': 'missing-image'},
        'not-a-dict',
    ]

    assert normalize_table_image_map(value) == [{'content': 'keep', 'image': 'image'}]


def test_normalize_table_image_map_returns_empty_for_invalid_json_or_type():
    assert normalize_table_image_map('{bad json') == []
    assert normalize_table_image_map(123) == []


def test_merge_table_image_maps_keeps_last_image_for_duplicate_content():
    merged = merge_table_image_maps(
        {'a': 'old-a', 'b': 'b'},
        [{'content': 'a', 'image': 'new-a'}],
    )

    assert merged == [{'content': 'a', 'image': 'new-a'}, {'content': 'b', 'image': 'b'}]


def test_serialize_table_image_map_returns_json_or_none():
    serialized = serialize_table_image_map({'表格': '![图](table.png)'})

    assert serialized == '[{"content": "表格", "image": "![图](table.png)"}]'
    assert serialize_table_image_map({}) is None


def test_table_image_map_stringifies_content_and_image_values():
    value = [{'content': 123, 'image': 456}]

    assert normalize_table_image_map(value) == [{'content': '123', 'image': '456'}]

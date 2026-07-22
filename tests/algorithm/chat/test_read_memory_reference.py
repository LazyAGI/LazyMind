from __future__ import annotations

from unittest.mock import patch

import pytest

from lazymind.chat.engine.tools.memory import (
    MAX_REFERENCE_READ_COUNT,
    MAX_REFERENCE_READ_LINES,
    MemoryTools,
    truncate_reference_content,
)
from lazymind.common.memory.errors import MemoryNotFoundError, MemoryStoreError
from lazymind.common.memory.paths import REFERENCE_ROOT


def test_truncate_reference_content_by_line_count():
    content = '\n'.join(f'line {idx}' for idx in range(10)) + '\n'
    truncated, was_truncated = truncate_reference_content(content, max_lines=3, max_chars=10_000)
    assert was_truncated is True
    assert truncated.splitlines() == ['line 0', 'line 1', 'line 2']


def test_truncate_reference_content_by_char_count():
    long_line = 'x' * 100
    content = f'{long_line}\nshort\n'
    truncated, was_truncated = truncate_reference_content(content, max_lines=100, max_chars=50)
    assert was_truncated is True
    assert len(truncated) == 50


def test_truncate_reference_content_keeps_short_text():
    content = 'short reference\n'
    truncated, was_truncated = truncate_reference_content(content)
    assert was_truncated is False
    assert truncated == content


def test_read_memory_reference_returns_section_content():
    ref = 'references/response.md#pref-response-technical-detail'
    tools = MemoryTools()
    with patch('lazymind.chat.engine.tools.memory.MemoryRemoteStore') as store_cls:
        store_cls.return_value.store.read_reference.return_value = (
            '## Pref Response Technical Detail\n'
            'Explain motivations and tradeoffs.\n'
        )
        payload = tools.read_memory_reference(ref)

    assert payload['success'] is True
    item = payload['result']['items'][0]
    assert item['ref'] == ref
    assert item['path'] == f'{REFERENCE_ROOT}/response.md'
    assert item['anchor'] == 'pref-response-technical-detail'
    assert 'Explain motivations and tradeoffs.' in item['content']
    assert payload['result']['ref_count'] == 1
    assert payload['result']['truncated_count'] == 0


def test_read_memory_reference_reads_multiple_refs():
    refs = [
        'references/response.md#tone',
        'references/response.md#structure',
    ]
    tools = MemoryTools()
    with patch('lazymind.chat.engine.tools.memory.MemoryRemoteStore') as store_cls:
        store_cls.return_value.store.read_reference.side_effect = [
            '## Tone\nconcise\n',
            '## Structure\nconclusion-first\n',
        ]
        payload = tools.read_memory_reference(refs)

    assert payload['success'] is True
    assert payload['result']['ref_count'] == 2
    assert [item['ref'] for item in payload['result']['items']] == refs
    assert store_cls.return_value.store.read_reference.call_count == 2


def test_read_memory_reference_appends_warning_when_truncated():
    long_content = '\n'.join(f'line {idx}' for idx in range(MAX_REFERENCE_READ_LINES + 5)) + '\n'
    tools = MemoryTools()
    with patch('lazymind.chat.engine.tools.memory.MemoryRemoteStore') as store_cls:
        store_cls.return_value.store.read_reference.return_value = long_content
        payload = tools.read_memory_reference('references/response.md')

    item = payload['result']['items'][0]
    assert item['truncated'] is True
    assert 'WARNING: This reference content was truncated' in item['content']
    assert payload['result']['truncated_count'] == 1


def test_read_memory_reference_rejects_empty_refs():
    payload = MemoryTools().read_memory_reference('   ')
    assert payload['success'] is False
    assert 'refs is required' in payload['error']['reason']


def test_read_memory_reference_rejects_too_many_refs():
    refs = [f'references/topic-{idx}.md' for idx in range(MAX_REFERENCE_READ_COUNT + 1)]
    payload = MemoryTools().read_memory_reference(refs)
    assert payload['success'] is False
    assert 'At most' in payload['error']['reason']


def test_read_memory_reference_rejects_invalid_ref():
    payload = MemoryTools().read_memory_reference('memory/users/profile.md')
    assert payload['success'] is False
    assert 'Invalid ref' in payload['error']['reason']


def test_read_memory_reference_handles_not_found():
    tools = MemoryTools()
    with patch('lazymind.chat.engine.tools.memory.MemoryRemoteStore') as store_cls:
        store_cls.return_value.store.read_reference.side_effect = MemoryNotFoundError('missing')
        payload = tools.read_memory_reference('references/missing.md')

    assert payload['success'] is False
    assert 'Reference not found' in payload['error']['reason']


def test_read_memory_reference_handles_store_error():
    tools = MemoryTools()
    with patch('lazymind.chat.engine.tools.memory.MemoryRemoteStore') as store_cls:
        store_cls.return_value.store.read_reference.side_effect = MemoryStoreError('remote down')
        payload = tools.read_memory_reference('references/response.md')

    assert payload['success'] is False
    assert 'Failed to read' in payload['error']['reason']


@pytest.mark.parametrize('ref', [
    'references/response.md',
    'references/response.md#tone',
])
def test_read_memory_reference_accepts_single_ref_string(ref):
    tools = MemoryTools()
    with patch('lazymind.chat.engine.tools.memory.MemoryRemoteStore') as store_cls:
        store_cls.return_value.store.read_reference.return_value = 'content'
        payload = tools.read_memory_reference(ref)

    assert payload['success'] is True
    store_cls.return_value.store.read_reference.assert_called_once_with(ref)


def test_read_memory_reference_deduplicates_repeated_refs():
    ref = 'references/response.md'
    tools = MemoryTools()
    with patch('lazymind.chat.engine.tools.memory.MemoryRemoteStore') as store_cls:
        store_cls.return_value.store.read_reference.return_value = 'content'
        payload = tools.read_memory_reference([ref, ref])

    assert payload['success'] is True
    assert payload['result']['ref_count'] == 1
    store_cls.return_value.store.read_reference.assert_called_once_with(ref)

from __future__ import annotations

from unittest.mock import patch

import lazyllm
import pytest

from lazymind.chat.engine.tools.memory import (
    MAX_REFERENCE_READ_COUNT,
    MemoryTools,
)
from lazymind.common.memory.paths import REFERENCE_ROOT


def test_read_memory_reference_returns_section_content():
    ref = 'references/response.md#pref-response-technical-detail'
    tools = MemoryTools()
    with patch('lazymind.chat.engine.tools.memory.MemoryStore') as store_cls:
        store_cls.return_value.read_reference.return_value = (
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


def test_read_memory_reference_reads_multiple_refs():
    refs = [
        'references/response.md#tone',
        'references/response.md#structure',
    ]
    tools = MemoryTools()
    with patch('lazymind.chat.engine.tools.memory.MemoryStore') as store_cls:
        store_cls.return_value.read_reference.side_effect = [
            '## Tone\nconcise\n',
            '## Structure\nconclusion-first\n',
        ]
        payload = tools.read_memory_reference(refs)

    assert payload['success'] is True
    assert payload['result']['ref_count'] == 2
    assert [item['ref'] for item in payload['result']['items']] == refs
    assert store_cls.return_value.read_reference.call_count == 2


def test_read_memory_reference_returns_large_reference_in_full():
    long_content = '\n'.join(f'line {idx}' for idx in range(500)) + '\n'
    tools = MemoryTools()
    with patch('lazymind.chat.engine.tools.memory.MemoryStore') as store_cls:
        store_cls.return_value.read_reference.return_value = long_content
        payload = tools.read_memory_reference('references/response.md')

    item = payload['result']['items'][0]
    assert item['content'] == long_content
    assert item['content_length'] == len(long_content)


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
    payload = MemoryTools().read_memory_reference('memory/users/profile.yaml')
    assert payload['success'] is False
    assert 'Invalid ref' in payload['error']['reason']


def test_read_memory_reference_handles_not_found():
    tools = MemoryTools()
    with patch('lazymind.chat.engine.tools.memory.MemoryStore') as store_cls:
        store_cls.return_value.read_reference.side_effect = FileNotFoundError('missing')
        payload = tools.read_memory_reference('references/missing.md')

    assert payload['success'] is False
    assert 'Reference not found' in payload['error']['reason']


def test_read_memory_reference_handles_store_error():
    tools = MemoryTools()
    with patch('lazymind.chat.engine.tools.memory.MemoryStore') as store_cls:
        store_cls.return_value.read_reference.side_effect = RuntimeError('remote down')
        payload = tools.read_memory_reference('references/response.md')

    assert payload['success'] is False
    assert 'Failed to read' in payload['error']['reason']


@pytest.mark.parametrize('ref', [
    'references/response.md',
    'references/response.md#tone',
])
def test_read_memory_reference_accepts_single_ref_string(ref):
    tools = MemoryTools()
    with patch('lazymind.chat.engine.tools.memory.MemoryStore') as store_cls:
        store_cls.return_value.read_reference.return_value = 'content'
        payload = tools.read_memory_reference(ref)

    assert payload['success'] is True
    store_cls.return_value.read_reference.assert_called_once_with(ref)


def test_read_memory_reference_deduplicates_repeated_refs():
    ref = 'references/response.md'
    tools = MemoryTools()
    with patch('lazymind.chat.engine.tools.memory.MemoryStore') as store_cls:
        store_cls.return_value.read_reference.return_value = 'content'
        payload = tools.read_memory_reference([ref, ref])

    assert payload['success'] is True
    assert payload['result']['ref_count'] == 1
    store_cls.return_value.read_reference.assert_called_once_with(ref)


def test_read_memory_reference_records_read_result_without_mutation():
    ledger = []
    lazyllm.globals['agentic_config'] = {'memory_tool_results': ledger}
    with patch('lazymind.chat.engine.tools.memory.MemoryStore') as store_cls:
        store_cls.return_value.read_reference.return_value = 'content'
        payload = MemoryTools().read_memory_reference('references/response.md')

    assert payload['success'] is True
    assert ledger[-1]['tool'] == 'read_memory_reference'
    assert ledger[-1]['success'] is True
    assert ledger[-1]['mutation'] is False

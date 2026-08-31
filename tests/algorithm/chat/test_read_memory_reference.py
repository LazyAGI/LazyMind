from __future__ import annotations

from unittest.mock import patch

import lazyllm
import pytest
from lazyllm.tools.agent import ToolExecutionError

from lazymind.chat.engine.tools.memory import (
    MAX_REFERENCE_READ_COUNT,
    MemoryTools,
)
from lazymind.common.memory.paths import (
    PREFERENCE_PATH,
    PROFILE_PATH,
    SOUL_PATH,
)


@pytest.mark.parametrize(
    ('target', 'reader_name', 'path'),
    [
        ('soul', 'read_soul', SOUL_PATH),
        ('profile', 'read_profile', PROFILE_PATH),
        ('preference', 'read_preference', PREFERENCE_PATH),
    ],
)
def test_read_memory_returns_complete_target_document(target, reader_name, path):
    tools = MemoryTools()
    with patch('lazymind.chat.engine.tools.memory.MemoryStore') as store_cls:
        getattr(store_cls.return_value, reader_name).return_value = 'dynamic:\n  value: current\n'
        payload = tools.read_memory(target)

    assert payload == {
        'target': target,
        'path': path,
        'content': 'dynamic:\n  value: current\n',
        'content_length': 26,
    }


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

    assert payload['ref_count'] == 2
    assert [item['ref'] for item in payload['items']] == refs
    assert store_cls.return_value.read_reference.call_count == 2


def test_read_memory_reference_rejects_too_many_refs():
    refs = [f'references/topic-{idx}.md' for idx in range(MAX_REFERENCE_READ_COUNT + 1)]
    with pytest.raises(ToolExecutionError, match='At most'):
        MemoryTools().read_memory_reference(refs)


def test_read_memory_reference_handles_not_found():
    tools = MemoryTools()
    with patch('lazymind.chat.engine.tools.memory.MemoryStore') as store_cls:
        store_cls.return_value.read_reference.side_effect = FileNotFoundError('missing')
        with pytest.raises(ToolExecutionError, match='Reference not found'):
            tools.read_memory_reference('references/missing.md')


def test_read_memory_reference_records_read_result_without_mutation():
    ledger = []
    lazyllm.globals['agentic_config'] = {'memory_operation_ledger': ledger}
    with patch('lazymind.chat.engine.tools.memory.MemoryStore') as store_cls:
        store_cls.return_value.read_reference.return_value = 'content'
        payload = MemoryTools().read_memory_reference('references/response.md')

    assert payload['items'][0]['content'] == 'content'
    assert ledger[-1]['operation'] == 'read_memory_reference'
    assert ledger[-1]['status'] == 'succeeded'
    assert ledger[-1]['mutation'] == 'none'

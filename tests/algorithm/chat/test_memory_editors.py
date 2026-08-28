from __future__ import annotations

import json
from typing import Any, Dict, List, Optional
from unittest.mock import patch

import lazyllm
import pytest
import yaml
from lazyllm.tools import ToolManager
from lazyllm.tools.agent import ToolExecutionError

from lazymind.chat.engine.tools.memory import MemoryTools
from lazymind.common.memory.editors import (
    apply_memory_operations,
    delete_preference_entry,
    validate_preference_name,
)
from lazymind.common.memory.exceptions import PreferenceCapacityExceededError
from lazymind.common.memory.paths import (
    PREFERENCE_PATH,
    PROFILE_PATH,
    SOUL_PATH,
    build_reference_path,
    normalize_memory_path,
)
from lazymind.common.memory.store import MemoryStore
from lazymind.common.memory.validation import PreferenceItem, append_preference_item
from lazymind.config import config as _cfg

SAMPLE_SOUL = (
    'schema_version: 2\n'
    'identity:\n'
    '  name: "LazyMind"\n'
    '  role: "personal_ai_assistant"\n'
    '  description: "面向研究、分析和复杂任务的个人智能助手"\n'
    'mission:\n'
    '  primary_goal: "帮助用户准确、高效地思考并完成工作"\n'
    '  success_definition: "输出可靠、可执行且符合用户真实目标的结果"\n'
    'interaction:\n'
    '  default_relationship_mode: "collaborator"\n'
    '  default_tone: "warm_direct"\n'
    '  default_initiative_level: "proactive"\n'
    '  default_challenge_level: "constructive"\n'
    '  default_decision_mode: "recommend_then_confirm"\n'
    'epistemic:\n'
    '  uncertainty_style: "explicit"\n'
    '  verification_mode: "when_material"\n'
)

SAMPLE_PROFILE = (
    'schema_version: 2\n'
    'identity:\n'
    '  preferred_name: null\n'
    '  aliases: []\n'
    'locale:\n'
    '  languages: ["zh-CN"]\n'
    '  residence: "CN"\n'
    'professional:\n'
    '  occupations: []\n'
    '  organizations: []\n'
    '  industries: []\n'
    '  expertise_domains: []\n'
)

SAMPLE_PREFERENCE = 'preferences: []\n'


class FakeRemoteFS:
    def __init__(self, files: Optional[Dict[str, str]] = None):
        self.files: Dict[str, str] = dict(files or {})
        self.dirs: set[str] = set()
        self.fail_write_paths: set[str] = set()
        self.fail_rm_paths: set[str] = set()

    def exists(self, path: str) -> bool:
        normalized = normalize_memory_path(path)
        return normalized in self.files or normalized in self.dirs

    def ls(self, path: str, detail: bool = True) -> List[Any]:
        normalized = normalize_memory_path(path)
        prefix = normalized.rstrip('/') + '/'
        items = []
        seen = set()
        for file_path in sorted(self.files):
            if not file_path.startswith(prefix):
                continue
            rest = file_path[len(prefix):]
            name = rest.split('/', 1)[0]
            full = f'{normalized}/{name}'
            if full in seen:
                continue
            seen.add(full)
            if '/' in rest:
                items.append({'name': full, 'path': full, 'type': 'dir'})
            else:
                items.append({
                    'name': full,
                    'path': full,
                    'type': 'file',
                    'size': len(self.files[file_path]),
                })
        return items

    def makedirs(self, path: str, exist_ok: bool = True) -> None:
        self.dirs.add(normalize_memory_path(path))

    def write(self, path: str, content: str, content_type: str = 'text/plain; charset=utf-8') -> None:
        normalized = normalize_memory_path(path)
        if normalized in self.fail_write_paths:
            raise RuntimeError(f'write failed: {normalized}')
        self.files[normalized] = content
        parent = normalized.rsplit('/', 1)[0]
        self.dirs.add(parent)

    def rm(self, path: str) -> None:
        normalized = normalize_memory_path(path)
        if normalized in self.fail_rm_paths:
            raise RuntimeError(f'delete failed: {normalized}')
        self.files.pop(normalized, None)

    def open(self, path: str, mode: str = 'rb', **kwargs):
        normalized = normalize_memory_path(path)
        if normalized not in self.files:
            raise FileNotFoundError(normalized)

        class _Handle:
            def __init__(self, text: str):
                self._text = text

            def read(self):
                return self._text

            def __enter__(self):
                return self

            def __exit__(self, *args):
                return False

        return _Handle(self.files[normalized])


class FakeMemoryStore(MemoryStore):
    def __init__(self, fs: FakeRemoteFS):
        super().__init__(fs)


def _tools_with_store(fs: FakeRemoteFS):
    store = FakeMemoryStore(fs)
    return MemoryTools(), store


def _reset_ledger() -> list[dict[str, Any]]:
    ledger: list[dict[str, Any]] = []
    lazyllm.globals['agentic_config'] = {
        'memory_operation_ledger': ledger,
        'memory_source_kind': 'chat_explicit',
        'conversation_id': 'conversation-1',
    }
    return ledger


def _tool_call(name: str, arguments: dict[str, Any]) -> dict[str, Any]:
    return {
        'id': 'call-memory-test',
        'type': 'function',
        'function': {
            'name': name,
            'arguments': json.dumps(arguments, ensure_ascii=False),
        },
    }


def test_common_memory_editors_use_plain_values_and_exceptions():
    assert validate_preference_name(' pref.response.concise ') == 'pref.response.concise'
    edited = apply_memory_operations(
        SAMPLE_PROFILE,
        [{'op': 'add', 'path': 'identity.aliases', 'value': 'Neo'}],
        label='profile',
    )

    assert 'ok' not in edited
    assert edited['operations'] == [
        {'op': 'add', 'path': 'identity.aliases', 'value': 'Neo'},
    ]
    with pytest.raises(ValueError, match='preference name must match'):
        validate_preference_name('invalid')
    with pytest.raises(FileNotFoundError, match='not found'):
        delete_preference_entry(SAMPLE_PREFERENCE, name='pref.missing')


def test_soul_editor_updates_supported_field():
    ledger = _reset_ledger()
    fs = FakeRemoteFS({
        SOUL_PATH: SAMPLE_SOUL,
        PROFILE_PATH: SAMPLE_PROFILE,
        PREFERENCE_PATH: SAMPLE_PREFERENCE,
    })
    tools, store = _tools_with_store(fs)
    with patch('lazymind.chat.engine.tools.memory.MemoryStore', lambda *args, **kwargs: store):
        payload = tools.soul_editor([
            {'op': 'set', 'path': 'identity.description', 'value': '更直接的助手'},
        ])

    assert payload['status'] == 'applied'
    assert '更直接的助手' in fs.files[SOUL_PATH]
    assert ledger[-1]['operation'] == 'soul_editor'
    assert ledger[-1]['mutation'] == 'applied'
    assert ledger[-1]['status'] == 'succeeded'
    assert ledger[-1]['result']['status'] == 'applied'


def test_soul_editor_rejects_missing_field():
    ledger = _reset_ledger()
    fs = FakeRemoteFS({
        SOUL_PATH: SAMPLE_SOUL,
        PROFILE_PATH: SAMPLE_PROFILE,
        PREFERENCE_PATH: SAMPLE_PREFERENCE,
    })
    tools, store = _tools_with_store(fs)
    with patch('lazymind.chat.engine.tools.memory.MemoryStore', lambda *args, **kwargs: store):
        with pytest.raises(ToolExecutionError, match='unsupported soul operation path'):
            tools.soul_editor([
                {'op': 'set', 'path': 'identity.email', 'value': 'x@y.com'},
            ])
    assert ledger[-1]['status'] == 'failed'
    assert ledger[-1]['mutation'] == 'none'
    assert ledger[-1]['error_code'] == 'invalid_arguments'


def test_profile_editor_updates_list_field():
    ledger = _reset_ledger()
    fs = FakeRemoteFS({
        SOUL_PATH: SAMPLE_SOUL,
        PROFILE_PATH: SAMPLE_PROFILE,
        PREFERENCE_PATH: SAMPLE_PREFERENCE,
    })
    tools, store = _tools_with_store(fs)
    with patch('lazymind.chat.engine.tools.memory.MemoryStore', lambda *args, **kwargs: store):
        payload = tools.profile_editor([
            {'op': 'add', 'path': 'locale.languages', 'value': 'en-US'},
            {'op': 'add', 'path': 'locale.languages', 'value': 'en-US'},
            {'op': 'set', 'path': 'locale.residence', 'value': '中国'},
            {'op': 'add', 'path': 'professional.industries', 'value': 'software'},
        ])

    assert payload['status'] == 'applied'
    assert payload['change_count'] == 4
    assert fs.files[PROFILE_PATH].count('en-US') == 1
    assert 'residence:' in fs.files[PROFILE_PATH]
    assert '中国' in fs.files[PROFILE_PATH]
    assert 'software' in fs.files[PROFILE_PATH]
    assert ledger[-1]['mutation'] == 'applied'


def test_profile_editor_discovers_fields_from_loaded_document():
    _reset_ledger()
    dynamic_profile = (
        'schema_version: 2\n'
        'personal:\n'
        '  nickname: Neo\n'
        '  interests: [AI]\n'
        '  headline: null\n'
    )
    fs = FakeRemoteFS({
        SOUL_PATH: SAMPLE_SOUL,
        PROFILE_PATH: dynamic_profile,
        PREFERENCE_PATH: SAMPLE_PREFERENCE,
    })
    tools, store = _tools_with_store(fs)

    with patch('lazymind.chat.engine.tools.memory.MemoryStore', lambda *args, **kwargs: store):
        payload = tools.profile_editor([
            {'op': 'set', 'path': 'personal.nickname', 'value': 'Trinity'},
            {'op': 'add', 'path': 'personal.interests', 'value': 'Agents'},
            {'op': 'set', 'path': 'personal.headline', 'value': 'Engineer'},
        ])

    assert payload['status'] == 'applied'
    stored = fs.files[PROFILE_PATH]
    assert 'nickname: Trinity' in stored
    assert 'interests:' in stored
    assert '- AI' in stored
    assert '- Agents' in stored
    assert 'headline: Engineer' in stored


def test_soul_editor_uses_the_same_dynamic_field_contract():
    ledger = _reset_ledger()
    dynamic_soul = (
        'schema_version: 2\n'
        'custom:\n'
        '  title: Assistant\n'
        '  capabilities: [Research]\n'
        '  note: null\n'
    )
    fs = FakeRemoteFS({
        SOUL_PATH: dynamic_soul,
        PROFILE_PATH: SAMPLE_PROFILE,
        PREFERENCE_PATH: SAMPLE_PREFERENCE,
    })
    tools, store = _tools_with_store(fs)

    with patch('lazymind.chat.engine.tools.memory.MemoryStore', lambda *args, **kwargs: store):
        payload = tools.soul_editor([
            {'op': 'clear', 'path': 'custom.title'},
            {'op': 'add', 'path': 'custom.capabilities', 'value': 'Planning'},
            {'op': 'set', 'path': 'custom.note', 'value': 'Direct'},
        ])

    stored = yaml.safe_load(fs.files[SOUL_PATH])
    assert stored['custom'] == {
        'title': '',
        'capabilities': ['Research', 'Planning'],
        'note': 'Direct',
    }
    assert 'schema_version' not in payload['content']
    assert 'schema_version' not in str(ledger)


def test_read_memory_returns_only_visible_document_content():
    ledger = _reset_ledger()
    fs = FakeRemoteFS({
        SOUL_PATH: SAMPLE_SOUL,
        PROFILE_PATH: SAMPLE_PROFILE,
        PREFERENCE_PATH: SAMPLE_PREFERENCE,
    })
    tools, store = _tools_with_store(fs)

    with patch('lazymind.chat.engine.tools.memory.MemoryStore', lambda *args, **kwargs: store):
        payload = tools.read_memory('soul')

    assert 'schema_version' not in payload['content']
    assert 'identity:' in payload['content']
    assert 'schema_version' not in str(ledger)


def test_profile_editor_preserves_loaded_field_types():
    _reset_ledger()
    dynamic_profile = (
        'schema_version: 2\n'
        'personal:\n'
        '  nickname: Neo\n'
        '  interests: [AI]\n'
        '  headline: null\n'
        '  secondary_headline: null\n'
    )
    fs = FakeRemoteFS({
        SOUL_PATH: SAMPLE_SOUL,
        PROFILE_PATH: dynamic_profile,
        PREFERENCE_PATH: SAMPLE_PREFERENCE,
    })
    tools, store = _tools_with_store(fs)

    with patch('lazymind.chat.engine.tools.memory.MemoryStore', lambda *args, **kwargs: store):
        applied = tools.profile_editor([
            {'op': 'clear', 'path': 'personal.nickname'},
            {'op': 'clear', 'path': 'personal.interests'},
            {'op': 'remove', 'path': 'personal.interests', 'value': 'missing'},
            {'op': 'set', 'path': 'personal.headline', 'value': 'Engineer'},
            {'op': 'clear', 'path': 'personal.secondary_headline'},
        ])

        assert applied['status'] == 'applied'
        document = yaml.safe_load(fs.files[PROFILE_PATH])
        assert document['personal'] == {
            'nickname': '',
            'interests': [],
            'headline': 'Engineer',
            'secondary_headline': None,
        }
        unchanged = fs.files[PROFILE_PATH]

        for operation in (
            {'op': 'add', 'path': 'personal.nickname', 'value': 'N'},
            {'op': 'set', 'path': 'personal.interests', 'value': 'Agents'},
            {'op': 'clear', 'path': 'personal.headline', 'value': 'unexpected'},
            {'op': 'set', 'path': 'personal.unknown', 'value': 'value'},
            {'op': 'set', 'path': 'personal', 'value': 'value'},
            {'op': 'set', 'path': 'schema_version', 'value': '3'},
            {'op': 'set', 'path': 'schema_version.nested', 'value': '3'},
        ):
            with pytest.raises(ToolExecutionError) as captured:
                tools.profile_editor([operation])
            assert 'schema_version' not in str(captured.value)
            assert fs.files[PROFILE_PATH] == unchanged

        with pytest.raises(ToolExecutionError):
            tools.profile_editor([
                {'op': 'set', 'path': 'personal.nickname', 'value': 'Morpheus'},
                {'op': 'add', 'path': 'personal.headline', 'value': 'Invalid'},
            ])
        assert fs.files[PROFILE_PATH] == unchanged


def test_preference_editor_add_and_delete():
    ledger = _reset_ledger()
    fs = FakeRemoteFS({
        SOUL_PATH: SAMPLE_SOUL,
        PROFILE_PATH: SAMPLE_PROFILE,
        PREFERENCE_PATH: SAMPLE_PREFERENCE,
    })
    tools, store = _tools_with_store(fs)
    with patch('lazymind.chat.engine.tools.memory.MemoryStore', lambda *args, **kwargs: store):
        added = tools.preference_editor(
            'add',
            name='pref.response.concise',
            summary='回答要简洁',
            scenario='日常问答',
            details='先给结论，再按需补充背景。',
            reason='用户明确要求简洁回答',
        )
        assert added['status'] == 'applied'
        assert 'pref.response.concise' in fs.files[PREFERENCE_PATH]
        reference_path = build_reference_path('response-concise')
        assert reference_path in fs.files
        reference = fs.files[reference_path]
        assert 'name: pref.response.concise' in reference
        assert 'summary: 回答要简洁' in reference
        assert 'kind: chat_explicit' in reference
        assert 'conversation_id: conversation-1' in reference
        assert '## Application Scenarios' in reference
        assert '## Preference Details' in reference
        assert '## Reason' in reference

        deleted = tools.preference_editor('delete', name='pref.response.concise')
        assert deleted['status'] == 'applied'
        assert 'pref.response.concise' not in fs.files[PREFERENCE_PATH]
        assert build_reference_path('response-concise') not in fs.files
    assert [entry['operation'] for entry in ledger] == [
        'preference_editor',
        'preference_editor',
    ]
    assert all(entry['mutation'] == 'applied' for entry in ledger)


def test_preference_editor_reports_capacity_rejection_without_eviction():
    ledger = _reset_ledger()
    fs = FakeRemoteFS({
        SOUL_PATH: SAMPLE_SOUL,
        PROFILE_PATH: SAMPLE_PROFILE,
        PREFERENCE_PATH: SAMPLE_PREFERENCE,
    })
    tools, store = _tools_with_store(fs)
    capacity_error = PreferenceCapacityExceededError(
        current_items=20,
        attempted_items=21,
        max_items=20,
    )

    with (
        patch(
            'lazymind.chat.engine.tools.memory.MemoryStore',
            lambda *args, **kwargs: store,
        ),
        patch.object(store, 'add_preference_with_reference', side_effect=capacity_error),
        pytest.raises(ToolExecutionError) as captured,
    ):
        tools.preference_editor(
            'add',
            name='pref.development.windows',
            summary='Use Windows for development testing',
            scenario='Development and testing tasks',
            details='Prefer Windows-compatible commands and paths.',
            reason='The user explicitly requested this preference.',
        )

    message = str(captured.value)
    assert 'capacity is full (20/20)' in message
    assert 'new preference was not saved' in message
    assert 'No existing preference was deleted, overwritten, or reordered' in message
    assert ledger[-1]['status'] == 'failed'
    assert ledger[-1]['mutation'] == 'none'
    assert ledger[-1]['error_code'] == 'capacity_exceeded'
    assert ledger[-1]['result'] == {
        'current_items': 20,
        'attempted_items': 21,
        'max_items': 20,
    }


def test_memory_tools_use_only_tool_manager_envelope():
    _reset_ledger()
    fs = FakeRemoteFS({
        SOUL_PATH: SAMPLE_SOUL,
        PROFILE_PATH: SAMPLE_PROFILE,
        PREFERENCE_PATH: SAMPLE_PREFERENCE,
    })
    tools, store = _tools_with_store(fs)
    manager = ToolManager([tools])

    with patch('lazymind.chat.engine.tools.memory.MemoryStore', lambda *args, **kwargs: store):
        success = manager(_tool_call(
            'MemoryTools_profile_editor',
            {'operations': [
                {'op': 'add', 'path': 'identity.aliases', 'value': 'Neo'},
            ]},
        ))[0]
        fs.files[PREFERENCE_PATH] = append_preference_item(
            SAMPLE_PREFERENCE,
            PreferenceItem(
                name='pref.existing',
                summary='Existing preference',
                ref='references/existing.md',
                created_at='2026-08-27T00:00:00+00:00',
                updated_at='2026-08-27T00:00:00+00:00',
            ),
        )
        with _cfg.temp('preference_index_max_items', 1):
            failure = manager(_tool_call(
                'MemoryTools_preference_editor',
                {
                    'op': 'add',
                    'name': 'pref.response.concise',
                    'summary': '回答要简洁',
                    'scenario': '日常问答',
                    'details': '先给结论，再按需补充背景。',
                    'reason': '用户明确要求',
                },
            ))[0]

    assert set(success) == {'ok', 'value'}
    assert success['ok'] is True
    assert 'ok' not in success['value']
    assert set(failure) == {'ok', 'value'}
    assert failure['ok'] is False
    assert isinstance(failure['value'], str)
    assert 'new preference was not saved' in failure['value']


def test_preference_editor_records_partial_apply():
    ledger = _reset_ledger()
    fs = FakeRemoteFS({PREFERENCE_PATH: SAMPLE_PREFERENCE})
    reference_path = build_reference_path('response-concise')
    fs.fail_write_paths.add(PREFERENCE_PATH)
    fs.fail_rm_paths.add(reference_path)
    tools, store = _tools_with_store(fs)

    with (
        patch('lazymind.chat.engine.tools.memory.MemoryStore', lambda *args, **kwargs: store),
        pytest.raises(ToolExecutionError, match='partially applied'),
    ):
        tools.preference_editor(
            'add',
            name='pref.response.concise',
            summary='回答要简洁',
            scenario='日常问答',
            details='先给结论，再按需补充背景。',
            reason='用户明确要求',
        )

    assert ledger[-1]['status'] == 'failed'
    assert ledger[-1]['mutation'] == 'applied'
    assert ledger[-1]['error_code'] == 'partial_failure'
    assert ledger[-1]['result']['applied'] == ['reference']
    assert ledger[-1]['result']['failed'] == [
        'preference_index',
        'reference_cleanup',
    ]


def test_profile_editor_maps_remotefs_failure_to_storage_failed():
    ledger = _reset_ledger()
    fs = FakeRemoteFS({PROFILE_PATH: SAMPLE_PROFILE})
    fs.fail_write_paths.add(PROFILE_PATH)
    tools, store = _tools_with_store(fs)

    with (
        patch('lazymind.chat.engine.tools.memory.MemoryStore', lambda *args, **kwargs: store),
        pytest.raises(ToolExecutionError, match='Memory storage operation failed'),
    ):
        tools.profile_editor([
            {'op': 'add', 'path': 'identity.aliases', 'value': 'Neo'},
        ])

    assert ledger[-1]['status'] == 'failed'
    assert ledger[-1]['mutation'] == 'none'
    assert ledger[-1]['error_code'] == 'storage_failed'


def test_preference_editor_maps_missing_item_to_invalid_arguments():
    ledger = _reset_ledger()
    fs = FakeRemoteFS({PREFERENCE_PATH: SAMPLE_PREFERENCE})
    tools, store = _tools_with_store(fs)

    with (
        patch('lazymind.chat.engine.tools.memory.MemoryStore', lambda *args, **kwargs: store),
        pytest.raises(ToolExecutionError, match='not found'),
    ):
        tools.preference_editor('delete', name='pref.missing')

    assert ledger[-1]['mutation'] == 'none'
    assert ledger[-1]['error_code'] == 'invalid_arguments'


def test_preference_editor_requires_hidden_source_context_for_add():
    ledger: list[dict[str, Any]] = []
    lazyllm.globals['agentic_config'] = {'memory_operation_ledger': ledger}
    with pytest.raises(ToolExecutionError, match='memory_source_kind'):
        MemoryTools().preference_editor(
            'add',
            name='pref.response.concise',
            summary='回答要简洁',
            scenario='日常问答',
            details='先给结论，再按需补充背景。',
            reason='用户明确要求简洁回答',
        )

    assert ledger[-1]['mutation'] == 'none'
    assert ledger[-1]['error_code'] == 'missing_context'

from chat.tools import kb
from chat.tools import memory as memory_mod
from chat.tools import skill_manager as skill_manager_mod


def test_kb_tool_returns_error_result_for_invalid_arguments():
    result = kb.kb_get_window_nodes('', 1)

    assert result['success'] is False
    assert result['tool'] == 'kb_get_window_nodes'
    assert result['error']['type'] == 'ValueError'
    assert 'docid is required' in result['error']['detail']


def test_memory_tool_returns_error_result_for_unexpected_exception(monkeypatch):
    def raise_unexpected(_path, _payload):
        raise ValueError('backend payload is invalid')

    monkeypatch.setattr(memory_mod.lazyllm, 'globals', {'agentic_config': {'session_id': 'sid-1'}})
    monkeypatch.setattr(memory_mod, 'post_core_api', raise_unexpected)

    result = memory_mod.memory('memory', [{'title': 'pref', 'content': 'Remember the preference.'}])

    assert result['success'] is False
    assert result['tool'] == 'memory'
    assert result['error']['type'] == 'ValueError'
    assert 'backend payload is invalid' in result['error']['detail']


def test_skill_manage_returns_error_result_for_skill_index_exception(monkeypatch):
    def raise_unexpected(_base_dir):
        raise RuntimeError('skill index unavailable')

    monkeypatch.setattr(skill_manager_mod.lazyllm, 'globals', {'agentic_config': {'session_id': 'sid-1'}})
    monkeypatch.setattr(skill_manager_mod, 'list_all_skill_entries', raise_unexpected)

    result = skill_manager_mod.skill_manage(
        'existing',
        'modify',
        '',
        suggestions=[{'title': 'Update instructions', 'content': 'Tighten the wording.'}],
    )

    assert result['success'] is False
    assert result['tool'] == 'skill_manage'
    assert result['error']['type'] == 'RuntimeError'
    assert 'skill index unavailable' in result['error']['detail']

from lazymind.chat.engine.tools import kb
from lazymind.chat.engine.tools import skill_manager as skill_manager_mod


def test_kb_tool_returns_error_result_for_invalid_arguments(monkeypatch):
    monkeypatch.setattr(kb.lazyllm, 'globals', {'agentic_config': {'kb_api_key': 'test-key'}})
    result = kb.KBToolGroup().kb_get_window_nodes('', 1)

    assert result['success'] is False
    assert result['tool'] == 'kb_get_window_nodes'
    assert result['error']['type'] == 'ValueError'
    assert 'docid is required' in result['error']['detail']


def test_skill_editor_returns_error_result_for_skill_index_exception(monkeypatch):
    def raise_unexpected(_base_dir):
        raise RuntimeError('skill index unavailable')

    monkeypatch.setattr(skill_manager_mod.lazyllm, 'globals', {'agentic_config': {'session_id': 'sid-1'}})
    monkeypatch.setattr(skill_manager_mod, 'list_all_skill_entries', raise_unexpected)

    result = skill_manager_mod.skill_editor(
        'existing',
        'modify',
        '',
        suggestions=[{'title': 'Update instructions', 'content': 'Tighten the wording.'}],
    )

    assert result['success'] is False
    assert result['tool'] == 'skill_editor'
    assert result['error']['type'] == 'RuntimeError'
    assert 'skill index unavailable' in result['error']['detail']

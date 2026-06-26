import importlib

from lazymind.chat.engine.tools import kb
from lazyllm.tools.agent.toolsManager import MethodModuleTool

skill_editor_mod = importlib.import_module('lazymind.chat.engine.tools.skill_editor')


def test_kb_keyword_search_requires_explicit_target_in_schema():
    tool = MethodModuleTool(kb.KBToolGroup(), 'kb_keyword_search')

    required = set(tool.params_schema.model_json_schema().get('required', []))

    assert {'keyword', 'target'}.issubset(required)
    assert 'target_type' not in required
    assert 'docid' not in tool.params_schema.model_fields
    assert 'file_name' not in tool.params_schema.model_fields


def test_kb_keyword_search_maps_target_by_type(monkeypatch):
    calls = []

    class FakeDocument:
        def keyword_search(self, **kwargs):
            calls.append(kwargs)
            return []

    monkeypatch.setattr(kb.lazyllm, 'globals', {'agentic_config': {'filters': {'kb_id': 'kb-1'}}})
    monkeypatch.setattr(kb, 'DOCUMENT', FakeDocument())

    by_file = kb.KBToolGroup().kb_keyword_search('DeepSeek', 'report.pdf')
    by_docid = kb.KBToolGroup().kb_keyword_search('DeepSeek', 'doc-1', target_type='docid')

    assert by_file['success'] is True
    assert calls[0]['file_name'] == 'report.pdf'
    assert calls[0]['doc_id'] == ''
    assert by_docid['success'] is True
    assert calls[1]['file_name'] is None
    assert calls[1]['doc_id'] == 'doc-1'


def test_skill_editor_returns_error_result_for_skill_index_exception(monkeypatch):
    def raise_unexpected(_base_dir):
        raise RuntimeError('skill index unavailable')

    monkeypatch.setattr(skill_editor_mod.lazyllm, 'globals', {'agentic_config': {}})
    monkeypatch.setattr(skill_editor_mod, 'list_all_skill_entries', raise_unexpected)

    result = skill_editor_mod.skill_editor(
        'existing',
        'modify',
        'coding',
        operations=[{'op': 'replace_text', 'old': 'old', 'new': 'new'}],
    )

    assert result['success'] is False
    assert result['tool'] == 'skill_editor'
    assert result['error']['type'] == 'RuntimeError'
    assert 'skill index unavailable' in result['error']['detail']

import importlib

from lazymind.chat.engine.tools import kb
from lazyllm.tools.agent.toolsManager import MethodModuleTool

skill_editor_mod = importlib.import_module('lazymind.chat.engine.tools.skill_editor')


def test_kb_keyword_search_requires_explicit_target_in_schema():
    tool = MethodModuleTool(kb.KBToolkit(), 'kb_keyword_search')

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

    by_file = kb.KBToolkit().kb_keyword_search('DeepSeek', 'report.pdf')
    by_docid = kb.KBToolkit().kb_keyword_search('DeepSeek', 'doc-1', target_type='docid')

    assert by_file['success'] is True
    assert calls[0]['file_name'] == 'report.pdf'
    assert calls[0]['doc_id'] == ''
    assert by_docid['success'] is True
    assert calls[1]['file_name'] is None
    assert calls[1]['doc_id'] == 'doc-1'


def test_skill_editor_tool_group_exposes_action_specific_schemas():
    group = skill_editor_mod.SkillManagementToolkit()
    create_tool = MethodModuleTool(group, 'create_skill')
    install_tool = MethodModuleTool(group, 'install_skill')
    edit_tool = MethodModuleTool(group, 'edit_file')
    patch_tool = MethodModuleTool(group, 'patch_file')
    create_file_tool = MethodModuleTool(group, 'create_file')
    delete_tool = MethodModuleTool(group, 'delete_file')
    rename_tool = MethodModuleTool(group, 'rename_skill')
    remove_tool = MethodModuleTool(group, 'remove_skill')

    assert create_tool.name == 'SkillManagementToolkit_create_skill'
    assert patch_tool.name == 'SkillManagementToolkit_patch_file'
    assert 'modify_skill' not in group.__public_apis__
    create_fields = set(create_tool.params_schema.model_fields)
    install_fields = set(install_tool.params_schema.model_fields)
    editor_fields = {
        tool.name: set(tool.params_schema.model_fields)
        for tool in (
            edit_tool,
            patch_tool,
            create_file_tool,
            delete_tool,
            rename_tool,
            remove_tool,
        )
    }
    create_required = set(create_tool.params_schema.model_json_schema().get('required', []))
    patch_required = set(patch_tool.params_schema.model_json_schema().get('required', []))

    assert create_fields == {'name', 'content'}
    assert 'Single-segment skill name.' in create_tool.description
    assert install_fields == {'github_url'}
    assert editor_fields == {
        'SkillManagementToolkit_edit_file': {'name', 'path', 'content', 'reason'},
        'SkillManagementToolkit_patch_file': {
            'name', 'path', 'old_text', 'new_text', 'replace_all', 'reason',
        },
        'SkillManagementToolkit_create_file': {'name', 'path', 'content', 'reason'},
        'SkillManagementToolkit_delete_file': {'name', 'path', 'reason'},
        'SkillManagementToolkit_rename_skill': {'name', 'new_name'},
        'SkillManagementToolkit_remove_skill': {'name', 'reason'},
    }
    assert create_required == {'name', 'content'}
    assert {'name', 'path', 'old_text', 'new_text'}.issubset(patch_required)
    assert all('category' not in fields for fields in editor_fields.values())
    assert patch_tool.validate_parameters({
        'name': 'internal/web-research',
        'path': 'SKILL.md',
        'old_text': 'old',
        'new_text': 'new',
    })

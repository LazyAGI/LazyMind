import pytest
from lazyllm.tools.agent import ToolExecutionError

from lazymind.chat.service.chat_service import (
    _build_chat_artifact_tools,
    _build_chat_workspace_read_tools,
    _normalize_document_filter,
    _should_register_subagent_tools,
    _workflow_startup_clarification_available,
    _workflow_collects_knowledge_internally,
    _workflow_turn_is_bound,
)


def test_bound_workflow_keeps_only_read_only_workspace_tools():
    names = {tool.__name__ for tool in _build_chat_workspace_read_tools()}

    assert names == {'search_file_resource', 'read_file_resource'}
    assert 'save_chat_artifact' not in names
    assert 'write_file' not in names


def test_workflow_readers_reject_remote_skills_before_remote_or_local_access(monkeypatch):
    from lazymind.chat.engine.tools.file_resources import tools

    def unexpected(*args, **kwargs):
        pytest.fail('isolated remote Skill request reached a filesystem')

    for name in ('read_remote', 'grep_remote', '_resolve_text_target_for_tool'):
        monkeypatch.setattr(tools, name, unexpected)
    readers = {tool.__name__: tool for tool in _build_chat_workspace_read_tools()}
    with pytest.raises(ToolExecutionError, match='workflow isolation'):
        readers['read_file_resource']('remote://skills/external/example/SKILL.md')
    with pytest.raises(ToolExecutionError, match='workflow isolation'):
        readers['search_file_resource']('remote://skills/external/example', 'needle')


def test_workflow_readers_preserve_workspace_access(monkeypatch, tmp_path):
    from types import SimpleNamespace
    from lazymind.chat.engine.tools.file_resources import tools

    path = tmp_path / 'note.txt'
    path.write_text('needle', encoding='utf-8')
    monkeypatch.setattr(tools, '_resolve_text_target_for_tool', lambda *a, **kw: SimpleNamespace(
        path=str(path), workspace=str(tmp_path), display_name='note.txt', kind='workspace', file_id=None,
    ))
    readers = {tool.__name__: tool for tool in _build_chat_workspace_read_tools()}
    assert 'needle' in readers['read_file_resource']('note.txt')['text']
    assert readers['search_file_resource']('note.txt', 'needle')['matches']


def test_plain_chat_keeps_remote_capable_readers():
    from lazymind.chat.engine.tools.file_resources import tools

    registered = _build_chat_artifact_tools()
    assert tools.read_file_resource in registered
    assert tools.search_file_resource in registered
    assert tools.list_skill_files in registered


def test_workflow_toolmanager_uses_isolated_readers():
    import json
    from lazyllm.tools import ToolManager

    manager = ToolManager(_build_chat_workspace_read_tools())
    result = manager([{
        'id': 'isolated-read', 'type': 'function',
        'function': {'name': 'read_file_resource', 'arguments': json.dumps({
            'target': 'remote://skills/external/example/SKILL.md',
        })},
    }])[0]
    assert result['ok'] is False
    assert 'remote_skill_access_not_allowed' in str(result)


@pytest.mark.parametrize('with_snapshot', [False, True])
@pytest.mark.parametrize('name', ['read_file_resource', 'search_file_resource'])
def test_workflow_readers_through_local_authorization(monkeypatch, tmp_path, with_snapshot, name):
    from types import SimpleNamespace
    from lazyllm.tools import ToolManager
    from lazymind.chat.engine.agent_runtime.tool_call_guard import ToolExecutionMiddleware
    from lazymind.chat.engine.agent_runtime.workspace_policy import WorkspaceAuthorizationPolicy
    from lazymind.chat.engine.tools.workspace_context import WorkspaceContext
    from lazymind.chat.engine.tools.file_resources import tools
    from lazyllm.tools.agent import AuthorizationDecision, HostFileAccess

    path = tmp_path / 'note.txt'
    path.write_text('needle', encoding='utf-8')
    monkeypatch.setattr(tools, '_resolve_text_target_for_tool', lambda *a, **kw: SimpleNamespace(
        path=str(path), workspace=str(tmp_path), display_name='note.txt', kind='workspace', file_id=None,
    ))
    snapshot = {'workspace_id': 'test', 'root': str(tmp_path), 'permission_mode': 'always_ask'}
    permission = WorkspaceContext.from_snapshot(snapshot if with_snapshot else {})
    assert permission.local_runtime
    decisions = []
    original = WorkspaceAuthorizationPolicy.decide

    def decide(self, prepared):
        decision = original(self, prepared)
        decisions.append(decision)
        # Fail immediately instead of waiting for approval on a metadata regression.
        assert decision is AuthorizationDecision.ALLOW
        return decision

    monkeypatch.setattr(WorkspaceAuthorizationPolicy, 'decide', decide)
    manager = ToolManager(_build_chat_workspace_read_tools())
    metadata = manager.tools_info[name].runtime_metadata
    assert metadata.host_file_access is HostFileAccess.NONE
    assert metadata.exclusive is True
    middleware = ToolExecutionMiddleware(manager, workspace_permission=permission)
    args = {'target': 'note.txt'}
    if name == 'search_file_resource':
        args['pattern'] = 'needle'
    result = middleware.execute_with_records({
        'id': 'local-read', 'function': {'name': name, 'arguments': args},
    }).results[0]
    assert result['ok'], result
    assert 'needle' in str(result['value'])
    args = {**args, 'target': 'remote://skills/external/example/SKILL.md'}
    result = middleware.execute_with_records({
        'id': 'remote-read', 'function': {'name': name, 'arguments': args},
    }).results[0]
    assert not result['ok']
    assert 'remote_skill_access_not_allowed' in str(result)
    assert len(decisions) == 2


def test_public_document_filter_is_translated_to_rag_metadata_key():
    filters = {'kb_id': ['kb-1'], 'doc_id': ['doc-1']}

    _normalize_document_filter(filters)

    assert filters == {'kb_id': ['kb-1'], 'docid': 'doc-1'}


def test_selected_ppt_workflow_owns_knowledge_collection():
    assert _workflow_collects_knowledge_internally(
        None, ['builtin:deck-workflow'], [{
            'workflow_ref': 'builtin:deck-workflow',
            'runtime': {'collects_knowledge': True},
        }],
    )


def test_active_ppt_workflow_owns_knowledge_collection():
    assert _workflow_collects_knowledge_internally(
        {'workflow_id': 'deck-workflow', 'runtime': {'collects_knowledge': True}}, [],
    )


def test_unrelated_workflow_keeps_global_knowledge_tools():
    assert not _workflow_collects_knowledge_internally(
        {'workflow_ref': 'builtin:image-workflow'},
        ['builtin:image-workflow'],
    )


def test_completed_workflow_session_owns_mutation_tools():
    context = {'session_id': 'session-1', 'workflow_ref': 'builtin:ppt-workflow'}

    assert _workflow_turn_is_bound(context, [])
    assert not _should_register_subagent_tools(True, [], context)


def test_explicit_workflow_selection_owns_mutation_tools_before_session_exists():
    refs = ['builtin:ppt-workflow']

    assert _workflow_turn_is_bound(None, refs)
    assert not _should_register_subagent_tools(True, refs, None)


def test_plain_chat_keeps_generic_subagent_tools():
    assert not _workflow_turn_is_bound(None, [])
    assert _should_register_subagent_tools(True, [], None)


def test_selected_workflow_can_clarify_before_session_creation():
    runtime = {'clarification_fields': [{
        'id': 'topic', 'question': 'PPT 主题是什么？', 'type': 'text',
    }]}

    assert _workflow_startup_clarification_available(runtime, None)
    assert not _workflow_startup_clarification_available(
        runtime, {'session_id': 'session-1'},
    )


def test_discovery_mode_exposes_ask_for_declaratively_interactive_workflow():
    assert _workflow_startup_clarification_available(
        None,
        None,
        [{'runtime': {'clarification_fields': [{
            'id': 'topic', 'question': 'PPT 主题是什么？',
        }]}}],
        discovery_mode=True,
    )


def test_bound_unrelated_workflow_does_not_inherit_catalog_clarification():
    assert not _workflow_startup_clarification_available(
        None,
        None,
        [{'runtime': {'clarification_fields': [{
            'id': 'topic', 'question': 'PPT 主题是什么？',
        }]}}],
        discovery_mode=False,
    )

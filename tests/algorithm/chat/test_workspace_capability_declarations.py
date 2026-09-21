"""Workspace trust is declared by implementations, never by registry membership."""
import pytest

from lazyllm.tools.agent.toolsManager import ToolManager
from lazyllm.tools.agent.tool_runtime import HostFileAccess


def _assert_no_host_paths(tools):
    manager = ToolManager(tools)
    assert manager.tools_info
    assert {tool.runtime_metadata.host_file_access for tool in manager.tools_info.values()} == {HostFileAccess.NONE}


def test_default_internal_tool_implementations_declare_no_host_paths():
    from lazymind.chat.engine.tools import list_data_sources, url_fetch
    from lazymind.chat.engine.tools.calculator import calculator
    from lazymind.chat.engine.tools.vocab_learn import vocab_learn
    from lazymind.chat.engine.tools.ask_user import ask_user
    from lazymind.chat.engine.tools.lazy_kb import KBToolkit, kb_tmp_search
    from lazymind.chat.engine.tools.file_resources import tools as workspace
    from lazymind.chat.engine.tools import subagent_chat_tools as tasks
    _assert_no_host_paths([
        calculator, list_data_sources, url_fetch, vocab_learn, ask_user, KBToolkit(), kb_tmp_search,
        workspace.read_file_resource, workspace.search_file_resource,
        tasks.create_subagent, tasks.list_subagents,
        tasks.get_subagent_status, tasks.list_subagent_artifacts, tasks.get_subagent_artifacts,
    ])


def test_scoped_factories_preserve_declarations_when_registered():
    from lazymind.chat.engine.tools.file_resources.tools import build_resource_read_tools
    from lazymind.chat.engine.tools.session_env import build_session_env_tool
    from lazymind.chat.engine.tools.skill_listing import build_list_skills_tool
    from lazymind.chat.engine.tools.schedule import build_schedule_toolkit
    _assert_no_host_paths([
        *build_resource_read_tools(), build_session_env_tool({}, 'test'),
        build_list_skills_tool(['example']), *build_schedule_toolkit()['tools'],
    ])


def test_scoped_default_toolkits_declare_each_exposed_method():
    from lazymind.chat.service.component.tool_registry import DEFAULT_TOOLS, _registration_target
    for cfg in DEFAULT_TOOLS:
        if cfg.name in {'external_db', 'memory', 'skill_editor', 'mail'}:
            _assert_no_host_paths([_registration_target(cfg.tool)])


def test_unregistered_replacement_cannot_inherit_trust_from_public_name():
    from lazymind.chat.engine.tools.lazy_kb import KBToolkit
    toolkit = KBToolkit()

    def kb_search(query: str) -> str:
        """An unrelated replacement, deliberately lacking a capability declaration."""
        return query

    toolkit.kb_search = kb_search
    toolkit.__lazy_source__ = lambda: False
    manager = ToolManager([toolkit])
    replacement = next(tool for tool in manager.tools_info.values() if getattr(tool, '_method_name', '') == 'kb_search')
    assert replacement.runtime_metadata.host_file_access is HostFileAccess.UNDECLARED
    from lazyllm.tools.agent import AuthorizationDecision
    prepared = manager.prepare_tool_calls([
        {'id': 'unknown', 'type': 'function', 'function': {'name': replacement.name, 'arguments': {'query': 'x'}}},
    ], authorization_policy=lambda call: (AuthorizationDecision.DENY
                                          if call.host_file_access is HostFileAccess.UNDECLARED
                                          else AuthorizationDecision.ALLOW))
    result = manager.execute_prepared(prepared)
    assert result.records[0].prepared.authorization is AuthorizationDecision.DENY
    assert result.records[0].disposition.value == 'skipped'


def test_real_workflow_factories_declare_their_exposed_tools(monkeypatch):
    import lazyllm
    from lazymind.chat.engine.tools.intent_writer import build_intentwrite_tool
    from lazymind.chat.workflow import workflow_manager as workflows
    lazyllm.globals['agentic_config'] = lazyllm.globals.get('agentic_config') or {}
    monkeypatch.setitem(lazyllm.globals, 'agentic_config', {'user_id': 'u', 'conversation_id': 'c'})
    toolkit = workflows.HostWorkflowToolkit(workflows._client, origin_ref='c')
    activation = {'workflow_id': 'demo', 'workflow_ref': 'demo', 'tool_name': 'trigger_demo_workflow'}
    groups = [
        [build_intentwrite_tool(conversation_id='c', current_query='write')],
        [workflows._handoff_tool('session', 'write')],
        workflows._safe_session_tools(toolkit, 'session'),
        workflows._safe_authoring_tools(toolkit),
        workflows._workflow_trigger_tools([activation], set(), 'write', 'c'),
        workflows.resolve_workflow_injection(
            None, conversation_id='c', current_query='write', workflow_activations=[activation],
        ).tools,
    ]
    for tools in groups:
        _assert_no_host_paths(tools)


@pytest.mark.parametrize('dependency', ['toolkit', 'method', 'client', 'session', 'environment'])
def test_factory_injected_implementations_remain_undeclared(dependency):
    from lazymind.chat.engine.tools.session_env import build_session_env_tool
    from lazymind.chat.workflow import workflow_manager as workflows

    def unexpected(*args):
        pytest.fail('injected callback executed during declaration or validation')

    class OtherToolkit:
        get_workflow_state = staticmethod(unexpected)

    class OtherStore(dict):
        setdefault = unexpected

    toolkit = workflows.HostWorkflowToolkit(workflows._client)
    session = 'session'
    if dependency == 'toolkit':
        toolkit = OtherToolkit()
    elif dependency == 'method':
        toolkit.get_workflow_state = unexpected
    elif dependency == 'client':
        toolkit = workflows.HostWorkflowToolkit(unexpected)
    elif dependency == 'session':
        session = unexpected
    tool = (build_session_env_tool(OtherStore(), 'c') if dependency == 'environment'
            else workflows._safe_session_tools(toolkit, session)[0])
    manager = ToolManager([tool])
    assert next(iter(manager.tools_info.values())).runtime_metadata.host_file_access is HostFileAccess.UNDECLARED


def test_registered_cloud_and_search_suppliers_preserve_definition_metadata():
    from lazymind.chat.lazyllm_tool_docs import ensure_lazyllm_tool_docs
    from lazymind.chat.service.component.tool_registry import DEFAULT_TOOLS
    for cfg in DEFAULT_TOOLS:
        if cfg.name in {'cloud_files', 'web_search', 'academic_search', 'wikipedia'}:
            ensure_lazyllm_tool_docs([cfg.tool])
            _assert_no_host_paths([cfg.tool])


def test_cloud_local_upload_helpers_do_not_inherit_no_host_paths_capability():
    from lazyllm.tools.agent.tool_runtime import _get_tool_runtime_metadata
    from lazyllm.tools.fs.supplier.feishu import FeishuFS
    from lazyllm.tools.fs.supplier.notion import NotionFS
    for method in [FeishuFS.put_file, FeishuFS.get_file, NotionFS.put_file,
                   NotionFS.get_file, NotionFS.write_doc_blocks]:
        metadata = _get_tool_runtime_metadata(method)
        assert metadata is None or metadata.host_file_access is HostFileAccess.UNDECLARED


def test_every_default_exposed_tool_has_a_host_file_capability():
    from lazymind.chat.lazyllm_tool_docs import ensure_lazyllm_tool_docs
    from lazymind.chat.service.component.tool_registry import DEFAULT_TOOLS
    tools = [cfg.tool for cfg in DEFAULT_TOOLS if cfg.tool is not None]
    ensure_lazyllm_tool_docs(tools)
    manager = ToolManager(tools)
    missing = [name for name, tool in manager.tools_info.items()
               if tool.runtime_metadata.host_file_access is HostFileAccess.UNDECLARED]
    assert missing == []

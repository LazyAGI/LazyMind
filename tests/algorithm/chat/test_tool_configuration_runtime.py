import asyncio
import copy
import json
from unittest.mock import Mock

import pytest
import lazyllm
from lazyllm.tools.agent.toolsManager import ToolManager
from lazymind.chat.engine.agent_runtime.tool_configuration import ToolConfigurationRuntime


@pytest.fixture(autouse=True)
def isolated_runtime_state():
    old = lazyllm.locals['_lazyllm_agent'].copy()
    auth = {key: copy.deepcopy(lazyllm.globals.config[key])
            for key in ('dynamic_tool_auth', 'dynamic_fs_auth')}
    lazyllm.locals['_lazyllm_agent']['workspace'] = {}
    yield
    lazyllm.locals['_lazyllm_agent'] = old
    for key, value in auth.items():
        lazyllm.globals.config[key] = value


def lookup(query: str) -> str:
    '''Look up a document.

    Args:
        query (str): Search text.
    '''
    return query


def runtime():
    host = ToolConfigurationRuntime('u', 'c', 'h', 'r', loader=lambda _: [lookup])
    groups = asyncio.run(host.mcp_tools([{'service': 'mcp:one', 'label': 'Documents', 'status': 'needs_authorization'}]))
    manager = ToolManager(groups)
    host.bind(manager)
    action = {'id': 'a', 'version': 2, 'service': 'mcp:one', 'label': 'Documents', 'status': 'ready'}
    host.actions['mcp:one'] = action
    host.intents['mcp:one'] = {'mcp_one'}
    host._post = Mock(side_effect=lambda operation, **kwargs: {'actions': [action]} if operation == 'list' else
                      {'actions': [{'action': action, 'pending_delivery': True, 'mcp_config': {'id': 'one'}}]})
    return host, manager


def test_executor_dedup_preserves_unready_mcp_gateway_and_independent_tools():
    from lazyllm.tools.agent.toolsManager import ToolGroup
    from lazymind.chat.engine.agent_runtime.executor import _deduplicate_tools

    host, original = runtime()
    search = ToolGroup(tools=[other_lookup], name='search')
    manager = ToolManager(_deduplicate_tools([search, *original._tools, other_lookup]))
    host.bind(manager)
    host._post = Mock(return_value={'status': 'needs_authorization'})
    host.actions['mcp:one']['status'] = 'needs_authorization'
    names = {item['function']['name'] for item in manager.tools_description}
    assert {'get_search_methods', 'get_mcp_one_methods', 'other_lookup'} <= names
    result = manager([
        {'id': '1', 'function': {'name': 'get_mcp_one_methods', 'arguments': '{}'}},
        {'id': '2', 'function': {'name': 'other_lookup', 'arguments': '{"query":"independent"}'}},
    ])
    assert result[0]['needs_configuration'] is True
    assert result[1]['ok'] is True and result[1]['value'] == 'independent'


def test_refresh_catalog_notice_and_ack_follow_actual_model_request():
    host, manager = runtime()
    host.before_request()
    assert 'lookup' in manager.tools_info
    assert 'Connection is ready' in host.model_context()
    assert not any(call.args[0] == 'ack' for call in host._post.call_args_list)
    host.observe('history_ready')
    host.observe('turn_end')
    assert host._post.call_args.args == ('ack',)
    assert host._post.call_args.kwargs['version'] == 2


def test_failed_refresh_preserves_old_catalog_and_pending_delivery():
    host, manager = runtime()
    retrieval = manager.enable_tool_retrieval(groups={'mcp_one'}, required=[], estimate_tokens=lambda _: 1,
                                              threshold_tokens=100)
    retrieval.validate_load = Mock(side_effect=ValueError('budget'))
    original = manager.tools_description
    host.before_request()
    assert 'lookup' not in manager.tools_info
    assert 'budget' in host.model_context()
    host.observe('history_ready')
    host.observe('turn_end')
    assert not any(call.args[0] == 'ack' for call in host._post.call_args_list)
    assert manager.tools_description == original
    retrieval.validate_load = None
    host.before_request()
    assert 'lookup' in manager.tools_info


def test_repeated_ready_notice_does_not_reload_unloaded_retrieval_tools():
    host, manager = runtime()
    retrieval = manager.enable_tool_retrieval(groups={'mcp_one'}, required=[], estimate_tokens=lambda _: 1,
                                              threshold_tokens=100)
    host.before_request()
    assert 'lookup' in {d['function']['name'] for d in retrieval.descriptions()}
    retrieval.load([], ['lookup'])
    host.before_request()
    assert 'lookup' not in {d['function']['name'] for d in retrieval.descriptions()}
    assert 'Connection is ready' in host.model_context()


def test_missing_configuration_blocks_execution_without_blocking_independent_calls():
    host, manager = runtime()
    manager = ToolManager([*manager._tools, other_lookup])
    host.bind(manager)
    host._post = Mock(return_value={'status': 'needs_authorization'})
    host.actions['mcp:one']['status'] = 'needs_authorization'
    gateway = next(iter(manager.tools_info))
    result = manager([{'id': '1', 'function': {'name': gateway, 'arguments': '{}'}},
                      {'id': '2', 'function': {'name': 'other_lookup', 'arguments': '{"query":"independent"}'}}])
    assert result[0]['needs_configuration'] is True
    assert result[1]['ok'] is True and result[1]['value'] == 'independent'
    assert host.services['mcp:one']['status'] == 'needs_authorization'


def test_provider_selection_remains_pinned_after_earlier_provider_becomes_ready():
    ready = [False, True]
    manager = ToolManager([{'name': 'search', 'desc': 'search', 'pick_first_valid': True,
                           'discoverable': True, 'tools': [
                               (lookup, lambda: ready[0]), (other_lookup, lambda: ready[1])]}])
    lazyllm.locals['_lazyllm_agent']['workspace'] = {}
    assert list(manager.atomic_tool_catalog()) == ['other_lookup']
    ready[0] = True
    assert list(manager.atomic_tool_catalog()) == ['other_lookup']


def other_lookup(query: str) -> str:
    '''Search with another provider.

    Args:
        query (str): Search text.
    '''
    return query


class ScriptedModel:
    def __init__(self, outputs):
        self._module_id = f'p1-model-{id(self)}'
        self.outputs = iter(outputs)
        self.requests = []

    def share(self, **kwargs):
        return copy.copy(self)

    def used_by(self, _):
        return self

    def __call__(self, value, **kwargs):
        self.requests.append((copy.deepcopy(value), copy.deepcopy(kwargs)))
        return next(self.outputs)


@pytest.mark.parametrize('exposure', ['eager', 'gateway', 'retrieval'])
def test_complete_natural_tool_loop_refreshes_without_replaying(exposure):
    from lazyllm.tools import ReactAgent
    from lazyllm.tools.agent.toolsManager import ToolGroup

    host = ToolConfigurationRuntime('u', 'c', 'h', 'r', loader=lambda _: [lookup])
    group = ToolGroup(name='mcp_one', desc='Documents', tools=[lookup] if exposure == 'eager' else [],
                      lazy=exposure != 'eager', prefix=False, discoverable=True)
    host._register('mcp:one', group, 'needs_authorization')
    action = {'id': 'action', 'service': 'mcp:one', 'label': 'Documents',
              'version': 1, 'status': 'needs_authorization'}
    delivered = []

    def backend(operation, **payload):
        if operation == 'check':
            return {'status': action['status'], 'mcp_config': {'id': 'one'} if action['status'] == 'ready' else None}
        if operation == 'prepare':
            return dict(action)
        if operation == 'poll_batch':
            action.update(status='ready', version=2)
            return {'actions': [{'action': dict(action), 'mcp_config': {'id': 'one'},
                                 'pending_delivery': not delivered}]}
        if operation == 'ack':
            delivered.append(payload['version'])
            return {'acknowledged': True}
        raise AssertionError(operation)

    host._post = backend
    first = 'lookup' if exposure == 'eager' else 'get_mcp_one_methods'
    arguments = {'query': 'first'} if exposure == 'eager' else {}
    if exposure == 'retrieval':
        first, arguments = 'load_tools', {'tool_names': ['mcp_one'], 'unload_tool_names': []}

    def call(name, args, ident):
        return {'role': 'assistant', 'content': '', 'tool_calls': [
            {'id': ident, 'type': 'function', 'function': {'name': name, 'arguments': json.dumps(args)}}]}

    model = ScriptedModel([call(first, arguments, 'blocked'), call('lookup', {'query': 'next'}, 'business'),
                           {'role': 'assistant', 'content': 'done'}])
    agent = ReactAgent(llm=model, tools=[group], enable_builtin_tools=False, max_retries=4,
                       before_model_request=host.before_request, model_context_provider=host.model_context,
                       runtime_observer=host.observe)
    host.bind(agent._tools_manager)
    if exposure == 'retrieval':
        agent._tools_manager.enable_tool_retrieval(
            groups={'mcp_one'}, required=[], threshold_tokens=100, estimate_tokens=lambda _: 1)
    assert agent('search documents') == 'done'
    assert len(model.requests) == 3
    assert 'Connection is ready' in json.dumps(model.requests[1])
    assert 'needs_authorization' in json.dumps(model.requests[1])
    assert 'next' in json.dumps(model.requests[2])
    assert delivered == [2]
    assert '[Host runtime update]' not in json.dumps(lazyllm.locals['_lazyllm_agent']['history'])


def test_real_native_catalog_retains_mail_and_free_academic_provider():
    from lazymind.chat.service.component.tool_registry import DEFAULT_TOOLS
    host = ToolConfigurationRuntime('u', 'c', 'h', 'r', loader=lambda _: [])
    configs = [cfg for cfg in DEFAULT_TOOLS if cfg.name in {'mail', 'academic_search', 'cloud_files'}]
    manager = ToolManager(host.native_tools(configs))
    host.bind(manager)
    lazyllm.globals.config['dynamic_tool_auth'] = {}
    host._post = Mock(return_value={'status': 'needs_configuration'})
    assert 'get_MailToolkit_methods' in manager.tools_info
    assert host.prepare(['get_ArxivSearch_methods']) is None
    assert set(host.services) == {'mail', 'academic_search', 'feishu', 'notion', 'googledrive'}


def test_unknown_mcp_catalog_has_no_invented_members():
    host, manager = runtime()
    catalog = manager.atomic_tool_catalog()
    assert list(catalog) == ['get_mcp_one_methods']
    assert catalog['get_mcp_one_methods']['schema_state'] == 'unknown'


def test_native_budget_failure_rolls_back_credentials_and_activation():
    from lazymind.chat.service.component.tool_registry import DEFAULT_TOOLS
    host = ToolConfigurationRuntime('u', 'c', 'h', 'r', loader=lambda _: [])
    lazyllm.globals.config['dynamic_tool_auth'] = {}
    manager = ToolManager(host.native_tools([cfg for cfg in DEFAULT_TOOLS if cfg.name == 'mail']))
    host.bind(manager)
    action = {'id': 'mail-action', 'service': 'mail', 'label': 'Mailbox', 'version': 2, 'status': 'ready'}
    host.actions['mail'] = action
    host.intents['mail'] = {'get_MailToolkit_methods'}
    credential = json.dumps({'email': 'one@example.com', 'provider': 'qqmail', 'secret': 'test-secret'})
    host._post = Mock(side_effect=lambda operation, **kwargs: {'actions': [action]} if operation == 'list' else
                      {'actions': [{'action': action, 'pending_delivery': True,
                                    'tool_config': {'mail': credential}}]})
    manager.tool_load_validator = Mock(side_effect=ValueError('budget'))
    host.before_request()
    assert not lazyllm.globals.config['dynamic_tool_auth']
    assert lazyllm.locals['_lazyllm_agent']['workspace']['_active_groups'] == []
    assert host.applied == set()
    manager.tool_load_validator = None
    host.before_request()
    assert host._ready('mail')
    assert host.applied == {('mail-action', 2)}


def test_explicit_provider_does_not_fall_back_to_another_ready_provider():
    from lazymind.chat.service.component.tool_registry import DEFAULT_TOOLS
    host = ToolConfigurationRuntime('u', 'c', 'h', 'r', loader=lambda _: [], query='请用 Bing 搜索')
    lazyllm.globals.config['dynamic_tool_auth'] = {'google': 'test-key'}
    manager = ToolManager(host.native_tools([cfg for cfg in DEFAULT_TOOLS if cfg.name == 'web_search']))
    assert list(host.services) == ['web_search/bing']
    host.bind(manager)
    assert not host._ready('web_search/bing')
    assert list(manager.atomic_tool_catalog()) == ['get_WebSearchToolkit_methods']


def test_structured_configuration_event_does_not_request_automatic_continuation():
    from lazymind.chat.service.component import AgentEventFrameTranslator
    translator = AgentEventFrameTranslator(query='mail')
    action = {'id': 'a', 'service': 'mail', 'status': 'needs_configuration'}
    frames = translator.feed({'tag': 'tool_configuration', 'action': action})
    assert frames[0]['tool_configuration'] == action
    assert 'ask_pending' not in frames[0]
    assert 'capability_dependency' not in frames[0]


def test_new_request_recovers_configuration_without_restoring_old_load_intent():
    host, manager = runtime()
    host.actions.clear()
    host.intents.clear()
    retrieval = manager.enable_tool_retrieval(groups={'mcp_one'}, required=[], estimate_tokens=lambda _: 1,
                                              threshold_tokens=100)
    host.before_request()
    assert host._post.call_args_list[0].args == ('list',)
    assert 'lookup' in manager.tools_info
    assert 'lookup' not in {d['function']['name'] for d in retrieval.descriptions()}
    assert 'Connection is ready' in host.model_context()


@pytest.mark.parametrize('builtin_available,native_connected,expect_native', [
    (True, False, False), (True, True, True), (False, False, True),
])
def test_unconfigured_native_notion_does_not_compete_with_builtin_mcp(
        builtin_available, native_connected, expect_native):
    from lazymind.chat.service.component.tool_registry import DEFAULT_TOOLS
    host = ToolConfigurationRuntime('u', 'c', 'h', 'r', loader=lambda _: [])
    lazyllm.globals.config['dynamic_fs_auth'] = {'notion': 'test-token'} if native_connected else {}
    catalog = [{'service': 'mcp:msp_notion_personal', 'label': 'Notion',
                'status': 'needs_authorization'}] if builtin_available else []
    groups = host.native_tools([cfg for cfg in DEFAULT_TOOLS if cfg.name == 'cloud_files'],
                               mcp_catalog=catalog)
    manager = ToolManager([*groups, *asyncio.run(host.mcp_tools(catalog))])
    assert ('notion' in host.services) == expect_native
    assert 'feishu' in host.services
    if builtin_available:
        assert 'get_mcp_msp_notion_personal_methods' in manager.atomic_tool_catalog()


@pytest.mark.parametrize('failure,status', [('expired', 'needs_authorization'), ('unavailable', 'unavailable')])
def test_initial_mcp_catalog_load_isolates_auth_failures(failure, status):
    from lazymind.chat.service.mcp_oauth import MCPAuthorizationRequired, MCPAuthUnavailable

    def load(config):
        if config['id'] == 'broken':
            raise MCPAuthorizationRequired() if failure == 'expired' else MCPAuthUnavailable()
        return [lookup]

    host = ToolConfigurationRuntime('u', 'c', 'h', 'r', loader=load)
    issues = []
    groups = asyncio.run(host.mcp_tools([
        {'service': 'mcp:broken', 'label': 'Broken', 'status': 'ready', 'runtime': {'id': 'broken'}},
        {'service': 'mcp:healthy', 'label': 'Healthy', 'status': 'ready', 'runtime': {'id': 'healthy'}},
    ], issues=issues))
    assert len(groups) == 2
    assert host.services['mcp:broken']['status'] == status
    assert host.services['mcp:healthy']['status'] == 'ready'
    assert groups[1].get_flat_tools()
    assert issues == [{'server': 'Broken', 'status': status}]


def test_ready_configuration_does_not_overwrite_catalog_failure_or_create_card():
    host = ToolConfigurationRuntime('u', 'c', 'h', 'r', loader=lambda _: [])
    config = {'id': 'one'}
    groups = asyncio.run(host.mcp_tools([
        {'service': 'mcp:one', 'label': 'Documents', 'status': 'ready', 'runtime': config}]))
    host.bind(ToolManager(groups))
    host._post = Mock(return_value={'status': 'ready', 'mcp_config': config})
    result = host.prepare(['get_mcp_one_methods'])
    assert result['status'] == 'unavailable'
    assert host.actions == {}
    assert [call.args[0] for call in host._post.call_args_list] == ['check']


def test_new_runtime_restores_delivered_configuration_without_notice_or_ack():
    host, manager = runtime()
    host.actions.clear()
    host.intents.clear()
    action = {'id': 'a', 'version': 2, 'service': 'mcp:one', 'label': 'Documents', 'status': 'ready'}
    host._post = Mock(side_effect=lambda op, **kw: {'actions': [action]} if op == 'list' else {
        'actions': [{'action': action, 'pending_delivery': False, 'mcp_config': {'id': 'one'}}]})
    host.before_request()
    assert 'lookup' in manager.tools_info
    assert host.model_context() is None
    host.observe('history_ready')
    host.observe('turn_end')
    assert not any(call.args[0] == 'ack' for call in host._post.call_args_list)


def test_restored_delivered_action_does_not_repeat_catalog_failure_notice():
    host, _ = runtime()
    host.actions.clear()
    host.intents.clear()
    host.loader = lambda _: []
    action = {'id': 'a', 'version': 2, 'service': 'mcp:one', 'label': 'Documents', 'status': 'ready'}
    host._post = Mock(side_effect=lambda op, **kw: {'actions': [action]} if op == 'list' else {
        'actions': [{'action': action, 'pending_delivery': False, 'mcp_config': {'id': 'one'}}]})
    for _ in range(2):
        host.before_request()
        assert host.model_context() is None
        assert not host._ready('mcp:one')


@pytest.mark.parametrize('retrieval', [False, True])
def test_refresh_disk_commit_failure_preserves_real_state_store(tmp_path, monkeypatch, retrieval):
    from lazymind.chat.engine.agent_runtime.tool_retrieval import ToolStateStore
    import lazymind.chat.engine.agent_runtime.tool_retrieval as state_module

    host, manager = runtime()
    store = ToolStateStore(['refresh-transaction-test'])
    store.path = tmp_path / 'state.json'
    if retrieval:
        manager.enable_tool_retrieval(groups={'mcp_one'}, required=[], estimate_tokens=lambda _: 1,
                                      threshold_tokens=100, state_store=store)
    else:
        manager.group_state_store = store
    host.before_request()
    before = store.path.read_bytes()
    description = copy.deepcopy(manager.tools_description)
    lazyllm.globals.config['dynamic_tool_auth'] = {'bing': 'old'}

    def fail_commit(*_args):
        raise OSError('controlled atomic replace failure')

    monkeypatch.setattr(state_module.os, 'replace', fail_commit)
    result = manager.refresh_tool_group('mcp_one', definition={
        'name': 'mcp_one', 'desc': 'Updated documents', 'prefix': False, 'lazy': False,
        'tools': [other_lookup]}, tool_config={'bing': 'new'}, load=True)
    assert result['status'] == 'unavailable'
    assert store.path.read_bytes() == before
    assert manager.tools_description == description
    assert lazyllm.globals.config['dynamic_tool_auth'] == {'bing': 'old'}
    assert not list(tmp_path.glob('.tools-*'))

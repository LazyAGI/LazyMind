import copy
import json
from concurrent.futures import ThreadPoolExecutor
from types import SimpleNamespace

import pytest
import lazyllm
from lazyllm.tools.agent.toolsManager import ToolManager, fc_register
from lazyllm.tools.agent.toolError import ToolExecutionError
from lazymind.chat.engine.agent_runtime.models import AgentExecutionOptions, AgentRole
from lazymind.chat.engine.agent_runtime.tool_retrieval import ToolStateStore, configure_tool_retrieval
from lazymind.config import config
from lazymind.chat.engine.tools.workspace_context import WorkspaceContext


class ScriptedModel:
    _module_id = 'retrieval-integration-model'

    def __init__(self, outputs):
        self.outputs = iter(outputs)
        self.inputs = []

    def share(self, **kwargs):
        return copy.copy(self)

    def used_by(self, module_id):
        return self

    def __call__(self, value, **kwargs):
        self.inputs.append(value)
        return next(self.outputs)


@fc_register(host_file='NONE')
def search_mail(query: str) -> str:
    '''Search email messages.

    Args:
        query (str): Email keywords.
    '''
    return query


@fc_register(host_file='NONE')
def read_mail(message_id: str) -> str:
    '''Read email messages.

    Args:
        message_id (str): Email identifier.
    '''
    return message_id


@pytest.fixture
def scope(tmp_path):
    previous = config['agentic_workspace']
    old = lazyllm.globals.get('agentic_config')
    config['agentic_workspace'] = str(tmp_path)
    lazyllm.globals['agentic_config'] = {'enable_tool_retrieval': True, 'user_id': 'u', 'conversation_id': 'c'}
    yield tmp_path
    config['agentic_workspace'] = previous
    lazyllm.globals['agentic_config'] = old


def agent(scope='chat', preview=False, required=(), skills=None):
    skill_tools = skills.get_skill_tools() if hasattr(skills, 'get_skill_tools') else []
    result = SimpleNamespace(_tools_manager=ToolManager([search_mail, read_mail, *skill_tools]),
                             _skill_manager=skills, _prompt='tool policy',
                             _tools=[search_mail, read_mail, *skill_tools])
    plan = SimpleNamespace(role=AgentRole.CHAT, stop_tools=[], prompt=SimpleNamespace(current_input='Find mail'),
                           execution_options=AgentExecutionOptions(
        tool_state_scope=scope, context_preview=preview, required_tool_names=required))
    configure_tool_retrieval(result, plan)
    return result


def test_restore_unload_preview_and_atomic_disk_failure(scope, monkeypatch):
    a = agent()
    a._tools_manager.retrieval.load(['search_mail'], [])
    restored = agent()
    assert 'search_mail' in [x['function']['name'] for x in restored._tools_manager.tools_description]
    restored._tools_manager.retrieval.load([], ['search_mail'])
    assert len(agent()._tools_manager.tools_description) == 2
    assert len(agent('subagent:1')._tools_manager.tools_description) == 2
    before = {p: p.read_bytes() for p in scope.rglob('*.json')}
    preview = agent(preview=True, required=('search_mail',))
    assert len(preview._tools_manager.tools_description) == 3
    assert before == {p: p.read_bytes() for p in scope.rglob('*.json')}
    with pytest.raises(RuntimeError):
        preview._tools_manager.retrieval.load(['read_mail'], [])
    monkeypatch.setattr('lazymind.chat.engine.agent_runtime.tool_retrieval.os.replace',
                        lambda *args: (_ for _ in ()).throw(OSError('disk full')))
    with pytest.raises(OSError):
        restored._tools_manager.retrieval.load(['read_mail'], [])
    assert len(restored._tools_manager.tools_description) == 2


def test_skill_dependencies_protection_revocation_and_hard_budget(scope):
    allowed = ['search_mail', 'not_allowed']
    skills = SimpleNamespace(_get_visible_skill_info=lambda name: ({'allowed-tools': allowed}, None),
                             build_prompt=lambda: '', describe_prompt=lambda: [])
    a = agent(skills=skills)
    loaded = skills.on_skill_loaded('mail', allowed)
    assert loaded['loaded'] == ['search_mail']
    assert loaded['unavailable']
    with pytest.raises(ToolExecutionError):
        a._tools_manager.retrieval.load([], ['search_mail'])
    allowed.clear()
    a._tools_manager.retrieval.load([], ['search_mail'])
    with pytest.raises(RuntimeError, match='exceeds'):
        a._tools_manager.context_validator({'tool_definitions': []}, [], 'x ' * 1000000)


def test_concurrent_store_transactions_and_user_isolation(scope):
    stores = [ToolStateStore(['u', 'c', 'chat']) for _ in range(2)]

    def add(index):
        return stores[index].update(lambda state: {
            'loaded': [*state.get('loaded', []), str(index)], 'skills': {}})
    with ThreadPoolExecutor(2) as pool:
        list(pool.map(add, range(2)))
    assert set(stores[0].read()['loaded']) == {'0', '1'}
    assert ToolStateStore(['other-user', 'c', 'chat']).read() == {}


def test_required_tools_remain_loaded_when_scene_ends(scope):
    agent(required=('search_mail',))
    restored = agent()
    assert 'search_mail' in [x['function']['name'] for x in restored._tools_manager.tools_description]
    restored._tools_manager.retrieval.load([], ['search_mail'])
    assert len(restored._tools_manager.tools_description) == 2


def test_executor_search_load_execute_and_disabled_mode(scope):
    import json
    from lazymind.chat.engine.agent_runtime import AgentExecutor, AgentRunPlan, PromptBuilder

    def call(name, **args):
        return {'content': '', 'tool_calls': [
            {'id': name, 'type': 'function', 'function': {'name': name, 'arguments': json.dumps(args)}}]}

    plan = AgentRunPlan(
        role=AgentRole.CHAT,
        prompt=PromptBuilder.for_role(AgentRole.CHAT).input('Find email hello', source='user').build(),
        tools=[{'name': 'MailToolkit', 'desc': 'Search and read email.', 'tools': [search_mail, read_mail]}],
        execution_options=AgentExecutionOptions(enable_builtin_tools=False, skills=False, max_retries=5),
    )
    model = ScriptedModel([call('search_tools', query='email'), call('load_tools', tool_names=['MailToolkit']),
                           call('search_mail', query='hello'), {'content': 'done'}])
    created = AgentExecutor().create_agent(model, plan)
    context = created.describe_context(current_input=plan.prompt.current_input)
    guidance = ('Grouped tools are usually complementary and intended to work together. Prefer loading the group; '
                'load an individual member only when the required capability is clearly limited to that tool.')
    assert guidance in ' '.join(context['system_prompt'].split())
    load_schema = next(d for d in context['tool_definitions'] if d['function']['name'] == 'load_tools')
    assert guidance not in ' '.join(load_schema['function']['description'].split())
    assert created(plan.prompt.current_input) == 'done'
    assert len(model.inputs) == 4
    assert 'hello' in str(model.inputs[-1])
    assert 'search_mail' in [d['function']['name'] for d in created._tools_manager.tools_description]
    lazyllm.globals['agentic_config']['enable_tool_retrieval'] = False
    legacy = AgentExecutor().create_agent(ScriptedModel([{'content': 'done'}]), plan)
    assert {d['function']['name'] for d in legacy._tools_manager.tools_description} == {'get_MailToolkit_methods'}


def test_skill_file_loads_dependencies_but_listing_does_not(scope):
    from lazyllm.tools.agent.skill_manager import SkillManager
    folder = scope / 'skills' / 'email'
    folder.mkdir(parents=True)
    (folder / 'SKILL.md').write_text(
        '---\nname: email\ndescription: Email search\nallowed-tools: search_mail\n---\nSearch mail.',
        encoding='utf-8',
    )
    skills = SkillManager(dir=str(folder.parent), skills=['email'])
    a = agent(skills=skills)
    skills.list_skill()
    assert 'search_mail' not in [d['function']['name'] for d in a._tools_manager.tools_description]
    assert skills.get_skill('email')['tool_dependencies']['loaded'] == ['search_mail']
    restored = agent(skills=SkillManager(dir=str(folder.parent), skills=['email']))
    with pytest.raises(ToolExecutionError):
        restored._tools_manager.retrieval.load([], ['search_mail'])


def test_business_groups_and_writer_loading(scope):
    from lazymind.document_tools import WriterCreateToolkit, WriterRevisionToolkit
    from lazymind.chat.lazyllm_tool_docs import ensure_lazyllm_tool_docs
    tools = [WriterCreateToolkit(), WriterRevisionToolkit(),
             {'name': 'CloudFileToolkit', 'desc': 'Cloud files.', 'tools': [
                 {'name': 'FeishuWikiFS', 'desc': 'Wiki email correspondence.', 'tools': [search_mail, read_mail]}]}]
    ensure_lazyllm_tool_docs(tools)
    a = agent()
    a._tools_manager = ToolManager(tools)
    plan = SimpleNamespace(role=AgentRole.CHAT, stop_tools=[], prompt=SimpleNamespace(current_input='Find mail'),
                           execution_options=AgentExecutionOptions())
    configure_tool_retrieval(a, plan)
    manager = a._tools_manager
    found = manager.retrieval.search('wiki', 5, 'long')
    assert found[0]['name'] == 'FeishuWikiFS'
    assert found[0]['matched_members'] == []
    for name, count in [('WriterCreateToolkit', 19), ('WriterRevisionToolkit', 12)]:
        loaded = manager.retrieval.load([name], [])['loaded']
        assert len(loaded) == count
        assert all(member.startswith(name + '_') for member in loaded)
        assert set(loaded).issubset({d['function']['name'] for d in manager.tools_description})
        manager.retrieval.load([], [name])
    assert not any(name.startswith('get_') and name.endswith('_methods') for name in manager.tools_info)


def test_group_restore_and_legacy_gateway(scope):
    tools = [{'name': 'MailToolkit', 'desc': 'Search email correspondence.', 'lazy': True,
              'tools': [search_mail, read_mail]}]
    a = agent()
    a._tools_manager = ToolManager(tools)
    legacy = a._tools_manager.tools_description
    assert legacy[0]['function']['name'] == 'get_MailToolkit_methods'
    plan = SimpleNamespace(role=AgentRole.CHAT, stop_tools=[], prompt=SimpleNamespace(current_input='Find mail'),
                           execution_options=AgentExecutionOptions())
    configure_tool_retrieval(a, plan)
    found = a._tools_manager.retrieval.search('email', 5, 'short')[0]
    assert found['name'] == 'MailToolkit'
    assert 'search_mail' in {item['name'] for item in found['matched_members']}
    a._tools_manager.retrieval.load(['search_mail'], [])
    a._tools_manager = ToolManager(tools)
    configure_tool_retrieval(a, plan)
    assert 'read_mail' not in {d['function']['name'] for d in a._tools_manager.tools_description}


def test_mcp_server_groups_follow_registered_tools(scope, monkeypatch):
    import asyncio
    from lazymind.chat.service import chat_service
    from lazymind.chat.engine.agent_runtime import AgentExecutor, AgentRunPlan, PromptBuilder

    class Client(chat_service.MCPClient):
        def __init__(self, **kwargs):
            super().__init__(**kwargs)

        def get_tools(self, allowed_tools=None):
            def lookup(query: str) -> str:
                """Search correspondence by subject.

                Args:
                    query (str): Correspondence keywords.
                """
                return query
            lookup.__name__ = allowed_tools[0]
            from lazyllm.tools.agent.toolsManager import fc_register
            lookup.__mcp_tool_name__ = lookup.__name__
            return [fc_register(tool_source='mcp')(lookup)]

    monkeypatch.setattr(chat_service, 'MCPClient', Client)
    monkeypatch.setattr(chat_service, '_mcp_tool_cache', {})
    configs = [{'id': 'a', 'name': 'Mailbox', 'url': 'https://example.test/a', 'allowed_tools': ['lookup_a']},
               {'id': 'b', 'name': 'Mailbox', 'url': 'https://example.test/b', 'allowed_tools': ['lookup_b']},
               {'name': 'Legacy', 'url': 'https://example.test/legacy', 'allowed_tools': ['lookup_legacy']}]
    tools = asyncio.run(chat_service._build_mcp_tools(configs))
    # Actual executor registration also drops duplicate names before constructing groups.
    plan = AgentRunPlan(role=AgentRole.CHAT,
                        prompt=PromptBuilder.for_role(AgentRole.CHAT).input('Find mail', source='user').build(),
                        tools=[*tools, tools[0]],
                        execution_options=AgentExecutionOptions(enable_builtin_tools=False, skills=False))
    created = AgentExecutor().create_agent(object(), plan)
    retrieval = created._tools_manager.retrieval
    assert {r['name'] for r in retrieval.search('subject', 5, 'short')} == {
        'mcp:a', 'mcp:b', 'lookup_legacy'}
    aliases = {entry['origin']: name for name, entry in created._tools_manager.atomic_tool_catalog().items()
               if entry['source'] == 'mcp'}
    assert retrieval.load(['mcp:a'], [])['loaded'] == [aliases['a']]
    assert retrieval.load([aliases['b']], [])['loaded'] == [aliases['b']]
    assert 'lookup_legacy' not in {d['function']['name'] for d in retrieval.descriptions()}
    assert retrieval.load([], ['mcp:a'])['unloaded'] == [aliases['a']]
    # A role which receives only b must not acquire a through the dynamic map.
    from dataclasses import replace
    plan = replace(plan, tools=[tools[1]])
    restricted = AgentExecutor().create_agent(object(), plan)
    with pytest.raises(ToolExecutionError):
        restricted._tools_manager.retrieval.load(['mcp:a'], [])
    configs[0]['name'] = 'Renamed service'
    plan = replace(plan, tools=asyncio.run(chat_service._build_mcp_tools(configs[:1])))
    renamed = AgentExecutor().create_agent(object(), plan)
    assert renamed._tools_manager.retrieval.search('renamed', 5, 'long')[0]['name'] == 'mcp:a'
    lazyllm.globals['agentic_config']['enable_tool_retrieval'] = False
    legacy = AgentExecutor().create_agent(object(), plan)
    assert [d['function']['name'] for d in legacy._tools_manager.tools_description] == [aliases['a']]


def test_hard_limit_rolls_back_load_skill_and_host_preload(scope):
    from lazymind.chat.engine.agent_runtime import AgentExecutor, AgentRunPlan, PromptBuilder

    def oversized(query: str) -> str:
        return query
    oversized.__doc__ = 'Large schema. ' * 10000 + '\n\nArgs:\n    query (str): Search keywords.\n'
    plan = AgentRunPlan(role=AgentRole.CHAT,
                        prompt=PromptBuilder.for_role(AgentRole.CHAT).input('Find mail', source='user').build(),
                        tools=[search_mail, oversized],
                        history=[{'role': 'user', 'content': 'compressible history ' * 10000}],
                        execution_options=AgentExecutionOptions(
                            enable_builtin_tools=False, skills=False, max_input_tokens=6000))
    created = AgentExecutor().create_agent(object(), plan)
    controller = created._tools_manager.retrieval
    # Isolate hard-limit rejection from the independent model-addition soft gate.
    controller.threshold_tokens = 1000000
    controller.load(['search_mail'], [])
    before = controller.descriptions()
    disk = {p: p.read_bytes() for p in scope.rglob('*.json')}
    for load in (lambda: controller.load(['oversized'], ['search_mail']),
                 lambda: controller.load_skill('large', ['oversized'])):
        with pytest.raises(ToolExecutionError, match='固定上下文'):
            load()
        assert controller.descriptions() == before
        assert disk == {p: p.read_bytes() for p in scope.rglob('*.json')}
    from dataclasses import replace
    result = created._tools_manager([{'id': 'too-large', 'function': {
        'name': 'load_tools', 'arguments': '{"tool_names":["oversized"],"unload_tool_names":["search_mail"]}'}}])
    assert result[0]['ok'] is False
    assert '固定上下文' in str(result[0])
    assert controller.descriptions() == before
    assert disk == {p: p.read_bytes() for p in scope.rglob('*.json')}
    model = ScriptedModel([
        {'content': '', 'tool_calls': [{'id': 'load', 'type': 'function', 'function': {
            'name': 'load_tools', 'arguments': '{"tool_names":["oversized"]}'}}]},
        {'content': 'recovered'},
    ])
    scripted = AgentExecutor().create_agent(model, replace(plan, history=[]))
    scripted._tools_manager.retrieval.threshold_tokens = 1000000
    assert scripted(plan.prompt.current_input) == 'recovered'
    assert len(model.inputs) == 2
    assert '固定上下文' in str(model.inputs[-1])
    assert scripted._tools_manager.retrieval.descriptions() == before
    assert disk == {p: p.read_bytes() for p in scope.rglob('*.json')}
    plan = replace(plan, execution_options=replace(plan.execution_options, required_tool_names=('oversized',)))
    with pytest.raises(ToolExecutionError, match='固定上下文'):
        AgentExecutor().create_agent(object(), plan)
    assert disk == {p: p.read_bytes() for p in scope.rglob('*.json')}


@pytest.mark.parametrize('component', ['system', 'input', 'skill'])
def test_load_budget_includes_fixed_context_components(scope, component):
    skills = SimpleNamespace(build_prompt=lambda: '', describe_prompt=lambda: [],
                             _get_visible_skill_info=lambda name: (None, None))
    a = SimpleNamespace(_tools_manager=ToolManager([search_mail]), _tools=[search_mail],
                        _prompt='System instructions', _skill_manager=skills)
    plan = SimpleNamespace(role=AgentRole.CHAT, stop_tools=[],
                           prompt=SimpleNamespace(current_input='Find mail'),
                           execution_options=AgentExecutionOptions(max_input_tokens=10000))
    configure_tool_retrieval(a, plan)
    controller = a._tools_manager.retrieval
    controller.threshold_tokens = 1000000
    before = controller.descriptions()
    disk = {p: p.read_bytes() for p in scope.rglob('*.json')}
    large = 'Fixed instructions ' * 10000
    if component == 'system':
        a._prompt += large
    elif component == 'input':
        plan.prompt.current_input += large
    else:
        skills.describe_prompt = lambda: [{'content': large}]
    with pytest.raises(ToolExecutionError, match='固定上下文'):
        controller.load(['search_mail'], [])
    assert controller.descriptions() == before
    assert disk == {p: p.read_bytes() for p in scope.rglob('*.json')}


@pytest.mark.parametrize('retrieval_enabled', [True, False])
@pytest.mark.parametrize('with_builtin', [True, False])
def test_same_name_mcp_members_keep_server_routing_and_cached_names(
        scope, monkeypatch, retrieval_enabled, with_builtin):
    import asyncio
    import json
    from dataclasses import replace
    from mcp.types import CallToolResult, TextContent
    from lazyllm.tools.mcp.tool_adaptor import generate_lazyllm_tool
    from lazymind.chat.service import chat_service
    from lazymind.chat.engine.agent_runtime import AgentExecutor, AgentRunPlan, PromptBuilder

    calls = []

    class Client(chat_service.MCPClient):
        def __init__(self, command_or_url, **kwargs):
            super().__init__(command_or_url, **kwargs)
            self.server = command_or_url.rsplit('/', 1)[-1]

        def get_tools(self, allowed_tools=None):
            return [generate_lazyllm_tool(self, SimpleNamespace(
                name='search', description='Search documents.',
                inputSchema={'type': 'object', 'properties': {'query': {'type': 'string'}},
                             'required': ['query']},
            ))]

        async def call_tool(self, name, arguments):
            calls.append((self.server, name, arguments))
            return CallToolResult(content=[TextContent(type='text', text=self.server)])

    def search(query: str) -> str:
        """Search builtin documents.

        Args:
            query (str): Search keywords.
        """
        return 'builtin'

    monkeypatch.setattr(chat_service, 'MCPClient', Client)
    monkeypatch.setattr(chat_service, '_mcp_tool_cache', {})
    lazyllm.globals['agentic_config']['enable_tool_retrieval'] = retrieval_enabled
    configs = [{'id': key, 'name': 'Documents', 'url': f'https://example.test/{key}'} for key in ('a', 'b')]
    cached = asyncio.run(chat_service._build_mcp_tools(configs))
    builtin = [search] if with_builtin else []
    plan = AgentRunPlan(role=AgentRole.CHAT,
                        prompt=PromptBuilder.for_role(AgentRole.CHAT).input('Find documents', source='user').build(),
                        tools=[*builtin, *cached, cached[0]],
                        execution_options=AgentExecutionOptions(
                            enable_builtin_tools=False, skills=False,
                            workspace_permission=WorkspaceContext(active=True, permission_mode='allow_all')))
    created = AgentExecutor().create_agent(object(), plan)
    aliases = {entry['origin']: name for name, entry in created._tools_manager.atomic_tool_catalog().items()
               if entry['source'] == 'mcp'}
    assert set(aliases) == {'a', 'b'}
    assert len(set(aliases.values())) == 2
    assert 'search' not in aliases.values()
    assert all(len(name) <= 64 and name.isidentifier() for name in aliases.values())
    if retrieval_enabled:
        retrieval = created._tools_manager.retrieval
        assert {r['name'] for r in retrieval.search('documents', 5, 'short')} >= {'mcp:a', 'mcp:b'}
        assert retrieval.load(['mcp:a'], [])['loaded'] == [aliases['a']]
        assert aliases['b'] not in {d['function']['name'] for d in retrieval.descriptions()}
        assert retrieval.load(['mcp:b'], [])['loaded'] == [aliases['b']]
    for server in ('a', 'b'):
        result = created._tools_manager([{'id': server, 'function': {
            'name': aliases[server], 'arguments': json.dumps({'query': server})}}])
        assert result[0]['ok'] is True
    assert sorted(calls) == [('a', 'search', {'query': 'a'}), ('b', 'search', {'query': 'b'})]
    assert [t.__name__ for t in cached] == ['search', 'search']
    reversed_agent = AgentExecutor().create_agent(object(), replace(plan, tools=[*reversed(cached), *builtin]))
    assert {entry['origin']: name for name, entry in reversed_agent._tools_manager.atomic_tool_catalog().items()
            if entry['source'] == 'mcp'} == aliases
    configs[0]['name'] = 'Renamed documents'
    renamed = asyncio.run(chat_service._build_mcp_tools(configs))
    renamed_agent = AgentExecutor().create_agent(object(), replace(plan, tools=[*builtin, *renamed]))
    assert {entry['origin']: name for name, entry in renamed_agent._tools_manager.atomic_tool_catalog().items()
            if entry['source'] == 'mcp'} == aliases
    single = AgentExecutor().create_agent(object(), replace(plan, tools=[cached[0], cached[0]]))
    assert [t.__name__ for t in single._tools] == ['search']
    assert {name for name, entry in single._tools_manager.atomic_tool_catalog().items()
            if entry['source'] == 'mcp'} == {aliases['a']}


def test_legacy_state_drops_optional_names_and_rebuilds_dependencies(scope):
    import json

    store = ToolStateStore(['u', 'c', 'chat'])
    store.path.parent.mkdir(parents=True, exist_ok=True)
    legacy = {'version': 1, 'loaded': ['search_mail'], 'skills': {'mail': ['search_mail']}}
    store.path.write_text(json.dumps(legacy))
    before = store.path.read_bytes()
    skills = SimpleNamespace(_get_visible_skill_info=lambda name: ({'allowed-tools': ['read_mail']}, None),
                             build_prompt=lambda: '', describe_prompt=lambda: [])
    preview = agent(preview=True, skills=skills)
    names = {d['function']['name'] for d in preview._tools_manager.tools_description}
    assert 'search_mail' not in names
    assert 'read_mail' in names
    assert store.path.read_bytes() == before

    def reject(definitions):
        raise ToolExecutionError('fixed context exceeds limit')

    controller = ToolManager([read_mail]).enable_tool_retrieval(
        required=['read_mail'], groups=[], estimate_tokens=len, threshold_tokens=1000,
        state_store=store, validate_load=reject)
    with pytest.raises(ToolExecutionError, match='fixed context'):
        controller.initialize()
    assert store.path.read_bytes() == before
    restored = agent(skills=skills)
    assert json.loads(store.path.read_text())['version'] == 2
    assert 'search_mail' not in {d['function']['name'] for d in restored._tools_manager.tools_description}
    # Optional loads made with the new names survive subsequent requests.
    restored._tools_manager.retrieval.load(['search_mail'], [])
    assert 'search_mail' in {d['function']['name'] for d in agent(skills=skills)._tools_manager.tools_description}


def test_mcp_dedup_uses_wire_identity_before_registration(scope):
    from lazyllm.tools.mcp.tool_adaptor import generate_lazyllm_tool
    from lazymind.chat.engine.agent_runtime import AgentExecutor, AgentRunPlan, PromptBuilder

    tools = [generate_lazyllm_tool(SimpleNamespace(server_id='a'), SimpleNamespace(
        name=name, description='Search documents.', inputSchema={'type': 'object', 'properties': {}}))
        for name in ('foo.bar', 'foo-bar')]
    plan = AgentRunPlan(role=AgentRole.CHAT,
                        prompt=PromptBuilder.for_role(AgentRole.CHAT).input('Find documents', source='user').build(),
                        tools=[*tools, tools[0]],
                        execution_options=AgentExecutionOptions(enable_builtin_tools=False, skills=False))
    created = AgentExecutor().create_agent(object(), plan)
    catalog = created._tools_manager.atomic_tool_catalog()
    members = {name for name, entry in catalog.items() if entry['source'] == 'mcp'}
    assert len(members) == 2
    assert set(created._tools_manager.retrieval.load(['mcp:a'], [])['loaded']) == members


def test_mcp_loaded_state_keeps_server_across_catalog_changes(scope):
    from dataclasses import replace
    from mcp.types import CallToolResult, TextContent
    from lazyllm.tools.mcp.tool_adaptor import generate_lazyllm_tool
    from lazymind.chat.engine.agent_runtime import AgentExecutor, AgentRunPlan, PromptBuilder

    def make_tool(server):
        async def call_tool(name, arguments):
            return CallToolResult(content=[TextContent(type='text', text=f'{server}:{name}')])
        return generate_lazyllm_tool(SimpleNamespace(server_id=server, call_tool=call_tool), SimpleNamespace(
            name='search', description='Search documents.', inputSchema={'type': 'object', 'properties': {}}))

    def search() -> str:
        '''Search local documents.'''
        return 'local'

    a, b = make_tool('a'), make_tool('b')
    plan = AgentRunPlan(role=AgentRole.CHAT,
                        prompt=PromptBuilder.for_role(AgentRole.CHAT).input('Find documents', source='user').build(),
                        tools=[a],
                        execution_options=AgentExecutionOptions(
                            enable_builtin_tools=False, skills=False,
                            workspace_permission=WorkspaceContext(active=True, permission_mode='allow_all')))
    first = AgentExecutor().create_agent(object(), plan)
    name = first._tools_manager.retrieval.load(['mcp:a'], [])['loaded'][0]
    for tools in ([search, a], [b, a, search], [a], [b, a]):
        restored = AgentExecutor().create_agent(object(), replace(plan, tools=tools))
        manager = restored._tools_manager
        exposed = {d['function']['name'] for d in manager.tools_description}
        assert exposed == {'search_tools', 'load_tools', name}
        assert manager.atomic_tool_catalog()[name]['origin'] == 'a'
        result = manager([{'id': 'call', 'function': {'name': name, 'arguments': '{}'}}])
        assert result[0]['ok'] is True
        assert 'a:search' in str(result)
    persisted = json.loads(next(scope.rglob('*.json')).read_text())
    assert persisted['version'] == 2
    assert set(persisted['loaded']) == {'search_tools', 'load_tools', name}


def test_current_file_resources_are_required_without_legacy_name_preloads(scope):
    from lazyllm.tools import FileSystemToolkit
    from lazymind.chat.engine.tools.file_resources.tools import build_resource_read_tools

    def read_file(path: str) -> str:
        '''Read from an optional external service.

        Args:
            path (str): Remote path.
        '''
        return path

    tools = [*build_resource_read_tools(), FileSystemToolkit(), read_file]
    result = SimpleNamespace(_tools_manager=ToolManager(tools), _tools=tools,
                             _skill_manager=None, _prompt='tool policy')
    plan = SimpleNamespace(role=AgentRole.CHAT, stop_tools=[], prompt=SimpleNamespace(current_input='Read a file'),
                           execution_options=AgentExecutionOptions())
    configure_tool_retrieval(result, plan)
    exposed = {d['function']['name'] for d in result._tools_manager.tools_description}
    assert {'read_file_resource', 'search_file_resource', 'read', 'write', 'ls', 'grep'} <= exposed
    assert 'read_file' not in exposed
    with pytest.raises(ToolExecutionError):
        result._tools_manager.retrieval.load([], ['read_file_resource'])

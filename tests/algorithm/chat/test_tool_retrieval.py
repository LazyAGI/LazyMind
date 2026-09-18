from concurrent.futures import ThreadPoolExecutor
from types import SimpleNamespace

import pytest
import lazyllm
from lazyllm.tools.agent.toolsManager import ToolManager
from lazyllm.tools.agent.toolError import ToolExecutionError
from lazymind.chat.engine.agent_runtime.models import AgentExecutionOptions, AgentRole
from lazymind.chat.engine.agent_runtime.tool_retrieval import ToolStateStore, configure_tool_retrieval
from lazymind.config import config


def search_mail(query: str) -> str:
    '''Search email messages.

    Args:
        query (str): Email keywords.
    '''
    return query


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
                             _skill_manager=skills, _prompt='tool policy')
    plan = SimpleNamespace(role=AgentRole.CHAT, stop_tools=[], execution_options=AgentExecutionOptions(
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
    skills = SimpleNamespace(_get_visible_skill_info=lambda name: ({'allowed-tools': allowed}, None))
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
    import copy
    import json
    from lazymind.chat.engine.agent_runtime import AgentExecutor, AgentRunPlan, PromptBuilder

    class Model:
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

    def call(name, **args):
        return {'content': '', 'tool_calls': [
            {'id': name, 'type': 'function', 'function': {'name': name, 'arguments': json.dumps(args)}}]}

    plan = AgentRunPlan(
        role=AgentRole.CHAT,
        prompt=PromptBuilder.for_role(AgentRole.CHAT).input('Find email hello', source='user').build(),
        tools=[{'name': 'MailToolkit', 'desc': 'Search and read email.', 'tools': [search_mail, read_mail]}],
        execution_options=AgentExecutionOptions(enable_builtin_tools=False, skills=False, max_retries=5),
    )
    model = Model([call('search_tools', query='email'), call('load_tools', tool_names=['MailToolkit']),
                   call('search_mail', query='hello'), {'content': 'done'}])
    created = AgentExecutor().create_agent(model, plan)
    assert created(plan.prompt.current_input) == 'done'
    assert len(model.inputs) == 4
    assert 'hello' in str(model.inputs[-1])
    assert 'search_mail' in [d['function']['name'] for d in created._tools_manager.tools_description]
    lazyllm.globals['agentic_config']['enable_tool_retrieval'] = False
    legacy = AgentExecutor().create_agent(Model([{'content': 'done'}]), plan)
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
    from lazymind.chat.engine.tools.writer import WriterCreateToolkit, WriterRevisionToolkit
    from lazymind.chat.lazyllm_tool_docs import ensure_lazyllm_tool_docs
    tools = [WriterCreateToolkit(), WriterRevisionToolkit(),
             {'name': 'CloudFileToolkit', 'desc': 'Cloud files.', 'tools': [
                 {'name': 'FeishuWikiFS', 'desc': 'Wiki email correspondence.', 'tools': [search_mail, read_mail]}]}]
    ensure_lazyllm_tool_docs(tools)
    a = agent()
    a._tools_manager = ToolManager(tools)
    plan = SimpleNamespace(role=AgentRole.CHAT, stop_tools=[], execution_options=AgentExecutionOptions())
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
    plan = SimpleNamespace(role=AgentRole.CHAT, stop_tools=[], execution_options=AgentExecutionOptions())
    configure_tool_retrieval(a, plan)
    found = a._tools_manager.retrieval.search('email', 5, 'short')[0]
    assert found['name'] == 'MailToolkit'
    assert 'search_mail' in {item['name'] for item in found['matched_members']}
    a._tools_manager.retrieval.load(['search_mail'], [])
    a._tools_manager = ToolManager(tools)
    configure_tool_retrieval(a, plan)
    assert 'read_mail' not in {d['function']['name'] for d in a._tools_manager.tools_description}

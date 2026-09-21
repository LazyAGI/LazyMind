from lazyllm.tools.agent import HostFileAccess
import pytest

import lazyllm
from lazymind.chat.service.component.tool_registry import (
    DEFAULT_TOOLS,
    IMAGE_GENERATION_PROMPT_APPENDIX,
    IMAGE_MARKDOWN_OUTPUT_APPENDIX,
    RETRIEVAL_CITATION_OUTPUT_APPENDIX,
    SKILL_TOOL_CONFIG,
    ToolConfig,
    _capability_is_denied,
    collect_system_prompt_appendices,
    filter_tools,
    get_all_tool_groups,
    tool_is_active,
)


@pytest.mark.parametrize(
    'query',
    [
        '不要使用知识库', '别用知识库', '我不想用知识库', '无需查询知识库',
        '不能使用知识库', '禁止调用知识库', '忽略知识库', 'do not use knowledge base',
    ],
)
def test_capability_denial_recognizes_common_wording(query):
    assert _capability_is_denied(query, ('知识库', 'knowledge base')) is True


def test_capability_denial_does_not_leak_across_positive_clause():
    assert _capability_is_denied(
        '不用知识库A，可以使用知识库B', ('知识库',),
    ) is False


@pytest.fixture(autouse=True)
def reset_dynamic_tool_auth():
    old_auth = lazyllm.globals.config['dynamic_tool_auth']
    old_agentic_config = lazyllm.globals.get('agentic_config')
    lazyllm.globals.config['dynamic_tool_auth'] = {}
    lazyllm.globals['agentic_config'] = {}
    try:
        yield
    finally:
        lazyllm.globals.config['dynamic_tool_auth'] = old_auth
        lazyllm.globals['agentic_config'] = old_agentic_config or {}


def _active_tool_names() -> set[str]:
    return {cfg.name for cfg in filter_tools(DEFAULT_TOOLS)}


def _tool_group(name: str) -> dict:
    return next(group for group in get_all_tool_groups() if group['name'] == name)


def test_web_search_requires_at_least_one_search_key():
    assert 'web_search' not in _active_tool_names()
    assert _tool_group('web_search')['active'] is False

    lazyllm.globals.config['dynamic_tool_auth'] = {'bing': 'bing-token'}

    assert 'web_search' in _active_tool_names()
    group = _tool_group('web_search')
    assert group['active'] is True
    assert any(method['name'] == 'BingSearch' and method['active'] for method in group['methods'])


def test_wikipedia_and_web_search_remain_visible_when_tavily_is_configured():
    lazyllm.globals.config['dynamic_tool_auth'] = {'tavily': 'tavily-token'}
    lazyllm.globals['agentic_config'] = {'query': '怎么制作 AI 视频'}

    assert 'web_search' in _active_tool_names()
    assert 'wikipedia' in _active_tool_names()


def test_wikipedia_remains_available_without_web_provider():
    lazyllm.globals.config['dynamic_tool_auth'] = {}
    lazyllm.globals['agentic_config'] = {'query': 'AI 视频是什么'}

    assert 'web_search' not in _active_tool_names()
    assert 'wikipedia' in _active_tool_names()


def test_temp_kb_activates_when_files_are_present():
    from lazyllm.tools.agent.toolsManager import ToolManager

    assert 'temp_kb' not in _active_tool_names()
    assert _tool_group('temp_kb')['active'] is False

    temp_kb_cfg = next(cfg for cfg in DEFAULT_TOOLS if cfg.name == 'temp_kb')
    manager = ToolManager([temp_kb_cfg.tool])
    assert manager.tools_description == []

    lazyllm.globals['agentic_config'] = {'files': ['tmp-a.md']}

    configs = filter_tools(DEFAULT_TOOLS)
    assert 'temp_kb' in {cfg.name for cfg in configs}
    manager = ToolManager([temp_kb_cfg.tool])
    assert [d['function']['name'] for d in manager.tools_description] == ['kb_tmp_search']
    group = _tool_group('temp_kb')
    assert group['active'] is True
    assert group['methods'] == [
        {
            'name': 'kb_tmp_search',
            'summary': 'Locate passages in this conversation\'s uploaded documents.',
        }
    ]


def test_registry_key_source_activates_function_tool():
    def gated_tool() -> None:
        return None

    cfg = ToolConfig(
        name='gated',
        label='gated',
        description='gated',
        tool=(
            gated_tool,
            lambda: (lazyllm.globals.get('agentic_config') or {}).get('files'),
        ),
        module='retrieval',
    )

    assert tool_is_active(cfg) is False
    lazyllm.globals['agentic_config'] = {'files': ['tmp-a.md']}
    assert tool_is_active(cfg) is True


def test_catalog_exposes_modules_without_registering_module_gateways():
    from lazyllm.tools.agent.toolsManager import ToolManager

    groups = get_all_tool_groups()
    assert all(group['module'] for group in groups)
    calculator = next(cfg for cfg in DEFAULT_TOOLS if cfg.name == 'calculator')
    manager = ToolManager([calculator.tool])
    names = {item['function']['name'] for item in manager.tools_description}
    assert names == {'calculator'}
    assert not any('utility' in name for name in names)


def test_memory_tools_are_registered_as_one_eager_group():
    from lazyllm.tools.agent.toolsManager import ToolManager

    configs = [
        cfg for cfg in DEFAULT_TOOLS
        if cfg.name in {'memory', 'read_memory', 'episode_create'}
    ]

    assert [cfg.name for cfg in configs] == ['memory']
    config = configs[0]
    assert [method['name'] for method in _tool_group('memory')['methods']] == [
        'read_memory',
        'read_memory_reference',
        'soul_editor',
        'profile_editor',
        'preference_editor',
        'episode_create',
    ]
    manager = ToolManager([config.tool])
    assert {item['function']['name'] for item in manager.tools_description} == {
        'MemoryTools_read_memory',
        'MemoryTools_read_memory_reference',
        'MemoryTools_soul_editor',
        'MemoryTools_profile_editor',
        'MemoryTools_preference_editor',
        'MemoryTools_episode_create',
    }
    assert not hasattr(config.tool, 'memory_editor')
    memory_policy = '\n'.join(config.appendix_system_prompt['tool_policy'])
    assert 'Never claim that information was saved unless' in memory_policy
    assert 'MemoryTools_episode_create' in memory_policy
    assert 'preference_editor' in memory_policy
    assert 'reports `preference_organizing`' in memory_policy
    assert 'preference change was not saved because maintenance is in progress' in memory_policy
    assert 'Never claim or imply that the write was queued, retried, evicted, or replaced automatically' in memory_policy


def test_writer_tools_publish_stable_capability_ids():
    capabilities = {
        config.name: config.capability_id
        for config in DEFAULT_TOOLS
        if config.name in {'writer_create', 'writer_revision'}
    }
    assert capabilities == {
        'writer_create': 'writer.create',
        'writer_revision': 'writer.revise',
    }


def test_shared_prompt_appendix_is_reused_and_deduplicated():
    configs = [
        cfg for cfg in DEFAULT_TOOLS
        if cfg.name in {'image_generator', 'image_editor', 'video_to_gif'}
    ]

    assert len(configs) == 3
    assert all(
        cfg.appendix_system_prompt is IMAGE_GENERATION_PROMPT_APPENDIX
        for cfg in configs
        if cfg.name != 'video_to_gif'
    )
    assert next(
        cfg for cfg in configs if cfg.name == 'video_to_gif'
    ).appendix_system_prompt is IMAGE_MARKDOWN_OUTPUT_APPENDIX
    collected = collect_system_prompt_appendices(configs)
    assert collected['output_contract'] == list(
        IMAGE_MARKDOWN_OUTPUT_APPENDIX['output_contract']
    )

    with_dynamic_attachment = collect_system_prompt_appendices(
        configs,
        extra_appendices=(IMAGE_MARKDOWN_OUTPUT_APPENDIX,),
    )
    assert with_dynamic_attachment == collected


def test_knowledge_base_priority_policy_is_not_globally_attached():
    kb_config = next(cfg for cfg in DEFAULT_TOOLS if cfg.name == 'kb')
    lazyllm.globals['agentic_config'] = {'filters': {}}
    default_appendices = collect_system_prompt_appendices([kb_config])
    lazyllm.globals['agentic_config'] = {'filters': {'kb_id': 'selected-kb'}}
    selected_appendices = collect_system_prompt_appendices([kb_config])

    assert not any(
        'Selected Knowledge Base Rules' in item
        for item in default_appendices.get('tool_policy', [])
    )
    assert any(
        'Selected Knowledge Base Rules' in item
        for item in selected_appendices['tool_policy']
    )


def test_document_preview_chat_replaces_mandatory_knowledge_search_policy():
    kb_config = next(cfg for cfg in DEFAULT_TOOLS if cfg.name == 'kb')
    lazyllm.globals['agentic_config'] = {
        'filters': {'kb_id': 'selected-kb'},
        'document_preview_chat': True,
        'document_selection_context_available': True,
    }

    appendices = collect_system_prompt_appendices([kb_config])

    assert any('Document Preview Chat Rules' in item for item in appendices['tool_policy'])
    assert not any('Selected Knowledge Base Rules' in item for item in appendices['tool_policy'])


def test_main_chat_keeps_mandatory_search_even_when_it_has_a_citation():
    kb_config = next(cfg for cfg in DEFAULT_TOOLS if cfg.name == 'kb')
    lazyllm.globals['agentic_config'] = {
        'filters': {'kb_id': 'selected-kb'},
        'document_preview_chat': False,
        'document_selection_context_available': True,
    }

    appendices = collect_system_prompt_appendices([kb_config])

    assert any('Selected Knowledge Base Rules' in item for item in appendices['tool_policy'])
    assert not any('Document Preview Chat Rules' in item for item in appendices['tool_policy'])


def test_conditional_prompt_appendix_provider_can_disable_itself():
    enabled = False
    config = ToolConfig(
        name='conditional', label='conditional', description='conditional',
        tool=lambda: None, module='utility',
        appendix_system_prompt=lambda: {'tool_policy': 'Enabled policy.'} if enabled else None,
    )

    assert collect_system_prompt_appendices([config]) == {}
    enabled = True
    assert collect_system_prompt_appendices([config]) == {'tool_policy': ['Enabled policy.']}


def test_search_tool_descriptions_distinguish_open_web_from_encyclopedic_lookup():
    web_config = next(cfg for cfg in DEFAULT_TOOLS if cfg.name == 'web_search')
    wikipedia_config = next(cfg for cfg in DEFAULT_TOOLS if cfg.name == 'wikipedia')
    policy = '\n'.join(collect_system_prompt_appendices([web_config])['tool_policy'])

    assert 'Wikipedia' not in policy
    assert 'current information' in web_config.tool['desc']
    assert 'stable encyclopedic background' in wikipedia_config.description_en
    assert 'not for news' in wikipedia_config.description_en


def test_external_retrieval_tools_share_the_citation_output_contract():
    configs = [
        cfg for cfg in DEFAULT_TOOLS
        if cfg.name in {'web_search', 'academic_search', 'wikipedia', 'url_fetch'}
    ]
    collected = collect_system_prompt_appendices(configs)

    assert collected['output_contract'] == list(RETRIEVAL_CITATION_OUTPUT_APPENDIX['output_contract'])
    assert any('exact `target_url`' in item for item in collected['tool_policy'])
    assert any('get_content' in item for item in collected['tool_policy'])


def test_mixed_kb_and_web_tools_share_one_citation_output_contract():
    configs = [
        cfg for cfg in DEFAULT_TOOLS
        if cfg.name in {'kb', 'web_search'}
    ]
    lazyllm.globals['agentic_config'] = {'filters': {'kb_id': 'selected-kb'}}
    collected = collect_system_prompt_appendices(configs)

    citation_contracts = [
        item for item in collected['output_contract']
        if 'Retrieval evidence citation rules' in item
    ]
    assert citation_contracts == list(RETRIEVAL_CITATION_OUTPUT_APPENDIX['output_contract'])
    contract = '\n'.join(citation_contracts)
    assert 'cite the supporting `ref` exactly once at the end of the paragraph' in contract
    assert 'do not add a citation merely because' in contract
    assert '[[document.chunk]]' not in contract
    assert 'the final answer must copy at least one of those `ref` values exactly' not in contract
    assert 'cite at least one result from each category' not in contract

    policy = '\n'.join(collected['tool_policy'])
    assert 'cite at least one result from each category' not in policy


def test_prompt_appendix_deduplication_normalizes_whitespace():
    first = ToolConfig(
        name='first', label='first', description='first', tool=lambda: None, module='utility',
        appendix_system_prompt={'safety': 'Confirm before writing external data.'},
    )
    second = ToolConfig(
        name='second', label='second', description='second', tool=lambda: None, module='utility',
        appendix_system_prompt={'safety': ' Confirm  before\nwriting external data. '},
    )

    assert collect_system_prompt_appendices([first, second]) == {
        'safety': ['Confirm before writing external data.'],
    }


def test_cloud_files_use_nested_supplier_toolkits():
    from lazyllm.tools.agent.toolsManager import ToolManager
    from lazymind.chat.lazyllm_tool_docs import ensure_lazyllm_tool_docs

    config = next(cfg for cfg in DEFAULT_TOOLS if cfg.name == 'cloud_files')
    ensure_lazyllm_tool_docs([config.tool])
    manager = ToolManager([config.tool])
    names = {item['function']['name'] for item in manager.tools_description}
    assert names == {'get_CloudFileToolkit_methods'}
    manager._tool_call['get_CloudFileToolkit_methods']({})
    names = {item['function']['name'] for item in manager.tools_description}
    assert not any(name.endswith('_read') for name in names)


def test_pick_first_valid_agent_tool_uses_group_config_description():
    lazyllm.globals.config['dynamic_tool_auth'] = {'bocha': 'bocha-token'}

    web_search_cfg = next(cfg for cfg in filter_tools(DEFAULT_TOOLS) if cfg.name == 'web_search')
    agent_tool = web_search_cfg.tool

    assert agent_tool['name'] == 'WebSearchToolkit'
    assert agent_tool['pick_first_valid'] is True
    assert agent_tool['tools']


def test_tool_catalog_localizes_display_fields_without_changing_runtime_description():
    zh_group = next(group for group in get_all_tool_groups('zh-CN') if group['name'] == 'web_search')
    en_group = next(group for group in get_all_tool_groups('en-US') if group['name'] == 'web_search')
    unsupported_group = next(group for group in get_all_tool_groups('fr-FR') if group['name'] == 'web_search')

    assert zh_group['label'] == '网页搜索'
    assert en_group['label'] == 'Web Search'
    assert en_group['description'] == (
        'Search the open internet for current information and broad research using the first '
        'available search provider.'
    )
    assert unsupported_group['label'] == zh_group['label']
    assert unsupported_group['description'] == zh_group['description']
    assert en_group['name'] == zh_group['name']
    assert en_group['methods'] == zh_group['methods']

    config = next(cfg for cfg in DEFAULT_TOOLS if cfg.name == 'web_search')
    agent_tool = config.tool
    assert agent_tool['desc']

    for group_config in [*DEFAULT_TOOLS, SKILL_TOOL_CONFIG]:
        assert group_config.label_en.strip()
        assert group_config.description_en.strip()


def test_workspace_metadata_reads_search_writer_and_mail_declarations():
    from lazyllm.tools.agent import ToolManager
    tools = [cfg.tool for cfg in DEFAULT_TOOLS if cfg.name in {'web_search', 'academic_search', 'writer_create', 'writer_revision', 'mail'}]
    from lazymind.chat.lazyllm_tool_docs import ensure_lazyllm_tool_docs
    ensure_lazyllm_tool_docs(tools)
    manager = ToolManager(tools)
    metadata = {name: tool.runtime_metadata for name, tool in manager.tools_info.items()}
    assert 'WriterCreateToolkit_render_markdown' in metadata
    assert 'WriterCreateToolkit_generate_outline' in metadata
    assert 'WriterRevisionToolkit_apply_string_replace' in metadata
    assert any(name.endswith('GoogleSearch_search') for name in metadata)
    assert any(name.endswith('SciverseSearch_meta_search') for name in metadata)
    assert 'WriterCreateToolkit_profile_resources' in metadata
    assert 'WriterCreateToolkit_generate_draft_section' in metadata
    assert any(name == 'MailToolkit_send_draft' for name in metadata)
    from lazyllm.tools.agent.tool_runtime import HostFileAccess
    assert metadata['WriterRevisionToolkit_apply_patch'].host_file_access is HostFileAccess.DECLARED
    assert metadata['WriterRevisionToolkit_apply_patch'].host_file_resolver is not None
    assert metadata['MailToolkit_send_draft'].host_file_access is HostFileAccess.NONE


def test_scoped_service_declarations_do_not_grant_lookalikes_capabilities():
    from lazyllm.tools.agent import ToolManager
    selected = {'external_db', 'memory', 'skill_editor', 'cloud_files', 'mail', 'vocab_learn', 'schedule'}
    for config in DEFAULT_TOOLS:
        if config.name not in selected:
            continue
        manager = ToolManager([config.tool])
        metadata = {name: tool.runtime_metadata for name, tool in manager.tools_info.items()}
        assert all(item.host_file_access is not HostFileAccess.UNDECLARED for item in metadata.values()), config.name

    class Lookalike:
        __public_apis__ = ['read']
        def read(self, path: str) -> str:
            """Read a lookalike path."""
            return path

    manager = ToolManager([Lookalike()])
    metadata = {name: tool.runtime_metadata for name, tool in manager.tools_info.items()}
    read_metadata = next(item for name, item in metadata.items() if name.endswith('_read'))
    assert read_metadata.host_file_access is HostFileAccess.UNDECLARED


def test_writer_markdown_sections_keep_existing_path_text_literal(tmp_path):
    from lazymind.document_tools.artifacts import _inline_draft_sections
    from lazyllm.tools.writer.tools.base import WriterToolBase
    private = tmp_path / 'private.md'
    private.write_text('must not be read as drafting content')
    artifacts = tmp_path / 'artifacts'
    artifacts.mkdir()
    normalized = _inline_draft_sections(artifacts, [str(private), '# Inline section'])
    tools = WriterToolBase(llm=None, artifact_store=str(artifacts))
    assert [tools._unified_section(item) for item in normalized] == [str(private), '# Inline section']


def test_workspace_skill_capabilities_are_owned_by_skill_implementations(tmp_path):
    from lazyllm.tools.agent import ToolManager
    from lazyllm.tools.agent.skill_manager import SkillManager
    from lazyllm.tools.fs.client import FS
    from lazymind.chat.engine.agent_runtime.executor import _skill_filesystem
    root, bound = tmp_path / 'skills', tmp_path / 'bound'
    skill = root / 'visible'
    skill.mkdir(parents=True)
    bound.mkdir()
    (skill / 'SKILL.md').write_text('---\nname: visible\ndescription: Reader fixture\n---\n# Visible\n')
    (skill / 'guide.md').write_text('normal reference')
    lazyllm.globals['agentic_config'] = {
        'user_id': 'u', 'conversation_id': 'c',
        'workspace_context': {
            'workspace_id': 'w', 'root': str(bound), 'workspace_version': 1,
            'permission_mode': 'always_ask', 'permission_version': 1,
        },
    }
    skill_fs = _skill_filesystem(FS, str(root))
    skills = SkillManager(dir=str(root), fs=skill_fs)
    manager = ToolManager(skills.get_skill_tools())
    metadata = {name: tool.runtime_metadata for name, tool in manager.tools_info.items()}
    assert set(metadata) == {'get_skill', 'read_reference', 'run_script'}
    assert 'visible' in skills.build_prompt()
    assert skills.read_reference('visible', 'guide.md')['content'] == 'normal reference'
    from lazyllm.tools.agent.tool_runtime import HostFileAccess
    assert metadata['get_skill'].host_file_access is HostFileAccess.NONE
    assert metadata['read_reference'].host_file_access is HostFileAccess.NONE
    assert metadata['run_script'].host_file_access is HostFileAccess.OPAQUE
    unguarded = SkillManager(dir=str(root), fs=skill_fs)
    assert all(tool.runtime_metadata.host_file_access is not HostFileAccess.UNDECLARED
               for tool in ToolManager(unguarded.get_skill_tools()).tools_info.values())


def test_workspace_remote_skill_reader_keeps_core_http_auth(monkeypatch, tmp_path):
    import json
    import threading
    from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
    from urllib.parse import parse_qs, urlsplit
    from lazyllm.tools.agent import ToolManager
    from lazyllm.tools.agent.skill_manager import SkillManager
    from lazyllm.tools.fs.client import FS
    from lazymind.config import config
    from lazymind.chat.engine.agent_runtime.tool_call_guard import ToolExecutionMiddleware
    files = {'skills/system/demo/SKILL.md': b'---\nname: demo\ndescription: Remote fixture\n---\n# Demo',
             'skills/system/demo/guide.md': b'Remote reference through Core'}
    requests_seen = []
    class Handler(BaseHTTPRequestHandler):
        def do_GET(self):
            url = urlsplit(self.path)
            query = parse_qs(url.query)
            path = query['path'][0]
            requests_seen.append((url.path, path, query.get('user_id'), self.headers.get('X-LazyMind-Internal-Token')))
            if url.path.endswith('/list'):
                paths = ['skills/system/demo'] if path == 'skills' else list(files)
                body = json.dumps({'items': [{'path': item, 'type': 'file' if item in files else 'directory'} for item in paths]}).encode()
            elif url.path.endswith('/info'):
                body = json.dumps({'size': len(files[path])}).encode()
            else:
                body = files[path]
            self.send_response(200)
            self.end_headers()
            self.wfile.write(body)
        def log_message(self, *_args):
            pass
    server = ThreadingHTTPServer(('127.0.0.1', 0), Handler)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    old_url, old_token = config['core_api_url'], config['core_internal_token']
    config['core_api_url'] = f'http://127.0.0.1:{server.server_port}'
    config['core_internal_token'] = 'local-test-token'
    # FS is a process singleton; isolate its configured RemoteFS instance instead
    # of changing production base-URL resolution to compensate for test order.
    monkeypatch.setattr(FS, '_instances', {})
    lazyllm.globals['agentic_config'] = {
        'user_id': 'owner', 'conversation_id': 'conversation',
        '_core_workspace_context': {'workspace_id': 'workspace'},
    }
    try:
        skills = SkillManager(dir='remote://skills', fs=FS)
        manager = ToolManager(skills.get_skill_tools())
        from lazymind.chat.engine.tools.workspace_context import WorkspaceContext
        middleware = ToolExecutionMiddleware(
            manager, workspace_permission=WorkspaceContext.from_config(lazyllm.globals['agentic_config']))
        result = middleware.execute_with_records({'id': 'read', 'function': {
            'name': 'read_reference', 'arguments': {'name': 'demo', 'rel_path': 'guide.md'},
        }})
        assert result.results[0]['ok'], (result.results, requests_seen)
        assert result.results[0]['value']['content'] == 'Remote reference through Core'
        assert requests_seen and all(item[2:] == (['owner'], 'local-test-token') for item in requests_seen)
        assert all(item[1].startswith('skills') for item in requests_seen)
    finally:
        server.shutdown()
        server.server_close()
        thread.join()
        config['core_api_url'], config['core_internal_token'] = old_url, old_token


@pytest.mark.parametrize('dependency', ['other_toolkit', 'overridden_method', 'client_factory', 'session_callback', 'env_store'])
def test_factory_captured_dependencies_cannot_read_bound_files(tmp_path, dependency):
    from lazyllm.tools.agent import ToolManager, ToolExecutionDisposition
    from lazymind.chat.engine.agent_runtime.tool_call_guard import ToolExecutionMiddleware
    from lazymind.chat.engine.tools.session_env import build_session_env_tool
    from lazymind.chat.workflow import workflow_manager as workflows
    marker = tmp_path / 'bound.txt'
    marker.write_text('non-sensitive bound fixture')
    effects = []
    def read_host(*_args):
        effects.append(marker.read_text())
        return {'read': True}
    class OtherToolkit:
        get_workflow_state = staticmethod(read_host)
    class OtherClient:
        get_state = staticmethod(read_host)
    class OtherStore(dict):
        def setdefault(self, *_args):
            read_host()
            return {}
    toolkit = workflows.HostWorkflowToolkit(workflows._client)
    session = 'session'
    if dependency == 'other_toolkit':
        toolkit = OtherToolkit()
    elif dependency == 'overridden_method':
        toolkit.get_workflow_state = read_host
    elif dependency == 'client_factory':
        toolkit = workflows.HostWorkflowToolkit(lambda: OtherClient())
    elif dependency == 'session_callback':
        def session():
            read_host()
            raise ValueError('stop before any network request')
    if dependency == 'env_store':
        tool = build_session_env_tool(OtherStore(), 'conversation')
        arguments = {'name': 'FIXTURE_TOKEN', 'value': 'fake'}
    else:
        tool = workflows._safe_session_tools(toolkit, session)[0]
        arguments = {}
    lazyllm.globals['agentic_config'] = {
        'user_id': 'u', 'conversation_id': 'c', '_core_workspace_context': {'workspace_id': 'bound'},
    }
    manager = ToolManager([tool])
    from lazymind.chat.engine.tools.workspace_context import WorkspaceContext
    middleware = ToolExecutionMiddleware(
        manager,
        workspace_permission=WorkspaceContext.from_config(lazyllm.globals['agentic_config']),
    )
    result = middleware.execute_with_records({'id': 'read', 'function': {'name': tool.__name__, 'arguments': arguments}})
    assert effects == []
    assert result.records[0].disposition is ToolExecutionDisposition.SKIPPED


def test_factory_code_with_foreign_globals_is_not_admitted(tmp_path):
    import types
    from lazyllm.tools.agent import ToolManager
    from lazymind.chat.engine.tools.skill_listing import build_list_skills_tool
    original = build_list_skills_tool(['known'])
    foreign = types.FunctionType(original.__code__, {**original.__globals__, 'len': lambda _: 0},
                                 original.__name__, original.__defaults__, original.__closure__)
    foreign.__doc__, foreign.__annotations__ = original.__doc__, original.__annotations__
    manager = ToolManager([foreign])
    assert manager.tools_info['list_skills'].runtime_metadata.host_file_access is HostFileAccess.UNDECLARED


def test_all_real_project_factories_remain_admitted_with_known_dependencies():
    from lazyllm.tools.agent import ToolManager
    from lazymind.chat.engine.tools.file_resources.tools import build_resource_read_tools
    from lazymind.chat.engine.tools.intent_writer import build_intentwrite_tool
    from lazymind.chat.engine.tools.skill_listing import build_list_skills_tool
    from lazymind.chat.engine.tools.session_env import build_session_env_tool
    from lazymind.chat.engine.tools.calculator import calculator
    from lazymind.chat.workflow import workflow_manager as workflows
    lazyllm.globals['agentic_config'] = {
        'user_id': 'u', 'conversation_id': 'c', '_core_workspace_context': {'workspace_id': 'bound'},
    }
    toolkit = workflows.HostWorkflowToolkit(workflows._client, origin_ref='c')
    activation = {'workflow_id': 'demo', 'workflow_ref': 'demo', 'tool_name': 'trigger_demo_workflow'}
    contribution = workflows.resolve_workflow_injection(
        None, conversation_id='c', current_query='make a draft', workflow_activations=[activation],
    )
    groups = [
        [{'name': 'fixture', 'desc': 'Known arithmetic tools', 'tools': [calculator], 'lazy': True}],
        build_resource_read_tools(),
        [build_intentwrite_tool(conversation_id='c', current_query='make a draft')],
        [build_list_skills_tool(['known'])],
        [build_session_env_tool({}, 'c')],
        [workflows._handoff_tool('session', 'make a draft')],
        workflows._safe_session_tools(toolkit, 'session'),
        workflows._safe_authoring_tools(toolkit),
        workflows._workflow_trigger_tools([activation], [], 'make a draft', 'c'),
        contribution.tools,
    ]
    for tools in groups:
        manager = ToolManager(tools)
        metadata = {name: tool.runtime_metadata for name, tool in manager.tools_info.items()}
        assert all(item.host_file_access is not HostFileAccess.UNDECLARED for item in metadata.values())


@pytest.mark.parametrize('callback_position', ['initialize_session', 'user_input', 'handoff_session', 'handoff_user_input'])
def test_workflow_factory_rejects_unreviewed_callback_chains(callback_position):
    from lazyllm.tools.agent import ToolManager
    from lazymind.chat.workflow import workflow_manager as workflows
    toolkit = workflows.HostWorkflowToolkit(workflows._client)
    def unreviewed():
        pytest.fail('an unreviewed callback must not execute')
    if callback_position == 'initialize_session':
        tool = workflows._safe_session_tools(toolkit, '', initialize_session=unreviewed)[0]
    elif callback_position == 'user_input':
        tool = workflows._safe_session_tools(toolkit, 'session', user_input=unreviewed)[2]
    elif callback_position == 'handoff_session':
        tool = workflows._handoff_tool(unreviewed)
    else:
        tool = workflows._handoff_tool('session', user_input=unreviewed)
    manager = ToolManager([tool])
    assert manager.tools_info[tool.__name__].runtime_metadata.host_file_access is HostFileAccess.UNDECLARED

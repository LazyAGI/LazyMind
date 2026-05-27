import importlib
import sys
from types import ModuleType, SimpleNamespace
from dataclasses import dataclass


def _import_agentic_module(monkeypatch):
    fake_lazyllm = ModuleType('lazyllm')
    fake_lazyllm.LOG = SimpleNamespace(
        info=lambda *args, **kwargs: None,
        debug=lambda *args, **kwargs: None,
        warning=lambda *args, **kwargs: None,
        error=lambda *args, **kwargs: None,
    )
    fake_lazyllm.bind = lambda *args, **kwargs: ('bind', args, kwargs)
    fake_lazyllm.loop = lambda *args, **kwargs: ('loop', args, kwargs)
    fake_lazyllm.once_wrapper = lambda *a, **kw: (lambda fn: fn)
    fake_lazyllm.pipeline = lambda *args, **kwargs: None
    fake_lazyllm.switch = lambda *args, **kwargs: ('switch', args, kwargs)
    fake_lazyllm.AutoModel = lambda model, config=False: f'model:{model}'
    fake_lazyllm.ThreadPoolExecutor = None  # patched per-test if needed

    class _FakeDocument:
        def __init__(self, *args, **kwargs):
            pass

    fake_lazyllm.Document = _FakeDocument

    fake_lazyllm.fc_register = lambda *a, **kw: (lambda fn: fn)

    # Sub-modules that agentic.py imports from lazyllm
    fake_lazyllm_tools = ModuleType('lazyllm.tools')
    fake_lazyllm_tools_agent = ModuleType('lazyllm.tools.agent')
    fake_lazyllm_tools_rag = ModuleType('lazyllm.tools.rag')
    fake_lazyllm_tools_tools = ModuleType('lazyllm.tools.tools')
    fake_lazyllm_tools_tools_search = ModuleType('lazyllm.tools.tools.search')
    fake_lazyllm_components = ModuleType('lazyllm.components')
    fake_lazyllm_components_formatter = ModuleType('lazyllm.components.formatter')

    class _FakeReactAgent:
        def __init__(self, *args, **kwargs):
            pass

    fake_lazyllm_tools_agent.ReactAgent = _FakeReactAgent

    class _FakeRetriever:
        def __init__(self, *args, **kwargs):
            pass

    class _FakeTempDocRetriever:
        def __init__(self, *args, **kwargs):
            pass

        def add_subretriever(self, *args, **kwargs):
            return None

    class _FakeReranker:
        def __init__(self, *args, **kwargs):
            pass

    class _FakeArxivSearch:
        def __init__(self, *args, **kwargs):
            pass

    class _FakeSearchProvider:
        def __init__(self, *args, **kwargs):
            pass

    fake_lazyllm_tools_rag.Retriever = _FakeRetriever
    fake_lazyllm_tools_rag.TempDocRetriever = _FakeTempDocRetriever
    fake_lazyllm_tools_rag.Reranker = _FakeReranker
    fake_lazyllm_tools_rag.Document = _FakeDocument
    fake_lazyllm_tools_tools_search.ArxivSearch = _FakeArxivSearch
    fake_lazyllm_tools_tools_search.BingSearch = _FakeSearchProvider
    fake_lazyllm_tools_tools_search.BochaSearch = _FakeSearchProvider
    fake_lazyllm_tools_tools_search.GoogleSearch = _FakeSearchProvider
    fake_lazyllm_tools_tools_search.WikipediaSearch = _FakeSearchProvider
    fake_lazyllm_components_formatter.encode_query_with_filepaths = lambda query, filepaths: query
    fake_lazyllm_tools_agent_fc = ModuleType('lazyllm.tools.agent.functionCall')
    fake_lazyllm_tools_agent_skill_manager = ModuleType('lazyllm.tools.agent.skill_manager')

    class _FakeFunctionCall:
        def __init__(self, *args, **kwargs):
            pass

    fake_lazyllm_tools_agent_fc.FunctionCall = _FakeFunctionCall

    class _FakeSkillManager:
        @staticmethod
        def _extract_protocol(raw):
            return ''

    fake_lazyllm_tools_agent_skill_manager.SkillManager = _FakeSkillManager
    fake_lazyllm_tools_fs = ModuleType('lazyllm.tools.fs')
    fake_lazyllm_tools_fs_client = ModuleType('lazyllm.tools.fs.client')
    fake_lazyllm_tools_fs_client.FS = object
    fake_lazyllm_tools_fs_supplier = ModuleType('lazyllm.tools.fs.supplier')
    fake_lazyllm_tools_fs_supplier_feishu = ModuleType('lazyllm.tools.fs.supplier.feishu')
    class _FakeFeishuFS:
        def __init__(self, *args, **kwargs):
            pass
    class _FakeLazyLLMFSBase:
        def __init__(self, *args, **kwargs):
            pass
    fake_lazyllm_tools_fs.LazyLLMFSBase = _FakeLazyLLMFSBase
    fake_lazyllm_tools_fs_supplier_feishu.FeishuFS = _FakeFeishuFS
    fake_lazyllm_tools_sandbox = ModuleType('lazyllm.tools.sandbox')
    fake_lazyllm_tools_sandbox_base = ModuleType('lazyllm.tools.sandbox.sandbox_base')
    fake_lazyllm_tools_sandbox_base.create_sandbox = lambda *a, **kw: None
    fake_lazyllm_tracing = ModuleType('lazyllm.tracing')
    fake_lazyllm_tracing.set_trace_context = lambda *a, **kw: None

    fake_tenacity = ModuleType('tenacity')
    fake_tenacity.retry = lambda *args, **kwargs: (lambda fn: fn)
    fake_tenacity.stop_after_attempt = lambda count: count
    fake_tenacity.wait_fixed = lambda delay: delay

    fake_prompts = ModuleType('lazymind.chat.engine.prompts.agentic')
    template = SimpleNamespace(substitute=lambda **kwargs: '{}', format=lambda **kwargs: 'formatted')
    fake_prompts.EVALUATOR_PROMPT = template
    fake_prompts.EXTRACTOR_PROMPT = template
    fake_prompts.GENERATE_PROMPT = template
    fake_prompts.PLANREFINE_PROMPT = template
    fake_prompts.PLANNER_PROMPT = template
    fake_prompts.TOOLCALL_PROMPT = template
    # Symbols used by lazymind.chat.service.agentic.config
    fake_prompts.CITATION_GUIDANCE = ''
    fake_prompts.DEFAULT_SYSTEM_PROMPT = ''
    fake_prompts.IMAGE_REFERENCE_MARKDOWN_GUIDANCE = ''
    fake_prompts.MEMORY_GUIDANCE = ''
    fake_prompts.SEARCH_GUIDANCE = ''
    fake_prompts.SKILLS_GUIDANCE = ''
    fake_prompts.TOOL_CALL_STATUS_GUIDANCE = ''
    fake_prompts.VISION_EXTRACTOR_GUIDANCE = ''
    fake_prompts.VOCAB_GUIDANCE = ''
    fake_prompts.VISION_EXTRACT_DEFAULT_INSTRUCTION = ''
    fake_review_prompts = ModuleType('lazymind.review.prompts')
    fake_review_prompts._COMBINED_REVIEW_PROMPT = ''
    fake_review_prompts._MEMORY_REVIEW_PROMPT = ''
    fake_review_prompts._SKILL_REVIEW_PROMPT = ''

    # Fake deep dependency modules to avoid import chain issues
    fake_review = ModuleType('lazymind.review.service.review')
    fake_review._build_review_decision = lambda *a, **kw: {'mode': None}
    fake_review._spawn_background_review = lambda *a, **kw: None

    fake_skill_manager = ModuleType('lazymind.chat.engine.tools.skill_manager')
    fake_skill_manager.list_all_skills_with_category = lambda *a, **kw: []

    class _FakeSkillManagerToolGroup:
        pass

    fake_skill_manager.SkillManagerToolGroup = _FakeSkillManagerToolGroup
    fake_local_models = ModuleType('lazymind.online_models.local_models')
    fake_tools_algo = ModuleType('lazymind.chat.engine.tools.algo')
    fake_tools_algo.ppl_search = lambda *a, **kw: []
    fake_vocab_db = ModuleType('lazymind.vocab.service.db')
    fake_vocab_db.fetch_chat_histories_for_session = lambda *a, **kw: []
    fake_vocab_db.fetch_vocab_groups_for_user_id = lambda *a, **kw: []
    fake_vocab_evolution = ModuleType('lazymind.vocab.engine.evolution')

    @dataclass
    class _FakeChatHistoryRecord:
        message_id: str = ''
        role: str = ''
        content: str = ''

    @dataclass
    class _FakeVocabEvolutionRequest:
        suggestions: list | None = None

    class _FakeActionPlanningModule:
        def __init__(self, *args, **kwargs):
            pass

    fake_vocab_evolution.ActionPlanningModule = _FakeActionPlanningModule
    fake_vocab_evolution.ChatHistoryRecord = _FakeChatHistoryRecord
    fake_vocab_evolution.SynonymCandidate = dict
    fake_vocab_evolution.VocabEvolutionRequest = _FakeVocabEvolutionRequest

    # Clear cached module so it gets re-imported with our fakes
    for name in list(sys.modules.keys()):
        if name == 'lazymind.chat.service.agentic.runtime':
            sys.modules.pop(name, None)

    # Fake config module so agentic.py's `from lazymind.config import config as _cfg` works
    fake_config_mod = ModuleType('lazymind.config')
    fake_config_mod.config = {
        'max_retries': 20,
        'memory_review_interval': 1,
        'skill_review_interval': 5,
        'model_config_path': 'dynamic',
        'skill_fs_url': 'remote://skills',
        'agentic_keep_full_turns': 3,
        'agentic_workspace': './workspace',
        'agentic_kb_url': 'http://example.test/kb',
        'agentic_kb_name': '__default__',
        'opensearch_uri': '',
        'opensearch_user': '',
        'opensearch_password': '',
    }

    monkeypatch.setitem(sys.modules, 'lazymind.config', fake_config_mod)
    monkeypatch.setitem(sys.modules, 'lazyllm', fake_lazyllm)
    monkeypatch.setitem(sys.modules, 'lazyllm.tools', fake_lazyllm_tools)
    monkeypatch.setitem(sys.modules, 'lazyllm.tools.agent', fake_lazyllm_tools_agent)
    monkeypatch.setitem(sys.modules, 'lazyllm.tools.rag', fake_lazyllm_tools_rag)
    monkeypatch.setitem(sys.modules, 'lazyllm.tools.tools', fake_lazyllm_tools_tools)
    monkeypatch.setitem(sys.modules, 'lazyllm.tools.tools.search', fake_lazyllm_tools_tools_search)
    monkeypatch.setitem(sys.modules, 'lazyllm.components', fake_lazyllm_components)
    monkeypatch.setitem(sys.modules, 'lazyllm.components.formatter', fake_lazyllm_components_formatter)
    fake_lazyllm.tools = fake_lazyllm_tools
    fake_lazyllm_tools.agent = fake_lazyllm_tools_agent
    fake_lazyllm_tools.rag = fake_lazyllm_tools_rag
    fake_lazyllm_tools.tools = fake_lazyllm_tools_tools
    fake_lazyllm_tools_tools.search = fake_lazyllm_tools_tools_search
    fake_lazyllm.components = fake_lazyllm_components
    fake_lazyllm_components.formatter = fake_lazyllm_components_formatter
    monkeypatch.setitem(sys.modules, 'lazyllm.tools.agent.functionCall', fake_lazyllm_tools_agent_fc)
    monkeypatch.setitem(sys.modules, 'lazyllm.tools.agent.skill_manager', fake_lazyllm_tools_agent_skill_manager)
    monkeypatch.setitem(sys.modules, 'lazyllm.tools.fs', fake_lazyllm_tools_fs)
    monkeypatch.setitem(sys.modules, 'lazyllm.tools.fs.client', fake_lazyllm_tools_fs_client)
    monkeypatch.setitem(sys.modules, 'lazyllm.tools.fs.supplier', fake_lazyllm_tools_fs_supplier)
    monkeypatch.setitem(sys.modules, 'lazyllm.tools.fs.supplier.feishu', fake_lazyllm_tools_fs_supplier_feishu)
    monkeypatch.setitem(sys.modules, 'lazyllm.tools.sandbox', fake_lazyllm_tools_sandbox)
    monkeypatch.setitem(sys.modules, 'lazyllm.tools.sandbox.sandbox_base', fake_lazyllm_tools_sandbox_base)
    monkeypatch.setitem(sys.modules, 'lazyllm.tracing', fake_lazyllm_tracing)
    monkeypatch.setitem(sys.modules, 'tenacity', fake_tenacity)
    monkeypatch.setitem(sys.modules, 'lazymind.chat.engine.prompts.agentic', fake_prompts)
    monkeypatch.setitem(sys.modules, 'lazymind.review.prompts', fake_review_prompts)
    monkeypatch.setitem(sys.modules, 'lazymind.review.service.review', fake_review)
    monkeypatch.setitem(sys.modules, 'lazymind.chat.engine.tools.skill_manager', fake_skill_manager)
    monkeypatch.setitem(sys.modules, 'lazymind.chat.engine.tools.algo', fake_tools_algo)
    monkeypatch.setitem(sys.modules, 'lazymind.online_models.local_models', fake_local_models)
    monkeypatch.setitem(sys.modules, 'lazymind.vocab.service.db', fake_vocab_db)
    monkeypatch.setitem(sys.modules, 'lazymind.vocab.engine.evolution', fake_vocab_evolution)

    return importlib.import_module('lazymind.chat.service.agentic.runtime')


def test_agentic_module_exports_expected_functions(monkeypatch):
    # Verify the public API surface of the agentic module.
    module = _import_agentic_module(monkeypatch)

    assert callable(module.agentic_rag)


def test_agentic_module_exports_stream_entry(monkeypatch):
    module = _import_agentic_module(monkeypatch)

    assert hasattr(module, '_agentic_forward_stream')


def test_agentic_rag_requires_query(monkeypatch):
    module = _import_agentic_module(monkeypatch)

    try:
        module.agentic_rag({})
    except ValueError as exc:
        assert 'query' in str(exc).lower()
    else:
        raise AssertionError('agentic_rag should raise ValueError when query is missing')


def test_agentic_rag_requires_non_empty_query(monkeypatch):
    module = _import_agentic_module(monkeypatch)

    try:
        module.agentic_rag({'query': '   '})
    except ValueError as exc:
        assert 'query' in str(exc).lower()
    else:
        raise AssertionError('agentic_rag should raise ValueError for blank query')


def test_lazyllm_queue_db_path_is_path_like(monkeypatch):
    # _lazyllm_queue_db_path() calls lazyllm.configs internally; just verify
    # the function exists and returns something with a 'name' attribute when
    # lazyllm.configs is available (i.e. in the real import context).
    import importlib
    real_module = importlib.import_module('lazymind.chat.service.agentic.runtime')
    path = real_module._lazyllm_queue_db_path()
    assert hasattr(path, 'name')


def test_build_agentic_run_context_uses_automodel(monkeypatch):
    # Verify stream context creation initializes AutoModel(model='llm', config=get_config_path()).
    module = _import_agentic_module(monkeypatch)

    automodel_calls = []

    class _FakeAgent:
        def __init__(self, llm, tools, **kwargs):
            automodel_calls.append(llm)

    monkeypatch.setattr(module, 'AutoModel', lambda model, config=False: f'model:{model}')

    class _FakeGlobals:
        _sid = 'test-sid'

        def get(self, key, default=None):
            return {}

        def _init_sid(self, sid):
            pass

        def __setitem__(self, key, value):
            pass

        def __getitem__(self, key):
            return {}

    module.lazyllm.globals = _FakeGlobals()
    module.lazyllm.locals = SimpleNamespace(
        get=lambda key, default=None: {},
        _init_sid=lambda sid: None,
        _sid='test-sid',
    )
    module.lazyllm.tools.agent.ReactAgent = _FakeAgent

    module._build_agentic_run_context(query='hello', stream=True)

    assert automodel_calls == ['model:llm']

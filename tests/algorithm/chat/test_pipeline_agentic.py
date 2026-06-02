import sys
from types import ModuleType, SimpleNamespace


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

    fake_lazyllm.fc_register = lambda *a, **kw: (lambda fn: fn)

    # Sub-modules that agentic.py imports from lazyllm
    fake_lazyllm_tools = ModuleType('lazyllm.tools')
    fake_lazyllm_tools_agent = ModuleType('lazyllm.tools.agent')

    class _FakeReactAgent:
        def __init__(self, *args, **kwargs):
            pass

    fake_lazyllm_tools_agent.ReactAgent = _FakeReactAgent
    fake_lazyllm_tools_agent_fc = ModuleType('lazyllm.tools.agent.functionCall')

    class _FakeFunctionCall:
        def __init__(self, *args, **kwargs):
            pass

    fake_lazyllm_tools_agent_fc.FunctionCall = _FakeFunctionCall
    fake_lazyllm_tools_fs = ModuleType('lazyllm.tools.fs')
    fake_lazyllm_tools_fs_client = ModuleType('lazyllm.tools.fs.client')
    fake_lazyllm_tools_fs_client.FS = object
    fake_lazyllm_tools_fs_supplier = ModuleType('lazyllm.tools.fs.supplier')
    fake_lazyllm_tools_fs_supplier_feishu = ModuleType('lazyllm.tools.fs.supplier.feishu')

    class _FakeFeishuFS:
        def __init__(self, *args, **kwargs):
            pass

    fake_lazyllm_tools_fs_supplier_feishu.FeishuFS = _FakeFeishuFS
    fake_lazyllm_tracing = ModuleType('lazyllm.tracing')
    fake_lazyllm_tracing.set_trace_context = lambda *a, **kw: None

    fake_lazyllm_tools_sandbox = ModuleType('lazyllm.tools.sandbox')
    fake_lazyllm_tools_sandbox_base = ModuleType('lazyllm.tools.sandbox.sandbox_base')
    fake_lazyllm_tools_sandbox_base.create_sandbox = lambda *a, **kw: None

    fake_tenacity = ModuleType('tenacity')
    fake_tenacity.retry = lambda *args, **kwargs: (lambda fn: fn)
    fake_tenacity.stop_after_attempt = lambda count: count
    fake_tenacity.wait_fixed = lambda delay: delay

    fake_prompts = ModuleType('chat.prompts.agentic')
    template = SimpleNamespace(substitute=lambda **kwargs: '{}', format=lambda **kwargs: 'formatted')
    fake_prompts.EVALUATOR_PROMPT = template
    fake_prompts.EXTRACTOR_PROMPT = template
    fake_prompts.GENERATE_PROMPT = template
    fake_prompts.PLANREFINE_PROMPT = template
    fake_prompts.PLANNER_PROMPT = template
    fake_prompts.TOOLCALL_PROMPT = template
    # Symbols used by chat.components.agentic.config
    fake_prompts.CITATION_GUIDANCE = ''
    fake_prompts.DEFAULT_SYSTEM_PROMPT = ''
    fake_prompts.IMAGE_REFERENCE_MARKDOWN_GUIDANCE = ''
    fake_prompts.VISION_EXTRACTOR_GUIDANCE = ''
    fake_prompts.MEMORY_GUIDANCE = ''
    fake_prompts.SEARCH_GUIDANCE = ''
    fake_prompts.SKILLS_GUIDANCE = ''
    fake_prompts.TOOL_CALL_STATUS_GUIDANCE = ''
    fake_prompts.VOCAB_GUIDANCE = ''
    fake_prompts._COMBINED_REVIEW_PROMPT = ''
    fake_prompts._MEMORY_REVIEW_PROMPT = ''
    fake_prompts._SKILL_REVIEW_PROMPT = ''

    # Fake deep dependency modules to avoid import chain issues
    fake_review = ModuleType('chat.components.agentic.review')
    fake_review._build_review_decision = lambda *a, **kw: {'mode': None}
    fake_review._spawn_background_review = lambda *a, **kw: None

    fake_skill_manager = ModuleType('chat.tools.skill_manager')
    fake_skill_manager.list_all_skills_with_category = lambda *a, **kw: []

    # Clear cached module so it gets re-imported with our fakes
    for name in list(sys.modules.keys()):
        if name in ('chat.pipelines.agentic', 'chat.pipelines'):
            sys.modules.pop(name, None)

    # Fake config module so agentic.py's `from config import config as _cfg` works
    fake_config_mod = ModuleType('config')
    fake_config_mod.config = {
        'max_retries': 20,
        'memory_review_interval': 1,
        'skill_review_interval': 5,
        'model_config_path': 'dynamic',
        'skill_fs_url': 'remote://skills',
        'agentic_keep_full_turns': 3,
        'agentic_workspace': './workspace',
    }

    # Fake chat.pipelines package for isolated agentic import
    import importlib.util
    real_pipelines_spec = importlib.util.find_spec('chat.pipelines')
    fake_pipelines_pkg = ModuleType('chat.pipelines')
    if real_pipelines_spec and real_pipelines_spec.submodule_search_locations:
        fake_pipelines_pkg.__path__ = list(real_pipelines_spec.submodule_search_locations)
    fake_pipelines_pkg.__package__ = 'chat.pipelines'

    # Fake chat.pipelines.get_ppl_search to avoid deep import chain
    fake_get_ppl_search_mod = ModuleType('chat.pipelines.get_ppl_search')
    fake_get_ppl_search_mod.get_ppl_search = lambda *a, **kw: None
    fake_get_ppl_search_mod.get_retriever = lambda *a, **kw: None
    fake_get_ppl_search_mod.get_remote_document = lambda *a, **kw: None

    monkeypatch.setitem(sys.modules, 'config', fake_config_mod)
    monkeypatch.setitem(sys.modules, 'chat.pipelines', fake_pipelines_pkg)
    monkeypatch.setitem(sys.modules, 'chat.pipelines.get_ppl_search', fake_get_ppl_search_mod)
    monkeypatch.setitem(sys.modules, 'lazyllm', fake_lazyllm)
    monkeypatch.setitem(sys.modules, 'lazyllm.tools', fake_lazyllm_tools)
    monkeypatch.setitem(sys.modules, 'lazyllm.tools.agent', fake_lazyllm_tools_agent)
    fake_lazyllm.tools = fake_lazyllm_tools
    fake_lazyllm_tools.agent = fake_lazyllm_tools_agent
    monkeypatch.setitem(sys.modules, 'lazyllm.tools.agent.functionCall', fake_lazyllm_tools_agent_fc)
    monkeypatch.setitem(sys.modules, 'lazyllm.tools.fs', fake_lazyllm_tools_fs)
    monkeypatch.setitem(sys.modules, 'lazyllm.tools.fs.client', fake_lazyllm_tools_fs_client)
    monkeypatch.setitem(sys.modules, 'lazyllm.tools.fs.supplier', fake_lazyllm_tools_fs_supplier)
    monkeypatch.setitem(sys.modules, 'lazyllm.tools.fs.supplier.feishu', fake_lazyllm_tools_fs_supplier_feishu)
    monkeypatch.setitem(sys.modules, 'lazyllm.tracing', fake_lazyllm_tracing)
    monkeypatch.setitem(sys.modules, 'lazyllm.tools.sandbox', fake_lazyllm_tools_sandbox)
    monkeypatch.setitem(sys.modules, 'lazyllm.tools.sandbox.sandbox_base', fake_lazyllm_tools_sandbox_base)
    monkeypatch.setitem(sys.modules, 'tenacity', fake_tenacity)
    monkeypatch.setitem(sys.modules, 'chat.prompts.agentic', fake_prompts)
    monkeypatch.setitem(sys.modules, 'chat.components.agentic.review', fake_review)
    monkeypatch.setitem(sys.modules, 'chat.tools.skill_manager', fake_skill_manager)

    return importlib.import_module('chat.pipelines.agentic')


def test_agentic_module_exports_expected_functions(monkeypatch):
    # Verify the public API surface of the agentic module.
    module = _import_agentic_module(monkeypatch)

    assert callable(module.agentic_rag)
    assert callable(module.agentic_forward)
    assert callable(module.get_ppl_agentic)
    assert callable(module._ensure_tools_registered)


def test_get_ppl_agentic_returns_agentic_rag(monkeypatch):
    module = _import_agentic_module(monkeypatch)

    result = module.get_ppl_agentic()

    assert result is module.agentic_rag


def test_agentic_rag_requires_query(monkeypatch):
    module = _import_agentic_module(monkeypatch)

    # Patch _ensure_tools_registered to avoid deep import chain
    monkeypatch.setattr(module, '_ensure_tools_registered', lambda: None)

    try:
        module.agentic_rag({}, {})
    except ValueError as exc:
        assert 'query' in str(exc).lower()
    else:
        raise AssertionError('agentic_rag should raise ValueError when query is missing')


def test_agentic_rag_requires_non_empty_query(monkeypatch):
    module = _import_agentic_module(monkeypatch)

    monkeypatch.setattr(module, '_ensure_tools_registered', lambda: None)

    try:
        module.agentic_rag({'query': '   '}, {})
    except ValueError as exc:
        assert 'query' in str(exc).lower()
    else:
        raise AssertionError('agentic_rag should raise ValueError for blank query')


def test_lazyllm_queue_db_path_is_path_like(monkeypatch):
    # _lazyllm_queue_db_path() calls lazyllm.configs internally; just verify
    # the function exists and returns something with a 'name' attribute when
    # lazyllm.configs is available (i.e. in the real import context).
    import importlib
    real_module = importlib.import_module('chat.pipelines.agentic')
    path = real_module._lazyllm_queue_db_path()
    assert hasattr(path, 'name')


def test_agentic_forward_uses_automodel(monkeypatch):
    # Verify agentic_forward calls AutoModel(model='llm').
    # We use the fake-lazyllm module to isolate the test.
    module = _import_agentic_module(monkeypatch)

    automodel_calls = []

    class _FakeAgent:
        def __init__(self, llm, tools, **kwargs):
            automodel_calls.append(llm)

        def __call__(self, query, llm_chat_history=None):
            return 'agent-output'

    monkeypatch.setattr(module, 'AutoModel', lambda model, config=False: f'model:{model}')
    # Patch lazyllm.globals and lazyllm.tools.agent.ReactAgent on the fake lazyllm

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

    module.agentic_forward(query='hello', history=[])

    assert automodel_calls == ['model:llm']

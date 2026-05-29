import importlib
import sys
from types import ModuleType, SimpleNamespace


def _import_chat_service(monkeypatch):
    for name in (
        'lazymind.chat.service.core.chat_service',
        'lazymind.chat.service.core',
        'lazymind.chat.service',
    ):
        sys.modules.pop(name, None)

    fake_lazyllm = ModuleType('lazyllm')
    fake_lazyllm.LOG = SimpleNamespace(
        info=lambda *args, **kwargs: None,
        warning=lambda *args, **kwargs: None,
    )
    fake_lazyllm.globals = SimpleNamespace(_init_sid=lambda sid=None, **kwargs: None)
    fake_lazyllm.locals = SimpleNamespace(_init_sid=lambda sid=None, **kwargs: None)
    fake_lazyllm.AutoModel = lambda model, config=False: f'model:{model}'

    fake_lazyllm_tools = ModuleType('lazyllm.tools')
    fake_lazyllm_tools_fs = ModuleType('lazyllm.tools.fs')
    fake_lazyllm_tools_fs_supplier = ModuleType('lazyllm.tools.fs.supplier')
    fake_lazyllm_tools_fs_supplier_feishu = ModuleType('lazyllm.tools.fs.supplier.feishu')
    fake_lazyllm_tools_tools = ModuleType('lazyllm.tools.tools')
    fake_lazyllm_tools_tools_search = ModuleType('lazyllm.tools.tools.search')
    fake_lazyllm_tool_config_inject = ModuleType('lazyllm.tools.tool_config_inject')

    class _FakeFeishuFS:
        def __init__(self, *args, **kwargs):
            pass

    fake_lazyllm_tools_fs_supplier_feishu.FeishuFS = _FakeFeishuFS

    class _FakeSearch:
        def __init__(self, *args, **kwargs):
            pass

    fake_lazyllm_tools_tools_search.ArxivSearch = type(
        'ArxivSearch', (_FakeSearch,), {'__public_apis__': ['search', 'get_content', 'get_contents']})
    fake_lazyllm_tools_tools_search.BingSearch = type(
        'BingSearch', (_FakeSearch,), {'__public_apis__': ['search', 'get_content', 'get_contents']})
    fake_lazyllm_tools_tools_search.BochaSearch = type(
        'BochaSearch', (_FakeSearch,), {'__public_apis__': ['search', 'get_content', 'get_contents']})
    fake_lazyllm_tools_tools_search.GoogleSearch = type(
        'GoogleSearch', (_FakeSearch,), {'__public_apis__': ['search', 'get_content', 'get_contents']})
    fake_lazyllm_tools_tools_search.WikipediaSearch = type(
        'WikipediaSearch', (_FakeSearch,), {'__public_apis__': ['search', 'get_content', 'get_contents']})
    fake_lazyllm_tool_config_inject.inject_tool_config = lambda tool_config: None

    fake_tracing = ModuleType('lazyllm.tracing')
    fake_tracing.current_trace = lambda: None
    fake_tracing.enable_trace = lambda fn, *args, **kwargs: fn(*args)
    fake_tracing.get_trace_context = lambda: SimpleNamespace(trace_id='trace-test')
    fake_tracing.set_trace_context = lambda *args, **kwargs: None

    fake_tracing_collect = ModuleType('lazyllm.tracing.collect')
    fake_tracing_collect_runtime = ModuleType('lazyllm.tracing.collect.runtime')
    fake_tracing_collect_runtime._runtime = SimpleNamespace(_provider=None)
    fake_tracing_collect_configs = ModuleType('lazyllm.tracing.collect.configs')

    fake_fastapi_responses = ModuleType('fastapi.responses')

    class _StreamingResponse:
        def __init__(self, content, media_type=None):
            self.body_iterator = content
            self.media_type = media_type

    fake_fastapi_responses.StreamingResponse = _StreamingResponse

    fake_sensitive_filter_mod = ModuleType('lazymind.chat.service.utils.sensitive_filter')

    class _SensitiveFilter:
        def __init__(self, path):
            self.loaded = False

        def check(self, query):
            return (False, None)

    fake_sensitive_filter_mod.SensitiveFilter = _SensitiveFilter

    fake_chat_config = ModuleType('lazymind.chat.config')
    fake_chat_config.RAG_MODE = True
    fake_chat_config.MAX_CONCURRENCY = 10
    fake_chat_config.LAZYMIND_LLM_PRIORITY = 0
    fake_chat_config.SENSITIVE_FILTER_RESPONSE_TEXT = 'blocked'
    fake_chat_config.SENSITIVE_WORDS_PATH = '/tmp/words.txt'

    fake_agentic = ModuleType('lazymind.chat.service.agentic.runtime')
    fake_agentic.stream_agentic_runtime = lambda **kwargs: kwargs

    fake_agentic_builder = ModuleType('lazymind.chat.engine.prompts.agentic_builder')
    fake_agentic_builder._build_system_prompt = lambda *args, **kwargs: 'prompt'

    fake_helpers = ModuleType('lazymind.chat.service.utils.file_validation')
    fake_helpers.validate_and_resolve_files = lambda files: files or []

    fake_load_config = ModuleType('lazymind.model_config')
    fake_load_config.get_config_path = lambda: 'runtime_models.yaml'
    fake_load_config.inject_model_config = lambda model_config: None
    fake_load_config.summarize_model_config_for_log = lambda model_config: 'summary'

    fake_config_mod = ModuleType('lazymind.config')
    fake_config_mod.config = {
        'skill_fs_url': 'remote://skills',
        'agentic_keep_full_turns': 3,
    }

    fake_markdown_images = ModuleType('lazymind.chat.service.utils.markdown_images')
    fake_markdown_images.rewrite_markdown_image_urls = lambda text, config=None: text

    monkeypatch.setitem(sys.modules, 'lazyllm', fake_lazyllm)
    monkeypatch.setitem(sys.modules, 'lazyllm.tools', fake_lazyllm_tools)
    monkeypatch.setitem(sys.modules, 'lazyllm.tools.fs', fake_lazyllm_tools_fs)
    monkeypatch.setitem(sys.modules, 'lazyllm.tools.fs.supplier', fake_lazyllm_tools_fs_supplier)
    monkeypatch.setitem(sys.modules, 'lazyllm.tools.fs.supplier.feishu', fake_lazyllm_tools_fs_supplier_feishu)
    monkeypatch.setitem(sys.modules, 'lazyllm.tools.tools', fake_lazyllm_tools_tools)
    monkeypatch.setitem(sys.modules, 'lazyllm.tools.tools.search', fake_lazyllm_tools_tools_search)
    monkeypatch.setitem(sys.modules, 'lazyllm.tools.tool_config_inject', fake_lazyllm_tool_config_inject)
    monkeypatch.setitem(sys.modules, 'lazyllm.tracing', fake_tracing)
    monkeypatch.setitem(sys.modules, 'lazyllm.tracing.collect', fake_tracing_collect)
    monkeypatch.setitem(sys.modules, 'lazyllm.tracing.collect.runtime', fake_tracing_collect_runtime)
    monkeypatch.setitem(sys.modules, 'lazyllm.tracing.collect.configs', fake_tracing_collect_configs)
    monkeypatch.setitem(sys.modules, 'fastapi.responses', fake_fastapi_responses)
    monkeypatch.setitem(sys.modules, 'lazymind.chat.service.utils.sensitive_filter', fake_sensitive_filter_mod)
    monkeypatch.setitem(sys.modules, 'lazymind.chat.config', fake_chat_config)
    monkeypatch.setitem(sys.modules, 'lazymind.chat.service.agentic.runtime', fake_agentic)
    monkeypatch.setitem(sys.modules, 'lazymind.chat.engine.prompts.agentic_builder', fake_agentic_builder)
    monkeypatch.setitem(sys.modules, 'lazymind.chat.service.utils.file_validation', fake_helpers)
    monkeypatch.setitem(sys.modules, 'lazymind.model_config', fake_load_config)
    monkeypatch.setitem(sys.modules, 'lazymind.config', fake_config_mod)
    monkeypatch.setitem(sys.modules, 'lazymind.chat.service.utils.markdown_images', fake_markdown_images)

    return importlib.import_module('lazymind.chat.service.core.chat_service')


def test_runtime_params_excludes_kb_binding(monkeypatch):
    module = _import_chat_service(monkeypatch)

    runtime_params = {
        'filters': {},
        'files': [],
        'priority': 3,
        'available_tools': None,
        'available_skills': None,
        'use_memory': None,
        'user_preference': None,
        'memory': None,
        'environment_context': {},
        'user_id': '',
        'stream': True,
    }

    module._sync_request_context(runtime_params)
    module.reset_citation_state(runtime_params)

    assert 'kb_url' not in runtime_params
    assert 'kb_name' not in runtime_params

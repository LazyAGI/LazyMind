import importlib
import sys
from types import ModuleType, SimpleNamespace


def _import_chat_service(monkeypatch):
    for name in (
        'chat.app.core.chat_service',
        'chat.app.core',
        'chat.app',
    ):
        sys.modules.pop(name, None)

    fake_lazyllm = ModuleType('lazyllm')
    fake_lazyllm.LOG = SimpleNamespace(
        info=lambda *args, **kwargs: None,
        warning=lambda *args, **kwargs: None,
    )
    fake_lazyllm.globals = SimpleNamespace(_init_sid=lambda sid=None, **kwargs: None)
    fake_lazyllm.locals = SimpleNamespace(_init_sid=lambda sid=None, **kwargs: None)

    fake_tracing = ModuleType('lazyllm.tracing')
    fake_tracing.current_trace = lambda: None
    fake_tracing.enable_trace = lambda fn, *args, **kwargs: fn(*args)

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

    fake_sensitive_filter_mod = ModuleType('chat.components.process.sensitive_filter')

    class _SensitiveFilter:
        def __init__(self, path):
            self.loaded = False

        def check(self, query):
            return (False, None)

    fake_sensitive_filter_mod.SensitiveFilter = _SensitiveFilter

    fake_chat_config = ModuleType('chat.config')
    fake_chat_config.RAG_MODE = True
    fake_chat_config.MULTIMODAL_MODE = True
    fake_chat_config.MAX_CONCURRENCY = 10
    fake_chat_config.LAZYMIND_LLM_PRIORITY = 0
    fake_chat_config.SENSITIVE_FILTER_RESPONSE_TEXT = 'blocked'
    fake_chat_config.SENSITIVE_WORDS_PATH = '/tmp/words.txt'

    fake_agentic = ModuleType('chat.pipelines.agentic')
    fake_agentic.agentic_rag = lambda params: params

    fake_helpers = ModuleType('chat.utils.helpers')
    fake_helpers.validate_and_resolve_files = lambda files: (files or [], [])

    fake_load_config = ModuleType('chat.utils.load_config')
    fake_load_config.get_config_path = lambda: 'runtime_models.yaml'
    fake_load_config.inject_model_config = lambda model_config: None
    fake_load_config.summarize_model_config_for_log = lambda model_config: 'summary'

    fake_markdown_images = ModuleType('chat.utils.markdown_images')
    fake_markdown_images.rewrite_markdown_image_urls = lambda text, config=None: text

    monkeypatch.setitem(sys.modules, 'lazyllm', fake_lazyllm)
    monkeypatch.setitem(sys.modules, 'lazyllm.tracing', fake_tracing)
    monkeypatch.setitem(sys.modules, 'lazyllm.tracing.collect', fake_tracing_collect)
    monkeypatch.setitem(sys.modules, 'lazyllm.tracing.collect.runtime', fake_tracing_collect_runtime)
    monkeypatch.setitem(sys.modules, 'lazyllm.tracing.collect.configs', fake_tracing_collect_configs)
    monkeypatch.setitem(sys.modules, 'fastapi.responses', fake_fastapi_responses)
    monkeypatch.setitem(sys.modules, 'chat.components.process.sensitive_filter', fake_sensitive_filter_mod)
    monkeypatch.setitem(sys.modules, 'chat.config', fake_chat_config)
    monkeypatch.setitem(sys.modules, 'chat.pipelines.agentic', fake_agentic)
    monkeypatch.setitem(sys.modules, 'chat.utils.helpers', fake_helpers)
    monkeypatch.setitem(sys.modules, 'chat.utils.load_config', fake_load_config)
    monkeypatch.setitem(sys.modules, 'chat.utils.markdown_images', fake_markdown_images)

    return importlib.import_module('chat.app.core.chat_service')


def test_build_query_params_does_not_store_kb_binding(monkeypatch):
    module = _import_chat_service(monkeypatch)

    params = module.build_query_params(
        query='hello',
        history=[{'role': 'user', 'content': 'world'}],
        filters=None,
        other_files=[],
        databases=None,
        debug=False,
        image_files=[],
        priority=3,
        dataset='unknown_dataset',
        session_id='sid-1',
        available_tools=None,
        available_skills=None,
        memory=None,
        user_preference=None,
        use_memory=None,
    )

    assert params['dataset'] == 'unknown_dataset'
    assert 'kb_url' not in params
    assert 'kb_name' not in params

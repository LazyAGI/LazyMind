"""Tests for dynamic model config: AutoModel dynamic shortcut, astream_call,
_inject_model_config, session isolation, and API parameter forwarding."""
import asyncio
import importlib
import sys
import textwrap
from pathlib import Path
from types import ModuleType, SimpleNamespace
from typing import Any, Dict
from unittest.mock import MagicMock, patch

import pytest


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

def write_yaml(tmp_path: Path, content: str) -> Path:
    p = tmp_path / 'runtime_models.yaml'
    p.write_text(textwrap.dedent(content), encoding='utf-8')
    return p


# ---------------------------------------------------------------------------
# Task 1: AutoModel source=dynamic shortcut
# ---------------------------------------------------------------------------

class TestAutoModelDynamic:
    def test_dynamic_source_returns_online_chat_module(self):
        '''AutoModel(source="dynamic") should return an OnlineChatModule instance.'''
        from lazyllm import AutoModel
        from lazyllm.module.llms.onlinemodule.chat import OnlineChatModule

        module = AutoModel(source='dynamic')
        assert isinstance(module, OnlineChatModule)

    def test_dynamic_source_with_dynamic_auth(self):
        '''dynamic_auth=True should be forwarded to OnlineChatModule.'''
        from lazyllm import AutoModel
        from lazyllm.module.llms.onlinemodule.chat import OnlineChatModule

        module = AutoModel(source='dynamic', dynamic_auth=True)
        assert isinstance(module, OnlineChatModule)
        # _api_key is set to 'dynamic' when dynamic_auth=True
        assert module._api_key == 'dynamic'

    def test_dynamic_source_from_yaml_config(self, tmp_path):
        '''AutoModel(model="llm", config=path) with source=dynamic in yaml should return OnlineChatModule.'''
        from lazyllm import AutoModel
        from lazyllm.module.llms.onlinemodule.chat import OnlineChatModule

        config_path = write_yaml(tmp_path, """
            llm:
              source: dynamic
              dynamic_auth: true
              type: llm
        """)

        module = AutoModel(model='llm', config=str(config_path))
        assert isinstance(module, OnlineChatModule)

    def test_automodel_config_expands_env_var(self, monkeypatch, tmp_path):
        from lazyllm.module.llms.utils import _get_module_config_map

        config_path = write_yaml(tmp_path, """
            llm:
              - source: openai
                name: my-model
                api_key: ${TEST_API_KEY}
        """)
        monkeypatch.setenv('TEST_API_KEY', 'secret-key')

        config = _get_module_config_map(str(config_path))

        assert config['llm'][0]['api_key'] == 'secret-key'

    def test_dynamic_chat_share_preserves_dynamic_router(self, tmp_path):
        '''share() must not re-resolve a dynamic OnlineChatModule to a static provider.'''
        from lazyllm import AutoModel
        from lazyllm.module.llms.onlinemodule.chat import OnlineChatModule

        config_path = write_yaml(tmp_path, """
            llm:
              source: dynamic
              dynamic_auth: true
              type: llm
        """)

        module = AutoModel(model='llm', config=str(config_path))
        shared = module.share(stream=True)

        assert isinstance(shared, OnlineChatModule)
        assert type(shared).__name__ == 'OnlineChatModule'
        assert shared.name == 'llm'
        assert shared._api_key == 'dynamic'

    def test_dynamic_embedding_module_is_cloudpickle_safe(self, tmp_path):
        '''Document server deployment cloudpickles dynamic embedding modules.'''
        import cloudpickle
        import threading
        from lazyllm import AutoModel
        from lazyllm.module.llms.onlinemodule.embedding import OnlineEmbeddingModule

        config_path = write_yaml(tmp_path, """
            embed_main:
              source: dynamic
              dynamic_auth: true
              type: embed
        """)

        module = AutoModel(model='embed_main', config=str(config_path))
        restored = cloudpickle.loads(cloudpickle.dumps(module))

        assert isinstance(restored, OnlineEmbeddingModule)
        assert isinstance(restored._lock, type(threading.Lock()))
        assert restored._suppliers == {}

    def test_shared_dynamic_chat_uses_injected_supplier(self, monkeypatch, tmp_path):
        '''A shared dynamic llm should still route to the per-request supplier/key/model.'''
        import json
        import lazyllm
        import requests
        from lazyllm import AutoModel
        from lazyllm.components.prompter import ChatPrompter
        from lazyllm.module.llms.onlinemodule.dynamic_router import ConfigsDict

        config_path = write_yaml(tmp_path, """
            llm:
              source: dynamic
              dynamic_auth: true
              type: llm
        """)
        sid = 'test-shared-dynamic-chat'
        lazyllm.globals._init_sid(sid)
        lazyllm.locals._init_sid(sid)
        lazyllm.globals.config['dynamic_model_configs'] = ConfigsDict({
            'llm': {
                'chat': {
                    'source': 'kimi',
                    'model': 'kimi-k2-0711-preview',
                    'url': 'https://api.moonshot.cn/',
                }
            }
        })
        lazyllm.globals.config['kimi_api_key'] = ConfigsDict({'llm': 'sk-test'})

        calls = []

        class FakeResponse:
            status_code = 200
            text = json.dumps({'choices': [{'message': {'content': 'ok'}}], 'usage': {}})

            def __enter__(self):
                return self

            def __exit__(self, *args):
                return False

            def iter_content(self, *args, **kwargs):
                return []

            def iter_lines(self):
                return []

        def fake_post(url, **kwargs):
            calls.append({'url': url, 'headers': kwargs.get('headers') or {}, 'json': kwargs.get('json') or {}})
            return FakeResponse()

        monkeypatch.setattr(requests, 'post', fake_post)

        prompter = ChatPrompter(instruction='system prompt')
        module = AutoModel(model='llm', config=str(config_path))
        shared = module.share(prompt=prompter, stream=False)
        with lazyllm.globals.stack_enter(shared.identities):
            supplier = shared._get_supplier()

        assert type(supplier).__name__ == 'KimiChat'
        assert supplier._prompt is prompter

        assert shared('hello', stream_output=False) == {'content': 'ok'}
        assert calls[0]['url'] == 'https://api.moonshot.cn/v1/chat/completions'
        assert calls[0]['headers'].get('Authorization') == 'Bearer sk-test'
        assert calls[0]['json'].get('model') == 'kimi-k2-0711-preview'

        lazyllm.locals.clear()
        lazyllm.globals.clear()


# ---------------------------------------------------------------------------
# Task 2: StreamCallHelper.astream
# ---------------------------------------------------------------------------

class TestStreamCallHelperAstream:
    @staticmethod
    def _patch_memory_queue(monkeypatch):
        import lazyllm

        class MemoryQueue:
            _items = []

            def clear(self):
                self._items.clear()

            def enqueue(self, item):
                self._items.append(item)

            def dequeue(self):
                if not self._items:
                    return None
                return [self._items.pop(0)]

        monkeypatch.setattr(lazyllm, 'FileSystemQueue', MemoryQueue)

    def test_astream_yields_chunks(self, monkeypatch):
        '''StreamCallHelper.astream should yield tokens from FileSystemQueue.'''
        import lazyllm
        from lazyllm.module.stream_helper import StreamCallHelper

        self._patch_memory_queue(monkeypatch)
        chunks_received = []

        def fake_impl(*args, **kwargs):
            # Simulate writing tagged JSON to the default queue
            lazyllm.FileSystemQueue().enqueue('{"tag": "text", "delta": "hello"}')
            lazyllm.FileSystemQueue().enqueue('{"tag": "text", "delta": " world"}')
            return 'hello world'

        helper = StreamCallHelper(fake_impl, interval=0.01)

        async def run():
            async for chunk in helper.astream('input'):
                chunks_received.append(chunk)

        asyncio.run(run())
        assert len(chunks_received) >= 1
        assert any('hello' in (c.get('delta') or '') for c in chunks_received)

    def test_astream_yields_result_when_queue_empty(self, monkeypatch):
        '''When queue is empty, future.result() should give the final result.'''
        from lazyllm.module.stream_helper import StreamCallHelper

        self._patch_memory_queue(monkeypatch)

        def fake_impl(*args, **kwargs):
            return 'final answer'

        helper = StreamCallHelper(fake_impl, interval=0.01)
        results = []

        async def run():
            async for chunk in helper.astream('input'):
                results.append(chunk)

        asyncio.run(run())
        assert helper.future.result() == 'final answer'


# ---------------------------------------------------------------------------
# Task 3: LLMBase.astream_call
# ---------------------------------------------------------------------------

class TestLLMBaseAstreamCall:
    def test_astream_call_is_async_generator(self):
        '''LLMBase.astream_call should be an async generator method.'''
        import inspect
        from lazyllm.module.servermodule import LLMBase

        assert inspect.isasyncgenfunction(LLMBase.astream_call)

    def test_astream_call_delegates_to_stream_call_helper(self):
        '''astream_call should yield chunks via StreamCallHelper.astream.'''
        from lazyllm.module.servermodule import LLMBase
        from lazyllm.module.stream_helper import StreamCallHelper

        chunks = []
        fake_chunks = [
            {'tag': 'text', 'delta': 'tok1'},
            {'tag': 'text', 'delta': 'tok2'},
        ]

        async def fake_astream(self_helper, *args, **kwargs):
            for c in fake_chunks:
                yield c

        with patch.object(StreamCallHelper, 'astream', fake_astream):
            # Create a minimal LLMBase-like object with share()
            class FakeLLM(LLMBase):
                def __init__(self):
                    # Skip full LLMBase init to avoid side effects
                    self._stream = False
                    self._type = None
                    self._static_params = {}
                    self._prompt = None
                    self._formatter = None

                def share(self, **kwargs):
                    return self

                def __call__(self, *args, **kwargs):
                    return 'result'

            llm = FakeLLM()

            async def run():
                async for chunk in llm.astream_call('query'):
                    chunks.append(chunk)

            asyncio.run(run())

        assert chunks == ['tok1', 'tok2']


# ---------------------------------------------------------------------------
# Task 4: _inject_model_config and helpers
# ---------------------------------------------------------------------------

class TestCoerceBool:
    '''Unit tests for coerce_bool — the skip_auth string-to-bool normalizer.'''

    def test_none_returns_none(self):
        from lazymind.model_config import coerce_bool
        assert coerce_bool(None) is None

    def test_true_bool(self):
        from lazymind.model_config import coerce_bool
        assert coerce_bool(True) is True

    def test_false_bool(self):
        from lazymind.model_config import coerce_bool
        assert coerce_bool(False) is False

    def test_string_true_variants(self):
        from lazymind.model_config import coerce_bool
        for v in ('true', 'True', 'TRUE', '1', 'yes', 'YES'):
            assert coerce_bool(v) is True, f'Expected True for {v!r}'

    def test_string_false_variants(self):
        from lazymind.model_config import coerce_bool
        for v in ('false', 'False', 'FALSE', '0', 'no', 'NO', ''):
            assert coerce_bool(v) is False, f'Expected False for {v!r}'

    def test_int_zero_is_false(self):
        from lazymind.model_config import coerce_bool
        assert coerce_bool(0) is False

    def test_int_nonzero_is_true(self):
        from lazymind.model_config import coerce_bool
        assert coerce_bool(1) is True


def _globals_config_patch(key: str, value: Any):
    '''Context manager: temporarily set lazyllm.globals["config"][key] = value.'''
    import lazyllm
    from contextlib import contextmanager

    @contextmanager
    def _ctx():
        cfg = lazyllm.globals['config']
        old = cfg.get(key)
        cfg[key] = value
        try:
            yield cfg
        finally:
            cfg[key] = old

    return _ctx()



class TestInjectModelConfig:
    def test_per_role_keyed_by_role_name(self, monkeypatch):
        '''Each role should be stored under its own name in ConfigsDict, not under "default".'''
        import textwrap
        from lazyllm.module.llms.onlinemodule.dynamic_router import ConfigsDict
        from lazymind.model_config import inject_model_config
        from lazymind.model_config import get_dynamic_role_slot_map

        yaml_content = textwrap.dedent('''
            llm:
              source: dynamic
              dynamic_auth: true
              type: llm
            reranker:
              source: dynamic
              dynamic_auth: true
              type: rerank
            embed_main:
              source: dynamic
              dynamic_auth: true
              type: embed
        ''')
        config_path = Path(__file__).parent / '_tmp_runtime_models.yaml'
        config_path.write_text(yaml_content, encoding='utf-8')
        try:
            get_dynamic_role_slot_map.cache_clear()
            monkeypatch.setattr('lazymind.model_config.get_config_path', lambda: str(config_path))

            with _globals_config_patch('dynamic_model_configs', None) as gcfg:
                inject_model_config({
                    'llm':          {'source': 'openai', 'model': 'gpt-4o',       'api_key': 'sk-chat'},
                    'embed_main':   {'source': 'siliconflow', 'model': 'bge-m3',       'api_key': 'sk-embed'},
                    'reranker':     {'source': 'siliconflow', 'model': 'bge-reranker', 'api_key': 'sk-embed'},
                })
                cfg = gcfg.get('dynamic_model_configs')

            assert isinstance(cfg, ConfigsDict)
            assert cfg['llm']['chat']['model'] == 'gpt-4o'
            assert cfg['embed_main']['embed']['model'] == 'bge-m3'
            assert cfg['reranker']['embed']['model'] == 'bge-reranker'
            assert 'default' not in cfg
        finally:
            config_path.unlink(missing_ok=True)
            get_dynamic_role_slot_map.cache_clear()

    def test_same_slot_different_roles_independent(self, monkeypatch, tmp_path):
        '''Two roles sharing the same slot (e.g. llm and evo_llm) can have independent configs.'''
        import textwrap
        from lazymind.model_config import inject_model_config
        from lazymind.model_config import get_dynamic_role_slot_map

        config_path = tmp_path / 'runtime_models.yaml'
        config_path.write_text(textwrap.dedent('''
            llm:
              source: dynamic
              type: llm
            evo_llm:
              source: dynamic
              type: llm
        '''), encoding='utf-8')
        get_dynamic_role_slot_map.cache_clear()
        monkeypatch.setattr('lazymind.model_config.get_config_path', lambda: str(config_path))

        with _globals_config_patch('dynamic_model_configs', None) as gcfg:
            inject_model_config({
                'llm':          {'source': 'openai', 'model': 'gpt-4o',      'api_key': 'sk-x'},
                'evo_llm': {'source': 'openai', 'model': 'gpt-4o-mini', 'api_key': 'sk-x'},
            })
            cfg = gcfg.get('dynamic_model_configs')

        assert cfg['llm']['chat']['model'] == 'gpt-4o'
        assert cfg['evo_llm']['chat']['model'] == 'gpt-4o-mini'
        get_dynamic_role_slot_map.cache_clear()

    def test_noop_when_no_dynamic_roles(self, monkeypatch, tmp_path):
        '''When no dynamic roles are configured, model_config=None is fine.'''
        import textwrap
        from lazymind.model_config import inject_model_config
        from lazymind.model_config import get_dynamic_role_slot_map

        config_path = tmp_path / 'runtime_models.yaml'
        config_path.write_text(textwrap.dedent('''
            llm:
              source: siliconflow
              type: llm
              model: qwen-turbo
        '''), encoding='utf-8')
        get_dynamic_role_slot_map.cache_clear()
        monkeypatch.setattr('lazymind.model_config.get_config_path', lambda: str(config_path))

        inject_model_config(None)
        inject_model_config({})
        get_dynamic_role_slot_map.cache_clear()

    def test_none_model_config_is_delegated_to_lazyllm(self, monkeypatch, tmp_path):
        '''None/empty model_config is delegated to lazyllm without local validation.'''
        import textwrap
        from lazymind.model_config import inject_model_config
        from lazymind.model_config import get_dynamic_role_slot_map

        config_path = tmp_path / 'runtime_models.yaml'
        config_path.write_text(textwrap.dedent('''
            llm:
              source: dynamic
              type: llm
        '''), encoding='utf-8')
        get_dynamic_role_slot_map.cache_clear()
        monkeypatch.setattr('lazymind.model_config.get_config_path', lambda: str(config_path))

        inject_model_config(None)
        inject_model_config({})
        get_dynamic_role_slot_map.cache_clear()

    def test_missing_roles_are_left_unconfigured(self, monkeypatch, tmp_path):
        '''When model_config omits dynamic roles, provided roles are still injected.'''
        import textwrap
        from lazyllm.module.llms.onlinemodule.dynamic_router import ConfigsDict
        from lazymind.model_config import inject_model_config
        from lazymind.model_config import get_dynamic_role_slot_map

        config_path = tmp_path / 'runtime_models.yaml'
        config_path.write_text(textwrap.dedent('''
            llm:
              source: dynamic
              type: llm
            embed_main:
              source: dynamic
              type: embed
        '''), encoding='utf-8')
        get_dynamic_role_slot_map.cache_clear()
        monkeypatch.setattr('lazymind.model_config.get_config_path', lambda: str(config_path))

        with _globals_config_patch('dynamic_model_configs', None) as gcfg:
            inject_model_config({'llm': {'source': 'openai', 'model': 'gpt-4o', 'api_key': 'sk-x'}})
            cfg = gcfg.get('dynamic_model_configs')

        assert isinstance(cfg, ConfigsDict)
        assert cfg['llm']['chat']['model'] == 'gpt-4o'
        assert 'embed_main' not in cfg
        get_dynamic_role_slot_map.cache_clear()

    def test_empty_bucket_is_skipped_by_lazyllm(self, monkeypatch, tmp_path):
        '''Empty role configs are skipped by lazyllm instead of raising locally.'''
        import textwrap
        from lazyllm.module.llms.onlinemodule.dynamic_router import ConfigsDict
        from lazymind.model_config import inject_model_config
        from lazymind.model_config import get_dynamic_role_slot_map

        config_path = tmp_path / 'runtime_models.yaml'
        config_path.write_text(textwrap.dedent('''
            llm:
              source: dynamic
              type: llm
        '''), encoding='utf-8')
        get_dynamic_role_slot_map.cache_clear()
        monkeypatch.setattr('lazymind.model_config.get_config_path', lambda: str(config_path))

        with _globals_config_patch('dynamic_model_configs', None) as gcfg:
            inject_model_config({'llm': {'source': None, 'model': None}})
            cfg = gcfg.get('dynamic_model_configs')
        assert isinstance(cfg, ConfigsDict)
        assert 'llm' not in cfg
        get_dynamic_role_slot_map.cache_clear()

    def test_unknown_role_is_forwarded_to_lazyllm(self, monkeypatch, tmp_path):
        '''Unknown role keys are forwarded to lazyllm without local filtering.'''
        import textwrap
        from lazyllm.module.llms.onlinemodule.dynamic_router import ConfigsDict
        from lazymind.model_config import inject_model_config
        from lazymind.model_config import get_dynamic_role_slot_map

        config_path = tmp_path / 'runtime_models.yaml'
        config_path.write_text(textwrap.dedent('''
            llm:
              source: dynamic
              type: llm
        '''), encoding='utf-8')
        get_dynamic_role_slot_map.cache_clear()
        monkeypatch.setattr('lazymind.model_config.get_config_path', lambda: str(config_path))

        with _globals_config_patch('dynamic_model_configs', None) as gcfg:
            inject_model_config({
                'llm': {'source': 'openai', 'model': 'gpt-4o', 'api_key': 'sk-x'},
                'nonexistent_role': {'source': 'openai', 'model': 'gpt-4o', 'api_key': 'sk-x'},
            })
            cfg = gcfg.get('dynamic_model_configs')

        assert cfg['llm']['chat']['model'] == 'gpt-4o'
        assert cfg['nonexistent_role']['chat']['model'] == 'gpt-4o'
        get_dynamic_role_slot_map.cache_clear()

    def test_skips_none_fields(self, monkeypatch, tmp_path):
        '''Only non-None fields should appear in the bucket; None values are stripped.'''
        import textwrap
        from lazymind.model_config import inject_model_config
        from lazymind.model_config import get_dynamic_role_slot_map

        config_path = tmp_path / 'runtime_models.yaml'
        config_path.write_text(textwrap.dedent('''
            llm:
              source: dynamic
              type: llm
        '''), encoding='utf-8')
        get_dynamic_role_slot_map.cache_clear()
        monkeypatch.setattr('lazymind.model_config.get_config_path', lambda: str(config_path))

        with _globals_config_patch('dynamic_model_configs', None) as gcfg:
            inject_model_config({'llm': {'source': 'qwen', 'model': None, 'base_url': None}})
            cfg = gcfg.get('dynamic_model_configs')

        assert 'source' in cfg['llm']['chat']
        assert 'model' not in cfg['llm']['chat']
        assert 'url' not in cfg['llm']['chat']
        get_dynamic_role_slot_map.cache_clear()



class TestGetDynamicRoleSlotMap:
    def test_maps_dynamic_roles_to_slots(self, tmp_path):
        import textwrap
        from lazymind.model_config import get_dynamic_role_slot_map

        config_path = tmp_path / 'runtime_models.yaml'
        config_path.write_text(textwrap.dedent('''
            llm:
              source: dynamic
              type: llm
            evo_llm:
              source: dynamic
              type: llm
            reranker:
              source: dynamic
              type: rerank
            embed_main:
              source: dynamic
              type: embed
        '''), encoding='utf-8')
        get_dynamic_role_slot_map.cache_clear()

        result = get_dynamic_role_slot_map(str(config_path))

        assert result == {
            'llm': 'chat',
            'evo_llm': 'chat',
            'reranker': 'embed',
            'embed_main': 'embed',
        }
        get_dynamic_role_slot_map.cache_clear()

    def test_ignores_non_dynamic_roles(self, tmp_path):
        import textwrap
        from lazymind.model_config import get_dynamic_role_slot_map

        config_path = tmp_path / 'runtime_models.yaml'
        config_path.write_text(textwrap.dedent('''
            llm:
              source: siliconflow
              type: llm
              model: qwen-turbo
            embed_main:
              source: dynamic
              type: embed
        '''), encoding='utf-8')
        get_dynamic_role_slot_map.cache_clear()

        result = get_dynamic_role_slot_map(str(config_path))

        assert 'llm' not in result
        assert result['embed_main'] == 'embed'
        get_dynamic_role_slot_map.cache_clear()


# ---------------------------------------------------------------------------
# Task 5: chat_routes model_config parameter forwarding
# ---------------------------------------------------------------------------

def _load_chat_routes_module():
    module_name = 'test_chat_routes_isolated_dynamic'
    module_path = Path(__file__).resolve().parents[3] / 'algorithm/lazymind/chat/api/chat_routes.py'
    spec = importlib.util.spec_from_file_location(module_name, module_path)
    module = importlib.util.module_from_spec(spec)
    sys.modules.pop(module_name, None)
    assert spec.loader is not None
    spec.loader.exec_module(module)
    return module


class TestChatRouteModelConfig:
    def test_model_config_forwarded_to_handle_chat(self, monkeypatch):
        '''chat route should forward model_config to handle_chat.'''
        import importlib.util
        from pydantic import BaseModel
        recorded = {}

        class FakeRuntime(BaseModel):
            llm_config: dict | None = None

        class FakeChatRequest(BaseModel):
            message: dict
            conversation: dict = {}
            runtime: FakeRuntime = FakeRuntime()

        async def fake_handle_chat(request):
            recorded['request'] = request
            return {'ok': True}

        fake_request = ModuleType('lazymind.chat.service.chat_request')
        fake_request.ChatRequest = FakeChatRequest

        fake_service = ModuleType('lazymind.chat.service.chat_service')
        fake_service.handle_chat = fake_handle_chat

        fake_config = ModuleType('lazymind.chat.config')
        fake_config.DEFAULT_CHAT_DATASET = 'default'

        monkeypatch.setitem(sys.modules, 'lazymind.chat.service.chat_request', fake_request)
        monkeypatch.setitem(sys.modules, 'lazymind.chat.service.chat_service', fake_service)
        monkeypatch.setitem(sys.modules, 'lazymind.chat.config', fake_config)

        routes_mod = _load_chat_routes_module()

        async def run():
            await routes_mod.chat(routes_mod.ChatRequest(
                message={'query': 'hello'},
                conversation={'session_id': 'sid-1'},
                runtime={'llm_config': {'source': 'openai', 'name': 'gpt-4o'}},
            ))

        asyncio.run(run())
        assert recorded['request'].runtime.llm_config == {'source': 'openai', 'name': 'gpt-4o'}

    def test_model_config_defaults_to_none(self, monkeypatch):
        '''model_config should default to None when not provided.'''
        import importlib.util
        from pydantic import BaseModel
        recorded = {}

        class FakeRuntime(BaseModel):
            llm_config: dict | None = None

        class FakeChatRequest(BaseModel):
            message: dict
            conversation: dict = {}
            runtime: FakeRuntime = FakeRuntime()

        async def fake_handle_chat(request):
            recorded['request'] = request
            return {'ok': True}

        fake_request = ModuleType('lazymind.chat.service.chat_request')
        fake_request.ChatRequest = FakeChatRequest

        fake_service = ModuleType('lazymind.chat.service.chat_service')
        fake_service.handle_chat = fake_handle_chat

        fake_config = ModuleType('lazymind.chat.config')
        fake_config.DEFAULT_CHAT_DATASET = 'default'

        monkeypatch.setitem(sys.modules, 'lazymind.chat.service.chat_request', fake_request)
        monkeypatch.setitem(sys.modules, 'lazymind.chat.service.chat_service', fake_service)
        monkeypatch.setitem(sys.modules, 'lazymind.chat.config', fake_config)

        routes_mod = _load_chat_routes_module()

        async def run():
            await routes_mod.chat(routes_mod.ChatRequest(
                message={'query': 'hello'},
                conversation={'session_id': 'sid-1'},
            ))

        asyncio.run(run())
        assert recorded['request'].runtime.llm_config is None

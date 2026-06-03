"""End-to-end tests for the model_config injection pipeline.

These tests use the real lazyllm library (no mocking of lazyllm internals) to
verify the full chain:

    HTTP request (model_config dict)
        → inject_model_config  (writes ConfigsDict + api_key into lazyllm.globals)
        → OnlineChatModule._get_supplier()  (builds supplier with api_key='dynamic')
        → supplier._materialize_lazy_api_key()  (reads globals.config['{source}_api_key'])
        → requests.post(url, json=body, headers={'Authorization': 'Bearer sk-...'})

No real LLM or KB service is required — requests.post is intercepted.

Run with:
    PYTHONPATH=/path/to/algorithm/lazyllm pytest tests/algorithm/chat/test_e2e_model_config.py -v
"""
import textwrap
from contextlib import contextmanager
from pathlib import Path
from unittest.mock import patch, MagicMock
import json

import pytest

import lazyllm
from lazyllm.module.llms.onlinemodule.chat import OnlineChatModule
from lazyllm.module.llms.onlinemodule.embedding import OnlineEmbeddingModule
from lazyllm.module.llms.onlinemodule.dynamic_router import ConfigsDict

from lazymind.model_config import inject_model_config, get_dynamic_role_slot_map


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

@contextmanager
def _runtime_models_yaml(tmp_path: Path, content: str):
    """Write a temporary runtime_models.yaml and patch the module-level path."""
    config_path = tmp_path / 'runtime_models.yaml'
    config_path.write_text(textwrap.dedent(content), encoding='utf-8')
    get_dynamic_role_slot_map.cache_clear()
    import lazymind.model_config as lc
    old = lc._DYNAMIC_CONFIG_PATH
    lc._DYNAMIC_CONFIG_PATH = config_path
    try:
        yield config_path
    finally:
        lc._DYNAMIC_CONFIG_PATH = old
        get_dynamic_role_slot_map.cache_clear()


@contextmanager
def _clean_globals():
    """Isolate dynamic_model_configs and per-source api_key entries in lazyllm.globals for each test."""
    cfg = lazyllm.globals['config']
    old_dmc = cfg.get('dynamic_model_configs')
    # Snapshot all {source}_api_key entries that may be written by inject_model_config.
    api_key_keys = [k for k in cfg.keys() if k.endswith('_api_key')]
    old_api_keys = {k: cfg.get(k) for k in api_key_keys}
    cfg['dynamic_model_configs'] = None
    try:
        yield cfg
    finally:
        cfg['dynamic_model_configs'] = old_dmc
        for k, v in old_api_keys.items():
            cfg[k] = v


def _make_fake_post(captured: list):
    """Return a requests.post mock that records (url, headers, json) and returns a minimal non-stream response."""
    def fake_post(url, json=None, headers=None, **kwargs):
        captured.append({'url': url, 'headers': headers or {}, 'json': json or {}})
        body = json_module.dumps({
            'choices': [{'message': {'content': 'ok', 'role': 'assistant'}, 'finish_reason': 'stop'}]
        })
        resp = MagicMock()
        resp.__enter__ = lambda s: s
        resp.__exit__ = MagicMock(return_value=False)
        resp.status_code = 200
        resp.text = body
        return resp
    return fake_post

# alias to avoid shadowing the `json` parameter name inside fake_post
import json as json_module


def _get_bucket(module):
    """Push module onto the call stack and read its dynamic bucket."""
    with lazyllm.globals.stack_enter(module.identities):
        return module._get_dynamic_bucket()


# ---------------------------------------------------------------------------
# Part 1: inject_model_config → lazyllm.globals (config routing layer)
# ---------------------------------------------------------------------------

class TestInjectToGlobals:
    """inject_model_config writes the correct ConfigsDict structure."""

    def test_single_llm_role(self, tmp_path):
        with _runtime_models_yaml(tmp_path, '''
            llm:
              source: dynamic
              type: llm
        '''):
            with _clean_globals() as gcfg:
                inject_model_config({
                    'llm': {'source': 'openai', 'model': 'gpt-4o', 'api_key': 'sk-x'},
                })
                cfg = gcfg['dynamic_model_configs']

        assert isinstance(cfg, ConfigsDict)
        assert cfg['llm']['chat']['source'] == 'openai'
        assert cfg['llm']['chat']['model'] == 'gpt-4o'

    def test_all_four_roles_stored_independently(self, tmp_path):
        with _runtime_models_yaml(tmp_path, '''
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
        '''):
            with _clean_globals() as gcfg:
                inject_model_config({
                    'llm':          {'source': 'openai', 'model': 'gpt-4o',       'api_key': 'sk-a'},
                    'evo_llm': {'source': 'openai', 'model': 'gpt-4o-mini',  'api_key': 'sk-a'},
                    'reranker':     {'source': 'siliconflow', 'model': 'bge-reranker', 'api_key': 'sk-b'},
                    'embed_main':   {'source': 'siliconflow', 'model': 'bge-m3',       'api_key': 'sk-b'},
                })
                cfg = gcfg['dynamic_model_configs']

        assert cfg['llm']['chat']['model'] == 'gpt-4o'
        assert cfg['evo_llm']['chat']['model'] == 'gpt-4o-mini'
        assert cfg['reranker']['embed']['model'] == 'bge-reranker'
        assert cfg['embed_main']['embed']['model'] == 'bge-m3'
        assert 'default' not in cfg

    def test_skip_auth_string_coerced_to_bool(self, tmp_path):
        with _runtime_models_yaml(tmp_path, '''
            llm:
              source: dynamic
              type: llm
        '''):
            with _clean_globals() as gcfg:
                inject_model_config({
                    'llm': {'source': 'openai', 'model': 'gpt-4o', 'skip_auth': 'true'},
                })
                cfg = gcfg['dynamic_model_configs']

        assert cfg['llm']['chat']['skip_auth'] is True

    def test_none_model_config_is_forwarded_to_lazyllm(self, tmp_path):
        with _runtime_models_yaml(tmp_path, '''
            llm:
              source: dynamic
              type: llm
        '''):
            inject_model_config(None)

    def test_missing_role_left_unconfigured(self, tmp_path):
        with _runtime_models_yaml(tmp_path, '''
            llm:
              source: dynamic
              type: llm
            embed_main:
              source: dynamic
              type: embed
        '''):
            with _clean_globals() as gcfg:
                inject_model_config({
                    'llm': {'source': 'openai', 'model': 'gpt-4o', 'api_key': 'sk-x'},
                    # embed_main is missing
                })
                cfg = gcfg['dynamic_model_configs']

        assert cfg['llm']['chat']['model'] == 'gpt-4o'
        assert 'embed_main' not in cfg

    def test_noop_when_no_dynamic_roles(self, tmp_path):
        """Static config: inject_model_config(None) should not raise."""
        with _runtime_models_yaml(tmp_path, '''
            llm:
              source: siliconflow
              model: qwen-turbo
              type: llm
        '''):
            inject_model_config(None)
            inject_model_config({})


# ---------------------------------------------------------------------------
# Part 2: OnlineChatModule reads the correct bucket from globals
# ---------------------------------------------------------------------------

class TestModuleReadsBucket:
    """OnlineChatModule/OnlineEmbeddingModule resolves the right bucket per role name."""

    def test_chat_module_reads_own_role_bucket(self, tmp_path):
        with _runtime_models_yaml(tmp_path, '''
            llm:
              source: dynamic
              type: llm
            evo_llm:
              source: dynamic
              type: llm
        '''):
            with _clean_globals():
                inject_model_config({
                    'llm':          {'source': 'openai', 'model': 'gpt-4o',      'api_key': 'sk-x'},
                    'evo_llm': {'source': 'openai', 'model': 'gpt-4o-mini', 'api_key': 'sk-x'},
                })

                m_llm = OnlineChatModule(source='dynamic', name='llm', stream=False)
                m_instruct = OnlineChatModule(source='dynamic', name='evo_llm', stream=False)

                bucket_llm = _get_bucket(m_llm)
                bucket_instruct = _get_bucket(m_instruct)

        assert bucket_llm['model'] == 'gpt-4o'
        assert bucket_instruct['model'] == 'gpt-4o-mini'

    def test_module_identities_contain_role_name(self):
        m = OnlineChatModule(source='dynamic', name='evo_llm', stream=False)
        assert m.identities[1] == 'evo_llm'

    def test_two_llm_roles_are_fully_isolated(self, tmp_path):
        with _runtime_models_yaml(tmp_path, '''
            llm:
              source: dynamic
              type: llm
            evo_llm:
              source: dynamic
              type: llm
        '''):
            with _clean_globals():
                inject_model_config({
                    'llm':          {'source': 'openai', 'model': 'gpt-4o',    'api_key': 'sk-1'},
                    'evo_llm': {'source': 'qwen',   'model': 'qwen-plus', 'api_key': 'sk-2'},
                })

                m1 = OnlineChatModule(source='dynamic', name='llm', stream=False)
                m2 = OnlineChatModule(source='dynamic', name='evo_llm', stream=False)

                b1 = _get_bucket(m1)
                b2 = _get_bucket(m2)

        assert b1['source'] == 'openai' and b1['model'] == 'gpt-4o'
        assert b2['source'] == 'qwen'   and b2['model'] == 'qwen-plus'


# ---------------------------------------------------------------------------
# Part 3: forward() sends correct Authorization header and model in body
# ---------------------------------------------------------------------------

class TestForwardUsesCorrectKeyAndModel:
    """Verify that the HTTP request carries the right api_key and model from model_config."""

    def test_authorization_header_and_model_from_bucket(self, tmp_path):
        """requests.post must receive Authorization: Bearer <bucket api_key> and correct model."""
        with _runtime_models_yaml(tmp_path, '''
            llm:
              source: dynamic
              type: llm
        '''):
            with _clean_globals():
                inject_model_config({
                    'llm': {'source': 'openai', 'model': 'gpt-4o', 'api_key': 'sk-from-request'},
                })

                m = OnlineChatModule(source='dynamic', name='llm', stream=False)
                captured = []

                with patch('requests.post', side_effect=_make_fake_post(captured)):
                    with lazyllm.globals.stack_enter(m.identities):
                        m.forward('hello')

        assert len(captured) == 1
        req = captured[0]
        assert req['headers'].get('Authorization') == 'Bearer sk-from-request'
        assert req['json'].get('model') == 'gpt-4o'
        messages = req['json'].get('messages', [])
        assert any(msg.get('content') == 'hello' for msg in messages)

    def test_two_users_get_independent_keys(self, tmp_path):
        """Two sequential requests with different api_keys must each use their own key.

        The module is constructed with dynamic_auth=True so that _api_key='dynamic'
        and the supplier resolves the key dynamically on every forward() call via
        _materialize_lazy_api_key() → globals.config['{source}_api_key'].
        Each request calls _init_sid() to set a distinct session id, so that
        inject_model_config writes into separate ConfigsDict slots and the two
        requests are fully isolated.
        """
        with _runtime_models_yaml(tmp_path, '''
            llm:
              source: dynamic
              type: llm
        '''):
            # dynamic_auth=True → self._api_key='dynamic' → supplier._dynamic_auth=True
            m = OnlineChatModule(source='dynamic', name='llm', stream=False, dynamic_auth=True)
            captured_a, captured_b = [], []

            with _clean_globals():
                # Request A (session-A)
                lazyllm.globals._init_sid(sid='session-A')
                inject_model_config({'llm': {'source': 'openai', 'model': 'gpt-4o', 'api_key': 'sk-user-A'}})
                with patch('requests.post', side_effect=_make_fake_post(captured_a)):
                    with lazyllm.globals.stack_enter(m.identities):
                        m.forward('hello from A')

                # Request B (session-B) — different key and model
                lazyllm.globals._init_sid(sid='session-B')
                inject_model_config({'llm': {'source': 'openai', 'model': 'gpt-4o-mini', 'api_key': 'sk-user-B'}})
                with patch('requests.post', side_effect=_make_fake_post(captured_b)):
                    with lazyllm.globals.stack_enter(m.identities):
                        m.forward('hello from B')

        assert captured_a[0]['headers']['Authorization'] == 'Bearer sk-user-A'
        assert captured_a[0]['json']['model'] == 'gpt-4o'
        assert captured_b[0]['headers']['Authorization'] == 'Bearer sk-user-B'
        assert captured_b[0]['json']['model'] == 'gpt-4o-mini'

    def test_llm_and_llm_instruct_use_independent_keys(self, tmp_path):
        """llm and llm_instruct share the same supplier slot but must use their own keys."""
        with _runtime_models_yaml(tmp_path, '''
            llm:
              source: dynamic
              type: llm
            evo_llm:
              source: dynamic
              type: llm
        '''):
            with _clean_globals():
                inject_model_config({
                    'llm':          {'source': 'openai', 'model': 'gpt-4o',      'api_key': 'sk-llm'},
                    'evo_llm': {'source': 'openai', 'model': 'gpt-4o-mini', 'api_key': 'sk-instruct'},
                })

                m_llm = OnlineChatModule(source='dynamic', name='llm', stream=False)
                m_instruct = OnlineChatModule(source='dynamic', name='evo_llm', stream=False)
                cap_llm, cap_instruct = [], []

                with patch('requests.post', side_effect=_make_fake_post(cap_llm)):
                    with lazyllm.globals.stack_enter(m_llm.identities):
                        m_llm.forward('q1')

                with patch('requests.post', side_effect=_make_fake_post(cap_instruct)):
                    with lazyllm.globals.stack_enter(m_instruct.identities):
                        m_instruct.forward('q2')

        assert cap_llm[0]['headers']['Authorization'] == 'Bearer sk-llm'
        assert cap_llm[0]['json']['model'] == 'gpt-4o'
        assert cap_instruct[0]['headers']['Authorization'] == 'Bearer sk-instruct'
        assert cap_instruct[0]['json']['model'] == 'gpt-4o-mini'

    def test_same_source_roles_use_independent_api_key_configs(self, tmp_path):
        with _runtime_models_yaml(tmp_path, '''
            llm:
              source: dynamic
              type: llm
            evo_llm:
              source: dynamic
              type: llm
        '''):
            with _clean_globals() as gcfg:
                inject_model_config({
                    'llm': {'source': 'openai', 'model': 'gpt-4o', 'api_key': 'sk-llm'},
                    'evo_llm': {'source': 'openai', 'model': 'gpt-4o-mini', 'api_key': 'sk-evo'},
                })
                key_cfg = gcfg['openai_api_key']

                m_llm = OnlineChatModule(source='dynamic', name='llm', stream=False, dynamic_auth=True)
                m_evo = OnlineChatModule(source='dynamic', name='evo_llm', stream=False, dynamic_auth=True)
                cap_llm, cap_evo = [], []

                with patch('requests.post', side_effect=_make_fake_post(cap_llm)):
                    with lazyllm.globals.stack_enter(m_llm.identities):
                        m_llm.forward('q1')
                with patch('requests.post', side_effect=_make_fake_post(cap_evo)):
                    with lazyllm.globals.stack_enter(m_evo.identities):
                        m_evo.forward('q2')

        assert key_cfg == {'llm': 'sk-llm', 'evo_llm': 'sk-evo'}
        assert cap_llm[0]['headers']['Authorization'] == 'Bearer sk-llm'
        assert cap_evo[0]['headers']['Authorization'] == 'Bearer sk-evo'

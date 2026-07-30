import importlib.util
import sys
from pathlib import Path
from types import ModuleType

import pytest


def test_config_reads_custom_environment_values(monkeypatch):
    # Config is a singleton; patch env vars and read directly from the config instance.
    monkeypatch.setenv('LAZYMIND_MOUNT_BASE_DIR', '/mnt/data')
    monkeypatch.setenv('LAZYMIND_SENSITIVE_RED_WORDS_PATH', '/tmp/red.txt')
    monkeypatch.setenv('LAZYMIND_SENSITIVE_GRAY_WORDS_PATH', '/tmp/gray.txt')
    monkeypatch.setenv('LAZYMIND_SENSITIVE_WHITELIST_PATH', '/tmp/whitelist.txt')
    monkeypatch.setenv('LAZYMIND_LLM_PRIORITY', '12')
    monkeypatch.setenv('LAZYMIND_MAX_CONCURRENCY', '7')
    monkeypatch.setenv('LAZYMIND_RAG_MODE', 'false')
    monkeypatch.setenv('LAZYMIND_DEFAULT_CHAT_DATASET', 'science')

    from lazymind.config import config as _cfg
    assert _cfg['mount_base_dir'] == '/mnt/data'
    assert _cfg['sensitive_red_words_path'] == '/tmp/red.txt'
    assert _cfg['sensitive_gray_words_path'] == '/tmp/gray.txt'
    assert _cfg['sensitive_whitelist_path'] == '/tmp/whitelist.txt'
    assert _cfg['llm_priority'] == 12
    assert _cfg['max_concurrency'] == 7
    assert _cfg['rag_mode'] is False
    assert _cfg['default_chat_dataset'] == 'science'

    with pytest.raises(KeyError):
        _cfg['sensitive_words_path']


def test_config_falls_back_to_defaults(monkeypatch):
    monkeypatch.delenv('LAZYMIND_LLM_PRIORITY', raising=False)
    monkeypatch.delenv('LAZYMIND_RAG_MODE', raising=False)

    from lazymind.config import config as _cfg
    assert _cfg['llm_priority'] == 0
    assert _cfg['rag_mode'] is True


def test_chat_config_bootstraps_canonical_config_module(monkeypatch):
    fake_config_module = ModuleType('config')
    fake_config_module.config = object()
    monkeypatch.setitem(sys.modules, 'config', fake_config_module)

    module_name = 'test_chat_config_isolated'
    module_path = Path(__file__).resolve().parents[3] / 'algorithm/lazymind/chat/config.py'
    spec = importlib.util.spec_from_file_location(module_name, module_path)
    module = importlib.util.module_from_spec(spec)
    sys.modules.pop(module_name, None)
    assert spec.loader is not None
    spec.loader.exec_module(module)

    assert Path(sys.modules['lazymind.config'].__file__).resolve() == (
        Path(__file__).resolve().parents[3] / 'algorithm/lazymind/config.py'
    ).resolve()
    assert module.DEFAULT_CHAT_DATASET == 'algo'

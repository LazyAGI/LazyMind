"""Tests for load_config.py — environment variable expansion and config path selection."""
import textwrap
from pathlib import Path

from chat.utils.load_config import load_model_config, get_config_path


def write_config(tmp_path: Path, content: str) -> Path:
    config_path = tmp_path / 'runtime_models.yaml'
    config_path.write_text(textwrap.dedent(content), encoding='utf-8')
    return config_path


def test_load_model_config_expands_env_var(monkeypatch, tmp_path):
    config_path = write_config(
        tmp_path,
        """
        llm:
          - source: openai
            name: my-model
            api_key: ${TEST_API_KEY}
        """,
    )
    monkeypatch.setenv('TEST_API_KEY', 'secret-key')

    config = load_model_config(str(config_path))

    # load_model_config returns raw yaml without env expansion
    assert config['llm'][0]['api_key'] == '${TEST_API_KEY}'


def test_load_model_config_uses_default_when_env_missing(monkeypatch, tmp_path):
    config_path = write_config(
        tmp_path,
        """
        llm:
          - source: openai
            api_key: ${MISSING_KEY:-fallback-value}
        """,
    )
    monkeypatch.delenv('MISSING_KEY', raising=False)

    config = load_model_config(str(config_path))

    # load_model_config returns raw yaml without env expansion
    assert config['llm'][0]['api_key'] == '${MISSING_KEY:-fallback-value}'


def test_load_model_config_leaves_unset_placeholder_intact(monkeypatch, tmp_path):
    config_path = write_config(
        tmp_path,
        """
        llm:
          - source: openai
            api_key: ${UNSET_KEY}
        """,
    )
    monkeypatch.delenv('UNSET_KEY', raising=False)

    config = load_model_config(str(config_path))

    assert config['llm'][0]['api_key'] == '${UNSET_KEY}'


def test_load_model_config_dynamic_role(tmp_path):
    config_path = write_config(
        tmp_path,
        """
        llm:
          source: dynamic
          dynamic_auth: true
          type: llm
        """,
    )

    config = load_model_config(str(config_path))

    assert config['llm']['source'] == 'dynamic'
    assert config['llm']['dynamic_auth'] is True


def test_load_model_config_preserves_agentic_section(tmp_path):
    config_path = write_config(
        tmp_path,
        """
        llm:
          source: dynamic
          type: llm
        agentic:
          kb_url: http://kb:8000
          timeout: 10
        """,
    )

    config = load_model_config(str(config_path))

    assert config['agentic']['kb_url'] == 'http://kb:8000'
    assert config['agentic']['timeout'] == 10


def test_get_config_path_returns_dynamic_by_default(monkeypatch):
    monkeypatch.delenv('LAZYMIND_MODEL_CONFIG_PATH', raising=False)
    from config import config as _cfg
    _cfg.refresh('model_config_path')
    path = get_config_path()
    assert path.endswith('runtime_models.yaml')
    assert 'inner' not in path
    assert 'online' not in path


def test_get_config_path_alias_online(monkeypatch):
    monkeypatch.setenv('LAZYMIND_MODEL_CONFIG_PATH', 'online')
    from config import config as _cfg
    _cfg.refresh('model_config_path')
    path = get_config_path()
    assert path.endswith('runtime_models.online.yaml')


def test_get_config_path_alias_dynamic(monkeypatch):
    monkeypatch.setenv('LAZYMIND_MODEL_CONFIG_PATH', 'dynamic')
    from config import config as _cfg
    _cfg.refresh('model_config_path')
    path = get_config_path()
    assert path.endswith('runtime_models.yaml')
    assert 'inner' not in path
    assert 'online' not in path


def test_get_config_path_alias_inner(monkeypatch):
    monkeypatch.setenv('LAZYMIND_MODEL_CONFIG_PATH', 'inner')
    from config import config as _cfg
    _cfg.refresh('model_config_path')
    path = get_config_path()
    assert 'inner' in path


def test_get_config_path_custom_override(monkeypatch, tmp_path):
    custom = str(tmp_path / 'custom.yaml')
    monkeypatch.setenv('LAZYMIND_MODEL_CONFIG_PATH', custom)
    from config import config as _cfg
    _cfg.refresh('model_config_path')
    path = get_config_path()
    assert path == custom


def test_load_model_config_expands_nested_env_vars(monkeypatch, tmp_path):
    config_path = write_config(
        tmp_path,
        """
        embed_main:
          - source: bgem3embed
            url: ${EMBED_URL:-http://localhost:8080}
            name: ${EMBED_MODEL:-default-model}
        """,
    )
    monkeypatch.setenv('EMBED_URL', 'http://prod-embed:9000')
    monkeypatch.delenv('EMBED_MODEL', raising=False)

    config = load_model_config(str(config_path))

    # load_model_config returns raw yaml without env expansion
    assert config['embed_main'][0]['url'] == '${EMBED_URL:-http://localhost:8080}'
    assert config['embed_main'][0]['name'] == '${EMBED_MODEL:-default-model}'

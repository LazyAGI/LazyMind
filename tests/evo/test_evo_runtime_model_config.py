import textwrap
from pathlib import Path

import pytest
import yaml

try:
    import lazyllm  # noqa: F401
except Exception as exc:
    pytest.skip(f'lazyllm unavailable in test environment: {exc}', allow_module_level=True)

from chat.utils.load_config import get_retrieval_settings, load_model_config


def write_config(tmp_path, content: str):
    config_path = tmp_path / 'runtime_models.yaml'
    config_path.write_text(textwrap.dedent(content), encoding='utf-8')
    return config_path


def test_model_config_resolves_env_and_single_embed(monkeypatch, tmp_path):
    config_path = write_config(
        tmp_path,
        """
        llm:
          source: siliconflow
          type: llm
          model: foo-chat
          api_key: ${TEST_API_KEY}
        reranker:
          source: siliconflow
          type: rerank
          model: foo-rerank
          api_key: ${TEST_API_KEY}
        embed_1:
          - source: siliconflow
            type: embed
            model: foo-embed
            api_key: ${TEST_API_KEY}
        """,
    )
    monkeypatch.setenv('TEST_API_KEY', 'secret-key')

    config = load_model_config(str(config_path), expand_env=True)
    settings = get_retrieval_settings(str(config_path))

    assert config['llm']['api_key'] == 'secret-key'
    assert settings.embed_keys == ['embed_1']
    assert settings.temp_doc_embed_key == 'embed_1'
    assert settings.file_search_embed_key == 'embed_1'
    assert [item['embed_key'] for item in settings.index_kwargs] == ['embed_1']
    assert settings.retriever_configs == [
        {'group_name': 'line', 'embed_keys': ['embed_1'], 'topk': 20, 'target': 'block'},
        {'group_name': 'block', 'embed_keys': ['embed_1'], 'topk': 20},
    ]


def test_model_config_supports_multiple_embeds(monkeypatch, tmp_path):
    config_path = write_config(
        tmp_path,
        """
        llm:
          source: siliconflow
          type: llm
          model: foo-chat
          api_key: ${TEST_API_KEY}
        reranker:
          source: siliconflow
          type: rerank
          model: foo-rerank
          api_key: ${TEST_API_KEY}
        embed_1:
          - source: siliconflow
            type: embed
            model: dense-model
            api_key: ${TEST_API_KEY}
        embed_2:
          - source: siliconflow
            type: embed
            model: sparse-model
            api_key: ${TEST_API_KEY}
            index_kwargs:
              index_type: SPARSE_INVERTED_INDEX
              metric_type: IP
        """,
    )
    monkeypatch.setenv('TEST_API_KEY', 'secret-key')

    settings = get_retrieval_settings(str(config_path))

    assert settings.embed_keys == ['embed_1', 'embed_2']
    assert settings.file_search_embed_key == 'embed_2'
    assert settings.temp_doc_embed_key == 'embed_1'
    assert [item['embed_key'] for item in settings.index_kwargs] == ['embed_1', 'embed_2']
    assert settings.retriever_configs[0]['embed_keys'] == ['embed_1', 'embed_2']
    assert settings.retriever_configs[1]['embed_keys'] == ['embed_1', 'embed_2']


def test_model_config_file_search_defaults_to_last_embed_key(monkeypatch, tmp_path):
    config_path = write_config(
        tmp_path,
        """
        llm:
          source: siliconflow
          type: llm
          model: foo-chat
          api_key: ${TEST_API_KEY}
        reranker:
          source: siliconflow
          type: rerank
          model: foo-rerank
          api_key: ${TEST_API_KEY}
        embed_1:
          - source: siliconflow
            type: embed
            model: foo-embed
            api_key: ${TEST_API_KEY}
        embed_2:
          - source: siliconflow
            type: embed
            model: sparse-model
            api_key: ${TEST_API_KEY}
        """,
    )
    monkeypatch.setenv('TEST_API_KEY', 'secret-key')

    settings = get_retrieval_settings(str(config_path))
    assert settings.file_search_embed_key == 'embed_2'


def test_model_config_expand_env_substitutes_variables(monkeypatch, tmp_path):
    config_path = write_config(
        tmp_path,
        """
        llm:
          source: siliconflow
          type: llm
          model: foo-chat
          api_key: ${TEST_API_KEY}
        reranker:
          source: siliconflow
          type: rerank
          model: foo-rerank
          api_key: ${TEST_API_KEY}
        embed_1:
          - source: siliconflow
            type: embed
            model: foo-embed
            api_key: ${TEST_API_KEY}
        """,
    )
    monkeypatch.setenv('TEST_API_KEY', 'secret-key')

    config = load_model_config(str(config_path), expand_env=True)
    assert config['llm']['api_key'] == 'secret-key'
    assert config['embed_1'][0]['api_key'] == 'secret-key'

    config_no_expand = load_model_config(str(config_path), expand_env=False)
    assert '${TEST_API_KEY}' in config_no_expand['llm']['api_key']


def test_model_config_uses_env_override_path(monkeypatch, tmp_path):
    config_path = write_config(
        tmp_path,
        """
        llm:
          source: siliconflow
          type: llm
          model: foo-chat
          api_key: ${TEST_API_KEY}
        reranker:
          source: siliconflow
          type: rerank
          model: foo-rerank
          api_key: ${TEST_API_KEY}
        embed_1:
          - source: siliconflow
            type: embed
            model: foo-embed
            api_key: ${TEST_API_KEY}
        """,
    )
    monkeypatch.setenv('TEST_API_KEY', 'secret-key')
    monkeypatch.setenv('LAZYMIND_MODEL_CONFIG_PATH', str(config_path))

    config = load_model_config(expand_env=True)
    settings = get_retrieval_settings()

    assert config['llm']['model'] == 'foo-chat'
    assert settings.embed_keys == ['embed_1']

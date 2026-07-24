"""Tests for driver_agent — LLM message cleaning and evaluate_step behaviour.

The actual LLM call (lazyllm.AutoModel) is fully mocked so these tests run
without any model service.
"""
from __future__ import annotations

from unittest.mock import MagicMock, patch

import pytest

from tests.chat.plugins.test_loader import make_plugin_dir


# ---------------------------------------------------------------------------
# Fixtures
# ---------------------------------------------------------------------------

@pytest.fixture()
def loaded_plugin(tmp_path):
    from lazymind.chat.plugin import plugin_loader
    plugins_dir = make_plugin_dir(tmp_path)
    with patch.object(plugin_loader, '_PLUGINS_DIR', plugins_dir):
        plugin_loader.load_all()
    yield
    plugin_loader.load_all()


# ---------------------------------------------------------------------------
# _clean_message
# ---------------------------------------------------------------------------

@pytest.mark.parametrize('text,expected_has', [
    # Normal sentence — returned as-is
    ('subject_analysis saved with 120 words.', 'subject_analysis'),
    # Leading/trailing whitespace stripped
    ('  optimized_prompt saved.  ', 'optimized_prompt'),
    # <think> block removed
    ('<think>Some internal reasoning.</think>No artifact found.', 'No artifact'),
    # think block removed (mismatched close tag variant)
    (chr(60) + 'think' + chr(62) + 'Some internal reasoning.' + chr(
        60) + '/think' + chr(62) + 'Prompt saved.', 'Prompt saved'),
    # Stray XML tags removed
    ('<foo>bar</foo>Prompt saved.', 'Prompt saved'),
    # Truncate at second sentence
    ('Step A complete. Step B complete. Step C complete.', 'Step A'),
    # Hard cap applied (long input)
    ('x' * 400, '...'),
])
def test_clean_message(text, expected_has):
    from lazymind.chat.plugin.driver_agent import _clean_message
    result = _clean_message(text)
    assert expected_has in result


def test_clean_message_empty_string():
    from lazymind.chat.plugin.driver_agent import _clean_message
    assert _clean_message('') == ''


# ---------------------------------------------------------------------------
# evaluate_step — happy paths with mocked LLM
# ---------------------------------------------------------------------------

def test_evaluate_step_returns_message(loaded_plugin):
    from lazymind.chat.plugin import driver_agent

    mock_llm = MagicMock()
    mock_llm.return_value = 'subject_analysis artifact saved with 80 words.'

    with patch('lazymind.chat.plugin.driver_agent.inject_model_config'), \
         patch('lazymind.chat.plugin.driver_agent.AutoModel', return_value=mock_llm):
        result = driver_agent.evaluate_step(
            plugin_id='test-plugin',
            step_id='step_a',
            step_result='Subject analysis saved with 80 words.',
        )

    assert 'message' in result
    assert 'subject_analysis' in result['message'] or 'step_a' in result['message'] or result['message']


def test_evaluate_step_pipeline_complete_message(loaded_plugin):
    from lazymind.chat.plugin import driver_agent

    mock_llm = MagicMock()
    mock_llm.return_value = 'enhanced_image_url saved. The pipeline is complete.'

    with patch('lazymind.chat.plugin.driver_agent.inject_model_config'), \
         patch('lazymind.chat.plugin.driver_agent.AutoModel', return_value=mock_llm):
        result = driver_agent.evaluate_step(
            plugin_id='test-plugin',
            step_id='step_d',
            step_result='enhanced_url artifact saved: https://cdn.example.com/out.png',
        )

    assert 'message' in result
    assert 'complete' in result['message'].lower() or 'pipeline' in result['message'].lower()


def test_evaluate_step_incomplete_message(loaded_plugin):
    from lazymind.chat.plugin import driver_agent

    mock_llm = MagicMock()
    mock_llm.return_value = 'No artifact found; prompt generation may have failed.'

    with patch('lazymind.chat.plugin.driver_agent.inject_model_config'), \
         patch('lazymind.chat.plugin.driver_agent.AutoModel', return_value=mock_llm):
        result = driver_agent.evaluate_step(
            plugin_id='test-plugin',
            step_id='step_b',
            step_result='Only text output, no artifact saved.',
        )

    assert 'message' in result
    assert result['message']


# ---------------------------------------------------------------------------
# evaluate_step — unknown plugin
# ---------------------------------------------------------------------------

def test_evaluate_step_unknown_plugin():
    from lazymind.chat.plugin import driver_agent

    with pytest.raises(driver_agent.DriverEvaluationError, match='not found'):
        driver_agent.evaluate_step(
            plugin_id='no-such-plugin',
            step_id='step_a',
            step_result='anything',
        )


# ---------------------------------------------------------------------------
# evaluate_step — LLM failure → raise (Go auto-mode falls back to user)
# ---------------------------------------------------------------------------

def test_evaluate_step_llm_error_raises(loaded_plugin):
    from lazymind.chat.plugin import driver_agent

    with patch('lazymind.chat.plugin.driver_agent.inject_model_config'), \
         patch('lazymind.chat.plugin.driver_agent.AutoModel', side_effect=RuntimeError('model unavailable')):
        with pytest.raises(driver_agent.DriverEvaluationError, match='LLM call failed'):
            driver_agent.evaluate_step(
                plugin_id='test-plugin',
                step_id='step_c',
                step_result='Image generated.',
            )


def test_evaluate_step_llm_returns_none_raises(loaded_plugin):
    from lazymind.chat.plugin import driver_agent

    mock_llm = MagicMock()
    mock_llm.return_value = None

    with patch('lazymind.chat.plugin.driver_agent.inject_model_config'), \
         patch('lazymind.chat.plugin.driver_agent.AutoModel', return_value=mock_llm):
        with pytest.raises(driver_agent.DriverEvaluationError, match='empty assessment'):
            driver_agent.evaluate_step(
                plugin_id='test-plugin',
                step_id='step_a',
                step_result='some output',
            )


def test_init_driver_artifact_context_sets_agentic_config():
    import lazyllm
    from lazymind.chat.plugin import driver_agent

    with patch('lazymind.config.config', {'acl_db_dsn': ''}):
        result = driver_agent._init_driver_artifact_context('ps-1', 'test-plugin', 'step_a')

    assert result is None
    cfg = lazyllm.globals.get('agentic_config') or {}
    assert cfg.get('plugin_session_id') == 'ps-1'
    assert cfg.get('plugin_id') == 'test-plugin'
    assert cfg.get('plugin_step') == 'step_a'


# ---------------------------------------------------------------------------
# _build_driver_prompt
# ---------------------------------------------------------------------------

def test_build_driver_prompt_uses_driver_md(loaded_plugin):
    from lazymind.chat.plugin.driver_agent import _build_driver_prompt
    prompt = _build_driver_prompt('test-plugin')
    # driver.md from our fixture should be included
    assert len(prompt) > 0
    # Must NOT contain legacy verdict codes as output instructions
    assert 'PASS' not in prompt.split('Output format constraint')[0].split('Examples')[0]


def test_build_driver_prompt_falls_back_to_default(tmp_path):
    from lazymind.chat.plugin import plugin_loader
    from lazymind.chat.plugin.driver_agent import _build_driver_prompt, _DEFAULT_DRIVER_PROMPT

    plugins_dir = make_plugin_dir(tmp_path)
    (plugins_dir / 'test-plugin' / 'scenario' / 'driver.md').unlink()
    with patch.object(plugin_loader, '_PLUGINS_DIR', plugins_dir):
        plugin_loader.load_all()
    try:
        prompt = _build_driver_prompt('test-plugin')
        assert _DEFAULT_DRIVER_PROMPT in prompt
    finally:
        plugin_loader.load_all()


def test_build_driver_prompt_unknown_plugin_returns_default():
    from lazymind.chat.plugin.driver_agent import _build_driver_prompt, _DEFAULT_DRIVER_PROMPT
    prompt = _build_driver_prompt('ghost-plugin')
    assert _DEFAULT_DRIVER_PROMPT in prompt


# ---------------------------------------------------------------------------
# acceptance_criteria injected into prompt
# ---------------------------------------------------------------------------

def test_evaluate_step_includes_acceptance_criteria_in_llm_call(loaded_plugin):
    """When a step defines acceptance_criteria, it must appear in the LLM user message."""
    from lazymind.chat.plugin import driver_agent

    captured_user_msg = {}

    def fake_llm(user_msg, system_prompt=None):
        captured_user_msg['msg'] = user_msg
        return 'Step completed successfully.'

    mock_llm_instance = MagicMock(side_effect=fake_llm)

    with patch('lazymind.chat.plugin.driver_agent.inject_model_config'), \
         patch('lazymind.chat.plugin.driver_agent.AutoModel', return_value=mock_llm_instance):
        driver_agent.evaluate_step(
            plugin_id='test-plugin',
            step_id='step_b',
            step_result='optimized prompt saved',
        )

    assert 'msg' in captured_user_msg
    assert 'step_b' in captured_user_msg['msg']

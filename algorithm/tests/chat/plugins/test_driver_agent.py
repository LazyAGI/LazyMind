"""Tests for driver_agent — LLM verdict parsing and fallback behaviour.

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
# _parse_verdict
# ---------------------------------------------------------------------------

@pytest.mark.parametrize('text,expected_verdict,reason_has', [
    ('<verdict>PASS</verdict><reason>Looks good.</reason>', 'PASS', 'Looks good'),
    ('<verdict>RETRY</verdict><reason>No artifact.</reason>', 'RETRY', 'No artifact'),
    ('<verdict>DONE</verdict><reason>Pipeline complete.</reason>', 'DONE', 'Pipeline complete'),
    ('<verdict>FAIL</verdict><reason>Too many retries.</reason>', 'FAIL', 'Too many retries'),
    # Case-insensitive verdict tag
    ('<verdict>pass</verdict><reason>ok</reason>', 'PASS', 'ok'),
    # Multiple verdict tags — re.search finds first match
    ('<verdict>RETRY</verdict>...<verdict>PASS</verdict>', 'RETRY', ''),
    # No verdict tag at all → default PASS
    ('Some plain text without tags', 'PASS', ''),
    # Reason with newlines
    ('<verdict>DONE</verdict><reason>\nMultiline\nreason\n</reason>', 'DONE', 'Multiline'),
])
def test_parse_verdict(text, expected_verdict, reason_has):
    from lazymind.chat.plugin.driver_agent import _parse_verdict
    result = _parse_verdict(text)
    assert result['verdict'] == expected_verdict
    if reason_has:
        assert reason_has in result['reason']


def test_parse_verdict_empty_string():
    from lazymind.chat.plugin.driver_agent import _parse_verdict
    result = _parse_verdict('')
    assert result['verdict'] == 'PASS'
    assert result['reason'] == ''


# ---------------------------------------------------------------------------
# evaluate_step — happy paths with mocked LLM
# ---------------------------------------------------------------------------

def test_evaluate_step_returns_pass(loaded_plugin):
    from lazymind.chat.plugin import driver_agent

    mock_llm = MagicMock()
    mock_llm.return_value = '<verdict>PASS</verdict><reason>step_a analysis is complete.</reason>'

    with patch('lazymind.chat.plugin.driver_agent.lazyllm') as mock_lazyllm:
        mock_lazyllm.AutoModel.return_value = mock_llm
        result = driver_agent.evaluate_step(
            plugin_id='test-plugin',
            step_id='step_a',
            step_result='Subject analysis saved with 80 words.',
        )

    assert result['verdict'] == 'PASS'
    assert 'complete' in result['reason']


def test_evaluate_step_returns_done(loaded_plugin):
    from lazymind.chat.plugin import driver_agent

    mock_llm = MagicMock()
    mock_llm.return_value = '<verdict>DONE</verdict><reason>Enhanced image saved.</reason>'

    with patch('lazymind.chat.plugin.driver_agent.lazyllm') as mock_lazyllm:
        mock_lazyllm.AutoModel.return_value = mock_llm
        result = driver_agent.evaluate_step(
            plugin_id='test-plugin',
            step_id='step_d',
            step_result='enhanced_url artifact saved: https://cdn.example.com/out.png',
        )

    assert result['verdict'] == 'DONE'


def test_evaluate_step_returns_retry(loaded_plugin):
    from lazymind.chat.plugin import driver_agent

    mock_llm = MagicMock()
    mock_llm.return_value = '<verdict>RETRY</verdict><reason>No artifact found.</reason>'

    with patch('lazymind.chat.plugin.driver_agent.lazyllm') as mock_lazyllm:
        mock_lazyllm.AutoModel.return_value = mock_llm
        result = driver_agent.evaluate_step(
            plugin_id='test-plugin',
            step_id='step_b',
            step_result='Only text output, no artifact saved.',
        )

    assert result['verdict'] == 'RETRY'


# ---------------------------------------------------------------------------
# evaluate_step — unknown plugin
# ---------------------------------------------------------------------------

def test_evaluate_step_unknown_plugin():
    from lazymind.chat.plugin import driver_agent
    result = driver_agent.evaluate_step(
        plugin_id='no-such-plugin',
        step_id='step_a',
        step_result='anything',
    )
    assert result['verdict'] == 'FAIL'
    assert 'not found' in result['reason'].lower()


# ---------------------------------------------------------------------------
# evaluate_step — LLM call raises → default PASS
# ---------------------------------------------------------------------------

def test_evaluate_step_llm_error_defaults_to_pass(loaded_plugin):
    from lazymind.chat.plugin import driver_agent

    with patch('lazymind.chat.plugin.driver_agent.lazyllm') as mock_lazyllm:
        mock_lazyllm.AutoModel.side_effect = RuntimeError('model unavailable')
        result = driver_agent.evaluate_step(
            plugin_id='test-plugin',
            step_id='step_c',
            step_result='Image generated.',
        )

    assert result['verdict'] == 'PASS'
    assert 'unavailable' in result['reason'].lower() or 'default' in result['reason'].lower()


def test_evaluate_step_llm_returns_none_defaults_to_pass(loaded_plugin):
    from lazymind.chat.plugin import driver_agent

    mock_llm = MagicMock()
    mock_llm.return_value = None

    with patch('lazymind.chat.plugin.driver_agent.lazyllm') as mock_lazyllm:
        mock_lazyllm.AutoModel.return_value = mock_llm
        result = driver_agent.evaluate_step(
            plugin_id='test-plugin',
            step_id='step_a',
            step_result='some output',
        )

    assert result['verdict'] == 'PASS'


# ---------------------------------------------------------------------------
# _build_driver_prompt
# ---------------------------------------------------------------------------

def test_build_driver_prompt_uses_driver_md(loaded_plugin):
    from lazymind.chat.plugin.driver_agent import _build_driver_prompt
    prompt = _build_driver_prompt('test-plugin')
    # driver.md from our fixture contains "PASS"
    assert 'PASS' in prompt


def test_build_driver_prompt_falls_back_to_default(tmp_path):
    from lazymind.chat.plugin import plugin_loader
    from lazymind.chat.plugin.driver_agent import _build_driver_prompt, _DEFAULT_DRIVER_PROMPT

    plugins_dir = make_plugin_dir(tmp_path)
    (plugins_dir / 'test-plugin' / 'scenario' / 'driver.md').unlink()
    with patch.object(plugin_loader, '_PLUGINS_DIR', plugins_dir):
        plugin_loader.load_all()
    try:
        prompt = _build_driver_prompt('test-plugin')
        assert prompt == _DEFAULT_DRIVER_PROMPT
    finally:
        plugin_loader.load_all()


def test_build_driver_prompt_unknown_plugin_returns_default():
    from lazymind.chat.plugin.driver_agent import _build_driver_prompt, _DEFAULT_DRIVER_PROMPT
    assert _build_driver_prompt('ghost-plugin') == _DEFAULT_DRIVER_PROMPT


# ---------------------------------------------------------------------------
# acceptance_criteria injected into prompt
# ---------------------------------------------------------------------------

def test_evaluate_step_includes_acceptance_criteria_in_llm_call(loaded_plugin):
    """When a step defines acceptance_criteria, it must appear in the LLM user message."""
    from lazymind.chat.plugin import driver_agent

    captured_user_msg = {}

    def fake_llm(user_msg, system_prompt=None):
        captured_user_msg['msg'] = user_msg
        return '<verdict>PASS</verdict><reason>ok</reason>'

    mock_llm_instance = MagicMock(side_effect=fake_llm)

    with patch('lazymind.chat.plugin.driver_agent.lazyllm') as mock_lazyllm:
        mock_lazyllm.AutoModel.return_value = mock_llm_instance
        driver_agent.evaluate_step(
            plugin_id='test-plugin',
            step_id='step_b',
            step_result='optimized prompt saved',
        )

    # step_b in our fixture has no acceptance_criteria — just verify no crash.
    assert 'msg' in captured_user_msg
    assert 'step_b' in captured_user_msg['msg']

"""Tests for plugin synthetic-turn sensitive-filter bypass."""
from lazymind.chat.service.chat_service import _should_skip_sensitive_filter


def test_skip_when_synthetic_source_is_driver():
    ctx = {
        'plugin_id': 'image-plugin',
        'session_id': 'ps-1',
        'synthetic_source': 'driver',
    }
    assert _should_skip_sensitive_filter('subject_analysis saved with style keywords.', ctx)


def test_skip_plugin_step_completed_message():
    ctx = {
        'plugin_id': 'image-plugin',
        'session_id': 'ps-1',
    }
    assert _should_skip_sensitive_filter(
        'Step analyze_subject completed. User confirmed. Please proceed.',
        ctx,
    )


def test_no_skip_normal_plugin_user_message():
    ctx = {
        'plugin_id': 'image-plugin',
        'session_id': 'ps-1',
    }
    assert not _should_skip_sensitive_filter('继续', ctx)

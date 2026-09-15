"""Tests for workflow synthetic-turn sensitive-filter bypass."""
from lazymind.chat.service.chat_service import _should_skip_sensitive_filter


def test_skip_when_synthetic_source_is_driver():
    ctx = {
        'workflow_id': 'image-workflow',
        'session_id': 'ps-1',
        'synthetic_source': 'driver',
    }
    assert _should_skip_sensitive_filter('subject_analysis saved with style keywords.', ctx)


def test_skip_workflow_step_completed_message():
    ctx = {
        'workflow_id': 'image-workflow',
        'session_id': 'ps-1',
    }
    assert _should_skip_sensitive_filter(
        'Step analyze_subject completed. User confirmed. Please proceed.',
        ctx,
    )


def test_no_skip_normal_workflow_user_message():
    ctx = {
        'workflow_id': 'image-workflow',
        'session_id': 'ps-1',
    }
    assert not _should_skip_sensitive_filter('继续', ctx)


def test_no_skip_synthetic_marker_without_workflow_identity():
    assert not _should_skip_sensitive_filter(
        'Step analyze_subject completed. Please proceed.',
        {'synthetic_source': 'driver'},
    )


def test_no_skip_without_workflow_context():
    assert not _should_skip_sensitive_filter('普通用户消息', None)

from __future__ import annotations

from types import SimpleNamespace

from lazymind.chat.service.chat_service import _context_preview_status


def test_context_preview_status_marks_compression_from_runtime_summary() -> None:
    payload = _context_preview_status(
        {
            'summary_text': 'Earlier turns summarized',
            'covered_through_seq': 18,
            'version': 1,
        },
        llm_enhanced=False,
        task_profile=None,
    )

    assert payload['preview_accuracy'] == 'deterministic'
    assert payload['compression_applied'] is True
    assert payload['compression_covered_through_seq'] == 18
    assert payload['requires_llm'] is False
    assert payload['llm_reason'] == ''


def test_context_preview_status_ignores_empty_or_invalid_summary_state() -> None:
    payload = _context_preview_status(
        {
            'summary_text': '   ',
            'covered_through_seq': 18,
        },
        llm_enhanced=True,
        task_profile=None,
    )

    assert payload['preview_accuracy'] == 'llm_enhanced'
    assert payload['compression_applied'] is False
    assert payload['compression_covered_through_seq'] == 0


def test_context_preview_status_requests_llm_only_for_rule_review() -> None:
    payload = _context_preview_status(
        {},
        llm_enhanced=False,
        task_profile=SimpleNamespace(
            routing_review_required=True,
            routing_review_reason='Need model routing',
        ),
    )

    assert payload['preview_accuracy'] == 'rule_only'
    assert payload['requires_llm'] is True
    assert payload['llm_reason'] == 'Need model routing'

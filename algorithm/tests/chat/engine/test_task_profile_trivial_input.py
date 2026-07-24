from lazymind.chat.engine.prompts.task_profile import resolve_task_profile


def test_blank_and_greeting_inputs_do_not_request_model_routing_review():
    for query in ('', '   ', '你好', 'hello', 'Hi!', '测试'):
        profile = resolve_task_profile(query, thinking_depth='high')

        assert profile.routing_review_required is False
        assert profile.routing_review_reason == ''


def test_greeting_with_attachment_keeps_normal_routing_behavior():
    profile = resolve_task_profile('你好', thinking_depth='high', has_attachments=True)

    assert profile.routing_review_required is True

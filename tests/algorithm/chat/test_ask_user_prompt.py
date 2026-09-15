from lazymind.chat.engine.agent_runtime import AgentRole, PromptBuilder
from lazymind.chat.service.component.event_translator import AgentEventFrameTranslator
from lazymind.chat.service.component.tool_rendering import _tool_result_frame_text
from lazymind.chat.service.component.tool_registry import ASK_USER_TOOL_CONFIG, collect_query_appendices


def test_ask_user_query_appendix_follows_user_input_and_is_not_system_history():
    appendix = '\n'.join(collect_query_appendices([ASK_USER_TOOL_CONFIG]))
    bundle = (
        PromptBuilder.for_role(AgentRole.CHAT)
        .runtime(
            'tools', 'Active Tool Instructions', appendix, 'tool.registry',
            authoritative=True, placement='after_input',
        )
        .input('Ask me one question.', source='user')
        .build()
    )

    assert bundle.current_input.index(appendix) > bundle.current_input.index(
        'Ask me one question.'
    )
    assert appendix and appendix not in bundle.system_prompt
    assert collect_query_appendices([]) == []
    assert collect_query_appendices([ASK_USER_TOOL_CONFIG], 'before') == []


def test_ask_user_tool_result_has_no_visible_preview():
    rendered = _tool_result_frame_text({
        'id': 'call-1',
        'name': 'ask_user',
        'result': 'Question sent to user (ask_id=123). Waiting for answer on next turn.',
    })

    assert rendered.startswith('<tool_result>')
    assert '<trp' not in rendered


def test_ask_pending_event_suppresses_stop_tool_receipt():
    translator = AgentEventFrameTranslator(query='Ask me one question.')

    frames = translator.feed({
        'tag': 'ask_pending',
        'ask_id': '123',
        'questions': [{'text': 'What matters most?', 'type': 'text'}],
    })
    final_frames = translator.finish(
        'Question sent to user (ask_id=123). Waiting for answer on next turn.'
    )

    assert frames[0]['ask_pending']['questions'][0]['text'] == 'What matters most?'
    assert final_frames == []

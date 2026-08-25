from lazymind.chat.engine.prompts.guidance import CLARIFICATION_GUIDANCE
from lazymind.chat.engine.prompts.system_prompt import build_system_prompt
from lazymind.chat.engine.agent_runtime import AgentRole, PromptBuilder
from lazymind.chat.service.component.event_translator import AgentEventFrameTranslator
from lazymind.chat.service.component.tool_rendering import _tool_result_frame_text
from lazymind.chat.service.component.tool_registry import (
    ASK_USER_TOOL_CONFIG,
    collect_query_appendices,
    collect_system_prompt_appendices,
)


def test_ask_user_contract_is_injected_when_tool_is_exposed():
    appendices = collect_system_prompt_appendices([ASK_USER_TOOL_CONFIG])

    prompt = build_system_prompt(True, tool_prompt_appendices=appendices)

    assert 'MUST be an actual `ask_user` function-tool call' in prompt
    assert 'Never put such a question or request in assistant prose.' in prompt
    assert 'This contract controls only the response channel' in prompt


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

    assert 'MUST make an actual `ask_user` function-tool call' in bundle.current_input
    assert 'NEVER write that request as assistant prose' in bundle.current_input
    assert 'every kind of user-facing question or reply request' in bundle.current_input
    assert 'one question at a time' not in bundle.current_input
    assert bundle.current_input.index('Active Tool Instructions') > bundle.current_input.index(
        '### User Instruction'
    )
    assert '\n\nAttention:' in bundle.current_input
    assert 'ALWAYS call the registered `ask_user` function tool' not in bundle.system_prompt
    assert collect_query_appendices([]) == []
    assert collect_query_appendices([ASK_USER_TOOL_CONFIG], 'before') == []


def test_clarification_module_does_not_offer_text_fallback_when_tool_exists():
    assert 'MUST be sent by calling it' in CLARIFICATION_GUIDANCE
    assert 'Only when the tool is absent' in CLARIFICATION_GUIDANCE


def test_ask_user_tool_result_has_no_visible_preview():
    rendered = _tool_result_frame_text({
        'id': 'call-1',
        'name': 'ask_user',
        'result': 'Question sent to user (ask_id=123). Waiting for answer on next turn.',
    })

    assert rendered.startswith('<tool_result>')
    assert '<trp' not in rendered


def test_run_script_failure_preview_surfaces_credential_guidance():
    rendered = _tool_result_frame_text({
        'id': 'call-2',
        'name': 'run_script',
        'result': {
            'status': 'failed',
            'error': (
                'Missing environment variable EXAMPLE_API_KEY required by skill api-skill. '
                'Get it from: https://example.test/api-keys'
            ),
            'hint': 'Configure KEY before retrying this skill.',
            'missing_env': ['EXAMPLE_API_KEY'],
            'setup_commands': ['export EXAMPLE_API_KEY="<your EXAMPLE_API_KEY>"'],
            'api_key_url': 'https://example.test/api-keys',
            'exit_code': 1,
        },
    }, language='zh', preview_value='scripts/fetch.py')

    preview = rendered.split('<tool_result>', 1)[0]
    assert '<trp' in preview
    assert '未能运行完成' in preview
    assert 'EXAMPLE_API_KEY' in preview
    assert 'export EXAMPLE_API_KEY' in preview
    assert 'https://example.test/api-keys' in preview
    assert 'Configure KEY' not in preview
    assert 'export KEY' not in preview
    assert 'scripts/fetch.py' not in preview


def test_run_script_failure_preview_surfaces_stderr():
    rendered = _tool_result_frame_text({
        'id': 'call-3',
        'name': 'run_script',
        'result': {
            'status': 'failed',
            'stderr': 'Traceback: skill script crashed while writing output.json',
            'exit_code': 1,
        },
    }, language='zh', preview_value='scripts/fetch.py')

    assert '<trp' in rendered
    assert '技能脚本未能运行完成' in rendered
    assert 'skill script crashed' in rendered
    assert 'scripts/fetch.py' not in rendered


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

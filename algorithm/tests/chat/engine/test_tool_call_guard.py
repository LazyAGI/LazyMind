from lazyllm.tools.agent import PreparedToolCall, ToolExecutionRecord
from lazymind.chat.engine.agent_runtime.tool_call_guard import ExactRepeatMonitor


def _record(call_id: str):
    arguments = {'query': 'same'}
    prepared = PreparedToolCall(
        tool_call={'id': call_id, 'function': {'name': 'search', 'arguments': arguments}},
        call_id=call_id,
        tool_name='search',
        arguments=arguments,
        validated_arguments=arguments,
    )
    return ToolExecutionRecord(
        prepared,
        {'ok': True, 'value': {'results': ['grounded']}},
    )


def test_identical_records_emit_a_fresh_notice_from_the_third_batch():
    monitor = ExactRepeatMonitor()
    notices = []

    for index in range(4):
        notices.append(monitor.after_tool_batch([_record(f'call-{index}')]).model_context)

    assert notices[:2] == [(), ()]
    assert len(notices[2]) == 1
    assert len(notices[3]) == 1

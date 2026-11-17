import json
import copy
import pytest
from types import SimpleNamespace
from unittest.mock import MagicMock

from lazyllm.module.llms.onlinemodule.base.protocol_ir import ResponseIR
from lazyllm.module.llms.onlinemodule.supplier.openai_responses import chat_to_responses_request
from lazymind.chat.engine.agent_runtime.summarizer import _llm_response_text
from lazymind.chat.engine.subagent import runner
from lazymind.chat.engine.subagent.db import MemorySubAgentStore


@pytest.mark.parametrize('native', [False, True])
@pytest.mark.parametrize('tail', ['missing', 'partial', 'next_assistant', 'duplicate', 'complete_split'])
def test_resume_only_replays_complete_tool_groups(native, tail):
    output = [{'type': 'reasoning', 'id': 'r', 'summary': [], 'encrypted_content': 'opaque'}] + [
        {'type': 'function_call', 'call_id': call_id, 'name': 'render', 'arguments': '{}'}
        for call_id in ('a', 'b')
    ]
    message = ResponseIR.from_responses({'status': 'completed', 'output': output}).message.to_chat()
    content = {'assistant_message': message} if native else {'tool_calls': message['tool_calls']}
    steps = [
        {'seq': 0, 'role': 'assistant', 'content': {'text': 'Earlier complete answer'}},
        {'seq': 1, 'role': 'assistant', 'content': content},
        {'seq': 2, 'role': 'plan', 'content': {'steps': ['render']}},
    ]
    if tail != 'missing':
        steps.append({'seq': 3, 'role': 'tool', 'content': {'tool_results': [
            {'tool_call_id': 'a', 'name': 'render', 'result': 'saved'}]}})
    if tail == 'next_assistant':
        steps.append({'seq': 4, 'role': 'assistant', 'content': {'text': 'premature'}})
    if tail in ('duplicate', 'complete_split'):
        steps.append({'seq': 4, 'role': 'tool', 'content': {'tool_results': [
            {'tool_call_id': 'a' if tail == 'duplicate' else 'b', 'name': 'render', 'result': 'saved'}]}})
    original = copy.deepcopy(steps)
    history = runner._rebuild_history_from_steps(MemorySubAgentStore({'id': 't'}, steps), 't')
    replay = chat_to_responses_request({'messages': history})['input']
    if tail == 'complete_split':
        assert len(history) == 4
        assert [item['call_id'] for item in replay if item.get('type') == 'function_call_output'] == ['a', 'b']
        if native:
            assert replay[1:4] == output
    else:
        assert len(history) == 1
        assert history[0]['content'] == 'Earlier complete answer'
        assert not any(item.get('type') == 'function_call' for item in replay)
    assert steps == original


def test_legacy_resume_calls_retain_name_and_arguments():
    store = MemorySubAgentStore({'id': 't'}, [
        {'seq': 0, 'role': 'assistant', 'content': {'tool_calls': [
            {'id': 'c', 'name': 'render', 'args': {'page': 2}}]}},
        {'seq': 1, 'role': 'tool', 'content': {'tool_results': [
            {'tool_call_id': 'c', 'name': 'render', 'result': 'saved'}]}},
    ])
    history = runner._rebuild_history_from_steps(store, 't')
    call = chat_to_responses_request({'messages': history})['input'][0]
    assert call['name'] == 'render'
    assert json.loads(call['arguments']) == {'page': 2}


def test_resume_roundtrips_provider_continuation():
    output = [
        {'type': 'reasoning', 'id': 'r', 'summary': [], 'encrypted_content': 'opaque'},
        {'type': 'function_call', 'call_id': 'c', 'name': 'render', 'arguments': '{"page":2}'},
    ]
    message = ResponseIR.from_responses({'status': 'completed', 'output': output}).message.to_chat()
    store = MemorySubAgentStore({'id': 't'})
    ctx = SimpleNamespace(db=store, task_id='t')
    runner._persist_step(ctx, 0, {'tag': 'tool_calls', 'tool_calls': message['tool_calls'],
                                  'assistant_message': message})
    store.append_step('t', 1, 'tool', {'tool_results': [
        {'tool_call_id': 'c', 'name': 'render', 'result': 'saved'}]})
    history = runner._rebuild_history_from_steps(store, 't')
    replay = chat_to_responses_request({'messages': history})['input']
    assert replay[:2] == output


def test_background_plan_accepts_content_blocks():
    model = MagicMock()
    model.share.return_value.return_value = {'content': [
        {'type': 'output_text', 'text': '["one","two","three"]'}]}
    assert runner._generate_display_plan(model, 'task') == ['one', 'two', 'three']


def test_completion_accepts_content_blocks_without_retry():
    model = MagicMock()
    verdict = {'completed': True, 'requires_artifact': False, 'artifact_keys': [], 'reason': 'Done.'}
    model.share.return_value.return_value = {'content': [{'type': 'text', 'text': json.dumps(verdict)}]}
    assert runner._evaluate_completion(model, 'task', [], [], 'done') == (True, 'Done.', '')
    assert model.share.call_count == 1


def test_summary_projects_text_and_excludes_reasoning():
    assert _llm_response_text({'reasoning_content': 'private', 'content': [
        {'type': 'text', 'text': '  summary'}, {'type': 'output_text', 'text': ' result  '},
    ]}) == 'summary result'


def test_main_history_restores_canonical_sidecar_without_parsing_text_tags():
    from lazymind.chat.service.component.history import normalize_history_for_agent
    message = {'role': 'assistant', 'content': '<tool_call>literal</tool_call>',
               '_lazyllm_provider_items': {'protocol': 'responses', 'items': []}}
    result = normalize_history_for_agent([{'role': 'assistant', 'history_seq': 3,
                                         'canonical_messages': [message]}])
    assert result == [{**message, 'history_seq': 3}]


def test_translator_emits_private_canonical_message_separately():
    from lazymind.chat.service.component.event_translator import AgentEventFrameTranslator
    message = {'role': 'assistant', 'content': 'done', '_lazyllm_provider_items': {
        'protocol': 'responses', 'items': [{'type': 'reasoning', 'encrypted_content': 'SECRET'}]}}
    frames = AgentEventFrameTranslator(query='q').feed({'tag': 'assistant_message', 'assistant_message': message})
    assert frames[0]['canonical_messages'] == [message]
    assert not frames[0]['text']


def test_canonical_redacts_provider_snapshot_without_mutating_call():
    from lazymind.chat.engine.agent_runtime.message_fields import canonical_history_message
    call = {'id': 'c', 'function': {'name': 'set_user_env', 'arguments': '{"name":"KEY","value":"SECRET"}'}}
    message = {'role': 'assistant', 'tool_calls': [call], '_lazyllm_provider_items': {
        'protocol': 'responses', 'items': [{'type': 'function_call', 'call_id': 'c',
                                           'name': 'set_user_env', 'arguments': call['function']['arguments']}]}}
    assert 'SECRET' not in json.dumps(canonical_history_message(message))
    assert 'SECRET' in call['function']['arguments']


def test_canonical_workflow_receipts_still_compact():
    from lazymind.chat.service.component.event_translator import AgentEventFrameTranslator
    from lazymind.chat.service.component.history import normalize_history_for_agent
    translator = AgentEventFrameTranslator(query='q')
    frames = translator.feed({'tag': 'tool_calls', 'tool_calls': [
        {'id': 'c', 'function': {'name': 'get_workflow_state', 'arguments': '{}'}}]})
    frames += translator.feed({'tag': 'tool_results', 'tool_results': [
        {'id': 'c', 'name': 'get_workflow_state', 'result': {
            'status': 'running', 'workflow_state': {'nodes': ['HUGE_GRAPH_NODE']}}}]})
    messages = [message for frame in frames for message in frame.get('canonical_messages', [])]
    restored = normalize_history_for_agent([{'canonical_messages': messages}], compact_workflow_receipts=True)
    assert 'HUGE_GRAPH_NODE' not in json.dumps(restored)

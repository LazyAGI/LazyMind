import copy
import json

import pytest

from lazyllm.module.llms.onlinemodule.base.model_request import ModelRequest
from lazyllm.module.llms.onlinemodule.supplier.openai_responses import chat_to_responses_request
from lazymind.chat.engine.agent_runtime.model_calls import request_tokens


@pytest.mark.parametrize('length', [100, 554 * 1024 * 4 // 3])
def test_responses_image_budget_matches_completions(length):
    chat = {'model': 'test', 'messages': [{'role': 'user', 'content': [
        {'type': 'text', 'text': 'Describe this JPEG'},
        {'type': 'image_url', 'image_url': {'detail': 'low',
                                            'url': 'data:image/jpeg;base64,' + 'A' * length}},
    ]}]}
    responses = chat_to_responses_request(chat)
    estimates = [request_tokens(ModelRequest(json.dumps(body), 'VLM', True))
                 for body in (chat, responses)]
    assert max(estimates) < 2000
    assert abs(estimates[0] - estimates[1]) < 30
    assert responses['input'][0]['content'][1]['detail'] == 'low'


def test_ir_keeps_tool_pairs_media_and_opaque_continuation():
    from lazyllm.module.llms.onlinemodule.base.protocol_ir import RequestIR
    body = {'model': 'test', 'input': [
        {'type': 'reasoning', 'id': 'rs_1', 'encrypted_content': 'opaque', 'summary': []},
        {'type': 'function_call', 'call_id': 'c1', 'name': 'render', 'arguments': '{"page":1}'},
        {'type': 'function_call_output', 'call_id': 'c1', 'output': 'saved'},
        {'role': 'user', 'content': [{'type': 'input_image', 'detail': 'high',
                                      'image_url': 'https://example.test/p.jpg'}]},
    ]}
    original = copy.deepcopy(body)
    request = RequestIR.from_payload(body, protocol='responses')
    assert request.messages[1].tool_calls[0].id == 'c1'
    assert request.messages[2].tool_call_id == 'c1'
    assert request.messages[3].content[0].kind == 'image'
    assert request.to_payload('responses') == original
    assert body == original
    with pytest.raises(ValueError, match='protocol'):
        request.to_payload('gemini')


@pytest.mark.parametrize('content', [None, [], [{'type': 'text', 'text': 'Finished'}]])
def test_agent_finalization_accepts_optional_typed_content(content):
    from types import SimpleNamespace
    from lazyllm.tools.agent.functionCall import FunctionCall
    state = SimpleNamespace(_runtime_observer=None)
    message = {} if content is None else {'content': content}
    expected = 'Finished' if content else ''
    assert FunctionCall._post_action(state, message) == expected


def test_response_ir_preserves_incomplete_even_when_it_has_tools():
    from lazyllm.module.llms.onlinemodule.supplier.openai_responses import responses_to_chat_chunk
    chunk = responses_to_chat_chunk({'status': 'incomplete', 'incomplete_details': {'reason': 'max_output_tokens'},
                                     'output': [{'type': 'function_call', 'call_id': 'c1',
                                                 'name': 'render', 'arguments': '{'}]})
    assert chunk['choices'][0]['finish_reason'] == 'length'


def test_responses_refusal_is_not_an_empty_success():
    from lazyllm.module.llms.onlinemodule.supplier.openai_responses import responses_to_chat_chunk
    chunk = responses_to_chat_chunk({'status': 'completed', 'output': [
        {'type': 'message', 'content': [{'type': 'refusal', 'refusal': 'Cannot comply'}]},
    ]})
    assert chunk['choices'][0]['finish_reason'] == 'content_filter'


def test_advanced_policy_preserves_local_capacity_error(monkeypatch):
    from lazyllm import AutoModel
    from lazyllm.common import new_session
    from lazyllm.module.llms.model_config_inject import inject_model_config
    from lazyllm.module.llms.onlinemodule.base.model_request import ModelCallControl
    from lazymind.chat.engine.agent_runtime.model_calls import ContextCapacityError
    from lazymind.provider_request import configure_request_advanced

    def reject(request):
        raise ContextCapacityError('Input exceeds the configured context capacity')

    def unexpected(*args, **kwargs):
        pytest.fail('Capacity rejection must happen before HTTP')

    monkeypatch.setattr('requests.Session.send', unexpected)
    with new_session('ir-capacity-error'):
        config = {'llm': {'source': 'openai_responses', 'model': 'gpt-6-sol', 'api_key': 'test-key',
                          'base_url': 'https://provider.example/v1/',
                          'request_advanced': {'headers': {'X-Test': 'test'}}}}
        inject_model_config(config)
        configure_request_advanced(config)
        model = AutoModel(source='dynamic', name='llm', dynamic_auth=True)
        with pytest.raises(ContextCapacityError):
            model('Hi', stream_output=False, call_control=ModelCallControl(request_check=reject))


@pytest.mark.parametrize('source', ['openai', 'openai_responses'])
def test_jpeg_reaches_http_after_semantic_admission(monkeypatch, tmp_path, source):
    from io import BytesIO
    from PIL import Image
    import requests
    from lazyllm import OnlineChatModule
    from lazyllm.module.llms.onlinemodule.base.model_request import ModelCallControl

    path = tmp_path / 'attachment.jpg'
    Image.new('RGB', (32, 32), 'blue').save(path)
    with path.open('ab') as file:
        file.write(b'\0' * (554 * 1024))
    sent, budgets = [], []

    def send(session, request, **kwargs):
        sent.append(json.loads(request.body))
        response = requests.Response()
        response.status_code = 200
        payload = ({'status': 'completed', 'output': [
            {'type': 'message', 'content': [{'type': 'output_text', 'text': 'A blue square'}]},
        ]} if source == 'openai_responses' else {'choices': [
            {'message': {'role': 'assistant', 'content': 'A blue square'}, 'finish_reason': 'stop'},
        ]})
        response._content = json.dumps(payload).encode()
        response._content_consumed = True
        response.raw = BytesIO()
        return response

    def admit(request):
        budgets.append(request_tokens(request))
        assert budgets[-1] < 2500
        assert request.ir.messages[-1].content[-1].kind == 'image'

    monkeypatch.setattr(requests.Session, 'send', send)
    model = OnlineChatModule(source=source, model='vision-test', type='vlm', api_key='test', stream=False)
    assert model('Describe', lazyllm_files=[str(path)], call_control=ModelCallControl(request_check=admit)) == (
        'A blue square')
    assert len(sent) == len(budgets) == 1
    body = json.dumps(sent[0])
    assert 'data:image/jpeg;base64,' in body


def test_request_ir_is_available_before_a_future_wire_adapter(monkeypatch):
    from lazyllm import OnlineChatModule
    from lazyllm.module.llms.onlinemodule.base.model_request import ModelCallControl
    from lazyllm.common import HandledException
    model = OnlineChatModule(source='openai', model='test', api_key='test')
    # A future adapter can use an entirely different wire layout without changing admission.
    monkeypatch.setattr(type(model), '_prepare_request_data', lambda self, data: {'future_wire': data['messages']})
    observed = []

    class StopBeforeHTTP(HandledException):
        pass

    def check(request):
        observed.append(request.ir)
        raise StopBeforeHTTP()

    with pytest.raises(StopBeforeHTTP):
        model('Semantic input', call_control=ModelCallControl(request_check=check))
    assert observed[0].messages[-1].text == 'Semantic input'


def test_reasoning_replay_does_not_restore_compacted_text():
    from lazyllm.module.llms.onlinemodule.supplier.openai_responses import responses_to_chat_chunk
    reasoning = {'type': 'reasoning', 'id': 'rs_1', 'summary': [], 'encrypted_content': 'opaque'}
    message = responses_to_chat_chunk({'status': 'completed', 'output': [
        reasoning,
        {'type': 'message', 'content': [{'type': 'output_text', 'text': 'old verbose text'}]},
        {'type': 'function_call', 'call_id': 'c1', 'name': 'render', 'arguments': '{}'},
    ]})['choices'][0]['message']
    message['content'] = 'compacted'
    body = chat_to_responses_request({'messages': [message]})
    assert body['input'][0] == reasoning
    assert body['input'][1]['content'] == 'compacted'
    assert 'old verbose text' not in json.dumps(body)


def test_context_budget_ignores_encrypted_continuation_bytes():
    from lazymind.chat.engine.agent_runtime.message_fields import estimate_message_tokens
    from lazyllm.module.llms.onlinemodule.supplier.openai_responses import responses_to_chat_chunk

    def message(length):
        return responses_to_chat_chunk({'output': [
            {'type': 'reasoning', 'encrypted_content': 'x' * length, 'summary': []},
            {'type': 'message', 'content': [{'type': 'output_text', 'text': 'Ready'}]},
        ]})['choices'][0]['message']

    assert estimate_message_tokens(message(10)) == estimate_message_tokens(message(100000))


def test_empty_responses_result_has_explicit_non_success_finish():
    from lazyllm.module.llms.onlinemodule.supplier.openai_responses import responses_to_chat_chunk
    chunk = responses_to_chat_chunk({'status': 'completed', 'output': []})
    assert chunk['choices'][0]['finish_reason'] == 'unknown'


def test_completions_boundary_does_not_send_responses_private_fields(monkeypatch):
    from lazyllm import OnlineChatModule
    from lazyllm.module.llms.onlinemodule.base.model_request import ModelCallControl
    from lazyllm.common import HandledException
    model = OnlineChatModule(source='openai', model='gpt-test', api_key='test')
    history = [{'role': 'assistant', 'content': 'Prior text', '_lazyllm_provider_items': {
        'protocol': 'responses', 'items': [{'type': 'reasoning', 'encrypted_content': 'private'}]},
    }]
    observed = []

    class StopBeforeHTTP(HandledException):
        pass

    def check(request):
        observed.append(request.payload)
        raise StopBeforeHTTP()

    with pytest.raises(StopBeforeHTTP):
        model('Continue', llm_chat_history=history, call_control=ModelCallControl(request_check=check))
    assert 'Prior text' in json.dumps(observed)
    assert '_lazyllm_provider_items' not in json.dumps(observed)
    assert 'encrypted_content' not in json.dumps(observed)
    assert history[0]['_lazyllm_provider_items']['items'][0]['encrypted_content'] == 'private'


def test_continuation_order_and_no_empty_assistant_message():
    from lazyllm.module.llms.onlinemodule.supplier.openai_responses import responses_to_chat_chunk
    output = [
        {'type': 'reasoning', 'id': 'r1', 'summary': [], 'encrypted_content': 'first'},
        {'type': 'function_call', 'call_id': 'c1', 'name': 'render', 'arguments': '{}'},
        {'type': 'reasoning', 'id': 'r2', 'summary': [], 'encrypted_content': 'second'},
        {'type': 'function_call', 'call_id': 'c2', 'name': 'render', 'arguments': '{}'},
    ]
    message = responses_to_chat_chunk({'status': 'completed', 'output': output})['choices'][0]['message']
    assert chat_to_responses_request({'messages': [message]})['input'] == output


@pytest.mark.parametrize('with_prefix', [False, True])
def test_text_and_tool_relative_order_survives_round_trip(with_prefix):
    from lazyllm.module.llms.onlinemodule.supplier.openai_responses import responses_to_chat_chunk
    output = [{'type': 'function_call', 'call_id': 'c1', 'name': 'render', 'arguments': '{}'},
              {'type': 'message', 'content': [{'type': 'output_text', 'text': 'after'}]}]
    if with_prefix:
        output[:0] = [{'type': 'message', 'content': [{'type': 'output_text', 'text': 'before'}]},
                      {'type': 'reasoning', 'summary': [], 'encrypted_content': 'opaque'}]
    message = responses_to_chat_chunk({'status': 'completed', 'output': output})['choices'][0]['message']
    replay = chat_to_responses_request({'messages': [message]})['input']
    assert [item.get('type', 'message') for item in replay] == [item['type'] for item in output]
    expected_text = ['before', 'after'] if with_prefix else ['after']
    assert [item['content'] for item in replay if 'content' in item] == expected_text
    message['content'] = 'compressed'
    replay = chat_to_responses_request({'messages': [message]})['input']
    assert [item['content'] for item in replay if 'content' in item] == ['compressed']


def test_legacy_image_block_still_has_media_budget():
    from lazymind.chat.engine.agent_runtime.message_fields import estimate_message_tokens

    def message(length):
        return {'role': 'user', 'content': [{'type': 'image', 'source': {
            'type': 'base64', 'media_type': 'image/jpeg', 'data': 'A' * length}}]}

    assert estimate_message_tokens(message(10)) == estimate_message_tokens(message(100000))
    assert estimate_message_tokens(message(100000)) < 2000


@pytest.mark.parametrize('content', [{'status': 'ok', 'value': 42}, [{'value': 42}], 42])
def test_structured_legacy_tool_results_keep_values(content):
    body = chat_to_responses_request({'messages': [{'role': 'tool', 'tool_call_id': 'c1', 'content': content}]})
    assert json.loads(body['input'][0]['output']) == content


@pytest.mark.parametrize('stream', [False, True])
@pytest.mark.parametrize('source', ['openai', 'openai_responses'])
def test_workflow_tool_round_through_native_http(monkeypatch, tmp_path, source, stream):
    from io import BytesIO
    import requests
    import lazyllm
    from lazyllm.tools.agent import ReactAgent

    sent = []
    artifact = tmp_path / 'slide.html'

    def render_slide() -> str:
        """Render a presentation page and return its path."""
        artifact.write_text('<h1>Slide</h1>')
        return str(artifact)

    def send(session, request, **kwargs):
        body = json.loads(request.body)
        sent.append(body)
        first = len(sent) == 1
        call = {'id': 'call_render', 'type': 'function', 'function': {'name': 'render_slide', 'arguments': '{}'}}
        message = {'tool_calls': [call]} if first else {'content': 'Presentation saved'}
        if source == 'openai_responses':
            output = ([{'type': 'reasoning', 'id': 'rs_1', 'summary': [], 'encrypted_content': 'opaque'},
                       {'type': 'function_call', 'call_id': 'call_render', 'name': 'render_slide', 'arguments': '{}'}]
                      if first else [{'type': 'message', 'content': [
                          {'type': 'output_text', 'text': 'Presentation saved'}]}])
            payload = {'status': 'completed', 'output': output}
            if stream:
                payload = {'type': 'response.completed', 'response': payload}
        else:
            payload = {'choices': [{'index': 0, 'delta' if stream else 'message': message,
                                    'finish_reason': 'tool_calls' if first else 'stop'}]}
        response = requests.Response()
        response.status_code = 200
        text = json.dumps(payload)
        response._content = (('data: ' + text + '\n\ndata: [DONE]\n\n') if stream else text).encode()
        response._content_consumed = True
        response.raw = BytesIO()
        return response

    monkeypatch.setattr(requests.Session, 'send', send)
    with lazyllm.new_session():
        model = lazyllm.OnlineChatModule(source=source, model='gpt-test', api_key='test', stream=stream)
        agent = ReactAgent(model, tools=[render_slide], enable_builtin_tools=False,
                           prompt='Render and summarize.', stream=stream, max_retries=3)
        assert agent('Create one slide') == 'Presentation saved'
    assert artifact.read_text() == '<h1>Slide</h1>'
    assert len(sent) == 2
    if source == 'openai_responses':
        items = sent[1]['input']
        assert any(item.get('type') == 'reasoning' and item['encrypted_content'] == 'opaque' for item in items)
        assert any(item.get('type') == 'function_call_output' and item['call_id'] == 'call_render' for item in items)
    else:
        assert any(item.get('role') == 'tool' and item['tool_call_id'] == 'call_render'
                   for item in sent[1]['messages'])

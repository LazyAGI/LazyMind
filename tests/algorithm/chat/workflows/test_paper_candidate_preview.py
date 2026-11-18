import json
from concurrent.futures import ThreadPoolExecutor

import lazyllm
import pytest

from fallback_test_support import candidate, sse_frame, wrapper
from lazymind.chat.engine.agent_runtime.model_calls import enable_tool_call_stream
from lazymind.chat.engine.agent_runtime.model_cancellation import CancellableLLM
from lazymind.document_tools.paper.runtime_events import AgentCandidatePreview
from lazyllm.module.llms.onlinemodule.base.onlineChatModuleBase import LazyLLMOnlineChatModuleBase


@pytest.mark.parametrize('step,field', list(AgentCandidatePreview.FIELDS.items()))
def test_candidate_text_streams_before_the_submission_is_complete(step, field):
    module = LazyLLMOnlineChatModuleBase.__new__(LazyLLMOnlineChatModuleBase)
    chunks = []
    module._stream_sink = chunks.append
    module._stream = True
    enable_tool_call_stream(module)
    parser = module._response_parser(True)
    preview = AgentCandidatePreview(step)
    text = '# Paper\n\n## Section\nEvidence: "observed" 中文.'
    arguments = json.dumps({'output': {field: text}, 'receipts': []}, ensure_ascii=True)
    split = arguments.index('Evidence:')
    parser.parse_json_payload(json.dumps({'choices': [{'delta': {'tool_calls': [{
        'index': 0, 'id': 'candidate-1', 'function': {
            'name': 'paper_submit_candidate', 'arguments': arguments[:split]}}]}}]}))
    first = [item for chunk in chunks for item in preview.feed(chunk)]
    assert any(item['type'] == 'artifact_stream' and '# Paper' in item['delta'] for item in first)
    assert all(item['slot'] == step + '_document' for item in first)
    chunks.clear()
    parser.parse_json_payload(json.dumps({'choices': [{'delta': {'tool_calls': [{
        'index': 0, 'function': {'arguments': arguments[split:]}}]}}]}))
    rest = [item for chunk in chunks for item in preview.feed(chunk)]
    rest += preview.feed({'tag': 'tool_calls', 'tool_calls': []})
    assert ''.join(item['delta'] for item in first + rest if item['type'] == 'artifact_stream') == text
    assert rest[-1]['type'] == 'artifact_stream_end'
    assert 'receipts' not in ''.join(item.get('delta', '') for item in first + rest)


@pytest.mark.parametrize('mode', ['cancellable', 'dynamic', 'fallback'])
@pytest.mark.parametrize('stream_in_call', [False, True])
def test_runtime_streams_candidate_arguments_without_changing_the_shared_model(
    provider, monkeypatch, mode, stream_in_call,
):
    def respond(request):
        text = '# ' + request['messages'][-1]['content']
        arguments = json.dumps({'output': {'manuscript': text}, 'receipts': []})
        return {'message': {'role': 'assistant', 'content': ''}, 'frames': [
            sse_frame({'content': 'Writing'}),
            sse_frame({'tool_calls': [{'index': 0, 'id': 'candidate', 'type': 'function', 'function': {
                'name': 'paper_submit_candidate', 'arguments': arguments[:20]}}]}),
            sse_frame({'tool_calls': [{'index': 0, 'function': {'arguments': arguments[20:]}}]},
                      finish='tool_calls'),
        ]}

    provider.on_request = respond
    if mode == 'fallback':
        model = wrapper(candidate(provider, 'model-b'), [])
        raw = model.raw_llm
    else:
        if mode == 'dynamic':
            from lazyllm import inject_model_config
            monkeypatch.setattr('lazymind.model_config.get_dynamic_role_slot_map', lambda: {'llm': 'chat'})
            inject_model_config(provider.config('model-b'))
            raw = lazyllm.AutoModel(source='dynamic', type='llm', name='llm', dynamic_auth=True)
        else:
            raw = lazyllm.OnlineChatModule(source='openai', model='model-b', url=provider.url,
                                           api_key='test-key', type='llm', stream=False)
        model = CancellableLLM(raw, cancel_check=lambda _: False)

    session = lazyllm.globals._sid

    def run(name):
        chunks = []
        preview = AgentCandidatePreview('write_document')
        events = []

        def sink(event):
            chunks.append(event)
            events.extend(preview.feed(event))

        with lazyllm.globals._bind_sid(session), lazyllm.locals._scope():
            stream = {'_stream_sink': sink}
            view = model.share(format=lambda response: response, stream=False if stream_in_call else stream)
            result = view(name, **({'stream_output': stream} if stream_in_call else {}))
        events.extend(preview.feed({'tag': 'tool_calls'}))
        assert json.loads(result['tool_calls'][0]['function']['arguments'])['output']['manuscript'] == '# ' + name
        assert ''.join(event['delta'] for event in events if event['type'] == 'artifact_stream') == '# ' + name
        assert ''.join(event['delta'] for event in chunks if event.get('tag') == 'text') == 'Writing'
        assert len([event for event in chunks if event.get('tag') == 'tool_call_delta']) == 2

    # Each invocation configures its own supplier; a shared model keeps independent streams.
    with ThreadPoolExecutor(max_workers=2) as pool:
        list(pool.map(run, ['First', 'Second']))
    chunks = []
    raw.share(stream={'_stream_sink': chunks.append})('Unadapted')
    assert not any(event.get('tag') == 'tool_call_delta' for event in chunks)


def test_preview_ignores_sources_and_withdraws_a_rolled_back_candidate():
    preview = AgentCandidatePreview('write_document')

    def delta(name, args):
        return {'tag': 'tool_call_delta', 'delta': json.dumps([{'index': 0, 'function': {
            'name': name, 'arguments': args}}])}
    assert preview.feed(delta('paper_read_material', '{"secret":"source"}')) == []
    preview.feed({'tag': 'tool_calls'})
    assert preview.feed(delta('paper_submit_candidate', '{"output":{"manuscript":"# Draft'))
    aborted = preview.feed({'tag': 'model_output', 'model_output': {'phase': 'rollback'}})
    assert aborted[-1]['type'] == 'artifact_stream_abort'
    recovered = preview.feed(delta('paper_submit_candidate', '{"output":{"manuscript":"# Recovered"}}'))
    recovered += preview.feed({'tag': 'tool_calls'})
    assert ''.join(event.get('delta', '') for event in recovered) == '# Recovered'

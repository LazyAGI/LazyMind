"""Core native-response and line-repair contracts, without live model calls."""
import json
from types import SimpleNamespace

import lazyllm
import pytest

from lazymind.document_tools.paper.analysis import trace
from lazymind.document_tools.paper.model_context import model_prompt
from lazymind.document_tools.paper.plotting import model
from lazymind.document_tools.paper.plotting.code_response import (
    normalize_response, parse_code_response, repair_response,
)


def test_local_repair_applies_ranges_against_the_same_source():
    source = 'def analyze():\n    score = 1\n    cost = 2\n    return score, cost\n'
    patched = repair_response(dict(replacements=[
        dict(start_line=3, end_line=3, code='    cost = 4'),
        dict(start_line=2, end_line=2, code='    score = 3'),
    ]), source)
    namespace = {}
    exec(patched, namespace)
    assert namespace['analyze']() == (3, 4)
    assert patched.splitlines()[0] == source.splitlines()[0]
    assert patched.splitlines()[-1] == source.splitlines()[-1]


def test_overlapping_ranges_are_not_applied():
    with pytest.raises(ValueError, match='重叠'):
        repair_response(dict(replacements=[dict(start_line=1, end_line=2, code='pass'),
                                           dict(start_line=2, end_line=2, code='pass')]), 'a\nb\n')


def test_code_is_read_from_native_body_without_merging_reasoning():
    code = 'def draw(data, statistics, spec):\n    return figure'
    response = normalize_response(dict(content=f'```python\n{code}\n```',
                                       reasoning_content='Unfinished draft code'))
    assert parse_code_response(response.body)['code'] == code
    assert response.reasoning == 'Unfinished draft code'
    assert normalize_response(dict(content=code, reasoning='Draft')).reasoning == 'Draft'
    assert normalize_response(dict(content=code, reasoning_details=[dict(text='Draft')])).reasoning == 'Draft'
    with pytest.raises(ValueError, match='未返回代码'):
        parse_code_response(normalize_response(dict(reasoning_content=code)).body)


@pytest.mark.parametrize('stream', [False, True])
@pytest.mark.parametrize('stage,method,body,expected', [
    ('plan_generation', 'call_model', '  {"questions":["Observed means?"],"outline":"Group means"}\n',
     dict(questions=['Observed means?'], outline='Group means')),
    ('figure_code_generation', 'call_code_model', '```python\nimport matplotlib.pyplot as plt\n```\n',
     dict(code='import matplotlib.pyplot as plt')),
    ('interpretation', 'call_markdown_model', '## Conclusion\n\nMean score: 2.\n',
     '## Conclusion\n\nMean score: 2.\n'),
])
def test_native_provider_channels_through_sdk_and_f4(monkeypatch, tmp_path, stream, stage, method, body, expected):
    from lazyllm.module.llms.onlinemodule.supplier.minimax import MinimaxChat
    from lazyllm.module.llms.onlinemodule.chat import OnlineChatModule, dynamic_chat_config

    client = OnlineChatModule(source='dynamic')
    unrelated = MinimaxChat(api_key='offline-test', model='other-flow')
    converter = MinimaxChat._convert_msg_format
    calls, thinking = [], '\n' * 3

    class Response:
        status_code = 200
        raw = SimpleNamespace()
        text = json.dumps(dict(choices=[dict(index=0, finish_reason='stop', message=dict(
            role='assistant', content=body, reasoning_content=thinking))]))

        def __enter__(self):
            return self

        def __exit__(self, *_):
            pass

        def iter_lines(self):
            fields = (dict(reasoning_details=[dict(text=thinking)]) if method == 'call_markdown_model'
                      else dict(reasoning=thinking))
            deltas = [fields, dict(content=body[:7]), dict(content=body[7:])]
            for delta in deltas:
                yield ('data: ' + json.dumps(dict(choices=[dict(index=0, delta=delta)]))).encode()
                if delta is fields:
                    step = json.loads((recorder.root / 'public.json').read_text())['process_steps'][0]
                    assert step['reasoning']['text'] == thinking and not step.get('output', {}).get('text')
            yield b'data: {"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}'
            yield b'data: [DONE]'

    def post(_url, **kwargs):
        calls.append(kwargs['json'])
        return Response()

    monkeypatch.setattr('requests.post', post)
    monkeypatch.setattr(lazyllm, 'AutoModel', lambda **_: client)
    monkeypatch.setenv('LAZYMIND_PAPER_MODEL_STREAM', 'auto' if stream else 'off')
    prompt = model_prompt('Offline request', facts={}, tables={})
    with dynamic_chat_config(modules=client, source='openai' if stream else 'minimax', model='f4-test',
                             url='http://provider.test/v1/', skip_auth=True), \
            trace.capture(tmp_path, 'native-test', emit=lambda _: None) as recorder:
        with lazyllm.globals.stack_enter(client.identities):
            client._get_supplier()  # Cached before F4 configures the actual caller.
        with trace.stage(stage, attempt=1):
            assert getattr(model, method)(prompt) == expected
    step = json.loads((recorder.root / 'public.json').read_text())['process_steps'][0]
    assert step['output']['text'] == body and step['reasoning']['text'] == thinking
    assert len(calls) == 1 and calls[0]['stream'] == stream
    assert calls[0]['messages'] == prompt['input']
    assert calls[0].get('reasoning_split') is (None if stream else True)
    assert ('response_format' in calls[0]) == (method == 'call_model')
    assert MinimaxChat._convert_msg_format is converter
    assert '_convert_msg_format' not in unrelated.__dict__ and unrelated._model_optional_params == {}

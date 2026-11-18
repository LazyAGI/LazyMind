"""Cancel real workflow Agent model requests without fallback candidates."""
import asyncio
import http.client
import json
import threading
import time
import uuid
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

import lazyllm
import pytest

from lazymind.chat.engine.agent_runtime import AgentExecutor
from lazymind.chat.engine.agent_runtime.cancellation import UserCancelledError, request_cancel
from lazymind.chat.engine.subagent.context import SubAgentContext
from lazymind.chat.engine.subagent.runner import _build_subagent_plan
from lazymind.chat.engine.tools.workspace_context import WorkspaceContext
from lazymind.model_config import inject_model_config


@pytest.mark.parametrize('phase', ['headers', 'body'])
@pytest.mark.parametrize('source', ['openai', 'openai_responses'])
@pytest.mark.parametrize('dynamic', [False, True])
def test_workflow_agent_cancels_model_wait_and_can_run_again(monkeypatch, tmp_path, phase, source, dynamic):
    entered_read, reached_server, release, finished = (threading.Event() for _ in range(4))
    requests, outcomes, results = [], [], []
    sid = 'workflow-model-cancel-' + uuid.uuid4().hex
    for method in (['_read_status'] if phase == 'headers' else ['read', 'read1']):
        original_read = getattr(http.client.HTTPResponse, method)

        def observed_read(response, *args, _read=original_read, **kwargs):
            entered_read.set()
            return _read(response, *args, **kwargs)

        monkeypatch.setattr(http.client.HTTPResponse, method, observed_read)
    monkeypatch.setattr('lazymind.model_config.get_dynamic_role_slot_map', lambda: {'llm': 'chat'})
    if source == 'openai_responses':
        event = {'type': 'response.completed', 'response': {'status': 'completed', 'output': [
            {'type': 'message', 'content': [{'type': 'output_text', 'text': 'Ready'}]},
        ]}}
    else:
        event = {'choices': [{'index': 0, 'delta': {'content': 'Ready'}, 'finish_reason': 'stop'}]}
    body = ('data: ' + json.dumps(event) + '\n\ndata: [DONE]\n\n').encode()

    class Handler(BaseHTTPRequestHandler):
        def log_message(self, *_args):
            pass

        def do_POST(self):
            requests.append(json.loads(self.rfile.read(int(self.headers['Content-Length']))))
            first = len(requests) == 1
            reached_server.set()
            if phase == 'headers' and first:
                release.wait(5)
            self.send_response(200)
            self.send_header('Content-Type', 'text/event-stream')
            self.send_header('Content-Length', str(len(body)))
            self.end_headers()
            self.wfile.flush()
            if phase == 'body' and first:
                release.wait(5)
            try:
                self.wfile.write(body)
            except OSError:
                pass  # The cancelled client's socket has already closed.

    server = ThreadingHTTPServer(('127.0.0.1', 0), Handler)
    serving = threading.Thread(target=lambda: server.serve_forever(poll_interval=0.01), daemon=True)
    serving.start()
    url = f'http://127.0.0.1:{server.server_port}/v1/'
    config = {'llm': {'source': source, 'model': 'test-model', 'base_url': url, 'api_key': 'test-key'}}

    def publish_result():
        """Publish the generated workflow result."""
        raise AssertionError('cancellation must prevent tool execution')

    def execute():
        with lazyllm.globals._bind_sid(sid), lazyllm.locals._scope():
            inject_model_config(config)
            model = (lazyllm.AutoModel(source='dynamic', type='llm', name='llm', dynamic_auth=True)
                     if dynamic else lazyllm.OnlineChatModule(
                         source=source, model='test-model', url=url, api_key='test-key', type='llm'))
            ctx = SubAgentContext(
                task_id='cancel-test', conversation_id='conversation', agent_type='workflow_step',
                objective='Return Ready.', params={'tools_only': True}, workspace_path=str(tmp_path),
                input_slots=[], output_slots=[], db=None, emit=lambda _: None,
            )
            plan = _build_subagent_plan(
                ctx, None, tools=[publish_result], tool_prompt_appendices={}, llm_config=config,
                workspace_permission=WorkspaceContext(local_runtime=False),
            )
            assert plan.execution_options.model_candidates == ()
            executor = AgentExecutor()

            async def drive():
                agent = executor.create_agent(model, plan)
                async for kind, value in executor.stream_agent(agent, plan):
                    if kind == 'final':
                        results.append(value)

            try:
                asyncio.run(drive())
            except (Exception, asyncio.CancelledError) as exc:
                outcomes.append(exc)
            finally:
                finished.set()
            release.wait(5)
            # Use the same session and original model after the first run exits.
            try:
                asyncio.run(drive())
            except (Exception, asyncio.CancelledError) as exc:
                outcomes.append(exc)

    worker = threading.Thread(target=execute, daemon=True)
    worker.start()
    try:
        assert reached_server.wait(5), outcomes
        assert entered_read.wait(2)
        request_cancel(sid)
        assert finished.wait(1), 'workflow still waited for model output before cancelling'
        assert len(outcomes) == 1 and isinstance(outcomes[0], UserCancelledError), outcomes
        assert len(requests) == 1, 'cancelled model requests must not retry'
        release.set()
        worker.join(5)
        assert not worker.is_alive()
        assert len(outcomes) == 1, outcomes
        assert results == ['Ready']
        assert len(requests) == 2
        assert all('call_control' not in payload for payload in requests)
    finally:
        release.set()
        worker.join(6)
        server.shutdown()
        server.server_close()
        serving.join(2)
        lazyllm.globals._clear_sid(sid)


def test_run_cancellation_preserves_request_controls_and_original_model(provider):
    from fallback_test_support import succeed
    from lazyllm.module.llms.onlinemodule.base import ModelCallControl
    from lazyllm.module.llms.onlinemodule.base.model_request import _ModelCallDeadlineExceeded
    from lazymind.chat.engine.agent_runtime.model_cancellation import CancellableLLM

    succeed(provider, 'model-b')
    raw = lazyllm.OnlineChatModule(
        source='openai', model='model-b', url=provider.url, api_key='test-key', type='llm', stream=False,
        static_params={'temperature': 0.25, 'max_tokens': 256},
    )
    cancelled = threading.Event()
    controlled = CancellableLLM(raw, cancel_check=lambda _: cancelled.is_set()).share(
        prompt='Keep the supplied system prompt.', format=lambda response: response['content'],
    )
    prepared = []
    assert controlled('hello', call_control=ModelCallControl(
        deadline=time.monotonic() + 5, request_check=prepared.append,
    )) == 'done:model-b'
    assert len(prepared) == 1
    payload = provider.requests[0]
    assert payload['temperature'] == 0.25 and payload['max_tokens'] == 256
    assert 'Keep the supplied system prompt.' in payload['messages'][0]['content']
    with pytest.raises(_ModelCallDeadlineExceeded):
        controlled('expired', call_control=ModelCallControl(deadline=time.monotonic() - 1))
    with pytest.raises(asyncio.CancelledError):
        controlled('caller cancelled', call_control=ModelCallControl(cancel_check=lambda: True))
    cancelled.set()
    with pytest.raises(UserCancelledError):
        controlled('run cancelled')
    assert len(provider.requests) == 1
    assert raw('independent request') == 'done:model-b'
    assert len(provider.requests) == 2


@pytest.mark.parametrize('dynamic', [False, True])
@pytest.mark.parametrize('wrapped', [False, True])
@pytest.mark.parametrize('first_fails', [False, True])
def test_model_keeps_agent_usage_attribution(provider, monkeypatch, dynamic, wrapped, first_fails):
    from lazyllm.module.llms.onlinemodule.base import ModelCallError
    from lazymind.chat.engine.agent_runtime.model_cancellation import CancellableLLM

    monkeypatch.setattr('lazymind.model_config.get_dynamic_role_slot_map', lambda: {'llm': 'chat'})
    provider.script['model-b'] = [{
        'message': {'role': 'assistant', 'content': 'done'},
        'usage': {'prompt_tokens': 11, 'completion_tokens': 7},
    }]
    provider.script['model-b'].insert(0, {
        **provider.script['model-b'][0], 'finish': 'length' if first_fails else 'stop',
    })
    with lazyllm.globals._bind_sid(uuid.uuid4().hex), lazyllm.locals._scope():
        inject_model_config(provider.config('model-b'))
        raw = (lazyllm.AutoModel(source='dynamic', type='llm', name='llm', dynamic_auth=True)
               if dynamic else lazyllm.OnlineChatModule(
                   source='openai', model='model-b', url=provider.url, api_key='test-key', type='llm'))
        controlled = CancellableLLM(raw, cancel_check=lambda _: False) if wrapped else raw
        first = controlled.share(stream=True).used_by('first-agent')
        second = controlled.share(stream=True).used_by('second-agent')
        if first_fails:
            with pytest.raises(ModelCallError):
                first('first round')
        else:
            first('first round')
        second('other agent')
        first('second round')
        usage = lazyllm.globals['usage']
        assert usage['first-agent']['prompt_tokens'] == 22
        assert usage['first-agent']['completion_tokens'] == 14
        assert usage['second-agent']['prompt_tokens'] == 11
        assert usage['second-agent']['completion_tokens'] == 7
        assert raw._used_by_moduleid is None


@pytest.mark.parametrize('dynamic', [False, True])
def test_cancellable_model_interrupts_for_guidance_and_can_continue(provider, monkeypatch, dynamic):
    from fallback_test_support import sse_frame
    from lazymind.chat.engine.agent_runtime.model_cancellation import CancellableLLM
    from lazymind.chat.engine.agent_runtime.steering import (
        SteeringInterruptedError, install_stream_interrupt_hook, steering_registry,
    )

    monkeypatch.setattr('lazymind.model_config.get_dynamic_role_slot_map', lambda: {'llm': 'chat'})
    provider.script['model-b'] = [{
        'message': {'role': 'assistant', 'content': 'partial'},
        'frames': [sse_frame({'content': 'partial'}), sse_frame({'content': 'obsolete'}, finish='stop')],
    }, {'message': {'role': 'assistant', 'content': 'continued'}}]
    session, run = uuid.uuid4().hex, uuid.uuid4().hex
    events = []

    def sink(event):
        events.append(event)
        if event.get('tag') == 'text' and event.get('delta') == 'partial':
            assert steering_registry.accept(run, 'conversation', 'guide', 'Change direction') == (True, '')

    with lazyllm.globals._bind_sid(session), lazyllm.locals._scope():
        inject_model_config(provider.config('model-b'))
        raw = (lazyllm.AutoModel(source='dynamic', type='llm', name='llm', dynamic_auth=True)
               if dynamic else lazyllm.OnlineChatModule(
                   source='openai', model='model-b', url=provider.url, api_key='test-key', type='llm'))
        controlled = CancellableLLM(raw, cancel_check=lambda _: False)
        steering_registry.register(run, 'conversation', session)
        try:
            assert install_stream_interrupt_hook(controlled)
            # FunctionCall shares the model after the hook has been installed.
            view = controlled.share(stream={'_stream_sink': sink})
            with pytest.raises(SteeringInterruptedError):
                view('start')
            assert ''.join(event.get('delta', '') for event in events if event.get('tag') == 'text') == 'partial'
            assert len(provider.requests) == 1
            assert steering_registry.drain_or_close(run)[0].content == 'Change direction'
            assert view('Change direction') == 'continued'
            assert len(provider.requests) == 2
        finally:
            steering_registry.unregister(run)

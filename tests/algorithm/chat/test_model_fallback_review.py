import time

from lazymind.config import config
import pytest

from fallback_test_support import candidate, fail, succeed, wrapper
from lazymind.chat.engine.agent_runtime.budget import build_context_budget


@pytest.mark.parametrize('previous_window', [32768, 131072])
def test_unknown_candidate_has_independent_default_budget(provider, previous_window):
    current = candidate(provider, 'model-a', window=previous_window)
    unknown = candidate(provider, 'model-b', window=None)
    fail(provider, 'model-a')
    succeed(provider, 'model-b')
    model = wrapper(current, [unknown])
    assert model('continue') == 'done:model-b'
    assert model.state.current.max_input_tokens is None
    assert model.state.budget == build_context_budget(None)


def test_fallback_sorts_effective_budgets_before_applying_candidate_limit(provider):
    from lazyllm.module.llms.onlinemodule.base import ModelCallError

    current = candidate(provider, 'model-a')
    alternatives = [candidate(provider, name, window=window) for name, window in (
        ('same', 32768), ('small', 16384), ('unknown', None), ('largest', 262144), ('medium', 65536),
    )]
    for item in [current, *alternatives]:
        fail(provider, item.model_id)
    model = wrapper(current, alternatives)
    with pytest.raises(ModelCallError):
        model('continue')
    assert [item['model'] for item in provider.requests] == ['model-a', 'largest', 'unknown', 'medium']
    assert alternatives[2].max_input_tokens is None


def test_unavailable_and_text_only_candidates_do_not_consume_the_limit(provider):
    a = candidate(provider, 'model-a')
    a.llm_config['llm']['vision'] = True
    alternatives = [candidate(provider, name, window=262144) for name in ('failed', 'bad-key', 'text-only')]
    visual = candidate(provider, 'visual-unknown', window=None)
    visual.llm_config['llm']['vision'] = True
    fail(provider, 'model-a')
    succeed(provider, 'visual-unknown')
    model = wrapper(a, [*alternatives, visual])
    model.state.failed_candidates.add(alternatives[0].candidate_id)
    model.state.failed_credentials.add((alternatives[1].provider_group_id, alternatives[1].credential_revision))
    with config.temp('model_fallback_max_candidates', 2):
        assert model('continue', llm_chat_history=[{'role': 'user', 'content': [
            {'type': 'image_url', 'image_url': {'url': 'https://images.example/fixture.png'}},
        ]}]) == 'done:visual-unknown'
    assert [item['model'] for item in provider.requests] == ['model-a', 'visual-unknown']


@pytest.mark.parametrize('text_only_backups', [0, 3])
def test_image_request_is_not_interrupted_by_the_soft_budget(provider, text_only_backups):
    a = candidate(provider, 'model-a')
    a.llm_config['llm']['vision'] = True
    succeed(provider, 'model-a')
    provider.on_request = lambda _: time.sleep(0.3)
    model = wrapper(a, [candidate(provider, f'text-{index}') for index in range(text_only_backups)])
    with config.temp('model_fallback_timeout_seconds', 0.1):
        assert model('continue', llm_chat_history=[{'role': 'user', 'content': [
            {'type': 'image_url', 'image_url': {'url': 'https://images.example/fixture.png'}},
        ]}]) == 'done:model-a'
    assert [item['model'] for item in provider.requests] == ['model-a']


def test_slow_initial_model_leaves_time_for_a_healthy_fallback(provider):
    import threading

    release = threading.Event()
    succeed(provider, 'model-a')
    succeed(provider, 'model-b')

    def slow_initial(request):
        if request['model'] == 'model-a':
            release.wait(2)

    provider.on_request = slow_initial
    model = wrapper(candidate(provider, 'model-a'), [candidate(provider, 'model-b')])
    events = []
    try:
        with config.temp('model_fallback_timeout_seconds', 0.6), \
                config.temp('model_fallback_read_timeout_seconds', 0.1):
            started = time.monotonic()
            assert model('continue', stream={'_stream_sink': events.append}) == 'done:model-b'
            assert time.monotonic() - started < 0.8
    finally:
        release.set()
    assert [item['model'] for item in provider.requests] == ['model-a', 'model-b']
    switches = [item['runtime_event']['data'] for item in events
                if (item.get('runtime_event') or {}).get('type') == 'model_switched']
    assert switches[0]['reason_code'] == 'request_timeout'
    assert model.state.current.model_id == 'model-b'


def test_sse_cancel_closes_the_socket_and_never_starts_fallback(provider):
    import json
    import threading
    from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
    from lazymind.chat.engine.agent_runtime.cancellation import UserCancelledError

    cancelled, disconnected = threading.Event(), threading.Event()
    requests = []

    class Handler(BaseHTTPRequestHandler):
        def log_message(self, *_args):
            pass

        def do_POST(self):
            requests.append(json.loads(self.rfile.read(int(self.headers['Content-Length']))))
            self.send_response(200)
            self.send_header('Content-Type', 'text/event-stream')
            self.end_headers()
            frame = {'choices': [{'index': 0, 'delta': {'content': 'uncommitted'}, 'finish_reason': None}]}
            self.wfile.write(('data: ' + json.dumps(frame) + '\n\n:' + 'x' * 2048 + '\n\n').encode())
            self.wfile.flush()
            self.connection.settimeout(2)
            try:
                closed = self.connection.recv(1) == b''
            except ConnectionResetError:
                closed = True
            if closed:
                disconnected.set()

    server = ThreadingHTTPServer(('127.0.0.1', 0), Handler)
    thread = threading.Thread(target=lambda: server.serve_forever(poll_interval=0.01), daemon=True)
    thread.start()
    a, b = [candidate(provider, name) for name in ('model-a', 'model-b')]
    a.llm_config['llm']['base_url'] = f'http://127.0.0.1:{server.server_port}/v1/'
    model = wrapper(a, [b])
    model._cancel_check = lambda _: cancelled.is_set()
    events = []

    def sink(event):
        events.append(event)
        if event.get('tag') == 'text':
            cancelled.set()

    try:
        with pytest.raises(UserCancelledError):
            model('continue', stream={'_stream_sink': sink})
        assert disconnected.wait(1)
    finally:
        server.shutdown()
        server.server_close()
        thread.join(2)
    assert [item['model'] for item in requests] == ['model-a']
    assert provider.requests == []
    assert [item['model_output']['phase'] for item in events
            if item.get('tag') == 'model_output'] == ['begin', 'rollback']


@pytest.mark.parametrize('limit', [1, 4])
def test_candidate_limit_includes_initial_model(provider, limit):
    candidates = [candidate(provider, 'model-' + str(i)) for i in range(6)]
    for item in candidates:
        fail(provider, item.model_id)
    model = wrapper(candidates[0], candidates[1:])
    with config.temp('model_fallback_max_candidates', limit):
        with pytest.raises(Exception) as failure:
            model('continue')
    assert [request['model'] for request in provider.requests] == [item.model_id for item in candidates[:limit]]
    assert failure.value.fallback_code == 'model_fallback_exhausted'


def test_soft_budget_stops_new_attempts_but_allows_the_last_attempt_to_finish(provider, monkeypatch):
    from types import SimpleNamespace
    from lazymind.chat.engine.agent_runtime import model_fallback

    candidates = [candidate(provider, 'model-' + str(i)) for i in range(5)]
    now = [0.0]
    monkeypatch.setattr(model_fallback, 'time', SimpleNamespace(monotonic=lambda: now[0]))

    def slow_failure(request):
        now[0] += 0.08
        return {'error': 'model_not_found', 'status': 404}

    provider.on_request = slow_failure
    model = wrapper(candidates[0], candidates[1:])
    with config.temp('model_fallback_timeout_seconds', 0.12), config.temp('model_fallback_max_candidates', 5):
        with pytest.raises(Exception) as failure:
            model('continue')
    assert [request['model'] for request in provider.requests] == ['model-0', 'model-1']
    assert failure.value.fallback_code == 'model_fallback_exhausted'


def test_ordinary_summary_does_not_consume_primary_soft_budget_or_candidates(provider):
    model = wrapper(candidate(provider, 'model-a'), [candidate(provider, 'model-b')])
    succeed(provider, 'model-a')
    provider.on_request = lambda _: time.sleep(0.08)
    with config.temp('model_fallback_timeout_seconds', 0.02):
        model.auxiliary_call('summary system', 'summary input')
        assert model('continue') == 'done:model-a'
    assert [item['model'] for item in provider.requests] == ['model-a', 'model-a']
    assert model.state.failed_candidates == set()


def test_balance_failure_switches_immediately_without_sdk_retry(provider):
    a = candidate(provider, 'model-a', source='deepseek')
    b = candidate(provider, 'model-b')
    provider.script['model-a'] = [{'error': 'balance_exhausted', 'status': 402}]
    succeed(provider, 'model-b')
    events = []
    assert wrapper(a, [b])('continue', stream={'_stream_sink': events.append}) == 'done:model-b'
    assert [request['model'] for request in provider.requests] == ['model-a', 'model-b']
    assert not any((event.get('runtime_event') or {}).get('type') == 'model_retry_scheduled' for event in events)
    changed = [event['runtime_event']['data'] for event in events
               if (event.get('runtime_event') or {}).get('type') == 'model_switched']
    assert changed[0]['reason_code'] == 'balance_exhausted'


@pytest.mark.parametrize('overrides', [False, True])
def test_initial_backup_and_auxiliary_calls_receive_the_same_timeout_policy(provider, monkeypatch, overrides):
    import requests

    a, b = candidate(provider, 'model-a'), candidate(provider, 'model-b')
    if overrides:
        a.llm_config['llm']['timeout'] = [0.3, 0.7]
        b.llm_config['llm']['timeout'] = [0.4, 0.8]
    fail(provider, 'model-a')
    succeed(provider, 'model-b')
    model = wrapper(a, [b])
    post = requests.post
    observed = []

    def record(url, **kwargs):
        observed.append((kwargs['json']['model'], kwargs['timeout']))
        return post(url, **kwargs)

    monkeypatch.setattr(requests, 'post', record)
    assert model('continue') == 'done:model-b'
    assert model.auxiliary_call('', 'final summary') == 'done:model-b'
    initial_timeout, backup_timeout = ((0.3, 0.7), (0.4, 0.8)) if overrides else ((3.0, 10.0), (3.0, 10.0))
    assert observed == [('model-a', initial_timeout), ('model-b', backup_timeout), ('model-b', backup_timeout)]


def test_continuous_generation_finishes_after_soft_budget(provider):
    from fallback_test_support import sse_frame

    a, b = candidate(provider, 'model-a'), candidate(provider, 'model-b')
    provider.script['model-a'] = [{'message': {'role': 'assistant', 'content': ''}, 'frames': [
        sse_frame({'role': 'assistant', 'content': 'one '}),
        sse_frame({'content': 'two '}), sse_frame({'content': 'three'}, finish='stop'),
    ], 'chunk_delay': 0.02}]
    succeed(provider, 'model-b')
    events = []
    with config.temp('model_fallback_timeout_seconds', 0.02), \
            config.temp('model_fallback_read_timeout_seconds', 0.1):
        assert wrapper(a, [b])('continue', stream={'_stream_sink': events.append}) == 'one two three'
    assert [request['model'] for request in provider.requests] == ['model-a']
    phases = [event['model_output']['phase'] for event in events if event.get('tag') == 'model_output']
    assert phases == ['begin', 'commit']


def test_expired_budget_after_request_validation_does_not_start_http(provider, monkeypatch):
    from types import SimpleNamespace
    from lazymind.chat.engine.agent_runtime import model_fallback

    now = [0.0]
    estimate = model_fallback.request_tokens
    monkeypatch.setattr(model_fallback, 'time', SimpleNamespace(monotonic=lambda: now[0]))

    def slow_estimate(request):
        result = estimate(request)
        now[0] += 31
        return result

    monkeypatch.setattr(model_fallback, 'request_tokens', slow_estimate)
    model = wrapper(candidate(provider, 'model-a'), [candidate(provider, 'model-b')])
    with pytest.raises(Exception) as failure:
        model('continue')
    assert failure.value.fallback_code == 'model_fallback_exhausted'
    assert provider.requests == []
    assert model.state.failed_candidates == set()


def test_cancel_during_blocked_read_does_not_start_backup(provider):
    import threading
    from lazymind.chat.engine.agent_runtime.cancellation import UserCancelledError

    cancelled, release = threading.Event(), threading.Event()

    def slow_initial(request):
        cancelled.set()
        release.wait(2)

    provider.on_request = slow_initial
    model = wrapper(candidate(provider, 'model-a'), [candidate(provider, 'model-b')])
    model._cancel_check = lambda _: cancelled.is_set()
    try:
        with config.temp('model_fallback_read_timeout_seconds', 0.1), pytest.raises(UserCancelledError):
            model('continue')
    finally:
        release.set()
    assert [request['model'] for request in provider.requests] == ['model-a']
    assert model.state.failed_candidates == set()


def test_local_cancellation_check_failure_does_not_try_another_model(provider):
    from lazyllm.module.module import ModuleExecutionError

    model = wrapper(candidate(provider, 'model-a'), [candidate(provider, 'model-b')])
    fail(provider, 'model-a')
    succeed(provider, 'model-b')

    def broken_check(_):
        if provider.requests:
            raise RuntimeError('fixture control failure')
        return False

    model._cancel_check = broken_check
    with pytest.raises(ModuleExecutionError, match='cancellation check failed') as failure:
        model('continue')
    assert failure.value._model_fallback_local_failure is True
    assert [request['model'] for request in provider.requests] == ['model-a']


@pytest.mark.parametrize('cancelled', [False, True])
def test_final_summary_after_switch_uses_confirmed_model_and_keeps_candidates(provider, tmp_path, cancelled):
    import threading
    import lazyllm
    from fallback_test_support import EFFECT_RECEIPTS, record_fallback_effect
    from lazymind.chat.engine.agent_runtime import (
        AgentExecutionOptions, AgentExecutor, AgentRole, AgentRunPlan, PromptBuilder,
    )
    from lazymind.chat.engine.agent_runtime.cancellation import UserCancelledError
    from lazymind.chat.engine.tools.workspace_context import WorkspaceContext

    a, b, c = [candidate(provider, name) for name in ('model-a', 'model-b', 'model-c')]
    fail(provider, 'model-a')
    provider.script['model-b'] = [{
        'message': {'role': 'assistant', 'content': '', 'tool_calls': [{
            'id': f'step-{index}', 'index': 0, 'type': 'function',
            'function': {'name': 'record_fallback_effect', 'arguments': f'{{"value":"step-{index}"}}'},
        }]}, 'finish': 'tool_calls',
    } for index in range(2)] + [
        {'message': {'role': 'assistant', 'content': 'final summary by B'}},
    ]
    stop = threading.Event()

    def observe(request):
        if cancelled and len(provider.requests) == 4:
            stop.set()

    provider.on_request = observe
    options = AgentExecutionOptions(
        llm_config=a.llm_config, model_candidates=(a, b, c), current_model_candidate_id=a.candidate_id,
        max_retries=1, workspace=str(tmp_path), enable_builtin_tools=False, skills=False,
        workspace_permission=WorkspaceContext(local_runtime=False),
        extra_stop_condition=lambda _: stop.is_set(),
    )
    plan = AgentRunPlan(
        role=AgentRole.CHAT, prompt=PromptBuilder.for_role(AgentRole.CHAT).input('Do two steps.', source='user').build(),
        tools=[record_fallback_effect], execution_options=options)
    raw = lazyllm.OnlineChatModule(source='openai', model=a.model_id, url=provider.url, api_key='fixture-key')
    with config.temp('context_compression_enabled', False), config.temp('agentic_workspace', str(tmp_path)), \
            config.temp('agentic_tool_limit_wait_timeout', 0.0):
        agent = AgentExecutor().create_agent(raw, plan)
        if cancelled:
            with pytest.raises(UserCancelledError):
                agent(plan.prompt.current_input)
        else:
            assert agent(plan.prompt.current_input) == 'final summary by B'
    assert [request['model'] for request in provider.requests] == ['model-a', 'model-b', 'model-b', 'model-b']
    assert EFFECT_RECEIPTS == ['step-0', 'step-1']
    assert not provider.requests[-1].get('tools')
    assert 'effect-receipt' in str(provider.requests[-1]['messages'])
    assert agent._model_fallback_state.failed_candidates == {a.candidate_id}
    assert agent._model_fallback_state.current.candidate_id == b.candidate_id

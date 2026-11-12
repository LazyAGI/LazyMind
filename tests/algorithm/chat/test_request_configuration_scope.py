from concurrent.futures import ThreadPoolExecutor
from threading import Barrier
from uuid import UUID, uuid4

import lazyllm
import pytest

from lazymind.chat import request_scope
from lazymind.chat.request_scope import request_configuration_scope


def _model_config(name):
    return {'llm': {'source': 'openai', 'model': f'model-{name}', 'api_key': f'model-key-{name}'}}


def _request_credentials():
    config = lazyllm.globals['config']
    model = config.get('dynamic_model_configs', {}).get('llm', {}).get('chat', {})
    model_key = config.get('openai_api_key', {}).get('llm')
    tool_key = config.get('dynamic_tool_auth', {}).get('tavily')
    return model.get('model'), model_key, tool_key


def test_failed_request_clears_its_configuration_and_locals():
    with pytest.raises(RuntimeError, match='request failed'):
        with request_configuration_scope(
            model_config=_model_config('a'), tool_config={'tavily': 'tool-key-a'},
        ) as first_sid:
            assert _request_credentials() == ('model-a', 'model-key-a', 'tool-key-a')
            lazyllm.locals.curr_key['tavily'] = 'tool-key-a'
            raise RuntimeError('request failed')

    with request_configuration_scope(model_config={}, tool_config={}) as second_sid:
        assert second_sid != first_sid
        assert _request_credentials() == (None, None, None)
        assert lazyllm.locals.curr_key == {}

    with lazyllm.globals._bind_sid(first_sid), lazyllm.locals._bind_sid(first_sid):
        try:
            assert _request_credentials() == (None, None, None)
            assert lazyllm.locals.curr_key == {}
        finally:
            lazyllm.locals._clear_sid(first_sid)
            lazyllm.globals._clear_sid(first_sid)


@pytest.mark.parametrize('fail', [False, True], ids=['normal-exit', 'exception'])
def test_nested_request_restores_outer_and_caller_scopes(fail):
    caller_global_sid, caller_local_sid = uuid4().hex, uuid4().hex
    with lazyllm.globals._bind_sid(caller_global_sid), lazyllm.locals._bind_sid(caller_local_sid):
        try:
            lazyllm.globals['sentinel'] = 'caller-global'
            lazyllm.locals['sentinel'] = 'caller-local'

            def run_request():
                with request_configuration_scope(
                    model_config=_model_config('a'), tool_config={'tavily': 'tool-key-a'},
                ) as outer_sid:
                    lazyllm.globals['sentinel'] = 'outer-global'
                    lazyllm.locals['sentinel'] = 'outer-local'
                    with request_configuration_scope(
                        model_config=_model_config('b'), tool_config={'tavily': 'tool-key-b'},
                    ) as inner_sid:
                        assert lazyllm.globals._sid == lazyllm.locals._sid == inner_sid
                        assert _request_credentials() == ('model-b', 'model-key-b', 'tool-key-b')
                        assert 'sentinel' not in lazyllm.globals
                        assert 'sentinel' not in lazyllm.locals

                    assert lazyllm.globals._sid == lazyllm.locals._sid == outer_sid
                    assert _request_credentials() == ('model-a', 'model-key-a', 'tool-key-a')
                    assert lazyllm.globals['sentinel'] == 'outer-global'
                    assert lazyllm.locals['sentinel'] == 'outer-local'
                    if fail:
                        raise RuntimeError('outer request failed')

            if fail:
                with pytest.raises(RuntimeError, match='outer request failed'):
                    run_request()
            else:
                run_request()

            assert lazyllm.globals._sid == caller_global_sid
            assert lazyllm.locals._sid == caller_local_sid
            assert lazyllm.globals['sentinel'] == 'caller-global'
            assert lazyllm.locals['sentinel'] == 'caller-local'
            assert _request_credentials() == (None, None, None)
        finally:
            lazyllm.locals._clear_sid(caller_local_sid)
            lazyllm.globals._clear_sid(caller_global_sid)


def test_concurrent_requests_only_observe_their_own_configuration():
    barrier = Barrier(2, timeout=5)

    def observe(name):
        with request_configuration_scope(
            model_config=_model_config(name), tool_config={'tavily': f'tool-key-{name}'},
        ) as sid:
            lazyllm.locals.curr_key['tavily'] = f'tool-key-{name}'
            barrier.wait()
            credentials = _request_credentials()
            local_key = lazyllm.locals.curr_key['tavily']
            assert lazyllm.globals._sid == lazyllm.locals._sid == sid
            return sid, credentials, local_key

    with ThreadPoolExecutor(max_workers=2) as executor:
        observations = list(executor.map(observe, ['a', 'b']))

    assert observations[0][0] != observations[1][0]
    for name, (sid, credentials, local_key) in zip(['a', 'b'], observations):
        assert sid.startswith('lazymind-request-')
        suffix = sid.removeprefix('lazymind-request-')
        parsed = UUID(hex=suffix)
        assert parsed.version == 4
        assert parsed.hex == suffix
        assert credentials == (f'model-{name}', f'model-key-{name}', f'tool-key-{name}')
        assert local_key == f'tool-key-{name}'


def test_failed_injection_clears_partially_initialized_scope(monkeypatch):
    caller_sids = lazyllm.globals._sid, lazyllm.locals._sid
    failed_sid = None

    def fail_injection(_):
        nonlocal failed_sid
        failed_sid = lazyllm.globals._sid
        assert lazyllm.locals._sid == failed_sid
        assert _request_credentials() == ('model-a', 'model-key-a', None)
        lazyllm.locals.curr_key['tavily'] = 'temporary-key'
        raise RuntimeError('injection failed')

    monkeypatch.setattr(request_scope, 'inject_tool_config', fail_injection)
    with pytest.raises(RuntimeError, match='injection failed'):
        with request_configuration_scope(model_config=_model_config('a'), tool_config={'tavily': 'tool-key-a'}):
            pytest.fail('A failed injection must not enter the request body')

    assert (lazyllm.globals._sid, lazyllm.locals._sid) == caller_sids
    assert failed_sid is not None
    with lazyllm.globals._bind_sid(failed_sid), lazyllm.locals._bind_sid(failed_sid):
        try:
            assert _request_credentials() == (None, None, None)
            assert lazyllm.locals.curr_key == {}
        finally:
            lazyllm.locals._clear_sid(failed_sid)
            lazyllm.globals._clear_sid(failed_sid)

import pytest
from unittest.mock import MagicMock

from lazyllm.tools.agent.toolError import exception_failure
from lazymind.chat.engine.tools.infra import core_api_client
from lazymind.chat.engine.tools.infra.core_api_client import (
    CoreAPIError,
    _raise_core_api_error,
)


@pytest.mark.parametrize('status_code', (401, 403, 408, 429, 502, 503, 504, 500))
def test_core_api_http_status_is_preserved_for_tool_failure_normalization(
    status_code,
):
    with pytest.raises(CoreAPIError) as exc_info:
        _raise_core_api_error(
            'GET', 'http://core.test/resource', status_code, {'message': 'request failed'},
        )

    failure = exception_failure('core_tool', exc_info.value)

    assert exc_info.value.status_code == status_code
    assert f'HTTP {status_code}' in failure['value']
    assert set(failure) == {'ok', 'value'}


@pytest.mark.parametrize('method', ('POST', 'PATCH'))
@pytest.mark.parametrize('status_code', (200, 409))
def test_core_api_writes_preserve_transport_contract(monkeypatch, method, status_code):
    monkeypatch.setattr(core_api_client, '_cfg', {
        'core_api_url': 'http://core.test/api/v1/', 'core_api_timeout': 5,
    })
    monkeypatch.setattr(core_api_client, '_current_user_headers', lambda: {
        'X-User-Id': 'current-user', 'X-LazyMind-Internal-Token': 'test-token',
    })
    session = MagicMock()
    session.__enter__.return_value = session
    monkeypatch.setattr(core_api_client.requests.sessions, 'Session', lambda: session)
    response = getattr(session, method.lower()).return_value
    response.ok = status_code == 200
    response.status_code = status_code
    body = {'code': 0, 'data': {'id': 'env-test'}} if response.ok else {'message': 'conflict'}
    response.json.return_value = body
    write = getattr(core_api_client, method.lower() + '_core_api')
    payload = {'name': 'TEST_TOKEN', 'value': 'private-test-value'}
    if response.ok:
        result = write('/user/env-vars', payload, user_id='explicit-user')
        assert result == {'persisted': 'core_api', 'url': 'http://core.test/api/v1/user/env-vars', 'response': body}
    else:
        with pytest.raises(CoreAPIError) as exc_info:
            write('/user/env-vars', payload, user_id='explicit-user')
        assert exc_info.value.method == method
        assert exc_info.value.status_code == 409
    assert session.trust_env is False
    getattr(session, method.lower()).assert_called_once_with(
        'http://core.test/api/v1/user/env-vars', json=payload, timeout=5,
        headers={'X-User-Id': 'explicit-user', 'X-LazyMind-Internal-Token': 'test-token'},
    )

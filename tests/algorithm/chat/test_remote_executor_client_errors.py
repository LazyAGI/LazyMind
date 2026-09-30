import httpx
import pytest

from lazymind.chat.workflow.client import RemoteExecutorClient


def test_attempt_context_error_preserves_core_cause_and_http_status():
    response = httpx.Response(503, request=httpx.Request('GET', 'http://core/context'), json={
        'ok': False, 'error': {'code': 'ATTEMPT_CONTEXT_FAILED',
                              'message': 'single-cardinality material "mode" has multiple distinct input bindings'},
    })
    with pytest.raises(httpx.HTTPStatusError) as caught:
        RemoteExecutorClient.data(response)
    assert 'ATTEMPT_CONTEXT_FAILED' in str(caught.value)
    assert 'multiple distinct input bindings' in str(caught.value)
    assert caught.value.response.status_code == 503


@pytest.mark.parametrize('body', [b'upstream unavailable', b'[]', b'{"error":"unavailable"}'])
def test_noncontract_error_keeps_http_failure(body):
    response = httpx.Response(503, request=httpx.Request('GET', 'http://core/context'), content=body)
    with pytest.raises(httpx.HTTPStatusError) as caught:
        RemoteExecutorClient.data(response)
    assert caught.value.response is response


def test_successful_context_response_keeps_data():
    response = httpx.Response(200, request=httpx.Request('GET', 'http://core/context'), json={
        'ok': True, 'data': {'step_id': 'render'},
    })
    assert RemoteExecutorClient.data(response) == {'step_id': 'render'}

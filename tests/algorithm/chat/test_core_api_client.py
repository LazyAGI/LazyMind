import pytest

from lazyllm.tools.agent.toolError import exception_failure
from lazymind.chat.engine.tools.infra.core_api_client import (
    CoreAPIError,
    _raise_core_api_error,
)


@pytest.mark.parametrize(('status_code', 'category', 'recovery_action'), (
    (401, 'PERMISSION_ERROR', 'request_authorization'),
    (403, 'PERMISSION_ERROR', 'request_authorization'),
    (408, 'TRANSIENT_ERROR', 'retry_later'),
    (429, 'TRANSIENT_ERROR', 'retry_later'),
    (502, 'TRANSIENT_ERROR', 'retry_later'),
    (503, 'TRANSIENT_ERROR', 'retry_later'),
    (504, 'TRANSIENT_ERROR', 'retry_later'),
    (500, 'DOMAIN_FAILURE', 'change_plan'),
))
def test_core_api_http_status_is_preserved_for_tool_failure_normalization(
    status_code, category, recovery_action,
):
    with pytest.raises(CoreAPIError) as exc_info:
        _raise_core_api_error(
            'GET', 'http://core.test/resource', status_code, {'message': 'request failed'},
        )

    failure = exception_failure('core_tool', exc_info.value)

    assert exc_info.value.status_code == status_code
    assert failure['error']['category'] == category
    assert failure['error']['recovery_action'] == recovery_action
    if category in {'PERMISSION_ERROR', 'TRANSIENT_ERROR'}:
        assert failure['error']['details']['status_code'] == status_code

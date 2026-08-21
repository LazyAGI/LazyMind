import pytest

from lazyllm.tools.agent.toolError import exception_failure
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

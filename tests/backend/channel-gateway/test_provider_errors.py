import copy
import pickle

import pytest

from channel_gateway.common.errors import ProviderRejectedError
from channel_gateway.wechat.domain import WeChatRejectedError


@pytest.mark.parametrize('error_type', [ProviderRejectedError, WeChatRejectedError])
@pytest.mark.parametrize('retryable', [False, True])
def test_provider_error_preserves_constructor_arguments(error_type, retryable):
    error = error_type('rejected', retryable=retryable)
    restored = error_type(*error.args)
    for candidate in (restored, copy.copy(error), pickle.loads(pickle.dumps(error))):
        assert type(candidate) is error_type
        assert str(candidate) == 'rejected'
        assert candidate.retryable is retryable

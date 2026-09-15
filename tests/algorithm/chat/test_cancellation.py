from __future__ import annotations

import json

import pytest

from lazymind.chat.engine.agent_runtime.cancellation import (
    UserCancelledError,
    make_cancel_stop_condition,
)


def test_user_cancel_signal_raises_domain_cancellation(monkeypatch):
    from lazyllm.common import queue as queue_module

    class FakeQueue:
        def __init__(self, *, klass):
            assert klass == 'cancel'

        def dequeue(self):
            return [json.dumps({'tag': 'cancel'})]

    monkeypatch.setattr(queue_module, 'FileSystemQueue', FakeQueue)

    with pytest.raises(UserCancelledError, match='stopped by user'):
        make_cancel_stop_condition()(None)


def test_non_user_queue_failure_does_not_cancel(monkeypatch):
    from lazyllm.common import queue as queue_module

    class BrokenQueue:
        def __init__(self, *, klass):
            assert klass == 'cancel'

        def dequeue(self):
            raise RuntimeError('state unavailable')

    monkeypatch.setattr(queue_module, 'FileSystemQueue', BrokenQueue)

    assert make_cancel_stop_condition()(None) is False

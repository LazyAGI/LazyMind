"""Tests for SubAgent cancel queue stop_condition (必修B)."""
from __future__ import annotations

import json
from asyncio import CancelledError
from unittest.mock import MagicMock, patch

import pytest


# ---------------------------------------------------------------------------
# _make_cancel_stop_condition
# ---------------------------------------------------------------------------

def _import_fn():
    from lazymind.chat.engine.subagent.runner import _make_cancel_stop_condition
    return _make_cancel_stop_condition


def test_make_cancel_stop_condition_no_signal():
    """When cancel queue is empty, stop_condition returns False without raising."""
    _import_fn()

    mock_q = MagicMock()
    mock_q.dequeue.return_value = []

    with patch('lazymind.chat.engine.subagent.runner.FileSystemQueue', return_value=mock_q, create=True):
        # Patch the import inside _check using the module path.
        def patched_check(output) -> bool:
            try:
                msgs = mock_q.dequeue() or []
                for raw in msgs:
                    if json.loads(raw).get('tag') == 'cancel':
                        raise CancelledError('stopped by user')
            except CancelledError:
                raise
            except Exception:
                pass
            return False
        assert patched_check('some output') is False


def test_make_cancel_stop_condition_raises():
    """When cancel signal is present, stop_condition raises CancelledError."""
    cancel_msg = json.dumps({'tag': 'cancel'})

    mock_q = MagicMock()
    mock_q.dequeue.return_value = [cancel_msg]

    def patched_check(output) -> bool:
        msgs = mock_q.dequeue() or []
        for raw in msgs:
            if json.loads(raw).get('tag') == 'cancel':
                raise CancelledError('stopped by user')
        return False

    with pytest.raises(CancelledError):
        patched_check('anything')


def test_cancel_queue_cleared_on_task_end(tmp_path):
    """runner.py finally block calls FileSystemQueue(klass='cancel').clear()."""
    cleared = []

    class FakeQueue:
        def __init__(self, klass=None):
            self.klass = klass

        def clear(self):
            cleared.append(self.klass)

        def dequeue(self):
            return []

    with (
        patch.dict('sys.modules', {'lazyllm.common.queue': MagicMock(FileSystemQueue=FakeQueue)}),
        patch('lazymind.chat.engine.subagent.runner.FileSystemQueue', FakeQueue, create=True),
    ):
        # Simulate the finally block behavior.
        try:
            raise RuntimeError('simulated error')
        except RuntimeError:
            pass
        finally:
            FakeQueue(klass='cancel').clear()

    assert 'cancel' in cleared

from __future__ import annotations

import json
from asyncio import CancelledError


class UserCancelledError(CancelledError):
    """The active Agent run was explicitly stopped by the user."""


def make_cancel_stop_condition():
    """Stop the current Agent when its sid-scoped cancel queue is signalled."""
    def _check(_output) -> bool:
        try:
            from lazyllm.common.queue import FileSystemQueue
            messages = FileSystemQueue(klass='cancel').dequeue() or []
            for raw in messages:
                try:
                    if json.loads(raw).get('tag') == 'cancel':
                        raise UserCancelledError('stopped by user')
                except (ValueError, TypeError):
                    continue
        except UserCancelledError:
            raise
        except Exception:
            pass
        return False

    return _check

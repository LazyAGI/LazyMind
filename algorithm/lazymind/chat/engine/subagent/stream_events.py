from __future__ import annotations

import asyncio
from collections.abc import AsyncIterator
from typing import Any

DRAFT_STREAM_EVENT_TYPES = frozenset({
    'artifact_stream_start',
    'artifact_stream',
    'artifact_stream_end',
    'artifact_stream_abort',
})


async def merge_agent_and_stream_events(
    agent_events: AsyncIterator[Any],
    stream_events: asyncio.Queue[dict[str, Any]],
) -> AsyncIterator[tuple[str, Any]]:
    """Yield tool-thread stream events while the Agent iterator is still running."""
    iterator = agent_events.__aiter__()
    agent_task: asyncio.Task[Any] | None = asyncio.create_task(iterator.__anext__())
    stream_task: asyncio.Task[Any] | None = asyncio.create_task(stream_events.get())
    agent_error: BaseException | None = None
    try:
        while agent_task is not None:
            wait_for = {agent_task}
            if stream_task is not None:
                wait_for.add(stream_task)
            done, _ = await asyncio.wait(wait_for, return_when=asyncio.FIRST_COMPLETED)

            if stream_task is not None and stream_task in done:
                yield 'stream', stream_task.result()
                stream_task = asyncio.create_task(stream_events.get())

            if agent_task in done:
                try:
                    item = agent_task.result()
                except StopAsyncIteration:
                    agent_task = None
                except BaseException as exc:  # noqa: BLE001 - preserve cancellation and failure.
                    agent_error = exc
                    agent_task = None
                else:
                    yield 'agent', item
                    agent_task = asyncio.create_task(iterator.__anext__())

        # Deliver call_soon_threadsafe callbacks queued immediately before the
        # tool/agent future completed, then propagate the original exception.
        await asyncio.sleep(0)
        if stream_task is not None and stream_task.done():
            yield 'stream', stream_task.result()
            stream_task = None
        while not stream_events.empty():
            yield 'stream', stream_events.get_nowait()
        if agent_error is not None:
            raise agent_error
    finally:
        for pending in (agent_task, stream_task):
            if pending is not None and not pending.done():
                pending.cancel()


__all__ = ['DRAFT_STREAM_EVENT_TYPES', 'merge_agent_and_stream_events']

import asyncio
from threading import Event

import lazyllm
import pytest
from fastapi import HTTPException

from lazymind.common import maintenance
from lazymind.config import config


async def wait_event(event):
    for _ in range(200):
        if event.is_set():
            return
        await asyncio.sleep(0.005)
    raise AssertionError('worker did not reach barrier')


@pytest.mark.asyncio
async def test_capacity_is_held_until_cancelled_thread_exits_and_contexts_are_isolated():
    started = [Event(), Event()]
    release = Event()
    contexts = []

    def work(*, task_id, run_id, user_id):
        context = dict(lazyllm.globals['agentic_config'])
        contexts.append((context, lazyllm.globals._sid))
        started[int(user_id)].set()
        assert release.wait(3)
        maintenance.check_cancelled()
        return user_id

    with config.temp('memory_maintenance_workers', 2):
        tasks = [
            asyncio.create_task(
                maintenance.execute(
                    None, work, timeout=3, task_id=f'memory_review_{i}', run_id=f'capacity-{i}', user_id=str(i)
                )
            )
            for i in range(2)
        ]
        try:
            await wait_event(started[0])
            await wait_event(started[1])
            # The event loop is free while both synchronous workers are blocked.
            await asyncio.wait_for(asyncio.sleep(0.001), 0.1)
            tasks[0].cancel()
            with pytest.raises(asyncio.CancelledError):
                await tasks[0]
            with pytest.raises(HTTPException) as full:
                await maintenance.execute(
                    None, work, timeout=1, task_id='memory_review_3', run_id='capacity-3', user_id='3'
                )
            assert full.value.status_code == 503
            assert full.value.detail['code'] == 'maintenance_busy'
        finally:
            release.set()
            await asyncio.gather(*tasks, return_exceptions=True)
            for _ in range(100):
                if maintenance._active == 0:
                    break
                await asyncio.sleep(0.01)
    assert maintenance._active == 0
    assert {c['user_id'] for c, _ in contexts} == {'0', '1'}
    assert {sid for _, sid in contexts} == {'memory_review_0:capacity-0', 'memory_review_1:capacity-1'}
    assert not maintenance._executions


@pytest.mark.asyncio
async def test_disconnect_sets_stop_signal_before_any_following_remote_operation():
    started, release, refused = Event(), Event(), Event()

    class Request:
        async def is_disconnected(self):
            return True

    def work(**_):
        started.set()
        assert release.wait(3)
        try:
            maintenance.identity_headers()
        except maintenance.MaintenanceCancelled:
            refused.set()
            raise

    task = asyncio.create_task(
        maintenance.execute(
            Request(), work, timeout=3, task_id='preference_organizer_disconnect', run_id='disconnect-run', user_id='u'
        )
    )
    try:
        await wait_event(started)
        with pytest.raises(HTTPException) as disconnected:
            await task
        assert disconnected.value.status_code == 499
        assert maintenance._active == 1
    finally:
        release.set()
        await wait_event(refused)
        for _ in range(100):
            if not maintenance._active:
                break
            await asyncio.sleep(0.01)
    assert not maintenance._active

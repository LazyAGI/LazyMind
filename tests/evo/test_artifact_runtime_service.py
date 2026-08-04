"""Service-level tests for persistent ArtifactRuntime execution."""

import asyncio
import os
from pathlib import Path

import pytest

from evo.artifact_runtime import (
    ArtifactCommit,
    ArtifactDraft,
    ArtifactKey,
    ArtifactRuntime,
    DefinitionError,
    OperationResult,
    one,
    operation,
    scalar,
)


FAILURES_BY_RUN = {}


@operation(
    op_id='test.runtime.normalize',
    inputs={'source': one('source')},
    outputs={'normalized': scalar('normalized')},
    execution='cooperative',
)
async def normalize_operation(ctx, source):
    await ctx.report(
        'normalize',
        'source normalized',
        current=1,
        total=1,
        detail={'executor': 'cooperative'},
    )
    return OperationResult({'normalized': int(source) + 1})


@operation(
    op_id='test.runtime.double',
    inputs={'normalized': one('normalized')},
    outputs={'result': scalar('result')},
    execution='cooperative',
)
async def double_operation(ctx, normalized):
    await ctx.report(
        'double',
        'value doubled',
        current=1,
        total=1,
        detail={'executor': 'cooperative'},
    )
    return OperationResult({'result': int(normalized) * 2})


@operation(
    op_id='test.runtime.isolated_double',
    inputs={'normalized': one('normalized')},
    outputs={'result': scalar('result')},
)
async def isolated_double_operation(ctx, normalized):
    await ctx.report(
        'double',
        'value doubled',
        current=1,
        total=1,
        detail={'executor': 'isolated'},
    )
    return OperationResult({'result': int(normalized) * 2})


@operation(
    op_id='test.runtime.fail_once',
    inputs={'source': one('source')},
    outputs={'result': scalar('result')},
    execution='cooperative',
)
async def fail_once_operation(ctx, source):
    count = FAILURES_BY_RUN.get(ctx.run_id, 0) + 1
    FAILURES_BY_RUN[ctx.run_id] = count
    if count == 1:
        raise RuntimeError('intentional first-attempt failure')
    return OperationResult({'result': source})


@operation(
    op_id='test.runtime.slow_copy',
    inputs={'source': one('source')},
    outputs={'result': scalar('result')},
    execution='cooperative',
)
async def slow_copy_operation(ctx, source):
    await ctx.report('started', detail={'delay': source['delay']})
    await asyncio.sleep(float(source['delay']))
    return OperationResult({'result': source['value']})


def _initial_commit(value):
    key = ArtifactKey.scalar('source')
    return ArtifactCommit(
        'seed',
        'user:test',
        (ArtifactDraft(key, value),),
        {key: None},
    )


@pytest.mark.asyncio
async def test_runtime_executes_pipeline_retries_artifact_and_reopens(tmp_path):
    operations = (normalize_operation, double_operation)
    runtime = await ArtifactRuntime.open(tmp_path, operations, terminate_timeout=2.0)
    result_key = ArtifactKey.scalar('result')
    try:
        created = await runtime.create('run-1', _initial_commit(3))
        assert created.status == 'created'
        assert created.ready_count == 1

        await runtime.start('run-1')
        completed = await runtime.wait_until_settled('run-1', timeout=10.0)
        assert completed.status == 'completed'
        first_ref = completed.completed_artifacts[result_key]
        assert await runtime.read('run-1', first_ref) == 8
        assert [attempt.status for attempt in await runtime.attempts('run-1')] == [
            'succeeded',
            'succeeded',
        ]
        assert {
            event.update.phase for event in await runtime.progress_events('run-1')
        } == {'normalize', 'double'}

        await runtime.retry_artifact(
            'run-1',
            result_key,
            request_id='retry-result',
        )
        retried = await runtime.wait_until_settled('run-1', timeout=10.0)
        second_ref = retried.completed_artifacts[result_key]
        assert second_ref.version == first_ref.version + 1
        request = (await runtime.retry_requests('run-1'))[0]
        assert request.status == 'fulfilled'
        assert request.base_ref == first_ref
        assert request.result_ref == second_ref
        assert [record.ref for record in await runtime.history('run-1', result_key)] == [
            first_ref,
            second_ref,
        ]
        assert await runtime.run_ids() == ('run-1',)
        assert await runtime.has_run('run-1') is True
    finally:
        await runtime.close()

    reopened = await ArtifactRuntime.open(tmp_path, operations, terminate_timeout=2.0)
    try:
        snapshot = await reopened.snapshot('run-1')
        assert snapshot.status == 'completed'
        assert await reopened.read(
            'run-1',
            snapshot.completed_artifacts[result_key],
        ) == 8
        await reopened.delete_run('run-1')
        assert await reopened.has_run('run-1') is False
    finally:
        await reopened.close()


@pytest.mark.asyncio
async def test_runtime_executes_isolated_operation_when_process_inspection_is_allowed(
    tmp_path,
    monkeypatch,
):
    existing_pythonpath = os.environ.get('PYTHONPATH', '')
    test_pythonpath = str(Path(__file__).parent)
    if existing_pythonpath:
        test_pythonpath += os.pathsep + existing_pythonpath
    monkeypatch.setenv('PYTHONPATH', test_pythonpath)

    runtime = await ArtifactRuntime.open(
        tmp_path,
        (isolated_double_operation,),
        terminate_timeout=2.0,
    )
    normalized_key = ArtifactKey.scalar('normalized')
    result_key = ArtifactKey.scalar('result')
    try:
        await runtime.create('run-isolated', ArtifactCommit(
            'seed',
            'user:test',
            (ArtifactDraft(normalized_key, 4),),
            {normalized_key: None},
        ))
        await runtime.start('run-isolated')
        completed = await runtime.wait_until_settled(
            'run-isolated',
            timeout=10.0,
        )
        if (
            completed.status == 'failed'
            and completed.error is not None
            and 'sysctl(KERN_PROC_ALL)' in completed.error.message
        ):
            pytest.skip('macOS sandbox blocks psutil process-tree inspection')

        assert completed.status == 'completed'
        assert await runtime.read(
            'run-isolated',
            completed.completed_artifacts[result_key],
        ) == 8
        assert [
            event.update.phase
            for event in await runtime.progress_events('run-isolated')
        ] == ['double']
    finally:
        await runtime.close()


@pytest.mark.asyncio
async def test_runtime_records_failure_and_retries_the_run(tmp_path):
    FAILURES_BY_RUN.pop('run-failure', None)
    runtime = await ArtifactRuntime.open(tmp_path, (fail_once_operation,))
    try:
        await runtime.create('run-failure', _initial_commit('value'))
        await runtime.start('run-failure')
        failed = await runtime.wait_until_settled('run-failure', timeout=5.0)
        assert failed.status == 'failed'
        assert failed.error.kind == 'RuntimeError'
        assert 'intentional first-attempt failure' in failed.error.message

        await runtime.retry('run-failure')
        completed = await runtime.wait_until_settled('run-failure', timeout=5.0)
        assert completed.status == 'completed'
        result_ref = completed.completed_artifacts[ArtifactKey.scalar('result')]
        assert await runtime.read('run-failure', result_ref) == 'value'
        assert [attempt.status for attempt in await runtime.attempts('run-failure')] == [
            'failed',
            'succeeded',
        ]
    finally:
        await runtime.close()


@pytest.mark.asyncio
async def test_runtime_pause_resume_and_cancel_lifecycle(tmp_path):
    runtime = await ArtifactRuntime.open(tmp_path, (slow_copy_operation,))
    source_key = ArtifactKey.scalar('source')
    try:
        await runtime.create(
            'run-pause',
            _initial_commit({'value': 'old', 'delay': 30}),
        )
        started = await runtime.start('run-pause')
        assert started.status == 'running'
        assert started.running

        paused = await runtime.pause('run-pause')
        assert paused.status == 'paused'
        assert not paused.running
        assert (await runtime.attempts('run-pause'))[0].status == 'cancelled'

        source = await runtime.head('run-pause', source_key)
        assert source is not None
        await runtime.commit('run-pause', ArtifactCommit(
            'replace-source',
            'user:test',
            (ArtifactDraft(source_key, {'value': 'new', 'delay': 0}),),
            {source_key: source.ref},
        ))
        await runtime.resume('run-pause')
        completed = await runtime.wait_until_settled('run-pause', timeout=5.0)
        assert completed.status == 'completed'
        result_ref = completed.completed_artifacts[ArtifactKey.scalar('result')]
        assert await runtime.read('run-pause', result_ref) == 'new'

        await runtime.create(
            'run-cancel',
            _initial_commit({'value': 'unused', 'delay': 30}),
        )
        await runtime.start('run-cancel')
        cancelled = await runtime.cancel('run-cancel')
        assert cancelled.status == 'cancelled'
        assert not cancelled.active_attempts
        assert (await runtime.attempts('run-cancel'))[0].status == 'cancelled'
        with pytest.raises(DefinitionError, match='cannot start'):
            await runtime.start('run-cancel')
    finally:
        await runtime.close()


@pytest.mark.asyncio
async def test_runtime_facade_reads_filters_releases_and_closes_cleanly(tmp_path):
    runtime = await ArtifactRuntime.open(
        tmp_path,
        (normalize_operation, double_operation),
        terminate_timeout=2.0,
    )
    source_key = ArtifactKey.scalar('source')
    result_key = ArtifactKey.scalar('result')

    async with runtime:
        created = await runtime.create('run-facade', _initial_commit(5))
        assert await runtime.inspect('run-facade') == created
        assert await runtime.wait_for_status(
            'run-facade',
            'created',
            timeout=1.0,
        ) == created

        await runtime.start('run-facade')
        completed = await runtime.wait_for_status(
            'run-facade',
            'completed',
            timeout=5.0,
        )
        source_ref = completed.completed_artifacts[source_key]
        result_ref = completed.completed_artifacts[result_key]
        assert await runtime.read_many(
            'run-facade',
            (source_ref, result_ref),
        ) == {
            source_ref: 5,
            result_ref: 12,
        }
        assert (await runtime.record('run-facade', result_ref)).ref == result_ref
        assert (await runtime.head('run-facade', result_key)).ref == result_ref

        attempts = await runtime.attempts('run-facade')
        second_events = await runtime.progress_events(
            'run-facade',
            attempts[1].attempt_id,
        )
        assert [event.update.phase for event in second_events] == ['double']

        await runtime.release('run-facade')
        assert (await runtime.snapshot('run-facade')).status == 'completed'
        with pytest.raises(DefinitionError, match='already exists'):
            await runtime.create('run-facade', _initial_commit(1))
        with pytest.raises(DefinitionError, match='run not found'):
            await runtime.snapshot('missing-run')

        await runtime.delete_run('run-facade')
        assert await runtime.run_ids() == ()

    with pytest.raises(RuntimeError, match='closed'):
        await runtime.has_run('run-facade')

"""Unit and persistence tests for the artifact runtime core."""

import asyncio

import pytest

from evo.artifact_runtime import (
    ArtifactCommit,
    ArtifactDraft,
    ArtifactKey,
    ArtifactRecord,
    ArtifactRef,
    ArtifactRetryRequest,
    ArtifactSnapshot,
    AttemptSnapshot,
    BoundAggregate,
    DefinitionError,
    OperationContext,
    OperationInvocation,
    OperationResult,
    PartitionGuard,
    PartitionSet,
    PlanningError,
    ProgressUpdate,
    RuntimeErrorInfo,
    RuntimeSnapshot,
    all_items,
    each,
    keyed,
    one,
    operation,
    partitioned,
    scalar,
)
from evo.artifact_runtime.artifact import merge_refs
from evo.artifact_runtime.execution import start_execution
from evo.artifact_runtime.planning import (
    PlanAwaiting,
    PlanComplete,
    PlanReady,
    compile_operations,
    obsolete_retries,
    plan_next,
)
from evo.artifact_runtime.store import ArtifactStore


# Artifact and runtime-state value objects


def test_artifact_keys_refs_and_partition_sets_validate_identity():
    scalar = ArtifactKey.scalar('config')
    member = ArtifactKey.partition('case', 'case-1')

    assert scalar == ArtifactKey('config')
    assert member == ArtifactKey('case', 'case-1')
    assert 'case-1' in PartitionSet(('case-1', 'case-2'))

    with pytest.raises(DefinitionError, match='partition_key'):
        ArtifactKey('case', ' ')
    with pytest.raises(DefinitionError, match='>= 1'):
        ArtifactRef(scalar, 0)
    with pytest.raises(DefinitionError, match='unique'):
        PartitionSet(('case-1', 'case-1'))


def test_records_and_merge_refs_are_canonical_and_reject_conflicts():
    first = ArtifactRef(ArtifactKey.scalar('first'), 1)
    second = ArtifactRef(ArtifactKey.scalar('second'), 1)
    record = ArtifactRecord(
        ArtifactRef(ArtifactKey.scalar('result'), 1),
        'operation:combine',
        (second, first),
    )

    assert record.input_refs == (first, second)
    assert merge_refs((second,), (first, second)) == (first, second)

    conflicting = ArtifactRef(first.key, 2)
    with pytest.raises(DefinitionError, match='conflicting refs'):
        merge_refs((first,), (conflicting,))
    with pytest.raises(DefinitionError, match='at most one ref'):
        ArtifactRecord(record.ref, record.producer, (first, conflicting))


def test_effective_records_remove_stale_lineage_transitively():
    source_key = ArtifactKey.scalar('source')
    middle_key = ArtifactKey.scalar('middle')
    result_key = ArtifactKey.scalar('result')
    old_source = ArtifactRef(source_key, 1)
    current_source = ArtifactRef(source_key, 2)
    middle = ArtifactRef(middle_key, 1)
    result = ArtifactRef(result_key, 1)
    snapshot = ArtifactSnapshot({
        source_key: ArtifactRecord(current_source, 'user:input'),
        middle_key: ArtifactRecord(middle, 'operation:middle', (old_source,)),
        result_key: ArtifactRecord(result, 'operation:result', (middle,)),
    })

    assert snapshot.effective_records() == {
        source_key: ArtifactRecord(current_source, 'user:input'),
    }


def test_artifact_commit_normalizes_values_and_enforces_preconditions():
    key = ArtifactKey.scalar('config')
    ref = ArtifactRef(key, 1)
    guard = PartitionGuard(ArtifactKey.scalar('parts'), 'a')
    commit = ArtifactCommit(
        'commit-1',
        'user:test',
        (ArtifactDraft(key, {'enabled': True}),),
        {key: ref},
        (guard,),
    )

    assert commit.output_keys == (key,)
    assert commit.expected_heads[key] == ref
    assert commit.partition_guards == (guard,)

    with pytest.raises(DefinitionError, match='write keys must be unique'):
        ArtifactCommit(
            'duplicate',
            'user:test',
            (ArtifactDraft(key, 1), ArtifactDraft(key, 2)),
        )
    with pytest.raises(DefinitionError, match='expected head'):
        ArtifactCommit(
            'wrong-head',
            'user:test',
            (ArtifactDraft(key, 1),),
            {key: ArtifactRef(ArtifactKey.scalar('other'), 1)},
        )


def test_progress_attempt_retry_and_runtime_state_invariants():
    update = ProgressUpdate(
        'download',
        'halfway',
        current=1,
        total=2,
        detail={'source': 'fixture'},
    )
    assert dict(update.detail) == {'source': 'fixture'}

    with pytest.raises(DefinitionError, match='cannot exceed'):
        ProgressUpdate('download', current=3, total=2)
    with pytest.raises(DefinitionError, match='JSON-serializable'):
        ProgressUpdate('download', detail={'bad': object()})

    key = ArtifactKey.scalar('result')
    base = ArtifactRef(key, 1)
    newer = ArtifactRef(key, 2)
    fulfilled = ArtifactRetryRequest(
        'retry-1',
        key,
        base,
        'fulfilled',
        1.0,
        newer,
    )
    assert fulfilled.result_ref == newer

    error = RuntimeErrorInfo('RuntimeError', 'boom')
    attempt = AttemptSnapshot(
        'attempt-1',
        'invocation-1',
        'op',
        '',
        'failed',
        1.0,
        error=error,
    )
    snapshot = RuntimeSnapshot(
        'run-1',
        'failed',
        error=error,
        active_attempts=(attempt,),
    )
    assert snapshot.error == error

    with pytest.raises(DefinitionError, match='requires error details'):
        RuntimeSnapshot('run-2', 'failed')
    with pytest.raises(DefinitionError, match='newer than base'):
        ArtifactRetryRequest('retry-2', key, base, 'fulfilled', 1.0, base)


# Operation declaration, binding, execution, and demand planning


@operation(
    op_id='test.plan.split',
    inputs={'source': one('source')},
    outputs={
        'parts': scalar('parts'),
        'items': partitioned('item', over='parts'),
        'weights': partitioned('weight', over='parts'),
    },
    execution='cooperative',
)
async def split_operation(ctx, source):
    await ctx.report('split', total=2)
    return OperationResult({
        'parts': PartitionSet(('b', 'a')),
        'items': {'b': source + '-b', 'a': source + '-a'},
        'weights': {'b': 2, 'a': 1},
    })


@operation(
    op_id='test.plan.process',
    inputs={
        'item': each('item', over='parts'),
        'weight': keyed('weight'),
    },
    outputs={'processed': partitioned('processed')},
    execution='cooperative',
    max_concurrency=2,
)
async def process_operation(ctx, item, weight):
    return OperationResult({'processed': f'{item}:{weight}'})


@operation(
    op_id='test.plan.aggregate',
    inputs={'items': all_items('processed', over='parts')},
    outputs={'result': scalar('result')},
    execution='cooperative',
)
async def aggregate_operation(ctx, items):
    return OperationResult({'result': tuple(items.values())})


OPERATIONS = (split_operation, process_operation, aggregate_operation)


def _record(key, version=1, *, producer='user:test', inputs=()):
    ref = ArtifactRef(key, version)
    return ArtifactRecord(ref, producer, inputs)


def _apply(snapshot, commit):
    records = dict(snapshot.records)
    partition_sets = dict(snapshot.partition_sets)
    for write in commit.writes:
        previous = records.get(write.key)
        version = 1 if previous is None else previous.ref.version + 1
        ref = ArtifactRef(write.key, version)
        records[write.key] = ArtifactRecord(ref, commit.producer, write.input_refs)
        if isinstance(write.value, PartitionSet):
            partition_sets[write.key] = write.value
    return ArtifactSnapshot(records, partition_sets)


def test_operation_context_reports_validated_progress():
    updates = []

    async def report(update):
        updates.append(update)

    context = OperationContext('run-1', 'invocation-1', _reporter=report)
    asyncio.run(context.report(
        'prepare',
        'ready',
        current=1,
        total=3,
        detail={'kind': 'fixture'},
    ))

    assert updates[0].phase == 'prepare'
    assert dict(updates[0].detail) == {'kind': 'fixture'}


def test_compile_and_plan_partitioned_pipeline_from_input_to_completion():
    definition = compile_operations(OPERATIONS)
    assert tuple(op.spec.op_id for op in definition.operations) == (
        'test.plan.split',
        'test.plan.process',
        'test.plan.aggregate',
    )
    assert definition.terminal_artifact_ids == ('result',)
    assert definition.partition_set_by_artifact == {
        'item': 'parts',
        'weight': 'parts',
        'processed': 'parts',
    }

    empty = plan_next(definition, ArtifactSnapshot())
    assert isinstance(empty, PlanAwaiting)
    assert empty.artifact_keys == (ArtifactKey.scalar('source'),)

    source_key = ArtifactKey.scalar('source')
    source_record = _record(source_key)
    snapshot = ArtifactSnapshot({source_key: source_record})
    split_plan = plan_next(definition, snapshot)
    assert isinstance(split_plan, PlanReady)
    assert [item.operation.spec.op_id for item in split_plan.invocations] == [
        'test.plan.split',
    ]
    assert split_plan.invocations[0].bind_values({
        source_record.ref: 'fixture',
    }) == {'source': 'fixture'}

    split_result = asyncio.run(
        split_operation(
            OperationContext('run-1', split_plan.invocations[0].invocation_id),
            'fixture',
        )
    )
    split_commit = split_plan.invocations[0].artifact_commit(split_result)
    definition.validate_commit(split_commit)
    snapshot = _apply(snapshot, split_commit)

    process_plan = plan_next(definition, snapshot)
    assert isinstance(process_plan, PlanReady)
    assert [item.partition_key for item in process_plan.invocations] == ['b', 'a']
    for invocation in process_plan.invocations:
        commit = invocation.artifact_commit(OperationResult({
            'processed': f'processed-{invocation.partition_key}',
        }))
        definition.validate_commit(commit)
        assert commit.partition_guards[0].partition_key == invocation.partition_key
        snapshot = _apply(snapshot, commit)

    aggregate_plan = plan_next(definition, snapshot)
    assert isinstance(aggregate_plan, PlanReady)
    aggregate = aggregate_plan.invocations[0]
    assert tuple(
        ref.key.partition_key for ref in aggregate.inputs['items'].member_refs
    ) == ('b', 'a')
    snapshot_before_result = snapshot
    snapshot = _apply(
        snapshot,
        aggregate.artifact_commit(OperationResult({'result': ('b', 'a')})),
    )
    assert isinstance(plan_next(definition, snapshot), PlanComplete)

    retry_key = ArtifactKey.partition('processed', 'b')
    retry_ref = snapshot_before_result.records[retry_key].ref
    retry = ArtifactRetryRequest(
        'retry-b',
        retry_key,
        retry_ref,
        'pending',
        1.0,
    )
    retry_plan = plan_next(definition, snapshot_before_result, (retry,))
    assert isinstance(retry_plan, PlanReady)
    assert len(retry_plan.invocations) == 1
    assert retry_plan.invocations[0].partition_key == 'b'
    assert retry_plan.invocations[0].retry_request_id == 'retry-b'


def test_planner_invalidates_operation_outputs_when_input_version_changes():
    definition = compile_operations(OPERATIONS)
    source_key = ArtifactKey.scalar('source')
    source_record = _record(source_key)
    snapshot = ArtifactSnapshot({source_key: source_record})

    split_invocation = plan_next(definition, snapshot).invocations[0]
    snapshot = _apply(snapshot, split_invocation.artifact_commit(OperationResult({
        'parts': PartitionSet(('a',)),
        'items': {'a': 'value'},
        'weights': {'a': 1},
    })))
    process_invocation = plan_next(definition, snapshot).invocations[0]
    snapshot = _apply(
        snapshot,
        process_invocation.artifact_commit(OperationResult({'processed': 'done'})),
    )
    aggregate_invocation = plan_next(definition, snapshot).invocations[0]
    snapshot = _apply(
        snapshot,
        aggregate_invocation.artifact_commit(OperationResult({'result': 'done'})),
    )

    records = dict(snapshot.records)
    records[source_key] = _record(source_key, 2)
    changed = ArtifactSnapshot(records, snapshot.partition_sets)
    replanned = plan_next(definition, changed)

    assert isinstance(replanned, PlanReady)
    assert replanned.invocations[0].operation is split_operation
    assert replanned.invocations[0].inputs['source'].version == 2
    assert set(replanned.view.records) == {source_key}


def test_operation_declarations_reject_invalid_signatures_and_graphs():
    with pytest.raises(DefinitionError, match='async def'):
        operation(
            op_id='test.invalid.sync',
            inputs={},
            outputs={'result': scalar('sync-result')},
            execution='cooperative',
        )(lambda ctx: OperationResult({'result': 1}))

    with pytest.raises(DefinitionError, match='keyed inputs require'):

        @operation(
            op_id='test.invalid.keyed',
            inputs={'value': keyed('value')},
            outputs={'result': scalar('keyed-result')},
            execution='cooperative',
        )
        async def invalid_keyed(ctx, value):
            return OperationResult({'result': value})

    @operation(
        op_id='test.cycle.left',
        inputs={'right': one('right')},
        outputs={'left': scalar('left')},
        execution='cooperative',
    )
    async def left(ctx, right):
        return OperationResult({'left': right})

    @operation(
        op_id='test.cycle.right',
        inputs={'left': one('left')},
        outputs={'right': scalar('right')},
        execution='cooperative',
    )
    async def right(ctx, left):
        return OperationResult({'right': left})

    with pytest.raises(DefinitionError, match='acyclic'):
        compile_operations((left, right))


def test_invocation_tracks_aggregate_lineage_and_rejects_invalid_batch_results():
    parts_ref = ArtifactRef(ArtifactKey.scalar('parts'), 1)
    member_refs = (
        ArtifactRef(ArtifactKey.partition('processed', 'a'), 1),
        ArtifactRef(ArtifactKey.partition('processed', 'b'), 1),
    )
    aggregate = BoundAggregate(parts_ref, member_refs)
    invocation = OperationInvocation(
        aggregate_operation,
        {'items': aggregate},
    )

    assert invocation.value_refs() == member_refs
    assert invocation.lineage_refs() == (parts_ref, *member_refs)
    assert invocation.bind_values({
        member_refs[0]: 'A',
        member_refs[1]: 'B',
    }) == {'items': {'a': 'A', 'b': 'B'}}
    assert invocation.invocation_id == OperationInvocation(
        aggregate_operation,
        {'items': aggregate},
    ).invocation_id

    split = OperationInvocation(
        split_operation,
        {'source': ArtifactRef(ArtifactKey.scalar('source'), 1)},
    )
    with pytest.raises(DefinitionError, match='must be a mapping'):
        split.artifact_commit(OperationResult({
            'parts': PartitionSet(('a',)),
            'items': 'not-a-mapping',
            'weights': {'a': 1},
        }))
    with pytest.raises(DefinitionError, match='must share keys'):
        split.artifact_commit(OperationResult({
            'parts': PartitionSet(('a',)),
            'items': {'a': 'A'},
            'weights': {'b': 1},
        }))
    with pytest.raises(DefinitionError, match='must be PartitionSet'):
        split.artifact_commit(OperationResult({
            'parts': ('a',),
            'items': {'a': 'A'},
            'weights': {'a': 1},
        }))
    with pytest.raises(DefinitionError, match='keys must match'):
        split.artifact_commit(OperationResult({
            'parts': PartitionSet(('b',)),
            'items': {'a': 'A'},
            'weights': {'a': 1},
        }))


def test_compile_and_retry_validation_reject_ambiguous_requests():
    with pytest.raises(DefinitionError, match='duplicate operation id'):
        compile_operations((split_operation, split_operation))

    definition = compile_operations(OPERATIONS)
    key = ArtifactKey.scalar('result')
    record = _record(key)
    snapshot = ArtifactSnapshot({key: record})
    cancelled = ArtifactRetryRequest(
        'cancelled',
        key,
        record.ref,
        'cancelled',
        1.0,
    )
    with pytest.raises(DefinitionError, match='must be pending'):
        plan_next(definition, snapshot, (cancelled,))

    pending = ArtifactRetryRequest('pending', key, record.ref, 'pending', 1.0)
    duplicate = ArtifactRetryRequest('duplicate', key, record.ref, 'pending', 2.0)
    with pytest.raises(PlanningError, match='one invocation'):
        plan_next(definition, snapshot, (pending, duplicate))

    advanced = ArtifactSnapshot({key: _record(key, 2)})
    assert obsolete_retries(definition, advanced, (pending,)) == (pending,)


@pytest.mark.asyncio
async def test_cooperative_execution_validates_results_and_termination():
    source_ref = ArtifactRef(ArtifactKey.scalar('source'), 1)
    invocation = OperationInvocation(
        split_operation,
        {'source': source_ref},
    )
    context = OperationContext('run', invocation.invocation_id)
    handle = await start_execution(
        invocation,
        context,
        {'source': 'value'},
        terminate_timeout=1.0,
    )
    result = await handle.wait()
    assert result.values['parts'] == PartitionSet(('b', 'a'))
    await handle.terminate()

    with pytest.raises(ValueError, match='must be positive'):
        await start_execution(
            invocation,
            context,
            {'source': 'value'},
            terminate_timeout=0,
        )


# SQLite persistence, concurrency guards, retries, and recovery


@pytest.mark.asyncio
async def test_store_persists_idempotent_commits_history_and_partition_sets(tmp_path):
    store = await ArtifactStore.open(tmp_path)
    key = ArtifactKey.scalar('config')
    initial = ArtifactCommit(
        'seed',
        'user:test',
        (ArtifactDraft(key, {'value': 1}),),
        {key: None},
    )
    try:
        await store.create_run('run-1', initial)
        first = await store.head('run-1', key)
        assert first is not None
        assert await store.read('run-1', first.ref) == {'value': 1}

        replay = await store.commit('run-1', initial)
        assert replay.status == 'ok'
        assert replay.replayed is True
        assert replay.refs == (first.ref,)

        update = ArtifactCommit(
            'update',
            'user:test',
            (ArtifactDraft(key, {'value': 2}),),
            {key: first.ref},
        )
        applied = await store.commit('run-1', update)
        assert applied.status == 'ok'
        assert applied.refs[0].version == 2

        stale = await store.commit('run-1', ArtifactCommit(
            'stale',
            'user:test',
            (ArtifactDraft(key, {'value': 3}),),
            {key: first.ref},
        ))
        assert stale.status == 'stale'
        assert stale.refs == ()
        assert [record.ref.version for record in await store.history('run-1', key)] == [1, 2]
        assert await store.read_many('run-1', (first.ref, applied.refs[0])) == {
            first.ref: {'value': 1},
            applied.refs[0]: {'value': 2},
        }

        parts_key = ArtifactKey.scalar('parts')
        item_a = ArtifactKey.partition('item', 'a')
        partition_commit = ArtifactCommit(
            'partitions',
            'user:test',
            (
                ArtifactDraft(parts_key, PartitionSet(('a',))),
                ArtifactDraft(item_a, 'A'),
            ),
            {parts_key: None, item_a: None},
        )
        await store.commit('run-1', partition_commit)
        snapshot = await store.snapshot('run-1', ('parts',))
        assert snapshot.partition_sets[parts_key] == PartitionSet(('a',))
        assert await store.read('run-1', snapshot.records[item_a].ref) == 'A'

        with pytest.raises(DefinitionError, match='commit id reused'):
            await store.commit('run-1', ArtifactCommit(
                'update',
                'user:test',
                (ArtifactDraft(key, {'different': True}),),
            ))
    finally:
        await store.close()


@pytest.mark.asyncio
async def test_store_tracks_retry_progress_and_recovers_interrupted_attempts(tmp_path):
    store = await ArtifactStore.open(tmp_path)
    key = ArtifactKey.scalar('source')
    initial = ArtifactCommit(
        'seed',
        'user:test',
        (ArtifactDraft(key, 'value'),),
    )
    await store.create_run('run-1', initial)
    head = await store.head('run-1', key)
    assert head is not None

    request = await store.request_retry('run-1', 'retry-1', key, head.ref)
    repeated = await store.request_retry('run-1', 'retry-1', key, head.ref)
    assert repeated == request
    assert (await store.cancel_retry('run-1', 'retry-1')).status == 'cancelled'

    await store.set_run_state('run-1', 'running')
    attempt = await store.create_attempt(
        'run-1',
        'attempt-1',
        'invocation-1',
        'test.operation',
        '',
        (head.ref,),
        (ArtifactKey.scalar('result'),),
    )
    assert attempt.status == 'scheduled'
    attempt = await store.set_attempt_status('run-1', 'attempt-1', 'running')
    assert attempt.started_at is not None
    event = await store.append_progress(
        'run-1',
        'attempt-1',
        ProgressUpdate('work', current=1, total=2),
    )
    assert event.sequence == 1
    await store.close()

    recovered = await ArtifactStore.open(tmp_path)
    try:
        assert await recovered.recover_runs() == ('run-1',)
        assert (await recovered.run_state('run-1')).status == 'paused'
        attempts = await recovered.attempts('run-1')
        assert attempts[0].status == 'interrupted'
        events = await recovered.progress_events('run-1', 'attempt-1')
        assert [item.update.phase for item in events] == ['work']
        assert [item.status for item in await recovered.retry_requests('run-1')] == [
            'cancelled',
        ]

        await recovered.delete_run('run-1')
        assert await recovered.run_ids() == ()
        with pytest.raises(DefinitionError, match='run not found'):
            await recovered.snapshot('run-1')
    finally:
        await recovered.close()


@pytest.mark.asyncio
async def test_store_rejects_missing_payloads_invalid_transitions_and_closed_access(tmp_path):
    store = await ArtifactStore.open(tmp_path)
    source_key = ArtifactKey.scalar('source')
    missing_ref = ArtifactRef(source_key, 1)
    await store.create_run('run-1')
    try:
        with pytest.raises(KeyError) as missing:
            await store.read('run-1', missing_ref)
        assert missing.value.args == (missing_ref,)
        with pytest.raises(DefinitionError, match='input artifact is missing'):
            await store.read_many('run-1', (missing_ref,))
        with pytest.raises(DefinitionError, match='already exists'):
            await store.create_run('run-1')

        await store.set_run_state('run-1', 'running')
        attempt = await store.create_attempt(
            'run-1',
            'attempt-1',
            'invocation-1',
            'test.operation',
            '',
            (),
            (ArtifactKey.scalar('result'),),
        )
        assert attempt.status == 'scheduled'
        cancelled = await store.set_attempt_status(
            'run-1',
            'attempt-1',
            'cancelled',
        )
        assert cancelled.status == 'cancelled'
        with pytest.raises(DefinitionError, match='cannot transition attempt'):
            await store.set_attempt_status('run-1', 'attempt-1', 'running')
        with pytest.raises(DefinitionError, match='running attempt'):
            await store.append_progress(
                'run-1',
                'attempt-1',
                ProgressUpdate('work'),
            )
    finally:
        await store.close()

    with pytest.raises(RuntimeError, match='closed'):
        await store.run_ids()

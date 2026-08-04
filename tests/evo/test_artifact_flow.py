"""Tests for flow definitions, projections, approvals, and stage retries."""

import pytest

from evo.artifact_flow import (
    ArtifactFlow,
    FlowDefinition,
    FlowSnapshot,
    FlowStage,
    StageProgress,
)
from evo.artifact_flow.projection import project_flow
from evo.artifact_runtime import (
    ArtifactCommit,
    ArtifactDraft,
    ArtifactKey,
    ArtifactRef,
    ArtifactRetryRequest,
    AttemptSnapshot,
    DefinitionError,
    OperationResult,
    RuntimeErrorInfo,
    RuntimeSnapshot,
    one,
    operation,
    scalar,
)


DRAFT_KEY = ArtifactKey.scalar('draft')
APPROVAL_KEY = ArtifactKey.scalar('draft-approval')
FINAL_KEY = ArtifactKey.scalar('final')
FLOW_FAILURES = {}


@operation(
    op_id='test.flow.draft',
    inputs={'source': one('source')},
    outputs={'draft': scalar('draft')},
    execution='cooperative',
)
async def draft_operation(ctx, source):
    return OperationResult({'draft': f'draft:{source}'})


@operation(
    op_id='test.flow.publish',
    inputs={
        'draft': one('draft'),
        'approval': one('draft-approval'),
    },
    outputs={'final': scalar('final')},
    execution='cooperative',
)
async def publish_operation(ctx, draft, approval):
    count = FLOW_FAILURES.get(ctx.run_id, 0) + 1
    FLOW_FAILURES[ctx.run_id] = count
    if count == 1:
        raise RuntimeError('publish failed once')
    return OperationResult({
        'final': {
            'draft': draft,
            'approved_stage': approval['stage'],
        },
    })


def _definition():
    return FlowDefinition(
        (publish_operation, draft_operation),
        (
            FlowStage('draft', DRAFT_KEY, APPROVAL_KEY),
            FlowStage('publish', FINAL_KEY),
        ),
    )


def test_flow_definition_assigns_stages_and_enforces_approval_gates():
    definition = _definition()

    assert definition.stage_index_for_artifact('draft') == 0
    assert definition.stage_index_for_operation('test.flow.publish') == 1
    assert definition.stage_entry_operations(0) == (draft_operation,)
    assert definition.stage_entry_operations(1) == (publish_operation,)

    @operation(
        op_id='test.flow.ungated',
        inputs={'draft': one('draft')},
        outputs={'result': scalar('ungated-result')},
        execution='cooperative',
    )
    async def ungated(ctx, draft):
        return OperationResult({'result': draft})

    @operation(
        op_id='test.flow.gated',
        inputs={
            'intermediate': one('ungated-result'),
            'approval': one('draft-approval'),
        },
        outputs={'result': scalar('gated-result')},
        execution='cooperative',
    )
    async def gated(ctx, intermediate, approval):
        return OperationResult({'result': (intermediate, approval)})

    with pytest.raises(ValueError, match='previous stage approval'):
        FlowDefinition(
            (draft_operation, ungated, gated),
            (
                FlowStage('draft', DRAFT_KEY, APPROVAL_KEY),
                FlowStage('ungated', ArtifactKey.scalar('gated-result')),
            ),
        )
    with pytest.raises(ValueError, match='non-final stage requires'):
        FlowDefinition(
            (draft_operation, publish_operation),
            (
                FlowStage('draft', DRAFT_KEY),
                FlowStage('publish', FINAL_KEY),
            ),
        )
    with pytest.raises(ValueError, match='final stage must not'):
        FlowDefinition(
            (draft_operation,),
            (FlowStage('draft', DRAFT_KEY, APPROVAL_KEY),),
        )


def test_project_flow_maps_runtime_frontier_to_stage_progress():
    definition = _definition()
    draft_ref = ArtifactRef(DRAFT_KEY, 1)
    approval_ref = ArtifactRef(APPROVAL_KEY, 1)

    awaiting = project_flow(definition, RuntimeSnapshot(
        'run-1',
        'running',
        completed_artifacts={DRAFT_KEY: draft_ref},
        awaiting_artifacts=(APPROVAL_KEY,),
    ))
    assert awaiting.status == 'awaiting_approval'
    assert awaiting.current_stage == 'draft'
    assert awaiting.pending_approval.stage == 'draft'
    assert [stage.status for stage in awaiting.stages] == [
        'awaiting_approval',
        'pending',
    ]

    active_attempt = AttemptSnapshot(
        'attempt-1',
        'invocation-1',
        publish_operation.spec.op_id,
        '',
        'running',
        1.0,
    )
    running = project_flow(definition, RuntimeSnapshot(
        'run-1',
        'running',
        completed_artifacts={
            DRAFT_KEY: draft_ref,
            APPROVAL_KEY: approval_ref,
        },
        active_attempts=(active_attempt,),
    ))
    assert running.status == 'running'
    assert running.current_stage == 'publish'
    assert [stage.status for stage in running.stages] == ['completed', 'running']

    final_ref = ArtifactRef(FINAL_KEY, 1)
    completed = project_flow(definition, RuntimeSnapshot(
        'run-1',
        'completed',
        completed_artifacts={
            DRAFT_KEY: draft_ref,
            APPROVAL_KEY: approval_ref,
            FINAL_KEY: final_ref,
        },
    ))
    assert completed.status == 'completed'
    assert all(stage.status == 'completed' for stage in completed.stages)


def test_flow_state_objects_validate_identity_and_expose_gate_state():
    result_ref = ArtifactRef(DRAFT_KEY, 1)
    approval_ref = ArtifactRef(APPROVAL_KEY, 1)
    progress = StageProgress(
        'draft',
        DRAFT_KEY,
        result_ref,
        APPROVAL_KEY,
        approval_ref,
        'completed',
    )
    snapshot = FlowSnapshot(
        RuntimeSnapshot('run-state'),
        (progress,),
    )

    assert snapshot.status == 'idle'
    assert snapshot.current_stage == 'draft'
    assert progress.completed is True
    assert progress.approved is True
    assert progress.has_result is True
    assert progress.has_approval is True

    with pytest.raises(ValueError, match='result_ref'):
        StageProgress(
            'draft',
            DRAFT_KEY,
            ArtifactRef(FINAL_KEY, 1),
        )
    with pytest.raises(ValueError, match='names must be unique'):
        FlowSnapshot(RuntimeSnapshot('duplicate'), (progress, progress))
    with pytest.raises(ValueError, match='differ'):
        FlowStage('invalid', DRAFT_KEY, DRAFT_KEY)


@pytest.mark.parametrize(
    ('runtime', 'retries', 'expected'),
    (
        (
            RuntimeSnapshot(
                'run-paused',
                'paused',
                completed_artifacts={
                    DRAFT_KEY: ArtifactRef(DRAFT_KEY, 1),
                    APPROVAL_KEY: ArtifactRef(APPROVAL_KEY, 1),
                },
                active_attempts=(
                    AttemptSnapshot(
                        'attempt-paused',
                        'invocation-paused',
                        publish_operation.spec.op_id,
                        '',
                        'interrupted',
                        1.0,
                    ),
                ),
            ),
            (),
            ('paused', ('completed', 'paused')),
        ),
        (
            RuntimeSnapshot(
                'run-failed',
                'failed',
                completed_artifacts={
                    DRAFT_KEY: ArtifactRef(DRAFT_KEY, 1),
                    APPROVAL_KEY: ArtifactRef(APPROVAL_KEY, 1),
                },
                error=RuntimeErrorInfo('RuntimeError', 'publish failed'),
                active_attempts=(
                    AttemptSnapshot(
                        'attempt-failed',
                        'invocation-failed',
                        publish_operation.spec.op_id,
                        '',
                        'failed',
                        1.0,
                        error=RuntimeErrorInfo('RuntimeError', 'publish failed'),
                    ),
                ),
            ),
            (),
            ('failed', ('completed', 'failed')),
        ),
        (
            RuntimeSnapshot(
                'run-cancelled',
                'cancelled',
                completed_artifacts={
                    DRAFT_KEY: ArtifactRef(DRAFT_KEY, 1),
                },
            ),
            (
                ArtifactRetryRequest(
                    'retry-draft',
                    DRAFT_KEY,
                    ArtifactRef(DRAFT_KEY, 1),
                    'cancelled',
                    1.0,
                ),
            ),
            ('cancelled', ('cancelled', 'pending')),
        ),
    ),
)
def test_project_flow_preserves_active_pause_failure_and_cancel_boundaries(
    runtime,
    retries,
    expected,
):
    projected = project_flow(_definition(), runtime, retries)

    assert projected.status == expected[0]
    assert tuple(stage.status for stage in projected.stages) == expected[1]


@pytest.mark.asyncio
async def test_artifact_flow_approval_and_earlier_stage_retry(tmp_path):
    FLOW_FAILURES.pop('flow-run', None)
    flow = await ArtifactFlow.open(tmp_path, _definition())
    source_key = ArtifactKey.scalar('source')
    try:
        created = await flow.create('flow-run', ArtifactCommit(
            'seed',
            'user:test',
            (ArtifactDraft(source_key, 'input'),),
            {source_key: None},
        ))
        assert created.status == 'idle'

        await flow.start('flow-run')
        boundary = await flow.wait_until_boundary('flow-run', timeout=5.0)
        assert boundary.status == 'awaiting_approval'
        assert boundary.pending_approval.stage == 'draft'
        first_draft = boundary.stages[0].result_ref

        with pytest.raises(DefinitionError, match=r'require approve\(\)'):
            await flow.commit('flow-run', ArtifactCommit(
                'manual-approval',
                'user:test',
                (ArtifactDraft(APPROVAL_KEY, {'approved': True}),),
            ))

        await flow.approve('flow-run', 'draft')
        failed = await flow.wait_until_boundary('flow-run', timeout=5.0)
        assert failed.status == 'failed'
        assert failed.current_stage == 'publish'

        await flow.retry(
            'flow-run',
            stage='draft',
            request_id='rewind',
        )
        rewound = await flow.wait_until_boundary('flow-run', timeout=5.0)
        assert rewound.status == 'awaiting_approval'
        assert rewound.current_stage == 'draft'
        second_draft = rewound.stages[0].result_ref
        assert second_draft.version == first_draft.version + 1

        await flow.approve('flow-run', 'draft')
        completed = await flow.wait_until_boundary('flow-run', timeout=5.0)
        assert completed.status == 'completed'
        final_ref = completed.stages[1].result_ref
        assert await flow.read('flow-run', final_ref) == {
            'draft': 'draft:input',
            'approved_stage': 'draft',
        }
        assert [record.ref.version for record in await flow.history(
            'flow-run',
            DRAFT_KEY,
        )] == [1, 2]
        assert [request.status for request in await flow.retry_requests(
            'flow-run',
        )] == ['fulfilled']
        assert await flow.read_many(
            'flow-run',
            (second_draft, final_ref),
        ) == {
            second_draft: 'draft:input',
            final_ref: {
                'draft': 'draft:input',
                'approved_stage': 'draft',
            },
        }
        assert (await flow.record('flow-run', final_ref)).ref == final_ref
        assert (await flow.head('flow-run', FINAL_KEY)).ref == final_ref
        assert [attempt.status for attempt in await flow.attempts('flow-run')] == [
            'succeeded',
            'failed',
            'succeeded',
            'succeeded',
        ]
        assert await flow.progress_events('flow-run') == ()
        assert await flow.run_ids() == ('flow-run',)
        assert await flow.has_run('flow-run') is True

        await flow.release('flow-run')
        assert (await flow.snapshot('flow-run')).status == 'completed'
        await flow.delete_run('flow-run')
        assert await flow.has_run('flow-run') is False
    finally:
        await flow.close()

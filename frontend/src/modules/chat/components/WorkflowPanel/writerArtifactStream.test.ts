import { describe, expect, it } from 'vitest';
import type { WorkflowSession } from '@/modules/chat/store/workflowPanel';
import type { SubAgentTask, TaskArtifactStream } from '@/modules/chat/store/taskCenter';
import { findWriterArtifactStream, shouldShowArtifactStream } from './writerArtifactStream';

function stream(slot: string, streamId: string): TaskArtifactStream {
  return {
    task_id: `${streamId}-task`,
    slot,
    content_type: 'text/markdown',
    stream_id: streamId,
    chunk_index: 2,
    content: 'preview',
    state: 'streaming',
  };
}

function task(taskId: string, status: SubAgentTask['status'], streams: TaskArtifactStream[]): SubAgentTask {
  return {
    task_id: taskId,
    title: '',
    agent_type: 'workflow_step',
    mode: 'auto',
    status,
    progress_pct: 50,
    sources: [],
    artifacts: [],
    artifact_streams: streams,
    execution_log: [],
  };
}

describe('findWriterArtifactStream', () => {
  it.each(['running', 'succeeded', 'failed', 'interrupted', 'canceled'] as const)(
    'flushes the display backlog at 100%% or terminal status: %s', (status) => {
      const original = { ...stream('experiment_document', 's'), deltas: ['pre', 'view'] };
      const owner = task('owner', status, [original]);
      owner.progress_pct = status === 'running' ? 100 : 50;
      const result = findWriterArtifactStream(
        { steps: [{ step_id: 'experiment', task_id: 'owner' }] } as WorkflowSession,
        'experiment', 'experiment_document', [owner],
      );
      expect(result?.content).toBe('preview');
      expect(result?.deltas).toBeUndefined();
      expect(result?.state).toBe('ended');
      expect(original.deltas).toEqual(['pre', 'view']);
    },
  );
  it('uses completion from the owning workflow step even when task SSE lags', () => {
    const original = { ...stream('revise_document_document', 'revision'), deltas: ['pre', 'view'] };
    const owner = task('owner', 'running', [original]);
    const session = { steps: [{ step_id: 'revise_document', task_id: 'owner', status: 'succeeded' }] } as WorkflowSession;
    const result = findWriterArtifactStream(session, 'revise_document', original.slot, [owner]);
    expect(result?.state).toBe('ended');
    expect(result?.deltas).toBeUndefined();
    expect(original.state).toBe('streaming');
  });

  it('streams a retry independently of the completed attempt', () => {
    const slot = 'revise_document_document';
    const retry = stream(slot, 'retry-stream');
    const session = { steps: [
      { step_id: 'revise_document', task_id: 'old', status: 'succeeded' },
      { step_id: 'revise_document', task_id: 'retry', status: 'running' },
    ] } as WorkflowSession;
    expect(findWriterArtifactStream(session, 'revise_document', slot, [
      task('old', 'succeeded', [stream(slot, 'old-stream')]), task('retry', 'running', [retry]),
    ])).toBe(retry);
  });
  const stepCases: Array<[string, string]> = [
    ['outline', 'outline_document'],
    ['write_outline', 'write_outline_document'],
    ['write_flat_document', 'flat_draft_document'],
    ['write_document', 'draft_document'],
    ['write_document', 'write_document_document'],
    ['revise_document', 'revise_document_document'],
    ['experiment', 'experiment_document'],
    ['experiment', 'experiment_code'],
    ['experiment', 'experiment_results'],
  ];

  it.each(stepCases)('matches the %s step stream to %s', (stepId: string, slotId: string) => {
    const expected = stream(slotId, `${stepId}-stream`);
    const session = {
      steps: [{ step_id: stepId, task_id: `${stepId}-task` }],
    } as WorkflowSession;

    expect(findWriterArtifactStream(
      session,
      stepId,
      slotId,
      [task(`${stepId}-task`, 'running', [expected])],
    )).toBe(expected);
  });

  it.each(['draft_document', 'write_outline_document'])('uses only an unambiguous live fallback for %s', (slotId) => {
    const expected = stream(slotId, 'draft-stream');
    const session = { steps: [] } as unknown as WorkflowSession;

    expect(findWriterArtifactStream(
      session,
      'write_document',
      slotId,
      [task('draft-task', 'running', [expected])],
    )).toBe(expected);

    expect(findWriterArtifactStream(
      session,
      'write_document',
      slotId,
      [
        task('draft-task-1', 'running', [expected]),
        task('draft-task-2', 'pending', [stream(slotId, 'other-stream')]),
      ],
    )).toBeUndefined();
  });

  it('does not attach Writer previews to unrelated slots', () => {
    expect(findWriterArtifactStream(
      { steps: [] } as unknown as WorkflowSession,
      'prepare',
      'source_document',
      [task('prepare-task', 'running', [stream('source_document', 'source-stream')])],
    )).toBeUndefined();
  });

  it.each(['write_outline_document', 'write_document_document', 'revise_document_document'])(
    'withdraws rejected paper previews for %s, including the live fallback',
    (slotId) => {
      const rejected = { ...stream(slotId, 'rejected'), state: 'aborted' as const };
      const tasks = [task('paper-task', 'running', [rejected])];
      expect(findWriterArtifactStream(
        { workflow_id: 'paper-writing', steps: [{ step_id: 'paper', task_id: 'paper-task' }] } as WorkflowSession,
        'paper', slotId, tasks,
      )).toBeUndefined();
      expect(findWriterArtifactStream(
        { workflow_id: 'paper-writing', steps: [] } as unknown as WorkflowSession, 'paper', slotId, tasks,
      )).toBeUndefined();
    },
  );
});

it.each(['research_topic', 'refine_topic', 'literature_review', 'experiment', 'data_analysis',
  'write_outline', 'write_document', 'peer_review', 'revise_document', 'reading_reflection', 'finish'])(
  'keeps %s preview until the same attempt publishes its selected revision', (stepId) => {
    const session = { workflow_id: 'paper-writing', steps: [
      { id: 'new', step_id: stepId, task_id: 'owner', attempt: 2, status: 'succeeded' },
    ] } as WorkflowSession;
    const preview = findWriterArtifactStream(session, stepId, `${stepId}_document`, [
      task('owner', 'running', [stream(`${stepId}_document`, 'live')]),
    ])!;
    expect(preview.state).toBe('ended');
    const old = { selected: true, producer_attempt_id: 'old', step_id: stepId, attempt: 1 } as any;
    expect(shouldShowArtifactStream(preview, [old])).toBe(true);
    expect(shouldShowArtifactStream(preview, [{ ...old, producer_attempt_id: 'new', attempt: 2 }])).toBe(false);
    expect(shouldShowArtifactStream(preview, [{ ...old, producer_attempt_id: 'new', selected: false }])).toBe(true);
  },
);

it('does not borrow a previous or unrelated paper attempt preview', () => {
  const session = { workflow_id: 'paper-writing', steps: [
    { id: 'old', step_id: 'experiment', task_id: 'old', attempt: 1, status: 'succeeded' },
    { id: 'new', step_id: 'experiment', task_id: 'new', attempt: 2, status: 'running' },
  ] } as WorkflowSession;
  expect(findWriterArtifactStream(session, 'experiment', 'experiment_document', [
    task('old', 'succeeded', [stream('experiment_document', 'old')]),
    task('unrelated', 'running', [stream('experiment_document', 'other')]),
  ])).toBeUndefined();
});

it.each(['writer-workflow', 'custom-workflow'])(
  'preserves the existing preview handoff in %s', (workflowId) => {
    const session = { workflow_id: workflowId, steps: [
      { id: 'current', step_id: 'custom', task_id: 'owner', attempt: 2, status: 'succeeded' },
    ] } as WorkflowSession;
    const preview = findWriterArtifactStream(session, 'custom', 'custom_output', [
      task('owner', 'succeeded', [stream('custom_output', 'current')]),
    ])!;
    expect(preview.attempt_id).toBeUndefined();
    expect(shouldShowArtifactStream(preview, [{ selected: true, producer_attempt_id: 'previous' } as any])).toBe(false);
    expect(shouldShowArtifactStream(preview, [{ selected: true, producer_attempt_id: 'current' } as any])).toBe(false);
    expect(shouldShowArtifactStream({ ...preview, state: 'aborted' }, [
      { selected: true, producer_attempt_id: 'previous' } as any,
    ])).toBe(false);
    expect(shouldShowArtifactStream({ ...preview, state: 'aborted' }, [])).toBe(true);
  },
);

it('preserves the standalone Writer aborted-preview error view', () => {
  const rejected = { ...stream('draft_document', 'failed'), state: 'aborted' as const };
  expect(findWriterArtifactStream(
    { workflow_id: 'writer-workflow', steps: [{ step_id: 'write', task_id: 'owner' }] } as WorkflowSession,
    'write', 'draft_document', [task('owner', 'failed', [rejected])],
  )?.state).toBe('aborted');
});

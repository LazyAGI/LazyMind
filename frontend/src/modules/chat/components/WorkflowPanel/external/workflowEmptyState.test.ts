import { describe, expect, it } from 'vitest';
import type { WorkflowRuntimeProjection } from '@/modules/chat/store/workflowPanel';
import { workflowEmptyStateKey } from './workflowEmptyState';

type Node = NonNullable<WorkflowRuntimeProjection['nodes']>[string];
const idle: Node = {
  execution: 'none', validity: 'effective', branch: 'active',
  readiness: 'not_applicable', reachability: 'unreachable', requires_approval: false,
};

describe('workflow empty content state', () => {
  it.each<[Partial<Node>, string]>([
    [{ branch: 'pruned' }, 'Skipped'],
    [{ branch: 'bypassed' }, 'Skipped'],
    [{}, 'NotStarted'],
    [{ readiness: 'ready', reachability: 'reachable' }, 'NotStarted'],
    [{ readiness: 'blocked', reachability: 'reachable' }, 'Blocked'],
    [{ execution: 'pending' }, 'Running'],
    [{ execution: 'queued' }, 'Running'],
    [{ execution: 'claimed' }, 'Running'],
    [{ execution: 'running' }, 'Running'],
    [{ execution: 'failed' }, 'Failed'],
    [{ execution: 'interrupted' }, 'Interrupted'],
    [{ execution: 'cancelled' }, 'Interrupted'],
    [{ execution: 'canceled' }, 'Interrupted'],
    [{ execution: 'succeeded', requires_approval: true }, 'Completed'],
    [{ execution: 'running', branch: 'pruned' }, 'Running'],
    [{ execution: 'failed', branch: 'bypassed' }, 'Failed'],
    [{ validity: 'stale', execution: 'succeeded' }, 'Unknown'],
    [{ execution: 'unrecognized' }, 'Unknown'],
  ])('explains %j as %s', (patch, expected) => {
    expect(workflowEmptyStateKey({ projection: { nodes: { step: { ...idle, ...patch } } } }, 'step'))
      .toBe(`chat.workflowEmpty${expected}`);
  });

  it('does not report another tab’s active execution as this step’s state', () => {
    const session = { projection: {
      current: ['running'],
      nodes: { running: { ...idle, execution: 'running' }, future: idle },
    } };
    expect(workflowEmptyStateKey(session, 'future')).toBe('chat.workflowEmptyNotStarted');
    expect(workflowEmptyStateKey(session, 'missing')).toBe('chat.workflowEmptyUnknown');
    expect(workflowEmptyStateKey(session)).toBe('chat.workflowEmptyUnknown');
    expect(workflowEmptyStateKey({}, 'future')).toBe('chat.workflowEmptyUnknown');
  });
});

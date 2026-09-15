import { describe, expect, it } from 'vitest';

import { resolvePendingApprovalStep } from './workflowApproval';

describe('resolvePendingApprovalStep', () => {
  it('finds the latest succeeded approval step when current_step_id was cleared', () => {
    expect(resolvePendingApprovalStep({
      status: 'waiting',
      current_step_id: '',
      steps: [
        {
          id: 'attempt-1', session_id: 'session-1', step_id: 'automatic', attempt: 1,
          task_id: 'task-1', status: 'succeeded', validity: 'effective',
          created_at: '2026-09-03T08:00:00Z', updated_at: '2026-09-03T08:01:00Z',
        },
        {
          id: 'attempt-2', session_id: 'session-1', step_id: 'review-images', attempt: 1,
          task_id: 'task-2', status: 'succeeded', validity: 'effective',
          created_at: '2026-09-03T08:02:00Z', updated_at: '2026-09-03T08:03:00Z',
        },
      ],
      projection: {
        nodes: {
          automatic: {
            requires_approval: false, execution: 'succeeded', validity: 'effective',
            reachability: 'reachable', readiness: 'not_applicable', branch: 'active',
          },
          'review-images': {
            requires_approval: true, execution: 'succeeded', validity: 'effective',
            reachability: 'reachable', readiness: 'not_applicable', branch: 'active',
          },
        },
      },
    }, 'waiting')).toBe('review-images');
  });

  it('does not reuse an older approval step when the latest step is automatic', () => {
    expect(resolvePendingApprovalStep({
      status: 'waiting',
      current_step_id: '',
      steps: [
        {
          id: 'attempt-1', session_id: 'session-1', step_id: 'review', attempt: 1,
          task_id: 'task-1', status: 'succeeded', validity: 'effective',
          created_at: '2026-09-03T08:00:00Z', updated_at: '2026-09-03T08:01:00Z',
        },
        {
          id: 'attempt-2', session_id: 'session-1', step_id: 'automatic', attempt: 1,
          task_id: 'task-2', status: 'succeeded', validity: 'effective',
          created_at: '2026-09-03T08:02:00Z', updated_at: '2026-09-03T08:03:00Z',
        },
      ],
      projection: {
        nodes: {
          review: {
            requires_approval: true, execution: 'succeeded', validity: 'effective',
            reachability: 'reachable', readiness: 'not_applicable', branch: 'active',
          },
          automatic: {
            requires_approval: false, execution: 'succeeded', validity: 'effective',
            reachability: 'reachable', readiness: 'not_applicable', branch: 'active',
          },
        },
      },
    }, 'waiting')).toBeUndefined();
  });
});

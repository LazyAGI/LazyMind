import { describe, expect, it } from 'vitest';
import type { WorkflowSessionStep } from '../../store/workflowPanel';
import { workflowNodeStatus, workflowTabStatus } from './workflowStepStatus';

describe('shared workflow step status', () => {
  it.each(['pruned', 'bypassed'] as const)('shows an unexecuted %s branch as skipped', branch => {
    const node = { execution: 'none', branch, validity: 'effective', requires_approval: false, reachability: 'unreachable', readiness: 'not_applicable' } as const;
    expect(workflowNodeStatus(node)).toBe('skipped');
    const session: Parameters<typeof workflowTabStatus>[1] = { current_step_id: 'slides', projection: { nodes: { prompts: node } } };
    expect(workflowTabStatus({ id: 'page_prompts', step_id: 'prompts' }, session)).toBe('skipped');
  });

  it.each(['running', 'succeeded', 'failed', 'interrupted', 'canceled'])('preserves a real %s execution over a branch label', execution => {
    expect(workflowNodeStatus({ execution, branch: 'pruned' })).toBe(execution);
  });

  it('does not infer skipping from tab order or from stale attempts and branches', () => {
    const session: Parameters<typeof workflowTabStatus>[1] = { current_step_id: 'slides', projection: { nodes: {
      prompts: { execution: 'none', branch: 'active', validity: 'effective', requires_approval: false, reachability: 'unreachable', readiness: 'not_applicable' },
    } } };
    expect(workflowTabStatus({ id: 'prompts' }, session)).toBe('pending');
    expect(workflowNodeStatus({ execution: 'none', branch: 'pruned', validity: 'stale' })).toBe('pending');
    expect(workflowNodeStatus()).toBe('pending');
  });

  it('uses the effective retry and the current producer for grouped tabs', () => {
    const attempts: Array<Pick<WorkflowSessionStep, 'step_id' | 'attempt' | 'status' | 'validity'>> = [
      { step_id: 'collect', attempt: 1, status: 'succeeded' },
      { step_id: 'research', attempt: 1, status: 'succeeded', validity: 'stale' },
      { step_id: 'research', attempt: 2, status: 'running' },
    ];
    const session: Parameters<typeof workflowTabStatus>[1] = { current_step_id: 'research', steps: attempts.map(attempt => ({
      ...attempt, id: `${attempt.step_id}-${attempt.attempt}`, task_id: `${attempt.step_id}-${attempt.attempt}`,
      session_id: 'session', created_at: '2026-11-06T00:00:00Z', updated_at: '2026-11-06T00:00:00Z',
    })) };
    expect(workflowTabStatus({ id: 'materials', status_step_ids: ['collect', 'research'] }, session)).toBe('running');
  });
});

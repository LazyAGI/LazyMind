import { expect, it } from 'vitest';
import { taskBelongsToWorkflow, workflowTaskDisplayStatus } from './workflowTaskStatus';
import type { WorkflowSessionStep } from '../store/workflowPanel';

it('classifies abnormal interruptions as failures and confirmed user stops as pauses', () => {
  expect(workflowTaskDisplayStatus('interrupted')).toBe('failed');
  expect(workflowTaskDisplayStatus('interrupted', 'failed')).toBe('failed');
  expect(workflowTaskDisplayStatus('interrupted', 'running')).toBe('failed');
  expect(workflowTaskDisplayStatus('interrupted', 'paused')).toBe('paused');
  expect(workflowTaskDisplayStatus('failed', 'paused')).toBe('failed');
  expect(workflowTaskDisplayStatus('paused', 'failed')).toBe('paused');
});

it('never applies the current pause to another workflow or an older retry', () => {
  const steps: WorkflowSessionStep[] = [1, 2].map(attempt => ({
    id: `attempt-${attempt}`, task_id: `task-${attempt}`, session_id: 'session', step_id: 'write',
    attempt, status: 'interrupted', validity: 'effective', created_at: '', updated_at: '',
  }));
  const session = { session_id: 'session', steps };
  expect(taskBelongsToWorkflow({ task_id: 'task-1', ordinary: { session_id: 'session' } }, session)).toBe(false);
  expect(taskBelongsToWorkflow({ task_id: 'task-2' }, session)).toBe(true);
  expect(taskBelongsToWorkflow({ task_id: 'other', ordinary: { session_id: 'other-session' } }, session)).toBe(false);
  steps[1].validity = 'stale';
  expect(taskBelongsToWorkflow({ task_id: 'task-2' }, session)).toBe(false);
});

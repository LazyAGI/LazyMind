import { describe, expect, it } from 'vitest';
import { reconcileWorkflowSessionStatus } from './workflowStatus';

describe('reconcileWorkflowSessionStatus', () => {
  it('changes a stale active session to waiting when the next step is ready', () => {
    expect(reconcileWorkflowSessionStatus('active', {
      current: [],
      ready: ['typed-artifact'],
      blocked: [],
    })).toBe('waiting');
  });

  it('keeps a session active while an attempt is current', () => {
    expect(reconcileWorkflowSessionStatus('active', {
      current: ['script-tool'],
      ready: [],
      blocked: [],
    })).toBe('active');
  });

  it('uses a completed projection when the persisted status is stale', () => {
    expect(reconcileWorkflowSessionStatus('active', {
      completed: true,
      current: [],
      ready: [],
      blocked: [],
    })).toBe('completed');
  });

  it.each(['completed', 'failed', 'stopped'] as const)(
    'does not regress a %s session when a running snapshot is replayed',
    (status) => {
      expect(reconcileWorkflowSessionStatus(status, {
        status: 'running',
        current: ['prompt'],
        ready: [],
        blocked: [],
      })).toBe(status);
    },
  );

  it('allows a waiting session to become active when execution resumes', () => {
    expect(reconcileWorkflowSessionStatus('waiting', {
      status: 'running',
      current: ['script-tool'],
      ready: [],
      blocked: [],
    })).toBe('active');
  });
});

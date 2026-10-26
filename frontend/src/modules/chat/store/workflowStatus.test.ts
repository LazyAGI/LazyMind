import { describe, expect, it } from 'vitest';
import { reconcileWorkflowSessionStatus } from './workflowStatus';

describe('reconcileWorkflowSessionStatus', () => {
  it('does not infer a completed pause from an interrupted attempt', () => {
    expect(reconcileWorkflowSessionStatus('active', {
      status: 'pausing', current: ['edit'],
      nodes: { edit: { execution: 'interrupted' } },
    })).toBe('pausing');
  });
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
    'reopens a %s session when the store supplies a newer running snapshot',
    (status) => {
      expect(reconcileWorkflowSessionStatus(status, {
        status: 'running',
        current: ['prompt'],
        ready: [],
        blocked: [],
      })).toBe('active');
    },
  );

  it('uses actual attempt execution when the projection has no status field', () => {
    expect(reconcileWorkflowSessionStatus('waiting', {
      current: ['edit'], nodes: { edit: { execution: 'running' } },
    })).toBe('active');
    expect(reconcileWorkflowSessionStatus('active', {
      current: ['edit'], nodes: { edit: { execution: 'failed' } },
    })).toBe('failed');
    expect(reconcileWorkflowSessionStatus('active', {
      current: ['edit'], nodes: { edit: { execution: 'interrupted' } },
    })).toBe('waiting');
    expect(reconcileWorkflowSessionStatus('failed', { completed: true })).toBe('completed');
  });

  it('allows a waiting session to become active when execution resumes', () => {
    expect(reconcileWorkflowSessionStatus('waiting', {
      status: 'running',
      current: ['script-tool'],
      ready: [],
      blocked: [],
    })).toBe('active');
  });
});

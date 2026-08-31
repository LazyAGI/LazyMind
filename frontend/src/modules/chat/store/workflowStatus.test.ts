import { describe, expect, it } from 'vitest';
import { isWorkflowReadyToStart, reconcileWorkflowSessionStatus } from './workflowStatus';

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

describe('isWorkflowReadyToStart', () => {
  it('distinguishes an undispatched ready step from an approval wait', () => {
    expect(isWorkflowReadyToStart('waiting', {
      current: [],
      ready: ['prepare'],
      nodes: { prepare: { requires_approval: false } },
    })).toBe(true);
  });

  it('keeps a ready step that requires approval labeled as waiting', () => {
    expect(isWorkflowReadyToStart('waiting', {
      current: [],
      ready: ['prepare'],
      nodes: { prepare: { requires_approval: true } },
    })).toBe(false);
  });

  it('does not guess when approval metadata is missing', () => {
    expect(isWorkflowReadyToStart('waiting', {
      current: [],
      ready: ['prepare'],
    })).toBe(false);
  });

  it('does not relabel a wait after a step has executed', () => {
    expect(isWorkflowReadyToStart('waiting', {
      current: [],
      ready: ['outline'],
      nodes: { outline: { requires_approval: false } },
    }, 1)).toBe(false);
  });
});

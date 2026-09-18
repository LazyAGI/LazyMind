import { describe, expect, it } from 'vitest';

import {
  emptyWorkflowProjection,
  markWorkflowResyncRequired,
  reduceWorkflowEvent,
} from '../../frontend/src/modules/chat/store/workflowProjection.ts';

const event = (overrides = {}) => ({
  contract_version: 'workflow.v1',
  cursor: 1,
  type: 'workflow.snapshot',
  state_version: 1,
  entity_id: 'session-1',
  payload: { projection: { status: 'running', nodes: {} } },
  ...overrides,
});

describe('Workflow Event Stream projection reducer', () => {
  it('rebuilds the same projection from snapshot and patches', () => {
    let state = reduceWorkflowEvent(emptyWorkflowProjection(), event());
    state = reduceWorkflowEvent(state, event({
      cursor: 2,
      type: 'workflow.patch',
      state_version: 2,
      payload: { projection: { status: 'waiting', nodes: { draft: { readiness: 'ready' } } } },
    }));

    expect(state.projection).toEqual({
      status: 'waiting',
      nodes: { draft: { readiness: 'ready' } },
    });
    expect(state.cursor).toBe(2);
    expect(state.stateVersion).toBe(2);
  });

  it('ignores duplicate replay and accepts gaps in database-wide event cursors', () => {
    const snapshot = event({ cursor: 10, state_version: 4 });
    const initial = reduceWorkflowEvent(emptyWorkflowProjection(), snapshot);
    expect(reduceWorkflowEvent(initial, event({
      cursor: 10, type: 'workflow.patch', state_version: 4, payload: {},
    }))).toBe(initial);

    const next = reduceWorkflowEvent(initial, event({
      cursor: 12, type: 'workflow.patch', state_version: 5,
      payload: { projection: { status: 'waiting' } },
    }));
    expect(next).toMatchObject({ cursor: 12, stateVersion: 5, resyncRequired: false,
      projection: { status: 'waiting' } });
  });

  it('recovers an expired cursor with a fresh authoritative snapshot', () => {
    const initial = reduceWorkflowEvent(emptyWorkflowProjection(), event({ cursor: 12, state_version: 5 }));
    const expired = markWorkflowResyncRequired(initial);
    expect(expired.resyncRequired).toBe(true);
    const recovered = reduceWorkflowEvent(expired, event({
      cursor: 20,
      state_version: 9,
      payload: { projection: { status: 'completed' } },
    }));
    expect(recovered).toMatchObject({ cursor: 20, stateVersion: 9, resyncRequired: false,
      projection: { status: 'completed' } });
  });

  it('accepts a later state version without requiring one event per version', () => {
    const initial = reduceWorkflowEvent(emptyWorkflowProjection(), event());
    const next = reduceWorkflowEvent(initial, event({ cursor: 2, type: 'attempt.patch', state_version: 3,
      entity_id: 'attempt-1', payload: { status: 'succeeded' } }));
    expect(next).toMatchObject({ cursor: 2, stateVersion: 3, resyncRequired: false,
      attempts: { 'attempt-1': { status: 'succeeded' } } });
  });

  it('consumes stale event cursors without letting an old status or snapshot undo completion', () => {
    const completed = reduceWorkflowEvent(emptyWorkflowProjection(), event({ cursor: 20, state_version: 9,
      payload: { projection: { status: 'completed', completed: true } } }));
    const next = reduceWorkflowEvent(completed, event({ cursor: 22, type: 'workflow.patch', state_version: 5,
      payload: { projection: { status: 'waiting' } } }));
    expect(next).toMatchObject({ cursor: 22, stateVersion: 9, resyncRequired: false,
      projection: { status: 'completed', completed: true } });
    expect(reduceWorkflowEvent(next, event({ cursor: 19, state_version: 5 }))).toBe(next);
  });

  it('accepts legacy version-zero attempt events without rolling back the workflow version', () => {
    const initial = reduceWorkflowEvent(emptyWorkflowProjection(), event({ cursor: 20, state_version: 9 }));
    const next = reduceWorkflowEvent(initial, event({ cursor: 23, state_version: 0, type: 'attempt.patch',
      entity_id: 'attempt-1', payload: { status: 'succeeded' } }));
    expect(next).toMatchObject({ cursor: 23, stateVersion: 9, resyncRequired: false,
      attempts: { 'attempt-1': { status: 'succeeded' } } });
    expect(next.projection).toEqual(initial.projection);
  });

  it('deep-merges high-frequency progress without increasing state_version', () => {
    let state = reduceWorkflowEvent(emptyWorkflowProjection(), event());
    state = reduceWorkflowEvent(state, event({
      cursor: 2, type: 'attempt.progress', entity_id: 'attempt-1', state_version: 1,
      payload: { completed: 2, detail: { phase: 'draft' } },
    }));
    state = reduceWorkflowEvent(state, event({
      cursor: 3, type: 'attempt.progress', entity_id: 'attempt-1', state_version: 1,
      payload: { total: 5, detail: { message: 'writing' } },
    }));
    expect(state.stateVersion).toBe(1);
    expect(state.progress['attempt-1']).toEqual({
      completed: 2, total: 5, detail: { phase: 'draft', message: 'writing' },
    });
  });

  it('rejects unknown major versions and unknown breaking event types', () => {
    const unsupported = reduceWorkflowEvent(emptyWorkflowProjection(), event({ contract_version: 'workflow.v2' }));
    expect(unsupported.errorCode).toBe('UNSUPPORTED_CONTRACT_VERSION');

    const initial = reduceWorkflowEvent(emptyWorkflowProjection(), event());
    const unknown = reduceWorkflowEvent(initial, event({ cursor: 2, type: 'workflow.deleted', state_version: 2 }));
    expect(unknown.errorCode).toBe('UNKNOWN_BREAKING_EVENT');
  });
});

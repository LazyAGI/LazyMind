import { describe, expect, it } from 'vitest';

import {
  emptyWorkflowProjection,
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

  it('accepts cursor replay, rejects a cursor gap, and recovers with an expired-cursor snapshot', () => {
    const snapshot = event({ cursor: 10, state_version: 4 });
    const initial = reduceWorkflowEvent(emptyWorkflowProjection(), snapshot);
    expect(reduceWorkflowEvent(initial, event({
      cursor: 10, type: 'workflow.patch', state_version: 4, payload: {},
    }))).toBe(initial);

    const gap = reduceWorkflowEvent(initial, event({ cursor: 12, type: 'workflow.patch', state_version: 5 }));
    expect(gap).toMatchObject({ resyncRequired: true, errorCode: 'CURSOR_GAP' });

    const recovered = reduceWorkflowEvent(gap, event({
      cursor: 20,
      state_version: 9,
      payload: { projection: { status: 'completed' } },
    }));
    expect(recovered).toMatchObject({ cursor: 20, stateVersion: 9, resyncRequired: false });
  });

  it('requests resync for a durable state_version gap', () => {
    const initial = reduceWorkflowEvent(emptyWorkflowProjection(), event());
    const gap = reduceWorkflowEvent(initial, event({ cursor: 2, type: 'attempt.patch', state_version: 3 }));
    expect(gap).toMatchObject({ resyncRequired: true, errorCode: 'STATE_VERSION_GAP' });
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

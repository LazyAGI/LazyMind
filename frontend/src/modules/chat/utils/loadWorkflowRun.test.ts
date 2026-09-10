import { beforeEach, describe, expect, it, vi } from 'vitest';

import {
  loadWorkflowRunSnapshot,
  panelCurrentStep,
  resetWorkflowRunControlCache,
} from './loadWorkflowRun';
import type { WorkflowControlView } from '@/modules/chat/utils/workflowControl';
import type { WorkflowSession } from '@/modules/chat/store/workflowPanel';

vi.mock('@/modules/chat/utils/workflowEventStream', () => ({
  subscribeWorkflowEventStream: vi.fn(() => ({ close() {}, resync() {} })),
}));

const hostedControl = {
  protocol: 'workflow.control.v1',
  session_id: 'hosted',
  state_version: 1,
  continuation: 'continue',
  reviews: [],
  binding: { bound: true, generation: 1 },
  active_executions: 1,
  native_execution_ids: [],
  active_execution_ids: [],
  available_actions: [],
  admission: { can_begin: true },
  delivery: null,
} as WorkflowControlView;

describe('panelCurrentStep', () => {
  it('follows the running projection node', () => {
    expect(panelCurrentStep('analyze_subject', { current: ['generate_image'] }, [
      { step_id: 'analyze_subject', attempt: 1, status: 'succeeded', validity: 'effective' },
    ] as WorkflowSession['steps'])).toBe('generate_image');
  });

  it('stays on the latest non-stale attempt when nothing is running', () => {
    expect(panelCurrentStep('analyze_subject', { current: [] }, [
      { step_id: 'analyze_subject', attempt: 1, status: 'succeeded', validity: 'effective' },
      { step_id: 'collect_materials', attempt: 1, status: 'succeeded', validity: 'effective' },
      { step_id: '__end__', attempt: 1, status: 'succeeded', validity: 'effective' },
    ] as WorkflowSession['steps'])).toBe('collect_materials');
  });

  it('falls back to the recorded column when there is no attempt history', () => {
    expect(panelCurrentStep('analyze_subject', { current: ['__end__'] }, [])).toBe('analyze_subject');
  });
});

describe('loadWorkflowRunSnapshot', () => {
  beforeEach(() => {
    resetWorkflowRunControlCache();
  });

  it('uses the control snapshot when the run is hosted', async () => {
    const run = {
      session_id: 'hosted',
      conversation_id: 'conv',
      workflow_id: 'image-workflow',
      workflow_mode: 'auto',
      status: 'waiting',
      current_step_id: 'stale-column',
      created_at: '',
      updated_at: '',
      steps: [{ step_id: 'analyze_subject', attempt: 1, status: 'running', validity: 'effective' }],
    } as WorkflowSession;
    const snapshot = await loadWorkflowRunSnapshot('hosted', {
      getControl: async () => ({
        data: {
          data: {
            session: run,
            control: hostedControl,
            projection: { current: ['analyze_subject'] },
          },
        },
      }),
      getSession: async () => { throw new Error('session fallback should not run'); },
      getProjection: async () => { throw new Error('projection fallback should not run'); },
    });
    expect(snapshot.session.current_step_id).toBe('analyze_subject');
    expect(snapshot.session.status).toBe('active');
    expect(snapshot.control?.protocol).toBe('workflow.control.v1');
  });

  it('falls back to session + projection for native runs', async () => {
    const error = Object.assign(new Error('legacy'), {
      response: { status: 409, data: { error: { code: 'CONTROL_PROTOCOL_REQUIRED' } } },
    });
    const snapshot = await loadWorkflowRunSnapshot('native', {
      getControl: async () => { throw error; },
      getSession: async () => ({
        data: {
          data: {
            session: {
              session_id: 'native',
              conversation_id: 'conv',
              workflow_id: 'image-workflow',
              workflow_mode: 'auto',
              status: 'waiting',
              current_step_id: '',
              created_at: '',
              updated_at: '',
              steps: [{ step_id: 'optimize_prompt', attempt: 1, status: 'succeeded', validity: 'effective' }],
            } as WorkflowSession,
          },
        },
      }),
      getProjection: async () => ({ data: { data: { projection: { current: [] } } } }),
    });
    expect(snapshot.session.current_step_id).toBe('optimize_prompt');
    expect(snapshot.control).toBeUndefined();
  });
});

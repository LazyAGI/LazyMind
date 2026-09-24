import { describe, expect, it } from 'vitest';

import type { TabDef, WorkflowSession } from '@/modules/chat/store/workflowPanel';
import { resolveCompletedContinueStep, resolveWorkflowContinueAction } from './workflowContinue';

const outlineTab: TabDef = {
  id: 'outline',
  step_id: 'outline',
  label: 'Outline',
  slots: [],
  completed_continue_step: 'write_document',
};

describe('resolveCompletedContinueStep', () => {
  it('uses the workflow-declared completed continuation', () => {
    expect(resolveCompletedContinueStep({
      status: 'completed',
    }, outlineTab)).toBe('write_document');
  });

  it('does not invent a continuation for undeclared or active tabs', () => {
    expect(resolveCompletedContinueStep({
      status: 'completed',
    }, { ...outlineTab, completed_continue_step: undefined })).toBeUndefined();
    expect(resolveCompletedContinueStep({ status: 'active' }, outlineTab)).toBeUndefined();
  });
});

function checkpoint(requiresApproval: boolean, execution = 'succeeded'): WorkflowSession {
  return {
    session_id: 's', conversation_id: 'c', workflow_id: 'image', workflow_mode: 'auto',
    status: 'waiting', current_step_id: 'analyze', created_at: '', updated_at: '',
    steps: [{ id: 'a', task_id: 'a', session_id: 's', step_id: 'analyze', attempt: 1,
      status: execution, validity: 'effective', created_at: '', updated_at: '' }],
    projection: { current: execution === 'succeeded' ? [] : ['analyze'], ready: ['collect'], nodes: {
      analyze: { requires_approval: requiresApproval, execution, validity: 'effective',
        reachability: 'reachable', readiness: 'not_applicable', branch: 'active' },
      collect: { requires_approval: true, execution: 'none', validity: 'effective',
        reachability: 'reachable', readiness: 'ready', branch: 'active' },
    } },
  };
}

describe('workflow continuation checkpoints', () => {
  it('hides continue between automatic steps even when a later step requires approval', () => {
    const session = checkpoint(false);
    expect(resolveWorkflowContinueAction(session, 'waiting')).toBeUndefined();
    session.projection!.ready = [];
    expect(resolveWorkflowContinueAction(session, 'waiting')).toBeUndefined();
  });

  it('only offers approval after the approval step succeeds, and hides it after skipping approval', () => {
    const session = checkpoint(true);
    expect(resolveWorkflowContinueAction(session, 'waiting')).toEqual({ kind: 'approval', stepId: 'analyze' });
    session.projection!.nodes!.analyze.requires_approval = false;
    expect(resolveWorkflowContinueAction(session, 'waiting')).toBeUndefined();
  });

  it.each(['pending', 'queued', 'claimed', 'running'])('hides stale approval when the next step is %s', (execution) => {
    const session = checkpoint(true);
    session.projection!.nodes!.collect.execution = execution;
    expect(resolveWorkflowContinueAction(session, 'waiting')).toBeUndefined();
  });

  it('does not offer continue before initial dispatch, while running, or after failure', () => {
    const initial = checkpoint(false, 'none');
    initial.steps = [];
    expect(resolveWorkflowContinueAction(initial, 'waiting')).toBeUndefined();
    expect(resolveWorkflowContinueAction(checkpoint(true), 'active')).toBeUndefined();
    expect(resolveWorkflowContinueAction(checkpoint(true, 'failed'), 'waiting')).toBeUndefined();
  });

  it('preserves explicit recovery after interruption and declared completed follow-on actions', () => {
    expect(resolveWorkflowContinueAction(checkpoint(false, 'interrupted'), 'waiting'))
      .toEqual({ kind: 'resume', stepId: 'analyze' });
    const completed = { ...checkpoint(false), status: 'completed' as const, projection: { completed: true } };
    expect(resolveWorkflowContinueAction(completed, 'completed')).toBeUndefined();
    expect(resolveWorkflowContinueAction(completed, 'completed', outlineTab))
      .toEqual({ kind: 'completed', stepId: 'write_document' });
  });

  it('does not reuse a stale current step pointer after a newer automatic attempt succeeds', () => {
    const session = checkpoint(true);
    session.steps!.push({ ...session.steps![0], id: 'b', task_id: 'b', step_id: 'collect', attempt: 2 });
    session.projection!.nodes!.collect.execution = 'succeeded';
    session.projection!.nodes!.collect.requires_approval = false;
    expect(resolveWorkflowContinueAction(session, 'waiting')).toBeUndefined();
  });
});

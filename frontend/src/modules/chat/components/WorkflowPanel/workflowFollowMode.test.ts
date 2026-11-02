import { describe, expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { load } from 'js-yaml';
import { hydrateWorkflowUI, type TabDef, type WorkflowSession, type WorkflowSessionStep } from '@/modules/chat/store/workflowPanel';
import { resolveWorkflowActiveTabIndex, resolveWorkflowRealTabIndex, workflowTabMatchesStep } from './workflowFollowMode';

const tabs: TabDef[] = [
  { id: 'plan', label: 'Plan', slots: [] },
  { id: 'result', step_id: 'write', status_step_ids: ['write', 'write_short'], label: 'Result', slots: [] },
];
const step = (id: string, status: string, minute: number): WorkflowSessionStep => ({
  id, session_id: 'run', step_id: id, attempt: 1, task_id: '', status, validity: 'effective',
  created_at: `2026-10-26T08:0${minute}:00Z`, updated_at: `2026-10-26T08:0${minute}:00Z`,
});
const node = (execution: string, requires_approval = false) => ({
  execution, requires_approval, validity: 'effective', reachability: 'reachable', readiness: 'not_applicable', branch: 'active',
});
const session = (patch: Partial<WorkflowSession> = {}): WorkflowSession => ({
  session_id: 'run', conversation_id: 'chat', workflow_id: 'workflow', workflow_mode: 'auto',
  status: 'active', current_step_id: '', created_at: '', updated_at: '', ...patch,
});

describe('workflow runtime position', () => {
  it('tracks either producer of a grouped result and preserves the stable tab id after insertion', () => {
    expect(resolveWorkflowRealTabIndex(session({ current_step_id: 'write_short' }), tabs)).toBe(1);
    expect(resolveWorkflowActiveTabIndex([tabs[1], tabs[0]], 'result')).toBe(0);
  });
  it('prefers current failure over running peers', () => {
    expect(resolveWorkflowRealTabIndex(session({ projection: {
      current: ['plan', 'write'], nodes: { plan: node('running'), write: node('failed') },
    } }), tabs, 'plan')).toBe(1);
  });
  it('keeps the same running branch when parallel projection order changes', () => {
    expect(resolveWorkflowRealTabIndex(session({ projection: {
      current: ['write', 'plan'], nodes: { plan: node('running'), write: node('queued') },
    } }), tabs, 'plan')).toBe(0);
  });
  it('locates a pending approval after the mutable current pointer has cleared', () => {
    expect(resolveWorkflowRealTabIndex(session({ status: 'waiting', steps: [step('plan', 'succeeded', 2)],
      projection: { nodes: { plan: node('succeeded', true) } },
    }), tabs)).toBe(0);
  });
  it('uses the last successful output on completion, not an obsolete pointer or stale newer attempt', () => {
    expect(resolveWorkflowRealTabIndex(session({ status: 'completed', current_step_id: 'plan', steps: [
      step('plan', 'succeeded', 1), step('write_short', 'succeeded', 2),
      { ...step('plan', 'succeeded', 3), validity: 'stale' },
    ] }), tabs)).toBe(1);
  });
  it('keeps a valid current pointer as a tie-break when legacy timestamps are identical', () => {
    expect(resolveWorkflowRealTabIndex(session({ status: 'completed', current_step_id: 'write',
      steps: [step('plan', 'succeeded', 1), step('write', 'succeeded', 1)],
    }), tabs)).toBe(1);
  });
  it('locates the actual final result even when it is the first tab and the current pointer is empty', () => {
    expect(resolveWorkflowRealTabIndex(session({ status: 'completed', steps: [
      step('plan', 'succeeded', 1), step('write_short', 'succeeded', 2),
      { ...step('plan', 'succeeded', 3), validity: 'stale' },
    ] }), [tabs[1], tabs[0]])).toBe(0);
  });
  it.each(['pending', 'running'])('keeps a resumed %s producer visible before the session status patch arrives', status => {
    expect(resolveWorkflowRealTabIndex(session({ status: 'completed', current_step_id: 'write', steps: [
      step('plan', 'succeeded', 1), step('write', status, 2),
    ] }), tabs)).toBe(1);
  });
  it('locates the effective failed attempt and ignores obsolete failed branches', () => {
    expect(resolveWorkflowRealTabIndex(session({ status: 'failed', current_step_id: 'write',
      steps: [step('plan', 'failed', 1), step('write', 'failed', 2)],
      projection: { stale: ['write'] },
    }), tabs)).toBe(0);
  });
  it('does not point at pruned, bypassed or unexecuted nodes', () => {
    expect(resolveWorkflowRealTabIndex(session({ current_step_id: 'plan', projection: {
      pruned: ['write'], bypassed: ['write_short'], nodes: { plan: node('none') },
    } }), tabs)).toBe(-1);
  });
  it('uses a newly selected output when its step update has not arrived', () => {
    expect(resolveWorkflowRealTabIndex(session({ slots: [
      { slot_id: 'result', step_id: 'write_short', selected: true, revision: 2, created_at: '2026-10-26T08:00:00Z' },
    ] as WorkflowSession['slots'] }), tabs)).toBe(1);
  });
  it('has no fabricated runtime position for an empty or unmapped session', () => {
    expect(resolveWorkflowRealTabIndex(session({ current_step_id: 'missing' }), tabs)).toBe(-1);
    expect(resolveWorkflowRealTabIndex(session(), [])).toBe(-1);
  });
  it.each(['ppt-workflow', 'writer-workflow', 'image-workflow', 'image-workflow-v2'])(
    'enables following and maps every execution step in %s', id => {
      const spec = load(readFileSync(resolve(process.cwd(), `../workflows/${id}/workflow.yaml`), 'utf8')) as { steps: { id: string }[] };
      const ui = hydrateWorkflowUI(spec);
      expect(ui.follow_mode?.default).toBe('following');
      for (const { id: stepId } of spec.steps) {
        expect(ui.tabs?.some(tab => workflowTabMatchesStep(tab, stepId)), stepId).toBe(true);
      }
    },
  );
});

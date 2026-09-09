import { describe, expect, it } from 'vitest';

import type {
  SlotRevision,
  TabDef,
  WorkflowSessionStep,
} from '@/modules/chat/store/workflowPanel';
import {
  resolveWorkflowControlScope,
  resolveWorkflowTabStepId,
  workflowSlotMatchesTabScope,
} from './workflowTabScope';

const steps = [
  { step_id: 'generate_image' },
  { step_id: 'enhance_image' },
] as WorkflowSessionStep[];

const firstFrame = {
  slot: 'generated_first_frame',
  step_id: 'generate_image',
  selected: true,
} as SlotRevision;

describe('workflow tab artifact scope', () => {
  const attachments = { id: 'attachments', label: 'Attachments', slots: [{id: 'rewritten_file'}] } as TabDef;

  it('targets the sole failed step for recovery before any grouped artifact exists', () => {
    expect(resolveWorkflowControlScope(attachments, {
      steps, slots: [], current_step_id: '', projection: { current: ['generate_image'] },
    })).toEqual({ stepId: 'generate_image', stepIds: [] });
  });

  it('limits grouped approval to visible producers even when another step is current', () => {
    expect(resolveWorkflowControlScope(attachments, {
      steps, current_step_id: '', projection: { current: ['verify'] },
      slots: [{...firstFrame, slot: 'rewritten_file', step_id: 'rewrite'}],
    })).toEqual({ stepId: 'verify', stepIds: ['rewrite'] });
  });

  it('does not guess a recovery target when several steps are current', () => {
    expect(resolveWorkflowControlScope(attachments, {
      steps, slots: [], current_step_id: '', projection: { current: ['one', 'two'] },
    }).stepId).toBe('');
  });

  it('keeps a normal step tab scoped to its producer', () => {
    const tab = {
      id: 'enhance_image',
      label: 'Enhance',
      slots: [],
    } as TabDef;

    expect(resolveWorkflowTabStepId(tab, steps)).toBe('enhance_image');
    expect(workflowSlotMatchesTabScope(tab, steps, firstFrame)).toBe(false);
  });

  it('keeps unchanged composite items visible after their revision becomes stale', () => {
    const tab = {
      id: 'page_prompts',
      step_id: 'plan_page_prompts',
      slot_scope: 'step',
      label: 'Page Prompts',
      slots: [],
    } as TabDef;
    const unchangedPage = {
      slot: 'slide_outline',
      step_id: 'plan_page_prompts',
      selected: false,
      validity: 'stale',
      list_index: 0,
      sort_order: 1,
    } as SlotRevision;

    expect(resolveWorkflowTabStepId(tab, steps)).toBe('plan_page_prompts');
    expect(workflowSlotMatchesTabScope(tab, steps, unchangedPage)).toBe(true);
  });

  it('lets a selected-scope composite join generate inputs with enhance outputs', () => {
    const tab = {
      id: 'enhance_image',
      slot_scope: 'selected',
      label: 'Enhance',
      slots: [],
    } as TabDef;

    expect(resolveWorkflowTabStepId(tab, steps)).toBeUndefined();
    expect(workflowSlotMatchesTabScope(tab, steps, firstFrame)).toBe(true);
    expect(workflowSlotMatchesTabScope(tab, steps, {
      ...firstFrame,
      selected: false,
    })).toBe(false);
  });
});

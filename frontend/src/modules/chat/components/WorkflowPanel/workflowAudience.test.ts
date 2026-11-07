import { describe, expect, it } from 'vitest';
import { hydrateWorkflowUI } from '../../store/workflowPanel';
import { hiddenUserSlots, workflowTabsForAudience, workflowUIForAudience } from './workflowAudience';

describe('workflow audience visibility', () => {
  const spec = {
    slots: [{ id: 'prompt', user_visible: false }, { id: 'slide', user_visible: true }, { id: 'notes' }],
    ui: { tabs: [
      { id: 'internal', slots: [{ id: 'prompt' }] },
      { id: 'preview', slots: [{ id: 'prompt' }, { id: 'slide' }, { id: 'notes' }],
        composite_layout: { direction: 'row', children: [{ slot: 'slide' }, { tabs: ['prompt', 'notes'] }] },
        actions: [{ id: 'export', inputs: { private_source: 'prompt', pages: 'slide' } }],
      },
    ] },
  };

  it('hides internal slots and empty tabs without modifying export inputs or developer UI', () => {
    const original = hydrateWorkflowUI(spec);
    const ordinary = workflowUIForAudience(original, false);
    expect(ordinary.tabs?.map(tab => tab.id)).toEqual(['preview']);
    expect(ordinary.tabs?.[0].slots.map(slot => slot.id)).toEqual(['slide', 'notes']);
    expect(ordinary.tabs?.[0].composite_layout).toEqual({ direction: 'row', children: [{ slot: 'slide' }, { tabs: ['notes'] }] });
    expect(ordinary.tabs?.[0].actions).toEqual(original.tabs?.[1].actions);
    expect(workflowUIForAudience(original, true)).toBe(original);
    expect(original.tabs?.[0].slots).toHaveLength(1);
  });

  it('retains root policy for automatic grids and prunes legacy layouts and process sections', () => {
    expect(hiddenUserSlots(hydrateWorkflowUI({ slots: spec.slots }))).toEqual(new Set(['prompt']));
    const ui = hydrateWorkflowUI({ ...spec, ui: { tabs: [{
      id: 'preview', slots: [{ id: 'slide' }, { id: 'prompt' }],
      composite_layout: ['prompt', { slot: { tabs: ['slide', 'prompt'] } }],
      generation_process: { result_slot: 'slide', slots: ['prompt'] },
    }] } });
    const tab = workflowUIForAudience(ui, false).tabs![0];
    expect(tab.composite_layout).toEqual([{ slot: { tabs: ['slide'] } }]);
    expect(tab.generation_process?.slots).toEqual([]);
  });

  it('preserves legacy layouts without declarations', () => {
    const ui = hydrateWorkflowUI({ ui: { tabs: [{ id: 'legacy', slots: [{ id: 'document' }] }] } });
    expect(workflowUIForAudience(ui, false)).toBe(ui);
  });

  it('combines audience and runtime tab visibility without using attempt count as the step list', () => {
    const ui = hydrateWorkflowUI({ ...spec, ui: { tabs: [
      { id: 'analysis', slots: [{ id: 'notes' }] },
      { id: 'prompts', slots: [{ id: 'prompt' }] },
      { id: 'materials', step_id: 'collect', hide_when_material: 'skip_collection', slots: [{ id: 'notes' }] },
      { id: 'preview', slots: [{ id: 'slide' }] },
    ] } });
    const session = { slots: [{ slot: 'skip_collection', selected: true }], projection: { past: ['collect'] } } as Parameters<typeof workflowTabsForAudience>[1];
    expect(workflowTabsForAudience(ui, session, true).map(tab => tab.id)).toEqual(['analysis', 'prompts', 'materials', 'preview']);
    expect(workflowTabsForAudience(ui, session, false).map(tab => tab.id)).toEqual(['analysis', 'materials', 'preview']);
    expect(workflowTabsForAudience(ui, { ...session, projection: { past: [] } }, true).map(tab => tab.id)).toEqual(['analysis', 'prompts', 'preview']);
  });
});

it('shares opt-in visibility while retaining completed, current and material-backed steps', () => {
  const ui = hydrateWorkflowUI({ ui: { hide_skipped_steps: true, hide_unstarted_steps: true,
    tabs: ['done', 'running', 'skipped', 'future', 'material'].map(id => ({ id, slots: [{ id }] })) } });
  const session = { workflow_mode: 'dynamic', slots: [{ slot: 'material', selected: true }],
    projection: { past: ['done'], current: ['running'], pruned: ['skipped'] } } as Parameters<typeof workflowTabsForAudience>[1];
  expect(workflowTabsForAudience(ui, session, true).map(tab => tab.id)).toEqual(['done', 'running', 'material']);
  expect(workflowTabsForAudience(ui, { ...session, workflow_mode: 'auto' }, true).map(tab => tab.id)).toEqual(['done', 'running', 'future', 'material']);
  expect(workflowTabsForAudience({ ...ui, hide_skipped_steps: false, hide_unstarted_steps: false }, session, true)).toHaveLength(5);
});

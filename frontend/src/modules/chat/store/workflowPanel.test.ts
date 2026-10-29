import { describe, expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { load } from 'js-yaml';

import {
  filterWorkflowTabs,
  hydrateWorkflowUI,
  workflowTabAllowsDownload,
  type SlotRevision,
} from './workflowPanel';

describe('hydrateWorkflowUI', () => {
  it('loads the academic workflow process-material rule from its package configuration', () => {
    const spec = load(readFileSync(resolve(process.cwd(), '../workflows/academic_research_pipeline/workflow.yaml'), 'utf8'));
    const ui = hydrateWorkflowUI(spec);
    const tab = ui.tabs?.find(tab => tab.id === 'draft');
    expect(tab?.generation_process).toEqual({
      result_slot: 'draft_document', slots: ['chapter_drafts', 'effective_outline_check_report'],
    });
    expect(tab?.slots.find(slot => slot.id === 'draft_document')?.widget?.readOnly).toBe(false);
    for (const id of tab!.generation_process!.slots) {
      expect(tab?.slots.find(slot => slot.id === id)?.widget?.readOnly).toBe(true);
    }
  });

  it('hydrates tab slot references with root slot list metadata', () => {
    const ui = hydrateWorkflowUI({
      slots: [
        {
          id: 'material_images',
          label: 'Reference Materials',
          type: 'image',
          cardinality: 'list',
          ordered: true,
        },
      ],
      ui: {
        slots: {
          material_images: { widgetType: 'image-grid', maxHeight: 320 },
        },
        tabs: [{
          id: 'materials',
          label: 'Materials',
          layout: 'grid',
          slots: [{ id: 'material_images', label: '素材图片' }],
        }],
      },
    });

    expect(ui.tabs?.[0].slots[0]).toEqual({
      id: 'material_images',
      label: '素材图片',
      type: 'image',
      cardinality: 'list',
      ordered: true,
      widget: { widgetType: 'image-grid', maxHeight: 320 },
    });
  });

  it('keeps a standalone UI payload usable', () => {
    const ui = { tabs: [{ id: 'result', label: 'Result', slots: [] }] };
    expect(hydrateWorkflowUI({ ui })).toBe(ui);
  });

  it('preserves declarative tab actions while hydrating slots', () => {
    const action = {
      id: 'export_deck',
      type: 'export' as const,
      provider: 'html-presentation',
      inputs: { pages: 'deck_pages' },
      formats: ['pdf'],
      alignment: 'sort_order' as const,
    };
    const ui = hydrateWorkflowUI({
      slots: [{ id: 'deck_pages', type: 'text', cardinality: 'list', ordered: true }],
      ui: {
        slots: { deck_pages: { widgetType: 'html-slide' } },
        tabs: [{ id: 'deck', slots: [{ id: 'deck_pages' }], actions: [action] }],
      },
    });

    expect(ui.tabs?.[0].actions).toEqual([action]);
    expect(ui.tabs?.[0].slots[0].widget?.widgetType).toBe('html-slide');
  });
});

describe('filterWorkflowTabs', () => {
  it('quick PPT hides executed intermediate tabs without removing their artifacts or final output', () => {
    const spec = load(readFileSync(resolve(process.cwd(), '../workflows/ppt-workflow/workflow.yaml'), 'utf8'));
    const ui = hydrateWorkflowUI(spec);
    const slots = [
      { slot: 'compact_presentation', selected: true },
      { slot: 'ppt_capability_requirements', selected: true },
      { slot: 'deck_outline', selected: true },
    ] as SlotRevision[];
    const runtime = { projection: { past: ['analyze_requirements', 'collect_materials', 'build_outline', 'plan_page_prompts'] } };
    expect(filterWorkflowTabs(ui.tabs, slots, ui.tab_visibility_ready_material, runtime).map(tab => tab.id))
      .toEqual(['composite_preview']);
    expect(filterWorkflowTabs(ui.tabs, slots.slice(1), ui.tab_visibility_ready_material, runtime).map(tab => tab.id))
      .toContain('page_prompts');
    expect(slots.some(slot => slot.slot === 'deck_outline')).toBe(true);
  });

  it('shows executed collection or academic analysis in the shared materials tab', () => {
    const tabs = [{ id: 'materials', label: 'Materials', step_id: 'collect_materials',
      status_step_ids: ['collect_materials', 'analyze_academic_sources'], slots: [],
      hide_when_material: 'skip_material_collection' }];
    const slots = [{ slot: 'skip_material_collection', selected: true }] as SlotRevision[];
    expect(filterWorkflowTabs(tabs, slots)).toEqual([]);
    for (const step_id of tabs[0].status_step_ids) {
      expect(filterWorkflowTabs(tabs, slots, undefined, {
        projection: { past: [step_id] },
      })).toEqual(tabs);
      expect(filterWorkflowTabs(tabs, slots, undefined, {
        projection: { past: [step_id], stale: [step_id] },
      })).toEqual([]);
    }
  });

  it('defers conditional tabs until the declared planning material is ready', () => {
    const tabs = [
      { id: 'planning', label: 'Planning', slots: [] },
      {
        id: 'direction', label: 'Direction', slots: [],
        hide_when_material: 'skip_direction',
      },
    ];

    expect(filterWorkflowTabs(tabs, [], 'execution_plan').map((tab) => tab.id)).toEqual([
      'planning',
    ]);

    const slots = [{
      slot_id: 'plan-id',
      revision: 1,
      selected: true,
      slot: 'execution_plan',
      created_at: '2026-08-22T00:00:00Z',
    }];
    expect(filterWorkflowTabs(tabs, slots, 'execution_plan').map((tab) => tab.id)).toEqual([
      'planning', 'direction',
    ]);
  });

  it('hides only tabs whose opt-in material has a selected revision', () => {
    const tabs = [
      { id: 'always', label: 'Always', slots: [] },
      {
        id: 'direction', label: 'Direction', slots: [],
        hide_when_material: 'skip_direction',
      },
      {
        id: 'design', label: 'Design', slots: [],
        hide_when_material: 'skip_design',
      },
    ];
    const slots = [{
      slot_id: 'skip-direction-id',
      revision: 1,
      selected: true,
      slot: 'skip_direction',
      created_at: '2026-08-21T00:00:00Z',
    }];

    expect(filterWorkflowTabs(tabs, slots).map((tab) => tab.id)).toEqual([
      'always', 'design',
    ]);
  });

  it('ignores unselected historical skip revisions', () => {
    const tabs = [{
      id: 'direction', label: 'Direction', slots: [],
      hide_when_material: 'skip_direction',
    }];
    const slots = [{
      slot_id: 'skip-direction-id',
      revision: 1,
      selected: false,
      slot: 'skip_direction',
      created_at: '2026-08-21T00:00:00Z',
    }];

    expect(filterWorkflowTabs(tabs, slots)).toEqual(tabs);
  });
});

describe('workflowTabAllowsDownload', () => {
  it('uses the explicit tab policy before the last-tab fallback', () => {
    const tab = { id: 'delivery', label: 'Delivery', slots: [], allow_download: true };
    expect(workflowTabAllowsDownload(tab, 1, 3)).toBe(true);
    expect(workflowTabAllowsDownload({ ...tab, allow_download: false }, 2, 3)).toBe(false);
  });

  it('keeps downloads on the final tab for existing workflow packages', () => {
    const tab = { id: 'result', label: 'Result', slots: [] };
    expect(workflowTabAllowsDownload(tab, 0, 2)).toBe(false);
    expect(workflowTabAllowsDownload(tab, 1, 2)).toBe(true);
  });
});

it('preserves authoritative workflow ownership and opt-in follow configuration', () => {
  const ui = { source_type: 'builtin', follow_mode: { default: 'following' }, tabs: [] };
  expect(hydrateWorkflowUI({ source_type: 'user', ui }).source_type).toBe('user');
  expect(hydrateWorkflowUI({ source_type: 'builtin', ui }).source_type).toBe('builtin');
  expect(hydrateWorkflowUI({ source_type: 'builtin' }).source_type).toBe('builtin');
  expect(hydrateWorkflowUI({ source_type: 'builtin', ui }).follow_mode).toEqual({ default: 'following' });
});

it('restores skipped tabs only for effective runtime activity', () => {
  const tabs = [{ id: 'result', step_id: 'generate', label: 'Result', slots: [], hide_when_material: 'skip' }];
  const slots = [{ slot: 'skip', selected: true }] as SlotRevision[];
  const node = { requires_approval: false, execution: 'running', validity: 'effective', reachability: 'reachable', readiness: '', branch: 'active' };
  expect(filterWorkflowTabs(tabs, slots, undefined, { projection: { current: ['generate'], nodes: { generate: node } } })).toEqual(tabs);
  expect(filterWorkflowTabs(tabs, slots, undefined, { projection: { current: ['generate'], nodes: { generate: { ...node, validity: 'stale' } } } })).toEqual([]);
});

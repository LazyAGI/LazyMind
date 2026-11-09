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
import type { WorkflowRuntimeProjection } from './workflowPanel';

describe('runtime skipped tab visibility', () => {
  const tabs = ['topic', 'review', 'analysis'].map(id => ({ id, label: id, slots: [] }));
  const projection: WorkflowRuntimeProjection = { pruned: ['topic'], bypassed: ['analysis'] };

  it('is opt-in and hides only explicitly skipped steps', () => {
    expect(filterWorkflowTabs(tabs, [], undefined, { projection })).toEqual(tabs);
    expect(filterWorkflowTabs(tabs, [], undefined, { projection }, true).map(t => t.id)).toEqual(['review']);
    expect(filterWorkflowTabs(tabs, [], undefined, undefined, true)).toEqual(tabs);
  });

  it('restores tabs after rewind changes the authoritative projection', () => {
    expect(filterWorkflowTabs(tabs, [], undefined, { projection }, true)).toHaveLength(1);
    expect(filterWorkflowTabs(tabs, [], undefined, { projection: { pruned: [], bypassed: [] } }, true)).toEqual(tabs);
  });

  it.each(['running', 'waiting', 'failed', 'succeeded'])('keeps effective %s work visible', execution => {
    const node = { branch: 'pruned', execution, validity: 'effective', readiness: 'not_applicable',
      reachability: 'unreachable', requires_approval: false };
    expect(filterWorkflowTabs(tabs, [], undefined, { projection: { nodes: { topic: node } } }, true)).toEqual(tabs);
  });

  it('keeps future and material-blocked steps visible', () => {
    const node = { branch: 'active', execution: 'none', validity: 'effective', readiness: 'blocked',
      reachability: 'unreachable', requires_approval: false };
    expect(filterWorkflowTabs(tabs, [], undefined, { projection: { nodes: { topic: node } } }, true)).toEqual(tabs);
  });

  it('shows only executed paper tabs after the workflow finishes', () => {
    const paper = hydrateWorkflowUI(load(readFileSync(resolve(process.cwd(), '../workflows/paper-writing/workflow.yaml'), 'utf8')));
    const visible = (projection: WorkflowRuntimeProjection) =>
      filterWorkflowTabs(paper.tabs, [], undefined, projection, true).map(tab => tab.id);

    expect(visible({ completed: true, past: ['research_topic', 'finish'] })).toEqual(['research_topic', 'finish']);
    expect(visible({ completed: true, past: ['peer_review', 'finish'] })).toEqual(['peer_review', 'finish']);
    expect(visible({ completed: false, past: ['research_topic'] })).toContain('revise_document');
    expect(visible({ completed: false, past: ['research_topic', 'finish'] })).toContain('revise_document');
    expect(visible({ completed: true, past: ['research_topic', 'revise_document', 'finish'] }))
      .toEqual(['research_topic', 'revise_document', 'finish']);
  });

  it('keeps a completed unexecuted tab when it has a selected artifact', () => {
    const tab = { id: 'future', label: 'Future', slots: [{ id: 'result', label: 'Result', type: 'file' as const }] };
    const slots = [{ slot_id: 'result-id', revision: 1, selected: true, slot: 'result', created_at: '2026-10-20T00:00:00Z' }];
    expect(filterWorkflowTabs([tab], slots, undefined, { completed: true, past: ['finish'] }, true)).toEqual([tab]);
    expect(filterWorkflowTabs([tab], [], undefined, { completed: true, past: ['finish'] }, false)).toEqual([tab]);
  });

  it('hides a composite only when every represented step is skipped', () => {
    const composite = [{ id: 'result', label: 'result', slots: [], status_step_ids: ['a', 'b'] }];
    expect(filterWorkflowTabs(composite, [], undefined, { projection: { pruned: ['a'] } }, true)).toEqual(composite);
    expect(filterWorkflowTabs(composite, [], undefined, { projection: { pruned: ['a'], bypassed: ['b'] } }, true)).toEqual([]);
  });

  it('reads the paper opt-in without changing PPT material-based rules', () => {
    const paper = hydrateWorkflowUI(load(readFileSync(resolve(process.cwd(), '../workflows/paper-writing/workflow.yaml'), 'utf8')));
    expect(paper.hide_skipped_steps).toBe(true);
    expect(filterWorkflowTabs(paper.tabs, [], undefined, { projection: { pruned: ['research_topic'] } }, paper.hide_skipped_steps)
      .some(tab => tab.id === 'research_topic')).toBe(false);
    const ppt = hydrateWorkflowUI(load(readFileSync(resolve(process.cwd(), '../workflows/ppt-workflow/workflow.yaml'), 'utf8')));
    expect(ppt.hide_skipped_steps).toBeUndefined();
    expect(ppt.tabs?.some(tab => tab.hide_when_material)).toBe(true);
  });

  it('hides skipped literature feedback while retaining future paper revision', () => {
    const paper = hydrateWorkflowUI(load(readFileSync(resolve(process.cwd(), '../workflows/paper-writing/workflow.yaml'), 'utf8')));
    const hidden = ['research_topic', 'literature_review', 'experiment', 'data_analysis', 'write_outline', 'write_document', 'reading_reflection'];
    const selected: WorkflowRuntimeProjection = { pruned: hidden, past: ['peer_review'] };
    const visible = () => filterWorkflowTabs(paper.tabs, [], undefined, selected, true).map(t => t.id);
    expect(visible()).toEqual(['peer_review', 'revise_document', 'finish']);
    selected.past = ['peer_review', 'refine_topic'];
    expect(visible()).toContain('refine_topic');
    selected.past = [];
    selected.pruned = [];
    expect(visible()).toContain('refine_topic');
    expect(visible()).not.toContain('prepare');
  });
});

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
  it.each([false, true])('keeps PPT intermediate results visible with legacy compact settings: %s', (legacy) => {
    const spec = load(readFileSync(resolve(process.cwd(), '../workflows/ppt-workflow/workflow.yaml'), 'utf8'));
    const ui = hydrateWorkflowUI(spec);
    const tabs = ui.tabs?.map(tab => ({
      ...tab,
      ...(legacy && tab.id !== 'composite_preview'
        ? { presentation_hide_when_material: 'compact_presentation' } : {}),
    }));
    const slots = [
      { slot: 'compact_presentation', selected: true },
      { slot: 'ppt_capability_requirements', selected: true },
      { slot: 'skip_background_images', selected: true },
      { slot: 'deck_outline', selected: true },
    ] as SlotRevision[];
    const runtime = { projection: { past: ['analyze_requirements', 'collect_materials', 'build_outline', 'plan_page_prompts'] } };
    expect(filterWorkflowTabs(tabs, slots, ui.tab_visibility_ready_material, runtime).map(tab => tab.id))
      .toEqual(['analysis', 'materials', 'outline', 'page_prompts', 'composite_preview']);
    expect(filterWorkflowTabs(tabs, slots.slice(0, 2), ui.tab_visibility_ready_material, runtime).map(tab => tab.id))
      .toEqual(['analysis', 'materials', 'background_prompts', 'background_images', 'outline', 'page_prompts', 'composite_preview']);
  });

  it.each([
    ['fast', ['screening_not_required', 'analysis_not_required'], ['expand_and_screen']],
    ['balanced', ['analysis_not_required'], ['expand_and_screen', 'screen_and_supplement']],
    ['deep', [], ['expand_and_screen', 'screen_and_supplement', 'analyze_relations']],
  ])('shows every planned paper expansion stage before %s starts', (_mode, materials, expected) => {
    const tabs = [
      { id: 'expand_and_screen', label: 'Search', slots: [] },
      { id: 'screen_and_supplement', label: 'Screen', slots: [], hide_when_material: 'screening_not_required' },
      { id: 'analyze_relations', label: 'Analyze', slots: [], hide_when_material: 'analysis_not_required' },
    ];
    const slots = materials.map(slot => ({ slot, selected: true })) as SlotRevision[];
    expect(filterWorkflowTabs(tabs, slots).map(tab => tab.id)).toEqual(expected);
  });

  it('shows a conditional stage again when a rewind clears its runtime bypass', () => {
    const tabs = [{ id: 'analysis', label: 'Analysis', slots: [], hide_when_material: 'list_ready' }];
    expect(filterWorkflowTabs(tabs, [], undefined, { projection: { bypassed: ['analysis'] } })).toEqual([]);
    expect(filterWorkflowTabs(tabs, [], undefined, { projection: { bypassed: [], ready: ['analysis'] } })).toEqual(tabs);
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

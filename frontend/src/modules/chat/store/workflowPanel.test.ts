import { describe, expect, it } from 'vitest';

import { filterWorkflowTabs, hydrateWorkflowUI } from './workflowPanel';

describe('hydrateWorkflowUI', () => {
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
    });
  });

  it('keeps a standalone UI payload usable', () => {
    const ui = { tabs: [{ id: 'result', label: 'Result', slots: [] }] };
    expect(hydrateWorkflowUI({ ui })).toBe(ui);
  });
});

describe('filterWorkflowTabs', () => {
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

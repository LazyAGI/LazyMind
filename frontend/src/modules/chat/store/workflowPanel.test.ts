import { describe, expect, it } from 'vitest';

import { hydrateWorkflowUI } from './workflowPanel';

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

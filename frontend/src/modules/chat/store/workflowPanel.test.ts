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

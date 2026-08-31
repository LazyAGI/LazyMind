import { describe, expect, it } from 'vitest';
import { parseWorkflowYaml } from './workflowParser';
import { serializeWorkflowModel } from './workflowSerializer';

describe('workflow UI declarations', () => {
  it('preserves html-slide widgets and exporter actions across an editor round trip', () => {
    const source = `
id: deck-workflow
name: Deck
steps:
  - {id: render, label: Render}
slots:
  - {id: deck_pages, type: text, cardinality: list, ordered: true}
  - {id: speaker_notes, type: text, cardinality: list, ordered: true}
ui:
  slots:
    deck_pages: {widgetType: html-slide}
  tabs:
    - id: deck
      step_id: render
      layout: composite
      slots: [{id: deck_pages}, {id: speaker_notes}]
      actions:
        - id: export_deck
          type: export
          provider: html-presentation
          inputs: {pages: deck_pages, notes: speaker_notes}
          formats: [pdf, editable-pptx]
          alignment: sort_order
`;

    const parsed = parseWorkflowYaml(source);
    expect(parsed?.ui?.slots?.deck_pages.widgetType).toBe('html-slide');
    expect(parsed?.ui?.tabs[0].step_id).toBe('render');
    expect(parsed?.ui?.tabs[0].actions?.[0].inputs.pages).toBe('deck_pages');

    const reparsed = parseWorkflowYaml(serializeWorkflowModel(parsed!));
    expect(reparsed?.ui?.slots?.deck_pages.widgetType).toBe('html-slide');
    expect(reparsed?.ui?.tabs[0].step_id).toBe('render');
    expect(reparsed?.ui?.tabs[0].actions?.[0]).toMatchObject({
      id: 'export_deck',
      provider: 'html-presentation',
      formats: ['pdf', 'editable-pptx'],
      alignment: 'sort_order',
    });
  });

  it('derives composite tab slots from the saved layout tree', () => {
    const source = `
id: composite-demo
name: Composite Demo
steps:
  - {id: render, label: Render}
slots:
  - {id: page_html, type: text, cardinality: list, ordered: true}
  - {id: bullet_points, type: text, cardinality: list, ordered: true}
ui:
  tabs:
    - id: result
      layout: composite
      slots: [{id: page_html}]
      composite_layout:
        direction: row
        children:
          - {slot: page_html, weight: 2}
          - {slot: bullet_points, weight: 1}
`;

    const parsed = parseWorkflowYaml(source)!;
    const reparsed = parseWorkflowYaml(serializeWorkflowModel(parsed))!;

    expect(reparsed.ui?.tabs[0].slots).toEqual([
      { id: 'page_html' },
      { id: 'bullet_points' },
    ]);
  });
});

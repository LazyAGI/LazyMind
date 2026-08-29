import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

describe('workflow panel compact layout', () => {
  it('keeps composite results independently scrollable', () => {
    const styles = readFileSync(
      new URL('../../frontend/src/modules/chat/components/WorkflowPanel/WorkflowPanel.scss', import.meta.url),
      'utf8',
    );
    const selector = ".workflow-panel__body > [role='tabpanel']:not([hidden]) > .composite-grid:not(.composite-grid--paged) {";
    const ruleStart = styles.indexOf(selector);
    const ruleEnd = styles.indexOf('}', ruleStart);
    const rule = styles.slice(ruleStart, ruleEnd);

    expect(ruleStart).toBeGreaterThan(-1);
    expect(rule).toContain('flex: 1 1 auto;');
    expect(rule).toContain('min-height: 0;');
    expect(rule).toContain('overflow-y: auto;');
    expect(rule).toContain('overscroll-behavior: contain;');
  });
});

import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

const styles = readFileSync(
  new URL('../../frontend/src/modules/chat/components/WorkflowPanel/WorkflowPanel.scss', import.meta.url),
  'utf8',
);
const compactStart = styles.indexOf('&:not(.workflow-panel--expanded) {');
const compactEnd = styles.indexOf('\n  &__tab-content {', compactStart);
const compactStyles = styles.slice(compactStart, compactEnd);
const expandedStart = styles.indexOf('&--expanded {');
const expandedEnd = styles.indexOf('\n  &--active', expandedStart);
const expandedStyles = styles.slice(expandedStart, expandedEnd);
const slideSource = readFileSync(
  new URL('../../frontend/src/modules/chat/components/WorkflowPanel/ppt/SlotHtmlSlide.tsx', import.meta.url),
  'utf8',
);

describe('workflow panel compact layout', () => {
  it('keeps no-tab GIF and image output independently scrollable', () => {
    const selector = '.workflow-panel__body > .workflow-panel__auto-grid,';
    const ruleStart = compactStyles.indexOf(selector);
    const ruleEnd = compactStyles.indexOf('}', ruleStart);
    const rule = compactStyles.slice(ruleStart, ruleEnd);

    expect(compactStart).toBeGreaterThan(-1);
    expect(compactEnd).toBeGreaterThan(compactStart);
    expect(ruleStart).toBeGreaterThan(-1);
    expect(rule).toContain('flex: 1 1 auto;');
    expect(rule).toContain('min-height: 0;');
    expect(rule).toContain('overflow-y: auto;');
    expect(rule).toContain('overscroll-behavior: contain;');
    expect(rule).toContain('scrollbar-gutter: stable;');
  });

  it('keeps tabbed non-paged composites independently scrollable', () => {
    const selector = ".workflow-panel__body > [role='tabpanel']:not([hidden]) > .composite-grid:not(.composite-grid--paged) {";
    const ruleStart = compactStyles.indexOf(selector);
    const ruleEnd = compactStyles.indexOf('}', ruleStart);
    const rule = compactStyles.slice(ruleStart, ruleEnd);

    expect(ruleStart).toBeGreaterThan(-1);
    expect(rule).toContain('flex: 1 1 auto;');
    expect(rule).toContain('min-height: 0;');
    expect(rule).toContain('overflow-y: auto;');
    expect(rule).toContain('overscroll-behavior: contain;');
  });

  it('keeps compact paged content from being flex-clipped', () => {
    const shellSelector = ".workflow-panel__body > [role='tabpanel']:not([hidden]) > .composite-shell--paged {";
    const shellStart = compactStyles.indexOf(shellSelector);
    const shellEnd = compactStyles.indexOf('}', shellStart);
    const shellRule = compactStyles.slice(shellStart, shellEnd);

    expect(shellStart).toBeGreaterThan(-1);
    expect(shellRule).toContain('min-height: 0;');

    const rowSelector = ".workflow-panel__body > [role='tabpanel']:not([hidden]) > .composite-shell--paged\n      .composite-grid--paged > .composite-grid__row--stack {";
    const rowRuleStart = compactStyles.indexOf(rowSelector);
    const rowRuleEnd = compactStyles.indexOf('}', rowRuleStart);
    const rowRule = compactStyles.slice(rowRuleStart, rowRuleEnd);

    expect(rowRuleStart).toBeGreaterThan(-1);
    expect(rowRule).toContain('flex-shrink: 0;');

    const gridStart = styles.indexOf('.composite-grid {');
    const pagedStart = styles.indexOf('  &--paged {', gridStart);
    const rowStart = styles.indexOf('  &__row {', pagedStart);
    const pagedRule = styles.slice(pagedStart, rowStart);

    expect(gridStart).toBeGreaterThan(-1);
    expect(pagedStart).toBeGreaterThan(-1);
    expect(rowStart).toBeGreaterThan(-1);
    expect(pagedRule).toContain('min-height: 0;');
    expect(pagedRule).toContain('overflow: auto;');
  });

  it('wires iframe wheel forwarding with a non-passive listener and cleanup', () => {
    expect(slideSource).toContain("doc.addEventListener('wheel', onWheel, { passive: false });");
    expect(slideSource).toContain("doc.removeEventListener('wheel', onWheel);");
    expect(slideSource).toContain('forwardSlideFrameWheel(frame, event);');
    expect(slideSource).not.toContain("frame.closest('.workflow-panel--expanded')");
  });
});

describe('workflow panel expanded layout', () => {
  it('keeps no-tab GIF and image output independently scrollable', () => {
    const selector = '.workflow-panel__body > .workflow-panel__auto-grid {';
    const ruleStart = expandedStyles.indexOf(selector);
    const ruleEnd = expandedStyles.indexOf('}', ruleStart);
    const rule = expandedStyles.slice(ruleStart, ruleEnd);

    expect(expandedStart).toBeGreaterThan(-1);
    expect(expandedEnd).toBeGreaterThan(expandedStart);
    expect(ruleStart).toBeGreaterThan(-1);
    expect(rule).toContain('flex: 1 1 0;');
    expect(rule).toContain('min-height: 0;');
    expect(rule).toContain('overflow-y: auto;');
    expect(rule).toContain('overscroll-behavior: contain;');
    expect(rule).toContain('scrollbar-gutter: stable;');
  });
});

import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

describe('model provider settings layout', () => {
  it('lets the embedded desktop grid shrink to the settings content width', () => {
    const styles = readFileSync(
      new URL('../../frontend/src/modules/settings/index.scss', import.meta.url),
      'utf8',
    );
    const desktopStart = styles.indexOf('@media (min-width: 1361px)');
    const selector = '.settings-integrated-surface.is-models .model-provider-shell {';
    const ruleStart = styles.indexOf(selector, desktopStart);
    const ruleEnd = styles.indexOf('}', ruleStart);
    const rule = styles.slice(ruleStart, ruleEnd);

    expect(desktopStart).toBeGreaterThan(-1);
    expect(ruleStart).toBeGreaterThan(desktopStart);
    expect(rule).toContain('width: 100%;');
    expect(rule).toContain(
      'grid-template-columns: minmax(0, 1fr) minmax(320px, 420px);',
    );
  });

  it('gives the embedded default-model page its own desktop scroll container', () => {
    const styles = readFileSync(
      new URL('../../frontend/src/modules/settings/index.scss', import.meta.url),
      'utf8',
    );
    const desktopStart = styles.indexOf('@media (min-width: 1361px)');
    const selector = '.settings-integrated-surface.is-models > .model-provider-service-page {';
    const ruleStart = styles.indexOf(selector, desktopStart);
    const ruleEnd = styles.indexOf('}', ruleStart);
    const rule = styles.slice(ruleStart, ruleEnd);

    expect(ruleStart).toBeGreaterThan(desktopStart);
    expect(rule).toContain('height: 100%;');
    expect(rule).toContain('min-height: 0;');
    expect(rule).toContain('overflow-y: auto;');
    expect(rule).toContain('overscroll-behavior: contain;');
  });
});

import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

describe('showcase detail layout', () => {
  it('keeps the replay body independently scrollable', () => {
    const styles = readFileSync(
      new URL('../../frontend/src/modules/showcase/index.scss', import.meta.url),
      'utf8',
    );
    const selector = '.showcase-replay-body {';
    const ruleStart = styles.indexOf(selector);
    const ruleEnd = styles.indexOf('}', ruleStart);
    const rule = styles.slice(ruleStart, ruleEnd);

    expect(ruleStart).toBeGreaterThan(-1);
    expect(rule).toContain('min-height: 0;');
    expect(rule).toContain('overflow-y: auto;');
    expect(rule).toContain('overscroll-behavior: contain;');
  });
});

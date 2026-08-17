import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

const readRepoFile = (path) => readFileSync(new URL(`../../${path}`, import.meta.url), 'utf8');

describe('memory skill translations', () => {
  it('defines every skill-market delete key in both locales', () => {
    const sources = [
      'frontend/src/modules/memory/components/SkillManagementSection/index.tsx',
      'frontend/src/modules/memory/components/SkillManagementSection/SkillMarketView.tsx',
    ].map(readRepoFile).join('\n');
    const referencedKeys = new Set(
      [...sources.matchAll(/admin\.(memorySkillMarketDelete[A-Za-z]*)/g)]
        .map((match) => match[1]),
    );

    expect(referencedKeys.size).toBe(5);
    for (const locale of ['zh-CN', 'en-US']) {
      const translations = readRepoFile(`frontend/src/i18n/locales/${locale}.ts`);
      for (const key of referencedKeys) {
        expect(translations, `missing admin.${key} in ${locale}`).toMatch(
          new RegExp(`\\n\\s+${key}:`),
        );
      }
    }
  });
});

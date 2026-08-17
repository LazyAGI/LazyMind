import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

describe('dataset duplicate-name error copy', () => {
  it('identifies error 2001102 as a dataset-name conflict', () => {
    const catalog = JSON.parse(readFileSync(
      new URL('../../i18n/errors/core.json', import.meta.url),
      'utf8',
    ));

    expect(catalog['2001102']).toEqual({
      'en-US': 'A dataset with this name already exists. Use a unique name.',
      'zh-CN': '数据集名称已存在，请使用未被占用的名称',
    });
  });
});

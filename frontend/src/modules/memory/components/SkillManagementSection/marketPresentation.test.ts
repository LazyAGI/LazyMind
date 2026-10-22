import { describe, expect, it } from 'vitest';
import { mapMarketSkillRecordToAsset, getMarketDisplayName, getMarketDisplayDescription } from './skillMarketMockData';
import { filterMarketSkills, isMarketSkillInstalled } from './skillHelpers';
import { createStructuredDraft } from '../../shared';

describe('market presentation', () => {
  const record = { id: 'x', name: 'browser-tool', description: 'Browser automation', display_name: '浏览器工具', display_description: '自动操作网页', display_locale: 'zh-CN', tag_labels: { web: '网页' }, tags: ['web'], category: 'web', content: 'original', marketSource: 'builtin' } as any;
  it('preserves canonical identity and presentation through detail drafts', () => {
    const asset = mapMarketSkillRecordToAsset(record);
    expect(asset.name).toBe('browser-tool');
    expect(getMarketDisplayName(asset)).toBe('浏览器工具');
    expect(getMarketDisplayDescription(createStructuredDraft(asset))).toBe('自动操作网页');
    expect(createStructuredDraft(asset).name).toBe('browser-tool');
    expect(asset.tags).toEqual(['web']);
    expect(isMarketSkillInstalled([{ ...asset, name: 'browser-tool' }], asset)).toBe(true);
  });
  it('searches both languages and filters by canonical tag values', () => {
    const assets = [mapMarketSkillRecordToAsset(record)];
    for (const keyword of ['浏览器', '操作网页', 'browser']) expect(filterMarketSkills(assets, {keyword, tag: 'web', source: 'builtin'})).toHaveLength(1);
    expect(filterMarketSkills(assets, {keyword: '', tag: '网页', source: 'builtin'})).toHaveLength(0);
  });
  it('searches Chinese metadata while English display text is selected', () => {
    const asset = mapMarketSkillRecordToAsset({...record, display_name: 'Browser Tool', display_description: 'Automate websites', display_locale: 'en-US', search_text: 'Browser Tool Automate websites 浏览器工具 自动操作网页'});
    for (const keyword of ['浏览器', '操作网页', 'browser', 'websites']) expect(filterMarketSkills([asset], {keyword, tag: 'web', source: 'builtin'})).toHaveLength(1);
    expect(asset.name).toBe('browser-tool');
    expect(asset.description).toBe('Browser automation');
    expect(getMarketDisplayName(asset)).toBe('Browser Tool');
  });

});

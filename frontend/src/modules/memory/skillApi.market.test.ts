import { describe, expect, it, vi } from 'vitest';
const market = vi.hoisted(() => ({ apiCoreSkillMarketGet: vi.fn(), apiCoreSkillMarketTagsGet: vi.fn(), apiCoreSkillMarketAdminItemsPost: vi.fn() }));
const http = vi.hoisted(() => ({ get: vi.fn() }));
vi.mock('@/api/generated/core-client', () => ({ Configuration: class {}, SkillsApiFactory: () => ({}), SkillDraftsApiFactory: () => ({}), SkillFsApiFactory: () => ({}), SkillRevisionsApiFactory: () => ({}), SkillSharesApiFactory: () => ({}), SkillMarketApiFactory: () => market, SkillDiffApiFactory: () => ({}) }));
vi.mock('@/components/request', () => ({ axiosInstance: http, BASE_URL: '', localizeErrorCode: vi.fn() }));
import { listBuiltinSkills, listSkillMarketPage, listSkillMarketTagCatalog, listSkillMarketTags, publishSkillToMarket } from './skillApi';
const presentation = { display_name: '网页助手', display_description: '操作网页', display_locale: 'zh-CN', tag_labels: { web: '网页' } };
describe('market API presentation', () => {
 it('keeps canonical builtin and admin fields beside display metadata', async () => {
  http.get.mockResolvedValue({ data: { items: [{ builtin_skill_uid: 'b', name: 'browser', description: 'Browse web', tags: ['web'], search_text: 'Browser 浏览器', ...presentation }] } });
  expect((await listBuiltinSkills())[0]).toMatchObject({ name: 'browser', description: 'Browse web', tags: ['web'], search_text: 'Browser 浏览器', ...presentation });
  market.apiCoreSkillMarketGet.mockResolvedValue({ data: { items: [{ market_item_id: 'm', source: { name: 'browser', description: 'Browse web' }, tags: ['web'], ...presentation }] } });
  expect((await listSkillMarketPage()).records[0]).toMatchObject({ name: 'browser', description: 'Browse web', tags: ['web'], ...presentation });
 });
 it('separates translated tag labels from canonical filter values and preserves the old wrapper', async () => {
  market.apiCoreSkillMarketTagsGet.mockResolvedValue({data: { tags: ['web', 'web'], tag_labels: {web: '网页'} }});
  expect(await listSkillMarketTagCatalog()).toEqual({tags: ['web'], tagLabels: {web: '网页'}});
  expect(await listSkillMarketTags()).toEqual(['web']);
 });
 it.each([{type:'url',url:'https://example.com/a.zip'}, {type:'uploaded_zip',uploadId:'upload'}] as const)('publishes display metadata for $type without canonical overrides', async (source) => {
  market.apiCoreSkillMarketAdminItemsPost.mockResolvedValue({data:{market_item_id:'m'}});
  await publishSkillToMarket({source, ...presentation});
  expect(market.apiCoreSkillMarketAdminItemsPost).toHaveBeenLastCalledWith({ marketPublishOpenAPIRequest: { name: '', display_name: presentation.display_name, display_description: presentation.display_description, tag_labels: presentation.tag_labels, source: source.type === 'url' ? source : {type:'uploaded_zip',upload_id:'upload'} } });
 });
});

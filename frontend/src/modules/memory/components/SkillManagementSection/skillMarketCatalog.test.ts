import { beforeEach, describe, expect, it, vi } from 'vitest';
import { listSkillMarketPage } from '../../skillApi';
import { loadAdminMarketCatalog } from './skillMarketCatalog';

vi.mock('../../skillApi', () => ({ listSkillMarketPage: vi.fn() }));
const list = vi.mocked(listSkillMarketPage);

describe('load complete admin market results', () => {
  beforeEach(() => { list.mockReset(); });

  it('loads every matching API page in server order, retaining keyword and tags', async () => {
    const records = Array.from({ length: 205 }, (_, i) => ({ id: String(i) }));
    list.mockImplementation(async options => ({
      records: records.slice((options!.page! - 1) * 100, options!.page! * 100),
      total: 205, page: options!.page!, pageSize: 100,
    } as Awaited<ReturnType<typeof listSkillMarketPage>>));
    expect(await loadAdminMarketCatalog('report', 'analysis')).toEqual(records);
    expect(list.mock.calls.map(([options]) => options)).toEqual([1, 2, 3].map(page => ({
      page, pageSize: 100, keyword: 'report', tags: ['analysis'],
    })));
  });

  it('rejects a failed later page instead of presenting incomplete results as the full catalog', async () => {
    list.mockResolvedValueOnce({ records: [], total: 101, page: 1, pageSize: 100 });
    list.mockRejectedValueOnce(new Error('catalog unavailable'));
    await expect(loadAdminMarketCatalog('', 'all')).rejects.toThrow('catalog unavailable');
  });
});

import { listSkillMarketPage } from '../../skillApi';

// The admin API caps pages at 100. Load the complete matching result so builtin
// and admin skills can be grouped together before the UI paginates them.
export async function loadAdminMarketCatalog(keyword: string, tag: string) {
  const options = { keyword, tags: tag === 'all' ? undefined : [tag], pageSize: 100 };
  const first = await listSkillMarketPage({ ...options, page: 1 });
  const records = [...first.records];
  const pageCount = Math.ceil(first.total / first.pageSize);
  for (let page = 2; page <= pageCount; page += 1) {
    const result = await listSkillMarketPage({ ...options, page });
    records.push(...result.records);
  }
  return records;
}

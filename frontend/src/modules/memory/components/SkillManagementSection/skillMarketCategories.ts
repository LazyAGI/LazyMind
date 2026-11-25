import type { SkillIconName } from './SkillIcon';
import type { StructuredAsset } from '../../shared';
import { builtinMarketCategories } from './builtinMarketCategories';

export const MARKET_CATEGORIES = [
  'research', 'writing', 'design', 'office', 'automation', 'business', 'learning', 'development',
] as const;
export type MarketCategory = typeof MARKET_CATEGORIES[number];
export type MarketCategoryFilter = MarketCategory | 'all';

const presentation: Record<MarketCategory, { tone: string; icon: SkillIconName }> = {
  research: { tone: 'blue', icon: 'scan-search' },
  writing: { tone: 'purple', icon: 'pen-line' },
  development: { tone: 'blue', icon: 'code-window' },
  design: { tone: 'purple', icon: 'palette' },
  office: { tone: 'teal', icon: 'chart-table' },
  automation: { tone: 'teal', icon: 'bot' },
  business: { tone: 'purple', icon: 'briefcase' },
  learning: { tone: 'blue', icon: 'open-book' },
};

// Published/custom skills retain their original metadata. Legacy categories map to
// the same eight display groups; general or unclassified tools live in Office.
const categoryAliases: Record<string, MarketCategory> = {
  search: 'research', review: 'research', chat: 'writing', data: 'office',
  productivity: 'office', marketing: 'business', finance: 'business',
  education: 'learning', career: 'learning', security: 'development',
  video: 'design', external: 'automation', 'project-management': 'automation',
  '检索与研究': 'research', '写作与文档': 'writing', '编程与开发': 'development',
  '设计与多媒体': 'design', '数据与办公': 'office', '智能体与自动化': 'automation',
  '营销与商业': 'business', '学习与成长': 'learning',
};

export function getMarketCategory(item: StructuredAsset): MarketCategory {
  if (Object.prototype.hasOwnProperty.call(builtinMarketCategories, item.id)) return builtinMarketCategories[item.id];
  const category = item.category.trim();
  if (MARKET_CATEGORIES.includes(category as MarketCategory)) return category as MarketCategory;
  return Object.prototype.hasOwnProperty.call(categoryAliases, category) ? categoryAliases[category] : 'office';
}

export function groupMarketSkills(items: StructuredAsset[]) {
  const groups = new Map<MarketCategory, StructuredAsset[]>();
  for (const item of items) {
    const category = getMarketCategory(item);
    const group = groups.get(category);
    if (group) group.push(item);
    else groups.set(category, [item]);
  }
  return MARKET_CATEGORIES.flatMap(category => {
    const skills = groups.get(category);
    return skills ? [{ category, skills }] : [];
  });
}

// pageSize is the minimum target. Complete the last category's row with its own
// remaining skills, then start the next page at that actual boundary.
export function getMarketPage(items: StructuredAsset[], category: MarketCategoryFilter, page: number, pageSize: number, columns = 1) {
  const groups = groupMarketSkills(items);
  const selectedGroups = groups.filter(group => category === 'all' || group.category === category);
  const ordered = selectedGroups.flatMap(group => group.skills);
  let offset = 0;
  const ranges = selectedGroups.map(group => {
    const start = offset;
    offset += group.skills.length;
    return { start, end: offset };
  });
  const pages: { start: number; end: number }[] = [];
  for (let start = 0; start < ordered.length;) {
    let end = Math.min(start + pageSize, ordered.length);
    const lastGroup = ranges.find(range => range.start < end && range.end >= end)!;
    const lastGroupCount = end - Math.max(start, lastGroup.start);
    const slots = (columns - lastGroupCount % columns) % columns;
    end = Math.min(end + slots, lastGroup.end);
    pages.push({ start, end });
    start = end;
  }
  const currentPage = Math.min(page, Math.max(1, pages.length));
  const range = pages[currentPage - 1];
  return {
    groups,
    total: ordered.length,
    page: currentPage,
    pageCount: pages.length,
    items: range ? ordered.slice(range.start, range.end) : [],
  };
}

export function getMarketCategoryPresentation(category: MarketCategory, t: (key: string) => string) {
  return { label: t(`admin.memorySkillMarketCategories.${category}`), ...presentation[category] };
}

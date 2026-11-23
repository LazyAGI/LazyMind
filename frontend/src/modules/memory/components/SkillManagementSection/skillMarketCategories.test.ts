import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { describe, expect, it } from 'vitest';
import type { StructuredAsset } from '../../shared';
import { builtinMarketCategories } from './builtinMarketCategories';
import { getMarketCategory, getMarketCategoryPresentation, getMarketPage, groupMarketSkills, MARKET_CATEGORIES } from './skillMarketCategories';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import SkillIcon from './SkillIcon';
import { builtinMarketSkillIcons, getMarketSkillIcon } from './skillMarketIcons';

const catalog = JSON.parse(readFileSync(resolve(__dirname, '../../../../../../skills/builtin-skills.lock.json'), 'utf8'));
const visible: StructuredAsset[] = catalog.skills.filter((s: { market_visible: boolean }) => s.market_visible)
  .map((s: { uid: string }) => ({ ...s, id: s.uid }));

describe('skill market classification and pagination', () => {
  it('uses eight distinct icon shapes for the eight categories', () => {
    const icons = MARKET_CATEGORIES.map(category => renderToStaticMarkup(createElement(SkillIcon, {
      name: getMarketCategoryPresentation(category, key => key).icon,
    })));
    expect(new Set(icons).size).toBe(8);
  });

  it('gives every visible builtin a purpose icon distinct from its category, with variation inside each family', () => {
    for (const skill of visible) {
      expect(Object.prototype.hasOwnProperty.call(builtinMarketSkillIcons, skill.id)).toBe(true);
      const icon = getMarketSkillIcon(skill);
      expect(icon).not.toBe(getMarketCategoryPresentation(getMarketCategory(skill), key => key).icon);
      expect(icon).not.toBe('ability-badge');
      const svg = renderToStaticMarkup(createElement(SkillIcon, { name: icon }));
      expect(svg).toMatch(/<(path|circle|rect|ellipse)\b/);
    }
    for (const group of groupMarketSkills(visible)) {
      expect(new Set(group.skills.map(getMarketSkillIcon)).size).toBeGreaterThan(1);
    }
    const iconOf = (name: string) => getMarketSkillIcon(visible.find(s => s.name === name)!);
    expect(iconOf('personal-document-search')).toBe('files');
    expect(iconOf('deep-research')).toBe('orbit-search');
    expect(iconOf('systematic-document-and-literature-review')).toBe('book-check');
    expect(iconOf('paper-search')).toBe('file-search');
    expect(iconOf('sciverse-paper-search')).toBe('telescope');
    expect(iconOf('humanizer')).toBe('feather');
    expect(iconOf('structured-longform-writing')).toBe('notebook-pen');
  });

  it('fills the writing row after 11 research skills and resumes at the next undisplayed skill', () => {
    const first = getMarketPage(visible, 'all', 1, 12, 3);
    const second = getMarketPage(visible, 'all', 2, 12, 3);
    expect(first.items).toHaveLength(14);
    expect(groupMarketSkills(first.items).map(group => group.skills.length)).toEqual([11, 3]);
    const writers = visible.filter(s => getMarketCategory(s) === 'writing');
    expect(second.items[0]).toBe(writers[3]);
    expect(getMarketPage(visible, 'all', 1, 12, 4).items).toHaveLength(15);
  });

  it('keeps complete rows, exact page counts, and no duplicate or missing skills at every grid width and filter', () => {
    for (const columns of [1, 2, 3, 4, 5]) {
      for (const pageSize of [12, 20, 24]) {
        for (const category of ['all', ...MARKET_CATEGORIES] as const) {
          const first = getMarketPage(visible, category, 1, pageSize, columns);
          const pages = Array.from({ length: first.pageCount }, (_, i) => getMarketPage(visible, category, i + 1, pageSize, columns));
          const expected = groupMarketSkills(visible).filter(group => category === 'all' || group.category === category).flatMap(group => group.skills);
          expect(pages.flatMap(p => p.items)).toEqual(expected);
          for (let i = 0; i < pages.length - 1; i += 1) {
            const pageGroups = groupMarketSkills(pages[i].items);
            const last = pageGroups[pageGroups.length - 1];
            if (last.category === getMarketCategory(pages[i + 1].items[0])) {
              expect(last.skills.length % columns).toBe(0);
            }
            expect(pages[i].items.length).toBeGreaterThanOrEqual(pageSize);
            expect(pages[i].items.length).toBeLessThan(pageSize + columns);
          }
          expect(getMarketPage(visible, category, 100, pageSize, columns).page).toBe(first.pageCount);
        }
      }
    }
  });
  it('explicitly reviews every visible builtin and keeps distinct skills with the same name', () => {
    expect(visible.every(s => Object.prototype.hasOwnProperty.call(builtinMarketCategories, s.id))).toBe(true);
    const groups = groupMarketSkills(visible);
    expect(groups.map(g => g.category)).toEqual(MARKET_CATEGORIES);
    expect(groups.map(g => g.skills.length)).toEqual([11, 10, 7, 6, 13, 20, 6, 16]);
    const all = groups.flatMap(g => g.skills);
    expect(new Set(all.map(s => s.id)).size).toBe(visible.length);
    expect(all.filter(s => s.name === 'frontend-design')).toHaveLength(2);
    const find = (name: string) => visible.find(s => s.name === name)!;
    expect(getMarketCategory(find('superpowers'))).toBe('development');
    expect(getMarketCategory(find('humanizer'))).toBe('writing');
    expect(getMarketCategory(find('agent-browser'))).toBe('automation');
    expect(getMarketCategory(find('bid-query-analysis-workbench'))).toBe('business');
    expect(getMarketCategory(find('PDF Generator'))).toBe('writing');
  });

  it('sorts the complete collection before paging, retains stable within-category order and never mutates assets', () => {
    const original = visible.slice();
    const expected = groupMarketSkills(visible).flatMap(g => g.skills);
    const pages = Array.from({ length: Math.ceil(visible.length / 12) }, (_, i) => getMarketPage(visible, 'all', i + 1, 12));
    expect(pages.flatMap(p => p.items)).toEqual(expected);
    expect(visible).toEqual(original);
    for (const category of MARKET_CATEGORIES) {
      const members = visible.filter(s => getMarketCategory(s) === category);
      const actual = Array.from({ length: Math.ceil(members.length / 5) }, (_, i) => getMarketPage(visible, category, i + 1, 5));
      expect(actual.flatMap(p => p.items)).toEqual(members);
      expect(actual.every(p => p.total === members.length)).toBe(true);
      expect(getMarketPage(visible, category, 100, 5).page).toBe(Math.ceil(members.length / 5));
    }
    expect(getMarketPage([], 'research', 9, 12)).toMatchObject({ items: [], page: 1, total: 0 });
  });
});

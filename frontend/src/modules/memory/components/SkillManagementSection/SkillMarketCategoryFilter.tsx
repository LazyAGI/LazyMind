import { useState } from 'react';
import { Button, Dropdown, Tooltip, type MenuProps } from 'antd';
import { TagsOutlined } from '@ant-design/icons';
import type { StructuredAsset } from '../../shared';
import SkillIcon from './SkillIcon';
import {
  getMarketCategoryPresentation, MARKET_CATEGORIES,
  type MarketCategory, type MarketCategoryFilter,
} from './skillMarketCategories';

interface Props {
  t: (key: string) => string;
  value: MarketCategoryFilter;
  groups: { category: MarketCategory; skills: StructuredAsset[] }[];
  onChange: (value: MarketCategoryFilter) => void;
  tags: string[];
  tag: string;
  onTagChange: (value: string) => void;
}

export default function SkillMarketCategoryFilter({ t, value, groups, onChange, tags, tag, onTagChange }: Props) {
  const [open, setOpen] = useState(false);
  const counts = new Map(groups.map(group => [group.category, group.skills.length]));
  const label = (name: string, count: number) => (
    <span className="memory-skill-market-category-option"><span>{name}</span><span>{count}</span></span>
  );
  const items: MenuProps['items'] = [
    { key: 'all', icon: <TagsOutlined />, label: label(t('admin.memorySkillCategoryAll'), groups.reduce((sum, group) => sum + group.skills.length, 0)) },
    ...MARKET_CATEGORIES.map(category => {
      const presentation = getMarketCategoryPresentation(category, t);
      return { key: category, icon: <SkillIcon name={presentation.icon} size={16} />, label: label(presentation.label, counts.get(category) || 0) };
    }),
  ];
  // Keep the existing fine-grained tag filter, independently from major categories.
  if (tags.length || tag !== 'all') items.push(
    { type: 'divider' },
    { type: 'group', key: 'tags', label: t('admin.memorySkillMarketTags'), children: [
      { key: 'tag:all', label: t('admin.memorySkillMarketAllTags') },
      ...tags.map(name => ({ key: `tag:${name}`, label: name })),
    ] },
  );
  return (
    <div className="memory-skill-market-category-popover">
      <Dropdown
        trigger={['click']}
        placement="bottomRight"
        open={open}
        onOpenChange={setOpen}
        overlayClassName="memory-skill-market-category-dropdown"
        menu={{
          items, selectable: true, selectedKeys: [value, `tag:${tag}`],
          'aria-label': t('admin.memorySkillMarketMajorCategories'),
          onClick: ({ key }) => {
            if (key.startsWith('tag:')) onTagChange(key.slice(4));
            else onChange(key as MarketCategoryFilter);
            setOpen(false);
          },
        }}
      >
        <Tooltip title={t('admin.memorySkillMarketTags')}>
          <Button
            aria-label={t('admin.memorySkillMarketTags')}
            aria-haspopup="menu"
            aria-expanded={open}
            className={value !== 'all' || tag !== 'all' ? 'is-active' : undefined}
            icon={<TagsOutlined />}
          />
        </Tooltip>
      </Dropdown>
    </div>
  );
}

export function SkillMarketCategorySummary({ t, value, total }: Pick<Props, 't' | 'value'> & { total: number }) {
  const presentation = value === 'all' ? null : getMarketCategoryPresentation(value, t);
  return (
    <div className="memory-skill-market-category-summary" role="status" aria-live="polite">
      <span className="memory-skill-market-current-category">
        {presentation ? <SkillIcon name={presentation.icon} size={16} /> : <TagsOutlined />}
        {presentation?.label || t('admin.memorySkillCategoryAll')}
        <span className="memory-skill-market-current-count">{total}</span>
      </span>
    </div>
  );
}

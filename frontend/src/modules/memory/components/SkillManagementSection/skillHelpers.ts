import type { StructuredAsset } from "../../shared";
import type { SkillAssetRecord } from "../../skillApi";
import type { SkillMarketSourceFilter, SkillSourceFilter } from "../../shared";
import type { MarketSkillAsset } from "./skillMarketMockData";
import { getMarketSource } from "./skillMarketMockData";

export const mapSkillAssetRecordToStructuredAsset = (
  item: SkillAssetRecord,
): StructuredAsset => ({
  id: item.id,
  name: item.name,
  description: item.description,
  category: item.category,
  tags: item.tags,
  content: item.content,
  headRevisionId: item.headRevisionId,
  draft: item.draft,
  autoEvo: item.autoEvo,
  isEnabled: item.isEnabled,
});

export const filterInstalledSkills = (
  items: StructuredAsset[],
  options: {
    keyword: string;
    category?: string;
    source: SkillSourceFilter;
  },
) => {
  const keyword = options.keyword.trim().toLowerCase();

  return items.filter((item) => {
    if (options.category && item.category !== options.category) {
      return false;
    }

    if (options.source !== "all" && resolveSkillSourceType(item) !== options.source) {
      return false;
    }

    if (!keyword) {
      return true;
    }

    return (
      item.name.toLowerCase().includes(keyword) ||
      item.description.toLowerCase().includes(keyword)
    );
  });
};

export const filterMarketSkills = (
  items: StructuredAsset[],
  options: {
    keyword: string;
    category: string;
    source: SkillMarketSourceFilter;
  },
) => {
  const keyword = options.keyword.trim().toLowerCase();

  return items.filter((item) => {
    if (options.category !== "all" && item.category !== options.category) {
      return false;
    }

    if (options.source !== "all" && getMarketSource(item) !== options.source) {
      return false;
    }

    if (!keyword) {
      return true;
    }

    return (
      item.name.toLowerCase().includes(keyword) ||
      item.description.toLowerCase().includes(keyword)
    );
  });
};

export const isMarketSkillInstalled = (
  installedSkills: StructuredAsset[],
  marketItem: StructuredAsset,
): boolean => {
  const marketSkill = marketItem as MarketSkillAsset;
  if (marketSkill.installed) {
    return true;
  }

  const marketItemId = marketSkill.marketItemId?.trim() || marketItem.id.trim();
  const normalizedName = marketItem.name.trim().toLowerCase();

  return installedSkills.some((skill) => {
    const installedMarketId = (skill as MarketSkillAsset).marketItemId?.trim();
    if (installedMarketId && marketItemId && installedMarketId === marketItemId) {
      return true;
    }
    return skill.name.trim().toLowerCase() === normalizedName;
  });
};

export const collectMarketCategories = (items: StructuredAsset[]) =>
  [...new Set(items.map((item) => item.category).filter(Boolean))].sort((left, right) =>
    left.localeCompare(right, "zh-CN"),
  );

const resolveSkillSourceType = (
  _item: StructuredAsset,
): "builtin" | "admin" | "personal" => "personal";

import type { StructuredAsset } from "../../shared";
import type { SkillAssetRecord } from "../../skillApi";
import {
  getMarketSource,
} from "./skillMarketMockData";
import {
  resolveSkillSourceType,
  type SkillMarketSourceFilter,
  type SkillSourceFilter,
} from "../../shared";

export const mapSkillAssetRecordToStructuredAsset = (
  item: SkillAssetRecord,
): StructuredAsset => ({
  id: item.id,
  name: item.name,
  description: item.description,
  category: item.category,
  tags: item.tags,
  content: item.content,
  parentId: item.parentId,
  parentSkillName: item.parentSkillName,
  protect: item.protect,
  autoEvo: item.autoEvo,
  autoEvoApplyStatus: item.autoEvoApplyStatus,
  autoEvoGeneration: item.autoEvoGeneration,
  autoEvoError: item.autoEvoError,
  fileExt: item.fileExt,
  isEnabled: item.isEnabled,
  hasPendingReviewSuggestions: item.hasPendingReviewSuggestions,
  hasPendingReviewResult: item.hasPendingReviewResult,
  hasPendingRemoveSuggestion: item.hasPendingRemoveSuggestion,
  reviewStatus: item.reviewStatus,
  suggestionStatus: item.suggestionStatus,
  nodeType: item.nodeType,
  updateStatus: item.updateStatus,
  builtinSkillUid: item.builtinSkillUid,
  originBuiltinSkillUid: item.originBuiltinSkillUid,
  isBuiltinTemplate: item.isBuiltinTemplate,
  activationStatus: item.activationStatus,
  readonly: item.readonly,
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
    if (item.isBuiltinTemplate) {
      return false;
    }
    if (item.parentId) {
      return false;
    }

    const sourceType = resolveSkillSourceType(item);
    if (options.source !== "all" && sourceType !== options.source) {
      return false;
    }

    if (options.category && item.category !== options.category) {
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
    if (item.parentId) {
      return false;
    }

    const isMarketItem =
      item.isBuiltinTemplate || getMarketSource(item) === "admin";
    if (!isMarketItem) {
      return false;
    }

    const marketSource = getMarketSource(item);
    if (options.source === "admin" && marketSource !== "admin") {
      return false;
    }
    if (options.source === "builtin" && marketSource !== "builtin") {
      return false;
    }

    if (options.category !== "all" && item.category !== options.category) {
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

export const buildInstalledSkillTree = (
  items: StructuredAsset[],
  allAssets: StructuredAsset[],
) => {
  const visibleParentIds = new Set(items.map((item) => item.id));

  return items.map((parent) => ({
    ...parent,
    children: allAssets.filter(
      (item) => item.parentId === parent.id && visibleParentIds.has(parent.id),
    ),
  }));
};

export const collectMarketCategories = (items: StructuredAsset[]) =>
  [...new Set(items.filter((item) => !item.parentId).map((item) => item.category).filter(Boolean))].sort(
    (left, right) => left.localeCompare(right, "zh-CN"),
  );

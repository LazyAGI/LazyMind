import type { StructuredAsset } from "../../shared";
import type { MarketSkillRecord } from "../../skillApi";

export type MarketSkillAsset = StructuredAsset & {
  marketSource?: "builtin" | "admin";
  marketItemId?: string;
  sourceSkillId?: string;
  installed?: boolean;
  installedSkillId?: string;
};

export const getMarketSource = (item: StructuredAsset): "builtin" | "admin" | "personal" => {
  const marketSource = (item as MarketSkillAsset).marketSource;
  if (marketSource === "admin") {
    return "admin";
  }
  if (marketSource === "builtin") {
    return "builtin";
  }
  return "personal";
};

export const mapMarketSkillRecordToAsset = (
  item: MarketSkillRecord,
): MarketSkillAsset => ({
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
  readonly: true,
  marketSource: item.marketSource,
  marketItemId: item.marketItemId,
  sourceSkillId: item.sourceSkillId,
  installed: item.installed,
  installedSkillId: item.installedSkillId,
});

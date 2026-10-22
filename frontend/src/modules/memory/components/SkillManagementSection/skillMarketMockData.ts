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
  search_text: item.search_text,
  display_name: item.display_name,
  display_description: item.display_description,
  display_locale: item.display_locale,
  tag_labels: item.tag_labels,
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
  provider: item.provider,
});

export const getMarketDisplayName = (item: { name: string; display_name?: string }) => item.display_name?.trim() || item.name;
export const getMarketDisplayDescription = (item: { description: string; display_description?: string }) => item.display_description?.trim() || item.description;

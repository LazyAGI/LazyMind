import type { StructuredAsset } from "../../shared";
import type { SkillOrganizeDepth } from "../../skillApi";

export const MIN_SKILL_ORGANIZE_SELECTION = 2;
export const MAX_SKILL_ORGANIZE_SELECTION = 20;

export const isSkillOrganizeEligible = (
  skill: Pick<StructuredAsset, "category" | "originBuiltinSkillUid" | "readonly" | "cloudResourceId">,
  mode: SkillOrganizeDepth = "light",
) => !skill.readonly && !skill.cloudResourceId && (
  mode === "light" || (skill.category === "internal" && !skill.originBuiltinSkillUid)
);

export const canSubmitSkillOrganize = (selectedCount: number) =>
  selectedCount >= MIN_SKILL_ORGANIZE_SELECTION &&
  selectedCount <= MAX_SKILL_ORGANIZE_SELECTION;

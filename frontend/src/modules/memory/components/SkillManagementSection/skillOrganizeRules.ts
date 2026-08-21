import type { StructuredAsset } from "../../shared";

export const MIN_SKILL_ORGANIZE_SELECTION = 2;
export const MAX_SKILL_ORGANIZE_SELECTION = 20;

export const isSkillOrganizeEligible = (
  skill: Pick<StructuredAsset, "category">,
) => skill.category === "internal";

export const canSubmitSkillOrganize = (selectedCount: number) =>
  selectedCount >= MIN_SKILL_ORGANIZE_SELECTION &&
  selectedCount <= MAX_SKILL_ORGANIZE_SELECTION;

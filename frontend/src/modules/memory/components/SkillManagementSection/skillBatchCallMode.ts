import type { StructuredAsset } from "../../shared";
import { buildSkillUpdatePayload, patchSkillAsset, type SkillCallMode } from "../../skillApi";

/** Update only settings: a selection may contain metadata loaded on older pages. */
export async function updateSelectedSkillCallModes(
  skills: StructuredAsset[],
  mode: SkillCallMode,
  patch: typeof patchSkillAsset = patchSkillAsset,
) {
  const succeededIds: string[] = [];
  const failed: Array<{ skill: StructuredAsset; error: unknown }> = [];
  const unique = new Map(skills.map((skill) => [skill.id, skill]));
  for (const skill of unique.values()) {
    if (skill.cloudResourceId || skill.readonly) {
      failed.push({ skill, error: new Error("Skill is not locally editable") });
      continue;
    }
    try {
      await patch(skill.id, buildSkillUpdatePayload({ callMode: mode, isEnabled: true }));
      succeededIds.push(skill.id);
    } catch (error) {
      failed.push({ skill, error });
    }
  }
  return { succeededIds, failed };
}

import { describe, expect, it } from "vitest";
import {
  skillOrganizeErrorText,
  skillOrganizeModeLabel,
  skillOrganizeStatusLabel,
} from "./skillOrganizeCopy";

const t = (key: string) => key;

describe("skillOrganizeCopy", () => {
  it("maps status and mode to copy keys", () => {
    expect(skillOrganizeStatusLabel("failed", t)).toBe("admin.memorySkillOrganizeStatusFailed");
    expect(skillOrganizeStatusLabel("completed", t)).toBe("admin.memorySkillOrganizeStatusCompleted");
    expect(skillOrganizeModeLabel("light", t)).toBe("admin.memorySkillOrganizeLight");
    expect(skillOrganizeModeLabel("deep", t)).toBe("admin.memorySkillOrganizeDeep");
  });

  it("keeps Chinese errors and replaces English ones", () => {
    expect(skillOrganizeErrorText("", "缺少 SKILL.md", t)).toBe("缺少 SKILL.md");
    expect(
      skillOrganizeErrorText("", "review failed: Skill name '写作' is invalid", t),
    ).toBe("admin.memorySkillOrganizeFailed");
    expect(skillOrganizeErrorText("skill_organize_invalid_package", "missing SKILL.md", t)).toBe(
      "admin.memorySkillOrganizeFailed",
    );
    expect(skillOrganizeErrorText("skill_organize_draft_conflict", "draft conflict", t)).toBe(
      "admin.memorySkillOrganizeDraftConflict",
    );
  });
});

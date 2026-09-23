import { describe, expect, it } from "vitest";
import {
  formatSkillOrganizeElapsed,
  skillOrganizeRunningTitleKey,
} from "./skillOrganizeProgress";

const t = (key: string, options?: Record<string, unknown>) =>
  `${key}${options ? JSON.stringify(options) : ""}`;

describe("skill organize progress copy", () => {
  it("maps known stages and falls back while waiting", () => {
    expect(skillOrganizeRunningTitleKey("pending")).toBe("admin.memorySkillOrganizeStagePending");
    expect(skillOrganizeRunningTitleKey("organize_plan")).toBe("admin.memorySkillOrganizeStagePlan");
    expect(skillOrganizeRunningTitleKey("organize_draft")).toBe("admin.memorySkillOrganizeStageDraft");
    expect(skillOrganizeRunningTitleKey("organize_apply")).toBe("admin.memorySkillOrganizeStageApply");
    expect(skillOrganizeRunningTitleKey("running")).toBe("admin.memorySkillOrganizeRunning");
  });

  it("keeps elapsed copy in the tooltip, not a second line", () => {
    expect(formatSkillOrganizeElapsed(4500, t)).toBe(
      'admin.memorySkillOrganizeElapsedSeconds{"count":4}',
    );
    expect(formatSkillOrganizeElapsed(125000, t)).toBe(
      'admin.memorySkillOrganizeElapsedMinutes{"minutes":2,"seconds":5}',
    );
  });
});

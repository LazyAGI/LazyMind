import { describe, expect, it } from "vitest";
import { isSkillOrganizeEligible } from "./skillOrganizeRules";

describe("organization scope", () => {
  it.each(["internal", "external", "learning", ""])("light accepts editable installed category %s", (category) => {
    expect(isSkillOrganizeEligible({ category }, "light")).toBe(true);
    expect(isSkillOrganizeEligible({ category, originBuiltinSkillUid: "builtin" }, "light")).toBe(true);
  });
  it.each(["light", "deep"] as const)("%s rejects readonly and cloud-only skills", (mode) => {
    expect(isSkillOrganizeEligible({ category: "internal", readonly: true }, mode)).toBe(false);
    expect(isSkillOrganizeEligible({ category: "internal", cloudResourceId: "cloud" }, mode)).toBe(false);
  });
  it("deep accepts only internal skills without builtin provenance", () => {
    expect(isSkillOrganizeEligible({ category: "internal" }, "deep")).toBe(true);
    expect(isSkillOrganizeEligible({ category: "internal", originBuiltinSkillUid: "builtin" }, "deep")).toBe(false);
    expect(isSkillOrganizeEligible({ category: "external" }, "deep")).toBe(false);
    expect(isSkillOrganizeEligible({ category: "legacy" }, "deep")).toBe(false);
  });
});

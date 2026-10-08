import { describe, expect, it } from "vitest";
import { isInvalidSkillPackageError } from "./skillUpload";

describe("isInvalidSkillPackageError", () => {
  it.each([
    { response: { status: 422, data: { message: "skill package must contain SKILL.md" } } },
    { response: { status: 422, data: { msg: "skill_md_missing" } } },
    { response: { status: 422, data: { error: { detail: "invalid skill package" } } } },
  ])("recognizes package validation failures", (error) => {
    expect(isInvalidSkillPackageError(error)).toBe(true);
  });

  it("does not relabel unrelated upload failures", () => {
    expect(isInvalidSkillPackageError({ response: { status: 500, data: { message: "database unavailable" } } })).toBe(false);
  });
});

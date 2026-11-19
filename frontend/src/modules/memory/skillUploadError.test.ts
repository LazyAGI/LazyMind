import { describe, expect, it } from "vitest";
import { isSkillAlreadyExistsError, isSkillPackageTooLargeError, isSkillSourceAmbiguousError } from "./skillUploadError";

describe("skill URL import error recovery", () => {
  it.each([
    { response: { status: 413, data: {} } },
    { response: { status: 400, data: { data: { code: "payload_too_large" } } } },
    { response: { data: { error: { code: "payload_too_large" } } } },
  ])("recognizes size failures without depending on English server messages", (error) => {
    expect(isSkillPackageTooLargeError(error)).toBe(true);
  });

  it.each([
    { response: { status: 400, data: { data: { code: "github_multiple_skills" } } } },
    { response: { status: 422, data: { data: { code: "skill_md_ambiguous" } } } },
    { response: { status: 400, data: { data: { code: "skill_source_ambiguous" } } } },
    { response: { data: { error: { code: "github_multiple_skills" } } } },
  ])("recognizes repositories or packages with multiple possible skill roots", (error) => {
    expect(isSkillSourceAmbiguousError(error)).toBe(true);
  });

  it("does not offer package recovery actions for unrelated failures", () => {
    const error = { response: { status: 500, data: { message: "database unavailable" } } };
    expect(isSkillPackageTooLargeError(error)).toBe(false);
    expect(isSkillSourceAmbiguousError(error)).toBe(false);
  });
});

describe("isSkillAlreadyExistsError", () => {
  it("recognizes the dedicated duplicate-skill semantic code", () => {
    expect(isSkillAlreadyExistsError({
      response: { data: { data: { code: "skill_already_exists" } } },
    })).toBe(true);
  });
  it("recognizes the skill path conflict returned by the create endpoint", () => {
    expect(
      isSkillAlreadyExistsError({
        response: {
          status: 409,
          data: {
            code: 2000107,
            data: { code: "path_exists" },
          },
        },
      }),
    ).toBe(true);
  });

  it("recognizes the legacy skill-exists business code", () => {
    expect(
      isSkillAlreadyExistsError({
        response: { data: { code: 2001108 } },
      }),
    ).toBe(true);
  });

  it("does not treat other conflicts as duplicate skills", () => {
    expect(
      isSkillAlreadyExistsError({
        response: {
          status: 409,
          data: {
            code: 2000107,
            data: { code: "draft_conflict" },
          },
        },
      }),
    ).toBe(false);
  });
});

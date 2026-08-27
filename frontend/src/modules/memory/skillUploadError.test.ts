import { describe, expect, it } from "vitest";
import { isSkillAlreadyExistsError } from "./skillUploadError";

describe("isSkillAlreadyExistsError", () => {
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

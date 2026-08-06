import { describe, expect, it } from "vitest";
import { shouldShowSkillMessageCenter } from "./collaborationVisibility";

describe("shouldShowSkillMessageCenter", () => {
  it("shows the message center when viewing installed skills without hiding group surfaces", () => {
    expect(
      shouldShowSkillMessageCenter({ skillView: "installed", hideUserGroupSurfaces: false }),
    ).toBe(true);
  });

  it("hides the message center when group surfaces are hidden", () => {
    expect(
      shouldShowSkillMessageCenter({ skillView: "installed", hideUserGroupSurfaces: true }),
    ).toBe(false);
  });

  it("hides the message center for non-installed views", () => {
    expect(
      shouldShowSkillMessageCenter({ skillView: "market", hideUserGroupSurfaces: false }),
    ).toBe(false);
    expect(
      shouldShowSkillMessageCenter({ skillView: "plugins", hideUserGroupSurfaces: false }),
    ).toBe(false);
  });
});

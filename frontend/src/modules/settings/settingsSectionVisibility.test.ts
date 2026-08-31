import { describe, expect, it } from "vitest";

import { isSettingsSectionVisible } from "./settingsSectionVisibility";

describe("isSettingsSectionVisible", () => {
  const desktopFeatures = {
    hideUserGroupSurfaces: true,
  };
  const cloudFeatures = {
    hideUserGroupSurfaces: false,
  };

  it("hides organization but keeps developer settings in desktop mode", () => {
    expect(
      isSettingsSectionVisible("organization", true, desktopFeatures),
    ).toBe(false);
    expect(
      isSettingsSectionVisible("developer", true, desktopFeatures),
    ).toBe(true);
  });

  it("keeps cloud organization settings admin-only", () => {
    expect(
      isSettingsSectionVisible("organization", true, cloudFeatures),
    ).toBe(true);
    expect(
      isSettingsSectionVisible("organization", false, cloudFeatures),
    ).toBe(false);
  });

  it("keeps developer settings available independently of administrator access", () => {
    expect(
      isSettingsSectionVisible("developer", false, cloudFeatures),
    ).toBe(true);
  });
});

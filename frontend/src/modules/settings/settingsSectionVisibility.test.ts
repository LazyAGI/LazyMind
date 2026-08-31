import { describe, expect, it } from "vitest";

import { isSettingsSectionVisible } from "./settingsSectionVisibility";

describe("isSettingsSectionVisible", () => {
  const desktopFeatures = {
    hideEvo: true,
    hideUserGroupSurfaces: true,
  };
  const cloudFeatures = {
    hideEvo: false,
    hideUserGroupSurfaces: false,
  };

  it("hides organization and developer settings in desktop mode", () => {
    expect(
      isSettingsSectionVisible("organization", true, desktopFeatures),
    ).toBe(false);
    expect(
      isSettingsSectionVisible("developer", true, desktopFeatures),
    ).toBe(false);
  });

  it("keeps cloud organization settings admin-only", () => {
    expect(
      isSettingsSectionVisible("organization", true, cloudFeatures),
    ).toBe(true);
    expect(
      isSettingsSectionVisible("organization", false, cloudFeatures),
    ).toBe(false);
  });

  it("keeps developer settings available when self-evolution is enabled", () => {
    expect(
      isSettingsSectionVisible("developer", false, cloudFeatures),
    ).toBe(true);
  });
});

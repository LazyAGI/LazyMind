import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

const readFrontendSource = (relativePath) => readFileSync(
  new URL(`../../frontend/src/${relativePath}`, import.meta.url),
  "utf8",
);

describe("developer mode access", () => {
  it("shows developer settings to every signed-in user without admin-only copy", () => {
    const settingsSource = readFrontendSource("modules/settings/index.tsx");

    expect(settingsSource).not.toContain('...(isAdmin ? [{ id: "developer"');
    expect(settingsSource).not.toContain('isAdmin && dashboardCard("developer"');
    expect(settingsSource).not.toContain("settingsPage.adminOnly");
    expect(settingsSource).not.toContain('t("settingsPage.admin")');
    expect(settingsSource).not.toContain("settings-admin-tag");
  });

  it("does not hide or disable developer mode for non-admin users", () => {
    const layoutSource = readFrontendSource("layouts/MainLayout.tsx");

    expect(layoutSource).not.toContain("isAdminUser && !runtimeFeatures.hideEvo");
    expect(layoutSource).not.toContain("...(!runtimeFeatures.hideEvo");
    expect(layoutSource).toContain('key: "/settings?section=developer"');
    expect(layoutSource).toContain(
      "const canAccessSelfEvolution = !hideEvo && developerActive && isAdminUser;",
    );
    expect(layoutSource).not.toContain("if (!isAdminUser && developerActive)");
  });

  it("uses developer copy without self-evolution when evo is hidden", () => {
    const settingsSource = readFrontendSource("modules/settings/index.tsx");

    expect(settingsSource).toContain(
      'import { runtimeFeatures } from "@/runtime/features";',
    );
    expect(settingsSource).toContain(
      '"settingsPage.confirm.developerEnableContentWithoutEvo"',
    );
    expect(settingsSource).toContain(
      '"settingsPage.developer.enableDescWithoutEvo"',
    );
  });
});

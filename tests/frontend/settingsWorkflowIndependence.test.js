import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

const readFrontendSource = (relativePath) => readFileSync(
  new URL(`../../frontend/src/${relativePath}`, import.meta.url),
  "utf8",
);

describe("settings workflow independence", () => {
  it("does not gate workflow controls on the task center", () => {
    const settingsSource = readFrontendSource("modules/settings/index.tsx");
    const resourceSettingsSource = readFrontendSource(
      "modules/settings/UserSkillWorkflowSettings.tsx",
    );

    expect(settingsSource).not.toContain("workflowControlBlocked");
    expect(settingsSource).not.toContain("taskCenterRequiredAria");
    expect(resourceSettingsSource).not.toContain("taskCenterEnabled");
    expect(resourceSettingsSource).not.toContain("taskCenterRequiredNotice");
    expect(resourceSettingsSource).toContain("controlEnabled={workflowsEnabled}");
  });
});

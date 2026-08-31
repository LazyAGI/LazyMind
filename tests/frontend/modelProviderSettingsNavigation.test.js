import { readdirSync, readFileSync } from "node:fs";
import { join, relative } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import { resolveModelNavigationStatus } from "../../frontend/src/modules/settings/modelNavigationStatus.ts";

const frontendSourceRoot = fileURLToPath(
  new URL("../../frontend/src", import.meta.url),
);

function collectSourceFiles(directory) {
  return readdirSync(directory, { withFileTypes: true }).flatMap((entry) => {
    const path = join(directory, entry.name);
    if (entry.isDirectory()) return collectSourceFiles(path);
    return /\.(?:ts|tsx)$/.test(entry.name) ? [path] : [];
  });
}

describe("model provider Settings navigation", () => {
  it("does not report models ready before the overview confirms availability", () => {
    expect(resolveModelNavigationStatus(undefined)).toBeUndefined();
    expect(resolveModelNavigationStatus(false)).toBe("pending");
    expect(resolveModelNavigationStatus(true)).toBe("ready");
  });

  it("does not leave runtime links to the removed model provider pages", () => {
    const staleFiles = collectSourceFiles(frontendSourceRoot)
      .filter((path) => relative(frontendSourceRoot, path) !== "router/index.tsx")
      .filter((path) => readFileSync(path, "utf8").includes("/model-providers"))
      .map((path) => relative(frontendSourceRoot, path));

    expect(staleFiles).toEqual([]);
  });

  it("opens the Models & Services Settings section from chat setup notices", () => {
    const newChatSource = readFileSync(
      join(frontendSourceRoot, "modules/chat/pages/newChat/index.tsx"),
      "utf8",
    );
    const settingsDestinations = newChatSource.match(
      /navigate\("\/settings\?section=models"\)/g,
    );

    expect(settingsDestinations).toHaveLength(2);
  });

  it("supports linking directly to the model provider tab", () => {
    const settingsSource = readFileSync(
      join(frontendSourceRoot, "modules/settings/index.tsx"),
      "utf8",
    );

    expect(settingsSource).toContain(
      'searchParams.get("view") === "providers"',
    );
    expect(settingsSource).toContain(
      '{ section: "models", view: "providers" }',
    );
  });

  it("wires the model navigation badge to the asynchronous overview state", () => {
    const settingsSource = readFileSync(
      join(frontendSourceRoot, "modules/settings/index.tsx"),
      "utf8",
    );

    expect(settingsSource).toContain("resolveModelNavigationStatus(");
    expect(settingsSource).toContain("loading || loadError ? undefined");
    expect(settingsSource).toContain("modelSection?.effective_enabled");
    expect(settingsSource).toContain("onModelSelectionChanged={syncOverview}");
    expect(settingsSource).toContain("onConfigurationChanged={syncOverview}");
    expect(settingsSource).not.toMatch(
      /id: "models"[^\n]+status: t\("settingsPage\.sectionStatus\.ready"\)/,
    );
  });
});

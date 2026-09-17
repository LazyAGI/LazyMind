import { existsSync, readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";

const settingsRoot = resolve(process.cwd(), "src/modules/settings");
const settingsSource = readFileSync(resolve(settingsRoot, "index.tsx"), "utf8");
const routerSource = readFileSync(resolve(process.cwd(), "src/router/index.tsx"), "utf8");
const componentPath = resolve(settingsRoot, "CloudUsageSettings.tsx");
const stylePath = resolve(settingsRoot, "index.scss");

describe("Desktop Cloud usage navigation and visual boundary", () => {
  it("adds Cloud usage as an existing Settings subview rather than a new top-level route", () => {
    expect(settingsSource).toContain('"cloud-usage"');
    expect(settingsSource).toContain("CloudUsageSettings");
    expect(routerSource).not.toMatch(/path=["']\/?cloud(?:-usage|\/usage)/);
  });

  it("keeps Cloud usage invisible until the optional Cloud runtime is available", () => {
	expect(settingsSource).toContain("isDesktopRuntime() && cloudRuntimeAvailable");
	expect(settingsSource).toContain("isCloudBusinessAvailable(await getCloudSession())");
  });

  it("keeps the component and styles inside the Desktop settings system", () => {
    expect(existsSync(componentPath)).toBe(true);
    if (!existsSync(componentPath)) return;

    const componentSource = readFileSync(componentPath, "utf8");
    const styleSource = readFileSync(stylePath, "utf8");
    const combined = `${componentSource}\n${styleSource}`;
    expect(componentSource).toContain("settings-cloud-usage");
    expect(combined).not.toMatch(/LazyCloud\/frontend|features\/console|usage-panel|cloud-web/i);
  });

  it("does not persist private account usage in Renderer storage", () => {
    expect(existsSync(componentPath)).toBe(true);
    if (!existsSync(componentPath)) return;

    const componentSource = readFileSync(componentPath, "utf8");
    const hookSource = readFileSync(resolve(settingsRoot, "hooks/useCloudUsageSettings.ts"), "utf8");
    expect(`${componentSource}\n${hookSource}`).not.toMatch(/localStorage|sessionStorage|indexedDB/i);
  });
});

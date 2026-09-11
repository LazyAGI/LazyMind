import { describe, expect, it } from "vitest";

import {
  DEFAULT_BROWSER_EXTENSION_TARGETS,
  detectPreferredBrowserExtensionTarget,
  resolveBrowserExtensionTargets,
} from "./browserExtensionTarget";

describe("browser extension targets", () => {
  it("detects Microsoft Edge from the Edg user-agent token", () => {
    expect(detectPreferredBrowserExtensionTarget(
      "Mozilla/5.0 Chrome/140.0.0.0 Safari/537.36 Edg/140.0.3485.54",
    )).toBe("edge");
  });

  it("defaults Chromium browsers to Chrome", () => {
    expect(detectPreferredBrowserExtensionTarget(
      "Mozilla/5.0 Chrome/140.0.0.0 Safari/537.36",
    )).toBe("chrome");
  });

  it("falls back to both supported browsers for older backends", () => {
    expect(resolveBrowserExtensionTargets()).toEqual(DEFAULT_BROWSER_EXTENSION_TARGETS);
  });
});

import { resolve } from "node:path";
import { compile } from "sass";
import { describe, expect, it } from "vitest";

const css = compile(resolve(__dirname, "index.scss"), { style: "expanded" }).css;

describe("compact resource menu layout", () => {
  it("keeps the add-resource trigger visible while the composer is idle", () => {
    expect(css).not.toMatch(/chat-input-surface[^}]+chat-add-resource-trigger[^}]+opacity:\s*0/);
  });

  it("keeps the resource popover out of the full-height panel rule", () => {
    const fullHeightRules = [...css.matchAll(/([^{}]+)\{[^{}]*height:\s*min\(420px,[^}]+/gs)];
    expect(fullHeightRules.length).toBeGreaterThan(0);
    for (const fullHeightRule of fullHeightRules) {
      expect(fullHeightRule[1]).not.toContain("chat-add-resource-popover");
    }
  });
});

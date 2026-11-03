import { resolve } from "node:path";
import { compile } from "sass";
import { describe, expect, it } from "vitest";

const css = compile(resolve(__dirname, "index.scss"), { style: "expanded" }).css;

describe("developer settings paused appearance", () => {
  it("uses one neutral surface and removes the active color from saved checked switches", () => {
    expect(css).toMatch(/\.settings-developer-options\.is-master-paused\s*\{[^}]*background:\s*#f3f4f6/);
    expect(css).toMatch(/\.settings-developer-options\.is-master-paused \.settings-detail-row\s*\{[^}]*background:\s*transparent/);
    expect(css).toMatch(/\.settings-developer-options\.is-master-paused \.settings-ref-switch\.ant-switch\.ant-switch-checked\s*\{[^}]*background:\s*#aeb7c4/);
  });
});

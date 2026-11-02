import { resolve } from "node:path";
import { compile } from "sass";
import { describe, expect, it } from "vitest";

const css = compile(resolve(__dirname, "index.scss"), { style: "expanded" }).css;

describe("skills header responsive layout", () => {
  it("moves controls below the title before desktop sidebar widths crowd the copy", () => {
    const sidebarWidthLayout = css.match(/@media \(max-width: 1100px\) \{([\s\S]*?)(?=\n@media|$)/)?.[1];
    expect(sidebarWidthLayout).toMatch(/\.settings-skill-resources-head\s*\{[^}]*grid-template-columns:\s*48px minmax\(0, 1fr\)/);
    expect(sidebarWidthLayout).toMatch(/\.settings-skill-group-controls\s*\{[^}]*grid-column:\s*1\s*\/\s*-1/);
    expect(sidebarWidthLayout).toMatch(/\.settings-skill-group-controls\s*\{[^}]*flex-wrap:\s*wrap/);
  });

  it("retains a single-row header on wide desktops", () => {
    expect(css).toMatch(/\.settings-skill-resources-head\s*\{[^}]*grid-template-columns:\s*48px minmax\(0, 1fr\) auto/);
  });
});

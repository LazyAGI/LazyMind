import fs from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";

const source = fs.readFileSync(
  path.resolve(
    process.cwd(),
    "../../frontend/src/modules/knowledge/pages/detail/index.tsx",
  ),
  "utf8",
);

describe("knowledge detail toolbar", () => {
  it("renders import and batch actions as single dropdown buttons", () => {
    expect(source).not.toContain("<Space.Compact>");
    expect(
      source.match(/className="knowledge-toolbar-dropdown-button"/g),
    ).toHaveLength(2);
  });
});

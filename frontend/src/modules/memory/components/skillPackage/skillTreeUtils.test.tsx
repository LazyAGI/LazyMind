import { describe, expect, it, vi } from "vitest";
import type { SkillTreeNodeRecord } from "../../skillApi";
import {
  buildAntTreeData,
  resolveCreateParentPath,
} from "./skillTreeUtils";

vi.mock("../../skillApi", () => ({ SKILL_MD_PATH: "SKILL.md" }));

const node = (
  name: string,
  path: string,
  type: "file" | "dir",
  children: SkillTreeNodeRecord[] = [],
): SkillTreeNodeRecord => ({
  name,
  path,
  type,
  fileType: type,
  mime: type === "file" ? "text/plain" : "",
  size: 0,
  binary: false,
  blobHash: "",
  children,
});

describe("skill tree creation target", () => {
  it("creates inside a selected directory", () => {
    expect(resolveCreateParentPath("references", new Set(["references"]))).toBe(
      "references",
    );
  });

  it("creates beside a selected file", () => {
    expect(
      resolveCreateParentPath(
        "references/guide.md",
        new Set(["references"]),
      ),
    ).toBe("references");
  });

  it("creates at the root beside a selected root-level file", () => {
    expect(resolveCreateParentPath("SKILL.md", new Set())).toBe("");
  });

  it("creates at the root when the tree selection is empty", () => {
    expect(resolveCreateParentPath("", new Set(["references"]))).toBe("");
  });

  it("allows directories to be selected as creation targets", () => {
    const tree = node("root", "", "dir", [
      node("references", "references", "dir", [
        node("guide.md", "references/guide.md", "file"),
      ]),
    ]);

    const [directory] = buildAntTreeData(tree, new Map());

    expect(directory.selectable).toBe(true);
    expect(directory.children?.[0].selectable).toBe(true);
  });
});

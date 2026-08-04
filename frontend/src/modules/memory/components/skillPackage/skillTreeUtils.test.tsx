import { describe, expect, it } from "vitest";
import {
  buildAntTreeData,
  buildDiffStatusMap,
  buildSkillItemPath,
  collectChangedFilePaths,
  collectSkillTreeDirectories,
  flattenSkillTree,
  getDiffStatusClass,
  isMarkdownSkillFile,
  isTextSkillFile,
  pickDefaultFilePath,
  resolveParentPathFromSelection,
  type SkillTreeFileItem,
} from "./skillTreeUtils";
import type { SkillDiffFileRecord, SkillTreeNodeRecord } from "../../skillApi";

const makeNode = (overrides: Partial<SkillTreeNodeRecord>): SkillTreeNodeRecord => ({
  name: "",
  path: "",
  type: "file",
  fileType: "",
  mime: "",
  size: 0,
  binary: false,
  blobHash: "",
  children: [],
  ...overrides,
});

const sampleTree: SkillTreeNodeRecord = makeNode({
  name: "root",
  path: "",
  type: "dir",
  children: [
    makeNode({ name: "SKILL.md", path: "SKILL.md", type: "file", mime: "text/markdown" }),
    makeNode({
      name: "scripts",
      path: "scripts",
      type: "dir",
      children: [
        makeNode({ name: "run.py", path: "scripts/run.py", type: "file", mime: "text/plain" }),
        makeNode({
          name: "logo.png",
          path: "scripts/logo.png",
          type: "file",
          mime: "image/png",
          binary: true,
        }),
      ],
    }),
  ],
});

describe("flattenSkillTree", () => {
  it("flattens a nested tree into a list of file items, skipping directories", () => {
    const files = flattenSkillTree(sampleTree);
    expect(files.map((file) => file.path)).toEqual([
      "SKILL.md",
      "scripts/run.py",
      "scripts/logo.png",
    ]);
    expect(files.every((file) => file.type === "file")).toBe(true);
  });

  it("returns an empty list for an empty directory", () => {
    expect(flattenSkillTree(makeNode({ name: "empty", type: "dir" }))).toEqual([]);
  });
});

describe("buildDiffStatusMap", () => {
  it("maps each file path to its status", () => {
    const files: SkillDiffFileRecord[] = [
      { path: "a.md", status: "added" } as SkillDiffFileRecord,
      { path: "b.md", status: "modified" } as SkillDiffFileRecord,
    ];
    const map = buildDiffStatusMap(files);
    expect(map.get("a.md")).toBe("added");
    expect(map.get("b.md")).toBe("modified");
  });

  it("skips entries without a path", () => {
    const files: SkillDiffFileRecord[] = [{ path: "", status: "added" } as SkillDiffFileRecord];
    expect(buildDiffStatusMap(files).size).toBe(0);
  });
});

describe("isTextSkillFile", () => {
  it("returns true for known text extensions", () => {
    const item: SkillTreeFileItem = {
      path: "a.md",
      name: "a.md",
      type: "file",
      binary: false,
      mime: "",
      fileType: "",
    };
    expect(isTextSkillFile(item)).toBe(true);
  });

  it("returns false for binary files even with a text extension", () => {
    const item: SkillTreeFileItem = {
      path: "a.md",
      name: "a.md",
      type: "file",
      binary: true,
      mime: "",
      fileType: "",
    };
    expect(isTextSkillFile(item)).toBe(false);
  });

  it("returns false for unrecognized extensions", () => {
    const item: SkillTreeFileItem = {
      path: "a.bin",
      name: "a.bin",
      type: "file",
      binary: false,
      mime: "",
      fileType: "",
    };
    expect(isTextSkillFile(item)).toBe(false);
  });
});

describe("isMarkdownSkillFile", () => {
  it("detects markdown by extension", () => {
    const item: SkillTreeFileItem = {
      path: "a.md",
      name: "a.md",
      type: "file",
      binary: false,
      mime: "",
      fileType: "",
    };
    expect(isMarkdownSkillFile(item)).toBe(true);
  });

  it("detects markdown by mime type", () => {
    const item: SkillTreeFileItem = {
      path: "a.txt",
      name: "a.txt",
      type: "file",
      binary: false,
      mime: "text/markdown",
      fileType: "",
    };
    expect(isMarkdownSkillFile(item)).toBe(true);
  });

  it("returns false for non-markdown files", () => {
    const item: SkillTreeFileItem = {
      path: "a.py",
      name: "a.py",
      type: "file",
      binary: false,
      mime: "text/plain",
      fileType: "",
    };
    expect(isMarkdownSkillFile(item)).toBe(false);
  });
});

describe("pickDefaultFilePath", () => {
  it("prefers SKILL.md when present", () => {
    const files = flattenSkillTree(sampleTree);
    expect(pickDefaultFilePath(files)).toBe("SKILL.md");
  });

  it("falls back to the first file when SKILL.md is absent", () => {
    const files: SkillTreeFileItem[] = [
      { path: "a.py", name: "a.py", type: "file", binary: false, mime: "", fileType: "" },
    ];
    expect(pickDefaultFilePath(files)).toBe("a.py");
  });

  it("returns an empty string when there are no files", () => {
    expect(pickDefaultFilePath([])).toBe("");
  });
});

describe("getDiffStatusClass", () => {
  it("maps known statuses to CSS classes", () => {
    expect(getDiffStatusClass("added")).toBe("is-added");
    expect(getDiffStatusClass("modified")).toBe("is-modified");
    expect(getDiffStatusClass("deleted")).toBe("is-deleted");
    expect(getDiffStatusClass("renamed")).toBe("is-modified");
  });

  it("returns an empty string for unknown or missing status", () => {
    expect(getDiffStatusClass("unchanged")).toBe("");
    expect(getDiffStatusClass(undefined)).toBe("");
  });
});

describe("buildAntTreeData", () => {
  it("builds tree nodes with icons and leaf flags matching directory structure", () => {
    const treeData = buildAntTreeData(sampleTree, new Map());
    expect(treeData).toHaveLength(2);
    const [skillNode, scriptsNode] = treeData;
    expect(skillNode.key).toBe("SKILL.md");
    expect(skillNode.isLeaf).toBe(true);
    expect(scriptsNode.key).toBe("scripts");
    expect(scriptsNode.isLeaf).toBe(false);
    expect(scriptsNode.children).toHaveLength(2);
  });

  it("applies a diff status class name when a status map entry matches", () => {
    const diffStatusMap = new Map([["SKILL.md", "modified"]]);
    const treeData = buildAntTreeData(sampleTree, diffStatusMap);
    expect(treeData[0].className).toBe("is-modified");
  });

  it("uses a custom renderTitle callback when provided", () => {
    const treeData = buildAntTreeData(sampleTree, new Map(), (item) => `custom:${item.name}`);
    expect(treeData[0].title).toBe("custom:SKILL.md");
  });
});

describe("collectChangedFilePaths", () => {
  it("filters out unchanged files and returns changed paths", () => {
    const files: SkillDiffFileRecord[] = [
      { path: "a.md", status: "unchanged" } as SkillDiffFileRecord,
      { path: "b.md", status: "modified" } as SkillDiffFileRecord,
      { path: "c.md", status: "" } as SkillDiffFileRecord,
    ];
    expect(collectChangedFilePaths(files)).toEqual(["b.md"]);
  });

  it("returns an empty array when nothing changed", () => {
    const files: SkillDiffFileRecord[] = [
      { path: "a.md", status: "unchanged" } as SkillDiffFileRecord,
    ];
    expect(collectChangedFilePaths(files)).toEqual([]);
  });
});

describe("collectSkillTreeDirectories", () => {
  it("collects sorted directory paths, excluding the file leaves", () => {
    const dirs = collectSkillTreeDirectories(sampleTree);
    expect(dirs).toEqual(["scripts"]);
  });

  it("returns an empty array for a tree with no nested directories", () => {
    const flatTree = makeNode({
      name: "root",
      type: "dir",
      path: "",
      children: [makeNode({ name: "a.md", path: "a.md", type: "file" })],
    });
    expect(collectSkillTreeDirectories(flatTree)).toEqual([]);
  });
});

describe("resolveParentPathFromSelection", () => {
  it("returns the parent directory of a nested path", () => {
    expect(resolveParentPathFromSelection("scripts/run.py")).toBe("scripts");
  });

  it("returns an empty string for a top-level path", () => {
    expect(resolveParentPathFromSelection("SKILL.md")).toBe("");
  });

  it("returns an empty string for an empty input", () => {
    expect(resolveParentPathFromSelection("")).toBe("");
  });
});

describe("buildSkillItemPath", () => {
  it("joins a parent path and a name", () => {
    expect(buildSkillItemPath("scripts", "run.py")).toBe("scripts/run.py");
  });

  it("returns just the trimmed name when there is no parent path", () => {
    expect(buildSkillItemPath("", "SKILL.md")).toBe("SKILL.md");
  });

  it("strips leading/trailing slashes from both segments", () => {
    expect(buildSkillItemPath("/scripts/", "/run.py/")).toBe("scripts/run.py");
  });

  it("returns an empty string when the name is empty after trimming", () => {
    expect(buildSkillItemPath("scripts", "   ")).toBe("");
  });
});

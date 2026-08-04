import { describe, expect, it } from "vitest";
import {
  buildTreeValuePathMap,
  buildTreeValueTitleMap,
  collapseSelectedTreeValues,
  collectTreeExpandableKeys,
  getTreeSelectLabelText,
  normalizeTreeSelectValues,
  type CollapsibleTreeNode,
} from "./treeSelectUtils";

describe("normalizeTreeSelectValues", () => {
  it("wraps a single value and trims/filters an array of values", () => {
    expect(normalizeTreeSelectValues("a")).toEqual(["a"]);
    expect(normalizeTreeSelectValues([" a ", "", "b"])).toEqual(["a", "b"]);
  });

  it("returns an empty array for nullish input", () => {
    expect(normalizeTreeSelectValues(undefined)).toEqual([]);
  });
});

describe("collapseSelectedTreeValues", () => {
  const treeData: CollapsibleTreeNode[] = [
    {
      value: "parent",
      title: "Parent",
      children: [
        { value: "child-1", title: "Child 1" },
        { value: "child-2", title: "Child 2" },
      ],
    },
  ];

  it("removes descendant values when their ancestor is already selected", () => {
    expect(collapseSelectedTreeValues(["parent", "child-1"], treeData)).toEqual([
      "parent",
    ]);
  });

  it("keeps unrelated selected values untouched", () => {
    expect(collapseSelectedTreeValues(["child-1", "child-2"], treeData)).toEqual([
      "child-1",
      "child-2",
    ]);
  });
});

describe("buildTreeValueTitleMap / buildTreeValuePathMap", () => {
  const treeData: CollapsibleTreeNode[] = [
    {
      value: "root",
      title: "Root",
      children: [{ value: "leaf", title: "Leaf" }],
    },
  ];

  it("maps each node value to its title", () => {
    const titleMap = buildTreeValueTitleMap(treeData);
    expect(titleMap.get("root")).toBe("Root");
    expect(titleMap.get("leaf")).toBe("Leaf");
  });

  it("maps each node value to its full breadcrumb path", () => {
    const pathMap = buildTreeValuePathMap(treeData);
    expect(pathMap.get("leaf")).toBe("Root / Leaf");
  });
});

describe("getTreeSelectLabelText", () => {
  it("returns a trimmed string/number label as text", () => {
    expect(getTreeSelectLabelText(" hi ")).toBe("hi");
    expect(getTreeSelectLabelText(42)).toBe("42");
  });

  it("returns an empty string for non-primitive labels", () => {
    expect(getTreeSelectLabelText(undefined)).toBe("");
  });
});

describe("collectTreeExpandableKeys", () => {
  it("collects keys of nodes that have children", () => {
    const nodes: CollapsibleTreeNode[] = [
      { key: "a", children: [{ key: "b" }] },
      { key: "c" },
    ];
    expect(collectTreeExpandableKeys(nodes)).toEqual(["a"]);
  });

  it("falls back to value when key is missing", () => {
    const nodes: CollapsibleTreeNode[] = [{ value: "v1", children: [{ value: "v2" }] }];
    expect(collectTreeExpandableKeys(nodes)).toEqual(["v1"]);
  });
});

import { describe, expect, it } from "vitest";
import TreeUtils from "./tree";

describe("TreeUtils.arrayToTree", () => {
  it("builds a nested tree from a flat array using parentId", () => {
    const array = [
      { id: "1", parentId: "" },
      { id: "2", parentId: "1" },
      { id: "3", parentId: "1" },
      { id: "4", parentId: "2" },
    ];

    const tree = TreeUtils.arrayToTree({ array });

    expect(tree).toHaveLength(1);
    expect(tree[0].id).toBe("1");
    expect(tree[0].level).toBe(0);
    expect(tree[0].children).toHaveLength(2);
    expect(tree[0].children[0].id).toBe("2");
    expect(tree[0].children[0].level).toBe(1);
    expect(tree[0].children[0].children).toHaveLength(1);
    expect(tree[0].children[0].children[0].id).toBe("4");
    expect(tree[0].children[0].children[0].level).toBe(2);
  });

  it("does not attach a children field for leaf nodes", () => {
    const array = [{ id: "1", parentId: "" }];
    const tree = TreeUtils.arrayToTree({ array });
    expect(tree[0].children).toBeUndefined();
  });

  it("returns an empty array when no items match the given parentId", () => {
    const array = [{ id: "1", parentId: "other" }];
    expect(TreeUtils.arrayToTree({ array, parentId: "root" })).toEqual([]);
  });
});

describe("TreeUtils.findNode", () => {
  const treeList = [
    {
      id: "1",
      children: [
        { id: "2", children: [{ id: "3" }] },
        { id: "4" },
      ],
    },
  ];

  it("finds a top-level node by predicate", () => {
    const found = TreeUtils.findNode(treeList, (node) => node.id === "1");
    expect(found?.id).toBe("1");
  });

  it("finds a deeply nested node by predicate", () => {
    const found = TreeUtils.findNode(treeList, (node) => node.id === "3");
    expect(found?.id).toBe("3");
  });

  it("returns undefined when no node matches", () => {
    expect(TreeUtils.findNode(treeList, (node) => node.id === "missing")).toBeUndefined();
  });
});

describe("TreeUtils.findAncestorFolderIds", () => {
  const tree = [
    {
      document_id: "root",
      type: "FOLDER",
      children: [
        {
          document_id: "sub-folder",
          type: "FOLDER",
          children: [{ document_id: "leaf-doc", type: "FILE" }],
        },
        { document_id: "sibling-doc", type: "FILE" },
      ],
    },
  ];

  it("returns the chain of ancestor folder ids for a nested target", () => {
    expect(TreeUtils.findAncestorFolderIds(tree, "leaf-doc")).toEqual([
      "root",
      "sub-folder",
    ]);
  });

  it("returns an empty array when the target is not found", () => {
    expect(TreeUtils.findAncestorFolderIds(tree, "unknown")).toEqual([]);
  });

  it("excludes non-folder ancestors from the result", () => {
    expect(TreeUtils.findAncestorFolderIds(tree, "sibling-doc")).toEqual(["root"]);
  });
});

describe("TreeUtils.findParents", () => {
  const treeList = [
    {
      key: "a",
      children: [
        { key: "b", children: [{ key: "c" }] },
      ],
    },
    { key: "d" },
  ];

  it("returns the path of nodes from root to the target key", () => {
    const path = TreeUtils.findParents(treeList, "c");
    expect(path.map((n: any) => n.key)).toEqual(["a", "b", "c"]);
  });

  it("returns a single-element path when the key is a top-level node", () => {
    const path = TreeUtils.findParents(treeList, "d");
    expect(path.map((n: any) => n.key)).toEqual(["d"]);
  });

  it("returns an empty array when the key does not exist", () => {
    expect(TreeUtils.findParents(treeList, "missing")).toEqual([]);
  });
});

describe("TreeUtils.flattenTree", () => {
  it("flattens a nested tree into a single-level array in DFS order", () => {
    const treeList = [
      { key: "a", children: [{ key: "b" }, { key: "c" }] },
      { key: "d" },
    ];
    const flat = TreeUtils.flattenTree(treeList);
    expect(flat.map((n: any) => n.key)).toEqual(["a", "b", "c", "d"]);
  });

  it("returns an empty array for an empty tree", () => {
    expect(TreeUtils.flattenTree([])).toEqual([]);
  });
});

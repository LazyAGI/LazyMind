import { describe, expect, it } from "vitest";
import {
  buildSyncGenerateScopes,
  collectScanTreeFileKeys,
  collectScanTreeNodesByKey,
  filterScanTreeChildren,
  getScanTreeNodeKey,
  getScanTreeNodeMergeKeys,
  getScanTreeNodePage,
  getScanTreeNodeParentKey,
  getTreeNodeUpdateState,
  isSelectableScanTreeDocument,
  mergeScanTreeChildren,
  normalizeLazyScanTreeNodes,
  shouldPollDocumentStatus,
} from "./scanTree";
import type { DocumentStatusRow } from "../constants/types";

describe("isSelectableScanTreeDocument", () => {
  it("requires selectable !== false and is_document === true", () => {
    expect(isSelectableScanTreeDocument({ is_document: true } as never)).toBe(true);
    expect(
      isSelectableScanTreeDocument({ is_document: true, selectable: false } as never),
    ).toBe(false);
    expect(isSelectableScanTreeDocument({ is_document: false } as never)).toBe(false);
  });
});

describe("getScanTreeNodeKey / getScanTreeNodeParentKey", () => {
  it("uses key or falls back to object_key", () => {
    expect(getScanTreeNodeKey({ key: "k1" } as never)).toBe("k1");
    expect(getScanTreeNodeKey({ object_key: "o1" } as never)).toBe("o1");
  });

  it("scopes parent key by binding id when both are present", () => {
    expect(
      getScanTreeNodeParentKey({ parent_key: "p1", binding_id: "b1" } as never),
    ).toBe("b1:p1");
    expect(getScanTreeNodeParentKey({ parent_key: "p1" } as never)).toBe("p1");
    expect(getScanTreeNodeParentKey({} as never)).toBe("");
  });
});

describe("collectScanTreeFileKeys", () => {
  it("collects only selectable document leaf keys across nested children", () => {
    const nodes = [
      {
        key: "folder-1",
        is_document: false,
        children: [
          { key: "doc-1", is_document: true },
          { key: "doc-2", is_document: true, selectable: false },
        ],
      },
      { key: "doc-3", is_document: true },
    ] as never;
    expect(collectScanTreeFileKeys(nodes)).toEqual(["doc-1", "doc-3"]);
  });
});

describe("collectScanTreeNodesByKey", () => {
  it("indexes nodes by key and by binding-scoped object key", () => {
    const nodes = [
      { key: "k1", object_key: "o1", binding_id: "b1", children: [{ key: "k2" }] },
    ] as never;
    const byKey = collectScanTreeNodesByKey(nodes);
    expect(byKey.get("k1")).toBeTruthy();
    expect(byKey.get("b1:o1")).toBeTruthy();
    expect(byKey.get("k2")).toBeTruthy();
  });
});

describe("getScanTreeNodePage", () => {
  it("reads items/next_cursor from a flat payload", () => {
    const page = getScanTreeNodePage({ items: [{ key: "a" }], next_cursor: "c1" });
    expect(page).toEqual({ items: [{ key: "a" }], nextCursor: "c1" });
  });

  it("reads items/next_cursor nested under data", () => {
    const page = getScanTreeNodePage({ data: { items: [{ key: "a" }] } });
    expect(page.items).toEqual([{ key: "a" }]);
    expect(page.nextCursor).toBe("");
  });

  it("defaults to empty items for unrecognized payloads", () => {
    expect(getScanTreeNodePage({})).toEqual({ items: [], nextCursor: "" });
  });
});

describe("getScanTreeNodeMergeKeys", () => {
  it("collects all identity candidates, trimmed and deduplicated of blanks", () => {
    expect(
      getScanTreeNodeMergeKeys({ key: "k1", object_key: "", node_ref: "n1" } as never),
    ).toEqual(["k1", "k1", "n1"]);
  });
});

describe("normalizeLazyScanTreeNodes", () => {
  it("strips children from each node without mutating the original", () => {
    const original = [{ key: "k1", children: [{ key: "k2" }] }] as never;
    const result = normalizeLazyScanTreeNodes(original);
    expect(result[0]).not.toHaveProperty("children");
    expect(original[0]).toHaveProperty("children");
  });
});

describe("filterScanTreeChildren", () => {
  it("excludes the parent node itself and keeps direct children", () => {
    const children = [
      { key: "parent-1" },
      { key: "child-1", parent_key: "parent-1" },
      { key: "child-2", parent_key: "other-parent" },
      { key: "child-3" },
    ] as never;
    expect(filterScanTreeChildren("parent-1", children)).toEqual([
      { key: "child-1", parent_key: "parent-1" },
      { key: "child-3" },
    ]);
  });
});

describe("buildSyncGenerateScopes", () => {
  it("falls back to object_key scope when node lookup misses", () => {
    const scopes = buildSyncGenerateScopes(["missing-key"], new Map());
    expect(scopes).toEqual([{ object_key: "missing-key" }]);
  });

  it("skips a node whose ancestor is already selected", () => {
    const nodeByKey = new Map([
      ["child", { key: "child", parent_key: "parent" }],
      ["parent", { key: "parent" }],
    ]) as never;
    const scopes = buildSyncGenerateScopes(["parent", "child"], nodeByKey);
    expect(scopes).toHaveLength(1);
    expect(scopes[0]).toMatchObject({ key: "parent" });
  });

  it("builds a full scope descriptor for an unselected-ancestor node", () => {
    const nodeByKey = new Map([
      [
        "leaf",
        {
          key: "leaf",
          object_key: "obj-1",
          node_ref: "ref-1",
          is_document: true,
          binding_id: "b1",
        },
      ],
    ]) as never;
    const scopes = buildSyncGenerateScopes(["leaf"], nodeByKey);
    expect(scopes).toEqual([
      {
        key: "leaf",
        object_key: "obj-1",
        node_ref: "ref-1",
        is_document: true,
        is_container: false,
        bindingId: "b1",
      },
    ]);
  });
});

describe("mergeScanTreeChildren", () => {
  it("merges children into the matching node at any depth", () => {
    const nodes = [
      { key: "root", children: [{ key: "child" }] },
    ] as never;
    const result = mergeScanTreeChildren(nodes, "child", [{ key: "grandchild" }] as never);
    expect(result[0].children[0].children).toEqual([{ key: "grandchild" }]);
  });

  it("returns nodes unchanged when the parent key is not found", () => {
    const nodes = [{ key: "root" }] as never;
    expect(mergeScanTreeChildren(nodes, "missing", [] as never)).toEqual(nodes);
  });
});

describe("getTreeNodeUpdateState", () => {
  it("derives update state from update_type/source_state and has_update", () => {
    expect(getTreeNodeUpdateState({ update_type: "new" } as never)).toBe("new");
    expect(
      getTreeNodeUpdateState({ source_state: "UNCHANGED" } as never),
    ).toBe("unchanged");
  });
});

describe("shouldPollDocumentStatus", () => {
  it("returns true if any item is reindexing/downloading/pending/running", () => {
    const items = [{ parseStatus: "parsed", syncState: "IDLE" }] as DocumentStatusRow[];
    expect(shouldPollDocumentStatus(items)).toBe(false);
    expect(
      shouldPollDocumentStatus([
        { parseStatus: "reindexing", syncState: "IDLE" },
      ] as DocumentStatusRow[]),
    ).toBe(true);
    expect(
      shouldPollDocumentStatus([
        { parseStatus: "parsed", syncState: "RUNNING" },
      ] as DocumentStatusRow[]),
    ).toBe(true);
  });
});

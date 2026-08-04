import { describe, expect, it } from "vitest";
import {
  buildFeishuTargetSeedNodes,
  buildFeishuTargetTreeFromScanNodes,
  collectFeishuTargetRefs,
  collectFeishuTargetTypes,
  getFeishuBindingTargetTypes,
  hasFeishuTargetTypes,
  mapFeishuScanNodeToTreeNode,
  normalizeCloudTargetRefs,
  normalizeFeishuTargetRef,
  normalizeFeishuTargetRefs,
  normalizeFeishuTargetType,
  normalizeFeishuTargetTypeRecord,
  normalizeLocalPathRefs,
  normalizeNotionTargetType,
  parseManualFeishuTargetValue,
  resolveSourceTypeFromValues,
  toScanFeishuTargetType,
  toUiFeishuTargetType,
  type FeishuTargetTreeNode,
} from "./feishuTarget";
import type { ScanV2Binding, ScanV2TreeNode } from "./scanAccessors";

describe("normalizeFeishuTargetType", () => {
  it("infers drive_folder / wiki_space from the targetRef prefix", () => {
    expect(normalizeFeishuTargetType(undefined, "feishu:drive:123")).toBe(
      "drive_folder",
    );
    expect(normalizeFeishuTargetType(undefined, "feishu:wiki:123")).toBe("wiki_space");
  });

  it("normalizes explicit target type strings", () => {
    expect(normalizeFeishuTargetType("folder")).toBe("drive_folder");
    expect(normalizeFeishuTargetType("wiki_node")).toBe("wiki_space");
  });

  it("returns undefined when nothing matches", () => {
    expect(normalizeFeishuTargetType("unknown", "unknown")).toBeUndefined();
  });
});

describe("toScanFeishuTargetType / toUiFeishuTargetType", () => {
  it("maps wiki_space to wiki_node for the scan API and back for the UI", () => {
    expect(toScanFeishuTargetType("wiki_space")).toBe("wiki_node");
    expect(toScanFeishuTargetType("drive_folder")).toBe("drive_folder");
    expect(toUiFeishuTargetType("wiki_node")).toBe("wiki_space");
  });
});

describe("normalizeFeishuTargetRef", () => {
  it("rewrites 4-segment feishu:wiki:<space>:<node> refs to wiki:<space>:<node>", () => {
    expect(normalizeFeishuTargetRef("feishu:wiki:space1:node1")).toBe(
      "wiki:space1:node1",
    );
  });

  it("leaves other refs unchanged", () => {
    expect(normalizeFeishuTargetRef("feishu:drive:folder1")).toBe(
      "feishu:drive:folder1",
    );
  });
});

describe("parseManualFeishuTargetValue", () => {
  const prefix = "__scan-feishu-manual-target__";

  it("parses a wiki manual target value", () => {
    expect(parseManualFeishuTargetValue(`${prefix}:wiki:node-1`)).toEqual({
      kind: "wiki",
      targetRef: "node-1",
      targetType: "wiki_space",
    });
  });

  it("parses a drive manual target value", () => {
    expect(parseManualFeishuTargetValue(`${prefix}:drive:folder-1`)).toMatchObject({
      kind: "drive",
      targetType: "drive_folder",
    });
  });

  it("returns null for values without the manual prefix or an invalid kind", () => {
    expect(parseManualFeishuTargetValue("not-manual")).toBeNull();
    expect(parseManualFeishuTargetValue(`${prefix}:bogus:x`)).toBeNull();
  });
});

describe("normalizeNotionTargetType", () => {
  it("normalizes legacy and modern notion target type strings", () => {
    expect(normalizeNotionTargetType("notion_database")).toBe("database");
    expect(normalizeNotionTargetType("page")).toBe("page");
  });

  it("returns undefined for unrecognized values", () => {
    expect(normalizeNotionTargetType("bogus")).toBeUndefined();
  });
});

describe("collectFeishuTargetTypes / collectFeishuTargetRefs", () => {
  const nodes: FeishuTargetTreeNode[] = [
    {
      key: "n1",
      value: "n1",
      targetRef: "feishu:wiki:s1",
      children: [{ key: "n2", value: "n2", targetRef: "feishu:wiki:s1:c1" }],
    } as FeishuTargetTreeNode,
  ];

  it("propagates inherited target types down to children", () => {
    const targetTypes = collectFeishuTargetTypes(nodes);
    expect(targetTypes.get("n1")).toBe("wiki_space");
    expect(targetTypes.get("n2")).toBe("wiki_space");
  });

  it("normalizes target refs recursively", () => {
    const targetRefs = collectFeishuTargetRefs(nodes);
    expect(targetRefs.get("n2")).toBe("wiki:s1:c1");
  });
});

describe("normalizeFeishuTargetRefs / normalizeCloudTargetRefs / normalizeLocalPathRefs", () => {
  it("normalizes a single value or array of feishu target refs", () => {
    expect(normalizeFeishuTargetRefs("feishu:wiki:s1:c1")).toEqual(["wiki:s1:c1"]);
    expect(normalizeFeishuTargetRefs(["a", " b "])).toEqual(["a", "b"]);
  });

  it("splits newline-delimited cloud target refs and trims them", () => {
    expect(normalizeCloudTargetRefs("a\nb\n\nc")).toEqual(["a", "b", "c"]);
  });

  it("normalizes local path refs, trimming and filtering blanks", () => {
    expect(normalizeLocalPathRefs([" /a ", "", "/b"])).toEqual(["/a", "/b"]);
  });
});

describe("buildFeishuTargetSeedNodes", () => {
  it("builds leaf nodes using cached labels/target types when available", () => {
    const nodes = buildFeishuTargetSeedNodes(["t1"], {
      targetLabels: { t1: "Label 1" },
      targetTypes: { t1: "drive_folder" },
      targetType: undefined,
    });
    expect(nodes[0]).toMatchObject({ value: "t1", title: "Label 1", targetType: "drive_folder" });
  });

  it("skips blank values", () => {
    expect(buildFeishuTargetSeedNodes(["  "], {})).toEqual([]);
  });
});

describe("hasFeishuTargetTypes / getFeishuBindingTargetTypes / normalizeFeishuTargetTypeRecord", () => {
  it("detects whether a target type record has any valid feishu type", () => {
    expect(hasFeishuTargetTypes({ a: "drive_folder" })).toBe(true);
    expect(hasFeishuTargetTypes({ a: "bogus" })).toBe(false);
    expect(hasFeishuTargetTypes(undefined)).toBe(false);
  });

  it("builds a target-ref -> target-type map from bindings", () => {
    const bindings: ScanV2Binding[] = [
      { target_ref: "t1", target_type: "wiki_node" } as ScanV2Binding,
    ];
    expect(getFeishuBindingTargetTypes(bindings)).toEqual({ t1: "wiki_space" });
  });

  it("normalizes a raw target type record and returns undefined when nothing is valid", () => {
    expect(normalizeFeishuTargetTypeRecord({ t1: "folder" })).toEqual({
      t1: "drive_folder",
    });
    expect(normalizeFeishuTargetTypeRecord({ t1: "bogus" })).toBeUndefined();
    expect(normalizeFeishuTargetTypeRecord(undefined)).toBeUndefined();
  });
});

describe("mapFeishuScanNodeToTreeNode", () => {
  it("maps a scan node into a tree node, inferring isLeaf from has_children", () => {
    const node: ScanV2TreeNode = {
      key: "n1",
      display_name: "Node 1",
      has_children: true,
      target_ref: "feishu:wiki:s1",
    } as ScanV2TreeNode;
    const result = mapFeishuScanNodeToTreeNode(node);
    expect(result).toMatchObject({
      value: "feishu:wiki:s1",
      title: "Node 1",
      isLeaf: false,
      targetType: "wiki_space",
    });
  });

  it("returns a node keyed by 'undefined' when identity fields are all missing", () => {
    const result = mapFeishuScanNodeToTreeNode({} as ScanV2TreeNode);
    expect(result).toMatchObject({ value: "undefined", title: "undefined" });
  });
});

describe("buildFeishuTargetTreeFromScanNodes", () => {
  it("returns an empty array for an empty node list", () => {
    expect(buildFeishuTargetTreeFromScanNodes([])).toEqual([]);
  });

  it("builds a nested tree when nodes already carry embedded children", () => {
    const nodes: ScanV2TreeNode[] = [
      {
        key: "root",
        display_name: "Root",
        children: [{ key: "child", display_name: "Child" }],
      } as ScanV2TreeNode,
    ];
    const tree = buildFeishuTargetTreeFromScanNodes(nodes);
    expect(tree).toHaveLength(1);
    expect(tree[0].children?.[0]?.title).toBe("Child");
  });

  it("builds a flat-to-tree structure using parent_key when there are no embedded children", () => {
    const nodes: ScanV2TreeNode[] = [
      { key: "root", object_key: "root", display_name: "Root" } as ScanV2TreeNode,
      {
        key: "child",
        object_key: "child",
        parent_key: "root",
        display_name: "Child",
      } as ScanV2TreeNode,
    ];
    const tree = buildFeishuTargetTreeFromScanNodes(nodes);
    expect(tree).toHaveLength(1);
    expect(tree[0].children?.[0]?.title).toBe("Child");
  });
});

describe("resolveSourceTypeFromValues", () => {
  it("resolves to local when local paths are present without feishu targets", () => {
    expect(
      resolveSourceTypeFromValues(null, { path: ["/a"], target: [] } as never),
    ).toBe("local");
  });

  it("resolves to feishu when feishu-shaped targets are present without local paths", () => {
    expect(
      resolveSourceTypeFromValues(null, {
        path: [],
        target: ["feishu:wiki:s1:c1"],
      } as never),
    ).toBe("feishu");
  });

  it("keeps the fallback type when nothing distinguishes the source", () => {
    expect(resolveSourceTypeFromValues("database", { path: [], target: [] } as never)).toBe(
      "database",
    );
  });
});

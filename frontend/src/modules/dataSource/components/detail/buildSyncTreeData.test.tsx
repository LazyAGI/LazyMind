import { describe, expect, it } from "vitest";
import type { TFunction } from "i18next";
import { render, screen } from "@testing-library/react";
import { buildSyncTreeData } from "./buildSyncTreeData";
import type { ScanV2TreeNode } from "../../utils/scanAccessors";

const t = ((key: string) => key) as TFunction;

describe("buildSyncTreeData", () => {
  it("returns an empty array for empty input", () => {
    expect(buildSyncTreeData([], t)).toEqual([]);
  });

  it("maps a leaf document node into a sync tree data node", () => {
    const node: ScanV2TreeNode = {
      key: "doc-1",
      object_key: "doc-1",
      display_name: "report.pdf",
      is_document: true,
      selectable: true,
      has_children: false,
    };

    const [result] = buildSyncTreeData([node], t);
    expect(result.key).toBe("doc-1");
    expect(result.isLeaf).toBe(true);
    expect(result.disableCheckbox).toBe(false);
    expect((result as { childrenLoaded?: boolean }).childrenLoaded).toBe(false);
    render(<div>{result.title as React.ReactNode}</div>);
    expect(screen.getByText("report.pdf")).toBeInTheDocument();
  });

  it("marks non-document nodes as non-selectable via disableCheckbox", () => {
    const node: ScanV2TreeNode = {
      key: "folder-1",
      object_key: "folder-1",
      display_name: "Folder",
      is_document: false,
      is_container: true,
      has_children: true,
    };

    const [result] = buildSyncTreeData([node], t);
    expect(result.disableCheckbox).toBe(true);
    expect(result.isLeaf).toBe(false);
  });

  it("recursively maps nested children and marks childrenLoaded", () => {
    const child: ScanV2TreeNode = {
      key: "doc-child",
      object_key: "doc-child",
      display_name: "child.pdf",
      is_document: true,
      selectable: true,
    };
    const parent: ScanV2TreeNode = {
      key: "folder-1",
      object_key: "folder-1",
      display_name: "Folder",
      is_container: true,
      has_children: true,
      children: [child],
    };

    const [result] = buildSyncTreeData([parent], t);
    expect((result as { childrenLoaded?: boolean }).childrenLoaded).toBe(true);
    expect(result.children).toHaveLength(1);
    expect(result.children?.[0].key).toBe("doc-child");
  });

  it("renders an update status chip when the node has update metadata", () => {
    const node: ScanV2TreeNode = {
      key: "doc-1",
      object_key: "doc-1",
      display_name: "updated.pdf",
      is_document: true,
      selectable: true,
      update_type: "new",
      has_update: true,
    };

    const [result] = buildSyncTreeData([node], t);
    render(<div>{result.title as React.ReactNode}</div>);
    expect(screen.getByText("admin.dataSourceFileUpdateNew")).toBeInTheDocument();
  });

  it("does not render an update chip when there is no update status", () => {
    const node: ScanV2TreeNode = {
      key: "doc-1",
      object_key: "doc-1",
      display_name: "plain.pdf",
      is_document: true,
      selectable: true,
    };

    const [result] = buildSyncTreeData([node], t);
    render(<div>{result.title as React.ReactNode}</div>);
    expect(screen.queryByText(/admin.dataSourceFileUpdate/)).not.toBeInTheDocument();
  });
});

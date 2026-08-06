import { act, renderHook, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { TFunction } from "i18next";
import type { DataSourceSummary, DocumentStatusRow } from "../constants/types";

const listSourceTreeChildrenMock = vi.fn();
const searchSourceTreeMock = vi.fn();
const generateParseTasksMock = vi.fn();

vi.mock("../api/clients", () => ({
  dataSourceScanApi: {
    listSourceTreeChildren: (...args: unknown[]) => listSourceTreeChildrenMock(...args),
    searchSourceTree: (...args: unknown[]) => searchSourceTreeMock(...args),
    generateParseTasks: (...args: unknown[]) => generateParseTasksMock(...args),
  },
}));

import { useSyncPicker } from "./useSyncPicker";

const t = ((key: string) => key) as TFunction;

function makeDetailSource(overrides: Partial<DataSourceSummary> = {}): DataSourceSummary {
  return {
    id: "src-1",
    name: "My Source",
    target: "/root",
    sourceType: "local",
    documentCount: 1,
    parsedDocumentCount: 1,
    status: "active",
    lastSync: "-",
    addCount: 0,
    deleteCount: 0,
    changeCount: 0,
    storageUsed: "0 B",
    documents: [],
    scanManaged: true,
    bindingId: "bind-1",
    bindingTreeKey: "",
    configVersion: 0,
    ...overrides,
  } as DataSourceSummary;
}

function makeHookParams(overrides: Record<string, unknown> = {}) {
  return {
    t,
    id: "src-1",
    routeSource: undefined,
    detailSource: makeDetailSource(),
    documents: [] as DocumentStatusRow[],
    setDocuments: vi.fn(),
    setLastSync: vi.fn(),
    setLastOperation: vi.fn(),
    stopSyncPolling: vi.fn(),
    startSyncPolling: vi.fn(),
    refreshDetailFromServer: vi.fn().mockResolvedValue([]),
    resetSyncStateToken: 0,
    ...overrides,
  };
}

describe("useSyncPicker", () => {
  beforeEach(() => {
    listSourceTreeChildrenMock.mockReset();
    searchSourceTreeMock.mockReset();
    generateParseTasksMock.mockReset();
  });

  it("opens the picker and loads the tree from listSourceTreeChildren", async () => {
    listSourceTreeChildrenMock.mockResolvedValue({
      data: { items: [{ key: "doc-1", object_key: "doc-1", display_name: "a.pdf", is_document: true }] },
    });

    const { result } = renderHook(() => useSyncPicker(makeHookParams()));

    act(() => {
      result.current.openSyncPicker();
    });

    expect(result.current.syncPickerOpen).toBe(true);

    await waitFor(() => expect(result.current.syncTreeLoading).toBe(false), { timeout: 2000 });

    expect(listSourceTreeChildrenMock).toHaveBeenCalled();
    expect(result.current.syncTreeData).toHaveLength(1);
  });

  it("does not open the picker when there is no detail source id", async () => {
    const { result } = renderHook(() =>
      useSyncPicker(makeHookParams({ detailSource: undefined })),
    );

    await act(async () => {
      result.current.openSyncPicker();
    });

    expect(result.current.syncPickerOpen).toBe(false);
    expect(listSourceTreeChildrenMock).not.toHaveBeenCalled();
  });

  it("warns and returns false from runSyncPipeline when nothing is selected", async () => {
    const { result } = renderHook(() => useSyncPicker(makeHookParams()));

    let outcome: boolean | undefined;
    await act(async () => {
      outcome = await result.current.runSyncPipeline([]);
    });

    expect(outcome).toBe(false);
    expect(generateParseTasksMock).not.toHaveBeenCalled();
  });

  it("returns false from runSyncPipeline when there is no detail source", async () => {
    const { result } = renderHook(() =>
      useSyncPicker(makeHookParams({ detailSource: undefined })),
    );

    let outcome: boolean | undefined;
    await act(async () => {
      outcome = await result.current.runSyncPipeline(["doc-1"]);
    });

    expect(outcome).toBe(false);
  });

  it("runs the generate pipeline for a known selectable key and reports success", async () => {
    listSourceTreeChildrenMock.mockResolvedValue({
      data: { items: [{ key: "doc-1", object_key: "doc-1", display_name: "a.pdf", is_document: true }] },
    });
    generateParseTasksMock.mockResolvedValue({
      data: { requested_count: 1, accepted_count: 1 },
    });

    const refreshDetailFromServer = vi.fn().mockResolvedValue([]);
    const startSyncPolling = vi.fn();
    const setLastOperation = vi.fn();
    const setLastSync = vi.fn();

    const { result } = renderHook(() =>
      useSyncPicker(
        makeHookParams({ refreshDetailFromServer, startSyncPolling, setLastOperation, setLastSync }),
      ),
    );

    act(() => {
      result.current.openSyncPicker();
    });
    await waitFor(() => expect(result.current.syncTreeLoading).toBe(false), { timeout: 2000 });

    let outcome: boolean | undefined;
    await act(async () => {
      outcome = await result.current.runSyncPipeline(["doc-1"]);
    });

    expect(outcome).toBe(true);
    expect(generateParseTasksMock).toHaveBeenCalled();
    expect(setLastOperation).toHaveBeenCalledWith(
      expect.objectContaining({ syncedCount: 1, checkedCount: 1 }),
    );
    expect(refreshDetailFromServer).toHaveBeenCalled();
    expect(startSyncPolling).toHaveBeenCalled();
  });

  it("returns false and stops polling when generateParseTasks rejects", async () => {
    listSourceTreeChildrenMock.mockResolvedValue({
      data: { items: [{ key: "doc-1", object_key: "doc-1", display_name: "a.pdf", is_document: true }] },
    });
    generateParseTasksMock.mockRejectedValue(new Error("boom"));

    const stopSyncPolling = vi.fn();
    const { result } = renderHook(() => useSyncPicker(makeHookParams({ stopSyncPolling })));

    act(() => {
      result.current.openSyncPicker();
    });
    await waitFor(() => expect(result.current.syncTreeLoading).toBe(false), { timeout: 2000 });

    let outcome: boolean | undefined;
    await act(async () => {
      outcome = await result.current.runSyncPipeline(["doc-1"]);
    });

    expect(outcome).toBe(false);
    expect(stopSyncPolling).toHaveBeenCalled();
  });

  it("computes filteredSyncNodeKeys and hasFilteredSelected from the loaded tree", async () => {
    listSourceTreeChildrenMock.mockResolvedValue({
      data: { items: [{ key: "doc-1", object_key: "doc-1", display_name: "a.pdf", is_document: true }] },
    });

    const { result } = renderHook(() => useSyncPicker(makeHookParams()));

    act(() => {
      result.current.openSyncPicker();
    });
    await waitFor(() => expect(result.current.syncTreeLoading).toBe(false), { timeout: 2000 });

    expect(result.current.filteredSyncNodeKeys).toEqual(["doc-1"]);
    // Initial open auto-selects all discovered files.
    expect(result.current.hasFilteredSelected).toBe(true);

    act(() => {
      result.current.setSyncSelectedDocIds([]);
    });

    expect(result.current.hasFilteredSelected).toBe(false);
  });
});

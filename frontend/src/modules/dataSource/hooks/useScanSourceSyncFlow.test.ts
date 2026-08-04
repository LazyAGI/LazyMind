import { act, renderHook, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { TFunction } from "i18next";

const getSourceMock = vi.fn();
const listSourceDocumentsMock = vi.fn();
const getSourceSummaryMock = vi.fn();

vi.mock("../api/clients", () => ({
  dataSourceScanApi: {
    getSource: (...args: unknown[]) => getSourceMock(...args),
    listSourceDocuments: (...args: unknown[]) => listSourceDocumentsMock(...args),
    getSourceSummary: (...args: unknown[]) => getSourceSummaryMock(...args),
    searchSourceTree: vi.fn(),
    listSourceTreeChildren: vi.fn(),
    generateParseTasks: vi.fn(),
  },
}));

import { useScanSourceSyncFlow } from "./useScanSourceSyncFlow";

const t = ((key: string) => key) as TFunction;

function makeDocument(overrides: Record<string, unknown> = {}) {
  return {
    document_id: "doc-1",
    name: "report.pdf",
    path: "/report.pdf",
    size_bytes: 1024,
    source_state: "UNCHANGED",
    has_update: false,
    ...overrides,
  };
}

describe("useScanSourceSyncFlow", () => {
  beforeEach(() => {
    getSourceMock.mockReset();
    listSourceDocumentsMock.mockReset();
    getSourceSummaryMock.mockReset();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it("does nothing when there is no sourceId", () => {
    const { result } = renderHook(() => useScanSourceSyncFlow({ t, sourceId: "" }));
    expect(result.current.detailSource).toBeUndefined();
    expect(getSourceMock).not.toHaveBeenCalled();
  });

  it("loads the detail source and documents when enabled with a sourceId", async () => {
    getSourceMock.mockResolvedValue({
      data: {
        source: { source_id: "src-1", name: "My Source", updated_at: "2024-01-01T00:00:00Z" },
        bindings: [{ binding_id: "bind-1", target_ref: "/root", connector_type: "local" }],
        summary: { document_objects: 1 },
      },
    });
    listSourceDocumentsMock.mockResolvedValue({ data: { items: [makeDocument()] } });
    getSourceSummaryMock.mockResolvedValue({ data: { document_objects: 1 } });

    const { result } = renderHook(() =>
      useScanSourceSyncFlow({ t, sourceId: "src-1", enabled: true }),
    );

    await waitFor(() => expect(result.current.detailLoading).toBe(false));

    expect(result.current.detailSource?.id).toBe("src-1");
    expect(result.current.detailSource?.name).toBe("My Source");
    expect(result.current.detailSource?.documents).toHaveLength(1);
    expect(result.current.detailSource?.documents[0].id).toBe("doc-1");
    expect(getSourceMock).toHaveBeenCalledWith(
      { sourceId: "src-1" },
      undefined,
    );
  });

  it("does not fetch detail when the hook is disabled", () => {
    renderHook(() => useScanSourceSyncFlow({ t, sourceId: "src-1", enabled: false }));
    expect(getSourceMock).not.toHaveBeenCalled();
  });

  it("keeps lastSync at the never-synced default when detail loading fails", async () => {
    getSourceMock.mockRejectedValue(new Error("network error"));
    const consoleErrorSpy = vi.spyOn(console, "error").mockImplementation(() => {});

    const { result } = renderHook(() =>
      useScanSourceSyncFlow({ t, sourceId: "src-1", enabled: true }),
    );

    await waitFor(() => expect(result.current.detailLoading).toBe(false));

    expect(result.current.detailSource).toBeUndefined();
    expect(result.current.lastSync).toBe("admin.dataSourceNeverSynced");
    consoleErrorSpy.mockRestore();
  });

  it("resets detail and documents when sourceId changes", async () => {
    getSourceMock.mockResolvedValue({
      data: {
        source: { source_id: "src-1", name: "My Source" },
        bindings: [],
        summary: {},
      },
    });
    listSourceDocumentsMock.mockResolvedValue({ data: { items: [makeDocument()] } });
    getSourceSummaryMock.mockResolvedValue({ data: {} });

    const { result, rerender } = renderHook(
      ({ sourceId }) => useScanSourceSyncFlow({ t, sourceId, enabled: true }),
      { initialProps: { sourceId: "src-1" } },
    );

    await waitFor(() => expect(result.current.detailSource?.documents).toHaveLength(1));

    getSourceMock.mockImplementation(() => new Promise(() => {}));
    act(() => {
      rerender({ sourceId: "src-2" });
    });

    expect(result.current.detailSource).toBeUndefined();
  });

  it("exposes sync picker controls from the underlying useSyncPicker hook", () => {
    const { result } = renderHook(() =>
      useScanSourceSyncFlow({ t, sourceId: "src-1", enabled: false }),
    );

    expect(typeof result.current.openSyncPicker).toBe("function");
    expect(typeof result.current.confirmSync).toBe("function");
    expect(result.current.syncPickerOpen).toBe(false);
  });
});

import { act, renderHook } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { TFunction } from "i18next";

const searchBindingTargetsMock = vi.fn();
const listBindingTargetChildrenMock = vi.fn();

vi.mock("../api/clients", () => ({
  dataSourceScanApi: {
    searchBindingTargets: (...args: unknown[]) => searchBindingTargetsMock(...args),
    listBindingTargetChildren: (...args: unknown[]) => listBindingTargetChildrenMock(...args),
  },
}));

vi.mock("@/components/request", () => ({
  getLocalizedErrorMessage: () => "localized-error",
}));

vi.mock("../utils/scanAccessors", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../utils/scanAccessors")>();
  return { ...actual, getScanTenantId: () => "tenant-1" };
});

import { useFeishuTargetTree } from "./useFeishuTargetTree";

const t = ((key: string) => key) as TFunction;

describe("useFeishuTargetTree", () => {
  beforeEach(() => {
    searchBindingTargetsMock.mockReset();
    listBindingTargetChildrenMock.mockReset();
  });

  it("shows an authorize-first helper node when there is no connection id", async () => {
    const { result } = renderHook(() =>
      useFeishuTargetTree({
        t,
        feishuTargetType: "drive_folder",
        getActiveFeishuAuthConnectionId: () => "",
      }),
    );

    await act(async () => {
      await result.current.loadFeishuTargetOptions("");
    });

    expect(result.current.feishuTargetTreeData).toHaveLength(1);
    expect(result.current.feishuTargetTreeData[0].title).toBe(
      "admin.dataSourceFeishuAuthorizeFirstBrowse",
    );
    expect(listBindingTargetChildrenMock).not.toHaveBeenCalled();
  });

  it("loads the browse tree via listBindingTargetChildren when authorized", async () => {
    listBindingTargetChildrenMock.mockResolvedValue({
      data: {
        items: [
          { key: "feishu:drive:1", display_name: "Drive Folder", has_children: true },
        ],
      },
    });

    const { result } = renderHook(() =>
      useFeishuTargetTree({
        t,
        feishuTargetType: "drive_folder",
        getActiveFeishuAuthConnectionId: () => "conn-1",
      }),
    );

    await act(async () => {
      await result.current.loadFeishuTargetOptions("");
    });

    expect(listBindingTargetChildrenMock).toHaveBeenCalled();
    expect(result.current.feishuTargetTreeData[0].title).toBe("Drive Folder");
  });

  it("searches the tree via searchBindingTargets when a keyword is present", async () => {
    searchBindingTargetsMock.mockResolvedValue({
      data: { items: [{ key: "feishu:drive:2", display_name: "Found Folder" }] },
    });

    const { result } = renderHook(() =>
      useFeishuTargetTree({
        t,
        feishuTargetType: "drive_folder",
        getActiveFeishuAuthConnectionId: () => "conn-1",
      }),
    );

    await act(async () => {
      await result.current.loadFeishuTargetOptions("Found");
    });

    expect(searchBindingTargetsMock).toHaveBeenCalled();
    expect(result.current.feishuTargetTreeData[0].title).toBe("Found Folder");
  });

  it("shows a no-targets helper node when the response has no items", async () => {
    listBindingTargetChildrenMock.mockResolvedValue({ data: { items: [] } });

    const { result } = renderHook(() =>
      useFeishuTargetTree({
        t,
        feishuTargetType: "drive_folder",
        getActiveFeishuAuthConnectionId: () => "conn-1",
      }),
    );

    await act(async () => {
      await result.current.loadFeishuTargetOptions("");
    });

    expect(result.current.feishuTargetTreeData[0].title).toBe("admin.dataSourceNoFeishuTargets");
  });

  it("shows a localized error helper node when the request fails", async () => {
    listBindingTargetChildrenMock.mockRejectedValue(new Error("boom"));

    const { result } = renderHook(() =>
      useFeishuTargetTree({
        t,
        feishuTargetType: "drive_folder",
        getActiveFeishuAuthConnectionId: () => "conn-1",
      }),
    );

    await act(async () => {
      await result.current.loadFeishuTargetOptions("");
    });

    expect(result.current.feishuTargetTreeData[0].title).toBe("localized-error");
  });

  it("seeds the tree with flat nodes via seedFeishuTargetTree", () => {
    const { result } = renderHook(() =>
      useFeishuTargetTree({
        t,
        feishuTargetType: "drive_folder",
        getActiveFeishuAuthConnectionId: () => "conn-1",
      }),
    );

    act(() => {
      result.current.seedFeishuTargetTree([
        { key: "seed-1", value: "seed-1", title: "Seed", isLeaf: true },
      ]);
    });

    expect(result.current.feishuTargetTreeData).toHaveLength(1);
    expect(result.current.feishuTargetTreeData[0].title).toBe("Seed");
    expect(result.current.feishuTargetLoading).toBe(false);
  });

  it("resets browse state via resetFeishuTargetBrowseOptions", () => {
    const { result } = renderHook(() =>
      useFeishuTargetTree({
        t,
        feishuTargetType: "drive_folder",
        getActiveFeishuAuthConnectionId: () => "conn-1",
      }),
    );

    act(() => {
      result.current.resetFeishuTargetBrowseOptions();
    });

    expect(result.current.feishuTargetTreeData).toEqual([]);
    expect(result.current.feishuTargetLoading).toBe(false);
  });
});

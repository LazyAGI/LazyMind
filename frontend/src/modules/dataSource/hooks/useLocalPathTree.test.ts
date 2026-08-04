import { act, renderHook, waitFor } from "@testing-library/react";
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

import { useLocalPathTree } from "./useLocalPathTree";

const t = ((key: string) => key) as TFunction;

function makeForm(pathValue: unknown = "") {
  return { getFieldValue: vi.fn(() => pathValue) } as any;
}

describe("useLocalPathTree", () => {
  beforeEach(() => {
    searchBindingTargetsMock.mockReset();
    listBindingTargetChildrenMock.mockReset();
    vi.useRealTimers();
  });

  it("loads root local path options via listBindingTargetChildren when no keyword is given", async () => {
    listBindingTargetChildrenMock.mockResolvedValue({
      data: {
        items: [
          {
            key: "/root",
            display_name: "root",
            is_container: true,
            has_children: true,
          },
        ],
      },
    });

    const { result } = renderHook(() =>
      useLocalPathTree({ t, form: makeForm(), getPreferredLocalAgentId: () => "agent-1" }),
    );

    await act(async () => {
      await result.current.loadLocalPathOptions("");
    });

    expect(listBindingTargetChildrenMock).toHaveBeenCalled();
    expect(result.current.localPathOptions).toHaveLength(1);
    expect(result.current.localPathOptions[0].title).toBe("root");
    expect(result.current.localPathLoading).toBe(false);
  });

  it("searches local paths via searchBindingTargets when a keyword is given", async () => {
    searchBindingTargetsMock.mockResolvedValue({
      data: { items: [{ key: "/found", display_name: "found", is_container: true }] },
    });

    const { result } = renderHook(() =>
      useLocalPathTree({ t, form: makeForm(), getPreferredLocalAgentId: () => "agent-1" }),
    );

    await act(async () => {
      await result.current.loadLocalPathOptions("found");
    });

    expect(searchBindingTargetsMock).toHaveBeenCalled();
    expect(result.current.localPathOptions[0].title).toBe("found");
  });

  it("shows a helper option when no directories are found", async () => {
    listBindingTargetChildrenMock.mockResolvedValue({ data: { items: [] } });

    const { result } = renderHook(() =>
      useLocalPathTree({ t, form: makeForm(), getPreferredLocalAgentId: () => "agent-1" }),
    );

    await act(async () => {
      await result.current.loadLocalPathOptions("");
    });

    expect(result.current.localPathOptions).toHaveLength(1);
    expect(result.current.localPathOptions[0].disabled).toBe(true);
    expect(result.current.localPathOptions[0].title).toBe("admin.dataSourceNoLocalDirectories");
  });

  it("shows a manual-scan-agent hint when there is no agent id and the request fails", async () => {
    listBindingTargetChildrenMock.mockRejectedValue(new Error("boom"));

    const { result } = renderHook(() =>
      useLocalPathTree({ t, form: makeForm(), getPreferredLocalAgentId: () => "" }),
    );

    await act(async () => {
      await result.current.loadLocalPathOptions("");
    });

    expect(result.current.localPathOptions[0].title).toBe("admin.dataSourceNoScanAgentManual");
  });

  it("shows the localized error message when there is an agent id and the request fails", async () => {
    listBindingTargetChildrenMock.mockRejectedValue(new Error("boom"));

    const { result } = renderHook(() =>
      useLocalPathTree({ t, form: makeForm(), getPreferredLocalAgentId: () => "agent-1" }),
    );

    await act(async () => {
      await result.current.loadLocalPathOptions("");
    });

    expect(result.current.localPathOptions[0].title).toBe("localized-error");
  });

  it("debounces search input via handleSearchLocalPathOptions", async () => {
    vi.useFakeTimers();
    listBindingTargetChildrenMock.mockResolvedValue({ data: { items: [] } });
    searchBindingTargetsMock.mockResolvedValue({ data: { items: [] } });

    const { result } = renderHook(() =>
      useLocalPathTree({ t, form: makeForm(), getPreferredLocalAgentId: () => "agent-1" }),
    );

    act(() => {
      result.current.handleSearchLocalPathOptions("docs");
    });
    expect(result.current.localPathLoading).toBe(true);
    expect(searchBindingTargetsMock).not.toHaveBeenCalled();

    await act(async () => {
      vi.advanceTimersByTime(300);
      await Promise.resolve();
    });

    expect(searchBindingTargetsMock).toHaveBeenCalled();
    vi.useRealTimers();
  });

  it("resets browse options and bumps the request sequence to cancel in-flight loads", async () => {
    const { result } = renderHook(() =>
      useLocalPathTree({ t, form: makeForm(), getPreferredLocalAgentId: () => "agent-1" }),
    );

    act(() => {
      result.current.resetLocalPathBrowseOptions();
    });

    expect(result.current.localPathOptions).toEqual([]);
    expect(result.current.localPathLoading).toBe(false);
  });
});

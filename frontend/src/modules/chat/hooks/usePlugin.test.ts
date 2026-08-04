import { act, renderHook, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const mockPluginSessionApi = {
  getLatestSession: vi.fn(),
  getSteps: vi.fn(),
  getProjection: vi.fn(),
  getSlots: vi.fn(),
  patchSlot: vi.fn(),
  listDismissedSessions: vi.fn(),
};
const mockPluginInfoApi = { getPlugin: vi.fn() };

vi.mock("@/modules/chat/utils/request", () => ({
  PluginInfoApi: () => mockPluginInfoApi,
  PluginSessionApi: () => mockPluginSessionApi,
  TempUploadServiceApi: () => ({}),
}));

vi.mock("@/i18n", () => ({
  default: { t: (key: string) => key, language: "" },
}));

vi.mock("@/components/request", () => ({
  extractErrorCode: vi.fn(() => undefined),
  getLocalizedErrorMessage: vi.fn(() => ""),
}));

import { usePluginSession, useSlot } from "./usePlugin";
import { usePluginStore } from "@/modules/chat/store/pluginPanel";

function resetStore() {
  usePluginStore.setState({
    sessionByConversation: {},
    loadingByConversation: {},
    autoRunningByConversation: {},
    pluginUIByPlugin: {},
    dismissedRefreshTrigger: {},
    dismissedSessionsByConversation: {},
    focusedTabByConversation: {},
    focusedSortOrderByConversation: {},
  });
}

describe("usePluginSession", () => {
  beforeEach(() => {
    resetStore();
    Object.values(mockPluginSessionApi).forEach((fn) => fn.mockReset());
    mockPluginSessionApi.listDismissedSessions.mockResolvedValue({
      data: { data: { sessions: [] } },
    });
  });

  it("loads the active session for the conversation on mount", async () => {
    mockPluginSessionApi.getLatestSession.mockResolvedValue({
      data: { data: { session: { session_id: "s1", status: "active" } } },
    });
    mockPluginSessionApi.getSteps.mockResolvedValue({ data: { data: { steps: [] } } });
    mockPluginSessionApi.getProjection.mockResolvedValue({ data: { data: { projection: {} } } });

    const { result } = renderHook(() => usePluginSession("conv-1"));

    await waitFor(() => {
      expect(result.current.session?.session_id).toBe("s1");
    });
    expect(result.current.loading).toBe(false);
  });

  it("returns null session and does not call the API for an empty conversationId", async () => {
    const { result } = renderHook(() => usePluginSession(""));

    expect(result.current.session).toBeNull();
    await waitFor(() => {
      expect(mockPluginSessionApi.getLatestSession).not.toHaveBeenCalled();
    });
  });

  it("refresh() re-fetches the session using silentError so failures stay quiet", async () => {
    mockPluginSessionApi.getLatestSession.mockResolvedValue({
      data: { data: { session: { session_id: "s1", status: "active" } } },
    });
    mockPluginSessionApi.getSteps.mockResolvedValue({ data: { data: { steps: [] } } });
    mockPluginSessionApi.getProjection.mockResolvedValue({ data: { data: { projection: {} } } });

    const { result } = renderHook(() => usePluginSession("conv-1"));
    await waitFor(() => expect(result.current.session?.session_id).toBe("s1"));

    mockPluginSessionApi.getLatestSession.mockClear();
    await act(async () => {
      result.current.refresh();
      await Promise.resolve();
    });

    expect(mockPluginSessionApi.getLatestSession).toHaveBeenCalledWith(
      "conv-1",
      { silentError: true },
    );
  });

  it("selectRevision patches the slot when a session is active", async () => {
    mockPluginSessionApi.getLatestSession.mockResolvedValue({
      data: { data: { session: { session_id: "s1", status: "active" } } },
    });
    mockPluginSessionApi.getSteps.mockResolvedValue({ data: { data: { steps: [] } } });
    mockPluginSessionApi.getProjection.mockResolvedValue({ data: { data: { projection: {} } } });
    mockPluginSessionApi.patchSlot.mockResolvedValue({});
    mockPluginSessionApi.getSlots.mockResolvedValue({ data: { data: { slots: [] } } });

    const { result } = renderHook(() => usePluginSession("conv-1"));
    await waitFor(() => expect(result.current.session?.session_id).toBe("s1"));

    await act(async () => {
      result.current.selectRevision("slot-a", 2);
      await Promise.resolve();
    });

    expect(mockPluginSessionApi.patchSlot).toHaveBeenCalledWith("s1", "slot-a", 2);
  });

  it("selectRevision is a no-op when there is no active session yet", async () => {
    mockPluginSessionApi.getLatestSession.mockResolvedValue({
      data: { data: { session: null } },
    });

    const { result } = renderHook(() => usePluginSession("conv-no-session"));
    await waitFor(() => expect(result.current.loading).toBe(false));

    act(() => {
      result.current.selectRevision("slot-a", 2);
    });

    expect(mockPluginSessionApi.patchSlot).not.toHaveBeenCalled();
  });
});

describe("useSlot", () => {
  beforeEach(() => {
    resetStore();
  });

  it("returns an empty array when there is no session for the conversation", () => {
    const { result } = renderHook(() => useSlot("conv-none", "slot-a"));
    expect(result.current).toEqual([]);
  });

  it("returns only selected revisions for the given slot_id, sorted by list_index", () => {
    usePluginStore.getState().setSession("conv-1", {
      session_id: "s1",
      slots: [
        { slot_id: "slot-a", selected: true, list_index: 2, slot: "a", created_at: "", revision: 1 },
        { slot_id: "slot-a", selected: false, list_index: 0, slot: "a", created_at: "", revision: 1 },
        { slot_id: "slot-a", selected: true, list_index: 0, slot: "a", created_at: "", revision: 2 },
        { slot_id: "slot-b", selected: true, list_index: 0, slot: "b", created_at: "", revision: 1 },
      ] as never,
    } as never);

    const { result } = renderHook(() => useSlot("conv-1", "slot-a"));

    expect(result.current).toHaveLength(2);
    expect(result.current[0].list_index).toBe(0);
    expect(result.current[1].list_index).toBe(2);
  });

  it("returns an empty array when the session has no slots", () => {
    usePluginStore.getState().setSession("conv-1", { session_id: "s1" } as never);

    const { result } = renderHook(() => useSlot("conv-1", "slot-a"));

    expect(result.current).toEqual([]);
  });
});

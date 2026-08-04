import { beforeEach, describe, expect, it, vi } from "vitest";

const mockPluginSessionApi = {
  getLatestSession: vi.fn(),
  getSteps: vi.fn(),
  getProjection: vi.fn(),
  getSlots: vi.fn(),
  patchSlot: vi.fn(),
  syncSessionSearchConfig: vi.fn(),
  listDismissedSessions: vi.fn(),
  deleteSlotItem: vi.fn(),
  patchSlotItem: vi.fn(),
  reorderSlotItems: vi.fn(),
  getSlotItemVersions: vi.fn(),
  rollbackSlotItem: vi.fn(),
  createSlotItem: vi.fn(),
  patchSlotCaption: vi.fn(),
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

import { buildPluginSearchConfig, usePluginStore } from "./pluginPanel";
import { extractErrorCode } from "@/components/request";

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

describe("buildPluginSearchConfig", () => {
  it("filters empty knowledgeBaseId entries and maps ids", () => {
    const result = buildPluginSearchConfig({
      knowledgeBaseId: ["kb1", "", "kb2"],
      creators: ["alice"],
      tags: ["tag1"],
    });

    expect(result).toEqual({
      dataset_list: [{ id: "kb1" }, { id: "kb2" }],
      creators: ["alice"],
      tags: ["tag1"],
    });
  });

  it("defaults to empty arrays when chatConfig is undefined", () => {
    expect(buildPluginSearchConfig(undefined)).toEqual({
      dataset_list: [],
      creators: [],
      tags: [],
    });
  });
});

describe("usePluginStore", () => {
  beforeEach(() => {
    resetStore();
    Object.values(mockPluginSessionApi).forEach((fn) => fn.mockReset());
    mockPluginInfoApi.getPlugin.mockReset();
    (extractErrorCode as ReturnType<typeof vi.fn>).mockReset().mockReturnValue(undefined);
  });

  it("setSession stores the session and clears autoRunning when status is no longer active", () => {
    usePluginStore.setState({
      autoRunningByConversation: { "conv-1": true },
    });

    usePluginStore.getState().setSession("conv-1", {
      session_id: "s1",
      conversation_id: "conv-1",
      plugin_id: "p1",
      status: "completed",
      current_step_id: "step1",
      created_at: "",
      updated_at: "",
    } as never);

    expect(usePluginStore.getState().sessionByConversation["conv-1"]?.session_id).toBe("s1");
    expect(usePluginStore.getState().autoRunningByConversation["conv-1"]).toBe(false);
  });

  it("updateSlot inserts a new slot or replaces an existing one by slot_id + list_index", () => {
    usePluginStore.getState().setSession("conv-1", {
      session_id: "s1",
      slots: [{ slot_id: "slot-a", slot: "a", selected: true, created_at: "", revision: 1 } as never],
    } as never);

    usePluginStore.getState().updateSlot("conv-1", {
      slot_id: "slot-a",
      slot: "a",
      selected: true,
      created_at: "",
      revision: 2,
    } as never);
    let slots = usePluginStore.getState().sessionByConversation["conv-1"]?.slots ?? [];
    expect(slots).toHaveLength(1);
    expect(slots[0].revision).toBe(2);

    usePluginStore.getState().updateSlot("conv-1", {
      slot_id: "slot-b",
      slot: "b",
      selected: true,
      created_at: "",
      revision: 1,
    } as never);
    slots = usePluginStore.getState().sessionByConversation["conv-1"]?.slots ?? [];
    expect(slots).toHaveLength(2);
  });

  it("updateSlot is a no-op when there is no session for the conversation", () => {
    usePluginStore.getState().updateSlot("conv-none", {
      slot_id: "slot-a",
      slot: "a",
      selected: true,
      created_at: "",
      revision: 1,
    } as never);

    expect(usePluginStore.getState().sessionByConversation["conv-none"]).toBeUndefined();
  });

  it("loadActiveSession loads session + steps + projection and dedupes concurrent calls", async () => {
    mockPluginSessionApi.getLatestSession.mockResolvedValue({
      data: { data: { session: { session_id: "s1", status: "active" } } },
    });
    mockPluginSessionApi.getSteps.mockResolvedValue({
      data: { data: { steps: [{ step_id: "step1" }, { step_id: "__end__" }] } },
    });
    mockPluginSessionApi.getProjection.mockResolvedValue({
      data: { data: { projection: { ready: ["step2"] } } },
    });
    mockPluginSessionApi.listDismissedSessions.mockResolvedValue({ data: { data: { sessions: [] } } });

    const promise1 = usePluginStore.getState().loadActiveSession("conv-1");
    const promise2 = usePluginStore.getState().loadActiveSession("conv-1");
    await Promise.all([promise1, promise2]);

    expect(mockPluginSessionApi.getLatestSession).toHaveBeenCalledTimes(1);
    const session = usePluginStore.getState().sessionByConversation["conv-1"];
    expect(session?.steps).toEqual([{ step_id: "step1" }]);
    expect(session?.projection).toEqual({ ready: ["step2"] });
  });

  it("loadActiveSession is a no-op for an empty conversationId", async () => {
    await usePluginStore.getState().loadActiveSession("");
    expect(mockPluginSessionApi.getLatestSession).not.toHaveBeenCalled();
  });

  it("loadActiveSession records runtime error code when steps/projection fetch fails", async () => {
    mockPluginSessionApi.getLatestSession.mockResolvedValue({
      data: { data: { session: { session_id: "s1", status: "active" } } },
    });
    mockPluginSessionApi.getSteps.mockRejectedValue(new Error("boom"));
    mockPluginSessionApi.getProjection.mockRejectedValue(new Error("boom"));
    mockPluginSessionApi.listDismissedSessions.mockResolvedValue({ data: { data: { sessions: [] } } });
    (extractErrorCode as ReturnType<typeof vi.fn>).mockReturnValue("PLUGIN_DEFINITION_CHANGED");

    await usePluginStore.getState().loadActiveSession("conv-1");

    const session = usePluginStore.getState().sessionByConversation["conv-1"];
    expect(session?.steps).toEqual([]);
    expect(session?.runtime_error_code).toBe("PLUGIN_DEFINITION_CHANGED");
  });

  it("fetchPluginUI caches results per pluginId+language", async () => {
    mockPluginInfoApi.getPlugin.mockResolvedValue({ data: { data: { ui: { tabs: [] } } } });

    const first = await usePluginStore.getState().fetchPluginUI("plugin-1");
    const second = await usePluginStore.getState().fetchPluginUI("plugin-1");

    expect(mockPluginInfoApi.getPlugin).toHaveBeenCalledTimes(1);
    expect(first).toBe(second);
  });

  it("fetchPluginUI returns an empty object when the request fails", async () => {
    mockPluginInfoApi.getPlugin.mockRejectedValue(new Error("fail"));

    const ui = await usePluginStore.getState().fetchPluginUI("plugin-err");

    expect(ui).toEqual({});
  });

  it("setFocusedTab mirrors the value onto both the sibling map and the session", () => {
    usePluginStore.getState().setSession("conv-1", { session_id: "s1" } as never);

    usePluginStore.getState().setFocusedTab("conv-1", "tab-a");

    expect(usePluginStore.getState().focusedTabByConversation["conv-1"]).toBe("tab-a");
    expect(usePluginStore.getState().sessionByConversation["conv-1"]?.focusedTab).toBe("tab-a");
  });

  it("setFocusedSortOrder mirrors the value onto both the sibling map and the session", () => {
    usePluginStore.getState().setFocusedSortOrder("conv-2", 3);

    expect(usePluginStore.getState().focusedSortOrderByConversation["conv-2"]).toBe(3);
    // No session yet for conv-2, so sessionByConversation stays untouched.
    expect(usePluginStore.getState().sessionByConversation["conv-2"]).toBeUndefined();
  });

  it("patchSlotItemValue extracts the numeric revision from the response", async () => {
    mockPluginSessionApi.patchSlotItem.mockResolvedValue({ data: { data: { revision: 7 } } });

    const revision = await usePluginStore
      .getState()
      .patchSlotItemValue("s1", "slot-a", 0, { text: "hi" });

    expect(revision).toBe(7);
  });

  it("patchSlotItemValue returns undefined when the response has no numeric revision", async () => {
    mockPluginSessionApi.patchSlotItem.mockResolvedValue({ data: {} });

    const revision = await usePluginStore
      .getState()
      .patchSlotItemValue("s1", "slot-a", 0, { text: "hi" });

    expect(revision).toBeUndefined();
  });

  it("bumpDismissedRefresh increments the counter and triggers a dismissed-session refetch", async () => {
    mockPluginSessionApi.listDismissedSessions.mockResolvedValue({
      data: { data: { sessions: [{ session_id: "d1", plugin_id: "p1" }] } },
    });

    usePluginStore.getState().bumpDismissedRefresh("conv-1");
    // fetchDismissedSessions is fire-and-forget; wait a tick for the promise to settle.
    await Promise.resolve();
    await Promise.resolve();

    expect(usePluginStore.getState().dismissedRefreshTrigger["conv-1"]).toBe(1);
  });
});

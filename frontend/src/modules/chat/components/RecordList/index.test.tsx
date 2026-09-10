import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import type { DragEndEvent } from "@dnd-kit/core";
import { MemoryRouter } from "react-router-dom";
import { beforeAll, beforeEach, describe, expect, it, vi } from "vitest";

import RecordList from "./index";
vi.mock("../../conversationOrganizer/api", () => ({ listConversationGroups: vi.fn().mockResolvedValue([]), removeConversation: vi.fn(), emitConversationGroupsChanged: vi.fn(), CONVERSATION_GROUPS_CHANGED_EVENT: "groups-changed" }));
import { emitConversationActivity } from "@/modules/chat/utils/conversationActivity";
import { CHAT_CONVERSATION_FILTER_KEY } from "@/modules/chat/constants/chat";
import { useConversationRunningStore } from "@/modules/chat/store/conversationRunning";
import { CONVERSATION_DRAG } from "../../conversationOrganizer/drag";

const drag = vi.hoisted(() => ({ end: (_event: DragEndEvent): Promise<void> | void => {} }));
vi.mock("@dnd-kit/core", async () => {
  const actual = await vi.importActual<typeof import("@dnd-kit/core")>("@dnd-kit/core");
  return {
    ...actual,
    DndContext: (props: React.ComponentProps<typeof actual.DndContext>) => {
      drag.end = props.onDragEnd!;
      return <actual.DndContext {...props} />;
    },
  };
});

const mocks = vi.hoisted(() => ({
  listConversations: vi.fn(),
  setPinned: vi.fn(),
  reorder: vi.fn(),
  deleteConversation: vi.fn(),
  listChatExecutors: vi.fn(),
  messageSuccess: vi.fn(),
  messageError: vi.fn(),
}));

vi.mock("react-i18next", () => ({
  useTranslation: () => ({
    t: (key: string, params?: Record<string, unknown>) =>
      ({
        "chat.conversationGroupPinned": "已置顶",
        "chat.conversationGroupToday": "今天",
        "chat.conversationGroupRecentWeek": "近一周",
        "chat.conversationGroupEarlier": "以前",
        "chat.conversationMainLabel": "主对话",
        "chat.pinConversation": "置顶",
        "chat.unpinConversation": "取消置顶",
        "chat.pinConversationSuccess": "会话已置顶",
        "chat.unpinConversationSuccess": "已取消置顶",
        "chat.pinConversationFailed": "置顶状态更新失败，请重试",
        "chat.reorderConversationFailed": "顺序保存失败，请重试",
        "settingsPage.recovery.moreActions": "更多操作",
        "settingsPage.recovery.archiveAction": "归档",
        "settingsPage.recovery.moveToTrash": "移入回收站",
        "chat.batch": "批量",
        "chat.selectAll": "全选",
        "chat.conversationChildSourceLabel": "来源",
        "chat.conversationChildForkLabel": "分支来源",
        "chat.expandChildConversations": `展开${params?.count}个子会话`,
        "chat.collapseChildConversations": "收起子会话",
        "chat.childConversationsLabel": `${params?.parent}的子会话`,
        "chat.conversationSidechatRelationshipTooltip":
          `“${params?.child}”是“${params?.parent}”的侧聊子会话`,
        "chat.conversationForkRelationshipTooltip":
          `“${params?.child}”由“${params?.parent}”Fork而来`,
        "chat.conversationGenericRelationshipTooltip":
          `“${params?.child}”来源于“${params?.parent}”`,
        "chat.conversationSourceFrom": `来源：${params?.parent}`,
        "chat.conversationForkedFrom": `分支来源：${params?.parent}`,
      })[key] || key,
  }),
}));

vi.mock("antd", async () => {
  const actual = await vi.importActual<typeof import("antd")>("antd");
  return {
    ...actual,
    message: {
      success: mocks.messageSuccess,
      error: mocks.messageError,
      warning: vi.fn(),
      open: vi.fn(),
      destroy: vi.fn(),
    },
  };
});

vi.mock("@/modules/chat/utils/request", () => ({
  ChatServiceApi: () => ({
    conversationServiceListConversations: mocks.listConversations,
    conversationServiceSetPinned: mocks.setPinned,
    conversationServiceReorder: mocks.reorder,
    conversationServiceDeleteConversation: mocks.deleteConversation,
  }),
  ConversationSettingsApi: () => ({
    listChatExecutors: mocks.listChatExecutors,
  }),
}));

vi.mock("@/api/generated/core-client", () => ({
  Configuration: class {},
  ConversationsApiFactory: () => ({}),
  DefaultApiFactory: () => ({}),
}));

vi.mock("@/components/request", () => ({ axiosInstance: {}, BASE_URL: "" }));
vi.mock("@/modules/chat/store/chatThink", () => ({
  useChatThinkStore: () => ({ setThink: vi.fn() }),
}));
vi.mock("@/modules/chat/store/chatNewMessage", () => ({
  useChatNewMessageStore: () => ({ setNewMessage: vi.fn() }),
}));
vi.mock("../ArchiveConversationModal", () => ({ default: () => null }));
vi.mock("@/modules/settings/recoveryApi", () => ({
  unarchiveConversation: vi.fn(),
}));
vi.mock("@/modules/chat/utils/download", () => ({ downloadStream: vi.fn() }));

const newerConversation = {
  conversation_id: "newer",
  display_name: "较新的会话",
  update_time: new Date(Date.now() - 60_000).toISOString(),
  search_config: {},
};
const olderConversation = {
  conversation_id: "older",
  display_name: "较早的会话",
  update_time: new Date(Date.now() - 120_000).toISOString(),
  search_config: {},
};

function renderRecordList(currentSessionId = "", onSelected = vi.fn()) {
  return render(
    <MemoryRouter>
      <RecordList
        compact
        hideHeader
        currentSessionId={currentSessionId}
        onSelected={onSelected}
        onRemove={vi.fn()}
      />
    </MemoryRouter>,
  );
}

function moreActionsFor(title: string) {
  const recordList = document.querySelector(".record-list");
  const record = recordList
    ? within(recordList as HTMLElement).getByText(title).closest(".record")
    : null;
  if (!(record instanceof HTMLElement)) {
    throw new Error(`record not found: ${title}`);
  }
  return within(record).getByRole("button", { name: "更多操作" });
}

describe("RecordList conversation pinning", () => {
  beforeAll(() => {
    Object.defineProperty(window, "matchMedia", {
      writable: true,
      value: vi.fn().mockImplementation((query: string) => ({
        matches: false,
        media: query,
        onchange: null,
        addListener: vi.fn(),
        removeListener: vi.fn(),
        addEventListener: vi.fn(),
        removeEventListener: vi.fn(),
        dispatchEvent: vi.fn(),
      })),
    });
  });

  beforeEach(() => {
    sessionStorage.removeItem(CHAT_CONVERSATION_FILTER_KEY);
    Object.values(mocks).forEach((mock) => mock.mockReset());
    mocks.listConversations.mockResolvedValue({
      data: {
        conversations: [newerConversation, olderConversation],
        next_page_token: "",
      },
    });
    mocks.listChatExecutors.mockResolvedValue({
      data: { data: { executors: [] } },
    });
  });

  it.each([false, true])("preserves group dragging and the sorting handle with organizer lock=%s", async (locked) => {
    mocks.listConversations.mockResolvedValue({ data: { conversations: [{
      ...newerConversation, group_id: null, organizing_run_id: locked ? "run" : null,
    }], next_page_token: "" } });
    renderRecordList();
    const title = await screen.findByText(newerConversation.display_name);
    const row = title.closest(".ant-col")!;
    await waitFor(() => expect(screen.getByRole("button", { name: "chat.reorderConversation" }))
      .toHaveAttribute("aria-disabled", String(locked)));
    expect(row).toHaveAttribute("draggable", String(!locked));
    if (!locked) {
      const dataTransfer = { setData: vi.fn(), effectAllowed: "" };
      fireEvent.dragStart(row, { dataTransfer });
      expect(dataTransfer.setData).toHaveBeenCalledWith(CONVERSATION_DRAG, JSON.stringify({ id: "newer", groupId: null }));
      expect(mocks.reorder).not.toHaveBeenCalled();
    }
  });

  it.each(["normal", "task"])("saves manual order in %s mode and preserves it after activity and reload", async (mode: string) => {
    sessionStorage.setItem(CHAT_CONVERSATION_FILTER_KEY, JSON.stringify([mode]));
    const saved = {
      conversation_id: "older", is_pinned: false, pinned_at: null, history_order: 1,
      order_updates: [
        { conversation_id: "older", history_order: 1 },
        { conversation_id: "newer", history_order: 2 },
      ],
    };
    let resolveSave!: (value: { data: typeof saved }) => void;
    mocks.reorder.mockImplementation(() => new Promise((resolve) => { resolveSave = resolve; }));
    const view = renderRecordList();
    await screen.findByText("较早的会话");
    expect(mocks.listConversations).toHaveBeenCalledWith(expect.anything(), {
      params: mode === "task" ? { is_task_conv: "true" } : { is_task_conv: "false", assistants: "lazymind" },
    });
    const event = { active: { id: "older" }, over: { id: "newer" } } as DragEndEvent;
    await act(async () => { void drag.end(event); void drag.end(event); });
    expect(mocks.reorder).toHaveBeenCalledTimes(1);
    expect(mocks.reorder).toHaveBeenCalledWith("older", "newer", "before");
    expect(document.querySelector(".record .title")?.textContent).toBe("较新的会话");
    mocks.listConversations.mockResolvedValue({ data: {
      conversations: [{ ...newerConversation, history_order: 2 }, { ...olderConversation, history_order: 1 }],
      next_page_token: "",
    } });
    await act(async () => resolveSave({ data: saved }));
    expect(document.querySelector(".record .title")?.textContent).toBe("较早的会话");
    expect(screen.queryByText("今天")).not.toBeInTheDocument();
    act(() => emitConversationActivity({ conversationId: "newer" }));
    expect(document.querySelector(".record .title")?.textContent).toBe("较早的会话");
    view.unmount();
    mocks.listConversations.mockResolvedValue({ data: {
      conversations: [{ ...newerConversation, history_order: 2 }, { ...olderConversation, history_order: 1 }],
      next_page_token: "",
    } });
    renderRecordList();
    await screen.findByText("较早的会话");
    expect(document.querySelector(".record .title")?.textContent).toBe("较早的会话");
  });

  it("keeps the existing order when saving a drag fails", async () => {
    mocks.reorder.mockRejectedValue(new Error("offline"));
    renderRecordList();
    await screen.findByText("较早的会话");
    await act(async () => drag.end({ active: { id: "older" }, over: { id: "newer" } } as DragEndEvent));
    expect(mocks.messageError).toHaveBeenCalledWith("顺序保存失败，请重试");
    expect(document.querySelector(".record .title")?.textContent).toBe("较新的会话");
  });

  it("keeps status subscriptions stable when conversation activity only changes display order", async () => {
    const view = renderRecordList();
    await screen.findByText("较早的会话");
    const watchers = useConversationRunningStore.getState().watchers;
    const list = document.querySelector<HTMLElement>(".record-list")!;
    list.scrollTo = vi.fn();
    act(() => emitConversationActivity({ conversationId: "older" }));
    await waitFor(() => expect(list.scrollTo).toHaveBeenCalled());
    expect(document.querySelector(".record .title")?.textContent).toBe("较早的会话");
    expect(useConversationRunningStore.getState().watchers).toBe(watchers);
    view.unmount();
    expect(Object.keys(useConversationRunningStore.getState().watchers)).toHaveLength(0);
  });

  it("reorders pinned conversations without moving ordinary history", async () => {
    const pin = { is_pinned: true, pinned_at: "2026-09-01T08:00:00Z" };
    mocks.listConversations.mockResolvedValue({ data: {
      conversations: [{ ...newerConversation, ...pin, history_order: 1 }, { ...olderConversation, ...pin, history_order: 2 },
        { conversation_id: "ordinary", display_name: "普通会话", search_config: {} }],
    } });
    mocks.reorder.mockImplementation(() => {
      mocks.listConversations.mockResolvedValue({ data: {
        conversations: [{ ...olderConversation, ...pin, history_order: 1 }, { ...newerConversation, ...pin, history_order: 2 },
          { conversation_id: "ordinary", display_name: "普通会话", search_config: {} }],
      } });
      return Promise.resolve({ data: {
        ...pin, conversation_id: "older", history_order: 1,
        order_updates: [{ conversation_id: "older", history_order: 1 }, { conversation_id: "newer", history_order: 2 }],
      } });
    });
    renderRecordList();
    await screen.findByText("较早的会话");
    await act(async () => drag.end({ active: { id: "older" }, over: { id: "ordinary" } } as DragEndEvent));
    expect(mocks.reorder).not.toHaveBeenCalled();
    await act(async () => drag.end({ active: { id: "older" }, over: { id: "newer" } } as DragEndEvent));
    const pinnedSection = screen.getByText("已置顶").closest(".record-group");
    expect(pinnedSection?.querySelector(".record .title")?.textContent).toBe("较早的会话");
    expect(within(pinnedSection as HTMLElement).queryByText("普通会话")).not.toBeInTheDocument();
  });

  it("limits the default normal filter to non-task LazyMind conversations", async () => {
    renderRecordList();

    await screen.findByText("较早的会话");
    expect(mocks.listConversations).toHaveBeenCalledWith(
      expect.anything(),
      { params: { is_task_conv: "false", assistants: "lazymind" } },
    );
  });

  it("pins and unpins a conversation without changing its activity date", async () => {
    mocks.setPinned.mockImplementation((_id: string, pinned: boolean) => {
      const data = { is_pinned: pinned, pinned_at: pinned ? "2026-08-30T10:00:00Z" : null };
      mocks.listConversations.mockResolvedValue({ data: {
        conversations: [newerConversation, { ...olderConversation, ...data }], next_page_token: "",
      } });
      return Promise.resolve({ data });
    });
    renderRecordList();

    await screen.findByText("较早的会话");
    const activityDate = screen
      .getByText("较早的会话")
      .closest(".record")
      ?.querySelector(".update-time")?.textContent;
    fireEvent.click(moreActionsFor("较早的会话"));
    fireEvent.click(await screen.findByText("置顶"));

    await waitFor(() => expect(mocks.setPinned).toHaveBeenCalledWith("older", true));
    const pinnedGroup = await screen.findByText("已置顶");
    const pinnedSection = pinnedGroup.closest(".record-group");
    expect(pinnedSection).not.toBeNull();
    expect(within(pinnedSection as HTMLElement).getByText("较早的会话")).toBeInTheDocument();
    expect(pinnedSection?.querySelector(".record-pin-icon")).toBeNull();
    expect(
      screen
        .getByText("较早的会话")
        .closest(".record")
        ?.querySelector(".update-time")?.textContent,
    ).toBe(activityDate);
    expect(mocks.messageSuccess).toHaveBeenCalledWith("会话已置顶");

    fireEvent.click(moreActionsFor("较早的会话"));
    fireEvent.click(await screen.findByText("取消置顶"));

    await waitFor(() => expect(mocks.setPinned).toHaveBeenLastCalledWith("older", false));
    await waitFor(() => expect(screen.queryByText("已置顶")).not.toBeInTheDocument());
    const todaySection = screen.getByText("今天").closest(".record-group");
    expect(todaySection?.querySelector(".title")?.textContent).toBe("较新的会话");
  });

  it.each([false, true])("rebuilds pagination after unpinning beyond the loaded window (refresh fails: %s)", async (failRefresh) => {
    const ordinary = Array.from({ length: 51 }, (_, index) => ({
      ...newerConversation, conversation_id: `a${index + 1}`, display_name: `会话 A${index + 1}`,
      update_time: new Date(Date.now() - (index + 1) * 60_000).toISOString(),
    }));
    const returning = { ...olderConversation, conversation_id: "p", display_name: "旧置顶会话",
      update_time: "2026-01-01T00:00:00Z", is_pinned: true, pinned_at: "2026-09-01T00:00:00Z" };
    let unpinned = false;
    let refreshAttempts = 0;
    let resolveOldPage!: (value: unknown) => void;
    mocks.listConversations.mockImplementation(({ pageToken }: { pageToken: string }) => {
      if (!pageToken) {
        if (unpinned && ++refreshAttempts === 1 && failRefresh) return Promise.reject(new Error("offline"));
        return Promise.resolve({ data: {
          conversations: unpinned ? ordinary.slice(0, 50) : [returning, ...ordinary.slice(0, 49)],
          next_page_token: "50",
        } });
      }
      if (!unpinned) return new Promise((resolve) => { resolveOldPage = resolve; });
      return Promise.resolve({ data: {
        conversations: [...ordinary.slice(50), { ...returning, is_pinned: false, pinned_at: null }],
        next_page_token: "",
      } });
    });
    mocks.setPinned.mockImplementation(() => {
      unpinned = true;
      return Promise.resolve({ data: { is_pinned: false, pinned_at: null } });
    });
    renderRecordList();
    await screen.findByText("旧置顶会话");
    const list = document.querySelector<HTMLElement>(".record-list")!;
    Object.defineProperties(list, {
      clientHeight: { value: 500, configurable: true },
      scrollHeight: { value: 1000, configurable: true },
      scrollTop: { value: 500, writable: true, configurable: true },
    });
    list.scrollTo = vi.fn();
    fireEvent.scroll(list);
    await waitFor(() => expect(mocks.listConversations).toHaveBeenCalledTimes(2));
    fireEvent.click(moreActionsFor("旧置顶会话"));
    fireEvent.click(await screen.findByText("取消置顶"));
    await waitFor(() => expect(mocks.setPinned).toHaveBeenCalledWith("p", false));
    if (failRefresh) {
      await waitFor(() => expect(mocks.messageError).toHaveBeenCalledWith("chat.fork.historyLoadFailed"));
      fireEvent.scroll(list);
    }
    await screen.findByText("会话 A50");
    expect(screen.queryByText("旧置顶会话")).not.toBeInTheDocument();
    expect(mocks.listConversations).toHaveBeenLastCalledWith(
      expect.objectContaining({ pageToken: "", pageSize: 50 }), expect.anything(),
    );
    // A page requested before unpinning must not append into the rebuilt window.
    await act(async () => resolveOldPage({ data: { conversations: ordinary.slice(49), next_page_token: "" } }));
    expect(screen.queryByText("会话 A51")).not.toBeInTheDocument();
    fireEvent.scroll(list);
    await screen.findByText("会话 A51");
    expect(mocks.listConversations).toHaveBeenLastCalledWith(
      expect.objectContaining({ pageToken: "50" }), expect.anything(),
    );
    const titles = [...list.querySelectorAll(".record .title")].map((node) => node.textContent);
    expect(titles).toEqual([...ordinary.map((item) => item.display_name), returning.display_name]);
  });

  it("keeps the current order and reports an error when pinning fails", async () => {
    mocks.setPinned.mockRejectedValueOnce(new Error("request failed"));
    renderRecordList();

    await screen.findByText("较早的会话");
    fireEvent.click(moreActionsFor("较早的会话"));
    fireEvent.click(await screen.findByText("置顶"));

    await waitFor(() =>
      expect(mocks.messageError).toHaveBeenCalledWith("置顶状态更新失败，请重试"),
    );
    expect(screen.queryByText("已置顶")).not.toBeInTheDocument();
    const todaySection = screen.getByText("今天").closest(".record-group");
    expect(todaySection?.querySelector(".title")?.textContent).toBe("较新的会话");
  });

  it("loads children moved into the first page by reordering their parent", async () => {
    const child = { ...olderConversation, conversation_id: "child", display_name: "随父会话移动的子会话",
      parent_conversation_id: "older", parent_display_name: olderConversation.display_name, relation_type: "sidechat" };
    mocks.listConversations.mockResolvedValue({ data: {
      conversations: [newerConversation, olderConversation], next_page_token: "2",
    } });
    mocks.reorder.mockImplementation(() => {
      mocks.listConversations.mockResolvedValue({ data: {
        conversations: [{ ...olderConversation, history_order: 1 }, child], next_page_token: "2",
      } });
      return Promise.resolve({ data: {
        conversation_id: "older", history_order: 1, is_pinned: false,
        order_updates: [{ conversation_id: "older", history_order: 1 }, { conversation_id: "newer", history_order: 2 }],
      } });
    });
    renderRecordList();
    await screen.findByText("较早的会话");
    await act(async () => drag.end({ active: { id: "older" }, over: { id: "newer" } } as DragEndEvent));
    fireEvent.click(await screen.findByRole("button", { name: "展开1个子会话" }));
    expect(within(screen.getByRole("group", { name: "较早的会话的子会话" })).getByText(child.display_name)).toBeInTheDocument();
    expect(mocks.listConversations).toHaveBeenLastCalledWith(
      expect.objectContaining({ pageToken: "" }), expect.anything(),
    );
  });

  it("nests retained children under their parent and keeps them collapsed by default", async () => {
    mocks.listConversations.mockResolvedValue({
      data: {
        conversations: [
          {
            ...newerConversation,
            conversation_id: "parent",
            display_name: "主会话",
          },
          {
            ...olderConversation,
            conversation_id: "sidechat-child",
            display_name: "侧聊方案",
            parent_conversation_id: "parent",
            parent_display_name: "主会话",
            relation_type: "sidechat",
          },
          {
            ...olderConversation,
            conversation_id: "fork-child",
            display_name: "分支方案",
            parent_conversation_id: "parent",
            parent_display_name: "主会话",
            relation_type: "fork",
          },
        ],
        next_page_token: "",
      },
    });

    const onSelected = vi.fn();
    renderRecordList("", onSelected);

    const parentTitle = await screen.findByText("主会话");
    expect(screen.queryByText("侧聊方案")).not.toBeInTheDocument();
    expect(screen.queryByText("分支方案")).not.toBeInTheDocument();

    fireEvent.mouseOver(parentTitle);
    const mainRelation = await screen.findByText("主对话");
    const mainPreview = mainRelation.closest(".record-preview-card");
    expect(mainPreview).not.toBeNull();
    expect(within(mainPreview as HTMLElement).queryByText("今天")).not.toBeInTheDocument();
    fireEvent.mouseOut(parentTitle);

    fireEvent.click(
      screen.getByRole("button", { name: "展开2个子会话" }),
    );

    const childGroup = screen.getByRole("group", {
      name: "主会话的子会话",
    });
    expect(within(childGroup).getByText("侧聊方案")).toBeInTheDocument();
    expect(within(childGroup).getByText("分支方案")).toBeInTheDocument();
    expect(within(childGroup).queryByText("来源")).not.toBeInTheDocument();
    expect(within(childGroup).queryByText("分支来源")).not.toBeInTheDocument();

    const sideChatRecord = within(childGroup)
      .getByText("侧聊方案")
      .closest(".record");
    expect(sideChatRecord).toHaveAttribute("role", "button");
    expect(sideChatRecord).toHaveAttribute("tabindex", "0");
    fireEvent.keyDown(sideChatRecord as HTMLElement, { key: "Enter" });
    expect(onSelected).toHaveBeenCalledWith(
      expect.objectContaining({ conversation_id: "sidechat-child" }),
    );

    const sidechatTitle = within(childGroup).getByText("侧聊方案");
    fireEvent.mouseOver(sidechatTitle);
    expect(await screen.findByText("来源：主会话")).toBeInTheDocument();
    fireEvent.mouseOut(sidechatTitle);

    const forkTitle = within(childGroup).getByText("分支方案");
    fireEvent.mouseOver(forkTitle);
    expect(await screen.findByText("分支来源：主会话")).toBeInTheDocument();
    fireEvent.mouseOut(forkTitle);

    fireEvent.click(moreActionsFor("侧聊方案"));
    expect(await screen.findByText("移入回收站")).toBeInTheDocument();
    expect(screen.queryByText("置顶")).not.toBeInTheDocument();
    expect(screen.queryByText("归档")).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "收起子会话" }));
    expect(screen.queryByText("侧聊方案")).not.toBeInTheDocument();
  });

  it("does not allow a child conversation to be selected independently in batch mode", async () => {
    mocks.listConversations.mockResolvedValue({
      data: {
        conversations: [
          {
            ...newerConversation,
            conversation_id: "parent",
            display_name: "主会话",
          },
          {
            ...olderConversation,
            conversation_id: "sidechat-child",
            display_name: "侧聊方案",
            parent_conversation_id: "parent",
            parent_display_name: "主会话",
            relation_type: "sidechat",
          },
        ],
        next_page_token: "",
      },
    });

    render(
      <MemoryRouter>
        <RecordList
          compact
          showBatchActions
          currentSessionId=""
          onSelected={vi.fn()}
          onRemove={vi.fn()}
        />
      </MemoryRouter>,
    );

    await screen.findByText("主会话");
    fireEvent.click(screen.getByRole("button", { name: "批量" }));
    fireEvent.click(screen.getByRole("button", { name: "展开1个子会话" }));

    const childCheckbox = within(
      screen.getByRole("group", { name: "主会话的子会话" }),
    ).getByRole("checkbox");
    expect(childCheckbox).toBeDisabled();

    fireEvent.click(screen.getByRole("checkbox", { name: /全选/ }));
    expect(childCheckbox).not.toBeChecked();
  });

  it("keeps a child nested when its parent is outside the loaded page", async () => {
    mocks.listConversations.mockResolvedValue({
      data: {
        conversations: [
          {
            ...newerConversation,
            conversation_id: "orphan-child",
            display_name: "可见的子会话",
            parent_conversation_id: "parent-not-loaded",
            parent_display_name: "未加载的主会话",
            relation_type: "sidechat",
          },
        ],
        next_page_token: "next-page",
      },
    });

    renderRecordList();

    expect(await screen.findByText("未加载的主会话")).toBeInTheDocument();
    expect(screen.queryByText("可见的子会话")).not.toBeInTheDocument();
    expect(screen.queryByText("来源")).not.toBeInTheDocument();
    expect(() => moreActionsFor("未加载的主会话")).toThrow();

    fireEvent.click(screen.getByRole("button", { name: "展开1个子会话" }));
    const childGroup = screen.getByRole("group", {
      name: "未加载的主会话的子会话",
    });
    expect(within(childGroup).getByText("可见的子会话")).toBeInTheDocument();
    expect(within(childGroup).queryByText("来源")).not.toBeInTheDocument();
    fireEvent.mouseOver(within(childGroup).getByText("可见的子会话"));
    expect(await screen.findByText("来源：未加载的主会话")).toBeInTheDocument();
  });

  it("expands an unloaded parent so a matching child remains visible in search", async () => {
    mocks.listConversations.mockResolvedValue({
      data: {
        conversations: [
          {
            ...newerConversation,
            conversation_id: "matched-child",
            display_name: "命中的子会话",
            parent_conversation_id: "parent-not-loaded",
            parent_display_name: "主会话",
            relation_type: "sidechat",
          },
        ],
        next_page_token: "",
      },
    });

    render(
      <MemoryRouter>
        <RecordList
          compact
          hideHeader
          searchText="命中"
          currentSessionId=""
          onSelected={vi.fn()}
          onRemove={vi.fn()}
        />
      </MemoryRouter>,
    );

    expect(await screen.findByText("命中的子会话")).toBeInTheDocument();
    expect(screen.getByRole("group", { name: "主会话的子会话" })).toBeInTheDocument();
  });
});

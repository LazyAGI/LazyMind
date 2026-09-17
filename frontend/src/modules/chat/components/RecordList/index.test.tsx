import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import type { DragEndEvent } from "@dnd-kit/core";
import { ConfigProvider } from "antd";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";

import RecordList from "./index";
import SidebarGroups from "../../conversationOrganizer/SidebarGroups";
import { assignConversation, emitConversationGroupsChanged, getConversationGroup } from "../../conversationOrganizer/api";
vi.mock("../../conversationOrganizer/api", () => ({ assignConversation: vi.fn(), getConversationGroup: vi.fn(), listConversationGroups: vi.fn().mockResolvedValue([]), removeConversation: vi.fn(), emitConversationGroupsChanged: vi.fn(), CONVERSATION_GROUPS_CHANGED_EVENT: "groups-changed" }));
import { emitConversationActivity } from "@/modules/chat/utils/conversationActivity";
import { CHAT_CONVERSATION_FILTER_KEY } from "@/modules/chat/constants/chat";
import { useConversationRunningStore } from "@/modules/chat/store/conversationRunning";
import { CONVERSATION_DRAG } from "../../conversationOrganizer/drag";

vi.mock("../ConversationTitleEditor", () => ({ default: ({ initialTitle, onClose }: { initialTitle: string; onClose: () => void }) => <input aria-label="会话名称" defaultValue={initialTitle} onKeyDown={event => { if (event.key === "Escape") onClose(); }} /> }));

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
  getConversationDetail: vi.fn(),
  listChatExecutors: vi.fn(),
  messageSuccess: vi.fn(),
  messageError: vi.fn(),
  batchDelete: vi.fn(),
  archiveConversation: vi.fn(),
  listArchiveFolders: vi.fn(),
}));

vi.mock("react-i18next", () => ({
  useTranslation: () => ({
    t: (key: string, params?: Record<string, unknown>) =>
      ({
        "chat.conversationGroupPinned": "已置顶",
        "chat.conversationGroupToday": "今天",
        "chat.conversationGroupYesterday": "昨天",
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
        "settingsPage.recovery.moveToTrashTitle": "删除会话",
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
    conversationServiceGetConversationDetail: mocks.getConversationDetail,
  }),
  ConversationSettingsApi: () => ({
    listChatExecutors: mocks.listChatExecutors,
  }),
}));

vi.mock("@/api/generated/core-client", () => ({
  Configuration: class {},
  ConversationsApiFactory: () => ({}),
  DefaultApiFactory: () => ({ apiCoreConversationsBatchDeletePost: mocks.batchDelete }),
}));

vi.mock("@/components/request", () => ({ axiosInstance: {}, BASE_URL: "" }));
vi.mock("@/modules/chat/store/chatThink", () => ({
  useChatThinkStore: () => ({ setThink: vi.fn() }),
}));
vi.mock("@/modules/chat/store/chatNewMessage", () => ({
  useChatNewMessageStore: () => ({ setNewMessage: vi.fn() }),
}));
vi.mock("@/modules/settings/recoveryApi", () => ({
  unarchiveConversation: vi.fn(),
  archiveConversation: mocks.archiveConversation,
  listArchiveFolders: mocks.listArchiveFolders,
  createArchiveFolder: vi.fn(),
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

function renderRecordList(currentSessionId = "", onSelected = vi.fn(), onRemove = vi.fn()) {
  return render(
    <ConfigProvider theme={{ token: { motion: false } }}><MemoryRouter>
      <RecordList
        compact
        hideHeader
        currentSessionId={currentSessionId}
        onSelected={onSelected}
        onRemove={onRemove}
      />
    </MemoryRouter></ConfigProvider>,
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
  it.each([false, true])("renames Chat/Work from the pinned list without changing ordering (work=%s)", async (work) => {
    sessionStorage.setItem(CHAT_CONVERSATION_FILTER_KEY, JSON.stringify([work ? "task" : "normal"]));
    mocks.listConversations.mockResolvedValue({ data: { conversations: [
      { ...olderConversation, is_task_conv: work, is_pinned: true, pinned_at: olderConversation.update_time },
      { ...newerConversation, is_task_conv: work },
    ] } });
    renderRecordList();
    await screen.findByText("较早的会话");
    fireEvent.click(moreActionsFor("较早的会话"));
    fireEvent.click(await screen.findByText("conversationOrganizer.renameConversation"));
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(screen.getByRole("textbox", { name: "会话名称" }).closest(".record")).toHaveClass("record-renaming");
    expect(screen.getByRole("textbox", { name: "会话名称" }).closest("[draggable]")).toHaveAttribute("draggable", "false");
    fireEvent.keyDown(screen.getByRole("textbox", { name: "会话名称" }), { key: "Escape" });
    const before = Array.from(document.querySelectorAll(".record .update-time"), el => el.textContent);
    act(() => window.dispatchEvent(new CustomEvent("lazymind:conversation-title-changed", {
      detail: { conversationId: "older", displayName: "自定义会话", titleRevision: 1 },
    })));
    expect(screen.getByText("自定义会话")).toBeInTheDocument();
    expect(Array.from(document.querySelectorAll(".record .title"), el => el.textContent)).toEqual(["自定义会话", "较新的会话"]);
    expect(Array.from(document.querySelectorAll(".record .update-time"), el => el.textContent)).toEqual(before);
  });
  afterEach(() => { vi.useRealTimers();  });

  it("keeps date sections with manual ordering and refuses dragging across dates", async () => {
    vi.useFakeTimers({ toFake: ["Date"] });
    vi.setSystemTime(new Date("2026-09-14T12:00:00"));
    const conversations = [
      { id: "earlier", title: "更早对话", date: "2026-09-01T10:00:00" },
      { id: "yesterday", title: "昨天对话", date: "2026-09-13T23:59:59" },
      { id: "today-1", title: "今日手动第一条", date: "2026-09-14T00:00:00" },
      { id: "week", title: "本周对话", date: "2026-09-12T12:00:00" },
      { id: "today-2", title: "今日手动第二条", date: "2026-09-14T11:00:00" },
    ].map(({ id, title, date }, index) => ({
      conversation_id: id, display_name: title, update_time: new Date(date).toISOString(),
      history_order: index + 1, search_config: {},
    }));
    mocks.listConversations.mockResolvedValue({ data: { conversations } });
    const view = renderRecordList();
    await screen.findByText("今日手动第一条");
    const expectGroups = () => {
      expect(Array.from(document.querySelectorAll(".record-group-title"), (el) => el.textContent))
        .toEqual(["今天", "昨天", "近一周", "以前"]);
      expect(Array.from(screen.getByText("今天").closest(".record-group")!.querySelectorAll(".title"), (el) => el.textContent))
        .toEqual(["今日手动第一条", "今日手动第二条"]);
      for (const [group, title] of [["昨天", "昨天对话"], ["近一周", "本周对话"], ["以前", "更早对话"]]) {
        expect(within(screen.getByText(group).closest(".record-group") as HTMLElement).getByText(title)).toBeInTheDocument();
      }
      expect(document.querySelector(".record-time-period")).not.toBeInTheDocument();
      expect(screen.getByText("今日手动第一条").closest(".record")!.querySelector(".update-time"))
        .toHaveTextContent(/^09\/14$/);
    };
    expectGroups();
    await act(async () => drag.end({ active: { id: "yesterday" }, over: { id: "today-1" } } as DragEndEvent));
    expect(mocks.reorder).not.toHaveBeenCalled();
    view.unmount();
    renderRecordList();
    await screen.findByText("今日手动第一条");
    expectGroups();
  });

  it("includes conversations in custom groups when batch mode is enabled", async () => {
    mocks.listConversations.mockResolvedValue({ data: { conversations: [
      newerConversation, { ...olderConversation, group_id: "group-1" },
    ] } });
    render(<ConfigProvider theme={{ token: { motion: false } }}><MemoryRouter><RecordList compact showBatchActions currentSessionId="" onSelected={vi.fn()} onRemove={vi.fn()} /></MemoryRouter></ConfigProvider>);
    await screen.findByText(newerConversation.display_name);
    expect(screen.queryByText(olderConversation.display_name)).not.toBeInTheDocument();
    fireEvent.click(screen.getByText("批量"));
    const groupedRow = await screen.findByText(olderConversation.display_name);
    expect(groupedRow.closest(".export-checkbox-item")).not.toHaveClass("ant-checkbox-wrapper-disabled");
    fireEvent.click(screen.getByText("全选"));
    const checkboxes = screen.getAllByRole("checkbox");
    expect(checkboxes.filter((item) => (item as HTMLInputElement).checked)).toHaveLength(3);
  });

  it("requires confirmation and preserves the conversation when deletion is cancelled", async () => {
    const onRemove = vi.fn();
    const onSelected = vi.fn();
    renderRecordList("older", onSelected, onRemove);
    await screen.findByText(newerConversation.display_name);
    fireEvent.click(moreActionsFor(newerConversation.display_name));
    const menuItem = await screen.findByRole("menuitem", { name: /common.delete/ });
    expect(menuItem).toHaveClass("ant-dropdown-menu-item-danger");
    fireEvent.click(menuItem);
    const dialog = await screen.findByRole("dialog");
    expect(dialog).toHaveAccessibleName("删除会话");
    expect(within(dialog).queryByText(newerConversation.display_name)).not.toBeInTheDocument();
    expect(within(dialog).getByText("settingsPage.recovery.moveToTrashDescription")).toBeInTheDocument();
    expect(mocks.deleteConversation).not.toHaveBeenCalled();
    fireEvent.click(within(dialog).getByRole("button", { name: "common.cancel" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    expect(screen.getByText(newerConversation.display_name)).toBeInTheDocument();
    expect(onRemove).not.toHaveBeenCalled();
    expect(onSelected).not.toHaveBeenCalled();
  });

  it("keeps a failed deletion open and allows an immediate successful retry", async () => {
    mocks.deleteConversation.mockRejectedValueOnce(new Error("offline"));
    const onRemove = vi.fn();
    renderRecordList("newer", vi.fn(), onRemove);
    await screen.findByText(newerConversation.display_name);
    fireEvent.click(moreActionsFor(newerConversation.display_name));
    fireEvent.click(await screen.findByRole("menuitem", { name: /common.delete/ }));
    const dialog = await screen.findByRole("dialog");
    fireEvent.click(within(dialog).getByRole("button", { name: "common.delete" }));
    await waitFor(() => expect(mocks.messageError).toHaveBeenCalledWith("settingsPage.recovery.operationFailed"));
    expect(screen.getByText(newerConversation.display_name)).toBeInTheDocument();
    expect(onRemove).not.toHaveBeenCalled();
    mocks.listConversations.mockResolvedValue({ data: { conversations: [olderConversation] } });
    fireEvent.click(await within(dialog).findByRole("button", { name: "common.delete" }));
    await waitFor(() => expect(onRemove).toHaveBeenCalledWith(expect.objectContaining({ conversation_id: "newer" })));
    expect(mocks.deleteConversation).toHaveBeenCalledTimes(2);
    expect(mocks.messageSuccess).toHaveBeenCalledWith("chat.deleteConversationSuccess");
    await waitFor(() => expect(screen.queryByText(newerConversation.display_name)).not.toBeInTheDocument());
  });

  it("keeps unsuccessful batch deletions selected and does not close their active conversation", async () => {
    mocks.batchDelete.mockResolvedValue({ data: { deleted_count: 1, deleted_ids: ["older"] } });
    const onRemove = vi.fn();
    render(<ConfigProvider theme={{ token: { motion: false } }}><MemoryRouter><RecordList compact showBatchActions currentSessionId="newer" onSelected={vi.fn()} onRemove={onRemove} /></MemoryRouter></ConfigProvider>);
    await screen.findByText(newerConversation.display_name);
    fireEvent.click(screen.getByText("批量"));
    fireEvent.click(screen.getByRole("checkbox", { name: /全选/ }));
    fireEvent.click(screen.getByRole("button", { name: /common.actions/ }));
    expect(await screen.findByRole("menuitem", { name: /chat.batchArchive/ })).toBeInTheDocument();
    fireEvent.click(screen.getByRole("menuitem", { name: /common.delete/ }));
    const dialog = await screen.findByRole("dialog");
    mocks.listConversations.mockResolvedValue({ data: { conversations: [newerConversation] } });
    fireEvent.click(within(dialog).getByRole("button", { name: "common.delete" }));
    await waitFor(() => expect(mocks.batchDelete).toHaveBeenCalled());
    await waitFor(() => expect(screen.queryByText(olderConversation.display_name)).not.toBeInTheDocument());
    expect(onRemove).not.toHaveBeenCalled();
    expect(screen.getByRole("checkbox", { name: new RegExp(newerConversation.display_name) })).toBeChecked();
  });

  it("archives selected conversations and retains only failures for retry", async () => {
    mocks.archiveConversation.mockImplementation(async (id: string) => {
      if (id === "older") throw new Error("offline");
    });
    const onRemove = vi.fn();
    render(<ConfigProvider theme={{ token: { motion: false } }}><MemoryRouter><RecordList compact showBatchActions currentSessionId="newer" onSelected={vi.fn()} onRemove={onRemove} /></MemoryRouter></ConfigProvider>);
    await screen.findByText(newerConversation.display_name);
    fireEvent.click(screen.getByText("批量"));
    fireEvent.click(screen.getByRole("checkbox", { name: /全选/ }));
    fireEvent.click(screen.getByRole("button", { name: /common.actions/ }));
    fireEvent.click(await screen.findByRole("menuitem", { name: /chat.batchArchive/ }));
    const dialog = await screen.findByRole("dialog");
    await within(dialog).findByRole("radio");
    mocks.listConversations.mockResolvedValue({ data: { conversations: [olderConversation] } });
    fireEvent.click(within(dialog).getByRole("button", { name: "归档" }));
    await waitFor(() => expect(onRemove).toHaveBeenCalledWith(expect.objectContaining({ conversation_id: "newer" })));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    expect(screen.queryByText(newerConversation.display_name)).not.toBeInTheDocument();
    expect(screen.getByRole("checkbox", { name: new RegExp(olderConversation.display_name) })).toBeChecked();
    expect(mocks.archiveConversation.mock.calls).toEqual([["newer", null], ["older", null]]);
    expect(mocks.batchDelete).not.toHaveBeenCalled();
    mocks.archiveConversation.mockResolvedValue(undefined);
    mocks.listConversations.mockResolvedValue({ data: { conversations: [] } });
    fireEvent.click(screen.getByRole("button", { name: /common.actions/ }));
    fireEvent.click(await screen.findByRole("menuitem", { name: /chat.batchArchive/ }));
    const retry = await screen.findByRole("dialog");
    await within(retry).findByRole("radio");
    fireEvent.click(within(retry).getByRole("button", { name: "归档" }));
    await waitFor(() => expect(screen.getByRole("button", { name: "批量" })).toBeInTheDocument());
    expect(mocks.archiveConversation.mock.calls).toEqual([["newer", null], ["older", null], ["older", null]]);
    expect(screen.queryByText(olderConversation.display_name)).not.toBeInTheDocument();
  });

  it.each([true, false])("only removes dependent side chats with their parent (sidechat=%s)", async (sidechat) => {
    const related = {
      ...olderConversation,
      ...(sidechat ? { parent_conversation_id: "newer", relation_type: "sidechat" }
        : { fork_origin: { source_conversation_id: "newer", source_title_snapshot: newerConversation.display_name } }),
    };
    mocks.listConversations.mockResolvedValue({ data: { conversations: [newerConversation, related] } });
    const onRemove = vi.fn();
    renderRecordList("older", vi.fn(), onRemove);
    await screen.findByText(newerConversation.display_name);
    fireEvent.click(moreActionsFor(newerConversation.display_name));
    fireEvent.click(await screen.findByRole("menuitem", { name: /common.delete/ }));
    const dialog = await screen.findByRole("dialog");
    mocks.listConversations.mockResolvedValue({ data: { conversations: sidechat ? [] : [related] } });
    fireEvent.click(within(dialog).getByRole("button", { name: "common.delete" }));
    await waitFor(() => expect(mocks.messageSuccess).toHaveBeenCalledWith("chat.deleteConversationSuccess"));
    if (sidechat) expect(onRemove).toHaveBeenCalledWith(expect.objectContaining({ conversation_id: "older" }));
    else expect(onRemove).not.toHaveBeenCalled();
  });

  it("allows an independent fork conversation to be archived", async () => {
    mocks.listConversations.mockResolvedValue({ data: { conversations: [{ ...newerConversation,
      fork_origin: { source_conversation_id: "source", source_title_snapshot: "分支来源" },
    }] } });
    renderRecordList();
    await screen.findByText("分支来源");
    fireEvent.click(screen.getByRole("button", { name: /展开1个子会话/ }));
    fireEvent.click(moreActionsFor(newerConversation.display_name));
    expect(await screen.findByRole("menuitem", { name: /归档/ })).toBeInTheDocument();
  });

  it("keeps custom groups visible and includes paginated group members in batch actions", async () => {
    const group = { id: "group-batch", name: "批量回归组" } as any;
    const batchGroups = [group];
    const member = { conversation_id: "group-only", display_name: "不在历史首页的组内对话", membership_revision: 1 };
    const pinned = { ...member, conversation_id: "group-pinned", display_name: "组内置顶对话", pinned_at: new Date().toISOString() };
    const nextMember = { ...member, conversation_id: "group-next", display_name: "组内下一页" };
    vi.mocked(getConversationGroup).mockImplementation(async (_id, token) => ({
      group, conversations: token ? [nextMember] : [member, pinned], nextPageToken: token ? "" : "group-page-2",
    }));
    mocks.listConversations.mockResolvedValue({ data: { conversations: [newerConversation, { ...pinned, group_id: group.id, update_time: pinned.pinned_at }] } });
    mocks.batchDelete.mockResolvedValue({ data: { deleted_count: 1 } });
    const onRemove = vi.fn();
    render(<ConfigProvider theme={{ token: { motion: false } }}><MemoryRouter><RecordList compact showBatchActions currentSessionId="group-only"
      onSelected={vi.fn()} onRemove={onRemove}
      groupSection={(batchSelection) => <SidebarGroups groups={batchGroups} batchSelection={batchSelection} onEdit={vi.fn()} onRemove={vi.fn()} />} /></MemoryRouter></ConfigProvider>);
    await screen.findByText(member.display_name);
    document.querySelector<HTMLElement>('.record-container')!.scrollTo = vi.fn();
    fireEvent.click(screen.getByText("批量"));
    const groupedRow = await screen.findByRole("checkbox", { name: member.display_name });
    expect(screen.getAllByText(pinned.display_name)).toHaveLength(1);
    expect(within(screen.getByTitle(group.name).closest('.conversation-group') as HTMLElement).getByRole('checkbox', { name: member.display_name })).toBe(groupedRow);
    fireEvent.click(groupedRow);
    fireEvent.click(screen.getByRole('checkbox', { name: new RegExp(newerConversation.display_name) }));
    expect(groupedRow).toBeChecked();
    fireEvent.click(screen.getByRole('button', { name: 'conversationOrganizer.showMore' }));
    const nextRow = await screen.findByRole('checkbox', { name: nextMember.display_name });
    fireEvent.click(screen.getByRole('checkbox', { name: /全选/ }));
    expect(groupedRow).toBeChecked();
    expect(nextRow).toBeChecked();
    expect(screen.getAllByRole('checkbox').filter(el => (el as HTMLInputElement).checked)).toHaveLength(6);
    fireEvent.click(screen.getByRole('checkbox', { name: /全选/ }));
    const groupSelectAll = screen.getByRole('checkbox', { name: 'conversationOrganizer.selectAllInGroup' });
    fireEvent.click(groupSelectAll);
    expect(groupedRow).toBeChecked();
    expect(nextRow).toBeChecked();
    expect(screen.getByRole('checkbox', { name: new RegExp(newerConversation.display_name) })).not.toBeChecked();
    fireEvent.click(groupSelectAll);
    expect(groupedRow).not.toBeChecked();
    expect(nextRow).not.toBeChecked();
    fireEvent.click(groupedRow);
    fireEvent.click(screen.getByRole('button', { name: /common.actions/ }));
    fireEvent.click(await screen.findByRole('menuitem', { name: /common.delete/ }));
    fireEvent.click(within(await screen.findByRole('dialog')).getByRole('button', { name: 'common.delete' }));
    await waitFor(() => expect(mocks.batchDelete).toHaveBeenCalledWith({ conversationBatchDeleteRequest: { conversation_ids: ['group-only'] } }));
    expect(onRemove).toHaveBeenCalledWith(expect.objectContaining({ conversation_id: 'group-only' }));
    fireEvent.click(await screen.findByRole('button', { name: '批量' }));
    expect(await screen.findByRole('checkbox', { name: member.display_name })).not.toBeChecked();
    fireEvent.click(screen.getByRole('checkbox', { name: member.display_name }));
    fireEvent.click(within(document.querySelector('.record-container') as HTMLElement).getByRole('button', { name: 'common.cancel' }));
    fireEvent.click(screen.getByRole('button', { name: '批量' }));
    expect(await screen.findByRole('checkbox', { name: member.display_name })).not.toBeChecked();
  });

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
    mocks.deleteConversation.mockResolvedValue({});
    mocks.listArchiveFolders.mockResolvedValue({ folders: [], unfiledTotalCount: 0 });
    mocks.archiveConversation.mockResolvedValue(undefined);
    mocks.getConversationDetail.mockResolvedValue({ data: { conversation: {} } });
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

  it.each(['normal', 'task'])('moves a %s conversation into a group from its sorting handle', async (mode) => {
    sessionStorage.setItem(CHAT_CONVERSATION_FILTER_KEY, JSON.stringify([mode]));
    mocks.listConversations.mockResolvedValue({ data: { conversations: [{ ...newerConversation, is_task_conv: mode === 'task' }] } });
    const groups = [{ id: 'destination', name: '目标组' }] as any;
    vi.mocked(getConversationGroup).mockResolvedValue({ group: groups[0], conversations: [], nextPageToken: '' });
    render(<ConfigProvider theme={{ token: { motion: false } }}><MemoryRouter><RecordList compact showBatchActions onSelected={vi.fn()}
      groupSection={(batchSelection) => <SidebarGroups groups={groups} batchSelection={batchSelection} onEdit={vi.fn()} onRemove={vi.fn()} />} /></MemoryRouter></ConfigProvider>);
    await screen.findByText(newerConversation.display_name);
    await screen.findByTitle('目标组');
    const event = { active: { id: 'newer' }, over: { id: 'group:destination', data: { current: { kind: 'conversation-group', groupId: 'destination' } } } } as unknown as DragEndEvent;
    await act(async () => drag.end(event));
    expect(assignConversation).toHaveBeenCalledWith('destination', 'newer');
    expect(emitConversationGroupsChanged).toHaveBeenCalled();
    expect(mocks.reorder).not.toHaveBeenCalled();
    vi.mocked(assignConversation).mockClear();
    fireEvent.click(screen.getByRole('button', { name: '批量' }));
    await act(async () => drag.end(event));
    expect(assignConversation).not.toHaveBeenCalled();
  });

  it('removes drag handles in batch mode and restores them after cancelling', async () => {
    render(<ConfigProvider theme={{ token: { motion: false } }}><MemoryRouter><RecordList compact showBatchActions onSelected={vi.fn()} /></MemoryRouter></ConfigProvider>);
    await screen.findByText(newerConversation.display_name);
    expect(screen.getAllByRole('button', { name: 'chat.reorderConversation' })).toHaveLength(2);
    fireEvent.click(screen.getByRole('button', { name: '批量' }));
    const row = screen.getByRole('checkbox', { name: new RegExp(newerConversation.display_name) });
    fireEvent.mouseEnter(row);
    expect(document.querySelector('.record-drag-handle')).toBeNull();
    expect(row.closest('.ant-col')).toHaveAttribute('draggable', 'false');
    fireEvent.click(row);
    expect(row).toBeChecked();
    fireEvent.click(within(document.querySelector('.record-container') as HTMLElement).getByRole('button', { name: 'common.cancel' }));
    expect(screen.getAllByRole('button', { name: 'chat.reorderConversation' })).toHaveLength(2);
    expect(screen.getByText(newerConversation.display_name).closest('.ant-col')).toHaveAttribute('draggable', 'true');
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
    expect(screen.getAllByText("今天")).toHaveLength(1);
    expect(screen.getByText("较新的会话").closest(".record")).not.toHaveTextContent("今天");
    expect(document.querySelector(".record-time-period")).not.toBeInTheDocument();
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
    expect(await screen.findByText("common.delete")).toBeInTheDocument();
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
      <ConfigProvider theme={{ token: { motion: false } }}><MemoryRouter>
        <RecordList
          compact
          showBatchActions
          currentSessionId=""
          onSelected={vi.fn()}
          onRemove={vi.fn()}
        />
      </MemoryRouter></ConfigProvider>,
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
      <ConfigProvider theme={{ token: { motion: false } }}><MemoryRouter>
        <RecordList
          compact
          hideHeader
          searchText="命中"
          currentSessionId=""
          onSelected={vi.fn()}
          onRemove={vi.fn()}
        />
      </MemoryRouter></ConfigProvider>,
    );

    expect(await screen.findByText("命中的子会话")).toBeInTheDocument();
    expect(screen.getByRole("group", { name: "主会话的子会话" })).toBeInTheDocument();
  });
});

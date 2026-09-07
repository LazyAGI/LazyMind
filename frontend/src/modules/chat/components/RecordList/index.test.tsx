import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { beforeAll, beforeEach, describe, expect, it, vi } from "vitest";

import RecordList from "./index";

const mocks = vi.hoisted(() => ({
  listConversations: vi.fn(),
  setPinned: vi.fn(),
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
        "settingsPage.recovery.moreActions": "更多操作",
        "settingsPage.recovery.archiveAction": "归档",
        "settingsPage.recovery.moveToTrash": "移入回收站",
        "chat.batch": "批量",
        "chat.selectAll": "全选",
        "chat.conversationChildSourceLabel": "来源",
        "chat.conversationChildForkLabel": "Fork自",
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
        "chat.conversationForkedFrom": `Fork自：${params?.parent}`,
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
vi.mock("react-infinite-scroll-component", () => ({
  default: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
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

  it("does not exclude external assistants from the default conversation list", async () => {
    renderRecordList();

    await screen.findByText("较早的会话");
    expect(mocks.listConversations).toHaveBeenCalledWith(
      expect.anything(),
      { params: { is_task_conv: "false" } },
    );
  });

  it("pins and unpins a conversation without changing its activity date", async () => {
    mocks.setPinned
      .mockResolvedValueOnce({
        data: { is_pinned: true, pinned_at: "2026-08-30T10:00:00Z" },
      })
      .mockResolvedValueOnce({
        data: { is_pinned: false, pinned_at: null },
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
    expect(within(childGroup).queryByText("Fork自")).not.toBeInTheDocument();

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
    expect(await screen.findByText("Fork自：主会话")).toBeInTheDocument();
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

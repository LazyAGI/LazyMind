import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { beforeAll, beforeEach, describe, expect, it, vi } from "vitest";

import RecordList from "./index";

const mocks = vi.hoisted(() => ({
  listConversations: vi.fn(),
  setPinned: vi.fn(),
  deleteConversation: vi.fn(),
  messageSuccess: vi.fn(),
  messageError: vi.fn(),
}));

vi.mock("react-i18next", () => ({
  useTranslation: () => ({
    t: (key: string) =>
      ({
        "chat.conversationGroupPinned": "已置顶",
        "chat.conversationGroupToday": "今天",
        "chat.conversationGroupRecentWeek": "近一周",
        "chat.conversationGroupEarlier": "以前",
        "chat.pinConversation": "置顶",
        "chat.unpinConversation": "取消置顶",
        "chat.pinConversationSuccess": "会话已置顶",
        "chat.unpinConversationSuccess": "已取消置顶",
        "chat.pinConversationFailed": "置顶状态更新失败，请重试",
        "settingsPage.recovery.moreActions": "更多操作",
        "settingsPage.recovery.archiveAction": "归档",
        "settingsPage.recovery.moveToTrash": "移入回收站",
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

function renderRecordList() {
  return render(
    <MemoryRouter>
      <RecordList
        compact
        hideHeader
        currentSessionId=""
        onSelected={vi.fn()}
        onRemove={vi.fn()}
      />
    </MemoryRouter>,
  );
}

function moreActionsFor(title: string) {
  const record = screen.getByText(title).closest(".record");
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
});

import { useConversationUnreadStore } from "@/modules/chat/store/conversationUnread";
import { useDatasetPermissionStore } from "@/modules/knowledge/store/dataset_permission";
import { lazy, Suspense } from "react";
import { ConfigProvider } from "antd";
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { Link, MemoryRouter, Route, Routes, useLocation } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

import MainLayout from "./MainLayout";
import { TASKS_REMOVED_EVENT } from '@/modules/taskCenter/events';
import {
  CHAT_CONVERSATION_LIST_REFRESH_EVENT,
  CHAT_SELECT_CONVERSATION_EVENT,
  CHAT_CONVERSATION_FILTER_KEY,
  readChatConversationFilters,
  selectChatConversationSources,
} from "@/modules/chat/constants/chat";

const mocks = vi.hoisted(() => ({
  initialOnRemove: null as null | ((conversation: { conversation_id?: string }) => void),
  latestRecordListProps: null as any,
  refreshRecordList: vi.fn(),
  fetchPreferences: vi.fn().mockResolvedValue({ document_directories_enabled: true }),
  listDirectoryBatches: vi.fn().mockResolvedValue([]),
}));

vi.mock("@/modules/user/uiPreferencesApi", () => ({
 fetchUserUiPreferences: mocks.fetchPreferences,
 USER_UI_PREFERENCES_CHANGED_EVENT: "lazymind:user-ui-preferences-changed",
}));
vi.mock("@/modules/documentDirectories/api", () => ({ listBatches: mocks.listDirectoryBatches }));

vi.mock("react-i18next", async (importOriginal) => ({
  ...(await importOriginal<typeof import("react-i18next")>()),
  useTranslation: () => ({ t: (key: string) => key }),
}));

vi.mock("@/components/LanguageSwitcher", () => ({
  default: () => null,
}));

vi.mock("@/components/auth", () => ({
  AUTH_USER_CHANGE_EVENT: "lazymind:user-change",
  AgentAppsAuth: {
    getUserInfo: () => ({
      token: "test-token",
      username: "admin",
      role: "system-admin",
    }),
    isLoggedIn: () => true,
    logout: vi.fn(),
  },
}));

vi.mock("@/modules/signin/utils/request", () => ({
  changeCurrentUserPassword: vi.fn(),
  fetchCurrentUser: vi.fn().mockResolvedValue(undefined),
  fetchCurrentUserDetail: vi.fn(),
  updateCurrentUserProfile: vi.fn(),
}));

vi.mock("@/modules/signin/utils/formRules", () => ({
  validatePassword: () => Promise.resolve(),
}));

vi.mock("@/utils/developerMode", () => ({
  DEVELOPER_ACTIVE_EVENT: "lazymind:developer-active",
  isDeveloperModeActive: () => false,
  syncDeveloperModeFromServer: vi.fn().mockResolvedValue(false),
}));

vi.mock("@/runtime/features", () => ({
  runtimeFeatures: { hideEvo: true },
}));

vi.mock("@/runtime/localSession", () => ({
  shouldHideLocalUserControls: () => false,
}));

vi.mock("@/runtime/useLocalSessionGate", () => ({
  useLocalSessionGate: () => ({
    enabled: true,
    loading: false,
    error: "",
    retry: vi.fn(),
  }),
}));

vi.mock("@/components/UserAgreementConsentModal", () => ({
  default: () => null,
  useUserAgreementConsentGate: () => ({
    needsConsent: false,
    markAccepted: vi.fn(),
    loading: false,
    checkFailed: false,
    retryCheck: vi.fn(),
  }),
}));

vi.mock("@/modules/channelGateway/components/TerminalConnectionQuickPanel", () => ({
  default: () => null,
}));

vi.mock("@/modules/chat/components/RecordList", async () => {
  const React = await import("react");
  const MockRecordList = React.forwardRef((props: any, ref) => {
    mocks.latestRecordListProps = props;
    mocks.initialOnRemove ??= props.onRemove;
    React.useImperativeHandle(ref, () => ({
      refresh: mocks.refreshRecordList,
    }));
    return <div data-testid="record-list" />;
  });
  MockRecordList.displayName = "MockRecordList";
  return { default: MockRecordList };
});

function LocationProbe() {
  return <div data-testid="location-path">{useLocation().pathname}</div>;
}

describe("MainLayout conversation removal", () => {
  it.each(['/agent/chat/home/removed', '/task-center'])("only leaves a deleted conversation that is currently open: %s", async path => {
    render(<MemoryRouter initialEntries={[path]}><MainLayout /><LocationProbe /></MemoryRouter>);
    act(() => window.dispatchEvent(new CustomEvent(TASKS_REMOVED_EVENT, { detail: { removed_task_ids: ['task'], trashed_conversation_ids: ['removed'], cancellation_pending: true } })));
    await waitFor(() => expect(screen.getByTestId('location-path')).toHaveTextContent(path === '/task-center' ? path : '/agent/chat/home'));
  });
  beforeEach(() => {
    sessionStorage.clear();
    localStorage.clear();
    mocks.initialOnRemove = null;
    mocks.latestRecordListProps = null;
    mocks.refreshRecordList.mockReset();
  });

  it("restores legacy task mode and preserves selected sources when starting a quick question", () => {
    sessionStorage.setItem(CHAT_CONVERSATION_FILTER_KEY, '["task","agent:codex"]');
    render(<MemoryRouter><MainLayout /></MemoryRouter>);
    expect(screen.getByRole("button", { name: /layout.newTask/ })).toHaveAttribute("aria-pressed", "true");
    act(() => selectChatConversationSources(["codex", "workbuddy"]));
    expect(screen.getByRole("button", { name: /layout.newTask/ })).toHaveAttribute("aria-pressed", "true");
    fireEvent.click(screen.getByRole("button", { name: /layout.newChat/ }));
    expect(screen.getByRole("button", { name: /layout.newChat/ })).toHaveAttribute("aria-pressed", "true");
    expect(readChatConversationFilters()).toEqual({ filter: "normal", sources: ["codex", "workbuddy"] });
  });

  it('captures the draft before notifying the page about a mode change', () => {
    const events: string[] = [];
    const capture = () => events.push('capture');
    const select = () => events.push('select');
    window.addEventListener('lazymind:chat-capture-mode-draft', capture);
    window.addEventListener(CHAT_SELECT_CONVERSATION_EVENT, select);
    try {
      sessionStorage.setItem(CHAT_CONVERSATION_FILTER_KEY, '["task"]');
      render(<MemoryRouter><MainLayout /></MemoryRouter>);
      fireEvent.click(screen.getByRole('button', { name: /layout.newChat/ }));
      expect(events).toEqual(['capture', 'select']);
    } finally {
      window.removeEventListener('lazymind:chat-capture-mode-draft', capture);
      window.removeEventListener(CHAT_SELECT_CONVERSATION_EVENT, select);
    }
  });

  it("uses a detail route for the selected conversation and returns home when it is removed", async () => {
    const selectedConversationId = "conversation-1";
    const selections: string[] = [];
    const handleSelection = (event: Event) => {
      selections.push(
        (event as CustomEvent<{ conversationId?: string }>).detail
          ?.conversationId || "",
      );
    };
    window.addEventListener(CHAT_SELECT_CONVERSATION_EVENT, handleSelection);

    render(
      <MemoryRouter initialEntries={["/agent/chat/home"]}>
        <MainLayout />
        <LocationProbe />
      </MemoryRouter>,
    );

    const staleRemoveCallback = mocks.initialOnRemove;
    expect(staleRemoveCallback).not.toBeNull();

    act(() => {
      mocks.latestRecordListProps.onSelected({
        conversation_id: selectedConversationId,
      });
    });

    await waitFor(() => {
      expect(mocks.latestRecordListProps.currentSessionId).toBe(
        selectedConversationId,
      );
      expect(screen.getByTestId("location-path")).toHaveTextContent(
        `/agent/chat/home/${selectedConversationId}`,
      );
    });
    expect(selections).toEqual([]);

    act(() => {
      staleRemoveCallback?.({ conversation_id: selectedConversationId });
    });

    await waitFor(() => {
      expect(selections[selections.length - 1]).toBe("");
      expect(screen.getByTestId("location-path")).toHaveTextContent(
        "/agent/chat/home",
      );
    });
    window.removeEventListener(CHAT_SELECT_CONVERSATION_EVENT, handleSelection);
  });

  it("replaces the home URL with the real id created by a new chat", async () => {
    render(
      <MemoryRouter initialEntries={["/agent/chat/home"]}>
        <MainLayout />
        <LocationProbe />
      </MemoryRouter>,
    );

    act(() => {
      window.dispatchEvent(
        new CustomEvent(CHAT_SELECT_CONVERSATION_EVENT, {
          detail: { conversationId: "conversation-new", source: "chat" },
        }),
      );
    });

    await waitFor(() => {
      expect(screen.getByTestId("location-path")).toHaveTextContent(
        "/agent/chat/home/conversation-new",
      );
    });
  });

  it("does not restore an old selection from a non-navigation event after returning home", async () => {
    render(<MemoryRouter initialEntries={["/agent/chat/home/old"]}><MainLayout /><LocationProbe /></MemoryRouter>);
    fireEvent.click(screen.getByRole("button", { name: "LazyMind" }));
    await waitFor(() => expect(mocks.latestRecordListProps.currentSessionId).toBe(""));
    act(() => window.dispatchEvent(new CustomEvent(CHAT_SELECT_CONVERSATION_EVENT, {
      detail: { conversationId: "old", source: "sidebar" },
    })));
    expect(screen.getByTestId("location-path")).toHaveTextContent(/^\/agent\/chat\/home$/);
    expect(mocks.latestRecordListProps.currentSessionId).toBe("");
  });

  it("ignores a late chat selection after leaving the chat page", async () => {
    render(<MemoryRouter initialEntries={["/task-center"]}><MainLayout /><LocationProbe /></MemoryRouter>);
    act(() => window.dispatchEvent(new CustomEvent(CHAT_SELECT_CONVERSATION_EVENT, {
      detail: { conversationId: "old", source: "chat" },
    })));
    expect(screen.getByTestId("location-path")).toHaveTextContent("/task-center");
    expect(mocks.latestRecordListProps.currentSessionId).toBe("");
  });

  it("keeps the home logo free of unread notifications without clearing conversation unread state", () => {
    useConversationUnreadStore.setState({ counts: { current: 2, other: 5 } });
    render(<MemoryRouter initialEntries={["/agent/chat/home/current"]}><MainLayout /></MemoryRouter>);
    const logo = screen.getByRole("button", { name: "LazyMind", exact: true });
    expect(logo.querySelector(".ant-badge-count")).not.toBeInTheDocument();
    expect(within(logo).queryByRole("status")).not.toBeInTheDocument();
    expect(useConversationUnreadStore.getState().counts).toEqual({ current: 2, other: 5 });
    useConversationUnreadStore.setState({ counts: {} });
  });

  it("refreshes the sidebar list when recovery invalidates conversation history", () => {
    render(
      <MemoryRouter initialEntries={["/settings?section=recovery"]}>
        <MainLayout />
      </MemoryRouter>,
    );
    expect(screen.getByTestId("record-list")).toBeInTheDocument();

    act(() => {
      window.dispatchEvent(
        new Event(CHAT_CONVERSATION_LIST_REFRESH_EVENT),
      );
    });

    expect(mocks.refreshRecordList).toHaveBeenCalledTimes(1);
  });
});

describe("MainLayout resizable navigation", () => {
  beforeEach(() => {
    sessionStorage.clear();
    localStorage.clear();
    useDatasetPermissionStore.getState().clearDataset();
    mocks.initialOnRemove = null;
    mocks.latestRecordListProps = null;
    mocks.refreshRecordList.mockReset();
  });

  function renderLayout(path = "/agent/chat/home") {
    return render(
      <MemoryRouter initialEntries={[path]}>
        <ConfigProvider theme={{ token: { motion: false } }}>
        <MainLayout />
        <LocationProbe />
        <Link to="/memory-management/workflows/draft-1">Open workflow</Link>
        <Link to="/lib/knowledge/detail/papers">Open papers</Link>
        <Link to="/task-center">Open tasks</Link>
        </ConfigProvider>
      </MemoryRouter>,
    );
  }

  function resizeHandle() {
    return screen.getByRole("separator", { name: "layout.resizeSidebar" });
  }

  it.each([
    "/agent/chat/home",
    "/memory-management/skills",
    "/memory-management/workflows",
    "/task-center",
    "/settings",
  ])("defaults to the normal width on %s, ignoring the old hidden-menu preference", (path) => {
    localStorage.setItem("lazymind:main-menu-collapsed", "1");
    renderLayout(path);
    expect(resizeHandle()).toHaveAttribute("aria-valuenow", "272");
    expect(screen.getByTestId("record-list")).toBeVisible();
  });

  it.each(["/lib/knowledge/paper-preview", "/lib/knowledge/detail/papers"])("starts paper libraries collapsed on %s but allows manual expansion", (path) => {
    localStorage.setItem("lazymind.paper-library.ids", JSON.stringify(["papers"]));
    renderLayout(path);
    expect(resizeHandle()).toHaveAttribute("aria-valuenow", "72");
    fireEvent.click(screen.getByRole("button", { name: "layout.expandMenu" }));
    expect(resizeHandle()).toHaveAttribute("aria-valuenow", "272");
    fireEvent.click(screen.getByRole("link", { name: "Open tasks" }));
    fireEvent.click(screen.getByRole("link", { name: "Open papers" }));
    expect(resizeHandle()).toHaveAttribute("aria-valuenow", "72");
  });

  it("preserves the normal sidebar width outside paper libraries", () => {
    localStorage.setItem("lazymind.paper-library.ids", JSON.stringify(["papers"]));
    renderLayout("/lib/knowledge/detail/regular");
    expect(resizeHandle()).toHaveAttribute("aria-valuenow", "272");
    fireEvent.keyDown(resizeHandle(), { key: "ArrowLeft" });
    fireEvent.click(screen.getByRole("link", { name: "Open papers" }));
    expect(resizeHandle()).toHaveAttribute("aria-valuenow", "72");
    fireEvent.click(screen.getByRole("button", { name: "layout.expandMenu" }));
    fireEvent.click(screen.getByRole("link", { name: "Open tasks" }));
    expect(resizeHandle()).toHaveAttribute("aria-valuenow", "256");
  });

  it("collapses after a server paper kind loads without relying on local storage and preserves manual expansion", () => {
    renderLayout("/lib/knowledge/detail/papers");
    expect(resizeHandle()).toHaveAttribute("aria-valuenow", "272");
    act(() => useDatasetPermissionStore.getState().setCurrentDataset({ dataset_id: "papers", library_kind: "paper", type: "DATASET_TYPE_TEXT" }));
    expect(resizeHandle()).toHaveAttribute("aria-valuenow", "72");
    fireEvent.click(screen.getByRole("button", { name: "layout.expandMenu" }));
    expect(resizeHandle()).toHaveAttribute("aria-valuenow", "272");
    act(() => useDatasetPermissionStore.getState().setCurrentDataset({ dataset_id: "papers", library_kind: "paper", type: "DATASET_TYPE_TEXT" }));
    expect(resizeHandle()).toHaveAttribute("aria-valuenow", "272");
    fireEvent.click(screen.getByRole("link", { name: "Open tasks" }));
    fireEvent.click(screen.getByRole("link", { name: "Open papers" }));
    expect(resizeHandle()).toHaveAttribute("aria-valuenow", "72");
  });

  it("uses an explicit server document kind over a stale paper marker", () => {
    localStorage.setItem("lazymind.paper-library.ids", JSON.stringify(["papers"]));
    useDatasetPermissionStore.getState().setCurrentDataset({ dataset_id: "papers", library_kind: "document", type: "DATASET_TYPE_TEXT" });
    renderLayout("/lib/knowledge/detail/papers");
    expect(resizeHandle()).toHaveAttribute("aria-valuenow", "272");
  });

  it("does not apply a different dataset's server kind to the current route", () => {
    useDatasetPermissionStore.getState().setCurrentDataset({ dataset_id: "papers", library_kind: "paper", type: "DATASET_TYPE_TEXT" });
    renderLayout("/lib/knowledge/detail/regular");
    expect(resizeHandle()).toHaveAttribute("aria-valuenow", "272");
  });

  it.each([
    "/memory-management/workflows/draft-1",
    "/memory-management/workflows/builtin/demo",
    "/memory-management/workflows/published/demo",
    "/memory-management/workflows/cloud/demo",
  ])("keeps the icon navigation available on %s", (path) => {
    renderLayout(path);
    expect(resizeHandle()).toHaveAttribute("aria-valuenow", "72");
    const navigation = document.getElementById("main-navigation");
    expect(navigation).toHaveStyle({ width: "72px" });
    expect(navigation).toContainElement(screen.getByRole("button", { name: "layout.expandMenu" }));
    for (const name of [
      "common.create", "layout.resourceLib", "layout.aiEvolution",
      "layout.taskCenter", "layout.searchConversations", "layout.conversationHistory", "layout.settings",
    ]) {
      expect(screen.getByRole("button", { name })).toBeVisible();
    }
  });

  it("offers both conversation modes from one compact create button and closes after selection", async () => {
    renderLayout("/agent/chat/home/current");
    fireEvent.click(screen.getByRole("button", { name: "layout.collapseMenu" }));
    expect(screen.queryByRole("button", { name: "layout.newChat" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "layout.newTask" })).not.toBeInTheDocument();
    const create = screen.getByRole("button", { name: "common.create" });
    expect(document.querySelectorAll(".sider-primary-action button")).toHaveLength(1);

    fireEvent.click(create);
    fireEvent.click(await screen.findByRole("menuitem", { name: "layout.newTask" }));
    expect(readChatConversationFilters().filter).toBe("task");
    expect(screen.getByTestId("location-path")).toHaveTextContent("/agent/chat/home");
    expect(screen.getByRole("button", { name: "common.create" })).toHaveAttribute("aria-expanded", "false");

    fireEvent.click(screen.getByRole("button", { name: "common.create" }));
    fireEvent.click(await screen.findByRole("menuitem", { name: "layout.newChat" }));
    expect(readChatConversationFilters().filter).toBe("normal");
    expect(screen.getByRole("button", { name: "common.create" })).toHaveAttribute("aria-expanded", "false");
    expect(resizeHandle()).toHaveAttribute("aria-valuenow", "72");
  });

  it.each([
    ["layout.taskCenter", "layout.taskCenter"],
    ["layout.searchConversations", "layout.searchConversations"],
    ["layout.conversationHistory", "layout.conversationHistory"],
    ["layout.settings", "layout.settings"],
    ["layout.terminalConnection", "layout.terminalConnection"],
    ["layout.expandMenu", "layout.expandMenu"],
    ["common.create", "common.create"],
  ])("shows a prompt tooltip for the compact %s icon without native title delays", async (name, label) => {
    renderLayout("/memory-management/workflows/draft-1");
    const button = screen.getByRole("button", { name });
    expect(button).not.toHaveAttribute("title");
    fireEvent.mouseEnter(button);
    await waitFor(() => expect(screen.getByRole("tooltip", { name: label })).toBeVisible(), { timeout: 500 });
  });

  it("resizes with the keyboard, clamps both limits and restores defaults on route changes", () => {
    renderLayout();
    fireEvent.keyDown(resizeHandle(), { key: "ArrowLeft" });
    expect(resizeHandle()).toHaveAttribute("aria-valuenow", "256");
    fireEvent.keyDown(resizeHandle(), { key: "Home" });
    fireEvent.keyDown(resizeHandle(), { key: "ArrowLeft" });
    expect(resizeHandle()).toHaveAttribute("aria-valuenow", "72");
    fireEvent.keyDown(resizeHandle(), { key: "End" });
    fireEvent.keyDown(resizeHandle(), { key: "ArrowRight" });
    expect(resizeHandle()).toHaveAttribute("aria-valuenow", "272");
    fireEvent.click(screen.getByRole("link", { name: "Open workflow" }));
    expect(resizeHandle()).toHaveAttribute("aria-valuenow", "72");
    fireEvent.keyDown(resizeHandle(), { key: "End" });
    expect(resizeHandle()).toHaveAttribute("aria-valuenow", "272");
    fireEvent.click(screen.getByRole("link", { name: "Open tasks" }));
    expect(resizeHandle()).toHaveAttribute("aria-valuenow", "272");
    fireEvent.click(screen.getByRole("link", { name: "Open workflow" }));
    expect(resizeHandle()).toHaveAttribute("aria-valuenow", "72");
  });

  it("updates width during dragging, retains it after release and cleans up on unmount", () => {
    const { unmount } = renderLayout();
    const handle = resizeHandle();
    handle.setPointerCapture = vi.fn();
    const pointer = (type: string, x: number) => {
      const event = new MouseEvent(type, { bubbles: true, clientX: x, button: 0 });
      Object.defineProperty(event, "pointerId", { value: 1 });
      fireEvent(handle, event);
    };
    pointer("pointerdown", 272);
    pointer("pointermove", 224);
    expect(handle).toHaveAttribute("aria-valuenow", "224");
    expect(document.body.style.cursor).toBe("col-resize");
    pointer("pointerup", 224);
    pointer("pointermove", 100);
    expect(handle).toHaveAttribute("aria-valuenow", "224");
    expect(document.body.style.cursor).toBe("");
    pointer("pointerdown", 224);
    pointer("pointermove", -100);
    expect(handle).toHaveAttribute("aria-valuenow", "72");
    pointer("pointermove", 900);
    expect(handle).toHaveAttribute("aria-valuenow", "272");
    unmount();
    expect(document.body.style.cursor).toBe("");
    expect(document.body.style.userSelect).toBe("");
  });

  it("keeps the compact width when navigating with sidebar controls or selecting a conversation", async () => {
    renderLayout("/agent/chat/home/current");
    fireEvent.keyDown(resizeHandle(), { key: "Home" });
    fireEvent.click(screen.getByRole("button", { name: "layout.taskCenter" }));
    expect(screen.getByTestId("location-path")).toHaveTextContent("/task-center");
    expect(resizeHandle()).toHaveAttribute("aria-valuenow", "72");
    fireEvent.click(screen.getByRole("button", { name: "layout.resourceLib" }));
    fireEvent.click(await screen.findByRole("button", { name: /layout.knowledgeBase/ }));
    expect(resizeHandle()).toHaveAttribute("aria-valuenow", "72");
    fireEvent.click(screen.getByRole("button", { name: "layout.aiEvolution" }));
    fireEvent.click(await screen.findByRole("button", { name: /admin.memoryTabSkills/ }));
    expect(resizeHandle()).toHaveAttribute("aria-valuenow", "72");
    fireEvent.click(screen.getByRole("button", { name: "layout.settings" }));
    fireEvent.click(await screen.findByRole("menuitem", { name: "layout.settings" }));
    expect(resizeHandle()).toHaveAttribute("aria-valuenow", "72");
    fireEvent.click(screen.getByRole("button", { name: "common.create" }));
    fireEvent.click(await screen.findByRole("menuitem", { name: "layout.newTask" }));
    expect(resizeHandle()).toHaveAttribute("aria-valuenow", "72");
    fireEvent.click(screen.getByRole("button", { name: "common.create" }));
    fireEvent.click(await screen.findByRole("menuitem", { name: "layout.newChat" }));
    expect(resizeHandle()).toHaveAttribute("aria-valuenow", "72");
    act(() => mocks.latestRecordListProps.onSelected({ conversation_id: "another-conversation" }));
    expect(screen.getByTestId("location-path")).toHaveTextContent("/agent/chat/home/another-conversation");
    expect(resizeHandle()).toHaveAttribute("aria-valuenow", "72");
  });

  it("snaps to the icon rail below the expanded minimum and reopens while dragging right", () => {
    renderLayout();
    const handle = resizeHandle();
    handle.setPointerCapture = vi.fn();
    const pointer = (type: string, clientX: number) => {
      const event = new MouseEvent(type, { bubbles: true, button: 0, clientX });
      Object.defineProperty(event, "pointerId", { value: 1 });
      fireEvent(handle, event);
    };
    pointer("pointerdown", 272);
    pointer("pointermove", 200);
    expect(handle).toHaveAttribute("aria-valuenow", "200");
    expect(screen.getByTestId("record-list")).toBeVisible();
    pointer("pointermove", 199);
    expect(handle).toHaveAttribute("aria-valuenow", "72");
    expect(screen.getByTestId("record-list")).not.toBeVisible();
    pointer("pointermove", 150);
    expect(handle).toHaveAttribute("aria-valuenow", "72");
    pointer("pointermove", 224);
    expect(handle).toHaveAttribute("aria-valuenow", "224");
    expect(screen.getByTestId("record-list")).toBeVisible();
    pointer("pointerup", 224);
    expect(handle).toHaveAttribute("aria-valuenow", "224");
  });

  it("uses keyboard arrows to cross the collapsed gap without getting stuck", () => {
    renderLayout();
    fireEvent.keyDown(resizeHandle(), { key: "Home" });
    fireEvent.keyDown(resizeHandle(), { key: "ArrowRight" });
    expect(resizeHandle()).toHaveAttribute("aria-valuenow", "200");
    fireEvent.keyDown(resizeHandle(), { key: "ArrowLeft" });
    expect(resizeHandle()).toHaveAttribute("aria-valuenow", "72");
    fireEvent.click(screen.getByRole("button", { name: "layout.taskCenter" }));
    expect(resizeHandle()).toHaveAttribute("aria-valuenow", "72");
  });

  it("preserves a custom width between pages and restores it after leaving workflow details", () => {
    renderLayout();
    for (let step = 0; step < 3; step++) fireEvent.keyDown(resizeHandle(), { key: "ArrowLeft" });
    expect(resizeHandle()).toHaveAttribute("aria-valuenow", "224");
    fireEvent.click(screen.getByRole("button", { name: "layout.taskCenter" }));
    expect(resizeHandle()).toHaveAttribute("aria-valuenow", "224");
    fireEvent.click(screen.getByRole("link", { name: "Open workflow" }));
    expect(resizeHandle()).toHaveAttribute("aria-valuenow", "72");
    fireEvent.keyDown(resizeHandle(), { key: "End" });
    fireEvent.click(screen.getByRole("link", { name: "Open tasks" }));
    expect(resizeHandle()).toHaveAttribute("aria-valuenow", "224");
    fireEvent.click(screen.getByRole("link", { name: "Open workflow" }));
    expect(resizeHandle()).toHaveAttribute("aria-valuenow", "72");
  });

  it("opens search and history from the icon rail without losing the search text", async () => {
    renderLayout("/memory-management/workflows/draft-1");
    fireEvent.click(screen.getByRole("button", { name: "layout.searchConversations" }));
    const search = screen.getByRole("searchbox");
    expect(search).toHaveFocus();
    fireEvent.change(search, { target: { value: "project" } });
    fireEvent.click(screen.getByRole("button", { name: "layout.collapseMenu" }));
    fireEvent.click(screen.getByRole("button", { name: "layout.conversationHistory" }));
    expect(screen.getByTestId("record-list")).toBeVisible();
    expect(screen.getByRole("searchbox")).toHaveValue("project");
    expect(resizeHandle()).toHaveAttribute("aria-valuenow", "272");
  });

  it.each(["pointercancel", "lostpointercapture", "blur"])("stops resizing when interrupted by %s", (type) => {
    renderLayout();
    const handle = resizeHandle();
    handle.setPointerCapture = vi.fn();
    const pointer = (eventType: string, button = 0, clientX = 120) => {
      const event = new MouseEvent(eventType, { bubbles: true, button, clientX });
      Object.defineProperty(event, "pointerId", { value: 1 });
      fireEvent(handle, event);
    };
    pointer("pointerdown", 2);
    expect(handle.setPointerCapture).not.toHaveBeenCalled();
    pointer("pointerdown");
    if (type === "blur") fireEvent(window, new Event("blur"));
    else pointer(type);
    pointer("pointermove", 0, 200);
    expect(handle).toHaveAttribute("aria-valuenow", "272");
    expect(document.body.style.cursor).toBe("");
    expect(document.body.style.userSelect).toBe("");
  });

  it("opens module subpages, settings and new tasks from the icon rail", async () => {
    renderLayout("/memory-management/workflows/draft-1");
    fireEvent.click(screen.getByRole("button", { name: "layout.resourceLib" }));
    fireEvent.click(await screen.findByRole("button", { name: /layout.knowledgeBase/ }));
    expect(screen.getByTestId("location-path")).toHaveTextContent("/lib/knowledge");
    fireEvent.click(screen.getByRole("link", { name: "Open workflow" }));
    fireEvent.click(screen.getByRole("button", { name: "layout.aiEvolution" }));
    fireEvent.click(await screen.findByRole("button", { name: /admin.memoryTabSkills/ }));
    expect(screen.getByTestId("location-path")).toHaveTextContent("/memory-management/skills");
    fireEvent.click(screen.getByRole("link", { name: "Open workflow" }));
    fireEvent.click(screen.getByRole("button", { name: "layout.settings" }));
    fireEvent.click(await screen.findByRole("menuitem", { name: "layout.settings" }));
    expect(screen.getByTestId("location-path")).toHaveTextContent("/settings");
    fireEvent.click(screen.getByRole("link", { name: "Open workflow" }));
    fireEvent.click(screen.getByRole("button", { name: "common.create" }));
    fireEvent.click(await screen.findByRole("menuitem", { name: "layout.newTask" }));
    expect(screen.getByTestId("location-path")).toHaveTextContent("/agent/chat/home");
    expect(readChatConversationFilters().filter).toBe("task");
  });

  it("preserves the narrow navigation while a workflow page is still loading", () => {
    const LoadingWorkflow = lazy(() => new Promise<{ default: () => null }>(() => {}));
    render(
      <MemoryRouter initialEntries={["/memory-management/workflows/draft-1"]}>
        <Suspense fallback={<div>Entire page loading</div>}>
          <Routes>
            <Route element={<MainLayout />}>
              <Route path="memory-management/workflows/:id" element={<LoadingWorkflow />} />
            </Route>
          </Routes>
        </Suspense>
      </MemoryRouter>,
    );
    expect(resizeHandle()).toHaveAttribute("aria-valuenow", "72");
    expect(screen.getByRole("button", { name: "common.create" })).toBeVisible();
    expect(screen.queryByText("Entire page loading")).not.toBeInTheDocument();
  });
});

describe("MainLayout document directory feature control", () => {
  it("hides navigation, stops polling and redirects an open directory when disabled", async () => {
    mocks.fetchPreferences.mockResolvedValue({ document_directories_enabled: true });
    mocks.listDirectoryBatches.mockClear();
    render(<MemoryRouter initialEntries={["/document-directories"]}>
      <Routes><Route element={<MainLayout />}>
        <Route path="/document-directories" element={<div>Directory content</div>} />
        <Route path="/settings" element={<div>Knowledge settings</div>} />
      </Route></Routes>
    </MemoryRouter>);
    expect(await screen.findByText("Directory content")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "layout.resourceLib" }));
    expect(await screen.findByText("directories.title")).toBeInTheDocument();
    await waitFor(() => expect(mocks.listDirectoryBatches).toHaveBeenCalled());
    const calls = mocks.listDirectoryBatches.mock.calls.length;
    act(() => window.dispatchEvent(new CustomEvent("lazymind:user-ui-preferences-changed", { detail: { document_directories_enabled: false } })));
    expect(await screen.findByText("Knowledge settings")).toBeInTheDocument();
    expect(screen.queryByText("Directory content")).not.toBeInTheDocument();
    expect(screen.queryByText("directories.title")).not.toBeInTheDocument();
    expect(mocks.listDirectoryBatches).toHaveBeenCalledTimes(calls);
    act(() => window.dispatchEvent(new CustomEvent("lazymind:user-ui-preferences-changed", { detail: { document_directories_enabled: true } })));
    expect(await screen.findByText("directories.title")).toBeInTheDocument();
  });
  it("does not load directory content or batches for an initially disabled user", async () => {
    mocks.fetchPreferences.mockResolvedValueOnce({ document_directories_enabled: false });
    mocks.listDirectoryBatches.mockClear();
    render(<MemoryRouter initialEntries={["/document-directories/example"]}>
      <Routes><Route element={<MainLayout />}>
        <Route path="/document-directories/:id" element={<div>Directory content</div>} />
        <Route path="/settings" element={<div>Knowledge settings</div>} />
      </Route></Routes>
    </MemoryRouter>);
    expect(await screen.findByText("Knowledge settings")).toBeInTheDocument();
    expect(screen.queryByText("Directory content")).not.toBeInTheDocument();
    expect(mocks.listDirectoryBatches).not.toHaveBeenCalled();
  });
});

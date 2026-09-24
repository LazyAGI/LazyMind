import { useConversationUnreadStore } from "@/modules/chat/store/conversationUnread";
import { lazy, Suspense } from "react";
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { Link, MemoryRouter, Route, Routes, useLocation } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

import MainLayout from "./MainLayout";
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
}));

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

  it("shows unread answers only for the current conversation and clears the product badge", () => {
    useConversationUnreadStore.setState({ counts: { current: 2, other: 5 } });
    render(<MemoryRouter initialEntries={["/agent/chat/home/current"]}><MainLayout /></MemoryRouter>);
    const logo = screen.getByRole("button", { name: "LazyMind", exact: true });
    expect(logo.querySelector(".ant-badge-count")).toHaveTextContent("2");
    expect(within(logo).getByRole("status")).toHaveTextContent("chat.unreadAnswers");
    act(() => useConversationUnreadStore.getState().setCount("current", 0));
    expect(within(logo).queryByRole("status")).not.toBeInTheDocument();
    expect(useConversationUnreadStore.getState().counts.other).toBe(5);
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
    mocks.initialOnRemove = null;
    mocks.latestRecordListProps = null;
    mocks.refreshRecordList.mockReset();
  });

  function renderLayout(path = "/agent/chat/home") {
    return render(
      <MemoryRouter initialEntries={[path]}>
        <MainLayout />
        <LocationProbe />
        <Link to="/memory-management/workflows/draft-1">Open workflow</Link>
        <Link to="/task-center">Open tasks</Link>
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
      "layout.newChat", "layout.newTask", "layout.resourceLib", "layout.aiEvolution",
      "layout.taskCenter", "layout.searchConversations", "layout.conversationHistory", "layout.settings",
    ]) {
      expect(screen.getByRole("button", { name })).toBeVisible();
    }
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
    fireEvent.click(screen.getByRole("button", { name: "layout.newTask" }));
    expect(resizeHandle()).toHaveAttribute("aria-valuenow", "72");
    fireEvent.click(screen.getByRole("button", { name: "layout.newChat" }));
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
    fireEvent.click(screen.getByRole("button", { name: "layout.newTask" }));
    expect(screen.getByTestId("location-path")).toHaveTextContent("/agent/chat/home");
    expect(screen.getByRole("button", { name: "layout.newTask" })).toHaveAttribute("aria-pressed", "true");
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
    expect(screen.getByRole("button", { name: "layout.newChat" })).toBeVisible();
    expect(screen.queryByText("Entire page loading")).not.toBeInTheDocument();
  });
});

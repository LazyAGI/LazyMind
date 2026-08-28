import { act, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, useLocation } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

import MainLayout from "./MainLayout";
import { CHAT_SELECT_CONVERSATION_EVENT } from "@/modules/chat/constants/chat";

const mocks = vi.hoisted(() => ({
  initialOnRemove: null as null | ((conversation: { conversation_id?: string }) => void),
  latestRecordListProps: null as any,
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

vi.mock("@/modules/chat/components/RecordList", () => ({
  default: (props: any) => {
    mocks.latestRecordListProps = props;
    mocks.initialOnRemove ??= props.onRemove;
    return <div data-testid="record-list" />;
  },
}));

function LocationProbe() {
  return <div data-testid="location-path">{useLocation().pathname}</div>;
}

describe("MainLayout conversation removal", () => {
  beforeEach(() => {
    sessionStorage.clear();
    localStorage.clear();
    mocks.initialOnRemove = null;
    mocks.latestRecordListProps = null;
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
});

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { I18nextProvider } from "react-i18next";
import { testI18n } from "@/test/testUtils";
import MainLayout from "./MainLayout";

vi.mock("./index.scss", () => ({}));
vi.mock("@/public/Lazy.png", () => ({ default: "logo.png" }));

vi.mock("@/modules/chat/components/RecordList", () => ({
  default: () => <div data-testid="record-list" />,
}));
vi.mock("@/components/LanguageSwitcher", () => ({
  default: () => <div data-testid="language-switcher" />,
}));
vi.mock("@/components/UserAgreementConsentModal", () => ({
  default: ({ open }: { open: boolean }) =>
    open ? <div data-testid="consent-modal" /> : null,
  useUserAgreementConsentGate: () => ({
    needsConsent: false,
    markAccepted: vi.fn(),
    loading: false,
  }),
}));

const getUserInfoMock = vi.hoisted(() => vi.fn());
const isLoggedInMock = vi.hoisted(() => vi.fn());
const logoutMock = vi.hoisted(() => vi.fn());
vi.mock("@/components/auth", () => ({
  AUTH_USER_CHANGE_EVENT: "lazymind:user-change",
  AgentAppsAuth: {
    getUserInfo: getUserInfoMock,
    isLoggedIn: isLoggedInMock,
    logout: logoutMock,
  },
}));

const fetchCurrentUserMock = vi.hoisted(() => vi.fn());
const fetchCurrentUserDetailMock = vi.hoisted(() => vi.fn());
const updateCurrentUserProfileMock = vi.hoisted(() => vi.fn());
const changeCurrentUserPasswordMock = vi.hoisted(() => vi.fn());
vi.mock("@/modules/signin/utils/request", () => ({
  fetchCurrentUser: fetchCurrentUserMock,
  fetchCurrentUserDetail: fetchCurrentUserDetailMock,
  updateCurrentUserProfile: updateCurrentUserProfileMock,
  changeCurrentUserPassword: changeCurrentUserPasswordMock,
}));

vi.mock("@/modules/signin/utils/formRules", () => ({
  validatePassword: () => Promise.resolve(),
}));

vi.mock("@/utils/developerMode", () => ({
  isDeveloperModeActive: () => false,
  persistDeveloperModeActive: vi.fn(),
  syncDeveloperModeFromServer: vi.fn().mockResolvedValue(false),
}));

vi.mock("@/runtime/features", () => ({
  runtimeFeatures: {
    hideEvo: true,
    hideCloudAdmin: false,
  },
}));

vi.mock("@/runtime/localSession", () => ({
  shouldHideLocalUserControls: () => false,
}));

const localSessionGateMock = vi.hoisted(() =>
  vi.fn(() => ({
    enabled: false,
    loading: false,
    error: "",
    retry: vi.fn(),
  })),
);
vi.mock("@/runtime/useLocalSessionGate", () => ({
  useLocalSessionGate: localSessionGateMock,
}));

function renderLayout(initialPath = "/agent/chat") {
  return render(
    <I18nextProvider i18n={testI18n}>
      <MemoryRouter initialEntries={[initialPath]}>
        <Routes>
          <Route path="*" element={<MainLayout />} />
        </Routes>
      </MemoryRouter>
    </I18nextProvider>,
  );
}

describe("MainLayout", () => {
  beforeEach(() => {
    localStorage.clear();
    getUserInfoMock.mockReset().mockReturnValue({
      token: "token-1",
      username: "alice",
      role: "admin",
    });
    isLoggedInMock.mockReset().mockReturnValue(true);
    logoutMock.mockReset();
    fetchCurrentUserMock.mockReset().mockResolvedValue({});
    localSessionGateMock.mockReset().mockReturnValue({
      enabled: false,
      loading: false,
      error: "",
      retry: vi.fn(),
    });
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("redirects to /login when the user is not logged in", () => {
    getUserInfoMock.mockReturnValue(null);
    render(
      <I18nextProvider i18n={testI18n}>
        <MemoryRouter initialEntries={["/agent/chat"]}>
          <Routes>
            <Route path="/login" element={<div data-testid="login-page" />} />
            <Route path="*" element={<MainLayout />} />
          </Routes>
        </MemoryRouter>
      </I18nextProvider>,
    );
    expect(screen.getByTestId("login-page")).toBeInTheDocument();
  });

  it("shows the local session gate loading state when the local session gate is enabled and loading", () => {
    localSessionGateMock.mockReturnValue({
      enabled: true,
      loading: true,
      error: "",
      retry: vi.fn(),
    });
    renderLayout();
    expect(screen.getByText("layout.preparingLocalSession")).toBeInTheDocument();
  });

  it("shows a retry button when the local session gate has an error", () => {
    const retry = vi.fn();
    localSessionGateMock.mockReturnValue({
      enabled: true,
      loading: false,
      error: "Something went wrong",
      retry,
    });
    renderLayout();
    expect(screen.getByText("Something went wrong")).toBeInTheDocument();
    fireEvent.click(screen.getByText("common.retry"));
    expect(retry).toHaveBeenCalledTimes(1);
  });

  it("renders the sidebar with the user's name when logged in", async () => {
    renderLayout();
    await waitFor(() => expect(fetchCurrentUserMock).toHaveBeenCalled());
    expect(screen.getByText("alice")).toBeInTheDocument();
    expect(screen.getByTestId("record-list")).toBeInTheDocument();
  });

  it("toggles the sidebar menu collapsed state when the collapse button is clicked", async () => {
    renderLayout();
    await waitFor(() => expect(fetchCurrentUserMock).toHaveBeenCalled());

    const toggleButton = screen.getByLabelText("layout.collapseMenu");
    fireEvent.click(toggleButton);

    expect(localStorage.getItem("lazymind:main-menu-collapsed")).toBe("1");
  });

  it("navigates to /agent/chat/home when starting a new chat", async () => {
    renderLayout();
    await waitFor(() => expect(fetchCurrentUserMock).toHaveBeenCalled());

    fireEvent.click(screen.getByText("layout.newChat"));

    await waitFor(() =>
      expect(screen.queryByText("layout.newChat")).toBeInTheDocument(),
    );
  });
});

import { describe, expect, it, vi, beforeEach } from "vitest";
import { renderWithProviders, screen, waitFor, fireEvent } from "@/test/testUtils";
import PreferenceConfigNotice from "./index";

const mockFetch = vi.fn();
const mockPatch = vi.fn();
const mockNavigate = vi.fn();

vi.mock("@/modules/user/uiPreferencesApi", () => ({
  fetchUserUiPreferences: (...args: unknown[]) => mockFetch(...args),
  patchUserUiPreferences: (...args: unknown[]) => mockPatch(...args),
}));

vi.mock("react-router-dom", async () => {
  const actual = await vi.importActual<typeof import("react-router-dom")>("react-router-dom");
  return { ...actual, useNavigate: () => mockNavigate };
});

describe("PreferenceConfigNotice", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockPatch.mockResolvedValue({});
  });

  it("renders nothing while hidden prop is true", async () => {
    renderWithProviders(<PreferenceConfigNotice hidden />);
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    expect(mockFetch).not.toHaveBeenCalled();
  });

  it("shows the notice when preferences are unconfigured and not dismissed", async () => {
    mockFetch.mockResolvedValue({
      chat_preference_notice_dismissed: false,
      user_preference_configured: false,
    });
    renderWithProviders(<PreferenceConfigNotice />);
    await waitFor(() =>
      expect(screen.getByText("chat.preferenceNotConfigured")).toBeInTheDocument(),
    );
  });

  it("does not show the notice when the user already configured preferences", async () => {
    mockFetch.mockResolvedValue({
      chat_preference_notice_dismissed: false,
      user_preference_configured: true,
    });
    renderWithProviders(<PreferenceConfigNotice />);
    await waitFor(() => expect(mockFetch).toHaveBeenCalled());
    expect(screen.queryByText("chat.preferenceNotConfigured")).not.toBeInTheDocument();
  });

  it("navigates to the experience page when the configure button is clicked", async () => {
    mockFetch.mockResolvedValue({
      chat_preference_notice_dismissed: false,
      user_preference_configured: false,
    });
    renderWithProviders(<PreferenceConfigNotice />);
    await waitFor(() =>
      expect(screen.getByText("chat.goToConfigure")).toBeInTheDocument(),
    );
    fireEvent.click(screen.getByText("chat.goToConfigure"));
    expect(mockNavigate).toHaveBeenCalledWith("/memory-management/experience");
  });

  it("dismisses the notice and persists the dismissal when 'don't show again' is clicked", async () => {
    mockFetch.mockResolvedValue({
      chat_preference_notice_dismissed: false,
      user_preference_configured: false,
    });
    renderWithProviders(<PreferenceConfigNotice />);
    await waitFor(() =>
      expect(screen.getByText("chat.dontShowAgain")).toBeInTheDocument(),
    );
    fireEvent.click(screen.getByText("chat.dontShowAgain"));
    expect(screen.queryByText("chat.preferenceNotConfigured")).not.toBeInTheDocument();
    expect(mockPatch).toHaveBeenCalledWith({ chat_preference_notice_dismissed: true });
  });
});

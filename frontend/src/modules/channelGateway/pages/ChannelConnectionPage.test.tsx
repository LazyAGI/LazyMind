import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, renderWithProviders, screen, waitFor } from "@/test/testUtils";
import { TerminalConnectionPage } from "./ChannelConnectionPage";

const listChannelAccountsMock = vi.hoisted(() => vi.fn());
const disconnectChannelAccountMock = vi.hoisted(() => vi.fn());

vi.mock("../api", () => ({
  listChannelAccounts: listChannelAccountsMock,
  disconnectChannelAccount: disconnectChannelAccountMock,
}));

const useChannelConnectionMock = vi.hoisted(() => vi.fn());
vi.mock("../hooks/useChannelConnection", () => ({
  useChannelConnection: useChannelConnectionMock,
}));

vi.mock("../channelConnectionPage.scss", () => ({}));

function baseHookState(overrides: Partial<ReturnType<typeof defaultHookState>> = {}) {
  return { ...defaultHookState(), ...overrides };
}

function defaultHookState() {
  return {
    t: (key: string) => key,
    accounts: [] as unknown[],
    session: null as unknown,
    sessionStarting: false,
    actionLoading: false,
    challengeValue: "",
    setChallengeValue: vi.fn(),
    startScan: vi.fn().mockResolvedValue(undefined),
    cancelScan: vi.fn().mockResolvedValue(undefined),
    refreshQr: vi.fn().mockResolvedValue(undefined),
    submitChallenge: vi.fn().mockResolvedValue(undefined),
    closeSessionPanel: vi.fn(),
  };
}

describe("TerminalConnectionPage", () => {
  beforeEach(() => {
    listChannelAccountsMock.mockReset().mockResolvedValue({ items: [] });
    disconnectChannelAccountMock.mockReset().mockResolvedValue(undefined);
    useChannelConnectionMock.mockReset().mockReturnValue(baseHookState());
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("renders the idle guide and starts a scan when the button is clicked", async () => {
    const startScan = vi.fn().mockResolvedValue(undefined);
    useChannelConnectionMock.mockReturnValue(baseHookState({ startScan }));

    renderWithProviders(<TerminalConnectionPage />);
    await waitFor(() => expect(listChannelAccountsMock).toHaveBeenCalled());

    expect(screen.getByText("channelGateway.wechat.readyTitle")).toBeInTheDocument();
    fireEvent.click(screen.getByText("channelGateway.wechat.startScan"));
    expect(startScan).toHaveBeenCalledTimes(1);
  });

  it("switches the active provider when a provider tab is clicked", async () => {
    renderWithProviders(<TerminalConnectionPage />);
    await waitFor(() => expect(listChannelAccountsMock).toHaveBeenCalled());

    fireEvent.click(screen.getByText("channelGateway.terminal.feishuTitle"));
    expect(useChannelConnectionMock).toHaveBeenCalledWith("feishu");
  });

  it("renders a QR code and challenge input while a session is active", async () => {
    useChannelConnectionMock.mockReturnValue(
      baseHookState({
        session: {
          id: "s1",
          status: "verification_required",
          message: "scan needed",
          allowed_actions: ["cancel", "submit_challenge"],
          qr: { payload: "qr-data", expires_at: null },
        },
      }),
    );

    renderWithProviders(<TerminalConnectionPage />);
    await waitFor(() => expect(listChannelAccountsMock).toHaveBeenCalled());

    expect(screen.getByText("scan needed")).toBeInTheDocument();
    expect(
      screen.getByPlaceholderText("channelGateway.wechat.challengePlaceholder"),
    ).toBeInTheDocument();
    expect(screen.getByText("channelGateway.wechat.cancelScan")).toBeInTheDocument();
  });

  it("submits the challenge value when the submit button is clicked", async () => {
    const submitChallenge = vi.fn().mockResolvedValue(undefined);
    useChannelConnectionMock.mockReturnValue(
      baseHookState({
        challengeValue: "123456",
        submitChallenge,
        session: {
          id: "s1",
          status: "verification_required",
          message: "scan needed",
          allowed_actions: ["submit_challenge"],
          qr: { payload: "qr-data" },
        },
      }),
    );

    renderWithProviders(<TerminalConnectionPage />);
    await waitFor(() => expect(listChannelAccountsMock).toHaveBeenCalled());

    fireEvent.click(screen.getByText("channelGateway.wechat.submitChallenge"));
    expect(submitChallenge).toHaveBeenCalledTimes(1);
  });

  it("opens the accounts panel and shows the fetched account rows in the modal", async () => {
    listChannelAccountsMock.mockImplementation((provider: string) =>
      Promise.resolve({
        items:
          provider === "wechat"
            ? [{ id: "acc-1", provider: "wechat", label: "My WeChat", status: "connected", runtime_status: "running" }]
            : [],
      }),
    );

    renderWithProviders(<TerminalConnectionPage />);
    await waitFor(() => expect(listChannelAccountsMock).toHaveBeenCalledTimes(2));

    fireEvent.click(screen.getByRole("button", { name: /channelGateway.terminal.viewAccounts/ }));

    await waitFor(() => expect(screen.getByText("My WeChat")).toBeInTheDocument());
  });

  it("shows a load error message when fetching accounts fails", async () => {
    listChannelAccountsMock.mockRejectedValue(new Error("network down"));
    const { message } = await import("antd");
    const errorSpy = vi.spyOn(message, "error");

    renderWithProviders(<TerminalConnectionPage />);

    await waitFor(() =>
      expect(errorSpy).toHaveBeenCalledWith("channelGateway.terminal.loadAccountsFailed"),
    );
  });
});

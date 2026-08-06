import { act, renderHook, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const apiMocks = vi.hoisted(() => ({
  listChannelAccounts: vi.fn(),
  disconnectChannelAccount: vi.fn(),
  createConnectionSession: vi.fn(),
  getConnectionSession: vi.fn(),
  submitConnectionChallenge: vi.fn(),
  refreshConnectionSession: vi.fn(),
  cancelConnectionSession: vi.fn(),
}));

const messageMocks = vi.hoisted(() => ({
  success: vi.fn(),
  error: vi.fn(),
  warning: vi.fn(),
}));

vi.mock("../api", () => ({
  listChannelAccounts: apiMocks.listChannelAccounts,
  disconnectChannelAccount: apiMocks.disconnectChannelAccount,
  createConnectionSession: apiMocks.createConnectionSession,
  getConnectionSession: apiMocks.getConnectionSession,
  submitConnectionChallenge: apiMocks.submitConnectionChallenge,
  refreshConnectionSession: apiMocks.refreshConnectionSession,
  cancelConnectionSession: apiMocks.cancelConnectionSession,
}));

vi.mock("antd", () => ({
  message: messageMocks,
}));

const stableT = (key: string) => key;
vi.mock("react-i18next", () => ({
  useTranslation: () => ({ t: stableT }),
}));

vi.mock("uuid", () => ({
  v4: () => "uuid-1",
}));

import { useChannelConnection } from "./useChannelConnection";

beforeEach(() => {
  Object.values(apiMocks).forEach((mockFn) => mockFn.mockReset());
  Object.values(messageMocks).forEach((mockFn) => mockFn.mockReset());
  apiMocks.listChannelAccounts.mockResolvedValue({ items: [] });
  apiMocks.cancelConnectionSession.mockResolvedValue(undefined);
});

describe("useChannelConnection - loadAccounts", () => {
  it("loads accounts on mount and clears the loading flag", async () => {
    apiMocks.listChannelAccounts.mockResolvedValue({ items: [{ id: "a1" }] });
    const { result } = renderHook(() => useChannelConnection("wechat"));

    expect(result.current.accountsLoading).toBe(true);
    await waitFor(() => expect(result.current.accountsLoading).toBe(false));
    expect(result.current.accounts).toEqual([{ id: "a1" }]);
  });

  it("shows an error message when loading accounts fails", async () => {
    apiMocks.listChannelAccounts.mockRejectedValue(new Error("boom"));
    const { result } = renderHook(() => useChannelConnection("wechat"));

    await waitFor(() => expect(result.current.accountsLoading).toBe(false));
    expect(messageMocks.error).toHaveBeenCalled();
  });
});

describe("useChannelConnection - startScan", () => {
  it("sets the session and schedules polling for a pending session", async () => {
    apiMocks.createConnectionSession.mockResolvedValue({
      id: "s1",
      status: "pending",
      poll_after_ms: 1000,
    });
    const { result } = renderHook(() => useChannelConnection("wechat"));
    await waitFor(() => expect(result.current.accountsLoading).toBe(false));

    await act(async () => {
      await result.current.startScan();
    });

    expect(apiMocks.createConnectionSession).toHaveBeenCalledWith("wechat", {
      idempotencyKey: "uuid-1",
    });
    expect(result.current.session).toEqual(
      expect.objectContaining({ id: "s1", status: "pending" }),
    );
    expect(result.current.sessionStarting).toBe(false);
  });

  it("shows success and reloads accounts when the session connects immediately", async () => {
    apiMocks.createConnectionSession.mockResolvedValue({ id: "s1", status: "connected" });
    apiMocks.listChannelAccounts.mockResolvedValue({ items: [{ id: "a1" }] });
    const { result } = renderHook(() => useChannelConnection("feishu"));
    await waitFor(() => expect(result.current.accountsLoading).toBe(false));

    await act(async () => {
      await result.current.startScan();
    });

    expect(messageMocks.success).toHaveBeenCalled();
    expect(result.current.accounts).toEqual([{ id: "a1" }]);
  });

  it("shows an error message when creating the session fails", async () => {
    apiMocks.createConnectionSession.mockRejectedValue(new Error("network down"));
    const { result } = renderHook(() => useChannelConnection("wechat"));
    await waitFor(() => expect(result.current.accountsLoading).toBe(false));

    await act(async () => {
      await result.current.startScan();
    });

    expect(messageMocks.error).toHaveBeenCalledWith("network down");
    expect(result.current.sessionStarting).toBe(false);
  });
});

describe("useChannelConnection - submitChallenge", () => {
  it("warns and does not submit when the value is not digits-only", async () => {
    const { result } = renderHook(() => useChannelConnection("wechat"));
    await waitFor(() => expect(result.current.accountsLoading).toBe(false));

    apiMocks.createConnectionSession.mockResolvedValue({ id: "s1", status: "verification_required" });
    await act(async () => {
      await result.current.startScan();
    });

    act(() => {
      result.current.setChallengeValue("abc123");
    });

    await act(async () => {
      await result.current.submitChallenge();
    });

    expect(messageMocks.warning).toHaveBeenCalled();
    expect(apiMocks.submitConnectionChallenge).not.toHaveBeenCalled();
  });

  it("submits a numeric challenge and reloads accounts on success", async () => {
    const { result } = renderHook(() => useChannelConnection("wechat"));
    await waitFor(() => expect(result.current.accountsLoading).toBe(false));

    apiMocks.createConnectionSession.mockResolvedValue({ id: "s1", status: "verification_required" });
    await act(async () => {
      await result.current.startScan();
    });

    act(() => {
      result.current.setChallengeValue("123456");
    });

    apiMocks.submitConnectionChallenge.mockResolvedValue({ id: "s1", status: "connected" });
    apiMocks.listChannelAccounts.mockResolvedValue({ items: [{ id: "a1" }] });

    await act(async () => {
      await result.current.submitChallenge();
    });

    expect(apiMocks.submitConnectionChallenge).toHaveBeenCalledWith("s1", "123456");
    expect(messageMocks.success).toHaveBeenCalled();
    expect(result.current.accounts).toEqual([{ id: "a1" }]);
  });

  it("does nothing when there is no active session", async () => {
    const { result } = renderHook(() => useChannelConnection("wechat"));
    await waitFor(() => expect(result.current.accountsLoading).toBe(false));

    act(() => {
      result.current.setChallengeValue("123456");
    });

    await act(async () => {
      await result.current.submitChallenge();
    });

    expect(apiMocks.submitConnectionChallenge).not.toHaveBeenCalled();
  });
});

describe("useChannelConnection - cancelScan / disconnectAccount / closeSessionPanel", () => {
  it("cancelScan clears the session and shows a success message", async () => {
    const { result } = renderHook(() => useChannelConnection("wechat"));
    await waitFor(() => expect(result.current.accountsLoading).toBe(false));

    apiMocks.createConnectionSession.mockResolvedValue({ id: "s1", status: "pending" });
    await act(async () => {
      await result.current.startScan();
    });

    await act(async () => {
      await result.current.cancelScan();
    });

    expect(apiMocks.cancelConnectionSession).toHaveBeenCalledWith("s1");
    expect(result.current.session).toBeNull();
    expect(messageMocks.success).toHaveBeenCalled();
  });

  it("cancelScan is a no-op when there is no active session", async () => {
    const { result } = renderHook(() => useChannelConnection("wechat"));
    await waitFor(() => expect(result.current.accountsLoading).toBe(false));

    await act(async () => {
      await result.current.cancelScan();
    });

    expect(apiMocks.cancelConnectionSession).not.toHaveBeenCalled();
  });

  it("disconnectAccount reloads the account list on success", async () => {
    apiMocks.disconnectChannelAccount.mockResolvedValue(undefined);
    apiMocks.listChannelAccounts.mockResolvedValueOnce({ items: [{ id: "a1" }] });
    const { result } = renderHook(() => useChannelConnection("wechat"));
    await waitFor(() => expect(result.current.accountsLoading).toBe(false));

    apiMocks.listChannelAccounts.mockResolvedValueOnce({ items: [] });
    await act(async () => {
      await result.current.disconnectAccount("a1");
    });

    expect(apiMocks.disconnectChannelAccount).toHaveBeenCalledWith("a1");
    expect(messageMocks.success).toHaveBeenCalled();
    expect(result.current.disconnectingAccountId).toBeNull();
  });

  it("closeSessionPanel resets the session state", async () => {
    const { result } = renderHook(() => useChannelConnection("wechat"));
    await waitFor(() => expect(result.current.accountsLoading).toBe(false));

    apiMocks.createConnectionSession.mockResolvedValue({ id: "s1", status: "pending" });
    await act(async () => {
      await result.current.startScan();
    });

    act(() => {
      result.current.closeSessionPanel();
    });

    expect(result.current.session).toBeNull();
  });
});

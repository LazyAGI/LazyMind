import { act, renderHook, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("./localSession", () => ({
  ensureLocalSession: vi.fn(),
  isLocalSessionEnabled: vi.fn(),
}));

import { ensureLocalSession, isLocalSessionEnabled } from "./localSession";
import { useLocalSessionGate } from "./useLocalSessionGate";

const mockedEnsureLocalSession = ensureLocalSession as unknown as ReturnType<typeof vi.fn>;
const mockedIsLocalSessionEnabled = isLocalSessionEnabled as unknown as ReturnType<typeof vi.fn>;

describe("useLocalSessionGate", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("is a no-op when local session is disabled", async () => {
    mockedIsLocalSessionEnabled.mockReturnValue(false);
    const refreshLayoutUser = vi.fn().mockResolvedValue(undefined);

    const { result } = renderHook(() => useLocalSessionGate(refreshLayoutUser));

    expect(result.current.enabled).toBe(false);
    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(mockedEnsureLocalSession).not.toHaveBeenCalled();
    expect(refreshLayoutUser).not.toHaveBeenCalled();
  });

  it("restores the session and refreshes the layout user on mount when enabled", async () => {
    mockedIsLocalSessionEnabled.mockReturnValue(true);
    mockedEnsureLocalSession.mockResolvedValue({ token: "tok" });
    const refreshLayoutUser = vi.fn().mockResolvedValue(undefined);

    const { result } = renderHook(() => useLocalSessionGate(refreshLayoutUser));

    expect(result.current.loading).toBe(true);
    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(mockedEnsureLocalSession).toHaveBeenCalledWith();
    expect(refreshLayoutUser).toHaveBeenCalledTimes(1);
    expect(result.current.error).toBe("");
  });

  it("sets an error message when restoring the session fails", async () => {
    mockedIsLocalSessionEnabled.mockReturnValue(true);
    mockedEnsureLocalSession.mockRejectedValue(new Error("network down"));
    const refreshLayoutUser = vi.fn().mockResolvedValue(undefined);
    vi.spyOn(console, "error").mockImplementation(() => {});

    const { result } = renderHook(() => useLocalSessionGate(refreshLayoutUser));

    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.error).toBeTruthy();
    expect(refreshLayoutUser).not.toHaveBeenCalled();
  });

  it("retry forces a fresh session restore", async () => {
    mockedIsLocalSessionEnabled.mockReturnValue(true);
    mockedEnsureLocalSession.mockResolvedValue({ token: "tok" });
    const refreshLayoutUser = vi.fn().mockResolvedValue(undefined);

    const { result } = renderHook(() => useLocalSessionGate(refreshLayoutUser));
    await waitFor(() => expect(result.current.loading).toBe(false));

    mockedEnsureLocalSession.mockClear();
    await act(async () => {
      await result.current.retry();
    });

    expect(mockedEnsureLocalSession).toHaveBeenCalledWith({ force: true });
  });
});

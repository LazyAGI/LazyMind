import { afterEach, describe, expect, it, vi } from "vitest";

vi.mock("@/modules/user/uiPreferencesApi", () => ({
  fetchUserUiPreferences: vi.fn(),
  patchUserUiPreferences: vi.fn(),
}));

import {
  DEVELOPER_ACTIVE_EVENT,
  DEVELOPER_ACTIVE_STORAGE_KEY,
  isDeveloperModeActive,
  persistDeveloperModeActive,
  setDeveloperModeActive,
  syncDeveloperModeFromServer,
} from "./developerMode";
import {
  fetchUserUiPreferences,
  patchUserUiPreferences,
} from "@/modules/user/uiPreferencesApi";

describe("isDeveloperModeActive / setDeveloperModeActive", () => {
  afterEach(() => {
    localStorage.clear();
    vi.restoreAllMocks();
  });

  it("is false by default and true after activation", () => {
    expect(isDeveloperModeActive()).toBe(false);
    setDeveloperModeActive(true);
    expect(isDeveloperModeActive()).toBe(true);
    expect(localStorage.getItem(DEVELOPER_ACTIVE_STORAGE_KEY)).toBe("1");
  });

  it("removes the storage key when deactivated", () => {
    setDeveloperModeActive(true);
    setDeveloperModeActive(false);
    expect(isDeveloperModeActive()).toBe(false);
    expect(localStorage.getItem(DEVELOPER_ACTIVE_STORAGE_KEY)).toBeNull();
  });

  it("dispatches a custom event with the active flag", () => {
    const listener = vi.fn();
    window.addEventListener(DEVELOPER_ACTIVE_EVENT, listener);
    setDeveloperModeActive(true);
    expect(listener).toHaveBeenCalledTimes(1);
    const event = listener.mock.calls[0][0] as CustomEvent<{ active: boolean }>;
    expect(event.detail).toEqual({ active: true });
    window.removeEventListener(DEVELOPER_ACTIVE_EVENT, listener);
  });

  it("returns false when localStorage access throws", () => {
    const spy = vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
      throw new Error("blocked");
    });
    expect(isDeveloperModeActive()).toBe(false);
    spy.mockRestore();
  });
});

describe("syncDeveloperModeFromServer", () => {
  afterEach(() => {
    localStorage.clear();
    vi.restoreAllMocks();
  });

  it("applies and returns the server-provided developer_mode_active flag", async () => {
    vi.mocked(fetchUserUiPreferences).mockResolvedValue({
      developer_mode_active: true,
    } as never);
    await expect(syncDeveloperModeFromServer()).resolves.toBe(true);
    expect(isDeveloperModeActive()).toBe(true);
  });

  it("falls back to the current local state when the request fails", async () => {
    vi.spyOn(console, "error").mockImplementation(() => {});
    setDeveloperModeActive(true);
    vi.mocked(fetchUserUiPreferences).mockRejectedValue(new Error("network error"));
    await expect(syncDeveloperModeFromServer()).resolves.toBe(true);
  });
});

describe("persistDeveloperModeActive", () => {
  afterEach(() => {
    localStorage.clear();
    vi.restoreAllMocks();
  });

  it("sets local state and patches the server", async () => {
    vi.mocked(patchUserUiPreferences).mockResolvedValue({} as never);
    await persistDeveloperModeActive(true);
    expect(isDeveloperModeActive()).toBe(true);
    expect(patchUserUiPreferences).toHaveBeenCalledWith({ developer_mode_active: true });
  });

  it("still sets local state even when the server patch fails", async () => {
    vi.spyOn(console, "error").mockImplementation(() => {});
    vi.mocked(patchUserUiPreferences).mockRejectedValue(new Error("network error"));
    await persistDeveloperModeActive(false);
    expect(isDeveloperModeActive()).toBe(false);
  });
});

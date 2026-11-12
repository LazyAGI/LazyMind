import { afterEach, describe, expect, it, vi } from "vitest";
import { desktopUpdateStatus, desktopUpdateCheck, desktopUpdateDownload, desktopUpdateCancel, desktopUpdateReveal, onDesktopUpdateState } from "./desktopUpdates";

afterEach(() => { Reflect.deleteProperty(window, "lazymindDesktop"); vi.restoreAllMocks(); });

describe("desktop update bridge", () => {
  it("does not invent an HTTP fallback in a web browser", async () => {
    const fetch = vi.spyOn(globalThis, "fetch");
    expect(await desktopUpdateStatus()).toBeNull();
    await expect(desktopUpdateCheck()).rejects.toThrow();
    await expect(desktopUpdateDownload("r2")).rejects.toThrow();
    expect(fetch).not.toHaveBeenCalled();
  });
  it("forwards only fixed actions and preserves the authoritative state", async () => {
    const state = { revision: 4, phase: "available", currentVersion: "1.0.0", releaseId: "r2", canInstall: false };
    const bridge = { updateStatus: vi.fn().mockResolvedValue(state), updateCheck: vi.fn().mockResolvedValue(state), updateDownload: vi.fn().mockResolvedValue(state), updateCancel: vi.fn().mockResolvedValue(state), updateReveal: vi.fn().mockResolvedValue(undefined), onUpdateState: vi.fn().mockReturnValue(() => {}) };
    Object.defineProperty(window, "lazymindDesktop", { configurable: true, value: bridge });
    expect(await desktopUpdateStatus()).toEqual(state);
    expect(await desktopUpdateCheck()).toEqual(state);
    await desktopUpdateDownload("r2"); expect(bridge.updateDownload).toHaveBeenCalledWith("r2");
    await desktopUpdateCancel(); expect(bridge.updateCancel).toHaveBeenCalledWith();
    await desktopUpdateReveal(); expect(bridge.updateReveal).toHaveBeenCalledWith();
    const unsubscribe = onDesktopUpdateState(() => {}); expect(typeof unsubscribe).toBe("function");
  });
});

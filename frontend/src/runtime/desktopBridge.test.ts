import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("./mode", () => ({
  isDesktopRuntime: vi.fn(),
}));

import { isDesktopRuntime } from "./mode";
import {
  exportDiagnostics,
  openDataDir,
  openLogsDir,
  resetRuntime,
  restartRuntime,
  runtimeStatus,
  selectExecutable,
  selectFolder,
} from "./desktopBridge";

const mockedIsDesktopRuntime = isDesktopRuntime as unknown as ReturnType<typeof vi.fn>;

describe("desktopBridge", () => {
  afterEach(() => {
    delete (window as Window & { lazymindDesktop?: unknown }).lazymindDesktop;
    vi.restoreAllMocks();
  });

  describe("when not running in desktop runtime", () => {
    beforeEach(() => {
      mockedIsDesktopRuntime.mockReturnValue(false);
    });

    it("returns unavailable for every bridge call without throwing", async () => {
      await expect(openLogsDir()).resolves.toEqual({ ok: false, reason: "unavailable" });
      await expect(openDataDir()).resolves.toEqual({ ok: false, reason: "unavailable" });
      await expect(restartRuntime()).resolves.toEqual({ ok: false, reason: "unavailable" });
      await expect(resetRuntime()).resolves.toEqual({ ok: false, reason: "unavailable" });
      await expect(runtimeStatus()).resolves.toEqual({ ok: false, reason: "unavailable" });
      await expect(selectFolder()).resolves.toBeNull();
      await expect(selectExecutable()).resolves.toBeNull();
      await expect(exportDiagnostics()).resolves.toBeNull();
    });
  });

  describe("when running in desktop runtime but window.lazymindDesktop is absent", () => {
    beforeEach(() => {
      mockedIsDesktopRuntime.mockReturnValue(true);
    });

    it("does not throw and reports unavailable", async () => {
      await expect(openLogsDir()).resolves.toEqual({ ok: false, reason: "unavailable" });
      await expect(runtimeStatus()).resolves.toEqual({ ok: false, reason: "unavailable" });
      await expect(selectFolder()).resolves.toBeNull();
    });
  });

  describe("when the bridge is present", () => {
    beforeEach(() => {
      mockedIsDesktopRuntime.mockReturnValue(true);
    });

    it("calls through to the bridge and returns ok on success", async () => {
      const openLogsDirMock = vi.fn().mockResolvedValue(undefined);
      (window as Window & { lazymindDesktop?: unknown }).lazymindDesktop = {
        openLogsDir: openLogsDirMock,
      };
      await expect(openLogsDir()).resolves.toEqual({ ok: true });
      expect(openLogsDirMock).toHaveBeenCalledTimes(1);
    });

    it("returns a failed result when the bridge handler throws", async () => {
      const error = new Error("boom");
      (window as Window & { lazymindDesktop?: unknown }).lazymindDesktop = {
        openLogsDir: vi.fn().mockRejectedValue(error),
      };
      await expect(openLogsDir()).resolves.toEqual({ ok: false, reason: "failed", error });
    });

    it("runtimeStatus resolves data on success", async () => {
      (window as Window & { lazymindDesktop?: unknown }).lazymindDesktop = {
        runtimeStatus: vi.fn().mockResolvedValue({ overallStatus: "ready", services: {} }),
      };
      const result = await runtimeStatus();
      expect(result).toEqual({
        ok: true,
        data: { overallStatus: "ready", services: {} },
      });
    });

    it("runtimeStatus reports failed when the bridge call rejects", async () => {
      const error = new Error("status failed");
      (window as Window & { lazymindDesktop?: unknown }).lazymindDesktop = {
        runtimeStatus: vi.fn().mockRejectedValue(error),
      };
      await expect(runtimeStatus()).resolves.toEqual({ ok: false, reason: "failed", error });
    });

    it("resetRuntime forwards the scope argument", async () => {
      const resetRuntimeMock = vi.fn().mockResolvedValue(undefined);
      (window as Window & { lazymindDesktop?: unknown }).lazymindDesktop = {
        resetRuntime: resetRuntimeMock,
      };
      await expect(resetRuntime("kb")).resolves.toEqual({ ok: true });
      expect(resetRuntimeMock).toHaveBeenCalledWith("kb");
    });

    it("selectFolder returns the selected path from the bridge", async () => {
      (window as Window & { lazymindDesktop?: unknown }).lazymindDesktop = {
        selectFolder: vi.fn().mockResolvedValue("/tmp/some-folder"),
      };
      await expect(selectFolder()).resolves.toBe("/tmp/some-folder");
    });

    it("exportDiagnostics returns the bridge result", async () => {
      (window as Window & { lazymindDesktop?: unknown }).lazymindDesktop = {
        exportDiagnostics: vi.fn().mockResolvedValue("/tmp/diag.zip"),
      };
      await expect(exportDiagnostics()).resolves.toBe("/tmp/diag.zip");
    });
  });
});

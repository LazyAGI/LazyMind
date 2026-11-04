import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import * as navigation from "./openManagedAuthorization";

const DEVICE_URL = "https://github.com/login/device";
// A new export is an integration contract, not a test stub or shadow implementation.
function open(url: string, popup?: Window | null) {
  const implementation = (navigation as unknown as Record<string, unknown>).openGitHubCLIAuthorization;
  expect(implementation, "GitHub navigation entrypoint must be implemented").toBeTypeOf("function");
  return (implementation as (url: string, popup?: Window | null) => Promise<{ ok: boolean; reason?: string }>)(url, popup);
}
function popup() {
  const doc = document.implementation.createHTMLDocument();
  const navigations = vi.fn();
  const reserved = { closed: false, opener: window, document: doc, navigations,
    close: vi.fn(), location: { replace: vi.fn() } };
  doc.addEventListener("click", event => {
    event.preventDefault(); // jsdom cannot navigate; the browser probe checks the real Referer.
    const link = event.target as HTMLAnchorElement;
    navigations({ href: link.href, rel: link.rel, target: link.target,
      policy: link.referrerPolicy || link.getAttribute("referrerpolicy"), opener: reserved.opener });
  });
  return reserved as unknown as Window & { navigations: ReturnType<typeof vi.fn> };
}
beforeEach(() => { Reflect.deleteProperty(window, "lazymindDesktop"); });
afterEach(() => { vi.restoreAllMocks(); Reflect.deleteProperty(window, "lazymindDesktop"); });

describe("GitHub device authorization navigation", () => {
  it("uses the dedicated Desktop API and closes an unused reserved popup", async () => {
    const bridge = vi.fn().mockResolvedValue({ opened: true });
    Object.defineProperty(window, "lazymindDesktop", { configurable: true, value: { openGitHubCLIAuthorization: bridge } });
    const nativeOpen = vi.spyOn(window, "open");
    const reserved = popup();
    expect(await open(DEVICE_URL, reserved)).toEqual({ ok: true });
    expect(bridge).toHaveBeenCalledOnce();
    expect(bridge).toHaveBeenCalledWith(DEVICE_URL);
    expect(reserved.close).toHaveBeenCalledOnce();
    expect(nativeOpen).not.toHaveBeenCalled();
  });
  it("uses an isolated ordinary browser popup for the fixed official URL", async () => {
    const reserved = popup();
    const nativeOpen = vi.spyOn(window, "open").mockReturnValue(reserved);
    expect(await open(DEVICE_URL)).toEqual({ ok: true });
    expect(nativeOpen).toHaveBeenCalledWith("about:blank", "_blank");
    expect(nativeOpen).toHaveBeenCalledOnce();
    expect(reserved.navigations).toHaveBeenCalledOnce();
    expect(reserved.navigations).toHaveBeenCalledWith({ href: DEVICE_URL, rel: "noreferrer",
      target: "_self", policy: "no-referrer", opener: null });
    expect(reserved.location.replace).not.toHaveBeenCalled();
  });
  it("reuses a reserved popup without opening a second window", async () => {
    const reserved = popup();
    const nativeOpen = vi.spyOn(window, "open");
    expect(await open(DEVICE_URL, reserved)).toEqual({ ok: true });
    expect(reserved.navigations).toHaveBeenCalledOnce();
    expect(reserved.navigations).toHaveBeenCalledWith({ href: DEVICE_URL, rel: "noreferrer",
      target: "_self", policy: "no-referrer", opener: null });
    expect(reserved.location.replace).not.toHaveBeenCalled();
    expect(nativeOpen).not.toHaveBeenCalled();
  });
  it.each([
    "http://github.com/login/device", "https://github.com.evil.test/login/device",
    "https://evil.test@github.com/login/device", "https://github.com:444/login/device",
    "https://github.com/login/device?user_code=ABCD-EFGH", "https://github.com/login/device#fixture",
    "https://github.com/login/device/", "https://github.com/login/oauth/authorize",
    "https://github.com/login/%64evice", "javascript:alert(1)", "file:///tmp/github", "",
  ])("rejects %s before any navigation", async url => {
    const bridge = vi.fn();
    Object.defineProperty(window, "lazymindDesktop", { configurable: true, value: { openGitHubCLIAuthorization: bridge } });
    const nativeOpen = vi.spyOn(window, "open");
    const reserved = popup();
    expect(await open(url, reserved)).toEqual({ ok: false, reason: "invalid" });
    expect(bridge).not.toHaveBeenCalled();
    expect(nativeOpen).not.toHaveBeenCalled();
    expect(reserved.location.replace).not.toHaveBeenCalled();
    expect(reserved.navigations).not.toHaveBeenCalled();
    expect(reserved.close).toHaveBeenCalledOnce();
  });
  it("reports Desktop open rejection as recoverable without leaking the native error", async () => {
    Object.defineProperty(window, "lazymindDesktop", { configurable: true, value: {
      openGitHubCLIAuthorization: vi.fn().mockRejectedValue(new Error("fixture-private-native-detail")),
    } });
    await expect(open(DEVICE_URL)).resolves.toEqual({ ok: false, reason: "blocked" });
  });
  it("does not report a blocked popup as success", async () => {
    vi.spyOn(window, "open").mockReturnValue(null);
    expect(await open(DEVICE_URL)).toEqual({ ok: false, reason: "blocked" });
  });
  it("keeps GitHub out of the existing Feishu and managed-Cloud navigators", async () => {
    expect(await navigation.openFeishuCLIAuthorization(DEVICE_URL, popup())).toEqual({ ok: false, reason: "invalid" });
    expect(await navigation.openManagedAuthorization(DEVICE_URL, popup())).toEqual({ ok: false, reason: "invalid" });
  });
});

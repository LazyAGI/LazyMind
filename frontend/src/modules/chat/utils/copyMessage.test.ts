import type { TFunction } from "i18next";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { copyMessage } from "./copyMessage";
import { CLIPBOARD_WRITE_TIMEOUT_MS } from "./clipboard";

const { success, error, info } = vi.hoisted(() => ({
  success: vi.fn(), error: vi.fn(), info: vi.fn(),
}));
vi.mock("antd", () => ({ message: { success, error }, Modal: { info } }));
const t = ((key: string) => key) as TFunction;
const originalClipboard = Object.getOwnPropertyDescriptor(navigator, "clipboard");

beforeEach(() => {
  vi.clearAllMocks();
  vi.spyOn(console, "warn").mockImplementation(() => undefined);
});
afterEach(() => {
  if (originalClipboard) Object.defineProperty(navigator, "clipboard", originalClipboard);
  else Reflect.deleteProperty(navigator, "clipboard");
  vi.useRealTimers();
  vi.restoreAllMocks();
});

function clipboard(writeText: (value: string) => Promise<void>) {
  Object.defineProperty(navigator, "clipboard", { configurable: true, value: { writeText } });
}

describe("message copy feedback without dialogs", () => {
  it("reports success only after the native write resolves", async () => {
    let complete!: () => void;
    clipboard(() => new Promise<void>((resolve) => { complete = resolve; }));
    const result = copyMessage("两人预算 2000 元", t);
    expect(success).not.toHaveBeenCalled();
    complete();
    await result;
    expect(success).toHaveBeenCalledOnce();
    expect(info).not.toHaveBeenCalled();
  });

  it("uses only a brief error message when the browser rejects copying", async () => {
    clipboard(vi.fn().mockRejectedValue(new DOMException("denied", "NotAllowedError")));
    await copyMessage("只比较天津和济南", t);
    expect(error).toHaveBeenCalledWith("chat.copyFailedRetry");
    expect(success).not.toHaveBeenCalled();
    expect(info).not.toHaveBeenCalled();
    expect(document.querySelector("textarea")).toBeNull();
  });

  it("reports a stalled write and suppresses a late success without opening a dialog", async () => {
    vi.useFakeTimers();
    let complete!: () => void;
    clipboard(() => new Promise<void>((resolve) => { complete = resolve; }));
    const result = copyMessage("原文", t);
    await vi.advanceTimersByTimeAsync(CLIPBOARD_WRITE_TIMEOUT_MS);
    await result;
    expect(error).toHaveBeenCalledOnce();
    complete();
    await Promise.resolve();
    expect(success).not.toHaveBeenCalled();
    expect(info).not.toHaveBeenCalled();
  });

  it("a new click can copy normally after a previous timed-out attempt", async () => {
    vi.useFakeTimers();
    const writeText = vi.fn().mockImplementationOnce(() => new Promise<void>(() => {}))
      .mockResolvedValue(undefined);
    clipboard(writeText);
    const first = copyMessage("原文", t);
    await vi.advanceTimersByTimeAsync(CLIPBOARD_WRITE_TIMEOUT_MS);
    await first;
    await copyMessage("原文", t);
    expect(success).toHaveBeenCalledOnce();
    expect(info).not.toHaveBeenCalled();
  });
});

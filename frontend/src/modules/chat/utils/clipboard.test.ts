import { afterEach, describe, expect, it, vi } from "vitest";
import { CLIPBOARD_WRITE_TIMEOUT_MS, copyTextToClipboard } from "./clipboard";

const originalClipboardDescriptor = Object.getOwnPropertyDescriptor(
  navigator,
  "clipboard",
);
const originalExecCommandDescriptor = Object.getOwnPropertyDescriptor(
  document,
  "execCommand",
);

function setClipboard(writeText?: (text: string) => Promise<void>) {
  Object.defineProperty(navigator, "clipboard", {
    configurable: true,
    value: writeText ? { writeText } : undefined,
  });
}

function setExecCommand(execCommand?: (command: string) => boolean) {
  Object.defineProperty(document, "execCommand", {
    configurable: true,
    value: execCommand,
  });
}

afterEach(() => {
  if (originalClipboardDescriptor) {
    Object.defineProperty(navigator, "clipboard", originalClipboardDescriptor);
  } else {
    delete (navigator as Navigator & { clipboard?: Clipboard }).clipboard;
  }

  if (originalExecCommandDescriptor) {
    Object.defineProperty(
      document,
      "execCommand",
      originalExecCommandDescriptor,
    );
  } else {
    delete (document as Document & { execCommand?: Document["execCommand"] })
      .execCommand;
  }

  document.querySelectorAll("textarea").forEach((element) => element.remove());
  vi.restoreAllMocks();
  vi.useRealTimers();
});

describe("copyTextToClipboard", () => {
  it("uses the Clipboard API only after the synchronous copy reports failure", async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    const execCommand = vi.fn().mockReturnValue(false);
    setClipboard(writeText);
    setExecCommand(execCommand);

    await expect(copyTextToClipboard("  copied text  ")).resolves.toBe(true);

    expect(writeText).toHaveBeenCalledWith("  copied text  ");
    expect(execCommand).toHaveBeenCalledOnce();
    expect(execCommand.mock.invocationCallOrder[0]).toBeLessThan(writeText.mock.invocationCallOrder[0]);
  });

  it("copies immediately even when the exposed async bridge would never settle", async () => {
    const writeText = vi.fn(() => new Promise<void>(() => {}));
    setClipboard(writeText);
    const execCommand = vi.fn().mockReturnValue(true);
    setExecCommand(execCommand);
    const copied = copyTextToClipboard("text");
    expect(execCommand).toHaveBeenCalledOnce();
    await expect(copied).resolves.toBe(true);
    expect(writeText).not.toHaveBeenCalled();
  });

  it("uses compatibility copying synchronously when the native API is absent and restores focus", async () => {
    const execCommand = vi.fn().mockReturnValue(true);
    const setSelectionRange = vi.spyOn(
      HTMLTextAreaElement.prototype,
      "setSelectionRange",
    );
    setClipboard();
    setExecCommand(execCommand);

    const input = document.createElement("textarea");
    document.body.appendChild(input);
    input.focus();
    const copied = copyTextToClipboard("fallback text");
    expect(execCommand).toHaveBeenCalledWith("copy");
    expect(document.activeElement).toBe(input);
    input.remove();
    await expect(copied).resolves.toBe(true);

    expect(execCommand).toHaveBeenCalledWith("copy");
    expect(setSelectionRange).toHaveBeenCalledWith(0, 13);
    expect(document.querySelector("textarea")).toBeNull();
  });

  it("throws and cleans up when neither copy path succeeds", async () => {
    setClipboard();
    setExecCommand(vi.fn().mockReturnValue(false));

    await expect(copyTextToClipboard("not copied")).rejects.toThrow(
      "Clipboard copy failed",
    );
    expect(document.querySelector("textarea")).toBeNull();
  });

  it("uses the synchronous legacy path when the modern API is unavailable", async () => {
    setClipboard();
    const execCommand = vi.fn().mockReturnValue(true);
    setExecCommand(execCommand);
    const copied = copyTextToClipboard("text");
    expect(execCommand).toHaveBeenCalledWith("copy");
    await expect(copied).resolves.toBe(true);
  });

  it("does nothing for blank text", async () => {
    const writeText = vi.fn();
    const execCommand = vi.fn();
    setClipboard(writeText);
    setExecCommand(execCommand);

    await expect(copyTextToClipboard("   ")).resolves.toBe(false);

    expect(writeText).not.toHaveBeenCalled();
    expect(execCommand).not.toHaveBeenCalled();
  });

  it("bounds a never-settling write without starting another clipboard write", async () => {
    vi.useFakeTimers();
    const writeText = vi.fn(() => new Promise<void>(() => {}));
    const execCommand = vi.fn();
    setClipboard(writeText);
    setExecCommand(execCommand);
    const copied = copyTextToClipboard("text");
    const rejected = expect(copied).rejects.toMatchObject({ name: "TimeoutError" });
    expect(writeText).toHaveBeenCalledOnce();
    await vi.advanceTimersByTimeAsync(CLIPBOARD_WRITE_TIMEOUT_MS);
    await rejected;
    expect(execCommand).toHaveBeenCalledOnce();
    expect(vi.getTimerCount()).toBe(0);
  });

  it("cleans up the timeout when writing succeeds promptly", async () => {
    vi.useFakeTimers();
    setClipboard(vi.fn().mockResolvedValue(undefined));
    await expect(copyTextToClipboard("text")).resolves.toBe(true);
    expect(vi.getTimerCount()).toBe(0);
  });

  it("starts the async fallback in the same click stack if the command throws", async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    setClipboard(writeText);
    setExecCommand(() => { throw new Error("unsupported"); });
    const copied = copyTextToClipboard("text");
    expect(writeText).toHaveBeenCalledWith("text");
    expect(document.querySelector("textarea")).toBeNull();
    await expect(copied).resolves.toBe(true);
  });
});

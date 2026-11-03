export const CLIPBOARD_WRITE_TIMEOUT_MS = 2000;

export async function copyTextToClipboard(text: string): Promise<boolean> {
  if (!text.trim()) {
    return false;
  }

  // Copy while the click's user activation is still live. Some embedded
  // browsers expose writeText before their async clipboard bridge is ready;
  // API presence alone is not a reason to wait on that bridge first.
  if (copySelectionSynchronously(text)) return true;

  // Only start an async write if the synchronous command failed. Never race
  // two writes: a delayed native write could overwrite a later copied message.
  if (
    typeof navigator !== "undefined" &&
    typeof navigator.clipboard?.writeText === "function"
  ) {
    // Start in the click handler, before any await, to retain user activation.
    const write = navigator.clipboard.writeText(text);
    let timer: ReturnType<typeof setTimeout> | undefined;
    try {
      await Promise.race([
        write,
        new Promise<never>((_, reject) => {
          timer = setTimeout(() => {
            reject(new DOMException("Clipboard write did not complete", "TimeoutError"));
          }, CLIPBOARD_WRITE_TIMEOUT_MS);
        }),
      ]);
    } finally {
      clearTimeout(timer);
    }
    return true;
  }
  throw new Error("Clipboard copy failed");
}

function copySelectionSynchronously(text: string): boolean {
  let legacyCopied = false;
  if (typeof document !== "undefined" && document.body && typeof document.execCommand === "function") {
    const textarea = document.createElement("textarea");
    const previousFocus = document.activeElement;
    const selection = document.getSelection();
    const ranges = selection
      ? Array.from({ length: selection.rangeCount }, (_, index) => selection.getRangeAt(index).cloneRange())
      : [];
    textarea.value = text;
    textarea.setAttribute("readonly", "");
    textarea.style.position = "fixed";
    textarea.style.left = "0";
    textarea.style.top = "0";
    textarea.style.width = "1px";
    textarea.style.height = "1px";
    textarea.style.opacity = "0";
    textarea.style.pointerEvents = "none";

    document.body.appendChild(textarea);
    try {
      textarea.focus({ preventScroll: true });
      textarea.select();
      textarea.setSelectionRange(0, text.length);
      legacyCopied = document.execCommand("copy");
    } catch {
      legacyCopied = false;
    } finally {
      textarea.remove();
      if (previousFocus instanceof HTMLElement && previousFocus.isConnected) {
        previousFocus.focus({ preventScroll: true });
      }
      if (selection) {
        selection.removeAllRanges();
        ranges.forEach((range) => selection.addRange(range));
      }
    }
  }

  return legacyCopied;
}

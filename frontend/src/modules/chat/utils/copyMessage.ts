import { message } from "antd";
import type { TFunction } from "i18next";
import { copyTextToClipboard } from "./clipboard";

/** All conversation copy buttons share the same real-write and recovery path. */
export async function copyMessage(text: string, t: TFunction): Promise<void> {
  if (!text.trim()) return;
  try {
    if (await copyTextToClipboard(text)) message.success(t("chat.copySuccess"));
  } catch (error) {
    // No message content is logged. These distinguish browser policy/focus
    // failures from a stale deployment or a missing API without reading clipboard.
    console.warn("Message clipboard write failed", {
      error: error instanceof Error ? error.name : "UnknownError",
      focused: document.hasFocus(),
      secureContext: window.isSecureContext,
      userActive: navigator.userActivation?.isActive,
    });
    message.error(t("chat.copyFailedRetry"));
  }
}

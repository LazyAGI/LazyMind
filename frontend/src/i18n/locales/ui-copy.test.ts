import { createInstance } from "i18next";
import { describe, expect, it } from "vitest";
import enUS from "./en-US";
import zhCN from "./zh-CN";

const keys = [
  "common.confirm",
  "agentIntegration.executablePathPlaceholderMacCodexDesktop",
  "agentIntegration.executablePathPlaceholderMacCLI",
  "agentIntegration.executablePathPlaceholderMacDesktop",
  "agentIntegration.executablePathPlaceholderWindowsCLI",
  "agentIntegration.executablePathPlaceholderWindowsDesktop",
  "chat.mentionWorkflowDisabled",
  "pdfReader.returnPosition",
];

describe.each(["zh-CN", "en-US"])("UI translation coverage in %s", (lng) => {
  it.each(keys)("resolves %s without fallback or a raw key", async (key) => {
    const instance = createInstance();
    await instance.init({
      lng,
      fallbackLng: false,
      resources: { "zh-CN": { translation: zhCN }, "en-US": { translation: enUS } },
    });
    expect(instance.exists(key)).toBe(true);
    const text = instance.t(key);
    expect(text.trim()).not.toBe("");
    expect(text).not.toBe(key);
    if (lng === "en-US") expect(text).not.toMatch(/[\u4e00-\u9fff]/);
  });
});

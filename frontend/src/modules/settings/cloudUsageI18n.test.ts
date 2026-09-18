import { describe, expect, it } from "vitest";

import enUS from "@/i18n/locales/en-US";
import zhCN from "@/i18n/locales/zh-CN";

describe("Cloud usage settings localization", () => {
  it.each([
    ["zh-CN", zhCN],
    ["en-US", enUS],
  ])("provides complete %s copy for every user-visible state", (_locale, messages) => {
    const cloudUsage = (messages.settingsPage as Record<string, unknown>).cloudUsage as Record<string, unknown> | undefined;
    expect(cloudUsage).toBeDefined();
    for (const key of [
      "title", "description", "model", "quota", "used", "remaining",
      "missingUsage", "signedOutTitle", "signedOutDescription", "signIn", "inactiveTitle",
      "inactiveDescription", "loadErrorTitle", "loadErrorDescription",
    ]) {
      expect(cloudUsage?.[key], key).toEqual(expect.any(String));
      expect(String(cloudUsage?.[key] || ""), key).not.toContain("settingsPage.");
    }
  });
});

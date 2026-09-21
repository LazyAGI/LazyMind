import { describe, expect, it } from "vitest";

import enUS from "@/i18n/locales/en-US";
import zhCN from "@/i18n/locales/zh-CN";

function valueAt(root: unknown, path: string): unknown {
  return path.split(".").reduce<unknown>((value, key) => {
    if (!value || typeof value !== "object") return undefined;
    return (value as Record<string, unknown>)[key];
  }, root);
}

describe("LazyMind Cloud system Provider translations", () => {
  it.each([
    ["zh-CN", zhCN],
    ["en-US", enUS],
  ])("defines complete model catalog states for %s", (_locale, messages) => {
    for (const path of [
      "modelProvider.cloudSystemBadge",
      "modelProvider.cloudSystemReadOnly",
      "modelProvider.cloudSystemLogin",
      "modelProvider.cloudSystemOpenPlan",
      "modelProvider.cloudSystemUnavailable",
      "modelProvider.cloudSystemDeprecated",
      "modelProvider.cloudSystemRetired",
      "chat.modelSelectorCloud",
      "chat.modelSelectorCloudUnavailable",
    ]) {
      expect(valueAt(messages, path), `${_locale} ${path}`).toEqual(expect.any(String));
      expect(String(valueAt(messages, path)), `${_locale} ${path}`).not.toHaveLength(0);
    }
  });
});

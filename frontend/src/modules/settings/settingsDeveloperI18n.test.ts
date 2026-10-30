import { describe, expect, it } from "vitest";

import enUS from "@/i18n/locales/en-US";
import zhCN from "@/i18n/locales/zh-CN";

describe.each([["en-US", enUS], ["zh-CN", zhCN]] as const)("%s developer settings copy", (_language, locale) => {
  it.each([
    "description",
    "inactiveNotice",
    "optionsGroupAria",
    "enableDesc",
    "enableDescWithoutEvo",
    "performanceDesc",
    "sensitiveWordFilterDesc",
  ] as const)("provides non-empty %s copy", (key) => {
    const copy = locale.settingsPage.developer[key as keyof typeof locale.settingsPage.developer];
    expect(copy).toBeTypeOf("string");
    expect(copy?.trim().length).toBeGreaterThan(0);
  });
});

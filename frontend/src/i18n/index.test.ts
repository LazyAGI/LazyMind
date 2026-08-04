import { beforeEach, describe, expect, it } from "vitest";

describe("i18n bootstrap", () => {
  beforeEach(() => {
    localStorage.clear();
  });

  it("exposes the supported LANGUAGES list and default language", async () => {
    const { LANGUAGES, DEFAULT_LANGUAGE } = await import("./index");
    expect(LANGUAGES).toEqual([
      { value: "zh-CN", label: "中文" },
      { value: "en-US", label: "English" },
    ]);
    expect(DEFAULT_LANGUAGE).toBe("zh-CN");
  });

  it("initializes the i18next instance with both locale resources and translation namespace", async () => {
    const i18nModule = await import("./index");
    const i18n = i18nModule.default;

    expect(i18n.isInitialized).toBe(true);
    expect(i18n.hasResourceBundle("zh-CN", "translation")).toBe(true);
    expect(i18n.hasResourceBundle("en-US", "translation")).toBe(true);
  });

  it("resolves a translation key from the zh-CN resource bundle", async () => {
    const i18nModule = await import("./index");
    const i18n = i18nModule.default;

    expect(i18n.getResource("zh-CN", "translation", "common.save")).toBe("保存");
  });
});

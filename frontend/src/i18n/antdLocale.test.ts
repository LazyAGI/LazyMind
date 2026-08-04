import { describe, expect, it } from "vitest";
import enUS from "antd/locale/en_US";
import zhCN from "antd/locale/zh_CN";
import { getAntdLocale } from "./antdLocale";

describe("getAntdLocale", () => {
  it("returns the English antd locale for en-prefixed languages", () => {
    expect(getAntdLocale("en-US")).toBe(enUS);
    expect(getAntdLocale("EN")).toBe(enUS);
    expect(getAntdLocale("en-GB")).toBe(enUS);
  });

  it("defaults to zh-CN locale for other or missing languages", () => {
    expect(getAntdLocale("zh-CN")).toBe(zhCN);
    expect(getAntdLocale(undefined)).toBe(zhCN);
    expect(getAntdLocale("fr-FR")).toBe(zhCN);
  });
});

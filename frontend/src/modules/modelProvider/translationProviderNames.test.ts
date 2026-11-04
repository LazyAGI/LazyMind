import { describe, expect, it } from "vitest";
import { translationProviderDisplayName } from "./translationProviderNames";

describe("translation provider display names", () => {
  it("shows Chinese names for the eight current sources in Chinese locales", () => {
    expect([
      "Google Translate Web", "Bing Translate Web", "Sogou Translate Web", "DeepL Web",
      "Tencent TranSmart Web", "Baidu Translation", "Aliyun Translation", "Niutrans",
    ].map((name) => translationProviderDisplayName(name, "zh-CN"))).toEqual([
      "谷歌翻译（网页）", "必应翻译（网页）", "搜狗翻译（网页）", "DeepL 网页翻译",
      "腾讯翻译君（网页）", "百度翻译", "阿里云机器翻译", "小牛翻译",
    ]);
  });

  it("keeps original names in other locales and for unknown providers", () => {
    expect(translationProviderDisplayName("Baidu Translation", "en-US")).toBe("Baidu Translation");
    expect(translationProviderDisplayName("Custom Translator", "zh-TW")).toBe("Custom Translator");
  });
});

import { afterEach, expect, it } from "vitest";
import i18n from "@/i18n";
import { getLocalizedErrorMessage } from "./request";

afterEach(() => i18n.changeLanguage("zh-CN"));

it.each([
  [
    "zh-CN",
    "技能包下载失败。原下载源：下载超时 · ModelScope：安装包不存在 · Hugging Face：安装包校验不通过",
  ],
  [
    "en-US",
    "Skill package download failed. Original source: download timed out · ModelScope: package not found · Hugging Face: package failed verification",
  ],
])("reports all built-in Skill download failures in %s", async (language, expected) => {
  await i18n.changeLanguage(language);
  const payload = {
    code: 2003147,
    message: "upstream diagnostics",
    data: {
      source_failures: [
        { source: "origin", reason: "timeout" },
        { source: "modelscope", reason: "missing" },
        { source: "hugging_face", reason: "invalid_package" },
      ],
    },
  };
  expect(getLocalizedErrorMessage({ response: { status: 502, data: payload } })).toBe(expected);
});

it("uses the existing localized error when structured details are absent", async () => {
  await i18n.changeLanguage("zh-CN");
  expect(getLocalizedErrorMessage({ response: { status: 502, data: { code: 2003147 } } }))
    .toBe(i18n.t("errors.2003147"));
});

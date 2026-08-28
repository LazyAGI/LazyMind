import { describe, expect, it } from "vitest";
import { isOpenAIProvider, shouldRedirectCustomBaseUrlToOpenAI } from "./ModelProvidersPage";

const provider = (name: string, baseUrl: string) => ({ name, source: name, baseUrl });

describe("self-hosted model provider redirect", () => {
  it("keeps custom OpenAI-compatible endpoints under OpenAI", () => {
    const openAI = provider("OpenAI", "https://api.openai.com/v1/");
    expect(isOpenAIProvider(openAI)).toBe(true);
    expect(shouldRedirectCustomBaseUrlToOpenAI(openAI, openAI.baseUrl, "http://127.0.0.1:8000")).toBe(false);
  });

  it("redirects a changed custom endpoint from another provider", () => {
    const qwen = provider("Qwen", "https://dashscope.aliyuncs.com/");
    expect(shouldRedirectCustomBaseUrlToOpenAI(qwen, qwen.baseUrl, "http://127.0.0.1:8000")).toBe(true);
  });

  it("does not interrupt unchanged or official alternate endpoints", () => {
    const qwen = provider("Qwen", "https://dashscope.aliyuncs.com/");
    expect(shouldRedirectCustomBaseUrlToOpenAI(qwen, qwen.baseUrl, qwen.baseUrl)).toBe(false);

    const sensenova = provider("SenseNova", "https://api.sensenova.cn/compatible-mode/v1/");
    expect(
      shouldRedirectCustomBaseUrlToOpenAI(
        sensenova,
        sensenova.baseUrl,
        "https://token.sensenova.cn/v1/chat/completions/"
      )
    ).toBe(false);
  });
});

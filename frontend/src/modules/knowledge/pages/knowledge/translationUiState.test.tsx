import { act, renderHook, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const mocks = vi.hoisted(() => ({ status: vi.fn(), translate: vi.fn() }));
vi.mock("@/modules/knowledge/api/translation", () => ({
  getTranslationServiceStatus: mocks.status,
  translateSelectionText: mocks.translate,
}));

import { getTranslationProgressLabel, useDocumentTranslationService, usePdfSelectionTranslation } from "./translationUiState";

beforeEach(() => { vi.clearAllMocks(); });

describe("document translation service state", () => {
  it("refreshes the selected source when the dialog opens and disables unsupported document API mode", async () => {
    mocks.status
      .mockResolvedValueOnce({ configured: true, provider: "DeepL", document_supported: true })
      .mockResolvedValueOnce({ configured: true, provider: "MyMemory", document_supported: false });
    const { result } = renderHook(() => useDocumentTranslationService());
    await waitFor(() => expect(result.current.provider).toBe("DeepL"));

    act(() => result.current.openModal());

    await waitFor(() => expect(result.current.provider).toBe("MyMemory"));
    expect(mocks.status).toHaveBeenCalledTimes(2);
    expect(result.current.modalOpen).toBe(true);
    expect(result.current.documentSupported).toBe(false);
    expect(result.current.mode).toBe("llm");
  });

  it("marks the dialog status as refreshing until its latest provider response arrives", async () => {
    let resolveNext!: (value: { configured: boolean; provider: string; document_supported: boolean }) => void;
    mocks.status
      .mockResolvedValueOnce({ configured: true, provider: "DeepL", document_supported: true })
      .mockImplementationOnce(() => new Promise((resolve) => { resolveNext = resolve; }));
    const { result } = renderHook(() => useDocumentTranslationService());
    await waitFor(() => expect(result.current.provider).toBe("DeepL"));

    act(() => result.current.openModal());
    expect(result.current.refreshing).toBe(true);
    await act(async () => { resolveNext({ configured: true, provider: "MyMemory", document_supported: false }); });
    expect(result.current.refreshing).toBe(false);
    expect(result.current.documentSupported).toBe(false);
  });

  it("uses the running job's provider and mode for its progress label", () => {
    expect(getTranslationProgressLabel({ provider_type: "api", provider: "DeepL" }, false, "llm", "MyMemory")).toBe("DeepL");
    expect(getTranslationProgressLabel({ provider_type: "llm", provider: "LazyMind LLM" }, false, "api", "DeepL")).toBe("LazyMind 大模型");
    expect(getTranslationProgressLabel({ provider_type: "api" }, false, "llm", "MyMemory")).toBe("翻译 API");
    expect(getTranslationProgressLabel(undefined, true, "api", "MyMemory")).toBe("MyMemory");
  });
});

describe("PDF selection translation", () => {
  it("passes an explicit source to MyMemory and re-translates when the user changes language", async () => {
    const refreshStatus = vi.fn().mockResolvedValue({ configured: true, provider: "MyMemory", document_supported: false });
    mocks.translate
      .mockResolvedValueOnce({ translated_text: "你好", source: "en", target: "zh" })
      .mockResolvedValueOnce({ translated_text: "hello", source: "zh", target: "en" });
    const onError = vi.fn();
    const { result } = renderHook(() => usePdfSelectionTranslation(refreshStatus, onError));

    await act(async () => { await result.current.translate("hello"); });
    expect(result.current.canChooseSource).toBe(true);
    expect(result.current.sourceLanguage).toBe("en");
    expect(result.current.result).toBe("你好");
    expect(mocks.translate).toHaveBeenCalledWith("hello", "en");

    await act(async () => { await result.current.changeSourceLanguage("zh"); });
    expect(mocks.translate).toHaveBeenLastCalledWith("hello", "zh");
    expect(result.current.result).toBe("hello");
    expect(onError).not.toHaveBeenCalled();
  });

  it("keeps other providers on automatic source detection", async () => {
    const refreshStatus = vi.fn().mockResolvedValue({ configured: true, provider: "DeepL", document_supported: true });
    mocks.translate.mockResolvedValue({ translated_text: "你好", source: "en", target: "zh" });
    const { result } = renderHook(() => usePdfSelectionTranslation(refreshStatus, vi.fn()));

    await act(async () => { await result.current.translate("hello"); });

    expect(result.current.canChooseSource).toBe(false);
    expect(mocks.translate).toHaveBeenCalledWith("hello", undefined);
  });
});

import { act, renderHook, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const mocks = vi.hoisted(() => ({ status: vi.fn(), translate: vi.fn() }));
vi.mock("@/modules/knowledge/api/translation", () => ({
  getTranslationServiceStatus: mocks.status,
  translateSelectionText: mocks.translate,
}));

import { getTranslationProgressLabel, resolveDocumentTranslationProvider, useDocumentTranslationService, usePdfSelectionTranslation } from "./translationUiState";

beforeEach(() => { vi.clearAllMocks(); });

describe("document translation service state", () => {
  it("uses only an explicitly chosen connection when regenerating an artifact", () => {
    const providers = [
      { group_id: "google", group_name: "Free web", provider_name: "Google Translate Web" },
      { group_id: "baidu", group_name: "Baidu account", provider_name: "Baidu Translation" },
    ];
    expect(resolveDocumentTranslationProvider(providers, "baidu")).toEqual(providers[1]);
    expect(resolveDocumentTranslationProvider(providers, undefined)).toBeUndefined();
    expect(resolveDocumentTranslationProvider(providers, "missing")).toBeUndefined();
  });

  it("offers another configured document API when the global default only supports short text", async () => {
    mocks.status.mockResolvedValue({
      configured: true, provider: "MyMemory", document_supported: false, selected_group_id: "memory",
      document_providers: [
        { group_id: "google", group_name: "Free web", provider_name: "Google Translate Web" },
        { group_id: "baidu", group_name: "Baidu account", provider_name: "Baidu Translation" },
      ],
    });
    const { result } = renderHook(() => useDocumentTranslationService());
    await waitFor(() => expect(result.current.providerGroupId).toBe("google"));
    expect(result.current.documentSupported).toBe(true);
    expect(result.current.mode).toBe("api");
    expect(result.current.documentProviders).toHaveLength(2);
    act(() => result.current.setProviderGroupId("baidu"));
    expect(result.current.provider).toBe("Baidu Translation");
  });

  it("drops a removed document provider selection after refreshing", async () => {
    mocks.status
      .mockResolvedValueOnce({ configured: true, provider: "Google Translate Web", document_supported: true, selected_group_id: "google",
        document_providers: [{ group_id: "google", group_name: "Free web", provider_name: "Google Translate Web" },
          { group_id: "baidu", group_name: "Baidu account", provider_name: "Baidu Translation" }] })
      .mockResolvedValueOnce({ configured: true, provider: "Google Translate Web", document_supported: true, selected_group_id: "google",
        document_providers: [{ group_id: "google", group_name: "Free web", provider_name: "Google Translate Web" }] });
    const { result } = renderHook(() => useDocumentTranslationService());
    await waitFor(() => expect(result.current.documentProviders).toHaveLength(2));
    act(() => result.current.setProviderGroupId("baidu"));
    await act(async () => { await result.current.refreshStatus(); });
    expect(result.current.providerGroupId).toBe("google");
  });
  it("refreshes the selected source when the dialog opens and disables unsupported document API mode", async () => {
    mocks.status
      .mockResolvedValueOnce({ configured: true, provider: "DeepL", document_supported: true, selected_group_id: "deepl",
        document_providers: [{ group_id: "deepl", provider_name: "DeepL", group_name: "DeepL" }] })
      .mockResolvedValueOnce({ configured: true, provider: "MyMemory", document_supported: false, document_providers: [] });
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
      .mockResolvedValueOnce({ configured: true, provider: "DeepL", document_supported: true, selected_group_id: "deepl",
        document_providers: [{ group_id: "deepl", provider_name: "DeepL", group_name: "DeepL" }] })
      .mockImplementationOnce(() => new Promise((resolve) => { resolveNext = resolve; }));
    const { result } = renderHook(() => useDocumentTranslationService());
    await waitFor(() => expect(result.current.provider).toBe("DeepL"));

    act(() => result.current.openModal());
    expect(result.current.refreshing).toBe(true);
    await act(async () => { resolveNext({ configured: true, provider: "MyMemory", document_supported: false, document_providers: [] }); });
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

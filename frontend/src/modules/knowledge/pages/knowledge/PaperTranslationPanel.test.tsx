import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import PaperTranslationPanel from "./PaperTranslationPanel";

afterEach(cleanup);

describe("paper translation panel", () => {
  it("submits typed text and clears the composer while showing one source and result", () => {
    const onTranslate = vi.fn();
    const props = { source: "old source", result: "old result", loading: false, providers: [], providerSaving: false, providersLoading: false, onProviderChange: vi.fn(), onTranslate, onRetranslateWithLLM: vi.fn() };
    const { rerender } = render(<PaperTranslationPanel {...props} />);
    const composer = screen.getByRole("textbox", { name: "输入要翻译的文字" });
    fireEvent.change(composer, { target: { value: "new source" } });
    fireEvent.keyDown(composer, { key: "Enter" });
    expect(onTranslate).toHaveBeenCalledWith("new source");
    expect(composer).toHaveValue("");
    rerender(<PaperTranslationPanel {...props} source="new source" result="new result" />);
    expect(screen.getByRole("region", { name: "翻译原文" })).toHaveTextContent("new source");
    expect(screen.getByRole("region", { name: "翻译结果" })).toHaveTextContent("new result");
    expect(screen.queryByText("old result")).not.toBeInTheDocument();
    const result = screen.getByRole("region", { name: "翻译结果" });
    const source = screen.getByRole("region", { name: "翻译原文" });
    expect(result.compareDocumentPosition(source) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(composer.closest("form")).toContainElement(screen.getByRole("button", { name: /选择翻译引擎/ }));
  });

  it("opens an anchored menu with the selected engine instead of a dialog", async () => {
    render(<PaperTranslationPanel source="" result="" loading={false}
      providers={[{ group_id: "google", provider_name: "Google Translate Web", group_name: "Google Translate Web" }]}
      providerGroupId="google" providerSaving={false} providersLoading={false}
      onProviderChange={vi.fn()} onTranslate={vi.fn()} onRetranslateWithLLM={vi.fn()} />);
    fireEvent.click(screen.getByRole("button", { name: /选择翻译引擎/ }));
    const menu = await screen.findByRole("menu");
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(within(menu).getByRole("menuitem", { name: /谷歌翻译（网页）/ })).toHaveClass("ant-dropdown-menu-item-selected");
    expect(within(menu).getByRole("link", { name: "管理翻译服务和密钥" })).toHaveAttribute("href", "/settings?section=model_defaults&service=translation");
  });

  it("changes the translation engine directly from the menu", async () => {
    const onProviderChange = vi.fn();
    render(<PaperTranslationPanel source="" result="" loading={false}
      providers={[
        { group_id: "google", provider_name: "Google Translate Web", group_name: "Google Translate Web" },
        { group_id: "tencent", provider_name: "Tencent Transmart Web", group_name: "Tencent Transmart Web" },
      ]}
      providerGroupId="google" providerSaving={false} providersLoading={false}
      onProviderChange={onProviderChange} onTranslate={vi.fn()} onRetranslateWithLLM={vi.fn()} />);
    fireEvent.click(screen.getByRole("button", { name: /选择翻译引擎/ }));
    fireEvent.click(within(await screen.findByRole("menu")).getByRole("menuitem", { name: /腾讯翻译君（网页）/ }));
    expect(onProviderChange).toHaveBeenCalledTimes(1);
    expect(onProviderChange).toHaveBeenCalledWith("tencent");
    await waitFor(() => expect(screen.getByRole("button", { name: /选择翻译引擎/ })).toHaveAttribute("aria-expanded", "false"));
  });

  it("keeps service configuration available when no engine is configured", async () => {
    render(<PaperTranslationPanel source="" result="" loading={false}
      providers={[]} providerSaving={false} providersLoading={false}
      onProviderChange={vi.fn()} onTranslate={vi.fn()} onRetranslateWithLLM={vi.fn()} />);
    fireEvent.click(screen.getByRole("button", { name: /选择翻译引擎/ }));
    const menu = await screen.findByRole("menu");
    expect(within(menu).getByText("暂无可用翻译引擎")).toBeInTheDocument();
    expect(within(menu).getByRole("link", { name: "管理翻译服务和密钥" })).toBeInTheDocument();
  });

  it("distinguishes connections and lets users search a longer engine list", async () => {
    const providers = Array.from({ length: 9 }, (_, index) => ({
      group_id: `google-${index + 1}`,
      provider_name: "Google Translate Web",
      group_name: `连接 ${index + 1}`,
    }));
    render(<PaperTranslationPanel source="" result="" loading={false}
      providers={providers} providerGroupId="google-1" providerSaving={false} providersLoading={false}
      onProviderChange={vi.fn()} onTranslate={vi.fn()} onRetranslateWithLLM={vi.fn()} />);
    expect(screen.getByRole("button", { name: /选择翻译引擎/ })).toHaveAttribute("title", "谷歌翻译（网页） · 连接 1");
    fireEvent.click(screen.getByRole("button", { name: /选择翻译引擎/ }));
    await act(async () => {
      fireEvent.change(screen.getByRole("searchbox", { name: "搜索翻译引擎" }), { target: { value: "连接 9" } });
    });
    const menu = screen.getByRole("menu");
    expect(within(menu).getByRole("menuitem", { name: /连接 9/ })).toBeInTheDocument();
    expect(within(menu).queryByRole("menuitem", { name: /连接 1/ })).not.toBeInTheDocument();
    expect(within(menu).getByRole("link", { name: "管理翻译服务和密钥" })).toBeInTheDocument();
  });

  it("retranslates the current source with an LLM from the result header", () => {
    const onRetranslateWithLLM = vi.fn();
    const { rerender } = render(<PaperTranslationPanel source="paper sentence" result="旧译文" loading={false}
      providers={[]} providerSaving={false} providersLoading={false}
      onProviderChange={vi.fn()} onTranslate={vi.fn()} onRetranslateWithLLM={onRetranslateWithLLM} />);
    const heading = screen.getByRole("heading", { name: "译文" }).parentElement;
    expect(heading).toContainElement(screen.getByRole("button", { name: "用大模型重新翻译" }));
    fireEvent.click(screen.getByRole("button", { name: "用大模型重新翻译" }));
    expect(onRetranslateWithLLM).toHaveBeenCalledTimes(1);
    rerender(<PaperTranslationPanel source="" result="" loading={false}
      providers={[]} providerSaving={false} providersLoading={false}
      onProviderChange={vi.fn()} onTranslate={vi.fn()} onRetranslateWithLLM={onRetranslateWithLLM} />);
    expect(screen.getByRole("button", { name: "用大模型重新翻译" })).toBeDisabled();
  });
});

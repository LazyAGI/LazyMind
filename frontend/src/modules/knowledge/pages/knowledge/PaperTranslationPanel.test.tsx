import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import PaperTranslationPanel from "./PaperTranslationPanel";

afterEach(cleanup);

describe("paper translation panel", () => {
  it("submits typed text and clears the composer while showing one source and result", () => {
    const onTranslate = vi.fn();
    const props = { source: "old source", result: "old result", loading: false, providers: [], providerSaving: false, providersLoading: false, onProviderChange: vi.fn(), onTranslate };
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
  });

  it("shows the Chinese provider name in the reader's translation settings", () => {
    render(<PaperTranslationPanel source="" result="" loading={false}
      providers={[{ group_id: "google", provider_name: "Google Translate Web", group_name: "Google Translate Web" }]}
      providerGroupId="google" providerSaving={false} providersLoading={false}
      onProviderChange={vi.fn()} onTranslate={vi.fn()} />);
    fireEvent.click(screen.getByRole("button", { name: "设置翻译引擎" }));
    expect(screen.getByText("谷歌翻译（网页）")).toBeInTheDocument();
  });
});

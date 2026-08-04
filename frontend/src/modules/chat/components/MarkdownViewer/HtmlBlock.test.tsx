import { describe, expect, it, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import HtmlBlock from "./HtmlBlock";

const { mockDownloadStream } = vi.hoisted(() => ({
  mockDownloadStream: vi.fn(),
}));

vi.mock("react-i18next", () => ({
  useTranslation: () => ({ t: (key: string) => key }),
}));

vi.mock("@/modules/chat/utils/download", () => ({
  downloadStream: mockDownloadStream,
}));

describe("HtmlBlock", () => {
  it("renders a preview iframe when the code contains renderable HTML", () => {
    render(<HtmlBlock code="<div>hello</div>" />);
    expect(document.querySelector(".md-html-preview-iframe")).toBeInTheDocument();
  });

  it("shows the preview-unavailable state for empty code", () => {
    render(<HtmlBlock code="" />);
    expect(screen.getByText("chat.markdownHtmlPreviewUnavailable")).toBeInTheDocument();
  });

  it("shows a streaming placeholder instead of the preview while streaming", () => {
    render(<HtmlBlock code="<div>hello</div>" isStreaming />);
    expect(screen.getByText("chat.markdownHtmlGenerating")).toBeInTheDocument();
    expect(document.querySelector(".md-html-preview-iframe")).not.toBeInTheDocument();
  });

  it("switches to the source view and shows highlighted code", () => {
    render(<HtmlBlock code="<div>hello</div>" />);
    fireEvent.click(screen.getByRole("button", { name: "chat.markdownSource" }));
    expect(document.querySelector(".md-code-source")).toBeInTheDocument();
  });

  it("disables the download button while streaming and enables it once done", () => {
    const { rerender } = render(<HtmlBlock code="<div>hello</div>" isStreaming />);
    expect(
      screen.getByRole("button", { name: "chat.markdownHtmlDownload" }),
    ).toBeDisabled();

    rerender(<HtmlBlock code="<div>hello</div>" isStreaming={false} />);
    expect(
      screen.getByRole("button", { name: "chat.markdownHtmlDownload" }),
    ).not.toBeDisabled();
  });

  it("downloads the html source as a blob when the download button is clicked", () => {
    render(<HtmlBlock code="<div>hello</div>" />);
    fireEvent.click(screen.getByRole("button", { name: "chat.markdownHtmlDownload" }));
    expect(mockDownloadStream).toHaveBeenCalledTimes(1);
    const [blob, filename] = mockDownloadStream.mock.calls[0];
    expect(blob).toBeInstanceOf(Blob);
    expect(filename).toBe("preview.html");
  });

  it("uses the document title as the download filename when present", () => {
    render(<HtmlBlock code="<html><head><title>My Report</title></head><body>hi</body></html>" />);
    fireEvent.click(screen.getByRole("button", { name: "chat.markdownHtmlDownload" }));
    expect(mockDownloadStream).toHaveBeenCalledWith(expect.any(Blob), "My-Report.html");
  });
});

import { describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import MarkdownViewer from "./index";

vi.mock("react-i18next", () => ({
  useTranslation: () => ({ t: (key: string) => key }),
}));

vi.mock("@/modules/knowledge/utils/imageUrl", () => ({
  resolveCoreAssetUrl: (path?: string) => path || "",
  resolveMarkdownImageUrlAsync: async (url: string) => url,
}));

describe("MarkdownViewer", () => {
  it("renders basic markdown content", async () => {
    render(<MarkdownViewer>{"# Title"}</MarkdownViewer>);
    await waitFor(() => {
      expect(screen.getByRole("heading", { level: 1, name: "Title" })).toBeInTheDocument();
    });
  });

  it("opens regular links in a new tab", async () => {
    render(<MarkdownViewer>{"[link](https://example.com)"}</MarkdownViewer>);
    await waitFor(() => {
      expect(screen.getByRole("link", { name: "link" })).toHaveAttribute("target", "_blank");
    });
  });

  it("wraps bare URLs into clickable links", async () => {
    render(<MarkdownViewer>{"See https://example.com for details"}</MarkdownViewer>);
    await waitFor(() => {
      expect(screen.getByRole("link", { name: "https://example.com" })).toBeInTheDocument();
    });
  });

  it("renders a source citation marker as plain text while streaming", async () => {
    render(
      <MarkdownViewer IS_STREAMING sources={[{ index: 1, content: "source body" }]}>
        {"See [1](#source-1) for more"}
      </MarkdownViewer>,
    );
    await waitFor(() => {
      expect(document.querySelector(".md-segment-index")).toBeInTheDocument();
    });
    // While streaming the citation should not become a Popover-triggering link.
    expect(screen.queryByRole("link")).not.toBeInTheDocument();
  });

  it("strips script tags found in raw HTML content instead of executing/rendering them", async () => {
    render(<MarkdownViewer>{"<script>alert(1)</script>text"}</MarkdownViewer>);
    await waitFor(() => {
      expect(screen.getByText("text")).toBeInTheDocument();
    });
    expect(document.querySelector("script")).not.toBeInTheDocument();
    expect(screen.queryByText(/alert\(1\)/)).not.toBeInTheDocument();
  });

  it("renders a mermaid code block via MermaidBlock instead of a plain <pre>", async () => {
    render(<MarkdownViewer>{"```mermaid\ngraph TD; A-->B;\n```"}</MarkdownViewer>);
    await waitFor(() => {
      expect(document.querySelector(".md-mermaid-block")).toBeInTheDocument();
    });
  });

  it("renders an html code block via HtmlBlock instead of a plain <pre>", async () => {
    render(<MarkdownViewer>{"```html\n<div>hi</div>\n```"}</MarkdownViewer>);
    await waitFor(() => {
      expect(document.querySelector(".md-html-block")).toBeInTheDocument();
    });
  });

  it("merges custom components with the built-in ones", async () => {
    render(
      <MarkdownViewer components={{ strong: () => <b data-testid="custom-strong">bold</b> }}>
        {"**bold text**"}
      </MarkdownViewer>,
    );
    await waitFor(() => {
      expect(screen.getByTestId("custom-strong")).toBeInTheDocument();
    });
  });

  it("applies the extra className to the wrapper", async () => {
    render(<MarkdownViewer className="custom-class">{"text"}</MarkdownViewer>);
    await waitFor(() => {
      expect(document.querySelector(".rag-markdown.custom-class")).toBeInTheDocument();
    });
  });
});

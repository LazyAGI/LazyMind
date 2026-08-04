import { describe, expect, it } from "vitest";
import { act, render, screen, waitFor } from "@testing-library/react";
import MarkdownViewer from "./index";

describe("MarkdownViewer (knowledge)", () => {
  it("shows a loading state briefly before rendering markdown", async () => {
    render(<MarkdownViewer>{"# Title"}</MarkdownViewer>);
    await waitFor(() => {
      expect(screen.getByRole("heading", { level: 1, name: "Title" })).toBeInTheDocument();
    });
  });

  it("renders GFM tables", async () => {
    const content = "| a | b |\n| - | - |\n| 1 | 2 |";
    render(<MarkdownViewer>{content}</MarkdownViewer>);
    await waitFor(() => {
      expect(screen.getByRole("table")).toBeInTheDocument();
    });
  });

  it("escapes raw HTML tags found within the markdown string content", async () => {
    render(<MarkdownViewer>{"<script>alert(1)</script>text"}</MarkdownViewer>);
    await waitFor(() => {
      expect(screen.getByText(/alert\(1\)/)).toBeInTheDocument();
    });
    expect(document.querySelector("script")).not.toBeInTheDocument();
  });

  it("opens links in a new tab", async () => {
    render(<MarkdownViewer>{"[link](https://example.com)"}</MarkdownViewer>);
    await waitFor(() => {
      expect(screen.getByRole("link", { name: "link" })).toHaveAttribute(
        "target",
        "_blank",
      );
    });
  });

  it("merges custom components passed via props with the built-in ones", async () => {
    render(
      <MarkdownViewer components={{ strong: () => <b data-testid="custom-strong">bold</b> }}>
        {"**bold text**"}
      </MarkdownViewer>,
    );
    await waitFor(() => {
      expect(screen.getByTestId("custom-strong")).toBeInTheDocument();
    });
  });

  it("renders non-string children (React nodes) directly without escaping", async () => {
    render(<MarkdownViewer>{null}</MarkdownViewer>);
    await waitFor(() => {
      expect(document.querySelector(".rag-markdown")).toBeInTheDocument();
    });
  });
});

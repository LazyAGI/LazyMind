import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import RenderMarkdown from "./render-markdown";

vi.mock("react-i18next", () => ({
  useTranslation: () => ({
    t: (key: string) => ({
      markdownRender: "Preview",
      markdownSource: "Source",
      "knowledge.markdownViewMode": "Markdown view mode",
    })[key] || key,
  }),
}));

vi.mock("@/modules/knowledge/components/MarkdownViewer", () => ({
  default: ({ children }: { children: string }) => (
    <div data-testid="markdown-preview">{children}</div>
  ),
}));

const markdown = "# Heading\n\n<script>alert('unsafe')</script>";
const fileData = new TextEncoder().encode(markdown).buffer;

describe("RenderMarkdown", () => {
  it("shows the rendered preview by default and lets the user view read-only source", () => {
    render(<RenderMarkdown fileData={fileData} />);

    expect(screen.getByTestId("markdown-preview")).toHaveTextContent("# Heading");

    fireEvent.click(screen.getByText("Source"));

    const source = document.querySelector(".file-viewer-markdown-source");
    expect(source).toHaveTextContent("# Heading");
    expect(source.tagName).toBe("PRE");
    expect(source).not.toHaveAttribute("contenteditable");
    expect(document.querySelector("script")).not.toBeInTheDocument();
  });
});

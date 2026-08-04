import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import MermaidBlock from "./MermaidBlock";

const mockRender = vi.fn();
const mockParse = vi.fn();
const mockInitialize = vi.fn();

vi.mock("react-i18next", () => ({
  useTranslation: () => ({ t: (key: string) => key }),
}));

vi.mock("mermaid", () => ({
  default: {
    initialize: mockInitialize,
    parse: mockParse,
    render: mockRender,
  },
}));

describe("MermaidBlock", () => {
  beforeEach(() => {
    mockRender.mockReset();
    mockParse.mockReset();
    mockInitialize.mockReset();
    mockParse.mockResolvedValue(undefined);
    mockRender.mockResolvedValue({ svg: "<svg><rect /></svg>" });
  });

  it("shows a rendering status while the diagram is being generated", () => {
    // Never resolves during this assertion window.
    mockRender.mockReturnValue(new Promise(() => {}));
    render(<MermaidBlock code="graph TD; A-->B;" />);
    expect(screen.getByText("chat.markdownDiagramRendering")).toBeInTheDocument();
  });

  it("renders the diagram svg once mermaid resolves", async () => {
    render(<MermaidBlock code="graph TD; A-->B;" />);
    await waitFor(() =>
      expect(document.querySelector(".md-mermaid-preview")).toBeInTheDocument(),
    );
    expect(document.querySelector(".md-mermaid-preview svg")).toBeInTheDocument();
  });

  it("shows an empty-code error state without calling mermaid", async () => {
    render(<MermaidBlock code="   " />);
    await waitFor(() =>
      expect(screen.getByText("chat.markdownDiagramRenderFailed")).toBeInTheDocument(),
    );
    expect(mockRender).not.toHaveBeenCalled();
  });

  it("falls back to the source view when rendering fails outside streaming", async () => {
    mockRender.mockRejectedValue(new Error("bad syntax"));
    // Use a code string not used by other tests: MermaidBlock caches successful
    // renders by code in a module-level Map, which would otherwise short-circuit
    // this render and skip calling mermaid.render entirely.
    render(<MermaidBlock code="graph TD; X-->Y-->Z;" />);

    await waitFor(() =>
      expect(screen.getByText("chat.markdownDiagramRenderFailed")).toBeInTheDocument(),
    );
    expect(screen.getByRole("tab", { name: "chat.markdownSource" })).toHaveAttribute(
      "aria-selected",
      "true",
    );
  });

  it("switches to source view and copies the code when the copy button is clicked", async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.assign(navigator, { clipboard: { writeText } });

    render(<MermaidBlock code="graph TD; A-->B;" />);
    await waitFor(() =>
      expect(document.querySelector(".md-mermaid-preview")).toBeInTheDocument(),
    );

    fireEvent.click(screen.getByRole("tab", { name: "chat.markdownSource" }));
    fireEvent.click(screen.getByRole("button", { name: "chat.markdownCopySource" }));

    await waitFor(() => expect(writeText).toHaveBeenCalledWith("graph TD; A-->B;"));
  });
});

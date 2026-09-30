import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import FileViewer from "./index";

vi.mock("react-i18next", () => ({
  useTranslation: () => ({ t: (key: string) => key }),
}));
vi.mock("@/components/auth", () => ({
  AgentAppsAuth: { getAuthHeaders: () => ({}) },
}));
vi.mock("@/components/request", () => ({ localizeErrorCode: (code: string) => code }));
vi.mock("@/modules/knowledge/utils/request", () => ({ normalizeProxyableUrl: (url: string) => url }));
vi.mock("@/components/ui", () => ({
  RenderPdf: () => null,
  exportPdfAsImagePdf: vi.fn(),
  isLearningActionCompatible: () => false,
}));
vi.mock("./renderers", () => {
  const Preview = () => <p>Use capabilities to describe provider behavior.</p>;
  return {
    RenderMarkdown: Preview,
    RenderTxt: Preview,
    RenderHtml: Preview,
    RenderWord: Preview,
    RenderExcel: Preview,
    RenderPpt: Preview,
  };
});

beforeEach(() => {
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue({
    ok: true,
    arrayBuffer: async () => new ArrayBuffer(1),
  }));
});
afterEach(() => {
  cleanup();
  window.getSelection()?.removeAllRanges();
  vi.unstubAllGlobals();
});

async function selectPreviewText() {
  const paragraph = await screen.findByText("Use capabilities to describe provider behavior.");
  const range = document.createRange();
  range.setStart(paragraph.firstChild!, 4);
  range.setEnd(paragraph.firstChild!, 16);
  window.getSelection()?.addRange(range);
  fireEvent.mouseUp(paragraph, { clientX: 200, clientY: 100 });
}

describe("document preview selection questions", () => {
  it.each(["md", "txt", "html", "docx", "xlsx", "pptx"])(
    "offers a %s selection question even when translation is not configured",
    async (extension) => {
      const onAsk = vi.fn();
      render(<FileViewer
        file={`https://example.test/source.${extension}`}
        fileName={`source.${extension}`}
        onPdfSelection={onAsk}
        onPdfTranslateSelection={vi.fn()}
        translationConfigured={false}
      />);
      await selectPreviewText();

      const askButton = screen.getByRole("button", { name: "knowledge.askPdfSelection" });
      expect(askButton).toBeEnabled();
      fireEvent.mouseDown(askButton);
      fireEvent.mouseUp(askButton);
      fireEvent.click(askButton);

      expect(onAsk).toHaveBeenCalledOnce();
      expect(onAsk).toHaveBeenCalledWith({
        text: "capabilities",
        context: "Use capabilities to describe provider behavior.",
        page: 1,
      });
      expect(window.getSelection()?.toString()).toBe("");
      expect(screen.queryByRole("button", { name: "knowledge.askPdfSelection" })).not.toBeInTheDocument();
    },
  );

  it("offers selection questions without requiring a translation callback", async () => {
    render(<FileViewer file="https://example.test/source.md" fileName="source.md" onPdfSelection={vi.fn()} />);
    await selectPreviewText();
    expect(screen.getByRole("button", { name: "knowledge.askPdfSelection" })).toBeEnabled();
    expect(screen.queryByRole("button", { name: "knowledge.translateSelection" })).not.toBeInTheDocument();
  });

  it("keeps the existing translation action when no question callback is supplied", async () => {
    const onTranslate = vi.fn();
    render(<FileViewer
      file="https://example.test/source.txt"
      fileName="source.txt"
      onPdfTranslateSelection={onTranslate}
      translationConfigured
    />);
    await selectPreviewText();
    expect(screen.queryByRole("button", { name: "knowledge.askPdfSelection" })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "knowledge.translateSelection" }));
    expect(onTranslate).toHaveBeenCalledOnce();
    expect(onTranslate).toHaveBeenCalledWith({ text: "capabilities", page: 1 });
  });

  it("does not offer a question for a selection extending outside the document preview", async () => {
    render(<><FileViewer file="https://example.test/source.md" fileName="source.md" onPdfSelection={vi.fn()} /><p>Outside preview</p></>);
    const paragraph = await screen.findByText("Use capabilities to describe provider behavior.");
    const outside = screen.getByText("Outside preview");
    const range = document.createRange();
    range.setStart(paragraph.firstChild!, 4);
    range.setEnd(outside.firstChild!, 7);
    window.getSelection()?.addRange(range);
    fireEvent.mouseUp(paragraph);
    expect(screen.queryByRole("button", { name: "knowledge.askPdfSelection" })).not.toBeInTheDocument();
  });
});

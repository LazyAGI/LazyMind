import { ConfigProvider } from "antd";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { guideDownloadDocument } from "./guideDownload";
import PaperGuideAsset from "./PaperGuideAsset";
import type { PaperGuideEvidence } from "@/api/generated/core-client";
it("restores the enlarged figure opener after Escape closes the guide modal", async () => {
  render(
    <ConfigProvider theme={{ token: { motion: false } }}>
      <PaperGuideAsset
        language="en"
        evidence={
          {
            id: "figure",
            text: "A table",
            source_block_id: "table",
            source_type: "table",
            locator: { kind: "bbox", page: 0 },
          } as PaperGuideEvidence
        }
        preview={async () => ({
          url: "data:image/png;base64,AA==",
          width: 100,
          height: 100,
          mode: "page",
        })}
      />
    </ConfigProvider>,
  );
  const opener = await screen.findByRole("button", {
    name: "Enlarge original figure",
  });
  opener.focus();
  fireEvent.click(opener);
  const dialog = await screen.findByRole("dialog");
  expect(dialog).toHaveStyle({ width: "fit-content" });
  const image = dialog.querySelector("img");
  expect(image).toHaveStyle({ maxWidth: "100%", maxHeight: "75dvh", height: "auto" });
  const close = screen.getByRole("button", { name: "Close" });
  close.focus();
  fireEvent.keyDown(dialog, { key: "Escape", code: "Escape", keyCode: 27 });
  await waitFor(() =>
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument(),
  );
  await waitFor(() => expect(opener).toHaveFocus());
});

it("labels the table start page and opens each physical evidence entry", async () => {
  const open = vi.fn();
  render(<PaperGuideAsset language="en" evidence={{ id: "parent", source_block_id: "table", source_type: "table", text: "Table 1", locator: { kind: "page", page: 2 }, fragment_evidence_ids: ["body-p3", "body-p4", "note-p4"] } as PaperGuideEvidence} preview={async () => undefined} onEvidence={open} />);
  expect(screen.getByText(/Table start page/)).toBeInTheDocument();
  const fragment = screen.getByRole("button", { name: "Table source fragment 2" });
  fireEvent.click(fragment);
  expect(open).toHaveBeenCalledWith("body-p4", fragment);
});

it("labels a rendered formula fallback as a source page, while retaining its authorized page action", async () => {
  const open = vi.fn();
  render(
    <PaperGuideAsset
      language="en"
      evidence={{ id: "formula", source_type: "formula", source_block_id: "eq",
        text: "x^2", locator: { kind: "page", page: 3 } }}
      originalDisplay={{ kind: "formula", page: 3, bbox: [.1,.2,.3,.4], number: "7" }}
      onOpenPage={open}
      preview={async () => ({ url: "blob:original-page", width: 100, height: 100, mode: "page" })}
    />,
  );
  expect(await screen.findByText(/Source page · PDF page 4/)).toBeInTheDocument();
  expect(screen.getByText(/No separate formula region/)).toBeInTheDocument();
  expect(screen.queryByText(/Equation \(7\)/)).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "Open PDF page 4" }));
  expect(open).toHaveBeenCalledWith(expect.objectContaining({ locator: { kind: "page", page: 3 } }));
});

it("keeps the formula crop and physical page action without labeling an unverified number", async () => {
  const open = vi.fn();
  const crop = "blob:formula-crop";
  render(
    <PaperGuideAsset
      language="en"
      evidence={{ id: "formula", source_type: "formula", source_block_id: "eq",
        text: "x^2", locator: { kind: "bbox", page: 3 } } as PaperGuideEvidence}
      originalDisplay={{ kind: "formula", page: 3, bbox: [.1,.2,.3,.4], number: "4" }}
      onOpenPage={open}
      preview={async () => ({ url: crop, width: 100, height: 100, mode: "region" })}
    />,
  );
  expect(await screen.findByRole("img", { name: /PDF page 4/ })).toHaveAttribute("src", crop);
  expect(screen.queryByText(/Equation \(4\)/)).not.toBeInTheDocument();
  expect(screen.getByText(/Original formula · PDF page 4/)).toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "Open PDF page 4" }));
  expect(open).toHaveBeenCalledWith(expect.objectContaining({ locator: { kind: "page", page: 3 } }));
});

it.each(["Figure 4: Our reparametrization.", "Fi ure 4: Our reparametrization."])("shows the numbered caption extracted from %s", async (caption) => {
  const view = render(<PaperGuideAsset compactCaption captionText="低秩重参数化。" language="zh" evidence={{ id: "figure", source_type: "image", source_block_id: "image", text: "Figure 1", locator: {kind: "page", page: 0} }} originalDisplay={{kind:"figure",page:0,caption}} preview={async()=>({url:"blob:figure",width:100,height:100,mode:"region"})} onOpenPage={vi.fn()} />);
  expect(view.container.querySelector('figcaption')).toHaveTextContent('论文图4：低秩重参数化。');
  expect(screen.queryByRole('button',{name:/打开 PDF/})).toBeNull();
});

it("offers a page preview on demand without embedding a whole page as a figure", async () => {
  const open = vi.fn();
  render(<PaperGuideAsset compactCaption captionText="结构图" language="zh"
    evidence={{ id: "figure", source_type: "image", source_block_id: "image", text: "", locator: {kind: "page", page: 7} }}
    originalDisplay={{kind: "figure", page: 7, caption: "Figure 3: Architecture."}}
    preview={async () => ({url: "blob:page", width: 612, height: 792, mode: "page"})} onOpenPage={open} />);
  const button = await screen.findByRole("button", { name: "查看原文第 8 页" });
  expect(screen.queryByRole("img")).not.toBeInTheDocument();
  expect(screen.getByText("论文图3：结构图").closest("figcaption")).toHaveTextContent(/^论文图3：结构图$/);
  fireEvent.click(button);
  expect(await screen.findByRole("dialog")).toBeInTheDocument();
  expect(screen.getByRole("img", { name: "PDF 第 8 页" })).toHaveAttribute("src", "blob:page");
  expect(open).not.toHaveBeenCalled();
});

it.each(["html", "pdf"] as const)("embeds fallback page pixels in %s", async format => {
  const url = "data:image/png;base64,AA==";
  const view = render(<article><section data-guide-role="method"><div data-guide-block="f"><PaperGuideAsset
    compactCaption language="en" evidence={{ id: "f", source_type: "image", source_block_id: "i", text: "", locator: { kind: "page", page: 1 } }}
    preview={async () => ({ url, width: 500, height: 700, mode: "page" })} /></div></section></article>);
  await screen.findByRole("button", { name: "View PDF page 2" });
  const result = await guideDownloadDocument(view.container.querySelector("article")!, "en", format);
  const doc = new DOMParser().parseFromString(result.html, "text/html");
  expect(doc.querySelector("figure img:not([hidden])")?.getAttribute("src")).toBe(url);
});

it("owns a mounted figure URL independently from cache eviction and releases it on unmount", async () => {
  const create = vi.fn(() => "blob:mounted");
  const revoke = vi.fn();
  vi.stubGlobal("URL", class extends URL {
    static createObjectURL = create;
    static revokeObjectURL = revoke;
  });
  try {
    const blob = new Blob(["pixels"], { type: "image/png" });
    const view = render(<PaperGuideAsset language="en"
      evidence={{ id: "f", source_type: "image", source_block_id: "i", text: "", locator: { kind: "page", page: 0 } }}
      preview={async () => ({ url: "blob:evicted", blob, width: 100, height: 100, mode: "region" })} />);
    const button = await screen.findByRole("button", { name: "Enlarge original figure" });
    expect(create).toHaveBeenCalledWith(blob);
    URL.revokeObjectURL("blob:evicted");
    fireEvent.click(button);
    const dialog = await screen.findByRole("dialog");
    expect(dialog.querySelector("img")?.getAttribute("src")).toBe("blob:mounted");
    view.unmount();
    expect(revoke).toHaveBeenCalledWith("blob:mounted");
  } finally {
    vi.unstubAllGlobals();
  }
});

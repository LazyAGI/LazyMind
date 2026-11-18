import { ConfigProvider } from "antd";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
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

it("shows only the numbered caption for an explainer original figure", async () => {
  const view = render(<PaperGuideAsset compactCaption captionText="低秩重参数化。" language="zh" evidence={{ id: "figure", source_type: "image", source_block_id: "image", text: "Figure 1", locator: {kind: "page", page: 0} }} originalDisplay={{kind:"figure",page:0,caption:"Figure 4: Our reparametrization."}} preview={async()=>({url:"blob:figure",width:100,height:100,mode:"region"})} onOpenPage={vi.fn()} />);
  expect(view.container.querySelector('figcaption')).toHaveTextContent('论文图4：低秩重参数化。');
  expect(screen.queryByRole('button',{name:/打开 PDF/})).toBeNull();
});

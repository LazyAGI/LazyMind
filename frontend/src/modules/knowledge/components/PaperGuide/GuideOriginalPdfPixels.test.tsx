import { Blob as NodeBlob } from "node:buffer";
import { createHash } from "node:crypto";
import { createRequire } from "node:module";
import { renderHook } from "@testing-library/react";
import { jsPDF } from "jspdf";
import { expect, it, vi } from "vitest";
import { useGuideSourcePreview } from "./useGuideSourcePreview";

vi.mock("react-pdf", async () => ({ pdfjs: await import("pdfjs-dist/legacy/build/pdf.mjs") }));

it("renders exact figure and equation pixels on their own pages of an authenticated PDF", async () => {
  const require = createRequire(import.meta.url);
  // PDF.js already supplies this native Node canvas dependency. Reuse it so
  // the test checks final PNG pixels rather than a mocked draw call or <img>.
  const { createCanvas, loadImage, DOMMatrix, ImageData, Path2D } =
    createRequire(require.resolve("pdfjs-dist/package.json"))("@napi-rs/canvas");
  vi.stubGlobal("DOMMatrix", DOMMatrix);
  vi.stubGlobal("ImageData", ImageData);
  vi.stubGlobal("Path2D", Path2D);

  const source = new jsPDF({ unit: "pt", format: [200, 200] });
  source.setFillColor(255, 0, 0);
  source.rect(50, 50, 40, 40, "F"); // same position on the wrong physical page
  source.addPage();
  source.setFillColor(0, 0, 255);
  source.rect(50, 50, 40, 40, "F"); // original chart
  source.setFillColor(0, 255, 0);
  source.rect(50, 94, 40, 6, "F"); // independent footnote, within prior padding
  source.setFillColor(255, 0, 255);
  source.rect(101, 50, 40, 40, "F"); // distinct adjacent chart
  source.addPage();
  source.setDrawColor(255, 255, 0);
  source.setLineWidth(3);
  // A vector x = y in the original PDF, separate from the figure pixels.
  source.line(40, 50, 53, 70);
  source.line(53, 50, 40, 70);
  source.line(61, 57, 77, 57);
  source.line(61, 63, 77, 63);
  source.line(85, 50, 93, 61);
  source.line(101, 50, 89, 73);
  const data = source.output("arraybuffer");
  const sha256 = createHash("sha256").update(new Uint8Array(data)).digest("hex");

  const canvases = new WeakMap<HTMLCanvasElement, ReturnType<typeof createCanvas>>();
  vi.spyOn(HTMLCanvasElement.prototype, "getContext").mockImplementation(function (this: HTMLCanvasElement) {
    let canvas = canvases.get(this);
    if (!canvas) {
      canvas = createCanvas(this.width, this.height);
      canvases.set(this, canvas);
    }
    const context = canvas.getContext("2d");
    const drawImage = context.drawImage.bind(context);
    context.drawImage = (image: HTMLCanvasElement, ...args: number[]) =>
      drawImage(canvases.get(image) || image, ...args);
    return context as CanvasRenderingContext2D;
  });
  vi.spyOn(HTMLCanvasElement.prototype, "toBlob").mockImplementation(function (this: HTMLCanvasElement, callback) {
    callback(new NodeBlob([canvases.get(this).toBuffer("image/png")]) as unknown as Blob);
  });
  const images = new Map<string, Blob>();
  vi.stubGlobal("URL", Object.assign(class extends URL {}, {
    createObjectURL(blob: Blob) {
      const url = "blob:synthetic-" + images.size;
      images.set(url, blob);
      return url;
    },
    revokeObjectURL(url: string) { images.delete(url); },
  }));
  const fileBytes = () => data.slice(0);
  const hook = renderHook(() =>
    useGuideSourcePreview("paper/revision/v17", sha256, true, sha256, fileBytes),
  );
  const evidence = {
    id: "f", source_block_id: "figure", source_type: "chart", text: "Figure synthetic blue marker",
    locator: { kind: "page", page: 1 },
  };
  const pixels = async (url: string, x: number, y: number) => {
    const bytes = Buffer.from(await images.get(url)!.arrayBuffer());
    const image = await loadImage(bytes);
    const canvas = createCanvas(image.width, image.height);
    const ctx = canvas.getContext("2d");
    ctx.drawImage(image, 0, 0);
    return [...ctx.getImageData(x, y, 1, 1).data];
  };
  try {
    const crop = await hook.result.current.preview(evidence, { kind: "figure", page: 1, bbox: [.25, .25, .45, .45] });
    expect(crop).toMatchObject({ mode: "region", width: 60, height: 60 });
    expect(await pixels(crop!.url, 30, 30)).toEqual([0, 0, 255, 255]);
    const full = await hook.result.current.preview(evidence, { kind: "figure", page: 1 });
    expect(full).toMatchObject({ mode: "page", width: 300, height: 300 });
    expect(await pixels(full!.url, 90, 90)).toEqual([0, 0, 255, 255]);
    expect(await pixels(full!.url, 90, 145)).toEqual([0, 255, 0, 255]);
    expect(await pixels(full!.url, 160, 90)).toEqual([255, 0, 255, 255]);
    const eq = await hook.result.current.preview({
      ...evidence, id: "eq", source_type: "formula", locator: { kind: "page", page: 2 },
    }, { kind: "formula", page: 2, bbox: [.19,.23,.53,.39] });
    expect(eq).toMatchObject({ mode: "region", width: 102, height: 48 });
    expect(await pixels(eq!.url, 43, 16)).toEqual([255, 255, 0, 255]);
  } finally {
    hook.unmount();
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  }
});

import { Blob as NodeBlob } from "node:buffer";
import { createHash } from "node:crypto";
import { createRequire } from "node:module";
import { fireEvent, render, renderHook, screen } from "@testing-library/react";
import { jsPDF } from "jspdf";
import { expect, it, vi } from "vitest";
import PaperGuideAsset from "./PaperGuideAsset";
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
  // Draw the printed (3) as PDF paths so rasterization is independent of system fonts.
  source.setDrawColor(0, 0, 0);
  source.setLineWidth(1);
  source.lines([[-2, 2], [0, 8], [2, 2]], 22, 53);
  source.lines([[5, 0], [0, 10], [-5, 0]], 24, 54);
  source.line(24, 59, 29, 59);
  source.lines([[2, 2], [0, 8], [-2, 2]], 31, 53);
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
    expect(crop).toMatchObject({ mode: "region", width: 120, height: 120 });
    expect(await pixels(crop!.url, 60, 60)).toEqual([0, 0, 255, 255]);
    const wide = await hook.result.current.preview(evidence, { kind: "figure", page: 1, bbox: [.02, .20, .49, .46] });
    expect(wide?.height).toBe(156);
    expect(wide!.width).toBeLessThan(132);
    expect(await pixels(wide!.url, 60, 60)).toEqual([0, 0, 255, 255]);
    // A top-of-page figure strip includes detached annotations beyond the body.
    // Here the blue body and magenta legend share a strip; the green caption is below it.
    const strip = await hook.result.current.preview(evidence, { kind: "figure", page: 1, bbox: [0, 0, 1, .46] });
    expect(strip?.mode).toBe("region");
    expect(strip!.width).toBeGreaterThan(269);
    expect(strip!.width).toBeLessThan(285);
    expect(strip!.height).toBeLessThan(132);
    expect(await pixels(strip!.url, 30, 30)).toEqual([0, 0, 255, 255]);
    expect(await pixels(strip!.url, 180, 30)).toEqual([255, 0, 255, 255]);
    const full = await hook.result.current.preview(evidence, { kind: "figure", page: 1 });
    expect(full).toMatchObject({ mode: "page", width: 600, height: 600 });
    expect(await pixels(full!.url, 180, 180)).toEqual([0, 0, 255, 255]);
    expect(await pixels(full!.url, 180, 290)).toEqual([0, 255, 0, 255]);
    expect(await pixels(full!.url, 320, 180)).toEqual([255, 0, 255, 255]);
    const equation = {
      ...evidence, id: "eq", source_type: "formula", text: "x = y", locator: { kind: "page" as const, page: 2 },
    };
    const originalDisplay = { kind: "formula" as const, page: 2, bbox: [.19,.23,.53,.39], number: "4" };
    const eq = await hook.result.current.preview(equation, originalDisplay);
    expect(eq).toMatchObject({ mode: "region", width: 204, height: 96 });
    expect(await pixels(eq!.url, 86, 32)).toEqual([255, 255, 0, 255]);
    expect(await pixels(eq!.url, 0, 32)).not.toEqual([0, 0, 0, 255]); // printed (3) is not in the crop

    const wholeEquationPage = await hook.result.current.preview(equation);
    expect(wholeEquationPage).toMatchObject({ mode: "page", width: 600, height: 600 });
    const pageBytes = Buffer.from(await images.get(wholeEquationPage!.url)!.arrayBuffer());
    const pageImage = await loadImage(pageBytes);
    const pageCanvas = createCanvas(pageImage.width, pageImage.height);
    const pageContext = pageCanvas.getContext("2d");
    pageContext.drawImage(pageImage, 0, 0);
    const numberPixels = pageContext.getImageData(48, 156, 90, 60).data;
    expect(Array.from(numberPixels as Uint8ClampedArray)
      .some((value, index) => index % 4 === 0 && value < 80)).toBe(true);

    const onOpenPage = vi.fn();
    const asset = render(
      <PaperGuideAsset
        language="en"
        evidence={equation}
        originalDisplay={originalDisplay}
        preview={hook.result.current.preview}
        onOpenPage={onOpenPage}
      />,
    );
    const equationImage = await screen.findByRole("img", { name: /PDF page 3/ });
    const displayedUrl = equationImage.getAttribute("src")!;
    expect(displayedUrl).not.toBe(eq!.url);
    expect(await pixels(displayedUrl, 86, 32)).toEqual([255, 255, 0, 255]);
    expect(screen.queryByText(/Equation \(4\)/)).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Open PDF page 3" }));
    expect(onOpenPage).toHaveBeenCalledWith(expect.objectContaining({
      source_type: "formula", locator: { kind: "page", page: 2 },
    }));
    asset.unmount();
    expect(images.has(displayedUrl)).toBe(false);
  } finally {
    hook.unmount();
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  }
});

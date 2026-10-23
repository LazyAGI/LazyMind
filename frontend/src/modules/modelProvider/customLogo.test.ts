import { describe, expect, it, vi } from "vitest";
import {
  CUSTOM_LOGO_MAX_FILE_BYTES,
  CUSTOM_LOGO_MAX_OUTPUT_BYTES,
  CustomLogoError,
  prepareCustomLogo,
  isCustomLogoDataUrl,
  validateCustomLogoFile,
  type CustomLogoDependencies,
} from "./customLogo";

const pngHeader = new Uint8Array([137, 80, 78, 71, 13, 10, 26, 10]);
const png = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVQIHWP4z8DwHwAFgAI/ScLbtAAAAABJRU5ErkJggg==";

function file(type = "image/png", content: BlobPart = pngHeader) {
  return new File([content], "chosen-image", { type });
}

function processor(width = 400, height = 200, dataUrl = png) {
  const source = {} as CanvasImageSource;
  const clearRect = vi.fn();
  const drawImage = vi.fn();
  const dispose = vi.fn();
  const toDataURL = vi.fn(() => dataUrl);
  const canvas = { width: 0, height: 0, getContext: () => ({ clearRect, drawImage }), toDataURL } as unknown as HTMLCanvasElement;
  const decode = vi.fn().mockResolvedValue({ width, height, source, dispose });
  const dependencies: CustomLogoDependencies = { decode, createCanvas: () => canvas };
  return { dependencies, decode, canvas, source, clearRect, drawImage, dispose, toDataURL };
}

async function errorCode(action: Promise<unknown>, code: string) {
  await expect(action).rejects.toMatchObject({ name: "CustomLogoError", code });
}

describe("custom logo file and output contract", () => {
  it("allows only bounded PNG data URLs as values and rejects external or malformed preview sources", () => {
    expect(isCustomLogoDataUrl(png)).toBe(true);
    for (const value of [undefined, null, "", "https://example.test/logo.png", "data:image/svg+xml;base64,PHN2Zy8+", "data:image/png;base64,YWJjZA=="])
      expect(isCustomLogoDataUrl(value)).toBe(false);
  });

  it("rejects base64 with nonzero unused padding bits rather than treating malformed preview values as PNG", () => {
    const noncanonical = "data:image/png;base64,iVBORw0KGgp=";
    expect(atob(noncanonical.split(",")[1])).toBe(String.fromCharCode(...pngHeader));
    expect(isCustomLogoDataUrl(noncanonical)).toBe(false);
  });
  it("rejects empty files, unsupported formats and files above the 2 MiB bound before decoding", async () => {
    const { dependencies, decode } = processor();
    await errorCode(prepareCustomLogo(file("image/png", ""), dependencies), "empty");
    await errorCode(prepareCustomLogo(file("image/svg+xml", "<svg/>"), dependencies), "type");
    await errorCode(prepareCustomLogo(file("image/png", new Uint8Array(CUSTOM_LOGO_MAX_FILE_BYTES + 1)), dependencies), "size");
    expect(decode).not.toHaveBeenCalled();
    expect(() => validateCustomLogoFile(file())).not.toThrow();
    expect(() => validateCustomLogoFile(file("image/gif", "GIF89a"))).toThrow(CustomLogoError);
  });

  it("accepts an exact 2 MiB image and checks actual signatures rather than trusting the filename or MIME", async () => {
    const body = new Uint8Array(CUSTOM_LOGO_MAX_FILE_BYTES);
    body.set(pngHeader);
    const { dependencies, decode } = processor();
    await expect(prepareCustomLogo(file("image/png", body), dependencies)).resolves.toBe(png);
    await errorCode(prepareCustomLogo(file("image/png", "<svg xmlns='http://www.w3.org/2000/svg'/>"), dependencies), "type");
    await errorCode(prepareCustomLogo(file("image/jpeg", pngHeader), dependencies), "type");
    expect(decode).toHaveBeenCalledTimes(1);
  });

  it.each([
    ["image/jpeg", new Uint8Array([255, 216, 255, 224, 0, 0, 0, 0, 0, 0, 0, 0])],
    ["image/webp", new Uint8Array([82, 73, 70, 70, 20, 0, 0, 0, 87, 69, 66, 80])],
  ])("accepts a declared %s only after its corresponding raster header is checked", async (type, header) => {
    const { dependencies } = processor();
    await expect(prepareCustomLogo(file(type, header), dependencies)).resolves.toBe(png);
  });

  it.each([
    [400, 200, 0, 32, 128, 64],
    [72, 288, 48, 0, 32, 128],
    [32, 32, 0, 0, 128, 128],
  ])("fits a %i by %i decoded image inside a transparent 128px square without cropping", async (width, height, x, y, drawnWidth, drawnHeight) => {
    const { dependencies, canvas, source, clearRect, drawImage, dispose, toDataURL } = processor(width, height);
    await expect(prepareCustomLogo(file(), dependencies)).resolves.toBe(png);
    expect(canvas.width).toBe(128);
    expect(canvas.height).toBe(128);
    expect(clearRect).toHaveBeenCalledWith(0, 0, 128, 128);
    expect(drawImage).toHaveBeenCalledWith(source, x, y, drawnWidth, drawnHeight);
    expect(toDataURL).toHaveBeenCalledWith("image/png");
    expect(dispose).toHaveBeenCalledOnce();
  });

  it("reports failed and zero-dimension decodes and always releases successfully decoded resources", async () => {
    const rejected = processor();
    rejected.decode.mockRejectedValue(new Error("browser decode failed"));
    await errorCode(prepareCustomLogo(file(), rejected.dependencies), "decode");
    const zero = processor(0, 10);
    await errorCode(prepareCustomLogo(file(), zero.dependencies), "decode");
    expect(zero.dispose).toHaveBeenCalledOnce();
  });

  it("enforces the decoded PNG output limit including an exact 96 KiB boundary", async () => {
    const makeOutput = (bytes: number) => `data:image/png;base64,${btoa(String.fromCharCode(...pngHeader) + "\0".repeat(bytes - pngHeader.length))}`;
    const exact = processor(200, 200, makeOutput(CUSTOM_LOGO_MAX_OUTPUT_BYTES));
    await expect(prepareCustomLogo(file(), exact.dependencies)).resolves.toBe(makeOutput(CUSTOM_LOGO_MAX_OUTPUT_BYTES));
    const over = processor(200, 200, makeOutput(CUSTOM_LOGO_MAX_OUTPUT_BYTES + 1));
    await errorCode(prepareCustomLogo(file(), over.dependencies), "output");
    expect(over.dispose).toHaveBeenCalledOnce();
  });

  it.each([
    "https://example.test/logo.png",
    "data:image/jpeg;base64,/9j/4AAQ",
    "data:image/png;base64,not-base64!",
    "data:image/png;base64,",
    "data:image/png;base64,YWJjZA==",
  ])("rejects malformed or non-PNG encoded output %s", async (output) => {
    const { dependencies, dispose } = processor(40, 40, output);
    await errorCode(prepareCustomLogo(file(), dependencies), "output");
    expect(dispose).toHaveBeenCalledOnce();
  });

  it("maps canvas encoding failure to the stable output error without leaking implementation messages", async () => {
    const { dependencies, toDataURL, dispose } = processor();
    toDataURL.mockImplementation(() => { throw new Error("private canvas detail"); });
    await errorCode(prepareCustomLogo(file(), dependencies), "output");
    expect(dispose).toHaveBeenCalledOnce();
  });
});

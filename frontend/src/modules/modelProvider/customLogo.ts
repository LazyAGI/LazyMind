export const CUSTOM_LOGO_MAX_FILE_BYTES = 2 * 1024 * 1024;
export const CUSTOM_LOGO_MAX_OUTPUT_BYTES = 96 * 1024;
export const CUSTOM_LOGO_SIZE = 128;

export type CustomLogoErrorCode = "empty" | "type" | "size" | "decode" | "output";

export class CustomLogoError extends Error {
  constructor(public readonly code: CustomLogoErrorCode) {
    super(code);
    this.name = "CustomLogoError";
  }
}

export interface CustomLogoDecodedImage {
  width: number;
  height: number;
  source: CanvasImageSource;
  dispose?: () => void;
}

export interface CustomLogoDependencies {
  decode: (file: File) => Promise<CustomLogoDecodedImage>;
  createCanvas: () => HTMLCanvasElement;
}

const pngSignature = [137, 80, 78, 71, 13, 10, 26, 10];
const acceptedTypes = new Set(["image/png", "image/jpeg", "image/webp"]);
const pngPrefix = "data:image/png;base64,";

export function validateCustomLogoFile(file: File): void {
  if (!file || file.size === 0) throw new CustomLogoError("empty");
  if (!acceptedTypes.has(file.type.trim().toLowerCase())) throw new CustomLogoError("type");
  if (!Number.isFinite(file.size) || file.size > CUSTOM_LOGO_MAX_FILE_BYTES) throw new CustomLogoError("size");
}

function hasBytes(bytes: Uint8Array, values: readonly number[], offset = 0) {
  return bytes.length >= offset + values.length && values.every((value, index) => bytes[offset + index] === value);
}

function readHeader(file: File): Promise<Uint8Array> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => reader.result instanceof ArrayBuffer
      ? resolve(new Uint8Array(reader.result)) : reject(new CustomLogoError("decode"));
    reader.onerror = reader.onabort = () => reject(new CustomLogoError("decode"));
    reader.readAsArrayBuffer(file.slice(0, 12));
  });
}

async function validateFileSignature(file: File) {
  const bytes = await readHeader(file);
  const type = file.type.trim().toLowerCase();
  const matches = type === "image/png" ? hasBytes(bytes, pngSignature)
    : type === "image/jpeg" ? hasBytes(bytes, [255, 216, 255])
      : hasBytes(bytes, [82, 73, 70, 70]) && hasBytes(bytes, [87, 69, 66, 80], 8);
  if (!matches) throw new CustomLogoError("type");
}

/** A strict local preview source; URLs and other data URL formats are never accepted. */
export function isCustomLogoDataUrl(value: unknown): value is string {
  if (typeof value !== "string" || !value.startsWith(pngPrefix)) return false;
  const payload = value.slice(pngPrefix.length);
  // Reject oversized values before allocating a decoded copy.
  if (!payload || payload.length > Math.ceil(CUSTOM_LOGO_MAX_OUTPUT_BYTES / 3) * 4 ||
    !/^(?:[A-Za-z0-9+/]{4})*(?:[A-Za-z0-9+/]{2}==|[A-Za-z0-9+/]{3}=)?$/.test(payload)) return false;
  try {
    const bytes = atob(payload);
    return bytes.length <= CUSTOM_LOGO_MAX_OUTPUT_BYTES && btoa(bytes) === payload &&
      pngSignature.every((value, index) => bytes.charCodeAt(index) === value);
  } catch {
    return false;
  }
}

function decodeImage(file: File): Promise<CustomLogoDecodedImage> {
  return new Promise((resolve, reject) => {
    const url = URL.createObjectURL(file);
    const image = new Image();
    const release = () => {
      image.onload = image.onerror = null;
      URL.revokeObjectURL(url);
    };
    image.onload = () => resolve({ width: image.naturalWidth, height: image.naturalHeight, source: image, dispose: release });
    image.onerror = () => {
      release();
      reject(new CustomLogoError("decode"));
    };
    image.src = url;
  });
}

const browserDependencies: CustomLogoDependencies = {
  decode: decodeImage,
  createCanvas: () => document.createElement("canvas"),
};

/** Decode a local raster, contain it in a transparent square, and emit bounded PNG bytes. */
export async function prepareCustomLogo(file: File, dependencies: CustomLogoDependencies = browserDependencies): Promise<string> {
  validateCustomLogoFile(file);
  await validateFileSignature(file);
  let image: CustomLogoDecodedImage;
  try {
    image = await dependencies.decode(file);
  } catch {
    throw new CustomLogoError("decode");
  }
  try {
    if (!Number.isFinite(image.width) || !Number.isFinite(image.height) || image.width <= 0 || image.height <= 0)
      throw new CustomLogoError("decode");
    try {
      const canvas = dependencies.createCanvas();
      canvas.width = canvas.height = CUSTOM_LOGO_SIZE;
      const context = canvas.getContext("2d");
      if (!context) throw new CustomLogoError("output");
      context.clearRect(0, 0, CUSTOM_LOGO_SIZE, CUSTOM_LOGO_SIZE);
      const scale = Math.min(CUSTOM_LOGO_SIZE / image.width, CUSTOM_LOGO_SIZE / image.height);
      const width = image.width * scale;
      const height = image.height * scale;
      context.drawImage(image.source, (CUSTOM_LOGO_SIZE - width) / 2, (CUSTOM_LOGO_SIZE - height) / 2, width, height);
      const output = canvas.toDataURL("image/png");
      if (!isCustomLogoDataUrl(output)) throw new CustomLogoError("output");
      return output;
    } catch {
      throw new CustomLogoError("output");
    }
  } finally {
    try { image.dispose?.(); } catch { /* Releasing the local decode must not replace the result. */ }
  }
}

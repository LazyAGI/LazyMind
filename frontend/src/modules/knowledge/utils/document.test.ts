import { describe, expect, it } from "vitest";
import {
  DETAIL_UNSUPPORTED_FILE_TYPES,
  IMAGE_DOCUMENT_FILE_TYPES,
  isDocumentDetailUnsupported,
  isImageDocument,
} from "./document";

describe("isImageDocument", () => {
  it("returns true for known image extensions regardless of case", () => {
    expect(isImageDocument("photo.PNG")).toBe(true);
    expect(isImageDocument("photo.jpg")).toBe(true);
    expect(isImageDocument("scan.tiff")).toBe(true);
  });

  it("returns false for non-image extensions", () => {
    expect(isImageDocument("report.pdf")).toBe(false);
    expect(isImageDocument("data.csv")).toBe(false);
  });

  it("returns false when fileName is undefined", () => {
    expect(isImageDocument(undefined)).toBe(false);
  });

  it("covers every declared image type", () => {
    IMAGE_DOCUMENT_FILE_TYPES.forEach((ext) => {
      expect(isImageDocument(`file.${ext}`)).toBe(true);
    });
  });
});

describe("isDocumentDetailUnsupported", () => {
  it("returns false for any file when the unsupported list is empty", () => {
    expect(DETAIL_UNSUPPORTED_FILE_TYPES).toEqual([]);
    expect(isDocumentDetailUnsupported("file.exe")).toBe(false);
    expect(isDocumentDetailUnsupported(undefined)).toBe(false);
  });
});

import { describe, expect, it } from "vitest";

import {
  allowedFileTypes,
  allowedImageTypes,
  allowedSpreadsheetTypes,
  allowedTextTypes,
  allowedUploadTypes,
} from ".";

describe("chat attachment file types", () => {
  it("uploads SVG as a file instead of a raster vision input", () => {
    expect(allowedTextTypes).toContain(".svg");
    expect(allowedUploadTypes).toContain(".svg");
    expect(allowedImageTypes).not.toContain(".svg");
  });
  it("exposes CSV and Excel formats in the file picker whitelist", () => {
    expect(allowedSpreadsheetTypes).toEqual([".csv", ".tsv", ".xls", ".xlsx"]);
    expect(allowedFileTypes).toEqual(expect.arrayContaining(allowedSpreadsheetTypes));
    expect(allowedUploadTypes).toEqual(expect.arrayContaining(allowedSpreadsheetTypes));
  });

  it("keeps spreadsheet extensions in one explicit category", () => {
    expect(allowedTextTypes).not.toEqual(
      expect.arrayContaining(allowedSpreadsheetTypes),
    );
    expect(new Set(allowedUploadTypes).size).toBe(allowedUploadTypes.length);
  });
});
